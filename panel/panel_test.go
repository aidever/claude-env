package main

import (
	"bufio"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	orig := "# 注释保留\nSOCKS_SERVER=old\nOTHER=keep me\nSOCKS_PORT=1\n"
	if err := os.WriteFile(p, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	pw := `p@ss w$rd'"#\x`
	err := writeEnv(p, map[string]string{"SOCKS_SERVER": "1.2.3.4", "SOCKS_PORT": "1080", "SOCKS_PASSWORD": pw},
		[]string{"SOCKS_SERVER", "SOCKS_PORT", "SOCKS_PASSWORD"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	if !strings.HasPrefix(s, "# 注释保留\nSOCKS_SERVER=1.2.3.4\nOTHER=keep me\nSOCKS_PORT=1080\n") {
		t.Fatalf("unexpected file:\n%s", s)
	}
	env, err := readEnv(p)
	if err != nil {
		t.Fatal(err)
	}
	if env["SOCKS_PASSWORD"] != pw || env["OTHER"] != "keep me" {
		t.Fatalf("round trip failed: %#v", env)
	}
	for _, v := range []string{"simple", "with space", "has$dollar", "it's", `a"b`} {
		if got := parseEnvValue(formatEnvValue(v)); got != v {
			t.Errorf("format/parse %q -> %q", v, got)
		}
	}
}

func TestParseLooklookStatus(t *testing.T) {
	raw := `看看客户端 / Looklook client: 1.74.0（运行中 / running）
已登录 / Signed in: myth（myth）
专属地址 / Your address: https://myth.looklook.dev/
可以使用到 / Membership until: 2027-10-01T13:00:09.235Z
远程访问 / Remote access: 已连接 / connected
局域网访问 / LAN access: 开启 / on（http://192.168.32.2:1244/）`
	st := parseLooklookStatus(raw)
	if !st.Running || !st.SignedIn || st.Account != "myth" || st.Address != "https://myth.looklook.dev/" ||
		st.Version != "1.74.0" || st.Remote == "" || !st.LanOn {
		t.Fatalf("%+v", st)
	}
	st = parseLooklookStatus("看看客户端 / Looklook client: 1.74.0（未运行 / not running）\n未登录 / Not signed in")
	if st.Running || st.SignedIn || st.LanOn {
		t.Fatalf("%+v", st)
	}
}

// fakeSocks 实现一个最小 SOCKS5 服务器：可选用户名密码，CONNECT 后对 HTTP 请求回 204。
func fakeSocks(t *testing.T, user, pass string) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				h := make([]byte, 2)
				io.ReadFull(c, h)
				io.ReadFull(c, make([]byte, h[1]))
				if user == "" {
					c.Write([]byte{5, 0})
				} else {
					c.Write([]byte{5, 2})
					v := make([]byte, 2)
					io.ReadFull(c, v)
					u := make([]byte, v[1])
					io.ReadFull(c, u)
					l := make([]byte, 1)
					io.ReadFull(c, l)
					p := make([]byte, l[0])
					io.ReadFull(c, p)
					if string(u) != user || string(p) != pass {
						c.Write([]byte{1, 1})
						return
					}
					c.Write([]byte{1, 0})
				}
				r := make([]byte, 5)
				io.ReadFull(c, r)
				io.ReadFull(c, make([]byte, int(r[4])+2))
				c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
				line, _ := bufio.NewReader(c).ReadString('\n')
				if strings.HasPrefix(line, "GET /generate_204") {
					c.Write([]byte("HTTP/1.1 204 No Content\r\n\r\n"))
				}
			}(c)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestSocks5(t *testing.T) {
	port := fakeSocks(t, "", "")
	if _, err := testSocks5("127.0.0.1", port, "", ""); err != nil {
		t.Fatalf("no-auth: %v", err)
	}
	port = fakeSocks(t, "u", "p")
	if _, err := testSocks5("127.0.0.1", port, "", ""); err == nil || !strings.Contains(err.Error(), "用户名和密码") {
		t.Fatalf("expected auth-required error, got %v", err)
	}
	if _, err := testSocks5("127.0.0.1", port, "u", "wrong"); err == nil || !strings.Contains(err.Error(), "错误") {
		t.Fatalf("expected wrong-password error, got %v", err)
	}
	if _, err := testSocks5("127.0.0.1", port, "u", "p"); err != nil {
		t.Fatalf("auth: %v", err)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if _, err := testSocks5("127.0.0.1", closed, "", ""); err == nil || !strings.Contains(err.Error(), "连不上") {
		t.Fatalf("expected connect error, got %v", err)
	}
}

func TestConfigValidate(t *testing.T) {
	c := ProxyConfig{Server: " socks5://1.2.3.4:1080 "}
	if err := c.validate(); err != nil || c.Server != "1.2.3.4" || c.Port != 1080 {
		t.Fatalf("%v %+v", err, c)
	}
	c = ProxyConfig{Server: "http://x/y", Port: 1}
	if err := c.validate(); err == nil {
		t.Fatal("expected error")
	}
}
