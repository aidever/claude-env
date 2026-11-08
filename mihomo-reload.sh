#!/usr/bin/env bash
# Reload the user rule files (/etc/mihomo/custom/direct.txt and proxy.txt)
# without restarting Mihomo. Only the rule providers are refreshed, so TUN
# stays up and no traffic can bypass the proxy during the reload.
set -euo pipefail
API="${MIHOMO_API:-http://127.0.0.1:9090}"
for name in custom-direct custom-proxy; do
  curl -fsS --noproxy '*' -X PUT "$API/providers/rules/$name"
  echo "reloaded $name ($(curl -fsS --noproxy '*' "$API/providers/rules" | jq -r ".providers[\"$name\"].ruleCount") rules)"
done
