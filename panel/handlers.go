package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readBody(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return errors.New("请求格式错误")
	}
	return nil
}

// ---------- 总览 ----------

func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	ds := dockerStatus()
	resp := map[string]any{
		"version":       version,
		"projectDir":    a.Dir,
		"composeExists": fileExists(a.composeFile()),
		"containerName": a.Container,
		"docker":        ds,
		"configured":    false,
	}
	if env, err := readEnv(a.envFile()); err == nil {
		resp["configured"] = env["SOCKS_SERVER"] != "" && env["SOCKS_PORT"] != ""
	}
	if ds.Running {
		resp["container"] = a.containerStatus()
	}
	a.jobs.mu.Lock()
	if a.jobs.active != nil && a.jobs.active.isRunning() {
		resp["activeJob"] = map[string]string{"id": a.jobs.active.ID, "title": a.jobs.active.Title}
	}
	a.jobs.mu.Unlock()
	writeJSON(w, 200, resp)
}

// ---------- 代理配置 ----------

type ProxyConfig struct {
	Server            string `json:"server"`
	Port              int    `json:"port"`
	Username          string `json:"username"`
	Password          string `json:"password"`
	RepairAuto        bool   `json:"repairAuto"`
	RepairIntervalMin int    `json:"repairIntervalMin"`
	LooklookAutostart bool   `json:"looklookAutostart"`
}

func (a *App) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	env, err := readEnv(a.envFile())
	if err != nil {
		writeErr(w, 500, "读取 .env 失败："+err.Error())
		return
	}
	port, _ := strconv.Atoi(env["SOCKS_PORT"])
	interval, err := strconv.Atoi(env["ENVIRONMENT_REPAIR_INTERVAL"])
	if err != nil || interval <= 0 {
		interval = 3600
	}
	writeJSON(w, 200, ProxyConfig{
		Server: env["SOCKS_SERVER"], Port: port, Username: env["SOCKS_USERNAME"], Password: env["SOCKS_PASSWORD"],
		RepairAuto:        env["ENVIRONMENT_REPAIR_AUTO_APPLY"] != "0",
		RepairIntervalMin: interval / 60,
		LooklookAutostart: env["LOOKLOOK_AUTOSTART"] != "0",
	})
}

func (c *ProxyConfig) validate() error {
	c.Server = strings.TrimSpace(c.Server)
	c.Server = strings.TrimPrefix(strings.TrimPrefix(c.Server, "socks5://"), "socks5h://")
	if h, p, err := net.SplitHostPort(c.Server); err == nil && c.Port == 0 {
		c.Server = h
		c.Port, _ = strconv.Atoi(p)
	}
	if c.Server == "" {
		return errors.New("请填写代理服务器地址")
	}
	if strings.ContainsAny(c.Server, " /") {
		return errors.New("代理服务器地址只填域名或 IP，不要带 http:// 或路径")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return errors.New("端口应在 1 - 65535 之间")
	}
	if c.RepairIntervalMin <= 0 {
		c.RepairIntervalMin = 60
	}
	return nil
}

func (a *App) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	var c ProxyConfig
	if err := readBody(r, &c); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := c.validate(); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	b2s := func(b bool) string {
		if b {
			return "1"
		}
		return "0"
	}
	updates := map[string]string{
		"SOCKS_SERVER":                  c.Server,
		"SOCKS_PORT":                    strconv.Itoa(c.Port),
		"SOCKS_USERNAME":                c.Username,
		"SOCKS_PASSWORD":                c.Password,
		"ENVIRONMENT_REPAIR_AUTO_APPLY": b2s(c.RepairAuto),
		"ENVIRONMENT_REPAIR_INTERVAL":   strconv.Itoa(c.RepairIntervalMin * 60),
		"LOOKLOOK_AUTOSTART":            b2s(c.LooklookAutostart),
	}
	order := []string{"SOCKS_SERVER", "SOCKS_PORT", "SOCKS_USERNAME", "SOCKS_PASSWORD",
		"ENVIRONMENT_REPAIR_AUTO_APPLY", "ENVIRONMENT_REPAIR_INTERVAL", "LOOKLOOK_AUTOSTART"}
	if err := writeEnv(a.envFile(), updates, order); err != nil {
		writeErr(w, 500, "保存 .env 失败："+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"saved": true, "server": c.Server, "port": c.Port})
}

func (a *App) handleProxyTest(w http.ResponseWriter, r *http.Request) {
	var c ProxyConfig
	if err := readBody(r, &c); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := c.validate(); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	host := hostSideAddress(c.Server)
	resp := map[string]any{"testedHost": host}
	if host != c.Server {
		resp["note"] = fmt.Sprintf("「%s」在容器里指向宿主机，所以面板用 127.0.0.1 测试。注意：宿主机上的代理软件需要开启「允许局域网连接」，容器才能连上。", c.Server)
	}
	d, err := testSocks5(host, c.Port, c.Username, c.Password)
	if err != nil {
		resp["ok"], resp["error"] = false, err.Error()
	} else {
		resp["ok"], resp["ms"] = true, d.Milliseconds()
	}
	writeJSON(w, 200, resp)
}

