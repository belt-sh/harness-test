FROM node:22-slim AS builder

RUN apt-get update && apt-get install -y curl git && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://go.dev/dl/go1.24.5.linux-$(dpkg --print-architecture).tar.gz | tar -C /usr/local -xz
ENV PATH="/usr/local/go/bin:$PATH"

COPY . /build
WORKDIR /build
RUN go build -o /harness-test .

# -------------------------------------------------------------------
FROM node:22-slim

RUN apt-get update && apt-get install -y \
    curl git jq python3 python3-pip python3-venv bubblewrap unzip \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /harness-test /usr/local/bin/harness-test

# belt CLI (optional; place at tests/belt before building for --hooks belt mode)
COPY tests/belt /usr/local/bin/belt

# Non-root user (Claude/Codex refuse --dangerously-skip-permissions as root)
RUN useradd -m -s /bin/bash testuser

# npm global installs go to user-writable dir
ENV NPM_CONFIG_PREFIX=/home/testuser/.npm-global
ENV PATH="/home/testuser/.npm-global/bin:/home/testuser/.local/bin:$PATH"
RUN mkdir -p /home/testuser/.npm-global && chown -R testuser:testuser /home/testuser

USER testuser

# bun runtime (Oh My Pi requires it)
RUN curl -fsSL https://bun.sh/install | bash
ENV PATH="/home/testuser/.bun/bin:$PATH"

WORKDIR /home/testuser

ENTRYPOINT ["harness-test"]
CMD ["--harness", "all", "--mode", "both"]
