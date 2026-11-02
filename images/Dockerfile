FROM node:24-bookworm-slim@sha256:c86ecead9e55a5210ac1727de38a4ca840bf03dcaf34dc9749addbf383b1920c AS base
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl git ripgrep procps tmux \
    && rm -rf /var/lib/apt/lists/*
ENV HOME=/tmp TERM=xterm-256color
WORKDIR /tmp

FROM base AS claude-install
ARG CLAUDE_VERSION=2.1.295
RUN curl -fsSL https://claude.ai/install.sh -o /tmp/install-claude.sh \
    && HOME=/root bash /tmp/install-claude.sh "$CLAUDE_VERSION" \
    && cp -L /root/.local/bin/claude /usr/local/bin/claude \
    && chmod 0755 /usr/local/bin/claude \
    && claude --version

FROM base AS claude
COPY --from=claude-install /usr/local/bin/claude /usr/local/bin/claude
ENV DISABLE_AUTOUPDATER=1
USER 1000:1000
CMD ["claude"]

FROM base AS codex
ARG CODEX_VERSION=0.153.4
RUN npm install --global "@openai/codex@${CODEX_VERSION}" \
    && npm cache clean --force \
    && codex --version
USER 1000:1000
CMD ["codex"]
