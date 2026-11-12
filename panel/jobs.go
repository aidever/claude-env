package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Job 是一个在后台运行的耗时命令（启动容器、拉镜像、环境修复、登录 Looklook…），
// 前端通过轮询 /api/jobs/{id}?offset=N 增量读取输出。
type Job struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Running  bool   `json:"running"`
	ExitCode int    `json:"exitCode"`
	Error    string `json:"error,omitempty"`

	mu     sync.Mutex
	out    bytes.Buffer
	cancel context.CancelFunc
}

func (j *Job) Write(p []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	// 防止输出无限增长（例如 looklook 一直在打印日志）。
	if j.out.Len() > 2<<20 {
		return len(p), nil
	}
	return j.out.Write(p)
}

func (j *Job) Printf(format string, args ...any) {
	fmt.Fprintf(j, format, args...)
}

type JobSnapshot struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Running  bool   `json:"running"`
	ExitCode int    `json:"exitCode"`
	Error    string `json:"error,omitempty"`
	Output   string `json:"output"`
	Offset   int    `json:"offset"`
}

func (j *Job) Snapshot(offset int) JobSnapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	b := j.out.Bytes()
	if offset < 0 || offset > len(b) {
		offset = 0
	}
	return JobSnapshot{
		ID: j.ID, Title: j.Title, Running: j.Running, ExitCode: j.ExitCode, Error: j.Error,
		Output: string(b[offset:]), Offset: len(b),
	}
}

type JobManager struct {
	mu     sync.Mutex
	seq    int
	jobs   map[string]*Job
	active *Job // 同一时间只允许一个“改动容器”的任务，避免用户连点
}

func NewJobManager() *JobManager {
	return &JobManager{jobs: map[string]*Job{}}
}

type step struct {
	label    string
	name     string
	args     []string
	optional bool // 失败只提示，不算整个任务失败
}

// Run 依次执行 steps，任一步失败即停止。exclusive 为 true 时与其他 exclusive 任务互斥。
func (m *JobManager) Run(title string, exclusive bool, timeout time.Duration, steps ...step) (*Job, error) {
	m.mu.Lock()
	if exclusive && m.active != nil && m.active.isRunning() {
		busy := m.active.Title
		m.mu.Unlock()
		return nil, fmt.Errorf("「%s」还在进行中，请等它完成后再操作", busy)
	}
	m.seq++
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	job := &Job{ID: strconv.Itoa(m.seq), Title: title, Running: true, cancel: cancel}
	m.jobs[job.ID] = job
	if exclusive {
		m.active = job
	}
	m.gc()
	m.mu.Unlock()

	go func() {
		defer cancel()
		code, err := 0, error(nil)
		for _, s := range steps {
			if s.label != "" {
				job.Printf("▶ %s\n", s.label)
			}
			c, e := runInto(ctx, job, s.name, s.args...)
			if s.optional && ctx.Err() == nil && (e != nil || c != 0) {
				job.Printf("⚠ 这一步没有成功，不影响其他功能，可以稍后在面板里重试\n")
				continue
			}
			code, err = c, e
			if err != nil || code != 0 {
				break
			}
		}
		if code != 0 || err != nil {
			if hint := explainFailure(job.Snapshot(0).Output); hint != "" {
				job.Printf("\n💡 %s\n", hint)
			}
		}
		job.mu.Lock()
		job.Running = false
		job.ExitCode = code
		if err != nil {
			job.Error = err.Error()
			if ctx.Err() == context.DeadlineExceeded {
				job.Error = "超时，已停止"
			}
		}
		job.mu.Unlock()
	}()
	return job, nil
}

func (m *JobManager) Get(id string) *Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.jobs[id]
}

// gc 只保留最近 30 个任务。调用方持有锁。
func (m *JobManager) gc() {
	if len(m.jobs) <= 30 {
		return
	}
	for id, j := range m.jobs {
		if n, _ := strconv.Atoi(id); n <= m.seq-30 && !j.isRunning() {
			delete(m.jobs, id)
		}
	}
}

func (j *Job) isRunning() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.Running
}

func runInto(ctx context.Context, w io.Writer, name string, args ...string) (int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = w
	cmd.Stderr = w
	hideWindow(cmd)
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

// explainFailure 把常见的 docker 报错翻译成小白能看懂的下一步。
func explainFailure(out string) string {
	lo := strings.ToLower(out)
	switch {
	case strings.Contains(lo, "port is already allocated"), strings.Contains(lo, "address already in use"),
		strings.Contains(lo, "ports are not available"):
		return "端口被占用：Looklook 管理台默认用 1234 端口。请关闭占用它的程序，或在项目目录的 .env 里加一行 LOOKLOOK_PORT=1235 后重新启动。"
	case strings.Contains(lo, "is already in use by container"):
		return "已经有一个同名容器（可能是之前在别的文件夹里创建的）。在 Docker Desktop 里删除名为 dev-proxy 的容器后重试。"
	case strings.Contains(lo, "/dev/net/tun"):
		return "Docker 不支持 TUN 设备。Windows 请确认 Docker Desktop 使用 WSL2 后端；Linux 请执行 sudo modprobe tun。"
	case strings.Contains(lo, "tls handshake timeout"), strings.Contains(lo, "i/o timeout"),
		strings.Contains(lo, "toomanyrequests"), strings.Contains(lo, "error pulling image"), strings.Contains(lo, "failed to resolve reference"):
		return "下载镜像失败，多半是网络问题。请给 Docker Desktop 配置代理或镜像加速（设置 → Resources → Proxies），然后重试。"
	case strings.Contains(lo, "cannot connect to the docker daemon"), strings.Contains(lo, "docker daemon is not running"),
		strings.Contains(lo, "pipe/docker_engine"):
		return "Docker 没有运行，请先打开 Docker Desktop，等它显示 Running 后再试。"
	case strings.Contains(lo, "permission denied") && strings.Contains(lo, "docker.sock"):
		return "当前用户没有 Docker 权限。Linux 请执行 sudo usermod -aG docker $USER 后重新登录。"
	case strings.Contains(lo, "no space left on device"):
		return "磁盘空间不足，请清理磁盘或在 Docker Desktop 里清理不用的镜像。"
	}
	return ""
}
