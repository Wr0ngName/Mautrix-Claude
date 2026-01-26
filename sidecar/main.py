#!/usr/bin/env python3
"""
Claude Agent SDK Sidecar for mautrix-claude bridge.

Provides HTTP API for Go bridge to communicate with Claude using Pro/Max subscription.
"""

import asyncio
import logging
import os
import time
import uuid
from dataclasses import dataclass, field
from typing import AsyncIterator, Dict, Optional

from fastapi import FastAPI, HTTPException
from fastapi.responses import StreamingResponse
from pydantic import BaseModel
from prometheus_client import Counter, Histogram, Gauge, generate_latest, CONTENT_TYPE_LATEST
from starlette.responses import Response

# Agent SDK imports
from claude_agent_sdk import query, ClaudeAgentOptions, ClaudeSDKClient

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(name)s - %(levelname)s - %(message)s'
)
logger = logging.getLogger(__name__)

# Configuration
PORT = int(os.getenv("CLAUDE_SIDECAR_PORT", "8090"))
# Default allowed tools: WebSearch, WebFetch, AskUserQuestion only
# No file access, no bash, no code editing - safe for multi-user chat
DEFAULT_ALLOWED_TOOLS = ["WebSearch", "WebFetch", "AskUserQuestion"]
ALLOWED_TOOLS = os.getenv("CLAUDE_SIDECAR_ALLOWED_TOOLS", ",".join(DEFAULT_ALLOWED_TOOLS)).split(",")
ALLOWED_TOOLS = [t.strip() for t in ALLOWED_TOOLS if t.strip()]
SYSTEM_PROMPT = os.getenv("CLAUDE_SIDECAR_SYSTEM_PROMPT", "You are a helpful AI assistant.")
MODEL = os.getenv("CLAUDE_SIDECAR_MODEL", "sonnet")
SESSION_TIMEOUT = int(os.getenv("CLAUDE_SIDECAR_SESSION_TIMEOUT", "3600"))  # 1 hour

# Prometheus metrics
REQUESTS_TOTAL = Counter('claude_sidecar_requests_total', 'Total requests', ['endpoint', 'status'])
REQUEST_DURATION = Histogram('claude_sidecar_request_duration_seconds', 'Request duration')
ACTIVE_SESSIONS = Gauge('claude_sidecar_active_sessions', 'Number of active sessions')
TOKENS_USED = Counter('claude_sidecar_tokens_total', 'Total tokens used', ['type'])

# FastAPI app
app = FastAPI(
    title="Claude Agent SDK Sidecar",
    description="HTTP API for mautrix-claude bridge",
    version="1.0.0"
)


@dataclass
class Session:
    """Represents a conversation session."""
    session_id: str
    portal_id: str
    created_at: float = field(default_factory=time.time)
    last_used: float = field(default_factory=time.time)
    message_count: int = 0


class ChatRequest(BaseModel):
    """Request body for chat endpoint."""
    portal_id: str
    message: str
    system_prompt: Optional[str] = None
    model: Optional[str] = None
    stream: bool = False


class ChatResponse(BaseModel):
    """Response body for chat endpoint."""
    portal_id: str
    session_id: str
    response: str
    tokens_used: Optional[int] = None


