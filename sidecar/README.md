# Claude Agent SDK Sidecar

Internal Python service that provides Claude AI capabilities using the official Agent SDK.

## Overview

This sidecar runs inside the mautrix-claude container and enables Pro/Max subscription support via the Claude Agent SDK instead of API credits.

**Note**: This is an internal component. Users don't need to configure or run it separately - it's automatically started when `ENABLE_SIDECAR=true` is set.

## How It Works

```
┌─────────────────────────────────────────────────────────────┐
│                  mautrix-claude Container                    │
├─────────────────────────────────────────────────────────────┤
│  ┌─────────────────┐         ┌─────────────────────────┐   │
│  │  Go Bridge      │  HTTP   │   Python Sidecar        │   │
│  │  (mautrix-      │◄───────►│   (Agent SDK)           │   │
│  │   claude)       │ :8090   │                         │   │
│  └────────┬────────┘         └───────────┬─────────────┘   │
│           │                              │                  │
│           │ Matrix                       │ Claude API       │
│           ▼                              ▼ (via Pro/Max)    │
│     Homeserver                      Anthropic               │
└─────────────────────────────────────────────────────────────┘
```

## Features

- **Per-room sessions**: Each Matrix room has isolated conversation context
- **Tool restrictions**: Only safe tools enabled (WebSearch, WebFetch, AskUserQuestion)
- **Health checks**: Prometheus metrics at `/metrics`
- **Graceful shutdown**: Proper cleanup of sessions

## Configuration

Environment variables (set when running the container):

| Variable | Default | Description |
|----------|---------|-------------|
| `ENABLE_SIDECAR` | `false` | Enable sidecar mode |
| `CLAUDE_SIDECAR_ALLOWED_TOOLS` | `WebSearch,WebFetch,AskUserQuestion` | Allowed tools |
| `CLAUDE_SIDECAR_SYSTEM_PROMPT` | `You are a helpful AI assistant.` | Default prompt |
| `CLAUDE_SIDECAR_MODEL` | `sonnet` | Model to use |
| `CLAUDE_SIDECAR_SESSION_TIMEOUT` | `3600` | Session timeout (seconds) |

## Security: Allowed Tools

Only safe tools are enabled by default:
- `WebSearch` - Search the web
- `WebFetch` - Fetch web pages
- `AskUserQuestion` - Ask clarifying questions

**Explicitly blocked** (hardcoded, cannot be enabled):
- Read, Write, Edit, MultiEdit
- Bash, Glob, Grep, LS
- Task, TodoWrite, TodoRead
- NotebookEdit

## Usage

Run the container with sidecar mode:

```bash
docker run -v ./data:/data \
  -v ~/.claude:/home/bridge/.claude:ro \
  -e ENABLE_SIDECAR=true \
  mautrix-claude
```

The `~/.claude` mount provides the Claude Code authentication for Pro/Max subscriptions.

## API Endpoints (Internal)

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Health check |
| `/metrics` | GET | Prometheus metrics |
| `/v1/chat` | POST | Send message, get response |
| `/v1/sessions/{id}` | DELETE | Clear session |
