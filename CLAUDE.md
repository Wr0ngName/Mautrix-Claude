# mautrix-claude development

## Build

Local development (no olm C headers required):
```bash
CGO_ENABLED=1 go build -tags "noolm,nocrypto" -o mautrix-claude ./cmd/mautrix-claude
```

Docker (has olm headers installed):
```bash
docker compose build
```

The Dockerfile uses `-tags "goolm"` because the builder stage installs `libsqlite3-dev` and the Go OLM implementation. Local builds use `noolm,nocrypto` to skip the C olm dependency.

## Test

```bash
CGO_ENABLED=1 go test -tags "noolm,nocrypto" ./...
```

## Project structure

- `cmd/mautrix-claude/` — main binary entry point
- `pkg/claudeapi/` — Anthropic Go SDK wrapper (messages, streaming, models, caching, metrics)
- `pkg/connector/` — Matrix bridge connector (client, login, commands)
- `pkg/sidecar/` — Go client for the Python sidecar HTTP API
- `sidecar/` — Python FastAPI sidecar for Pro/Max subscription via Claude Agent SDK

## SDK versions (pinned)

- **Go**: `github.com/anthropics/anthropic-sdk-go` — see `go.mod` for exact version
- **Python**: `claude-agent-sdk` — see `sidecar/requirements.txt` for exact version

## Sidecar

The Python sidecar (`sidecar/main.py`) bridges Claude Agent SDK for Pro/Max users. It runs on port 8090 inside the container and is started by `docker-run.sh` when `network.sidecar.enabled: true` in config.

Install sidecar deps locally (for IDE support):
```bash
pip install -r sidecar/requirements.txt
```

## Docker

Do NOT run docker scripts directly on the host. Use `docker compose` commands:
```bash
docker compose build
docker compose up -d
docker compose logs -f
```