class SessionManager:
    """Manages conversation sessions per portal."""

    def __init__(self):
        self.sessions: Dict[str, Session] = {}
        self._lock = asyncio.Lock()
        self._cleanup_task: Optional[asyncio.Task] = None

    async def start(self):
        """Start background cleanup task."""
        self._cleanup_task = asyncio.create_task(self._cleanup_loop())
        logger.info("Session manager started")

    async def stop(self):
        """Stop background cleanup task."""
        if self._cleanup_task:
            self._cleanup_task.cancel()
            try:
                await self._cleanup_task
            except asyncio.CancelledError:
                pass
        logger.info("Session manager stopped")

    async def _cleanup_loop(self):
        """Periodically clean up expired sessions."""
        while True:
            try:
                await asyncio.sleep(60)  # Check every minute
                await self._cleanup_expired()
            except asyncio.CancelledError:
                break
            except Exception as e:
                logger.error(f"Error in cleanup loop: {e}")

    async def _cleanup_expired(self):
        """Remove sessions older than timeout."""
        async with self._lock:
            now = time.time()
            expired = [
                portal_id for portal_id, session in self.sessions.items()
                if now - session.last_used > SESSION_TIMEOUT
            ]
            for portal_id in expired:
                del self.sessions[portal_id]
                logger.info(f"Cleaned up expired session for portal {portal_id}")

            ACTIVE_SESSIONS.set(len(self.sessions))

    async def get_or_create(self, portal_id: str) -> Session:
        """Get existing session or create new one."""
        async with self._lock:
            if portal_id not in self.sessions:
                session = Session(
                    session_id=str(uuid.uuid4()),
                    portal_id=portal_id
                )
                self.sessions[portal_id] = session
                logger.info(f"Created new session {session.session_id} for portal {portal_id}")
                ACTIVE_SESSIONS.set(len(self.sessions))

            session = self.sessions[portal_id]
            session.last_used = time.time()
            return session

    async def delete(self, portal_id: str) -> bool:
        """Delete a session."""
        async with self._lock:
            if portal_id in self.sessions:
                del self.sessions[portal_id]
                ACTIVE_SESSIONS.set(len(self.sessions))
                logger.info(f"Deleted session for portal {portal_id}")
                return True
            return False

    async def get_stats(self, portal_id: str) -> Optional[dict]:
        """Get session statistics."""
        async with self._lock:
            if portal_id in self.sessions:
                session = self.sessions[portal_id]
                return {
                    "session_id": session.session_id,
                    "portal_id": session.portal_id,
                    "created_at": session.created_at,
                    "last_used": session.last_used,
                    "message_count": session.message_count,
                    "age_seconds": time.time() - session.created_at
                }
            return None


# Global session manager
session_manager = SessionManager()


@app.on_event("startup")
async def startup():
    """Start session manager on app startup."""
    await session_manager.start()
    logger.info(f"Claude sidecar started on port {PORT}")
    logger.info(f"Allowed tools: {ALLOWED_TOOLS or 'none (chat only)'}")
    logger.info(f"Model: {MODEL}")


@app.on_event("shutdown")
async def shutdown():
    """Stop session manager on app shutdown."""
    await session_manager.stop()
    logger.info("Claude sidecar stopped")


@app.get("/health")
async def health():
    """Health check endpoint."""
    return {"status": "healthy", "sessions": len(session_manager.sessions)}


@app.get("/metrics")
async def metrics():
    """Prometheus metrics endpoint."""
    return Response(content=generate_latest(), media_type=CONTENT_TYPE_LATEST)


