package main

import (
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Looklook：只做两件事——通过 docker exec 查看状态，以及开关局域网访问。
// 登录等操作在 Looklook 自己的管理台里完成（面板给出管理台地址）。

type LooklookStatus struct {
	Available bool     `json:"available"`
	Running   bool     `json:"running"`
	SignedIn  bool     `json:"signedIn"`
	Account   string   `json:"account,omitempty"`
	Address   string   `json:"address,omitempty"`
	Remote    string   `json:"remote,omitempty"`
	Version   string   `json:"version,omitempty"`
	LanOn     bool     `json:"lanOn"`
	LocalURL  string   `json:"localUrl,omitempty"` // 宿主机上打开管理台的地址
	LanURLs   []string `json:"lanUrls,omitempty"`  // 局域网其他设备打开管理台的地址
	Raw       string   `json:"raw,omitempty"`
}

var reURL = regexp.MustCompile(`https?://[^\s）)]+`)

func parseLooklookStatus(raw string) LooklookStatus {
	st := LooklookStatus{Available: true, Raw: raw}
	for _, line := range strings.Split(raw, "\n") {
		label, value, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch {
		case strings.Contains(label, "Looklook client"):
			st.Version, _, _ = strings.Cut(value, "（")
			st.Running = strings.Contains(value, "运行中") || strings.Contains(value, "/ running")
		case strings.HasPrefix(label, "已登录"):
			st.SignedIn = true
			st.Account, _, _ = strings.Cut(value, "（")
		case strings.Contains(label, "Your address"):
			st.Address = reURL.FindString(value)
		case strings.Contains(label, "Remote access"):
			st.Remote = value
		case strings.Contains(label, "LAN access"):
			st.LanOn = strings.HasPrefix(value, "开启") || strings.Contains(value, "/ on")
		}
	}
	return st
}

// looklookPort 是容器发布到宿主机的管理台端口（compose 里 LOOKLOOK_PORT，默认 1234）。
func (a *App) looklookPort() int {
	env, _ := readEnv(a.envFile())
	if p, err := strconv.Atoi(env["LOOKLOOK_PORT"]); err == nil && p > 0 && p < 65536 {
		return p
	}
	return 1234
}

func (a *App) handleLooklookStatus(w http.ResponseWriter, r *http.Request) {
	if !a.containerStatus().Running {
		writeJSON(w, 200, LooklookStatus{})
		return
	}
	out, err := a.execOutput(15*time.Second, "looklook", "status")
	if out == "" && err != nil {
		writeJSON(w, 200, LooklookStatus{Available: false, Raw: err.Error()})
		return
	}
	st := parseLooklookStatus(out)
	if !st.Running {
		// status 输出格式变化时，用进程兜底判断
		if _, err := a.execOutput(5*time.Second, "pgrep", "-f", "looklook run"); err == nil {
			st.Running = true
		}
	}
	port := strconv.Itoa(a.looklookPort())
	st.LocalURL = "http://127.0.0.1:" + port + "/"
	for _, ip := range hostLANIPs() {
		st.LanURLs = append(st.LanURLs, "http://"+net.JoinHostPort(ip, port)+"/")
	}
	writeJSON(w, 200, st)
}

// lanOnStep 开启 Looklook 局域网访问。客户端刚启动时可能还没就绪，所以重试一会儿。
func (a *App) lanOnStep() step {
	return step{
		label:    "开启 Looklook 局域网访问",
		name:     "docker",
		args:     []string{"exec", a.Container, "sh", "-c", `for i in $(seq 1 30); do looklook access lan on 2>&1 && exit 0; sleep 1; done; exit 1`},
		optional: true,
	}
}

func (a *App) handleLooklook(w http.ResponseWriter, r *http.Request) {
	if !a.containerStatus().Running {
		writeErr(w, 409, "容器没有运行，请先启动容器")
		return
	}
	var (
		title string
		s     step
	)
	switch r.PathValue("action") {
	case "lan-on":
		title, s = "开启局域网访问", a.lanOnStep()
		s.optional = false
	case "lan-off":
		title, s = "关闭局域网访问", dockerStep("looklook access lan off", "exec", a.Container, "looklook", "access", "lan", "off")
	default:
		writeErr(w, 404, "未知操作")
		return
	}
	job, err := a.jobs.Run(title, false, time.Minute, s)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"job": job.ID})
}

// hostLANIPs 返回宿主机的局域网 IPv4（排除 Docker / 虚拟网卡），优先常见的家用网段。
func hostLANIPs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	skip := []string{"docker", "br-", "veth", "vEthernet", "vmnet", "virbr", "utun", "tun", "tap", "wg", "zt", "tailscale", "VirtualBox", "VMware", "Loopback", "lo"}
	var ips []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		bad := false
		for _, p := range skip {
			if strings.HasPrefix(strings.ToLower(ifc.Name), strings.ToLower(p)) {
				bad = true
				break
			}
		}
		if bad {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, ad := range addrs {
			ipn, ok := ad.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP.To4()
			if ip == nil || !ip.IsPrivate() || (ip[0] == 172 && ip[1] >= 17 && ip[1] <= 31) {
				continue // 172.17-31 基本都是 Docker / WSL 的虚拟网段
			}
			ips = append(ips, ip.String())
		}
	}
	rank := func(s string) int {
		switch {
		case strings.HasPrefix(s, "192.168."):
			return 0
		case strings.HasPrefix(s, "10."):
			return 1
		}
		return 2
	}
	sort.SliceStable(ips, func(i, j int) bool { return rank(ips[i]) < rank(ips[j]) })
	return ips
}
