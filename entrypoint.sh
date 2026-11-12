#!/usr/bin/env bash
set -euo pipefail
: "${SOCKS_SERVER:?Set SOCKS_SERVER, for example proxy.example.com}"
: "${SOCKS_PORT:?Set SOCKS_PORT, for example 13579}"

# Looklook's updater renames the extracted `fonts` directory. Keep its staging
# directory beside the install prefix so rename(2) cannot cross filesystems.
export TMPDIR="${LOOKLOOK_TMPDIR:-${LOOKLOOK_PREFIX:-/opt/looklook}/.tmp}"
export TMP="$TMPDIR"
export TEMP="$TMPDIR"
mkdir -p "$TMPDIR"

# User-editable rule files. Mount /etc/mihomo/custom from the host to keep them,
# then run `mihomo-reload` to apply edits without restarting the container.
CUSTOM_RULES_DIR=/etc/mihomo/custom
mkdir -p "$CUSTOM_RULES_DIR"
[ -f "$CUSTOM_RULES_DIR/direct.txt" ] || cat > "$CUSTOM_RULES_DIR/direct.txt" <<'RULES'
# 直连规则：每行一条，不写策略。例如：
# DOMAIN-SUFFIX,example.com
# IP-CIDR,1.2.3.4/32,no-resolve
RULES
[ -f "$CUSTOM_RULES_DIR/proxy.txt" ] || cat > "$CUSTOM_RULES_DIR/proxy.txt" <<'RULES'
# 走家宽代理的规则：每行一条，不写策略。例如：
# DOMAIN-SUFFIX,example.org
RULES

cat > /etc/mihomo/config.yaml <<EOF
mixed-port: 7890
allow-lan: false
mode: rule
log-level: info
ipv6: false
external-controller: 127.0.0.1:9090
proxies:
  - name: upstream
    type: socks5
    server: ${SOCKS_SERVER}
    port: ${SOCKS_PORT}
    username: ${SOCKS_USERNAME:-}
    password: ${SOCKS_PASSWORD:-}
    udp: true
proxy-groups:
  - name: GLOBAL
    type: select
    proxies: [upstream]
tun:
  enable: true
  device: mihomo0
  stack: mixed
  auto-route: true
  auto-detect-interface: true
  strict-route: true
  dns-hijack:
    - any:53
    - tcp://any:53
  mtu: 1500
dns:
  enable: true
  listen: 0.0.0.0:53
  ipv6: false
  enhanced-mode: fake-ip
  fake-ip-filter:
    - 4.kleeclub.top
    # Looklook 返回真实 IP，保证按 IP 直连的 UDP/STUN 流量也能命中规则
    - '+.looklook.dev'
    - '*.lan'
    - '*.local'
  fake-ip-range: 198.18.0.1/16
  default-nameserver:
    - 223.5.5.5
    - 119.29.29.29
  proxy-server-nameserver:
    - 223.5.5.5
    - 119.29.29.29
  nameserver-policy:
    '+.looklook.dev': ['223.5.5.5', '119.29.29.29']
  nameserver:
    - 223.5.5.5 # 用国内 DNS 兜底，避免代理未通时所有解析瘫痪
rule-providers:
  custom-direct:
    type: file
    behavior: classical
    format: text
    path: ${CUSTOM_RULES_DIR}/direct.txt
  custom-proxy:
    type: file
    behavior: classical
    format: text
    path: ${CUSTOM_RULES_DIR}/proxy.txt
rules:
  # 用户自定义规则优先，见 /etc/mihomo/custom/*.txt
  - RULE-SET,custom-direct,DIRECT
  - RULE-SET,custom-proxy,GLOBAL
  - DOMAIN,4.kleeclub.top,DIRECT
  # Looklook 服务全部直连：网站/API、中继（TCP 2333、STUN/TURN 3478）
  - DOMAIN,r3.looklook.dev,DIRECT
  - DOMAIN-SUFFIX,looklook.dev,DIRECT
  - IP-CIDR,154.219.107.44/32,DIRECT,no-resolve
  - IP-CIDR,47.108.195.230/32,DIRECT,no-resolve
  # Looklook 进程的 UDP（WebRTC 点对点、STUN/TURN）全部直连；TCP 不按进程放行，避免它访问 Anthropic 时用真实 IP
  - AND,((PROCESS-NAME,looklook),(NETWORK,UDP)),DIRECT
  - DOMAIN-SUFFIX,lan,DIRECT
  - DOMAIN-SUFFIX,local,DIRECT
  - IP-CIDR,10.0.0.0/8,DIRECT,no-resolve
  - IP-CIDR,172.16.0.0/12,DIRECT,no-resolve
  - IP-CIDR,192.168.0.0/16,DIRECT,no-resolve
  - MATCH,GLOBAL
EOF

export http_proxy="${HTTP_PROXY:-http://127.0.0.1:7890}"
export https_proxy="${HTTPS_PROXY:-http://127.0.0.1:7890}"
export all_proxy="${ALL_PROXY:-socks5://127.0.0.1:7890}"

environment_repair_scheduler() {
  [ "${ENVIRONMENT_REPAIR_AUTO_APPLY:-1}" = "1" ] || return 0
  (
    while :; do
      /usr/local/bin/environment-repair.sh >/tmp/environment-repair.log 2>&1 || true
      sleep "${ENVIRONMENT_REPAIR_INTERVAL:-3600}"
    done
  ) &
}

environment_repair_scheduler

exec mihomo -d /etc/mihomo
