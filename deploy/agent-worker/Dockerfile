# syntax=docker/dockerfile:1.4
#
# The image of an agent worker (Spec 092): a static dagu, git, python3, jq and the CLI
# of the agent. Dagu installs nothing at run time. This one image also serves a
# general worker and the egress proxy, with another entrypoint.
#
# Build from the repository root:
#   docker build -f deploy/agent-worker/Dockerfile -t dagu-agent-worker:local .
#
# The build downloads Claude Code. The tests of the repository do not run this build.
# Set CLAUDE_CODE_VERSION to a version that you tested.

# Stage 1: a static dagu. A worker needs no UI. The folder of the UI assets holds only
# .gitkeep, and go:embed still works. The file .dockerignore removes this folder from the
# build context, so the stage makes the file again.
FROM golang:1.27.3 AS go-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir -p internal/service/frontend/assets \
    && touch internal/service/frontend/assets/.gitkeep \
    && CGO_ENABLED=0 go build -tags netgo,osusergo -o /out/dagu ./cmd

# Stage 2: Claude Code. This stage keeps curl out of the final image.
# The install line is the native installer of Claude Code. It puts the binary at
# /root/.local/bin/claude. Check this line again when you change the version.
FROM debian:stable-slim AS claude-installer
ARG CLAUDE_CODE_VERSION=stable
RUN apt-get update \
    && apt-get install -y --no-install-recommends curl ca-certificates bash \
    && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://claude.ai/install.sh | bash -s "${CLAUDE_CODE_VERSION}" \
    && mkdir -p /out \
    && cp -L /root/.local/bin/claude /out/claude

# Stage 3: the image of the worker.
FROM debian:stable-slim

# git: the git steps and the agent. python3: the tests of the example project.
# jq: the gate of the example DAG.
RUN apt-get update \
    && apt-get install -y --no-install-recommends git python3 ca-certificates jq \
    && rm -rf /var/lib/apt/lists/*

# The restricted user reads a repository that the worker owns.
RUN git config --system --add safe.directory '*'

COPY --from=go-builder /out/dagu /usr/local/bin/dagu
COPY --from=claude-installer /out/claude /usr/local/bin/claude

# No setuid or setgid binary stays in the image.
RUN find / -xdev -perm /6000 -type f -exec chmod a-s {} + ; true

# The Dagu home and the temporary files are tmpfs mounts at run time.
ENV HOME=/tmp
ENV DAGU_HOME=/var/lib/dagu

ENTRYPOINT ["/usr/local/bin/dagu", "worker", "--config", "/etc/dagu/config.yaml", "--agent", "claude=/usr/local/bin/claude"]
