# mautrix-claude: Matrix bridge for Claude AI
#
# Supports both:
# - API mode (default): Direct Anthropic API with API key
# - Sidecar mode: Agent SDK with Pro/Max subscription (set ENABLE_SIDECAR=true)
#
# Usage:
#   API mode (default):
#     docker run -v ./data:/data mautrix-claude
#
#   Sidecar mode (Pro/Max):
#     docker run -v ./data:/data -v ~/.claude:/home/bridge/.claude:ro \
#       -e ENABLE_SIDECAR=true mautrix-claude

# ============== Stage 1: Build Go binary ==============
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git ca-certificates build-base sqlite-dev

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
    GID=1337 \
    ENABLE_SIDECAR=false \
    CLAUDE_SIDECAR_PORT=8090 \
    CLAUDE_SIDECAR_ALLOWED_TOOLS="WebSearch,WebFetch,AskUserQuestion" \
    CLAUDE_SIDECAR_MODEL="sonnet"

# Install system dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    bash \
    jq \
    curl \
    gosu \
    sqlite3 \
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

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=15s --retries=3 \
    CMD curl -sf http://localhost:29320/_health || exit 1

# Run startup script
CMD ["/docker-run.sh"]
