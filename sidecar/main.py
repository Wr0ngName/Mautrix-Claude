#!/usr/bin/env python3
"""
Claude Agent SDK Sidecar for mautrix-claude bridge.

Provides HTTP API for Go bridge to communicate with Claude using Pro/Max subscription.
"""

import asyncio
import hashlib
import json
import logging
import os
import shutil
import tempfile
import time
import uuid
from contextlib import asynccontextmanager
from dataclasses import dataclass, field
from pathlib import Path
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

# SECURITY: Whitelist of safe tools that are allowed in multi-user chat
# NEVER add file access, bash, or code editing tools here
SAFE_TOOLS_WHITELIST = frozenset([
    "WebSearch",
    "WebFetch",
    "AskUserQuestion",
])

# SECURITY: Tools that must NEVER be enabled (file access, code execution)
DANGEROUS_TOOLS = frozenset([
    "Bash", "Read", "Write", "Edit", "MultiEdit",
    "Glob", "Grep", "LS", "NotebookEdit",
    "Task", "TodoWrite", "TodoRead",
])

def validate_tools(tools: list[str]) -> list[str]:
    """Validate and filter tool list against whitelist. SECURITY-CRITICAL."""
    if not tools:
        return []
    validated = []
    for tool in tools:
        tool = tool.strip()
        if tool in DANGEROUS_TOOLS:
            logger.warning(f"SECURITY: Blocked attempt to enable dangerous tool: {tool}")
            continue
        if tool not in SAFE_TOOLS_WHITELIST:
            logger.warning(f"SECURITY: Ignoring unknown tool not in whitelist: {tool}")
            continue
        validated.append(tool)
    return validated

# Parse and validate allowed tools from environment
_raw_tools = os.getenv("CLAUDE_SIDECAR_ALLOWED_TOOLS", "WebSearch,WebFetch,AskUserQuestion").split(",")
ALLOWED_TOOLS = validate_tools(_raw_tools)

SYSTEM_PROMPT = os.getenv("CLAUDE_SIDECAR_SYSTEM_PROMPT", "You are a helpful AI assistant.")
MODEL = os.getenv("CLAUDE_SIDECAR_MODEL", "sonnet")
SESSION_TIMEOUT = int(os.getenv("CLAUDE_SIDECAR_SESSION_TIMEOUT", "3600"))  # 1 hour

# Input validation limits
MAX_MESSAGE_LENGTH = 100000  # ~100k chars, matches Go bridge limit
MAX_PORTAL_ID_LENGTH = 256   # Reasonable limit for portal IDs

# Prometheus metrics
REQUESTS_TOTAL = Counter('claude_sidecar_requests_total', 'Total requests', ['endpoint', 'status'])
REQUEST_DURATION = Histogram('claude_sidecar_request_duration_seconds', 'Request duration')
ACTIVE_SESSIONS = Gauge('claude_sidecar_active_sessions', 'Number of active sessions')
TOKENS_USED = Counter('claude_sidecar_tokens_total', 'Total tokens used', ['type'])

# Track auth status globally
_auth_validated = False


@asynccontextmanager
async def lifespan(app: FastAPI):
    """Lifespan context manager for startup and shutdown events."""
    global _auth_validated

    # Startup
    await session_manager.start()
    logger.info(f"Claude sidecar starting on port {PORT}")
    logger.info(f"Allowed tools: {ALLOWED_TOOLS or 'none (chat only)'}")
    logger.info(f"Model: {MODEL}")

    # Validate Claude Code authentication
    _auth_validated = await validate_claude_auth()
    if not _auth_validated:
        logger.error("WARNING: Claude Code is not authenticated!")
        logger.error("Run 'claude' to authenticate before using sidecar mode")
    else:
        logger.info("Claude sidecar ready")

    yield

    # Shutdown
    await session_manager.stop()
    await credentials_manager.cleanup_all()
    logger.info("Claude sidecar stopped")


