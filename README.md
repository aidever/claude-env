# Docker dev proxy

## 可视化面板（推荐新手使用）

不会用命令行也没关系，复制一行命令就能装好，之后桌面上会多一个「claude-env 面板」，双击即可打开（浏览器自动打开 `http://127.0.0.1:8686`）：

- **Windows**：打开 PowerShell，粘贴 `irm https://raw.githubusercontent.com/aidever/claude-env/main/install.ps1 | iex`
- **macOS / Linux**：打开终端，粘贴 `curl -fsSL https://raw.githubusercontent.com/aidever/claude-env/main/install.sh | sh`

也可以从 [Releases](https://github.com/aidever/claude-env/releases) 手动下载。重复双击不会开出多个面板，会直接跳到已打开的那个；再执行一次安装命令就是升级。

1. 安装并打开 [Docker Desktop](https://www.docker.com/products/docker-desktop/)（面板会检测 Docker 是否安装、是否在运行）
2. 填写代理 → 「测试连接」→「保存并应用」
3. 点「创建并启动」（首次会下载镜像），再点「网络检测」确认出口 IP
4. Looklook 卡片显示客户端状态、专属地址和局域网地址；面板默认为它开启局域网访问（管理台端口 `1234` 已发布到宿主机，可用 `.env` 里的 `LOOKLOOK_PORT` 修改）

面板还能编辑分流规则（文本，保存后调用容器内 `mihomo-reload` 立即生效）、运行 [IPQuality](https://github.com/xykt/IPQuality) 检测代理出口 IP 质量、查看日志、一键更新。代码在面板所在文件夹的 `workspace/` 里，Looklook 数据保存在 `looklook-data/`，更新或重建容器都不会丢。

面板只监听本机 127.0.0.1，不会暴露到局域网。源码在 [`panel/`](panel/)，`cd panel && go run . --dir ..` 即可在仓库里本地运行（此时使用仓库自带的 `docker-compose.yml` 本地构建镜像）。

This image provides a local HTTP/SOCKS proxy backed by an upstream SOCKS5 server. Configure only the upstream connection; the container listens on port `7890`.

The image includes Node.js 22, Claude Code (`claude`) and Codex (`codex`). After the container starts, verify them with `docker exec -it dev-proxy bash -lc 'node --version; claude --version; codex --version'`.

```bash
cp .env.example .env
# edit SOCKS_SERVER, SOCKS_PORT and optional credentials
docker compose up -d --build
```

The container keeps the HTTP/SOCKS proxy on its internal `7890` port and does not publish that port to the host by default. Add `ports: ["7890:7890"]` only when host access is needed. The image enables Mihomo TUN mode and DNS interception; Docker must be started with `NET_ADMIN`, `NET_RAW`, and `/dev/net/tun` (the included Compose file already sets these). The image is built automatically by GitHub Actions and published to GHCR on every release or push to `main`.

## Looklook client

The image includes the native Looklook client at `/opt/looklook`. It starts automatically with the container (`LOOKLOOK_AUTOSTART=0` to disable) and keeps its login in `./looklook-data`. Use Looklook directly, for example `docker exec -it dev-proxy looklook login` or `docker exec -it dev-proxy looklook run --no-browser`.

The container also runs the environment repair command once per hour by default. It applies the detected timezone without prompting and runs the bundled `fuck-claude` repair script. Set `ENVIRONMENT_REPAIR_AUTO_APPLY=0` to disable it or change `ENVIRONMENT_REPAIR_INTERVAL` (seconds).

## Proxy rules

All Looklook services (`*.looklook.dev`, relay IPs, and all UDP from the `looklook` process) connect directly instead of through the upstream proxy. To add your own rules, edit `/etc/mihomo/custom/direct.txt` (direct) or `/etc/mihomo/custom/proxy.txt` (via upstream), one Mihomo rule per line without a policy, e.g. `DOMAIN-SUFFIX,example.com`. The included Compose file mounts `./mihomo-rules` there so edits survive container recreation. Apply changes without restarting: `docker exec dev-proxy mihomo-reload`.
