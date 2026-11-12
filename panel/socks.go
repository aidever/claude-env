package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// testSocks5 从宿主机直接连上游 SOCKS5，完成握手 + 认证 + CONNECT，
// 再发一个 HTTP 请求确认代理真的能出网。返回耗时。
func testSocks5(server string, port int, user, pass string) (time.Duration, error) {
	start := time.Now()
	addr := net.JoinHostPort(server, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, 6*time.Second)
	if err != nil {
		return 0, fmt.Errorf("连不上 %s：%s（检查地址、端口，以及代理软件是否已开启）", addr, shortNetErr(err))
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(12 * time.Second))

	methods := []byte{0x00}
	if user != "" {
		methods = []byte{0x02}
	}
	if _, err := conn.Write(append([]byte{0x05, byte(len(methods))}, methods...)); err != nil {
		return 0, err
	}
	resp := make([]byte, 2)
	if _, err := readFull(conn, resp); err != nil {
		return 0, errors.New("对方没有按 SOCKS5 协议回应，这个端口可能不是 SOCKS5 代理（例如是 HTTP 代理端口）")
	}
	if resp[0] != 0x05 {
		return 0, errors.New("这个端口不是 SOCKS5 代理（可能是 HTTP 代理端口）")
	}
	switch resp[1] {
	case 0x00:
	case 0x02:
		if user == "" {
			return 0, errors.New("代理要求用户名和密码，请填写后再试")
		}
		if len(user) > 255 || len(pass) > 255 {
			return 0, errors.New("用户名或密码太长")
		}
		msg := []byte{0x01, byte(len(user))}
		msg = append(msg, user...)
		msg = append(msg, byte(len(pass)))
		msg = append(msg, pass...)
		if _, err := conn.Write(msg); err != nil {
			return 0, err
		}
		if _, err := readFull(conn, resp); err != nil || resp[1] != 0x00 {
			return 0, errors.New("用户名或密码错误")
		}
	case 0xFF:
		if user == "" {
			return 0, errors.New("代理要求用户名和密码，请填写后再试")
		}
		return 0, errors.New("代理不接受用户名密码认证，试试清空用户名和密码")
	default:
		return 0, fmt.Errorf("代理要求不支持的认证方式（0x%02x）", resp[1])
	}

	// CONNECT www.gstatic.com:80，然后请求 generate_204
	const host = "www.gstatic.com"
	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))}
	req = append(req, host...)
	req = binary.BigEndian.AppendUint16(req, 80)
	if _, err := conn.Write(req); err != nil {
		return 0, err
	}
	head := make([]byte, 4)
	if _, err := readFull(conn, head); err != nil {
		return 0, errors.New("代理握手成功，但连接外网超时（代理服务器自身可能无法上网）")
	}
	if head[1] != 0x00 {
		return 0, fmt.Errorf("代理握手成功，但它拒绝访问外网：%s", socksReply(head[1]))
	}
	var skip int
	switch head[3] {
	case 0x01:
		skip = 4
	case 0x04:
		skip = 16
	case 0x03:
		l := make([]byte, 1)
		if _, err := readFull(conn, l); err != nil {
			return 0, err
		}
		skip = int(l[0])
	}
	if _, err := readFull(conn, make([]byte, skip+2)); err != nil {
		return 0, err
	}
	if _, err := conn.Write([]byte("GET /generate_204 HTTP/1.1\r\nHost: " + host + "\r\nConnection: close\r\n\r\n")); err != nil {
		return 0, err
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return 0, errors.New("通过代理访问外网没有响应")
	}
	if !strings.Contains(line, " 204") && !strings.Contains(line, " 200") {
		return 0, fmt.Errorf("通过代理访问外网返回异常：%s", strings.TrimSpace(line))
	}
	return time.Since(start), nil
}

func readFull(c net.Conn, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		m, err := c.Read(b[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func socksReply(code byte) string {
	switch code {
	case 0x01:
		return "代理服务器内部错误"
	case 0x02:
		return "代理规则不允许"
	case 0x03:
		return "网络不可达"
	case 0x04:
		return "目标主机不可达"
	case 0x05:
		return "连接被拒绝"
	case 0x06:
		return "超时"
	default:
		return fmt.Sprintf("错误码 0x%02x", code)
	}
}

func shortNetErr(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "refused"):
		return "连接被拒绝"
	case strings.Contains(s, "timeout"), strings.Contains(s, "i/o timeout"):
		return "连接超时"
	case strings.Contains(s, "no such host"):
		return "找不到这个域名"
	}
	return s
}

// hostSideAddress 把容器视角的地址换成宿主机能连的地址：
// host.docker.internal 在宿主机上就是本机。
func hostSideAddress(server string) string {
	switch strings.ToLower(strings.TrimSpace(server)) {
	case "host.docker.internal", "gateway.docker.internal", "172.17.0.1":
		return "127.0.0.1"
	}
	return strings.TrimSpace(server)
}
