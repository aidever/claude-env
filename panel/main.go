// claude-env 运维面板：在宿主机上运行的轻量 Web 面板，用鼠标管理 claude-env 开发容器。
//
// 单个可执行文件，不依赖 Python/Node；只需要宿主机装好 Docker（Docker Desktop 或 docker-ce）。
// 默认只监听 127.0.0.1，启动后自动打开浏览器。
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

//go:embed web
var webFS embed.FS

var version = "dev"

type App struct {
	Dir       string // 项目目录：放 docker-compose.yml、.env、workspace/、mihomo-rules/
	Container string // 容器名
	Image     string // 生成 compose 文件时使用的镜像
	jobs      *JobManager
}

func main() {
	listen := flag.String("listen", "127.0.0.1:8686", "面板监听地址（默认只允许本机访问）")
	dir := flag.String("dir", "", "项目目录（默认：当前目录有 docker-compose.yml 就用当前目录，否则用程序所在目录）")
	container := flag.String("container", "dev-proxy", "容器名")
	image := flag.String("image", "ghcr.io/aidever/claude-env:latest", "首次生成 docker-compose.yml 时使用的镜像")
	noBrowser := flag.Bool("no-browser", false, "启动后不自动打开浏览器")
	showVersion := flag.Bool("version", false, "显示版本")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	projectDir, err := resolveProjectDir(*dir)
	if err != nil {
		log.Fatalf("无法确定项目目录：%v", err)
	}
	app := &App{Dir: projectDir, Container: *container, Image: *image, jobs: NewJobManager()}

	// 面板已经在运行（用户又双击了一次）：直接打开已有的面板，不再启动第二个。
	if existing := runningPanelURL(*listen); existing != "" {
		log.Printf("面板已经在运行：%s", existing)
		if !*noBrowser {
			_ = openBrowser(existing)
		}
		return
	}
	ln, err := listenWithFallback(*listen)
	if err != nil {
		log.Fatalf("无法启动面板（%s）：%v\n可以换一个端口，例如：--listen 127.0.0.1:8687", *listen, err)
	}
	url := "http://" + displayAddr(ln.Addr().String())
	log.Printf("claude-env 面板 %s 已启动：%s", version, url)
	log.Printf("项目目录：%s", projectDir)
	log.Printf("关闭此窗口即可退出面板（不会停止容器）")

	if !*noBrowser {
		go func() {
			time.Sleep(300 * time.Millisecond)
			_ = openBrowser(url)
		}()
	}

	srv := &http.Server{Handler: app.routes(), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.Serve(ln))
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(webFS, "web")
	mux.Handle("GET /", http.FileServer(http.FS(static)))

	mux.HandleFunc("GET /api/status", a.handleStatus)
	mux.HandleFunc("GET /api/config", a.handleGetConfig)
	mux.HandleFunc("POST /api/config", a.handleSaveConfig)
	mux.HandleFunc("POST /api/proxy/test", a.handleProxyTest)
	mux.HandleFunc("POST /api/network/check", a.handleNetworkCheck)
	mux.HandleFunc("POST /api/container/{action}", a.handleContainer)
	mux.HandleFunc("GET /api/logs", a.handleLogs)
	mux.HandleFunc("POST /api/ipquality", a.handleIPQuality)
	mux.HandleFunc("GET /api/rules/{name}", a.handleGetRules)
	mux.HandleFunc("POST /api/rules/{name}", a.handleSaveRules)
	mux.HandleFunc("GET /api/looklook", a.handleLooklookStatus)
	mux.HandleFunc("POST /api/looklook/{action}", a.handleLooklook)
	mux.HandleFunc("GET /api/jobs/{id}", a.handleJob)
	mux.HandleFunc("POST /api/open-folder", a.handleOpenFolder)
	return guard(mux)
}

// guard 防止其他网站借用户浏览器调用本机面板（CSRF / DNS rebinding）。
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if !isLocalHostname(host) {
			http.Error(w, "只允许通过 localhost / 127.0.0.1 访问面板", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Header.Get("X-Panel") != "1" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLocalHostname(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func displayAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsUnspecified() || ip.IsLoopback()) {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

func resolveProjectDir(flagDir string) (string, error) {
	if flagDir != "" {
		abs, err := filepath.Abs(flagDir)
		if err != nil {
			return "", err
		}
		return abs, os.MkdirAll(abs, 0o755)
	}
	if cwd, err := os.Getwd(); err == nil && fileExists(filepath.Join(cwd, "docker-compose.yml")) {
		return cwd, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	dir := filepath.Dir(exe)
	// `go run` 会把程序放进临时目录，这时退回到当前目录。
	if isTempDir(dir) {
		return os.Getwd()
	}
	return dir, nil
}

func isTempDir(dir string) bool {
	rel, err := filepath.Rel(os.TempDir(), dir)
	return err == nil && !strings.HasPrefix(rel, "..")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func openBrowser(url string) error {
	return openWith(url)
}

func openWith(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	case "darwin":
		cmd = exec.Command("open", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}

// runningPanelURL 检查 addr 上是否已经有一个本面板在运行。
func runningPanelURL(addr string) string {
	url := "http://" + displayAddr(addr)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url + "/api/status")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var st struct {
		ProjectDir string `json:"projectDir"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&st) != nil || st.ProjectDir == "" {
		return ""
	}
	return url
}

// listenWithFallback 端口被其他程序占用时，自动往后找一个空闲端口。
func listenWithFallback(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err == nil {
		return ln, nil
	}
	host, portStr, splitErr := net.SplitHostPort(addr)
	port, convErr := strconv.Atoi(portStr)
	if splitErr != nil || convErr != nil {
		return nil, err
	}
	for p := port + 1; p <= port+20 && p < 65536; p++ {
		if l, e := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(p))); e == nil {
			log.Printf("端口 %d 被占用，改用 %d", port, p)
			return l, nil
		}
	}
	return nil, err
}
