# The Go version go.mod asks for, from the image rather than go.dev: the old
# go1.24.5 tarball then made go download the go.mod toolchain as well, and
# either download failing mid-stream failed CI jobs (2026-09-24).
FROM golang:1.26.7-bookworm AS builder

COPY . /build
WORKDIR /build
RUN go build -o /harness-test .

# -------------------------------------------------------------------
FROM node:22-slim

RUN apt-get update && apt-get install -y \
    curl git jq python3 python3-pip python3-venv bubblewrap unzip \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /harness-test /usr/local/bin/harness-test

# belt CLI for --hooks belt mode: tests/fetch-belt.sh downloads the released binary
COPY tests/belt /usr/local/bin/belt

# Non-root user (Claude/Codex refuse --dangerously-skip-permissions as root)
RUN useradd -m -s /bin/bash testuser

# npm global installs go to user-writable dir
ENV NPM_CONFIG_PREFIX=/home/testuser/.npm-global
ENV PATH="/home/testuser/.npm-global/bin:/home/testuser/.local/bin:$PATH"

# Package caches at a fixed path, outside the per-harness temp HOME the runner
# creates. Left at their defaults they land under $HOME, so every harness
# downloaded its CLI from scratch and nothing survived the run. docker-compose
# mounts these as named volumes, which is what makes a second run fast.
ENV npm_config_cache=/home/testuser/.cache/npm
ENV PIP_CACHE_DIR=/home/testuser/.cache/pip
ENV UV_CACHE_DIR=/home/testuser/.cache/uv
ENV BUN_INSTALL_CACHE_DIR=/home/testuser/.cache/bun

RUN mkdir -p /home/testuser/.npm-global \
        /home/testuser/.cache/npm /home/testuser/.cache/pip \
        /home/testuser/.cache/uv /home/testuser/.cache/bun \
    && chown -R testuser:testuser /home/testuser

USER testuser

# bun runtime (Oh My Pi requires it)
RUN curl -fsSL https://bun.sh/install | bash
ENV PATH="/home/testuser/.bun/bin:$PATH"

WORKDIR /home/testuser

ENTRYPOINT ["harness-test"]
CMD ["--harness", "all", "--mode", "both"]
