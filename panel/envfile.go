package main

import (
	"os"
	"strings"
)

// 读写 docker compose 使用的 .env 文件：保留原有注释、顺序和其他变量，只改动面板管理的键。

func parseEnvValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 {
		switch raw[0] {
		case '\'':
			if end := strings.IndexByte(raw[1:], '\''); end >= 0 {
				return raw[1 : 1+end]
			}
		case '"':
			var b strings.Builder
			for i := 1; i < len(raw); i++ {
				c := raw[i]
				if c == '\\' && i+1 < len(raw) {
					i++
					switch raw[i] {
					case 'n':
						b.WriteByte('\n')
					default:
						b.WriteByte(raw[i])
					}
					continue
				}
				if c == '$' && i+1 < len(raw) && raw[i+1] == '$' {
					b.WriteByte('$')
					i++
					continue
				}
				if c == '"' {
					break
				}
				b.WriteByte(c)
			}
			return b.String()
		}
	}
	// 未加引号：" #" 之后是注释
	if i := strings.Index(raw, " #"); i >= 0 {
		raw = raw[:i]
	}
	return strings.TrimSpace(raw)
}

func formatEnvValue(v string) string {
	if v == "" {
		return ""
	}
	if !strings.ContainsAny(v, " \t#'\"$\\=`") {
		return v
	}
	if !strings.ContainsAny(v, "'\n") {
		return "'" + v + "'" // 单引号内容按原样读取，不做变量替换
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", "$$", "\n", `\n`)
	return `"` + r.Replace(v) + `"`
}

func readEnv(path string) (map[string]string, error) {
	vals := map[string]string{}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return vals, nil
		}
		return nil, err
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		t = strings.TrimPrefix(t, "export ")
		k, v, ok := strings.Cut(t, "=")
		if !ok {
			continue
		}
		vals[strings.TrimSpace(k)] = parseEnvValue(v)
	}
	return vals, nil
}

func writeEnv(path string, updates map[string]string, order []string) error {
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var lines []string
	if len(b) > 0 {
		lines = strings.Split(strings.TrimRight(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n"), "\n")
	}
	done := map[string]bool{}
	for i, line := range lines {
		t := strings.TrimPrefix(strings.TrimSpace(line), "export ")
		if strings.HasPrefix(t, "#") {
			continue
		}
		k, _, ok := strings.Cut(t, "=")
		k = strings.TrimSpace(k)
		if v, managed := updates[k]; ok && managed && !done[k] {
			lines[i] = k + "=" + formatEnvValue(v)
			done[k] = true
		}
	}
	for _, k := range order {
		if v, ok := updates[k]; ok && !done[k] {
			lines = append(lines, k+"="+formatEnvValue(v))
			done[k] = true
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
