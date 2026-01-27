# Typing Indicator Issue - Diagnostic Plan

## What We Know
1. Works in API mode - typing stays on for full duration
2. Works in mautrix-candy - typing stays on for full duration
3. FAILS in sidecar mode - typing shows 1-2s then disappears
4. Sidecar mode with Opus takes 10-15s total
5. Server/framework is NOT timing out (proven by API mode + candy working)

## What This Means
The bridge code is DOING SOMETHING in sidecar mode that clears typing early. Either:
- stopTyping() is being called prematurely
- MarkTyping is failing silently
- The ghost/intent has an issue specific to sidecar mode
- Something in sidecar flow is different

## Diagnostic Logging Added

Added comprehensive logging to `pkg/connector/client.go` to track exactly when typing starts/stops:

- `TYPING_DEBUG: Starting typing indicator` - with mode, ghost_id, room_id
- `TYPING_DEBUG: MarkTyping START success/failed`
- `TYPING_DEBUG: Calling CreateMessageStream`
- `TYPING_DEBUG: CreateMessageStream returned` - with error status and elapsed time
- `TYPING_DEBUG: Entering stream event loop`
- `TYPING_DEBUG: Received stream event` - for each event with type and elapsed time
- `TYPING_DEBUG: Stream loop completed, stopping typing`
- `TYPING_DEBUG: Stopping typing indicator` - with total duration
- `TYPING_DEBUG: MarkTyping STOP success/failed`

## How to Test

1. Deploy the updated bridge
2. Send message in sidecar mode with Opus
3. Watch Matrix client - note EXACTLY when typing appears/disappears
4. Check bridge logs with: `grep TYPING_DEBUG`
5. Compare timing between typing disappearance and log events

## Expected Log Pattern (if working correctly)

```
T=0.0s  TYPING_DEBUG: Starting typing indicator
T=0.0s  TYPING_DEBUG: MarkTyping START success
T=0.0s  TYPING_DEBUG: Calling CreateMessageStream
T=0.0s  TYPING_DEBUG: CreateMessageStream returned (no error)
T=0.0s  TYPING_DEBUG: Entering stream event loop
T=0.0s  TYPING_DEBUG: Received stream event (message_start)
[... wait for Claude response ...]
T=12s   TYPING_DEBUG: Received stream event (content_block_delta)
T=12s   TYPING_DEBUG: Received stream event (message_delta)
T=12s   TYPING_DEBUG: Received stream event (message_stop)
T=12s   TYPING_DEBUG: Stream loop completed
T=12s   TYPING_DEBUG: Stopping typing indicator (duration=12s)
T=12s   TYPING_DEBUG: MarkTyping STOP success
```

## What To Look For

1. **If "MarkTyping START success" but typing disappears in Matrix after 1-2s**:
   - Ghost not properly joined to room
   - Intent/ghost permission issue
   - Matrix homeserver issue specific to this ghost

2. **If "Stopping typing indicator" appears early (after 1-2s)**:
   - stopTyping() called prematurely
   - Error path triggered
   - Stream closed early

3. **If stream loop exits early**:
   - Channel closed prematurely by sidecar
   - Sidecar goroutine died
   - Context cancelled

4. **If MarkTyping FAILED logged**:
   - Error will show the reason
   - Likely permission/membership issue

## Next Actions Based on Findings

After collecting logs, implement targeted fix based on actual root cause.
