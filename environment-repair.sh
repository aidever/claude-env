#!/usr/bin/env bash
set -u

if [ -x /opt/fuck-claude/bin/apply-docker.sh ]; then
  /opt/fuck-claude/bin/apply-docker.sh || true
fi

# Apply the location-based timezone last; the repair script has its own
# default timezone and would otherwise overwrite the detected value.
/usr/local/bin/change.sh || true