# FastAPI app
app = FastAPI(
    title="Claude Agent SDK Sidecar",
    description="HTTP API for mautrix-claude bridge",
    version="1.0.0",
    lifespan=lifespan
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
    user_id: Optional[str] = None  # Matrix user ID for per-user sessions
    credentials_json: Optional[str] = None  # User's Claude credentials JSON
    message: str
    system_prompt: Optional[str] = None
    model: Optional[str] = None
    stream: bool = False

    def validate_input(self) -> None:
        """Validate input fields. Raises HTTPException on invalid input."""
        # Validate portal_id
        if not self.portal_id or len(self.portal_id) > MAX_PORTAL_ID_LENGTH:
            raise HTTPException(
                status_code=400,
                detail=f"Invalid portal_id: must be 1-{MAX_PORTAL_ID_LENGTH} characters"
            )
        # Basic portal_id format check (alphanumeric, underscore, dash, colon allowed)
        if not all(c.isalnum() or c in '_-:!' for c in self.portal_id):
            raise HTTPException(
                status_code=400,
                detail="Invalid portal_id: contains invalid characters"
            )
        # Validate message
        if not self.message:
            raise HTTPException(status_code=400, detail="Message cannot be empty")
        if len(self.message) > MAX_MESSAGE_LENGTH:
            raise HTTPException(
                status_code=400,
                detail=f"Message too long: {len(self.message)} chars (max {MAX_MESSAGE_LENGTH})"
            )


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


class CredentialsManager:
    """
    Manages per-user Claude credentials.

    Creates temporary directories with user credentials and provides
    the config directory path to use for Claude SDK queries.
    """

    def __init__(self, base_dir: Optional[str] = None):
        """Initialize credentials manager with base temp directory."""
        self._base_dir = Path(base_dir) if base_dir else Path(tempfile.gettempdir()) / "claude-creds"
        self._base_dir.mkdir(parents=True, exist_ok=True)
        self._lock = asyncio.Lock()
        logger.info(f"Credentials manager initialized at {self._base_dir}")

    def _get_user_dir(self, user_id: str) -> Path:
        """Get the credentials directory for a user (hashed for privacy)."""
        # Hash user ID to avoid path issues with special chars in Matrix IDs
        user_hash = hashlib.sha256(user_id.encode()).hexdigest()[:16]
        return self._base_dir / user_hash

    async def setup_credentials(self, user_id: str, credentials_json: str) -> str:
        """
        Set up credentials for a user and return the config directory path.

        Args:
            user_id: Matrix user ID
            credentials_json: JSON string of Claude credentials

        Returns:
            Path to the config directory to use for CLAUDE_CONFIG_DIR
        """
        async with self._lock:
            user_dir = self._get_user_dir(user_id)
            user_dir.mkdir(parents=True, exist_ok=True)

            # Write credentials file
            creds_file = user_dir / ".credentials.json"
            try:
                # Validate JSON before writing
                creds_data = json.loads(credentials_json)
                creds_file.write_text(json.dumps(creds_data, indent=2))
                logger.debug(f"Set up credentials for user {user_id[:20]}...")
                return str(user_dir)
            except json.JSONDecodeError as e:
                logger.error(f"Invalid credentials JSON for user {user_id}: {e}")
                raise ValueError(f"Invalid credentials JSON: {e}")

    async def cleanup_user(self, user_id: str) -> None:
        """Remove credentials for a user."""
        async with self._lock:
            user_dir = self._get_user_dir(user_id)
            if user_dir.exists():
                shutil.rmtree(user_dir, ignore_errors=True)
                logger.debug(f"Cleaned up credentials for user {user_id[:20]}...")

    async def cleanup_all(self) -> None:
        """Remove all cached credentials."""
        async with self._lock:
            if self._base_dir.exists():
                shutil.rmtree(self._base_dir, ignore_errors=True)
                self._base_dir.mkdir(parents=True, exist_ok=True)
                logger.info("Cleaned up all cached credentials")


# Global credentials manager
credentials_manager = CredentialsManager()

# Global lock for CLAUDE_CONFIG_DIR manipulation
# SECURITY: This prevents race conditions where concurrent requests could
# cause User A's query to run with User B's credentials
_env_lock = asyncio.Lock()


async def validate_claude_auth() -> bool:
    """Validate that Claude Code is authenticated by making a test query."""
    try:
        logger.info("Validating Claude Code authentication...")
        options = ClaudeAgentOptions(
            allowed_tools=[],  # No tools for validation
            permission_mode="bypassPermissions",
            model=MODEL,
            max_turns=1,  # Single turn only
        )

        # Simple test query
        async for message in query(prompt="Say 'OK' and nothing else.", options=options):
            if hasattr(message, 'result'):
                logger.info("Claude Code authentication validated successfully")
                return True

        logger.warning("Auth validation: no result received")
        return False
    except Exception as e:
        logger.error(f"Claude Code authentication failed: {e}")
        return False


@app.get("/health")
async def health():
    """Health check endpoint."""
    status = "healthy" if _auth_validated else "unhealthy"
    return {
        "status": status,
        "sessions": len(session_manager.sessions),
        "authenticated": _auth_validated
    }


@app.get("/metrics")
async def metrics():
    """Prometheus metrics endpoint."""
    return Response(content=generate_latest(), media_type=CONTENT_TYPE_LATEST)


@app.post("/v1/chat", response_model=ChatResponse)
async def chat(request: ChatRequest):
    """
    Send a message to Claude and get a response.

    Maintains conversation context per portal_id.
    Supports per-user credentials via user_id and credentials_json.
    """
    start_time = time.time()

    # Validate input before processing
    request.validate_input()

    # Get or create session for this portal (outside lock - session manager has own lock)
    session = await session_manager.get_or_create(request.portal_id)

    # SECURITY: Use global lock when manipulating CLAUDE_CONFIG_DIR to prevent
    # race conditions that could cause credential leakage between users.
    # This serializes requests with per-user credentials (performance trade-off for security).
    async with _env_lock:
        config_dir = None
        original_config_dir = os.environ.get("CLAUDE_CONFIG_DIR")

        try:
            if request.user_id and request.credentials_json:
                try:
                    config_dir = await credentials_manager.setup_credentials(
                        request.user_id, request.credentials_json
                    )
                    os.environ["CLAUDE_CONFIG_DIR"] = config_dir
                    logger.debug(f"Using per-user credentials from {config_dir}")
                except ValueError as e:
                    raise HTTPException(status_code=400, detail=str(e))

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

        except HTTPException:
            # Re-raise HTTP exceptions (validation errors, etc.)
            raise
        except Exception as e:
            # Log full error but don't expose details to client (security)
            logger.error(f"Error processing chat request for portal {request.portal_id}: {e}", exc_info=True)
            REQUESTS_TOTAL.labels(endpoint='/v1/chat', status='error').inc()
            raise HTTPException(status_code=500, detail="Internal error processing request")
        finally:
            # Restore original CLAUDE_CONFIG_DIR
            if original_config_dir is not None:
                os.environ["CLAUDE_CONFIG_DIR"] = original_config_dir
            elif config_dir is not None and "CLAUDE_CONFIG_DIR" in os.environ:
                del os.environ["CLAUDE_CONFIG_DIR"]


@app.post("/v1/chat/stream")
async def chat_stream(request: ChatRequest):
    """
    Send a message to Claude and stream the response.

    Returns Server-Sent Events (SSE) stream.
    Supports per-user credentials via user_id and credentials_json.
    """
    # Validate input before processing
    request.validate_input()

    # Get or create session for this portal (outside lock - session manager has own lock)
    session = await session_manager.get_or_create(request.portal_id)

    async def generate() -> AsyncIterator[str]:
        # SECURITY: Use global lock when manipulating CLAUDE_CONFIG_DIR
        async with _env_lock:
            config_dir = None
            original_config_dir = os.environ.get("CLAUDE_CONFIG_DIR")

            try:
                if request.user_id and request.credentials_json:
                    try:
                        config_dir = await credentials_manager.setup_credentials(
                            request.user_id, request.credentials_json
                        )
                        os.environ["CLAUDE_CONFIG_DIR"] = config_dir
                        logger.debug(f"Using per-user credentials from {config_dir}")
                    except ValueError as e:
                        yield f"data: {json.dumps({'type': 'error', 'message': str(e)})}\n\n"
                        return

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
                                    yield f"data: {json.dumps({'type': 'text', 'content': block.text})}\n\n"

                    # Stream final result
                    if hasattr(message, 'result'):
                        yield f"data: {json.dumps({'type': 'result', 'content': message.result})}\n\n"

                session.message_count += 1
                session.last_used = time.time()

                yield "data: {\"type\": \"done\"}\n\n"

            except Exception as e:
                # Log full error but don't expose details to client (security)
                logger.error(f"Error in stream for portal {request.portal_id}: {e}", exc_info=True)
                yield f"data: {json.dumps({'type': 'error', 'message': 'Internal error processing request'})}\n\n"
            finally:
                # Restore original CLAUDE_CONFIG_DIR
                if original_config_dir is not None:
                    os.environ["CLAUDE_CONFIG_DIR"] = original_config_dir
                elif config_dir is not None and "CLAUDE_CONFIG_DIR" in os.environ:
                    del os.environ["CLAUDE_CONFIG_DIR"]

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
