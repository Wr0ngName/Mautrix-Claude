# Claude Agent SDK Sidecar

Python sidecar service that provides Claude AI capabilities to the mautrix-claude bridge using the official Agent SDK.

## Why a Sidecar?

- **Pro/Max Support**: Uses Claude Code's authentication (Pro/Max subscription) instead of API credits
- **Official SDK**: Uses Anthropic's official Agent SDK - stable, maintained, ToS compliant
- **Isolation**: Runs in separate container with restricted permissions
- **Session Management**: Maintains conversation context per Matrix room

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     Docker Compose                          │
├─────────────────────────────────────────────────────────────┤
│  ┌─────────────────┐         ┌─────────────────────────┐   │
│  │  mautrix-claude │  HTTP   │   claude-sidecar        │   │
│  │    (Go bridge)  │◄───────►│   (Python Agent SDK)    │   │
│  │                 │  :8090  │                         │   │
│  └────────┬────────┘         └───────────┬─────────────┘   │
│           │                              │                  │
│           │ Matrix                       │ Claude API       │
│           ▼                              ▼ (via Pro/Max)    │
│     Homeserver                      Anthropic               │
└─────────────────────────────────────────────────────────────┘
```

## Features

- **Per-room sessions**: Each Matrix room has isolated conversation context
- **Streaming responses**: Real-time response streaming to Matrix
- **Tool restrictions**: No file/bash access - chat only mode
- **Health checks**: Prometheus metrics and health endpoints
- **Graceful shutdown**: Proper cleanup of sessions

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Health check |
| `/metrics` | GET | Prometheus metrics |
| `/v1/chat` | POST | Send message, get response |
| `/v1/sessions/{id}` | DELETE | Clear session |

## Security: Allowed Tools

By default, only safe tools are enabled:
- `WebSearch` - Search the web
- `WebFetch` - Fetch web pages
- `AskUserQuestion` - Ask clarifying questions

**Explicitly blocked** (no file access, no code execution):
- Read, Write, Edit, MultiEdit
- Bash, Glob, Grep, LS
- Task, TodoWrite, TodoRead
- NotebookEdit

## Configuration

Environment variables:
- `CLAUDE_SIDECAR_PORT`: HTTP port (default: 8090)
- `CLAUDE_SIDECAR_ALLOWED_TOOLS`: Comma-separated tools (default: WebSearch,WebFetch,AskUserQuestion)
- `CLAUDE_SIDECAR_SYSTEM_PROMPT`: Default system prompt
- `CLAUDE_SIDECAR_MODEL`: Model to use (default: sonnet)
- `CLAUDE_SIDECAR_SESSION_TIMEOUT`: Session timeout in seconds (default: 3600)

## Running

```bash
# Authenticate Claude Code first (one-time)
claude

# Run sidecar
docker compose up claude-sidecar
```