@app.post("/v1/chat", response_model=ChatResponse)
async def chat(request: ChatRequest):
    """
    Send a message to Claude and get a response.

    Maintains conversation context per portal_id.
    """
    start_time = time.time()

    try:
        # Get or create session for this portal
        session = await session_manager.get_or_create(request.portal_id)

        # Build options
        options = ClaudeAgentOptions(
            allowed_tools=ALLOWED_TOOLS if ALLOWED_TOOLS else [],
            permission_mode="bypassPermissions",  # No interactive prompts
            model=request.model or MODEL,
        )

        # Resume session if not first message
        if session.message_count > 0:
            options.resume = session.session_id

        # Set system prompt
        system_prompt = request.system_prompt or SYSTEM_PROMPT
        if system_prompt:
            options.system_prompt = system_prompt

        # Query Claude
        response_text = ""
        tokens_used = 0

        async for message in query(prompt=request.message, options=options):
            # Capture session ID on init
            if hasattr(message, 'subtype') and message.subtype == 'init':
                if hasattr(message, 'data') and 'session_id' in message.data:
                    session.session_id = message.data['session_id']

            # Capture result
            if hasattr(message, 'result'):
                response_text = message.result

            # Capture token usage if available
            if hasattr(message, 'usage'):
                if hasattr(message.usage, 'input_tokens'):
                    tokens_used += message.usage.input_tokens
                    TOKENS_USED.labels(type='input').inc(message.usage.input_tokens)
                if hasattr(message.usage, 'output_tokens'):
                    tokens_used += message.usage.output_tokens
                    TOKENS_USED.labels(type='output').inc(message.usage.output_tokens)

        # Update session
        session.message_count += 1
        session.last_used = time.time()

        REQUESTS_TOTAL.labels(endpoint='/v1/chat', status='success').inc()
        REQUEST_DURATION.observe(time.time() - start_time)

        return ChatResponse(
            portal_id=request.portal_id,
            session_id=session.session_id,
            response=response_text,
            tokens_used=tokens_used if tokens_used > 0 else None
        )

    except Exception as e:
        logger.error(f"Error processing chat request: {e}")
        REQUESTS_TOTAL.labels(endpoint='/v1/chat', status='error').inc()
        raise HTTPException(status_code=500, detail=str(e))


@app.post("/v1/chat/stream")
async def chat_stream(request: ChatRequest):
    """
    Send a message to Claude and stream the response.

    Returns Server-Sent Events (SSE) stream.
    """
    async def generate() -> AsyncIterator[str]:
        try:
            session = await session_manager.get_or_create(request.portal_id)

            options = ClaudeAgentOptions(
                allowed_tools=ALLOWED_TOOLS if ALLOWED_TOOLS else [],
                permission_mode="bypassPermissions",
                model=request.model or MODEL,
            )

            if session.message_count > 0:
                options.resume = session.session_id

            system_prompt = request.system_prompt or SYSTEM_PROMPT
            if system_prompt:
                options.system_prompt = system_prompt

            async for message in query(prompt=request.message, options=options):
                # Capture session ID
                if hasattr(message, 'subtype') and message.subtype == 'init':
                    if hasattr(message, 'data') and 'session_id' in message.data:
                        session.session_id = message.data['session_id']
                        yield f"data: {{\"type\": \"session\", \"session_id\": \"{session.session_id}\"}}\n\n"

                # Stream assistant messages
                if hasattr(message, 'type') and message.type == 'assistant':
                    if hasattr(message, 'message') and message.message:
                        for block in message.message.content:
                            if hasattr(block, 'text'):
                                import json
                                yield f"data: {json.dumps({'type': 'text', 'content': block.text})}\n\n"

                # Stream final result
                if hasattr(message, 'result'):
                    import json
                    yield f"data: {json.dumps({'type': 'result', 'content': message.result})}\n\n"

            session.message_count += 1
            session.last_used = time.time()

            yield "data: {\"type\": \"done\"}\n\n"

        except Exception as e:
            logger.error(f"Error in stream: {e}")
            import json
            yield f"data: {json.dumps({'type': 'error', 'message': str(e)})}\n\n"

    return StreamingResponse(
        generate(),
        media_type="text/event-stream",
        headers={
            "Cache-Control": "no-cache",
            "Connection": "keep-alive",
        }
    )


@app.delete("/v1/sessions/{portal_id}")
async def delete_session(portal_id: str):
    """Delete a session (clear conversation history)."""
    deleted = await session_manager.delete(portal_id)
    if deleted:
        return {"status": "deleted", "portal_id": portal_id}
    else:
        raise HTTPException(status_code=404, detail="Session not found")


@app.get("/v1/sessions/{portal_id}")
async def get_session(portal_id: str):
    """Get session statistics."""
    stats = await session_manager.get_stats(portal_id)
    if stats:
        return stats
    else:
        raise HTTPException(status_code=404, detail="Session not found")


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=PORT)
