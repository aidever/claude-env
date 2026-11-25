#!/usr/bin/env bash
# Docker-specific environment repair: apply container-appropriate settings
# (no systemd, no browsers).
set -u

FC_TARGET_TIMEZONE="${FUCK_CLAUDE_TIMEZONE:-America/Los_Angeles}"
FC_TARGET_LOCALE="${FUCK_CLAUDE_LOCALE:-en_US}.UTF-8"

# 1) Timezone — no timedatectl in Docker; manipulate /etc/localtime directly
if [ -f "/usr/share/zoneinfo/$FC_TARGET_TIMEZONE" ]; then
  ln -sf "/usr/share/zoneinfo/$FC_TARGET_TIMEZONE" /etc/localtime
  echo "$FC_TARGET_TIMEZONE" > /etc/timezone 2>/dev/null || true
fi

# 2) Locale env — write to a file sourced by shell rc files
mkdir -p "${HOME}/.config/fuck-claude"
cat >"${HOME}/.config/fuck-claude/env.sh" <<EOF
# managed by claude-env environment-repair
export LANG=${FC_TARGET_LOCALE}
export LC_ALL=${FC_TARGET_LOCALE}
export LANGUAGE=en_US:en
EOF
for rc in "${HOME}/.bashrc" "${HOME}/.profile"; do
  [ -f "$rc" ] || touch "$rc"
  if ! grep -q 'fuck-claude/env.sh' "$rc" 2>/dev/null; then
    printf '\n# fuck-claude locale\n[ -f "$HOME/.config/fuck-claude/env.sh" ] && . "$HOME/.config/fuck-claude/env.sh"\n' >>"$rc"
  fi
done

# 3) curlrc headers
if [ ! -f "${HOME}/.curlrc" ]; then
  cat >"${HOME}/.curlrc" <<'EOF'
# managed by claude-env environment-repair
header = "Sec-GPC: 1"
header = "DNT: 1"
header = "Accept-Language: en-US,en;q=0.9"
EOF
elif ! grep -q 'Sec-GPC' "${HOME}/.curlrc" 2>/dev/null; then
  cat >>"${HOME}/.curlrc" <<'EOF'

# added by claude-env environment-repair
header = "Sec-GPC: 1"
header = "DNT: 1"
header = "Accept-Language: en-US,en;q=0.9"
EOF
fi

# Apply the location-based timezone last; the fallback timezone above would
# otherwise overwrite the detected value.
/usr/local/bin/change.sh || true
