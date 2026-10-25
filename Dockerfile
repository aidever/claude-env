FROM ubuntu:24.04

ARG MIHOMO_VERSION=v1.19.20
ENV DEBIAN_FRONTEND=noninteractive
ENV LOOKLOOK_PREFIX=/opt/looklook
# Keep Looklook's download/extraction staging area on the same filesystem as
# its install prefix.  Its updater renames the extracted `fonts` directory;
# using /tmp can be a different mount in Docker and makes rename(2) fail with
# EXDEV ("Cross-device link").
ENV PATH=/root/.local/bin:${PATH}

COPY change.sh /usr/local/bin/change.sh
COPY environment-repair.sh /usr/local/bin/environment-repair.sh

RUN export TMPDIR=/tmp TMP=/tmp TEMP=/tmp && \
    apt-get update && apt-get install -y --no-install-recommends ca-certificates curl gzip iproute2 iptables locales git jq openssh-client build-essential ripgrep less procps python3 tzdata && \
    locale-gen en_US.UTF-8 && \
    curl -fsSL https://deb.nodesource.com/setup_22.x | bash - && \
    apt-get install -y --no-install-recommends nodejs && \
    npm install -g @anthropic-ai/claude-code @openai/codex && \
    arch="amd64" && curl -fsSL "https://github.com/MetaCubeX/mihomo/releases/download/${MIHOMO_VERSION}/mihomo-linux-${arch}-${MIHOMO_VERSION}.gz" -o /tmp/mihomo.gz && \
    gunzip /tmp/mihomo.gz && install -m 0755 /tmp/mihomo /usr/local/bin/mihomo && rm -f /tmp/mihomo && \
    export TMPDIR=/opt/looklook/.tmp TMP=/opt/looklook/.tmp TEMP=/opt/looklook/.tmp && \
    mkdir -p /opt/looklook/.tmp && \
    curl -fsSL https://looklook.dev/install.sh | sh && \
    git clone --depth 1 https://github.com/yiancode/fuck-claude.git /opt/fuck-claude && \
    mkdir -p /opt/looklook/.tmp && \
    rm -rf /var/lib/apt/lists/*

COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh /usr/local/bin/change.sh /usr/local/bin/environment-repair.sh && mkdir -p /etc/mihomo /workspace

EXPOSE 7890
VOLUME ["/workspace"]
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
