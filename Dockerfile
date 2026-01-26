# mautrix-claude: Matrix bridge for Claude AI
#
# Supports both API mode and sidecar mode (Pro/Max subscription).
# Mode is controlled by config.yaml: claude.sidecar.enabled
#
# For sidecar mode, mount Claude Code credentials:
#   docker run -v ./data:/data -v ~/.claude:/home/bridge/.claude:ro mautrix-claude

# ============== Stage 1: Build Go binary ==============
# Use Debian-based image to match runtime libc (glibc)
FROM golang:1.24-bookworm AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    git ca-certificates build-essential libsqlite3-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG COMMIT_HASH
ARG BUILD_TIME
ARG VERSION=0.1.0

RUN CGO_ENABLED=1 go build -tags "goolm" -o /usr/bin/mautrix-claude \
    -ldflags "-s -w \
        -X main.Tag=${VERSION} \
        -X main.Commit=${COMMIT_HASH:-$(git rev-parse HEAD 2>/dev/null || echo unknown)} \
        -X 'main.BuildTime=${BUILD_TIME:-$(date -Iseconds)}'" \
    ./cmd/mautrix-claude

# ============== Stage 2: Final image ==============
FROM python:3.11-slim

ENV UID=1337 \
    GID=1337

# Install system dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    bash \
    jq \
    curl \
    gosu \
    sqlite3 \
    procps \
    && curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

# Install Claude Code CLI (for Pro/Max authentication)
RUN npm install -g @anthropic-ai/claude-code

# Install yq for YAML processing
RUN curl -sL https://github.com/mikefarah/yq/releases/latest/download/yq_linux_amd64 \
    -o /usr/bin/yq && chmod +x /usr/bin/yq

# Create bridge user
RUN useradd -m -u 1337 bridge && \
    mkdir -p /data /app/sidecar /home/bridge/.claude && \
    chown -R bridge:bridge /data /app /home/bridge

WORKDIR /app

# Copy Go binary
COPY --from=builder /usr/bin/mautrix-claude /usr/bin/mautrix-claude

# Copy and install Python sidecar
COPY sidecar/requirements.txt /app/sidecar/
RUN pip install --no-cache-dir -r /app/sidecar/requirements.txt

COPY sidecar/main.py /app/sidecar/

# Copy startup script
COPY docker-run.sh /docker-run.sh
RUN chmod +x /docker-run.sh

# Volume for data
VOLUME /data
WORKDIR /data

# Health check - verify the bridge process is running
# The bridge doesn't expose a health endpoint, so we check if it's accepting connections
HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=3 \
    CMD pgrep -x mautrix-claude > /dev/null || exit 1

# Run startup script
CMD ["/docker-run.sh"]
