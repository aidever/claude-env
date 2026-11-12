package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// composeTemplate 用于项目目录里还没有 docker-compose.yml 的情况（例如用户只下载了面板程序）。
// 直接使用 GHCR 上构建好的镜像，不需要在本机编译。
const composeTemplate = `# 由 claude-env 面板生成。可以手动修改，面板会沿用你的修改。
services:
  dev-proxy:
    image: ${CLAUDE_ENV_IMAGE:-%s}
    container_name: %s
    hostname: looklook
    cap_add: [NET_ADMIN, NET_RAW]
    devices:
      - /dev/net/tun:/dev/net/tun
    extra_hosts:
      - host.docker.internal:host-gateway
    environment:
      SOCKS_SERVER: ${SOCKS_SERVER}
      SOCKS_PORT: ${SOCKS_PORT}
      SOCKS_USERNAME: ${SOCKS_USERNAME:-}
      SOCKS_PASSWORD: ${SOCKS_PASSWORD:-}
      ENVIRONMENT_REPAIR_AUTO_APPLY: ${ENVIRONMENT_REPAIR_AUTO_APPLY:-1}
      ENVIRONMENT_REPAIR_INTERVAL: ${ENVIRONMENT_REPAIR_INTERVAL:-3600}
      LOOKLOOK_AUTOSTART: ${LOOKLOOK_AUTOSTART:-1}
      # 管理台固定监听 1234，发布到宿主机，局域网设备才能访问
      LOOKLOOK_LISTEN: 0.0.0.0:1234
    ports:
      - ${LOOKLOOK_PORT:-1234}:1234
    volumes:
      - ./workspace:/workspace
      - ./mihomo-rules:/etc/mihomo/custom
      - ./looklook-data:/root/.local/share/looklook
    restart: unless-stopped
`

func (a *App) composeFile() string { return filepath.Join(a.Dir, "docker-compose.yml") }
func (a *App) envFile() string     { return filepath.Join(a.Dir, ".env") }

func (a *App) ensureComposeFile() error {
	if fileExists(a.composeFile()) {
		return nil
	}
	content := fmt.Sprintf(composeTemplate, a.Image, a.Container)
	return os.WriteFile(a.composeFile(), []byte(content), 0o644)
}

// composeUsesBuild 判断 compose 文件是本地构建（仓库自带的）还是直接拉镜像（面板生成的）。
func (a *App) composeUsesBuild() bool {
	b, err := os.ReadFile(a.composeFile())
	return err == nil && strings.Contains(string(b), "build:")
}

var (
	composeOnce sync.Once
	composeCmd  []string
)

// composeBase 返回 compose 命令前缀：优先 `docker compose`（v2），否则 `docker-compose`。
func composeBase() []string {
	composeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := runOutput(ctx, "docker", "compose", "version"); err == nil {
			composeCmd = []string{"docker", "compose"}
			return
		}
		if _, err := exec.LookPath("docker-compose"); err == nil {
			composeCmd = []string{"docker-compose"}
			return
		}
		composeCmd = []string{"docker", "compose"}
	})
	return composeCmd
}

func (a *App) composeStep(label string, args ...string) step {
	base := composeBase()
	full := append([]string{}, base[1:]...)
	full = append(full, "--project-directory", a.Dir, "-f", a.composeFile())
	full = append(full, args...)
	return step{label: label, name: base[0], args: full}
}

func dockerStep(label string, args ...string) step {
	return step{label: label, name: "docker", args: args}
}

// runOutput 执行命令并返回合并后的输出。
func runOutput(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		if s != "" {
			return s, fmt.Errorf("%s", s)
		}
		return s, err
	}
	return s, nil
}

func dockerOutput(timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := runOutput(ctx, "docker", args...)
	if ctx.Err() == context.DeadlineExceeded {
		return out, errors.New("docker 响应超时")
	}
	return out, err
}

type DockerStatus struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
}

func dockerStatus() DockerStatus {
	if _, err := exec.LookPath("docker"); err != nil {
		return DockerStatus{Error: "没有找到 docker 命令"}
	}
	st := DockerStatus{Installed: true}
	v, err := dockerOutput(8*time.Second, "version", "--format", "{{.Server.Version}}")
	if err != nil {
		st.Error = firstLine(err.Error())
		return st
	}
	st.Running, st.Version = true, v
	return st
}

type ContainerStatus struct {
	Exists     bool   `json:"exists"`
	State      string `json:"state,omitempty"` // running / exited / restarting / created / paused
	Running    bool   `json:"running"`
	StartedAt  string `json:"startedAt,omitempty"`
	Image      string `json:"image,omitempty"`
	Restarting bool   `json:"restarting"`
	ExitCode   int    `json:"exitCode"`
}

func (a *App) containerStatus() ContainerStatus {
	out, err := dockerOutput(8*time.Second, "inspect", "--format", "{{json .State}}|{{.Config.Image}}", a.Container)
	if err != nil {
		return ContainerStatus{}
	}
	stateJSON, image, _ := strings.Cut(out, "|")
	var s struct {
		Status     string
		Running    bool
		Restarting bool
		ExitCode   int
		StartedAt  string
	}
	_ = json.Unmarshal([]byte(stateJSON), &s)
	return ContainerStatus{
		Exists: true, State: s.Status, Running: s.Running && !s.Restarting, Restarting: s.Restarting,
		ExitCode: s.ExitCode, StartedAt: s.StartedAt, Image: image,
	}
}

// execOutput 在容器里执行命令并返回输出。
func (a *App) execOutput(timeout time.Duration, cmd ...string) (string, error) {
	args := append([]string{"exec", a.Container}, cmd...)
	return dockerOutput(timeout, args...)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
