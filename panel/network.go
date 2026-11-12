package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// ipEndpoints 都直接返回纯文本 IP。同时请求，谁先成功用谁，避免单个站点故障导致误判“网络不通”。
var ipEndpoints = []struct{ Name, URL string }{
	{"ip.me", "https://ip.me"},
	{"ifconfig.me", "https://ifconfig.me/ip"},
	{"ipify", "https://api.ipify.org"},
	{"icanhazip", "https://icanhazip.com"},
}

type ipResult struct {
	IP     string `json:"ip"`
	Source string `json:"source"`
	MS     int64  `json:"ms"`
}

// detectIP 在容器里并发 curl 多个 IP 查询站点，返回第一个成功的结果。
// 容器里是全局代理（TUN），所以拿到的就是代理出口 IP；能拿到即说明网络已通。
func (a *App) detectIP(ctx context.Context) (ipResult, []string) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	type res struct {
		r   ipResult
		err string
	}
	ch := make(chan res, len(ipEndpoints))
	start := time.Now()
	for _, ep := range ipEndpoints {
		go func(name, url string) {
			cmd := exec.CommandContext(ctx, "docker", "exec", a.Container, "curl", "-sS", "-m", "12", "-A", "curl/8", url)
			hideWindow(cmd)
			out, err := cmd.CombinedOutput()
			text := strings.TrimSpace(string(out))
			if err == nil {
				if ip := net.ParseIP(firstLine(text)); ip != nil {
					ch <- res{r: ipResult{IP: ip.String(), Source: name, MS: time.Since(start).Milliseconds()}}
					return
				}
			}
			if text == "" && err != nil {
				text = err.Error()
			}
			if len(text) > 120 {
				text = text[:120] + "…"
			}
			ch <- res{err: fmt.Sprintf("%s：%s", name, firstLine(text))}
		}(ep.Name, ep.URL)
	}
	var errs []string
	for range ipEndpoints {
		r := <-ch
		if r.err == "" {
			return r.r, nil // cancel() 会结束其余请求
		}
		errs = append(errs, r.err)
	}
	return ipResult{}, errs
}

func (a *App) handleNetworkCheck(w http.ResponseWriter, r *http.Request) {
	if !a.containerStatus().Running {
		writeErr(w, 409, "容器没有运行，请先启动容器")
		return
	}
	res, errs := a.detectIP(r.Context())
	if res.IP == "" {
		writeJSON(w, 200, map[string]any{"ok": false, "errors": errs})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "ip": res.IP, "source": res.Source, "ms": res.MS})
}

// handleIPQuality 在容器里运行 xykt/IPQuality 检测脚本。容器是全局代理，检测的就是代理出口 IP。
// -y 自动安装依赖，避免脚本等待输入。
func (a *App) handleIPQuality(w http.ResponseWriter, r *http.Request) {
	if !a.containerStatus().Running {
		writeErr(w, 409, "容器没有运行，请先启动容器")
		return
	}
	job, err := a.jobs.Run("IP 质量检测", false, 15*time.Minute, step{
		label: "运行 IPQuality 检测（首次会自动安装依赖，约 1-3 分钟）",
		name:  "docker",
		args:  []string{"exec", "-e", "TERM=xterm-256color", a.Container, "bash", "-c", "bash <(curl -Ls https://IP.Check.Place) -y"},
	})
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"job": job.ID})
}
