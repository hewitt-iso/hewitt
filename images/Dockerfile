FROM node:24-bookworm-slim@sha256:c86ecead9e55a5210ac1727de38a4ca840bf03dcaf34dc9749addbf383b1920c AS base
LABEL org.opencontainers.image.source="https://github.com/hewitt-iso/hewitt"
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash-completion ca-certificates curl git less ripgrep procps libevent-core-2.1-7 libncursesw6 ncurses-term \
    dbus gnome-keyring openssh-client libnss-wrapper \
    && rm -rf /var/lib/apt/lists/*
# GitHub host keys come from its TLS-authenticated metadata API; other hosts
# are accepted on first use.
RUN curl -fsSL https://api.github.com/meta \
    | node -e 'const m=JSON.parse(require("fs").readFileSync(0));for(const k of m.ssh_keys)for(const h of ["github.com","ssh.github.com"])console.log(h+" "+k)' \
    > /etc/ssh/ssh_known_hosts \
    && printf 'Host *\n    StrictHostKeyChecking accept-new\n' > /etc/ssh/ssh_config.d/hewitt.conf
ENV HOME=/tmp TERM=xterm-256color COLORTERM=truecolor LANG=C.UTF-8 LC_ALL=C.UTF-8 PATH=/hewitt/tools/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
WORKDIR /tmp

# Bookworm's tmux 3.3a cannot answer default-color queries from the attached
# terminal. Codex needs both OSC 10 and OSC 11 replies for composer shading.
FROM base AS tmux-build
USER root
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential bison pkg-config libevent-dev libncurses-dev python3 \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /tmp/tmux-build
RUN curl -fsSL https://github.com/tmux/tmux/releases/download/3.8/tmux-3.8.tar.gz -o tmux.tar.gz \
    && echo 'e79c699c7e949dccd0a4a125e17b8d1261e311b16979dbc8eb34542f3966d82e  tmux.tar.gz' | sha256sum -c - \
    && tar -xzf tmux.tar.gz --strip-components=1 \
    && ./configure --prefix=/usr/local \
    && make -j2 \
    && make install
COPY test-terminal-colors.py /tmp/test-terminal-colors.py
RUN python3 /tmp/test-terminal-colors.py

FROM base AS hewitt
COPY --from=tmux-build /usr/local/bin/tmux /usr/local/bin/tmux
RUN test "$(tmux -V)" = "tmux 3.8"
LABEL org.opencontainers.image.description="Hewitt unified workspace environment for coding agents"
COPY --chmod=0755 hewitt-agent /usr/local/bin/hewitt-agent
ENV DISABLE_AUTOUPDATER=1 OPENCODE_DISABLE_AUTOUPDATE=1
USER 1000:1000
CMD ["hewitt-agent"]

# Egress proxy for restricted sessions. Hewitt mounts its configuration and
# host allowlist; the proxy runs as the invoking user on an unprivileged port.
FROM base AS egress
RUN apt-get update && apt-get install -y --no-install-recommends tinyproxy \
    && rm -rf /var/lib/apt/lists/*
USER 1000:1000
CMD ["tinyproxy", "-d", "-c", "/hewitt/egress/tinyproxy.conf"]
