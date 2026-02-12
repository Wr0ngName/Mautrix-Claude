# mautrix-claude

A [Matrix](https://matrix.org) bridge for [Claude AI](https://www.anthropic.com/claude) built on the [mautrix](https://github.com/mautrix) bridge framework.

Chat with Claude models (Opus, Sonnet, Haiku) through any Matrix client. Supports both direct API access and Pro/Max subscriptions via the Claude Agent SDK sidecar.

## Features

- **Multi-model support** -- switch between Claude Opus, Sonnet, and Haiku per room
- **Vision API** -- send images (JPEG, PNG, GIF, WebP) and Claude will analyze them
- **Per-room configuration** -- independent system prompt, model, temperature, and max tokens per room
- **Two authentication modes** -- API key (pay-per-use) or Pro/Max subscription (via Agent SDK sidecar)
- **Conversation context** -- maintains history per room with configurable max age
- **Prompt caching** -- optional cache to reduce API costs on repeated context
- **Rate limiting** -- configurable per-minute request limits
- **E2E encryption** -- optional end-to-bridge encryption support

## Quick start

### 1. Build the image

```bash
docker compose build
```

### 2. Generate configuration

```bash
docker compose run --rm mautrix-claude
```

This creates `data/config.yaml` and exits. Edit it:

```bash
$EDITOR data/config.yaml
```

Set at minimum:
- `homeserver.address` -- your Matrix homeserver URL
- `homeserver.domain` -- your homeserver domain
- `bridge.permissions` -- who can use the bridge

### 3. Generate registration

Start again to generate the appservice registration:

```bash
docker compose run --rm mautrix-claude
```

This creates `data/registration.yaml`. Register it with your homeserver.

**Synapse** -- add to `homeserver.yaml`:
```yaml
app_service_config_files:
  - /path/to/mautrix-claude/data/registration.yaml
```

Then restart your homeserver.

### 4. Start the bridge

```bash
docker compose up -d
```

### 5. Log in

1. Invite `@claudebot:yourdomain.com` to a Matrix room
2. Send `login` to start the login flow
3. Enter your Anthropic API key from [console.anthropic.com](https://console.anthropic.com/)

You can now message Claude in the room.

## Sidecar mode (Pro/Max subscriptions)

Instead of paying per API call, you can use a Claude Pro or Max subscription through the Agent SDK sidecar.

### Setup

1. Install and authenticate Claude Code on the host:

```bash
npm install -g @anthropic-ai/claude-code
claude  # complete the OAuth flow in browser
```

2. Copy credentials into the bridge data directory:

```bash
cp -r ~/.claude/* ./data/.claude/
```

3. Enable sidecar in `data/config.yaml`:

```yaml
network:
  sidecar:
    enabled: true
```

4. Restart:

```bash
docker compose up -d
```

Users can now log in with `login` and choose the sidecar authentication method.

## Commands

These work in the management room (DM with the bridge bot) or via `!claude <command>` in other rooms:

| Command | Description |
|---------|-------------|
| `help` | Show available commands |
| `login` | Start authentication |
| `logout` | Remove current login |
| `models` | List available Claude models |
| `model <name>` | Switch model (`opus`, `sonnet`, `haiku`, or full ID) |
| `system <prompt>` | Set system prompt for this room |
| `temperature <0.0-1.0>` | Adjust response randomness |
| `max_tokens <n>` | Set max response tokens |

## Configuration reference

Key settings in `data/config.yaml` under the `network:` section:

```yaml
network:
  default_model: sonnet          # opus, sonnet, haiku, or full model ID
  max_tokens: 4096               # max response tokens (1-16384)
  temperature: 1.0               # randomness (0.0-1.0)
  system_prompt: "You are a helpful AI assistant."
  conversation_max_age_hours: 24  # 0 = unlimited context
  rate_limit_per_minute: 60       # 0 = unlimited
  sidecar:
    enabled: false                # true for Pro/Max mode
```

See [example-config.yaml](example-config.yaml) for all options including homeserver, appservice, bridge permissions, encryption, and logging.

## Architecture

```
Matrix Client
     |
     v
Matrix Homeserver
     |  (Application Service protocol)
     v
mautrix-claude (Go)
     |
     +--> Anthropic API  (API key mode)
     |
     +--> Python Sidecar (Pro/Max mode)
           |
           +--> Claude Agent SDK --> Anthropic
```

The bridge runs as a Matrix [application service](https://spec.matrix.org/latest/application-service-api/). Each Claude model family appears as a ghost user in Matrix (e.g. `@claude_sonnet:yourdomain.com`).

The optional Python sidecar (`sidecar/`) provides Pro/Max subscription support by wrapping the Claude Agent SDK behind a local HTTP API. It runs inside the same container and is started automatically when enabled.

## Building from source

Requires Go 1.24+:

```bash
go build -tags goolm -o mautrix-claude ./cmd/mautrix-claude
```

## Resource requirements

- **API mode**: minimal (the Go binary is lightweight)
- **Sidecar mode**: 2-4 GB RAM (the Claude Agent SDK bundles a Node.js CLI that is memory-intensive)

The provided `docker-compose.yaml` sets memory limits accordingly.

## Troubleshooting

**"Sidecar health check failed"** -- Check that credentials exist in `./data/.claude/` and review container logs for Python errors.

**"Claude Code is not authenticated"** -- Re-run `claude` on the host to complete OAuth, then copy credentials again.

**"Circuit breaker open"** -- The sidecar detected repeated failures. Check Anthropic API status and wait 30 seconds for the circuit to reset.

**"Failed to redact"** -- The bridge bot needs power level 50+ in management rooms to redact login commands containing secrets. Grant it moderator or ignore the warning (login still works).

**OOM kills in sidecar mode** -- Increase container memory to 4 GB+.

## License

See source files for license information.