// handleNetworkCheck 在容器里实际访问几个关键站点，验证代理 + 分流是否正常。
func (a *App) handleContainer(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	if !dockerStatus().Running {
		writeErr(w, 409, "Docker 没有运行，请先打开 Docker Desktop（或启动 docker 服务）")
		return
	}
	var (
		title string
		steps []step
		long  = 5 * time.Minute
	)
	switch action {
	case "start", "apply":
		if err := a.prepareProject(); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		title = "启动容器"
		if action == "apply" {
			title = "应用配置并重建容器"
		}
		args := []string{"up", "-d", "--remove-orphans"}
		if a.composeUsesBuild() {
			args = append(args, "--build")
			long = 30 * time.Minute
		} else {
			long = 20 * time.Minute // 第一次需要下载镜像
		}
		steps = []step{a.composeStep("docker compose "+strings.Join(args, " "), args...)}
	case "stop":
		title = "停止容器"
		steps = []step{dockerStep("docker stop "+a.Container, "stop", a.Container)}
	case "restart":
		title = "重启容器"
		steps = []step{dockerStep("docker restart "+a.Container, "restart", a.Container)}
	case "update":
		if err := a.prepareProject(); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		title = "更新到最新版本"
		long = 30 * time.Minute
		if a.composeUsesBuild() {
			steps = []step{
				a.composeStep("重新构建镜像（会拉取最新基础镜像）", "build", "--pull"),
				a.composeStep("用新镜像重建容器", "up", "-d", "--remove-orphans"),
			}
		} else {
			steps = []step{
				a.composeStep("下载最新镜像", "pull"),
				a.composeStep("用新镜像重建容器", "up", "-d", "--remove-orphans"),
			}
		}
		steps = append(steps, dockerStep("清理旧镜像", "image", "prune", "-f"))
	default:
		writeErr(w, 404, "未知操作")
		return
	}
	if action != "stop" {
		steps = append(steps, a.lanOnStep())
	}
	job, err := a.jobs.Run(title, true, long, steps...)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"job": job.ID})
}

// prepareProject 确保 compose 文件、.env 和挂载目录都就绪。
func (a *App) prepareProject() error {
	env, err := readEnv(a.envFile())
	if err != nil {
		return err
	}
	if env["SOCKS_SERVER"] == "" || env["SOCKS_PORT"] == "" {
		return errors.New("还没有填写代理设置，请先在「代理设置」里填写并保存")
	}
	if err := a.ensureComposeFile(); err != nil {
		return fmt.Errorf("生成 docker-compose.yml 失败：%w", err)
	}
	for _, d := range []string{"workspace", "mihomo-rules", "looklook-data"} {
		_ = os.MkdirAll(filepath.Join(a.Dir, d), 0o755)
	}
	return nil
}

func (a *App) handleLogs(w http.ResponseWriter, r *http.Request) {
	tail := r.URL.Query().Get("tail")
	if n, err := strconv.Atoi(tail); err != nil || n <= 0 || n > 5000 {
		tail = "300"
	}
	out, err := dockerOutput(15*time.Second, "logs", "--tail", tail, "--timestamps", a.Container)
	if err != nil && out == "" {
		writeErr(w, 409, "读取日志失败（容器可能还没创建）")
		return
	}
	writeJSON(w, 200, map[string]string{"logs": out})
}

// ---------- 环境修复 ----------

const ruleHeaderDirect = "# 直连规则：每行一条，不写策略。例如：\n# DOMAIN-SUFFIX,example.com\n# IP-CIDR,1.2.3.4/32,no-resolve\n"
const ruleHeaderProxy = "# 走家宽代理的规则：每行一条，不写策略。例如：\n# DOMAIN-SUFFIX,example.org\n"

func (a *App) rulePath(name string) (string, bool) {
	if name != "direct" && name != "proxy" {
		return "", false
	}
	return filepath.Join(a.Dir, "mihomo-rules", name+".txt"), true
}

func (a *App) handleGetRules(w http.ResponseWriter, r *http.Request) {
	p, ok := a.rulePath(r.PathValue("name"))
	if !ok {
		writeErr(w, 404, "未知规则文件")
		return
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		if r.PathValue("name") == "direct" {
			b = []byte(ruleHeaderDirect)
		} else {
			b = []byte(ruleHeaderProxy)
		}
	} else if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"content": string(b), "path": p})
}

func (a *App) handleSaveRules(w http.ResponseWriter, r *http.Request) {
	p, ok := a.rulePath(r.PathValue("name"))
	if !ok {
		writeErr(w, 404, "未知规则文件")
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := readBody(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	content := strings.ReplaceAll(body.Content, "\r\n", "\n")
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		writeErr(w, 500, "保存失败："+err.Error())
		return
	}
	resp := map[string]any{"saved": true}
	if a.containerStatus().Running {
		out, err := a.execOutput(20*time.Second, "mihomo-reload")
		resp["reloaded"] = err == nil
		resp["reloadOutput"] = out
	}
	writeJSON(w, 200, resp)
}

// ---------- Looklook ----------

func (a *App) handleJob(w http.ResponseWriter, r *http.Request) {
	job := a.jobs.Get(r.PathValue("id"))
	if job == nil {
		writeErr(w, 404, "任务不存在")
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	writeJSON(w, 200, job.Snapshot(offset))
}

func (a *App) handleOpenFolder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Which string `json:"which"`
	}
	_ = readBody(r, &body)
	target := a.Dir
	if body.Which == "workspace" {
		target = filepath.Join(a.Dir, "workspace")
		_ = os.MkdirAll(target, 0o755)
	}
	if err := openWith(target); err != nil {
		writeErr(w, 500, "打开文件夹失败："+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"opened": target})
}
