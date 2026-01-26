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
import pty
import re
import secrets
import select
import shutil
import subprocess
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
from claude_agent_sdk import query, ClaudeAgentOptions

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
        # Basic portal_id format check (alphanumeric and common ID chars allowed)
        if not all(c.isalnum() or c in '_-:!.' for c in self.portal_id):
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
    model: str  # Actual model used for this request
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
            except json.JSONDecodeError:
                # Don't log exception details as they may contain credential fragments
                logger.error(f"Invalid credentials JSON format for user {user_id[:20]}...")
                raise ValueError("Invalid credentials JSON format")

            # Create minimal settings.json - Claude CLI requires this
            settings_file = user_dir / "settings.json"
            settings_data = {
                "hasCompletedOnboarding": True,
                "autoUpdaterStatus": "disabled",
                "hasAcknowledgedCostThreshold": True,
            }
            settings_file.write_text(json.dumps(settings_data, indent=2))

            return str(user_dir)

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


# ============================================================================
# OAuth Login Flow (using claude setup-token subprocess)
# ============================================================================

# Pending OAuth flows (state -> {user_id, master_fd, proc, config_dir, created_at})
_oauth_pending: Dict[str, dict] = {}
_oauth_lock = asyncio.Lock()
OAUTH_PENDING_TIMEOUT = 600  # 10 minutes


class OAuthStartRequest(BaseModel):
    user_id: str


class OAuthStartResponse(BaseModel):
    auth_url: str
    state: str


class OAuthCompleteRequest(BaseModel):
    user_id: str
    state: str
    code: str


class OAuthCompleteResponse(BaseModel):
    success: bool
    credentials_json: Optional[str] = None
    message: str


def _run_setup_token_and_get_url(config_dir: str) -> tuple[str, int, any]:
    """
    Run claude setup-token in a PTY and capture the OAuth URL.

    Returns (auth_url, master_fd, process) tuple.
    The master_fd and process are kept alive to later send the auth code.
    """
    # Create pseudo-terminal
    master, slave = pty.openpty()

    # Environment without browser
    env = os.environ.copy()
    env['BROWSER'] = '/bin/false'
    env.pop('DISPLAY', None)
    env['CLAUDE_CONFIG_DIR'] = config_dir

    # Run claude setup-token
    proc = subprocess.Popen(
        ['claude', 'setup-token'],
        stdin=slave,
        stdout=slave,
        stderr=slave,
        preexec_fn=os.setsid,
        env=env
    )

    os.close(slave)

    # Read output until we find the URL and "Paste code" prompt
    output = b''
    start_time = time.time()

    while time.time() - start_time < 30:  # 30 second timeout
        ready, _, _ = select.select([master], [], [], 0.5)
        if ready:
            try:
                data = os.read(master, 4096)
                if data:
                    output += data
            except:
                break

        # Check if we have the URL and prompt
        decoded = output.decode('utf-8', errors='ignore')
        if 'https://claude.ai/oauth/authorize' in decoded and 'Paste code' in decoded:
            break

    # Parse the URL from output
    decoded = output.decode('utf-8', errors='ignore')
    # Remove ANSI escape codes
    clean = re.sub(r'\x1b\[[0-9;]*[a-zA-Z]', '', decoded)
    clean = re.sub(r'\x1b\[\?[0-9]+[a-zA-Z]', '', clean)

    # Find the OAuth URL
    url_match = re.search(r'(https://claude\.ai/oauth/authorize\S+)', clean)
    if not url_match:
        os.close(master)
        proc.terminate()
        raise RuntimeError("Failed to get OAuth URL from claude setup-token")

    auth_url = url_match.group(1)
    return auth_url, master, proc


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

        # Simple test query - MUST consume all messages to avoid cancel scope errors
        got_result = False
        async for message in query(prompt="Say 'OK' and nothing else.", options=options):
            if hasattr(message, 'result'):
                got_result = True
                # Don't break/return - let the generator complete naturally

        if got_result:
            logger.info("Claude Code authentication validated successfully")
            return True

        logger.warning("Auth validation: no result received")
        return False
    except Exception as e:
        logger.error(f"Claude Code authentication failed: {e}")
        return False


@app.get("/health")
async def health():
    """
    Health check endpoint.

    Always returns 200 OK so the sidecar is considered "running".
    The "authenticated" field indicates whether Claude Code auth is valid.
    The "status" field is "healthy" only when authenticated.

    Consumers should check the "authenticated" field to determine if
    the sidecar can actually process requests.
    """
    return {
        "status": "healthy" if _auth_validated else "degraded",
        "sessions": len(session_manager.sessions),
        "authenticated": _auth_validated,
        "message": None if _auth_validated else "Claude Code not authenticated - run 'claude' to authenticate"
    }


@app.get("/metrics")
async def metrics():
    """Prometheus metrics endpoint."""
    return Response(content=generate_latest(), media_type=CONTENT_TYPE_LATEST)


class TestAuthRequest(BaseModel):
    """Request body for auth test endpoint."""
    user_id: str
    credentials_json: str


class TestAuthResponse(BaseModel):
    """Response body for auth test endpoint."""
    success: bool
    message: str


@app.post("/v1/auth/test", response_model=TestAuthResponse)
async def test_auth(request: TestAuthRequest):
    """
    Test user credentials by making a minimal Claude API call.

    This validates that the provided credentials are valid and can
    communicate with Claude before completing the login flow.
    """
    if not request.user_id or not request.credentials_json:
        raise HTTPException(status_code=400, detail="user_id and credentials_json required")

    # Validate credentials JSON format
    try:
        creds = json.loads(request.credentials_json)
        if "claudeAiOauth" not in creds and "access_token" not in creds:
            return TestAuthResponse(
                success=False,
                message="Invalid credentials format: missing authentication data"
            )
    except json.JSONDecodeError as e:
        return TestAuthResponse(success=False, message=f"Invalid JSON: {e}")

    # SECURITY: Use global lock when manipulating CLAUDE_CONFIG_DIR
    async with _env_lock:
        config_dir = None
        original_config_dir = os.environ.get("CLAUDE_CONFIG_DIR")

        try:
            # Set up user credentials
            config_dir = await credentials_manager.setup_credentials(
                request.user_id, request.credentials_json
            )
            os.environ["CLAUDE_CONFIG_DIR"] = config_dir
            logger.info(f"Testing credentials for user {request.user_id[:20]}...")

            # Make a minimal test query
            options = ClaudeAgentOptions(
                allowed_tools=[],  # No tools for test
                permission_mode="bypassPermissions",
                model="haiku",  # Use cheapest/fastest model for test
                max_turns=1,
            )

            # Simple test prompt - consume all messages to avoid cleanup errors
            got_response = False
            async for message in query(prompt="Say 'OK'", options=options):
                if hasattr(message, 'result'):
                    got_response = True
                    # Don't break - let the generator complete naturally

            if got_response:
                logger.info(f"Credentials validated successfully for user {request.user_id[:20]}...")
                return TestAuthResponse(success=True, message="Credentials validated successfully")
            else:
                logger.warning(f"Credentials test failed for user {request.user_id[:20]}... - no response")
                return TestAuthResponse(success=False, message="Authentication failed - no response from Claude")

        except Exception as e:
            logger.error(f"Credentials test failed for user {request.user_id[:20]}...: {e}")
            error_msg = str(e)
            if "authentication" in error_msg.lower() or "unauthorized" in error_msg.lower():
                return TestAuthResponse(success=False, message="Invalid or expired credentials")
            return TestAuthResponse(success=False, message=f"Authentication failed: {error_msg}")
        finally:
            # Restore original CLAUDE_CONFIG_DIR
            if original_config_dir is not None:
                os.environ["CLAUDE_CONFIG_DIR"] = original_config_dir
            elif config_dir is not None and "CLAUDE_CONFIG_DIR" in os.environ:
                del os.environ["CLAUDE_CONFIG_DIR"]


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

            # Determine actual model to use
            actual_model = request.model or MODEL

            # Build options
            options = ClaudeAgentOptions(
                allowed_tools=ALLOWED_TOOLS if ALLOWED_TOOLS else [],
                permission_mode="bypassPermissions",  # No interactive prompts
                model=actual_model,
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
                model=actual_model,
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

                # Determine actual model to use
                actual_model = request.model or MODEL

                options = ClaudeAgentOptions(
                    allowed_tools=ALLOWED_TOOLS if ALLOWED_TOOLS else [],
                    permission_mode="bypassPermissions",
                    model=actual_model,
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
                            yield f"data: {json.dumps({'type': 'session', 'session_id': session.session_id, 'model': actual_model})}\n\n"

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


def _cleanup_oauth_flow(flow_data: dict) -> None:
    """Clean up resources from an OAuth flow (PTY, process, config dir)."""
    # Close PTY master fd
    try:
        os.close(flow_data.get("master_fd", -1))
    except:
        pass
    # Terminate process
    try:
        proc = flow_data.get("proc")
        if proc:
            proc.terminate()
            proc.wait(timeout=2)
    except:
        pass
    # Remove config directory
    try:
        config_dir = flow_data.get("config_dir")
        if config_dir:
            shutil.rmtree(config_dir, ignore_errors=True)
            logger.debug(f"Cleaned up OAuth config dir: {config_dir}")
    except:
        pass


@app.post("/v1/auth/oauth/start", response_model=OAuthStartResponse)
async def oauth_start(request: OAuthStartRequest):
    """
    Start OAuth login flow using claude setup-token subprocess.

    Returns an authorization URL that the user should visit in their browser.
    After authenticating, they'll see a code to paste back to complete login.
    """
    if not request.user_id:
        raise HTTPException(status_code=400, detail="user_id required")

    # Generate state for this flow (used for both CSRF protection and unique directory)
    state = secrets.token_urlsafe(32)

    # Create temp config dir using state (unique per flow, not per user)
    # This allows same user to have multiple concurrent login attempts
    config_dir = str(Path(tempfile.gettempdir()) / f"claude-oauth-{state[:16]}")
    Path(config_dir).mkdir(parents=True, exist_ok=True)

    try:
        # Run claude setup-token and get the OAuth URL
        auth_url, master_fd, proc = _run_setup_token_and_get_url(config_dir)

        # Store pending OAuth flow
        async with _oauth_lock:
            # Clean up expired pending flows
            now = time.time()
            expired_states = [s for s, data in _oauth_pending.items()
                             if now - data["created_at"] > OAUTH_PENDING_TIMEOUT]
            for s in expired_states:
                _cleanup_oauth_flow(data)
                del _oauth_pending[s]

            # Store this flow
            _oauth_pending[state] = {
                "user_id": request.user_id,
                "master_fd": master_fd,
                "proc": proc,
                "config_dir": config_dir,
                "created_at": now,
            }

        logger.info(f"Started OAuth flow for user {request.user_id[:20]}...")
        return OAuthStartResponse(auth_url=auth_url, state=state)

    except Exception as e:
        # Clean up on failure
        shutil.rmtree(config_dir, ignore_errors=True)
        logger.error(f"Failed to start OAuth flow: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to start OAuth: {e}")


@app.post("/v1/auth/oauth/complete", response_model=OAuthCompleteResponse)
async def oauth_complete(request: OAuthCompleteRequest):
    """
    Complete OAuth login flow by sending the code to claude setup-token.

    The code is displayed to the user after they complete authentication in their browser.
    Returns credentials_json that can be used for subsequent requests.
    """
    if not request.user_id or not request.state or not request.code:
        raise HTTPException(status_code=400, detail="user_id, state, and code required")

    # Get and validate pending flow
    async with _oauth_lock:
        flow_data = _oauth_pending.get(request.state)
        if not flow_data:
            return OAuthCompleteResponse(
                success=False,
                message="Invalid or expired OAuth state. Please start the login flow again."
            )

        # Validate user matches - if mismatch, this could be a hijack attempt
        # Clean up and reject to prevent resource leak
        if flow_data["user_id"] != request.user_id:
            _cleanup_oauth_flow(flow_data)
            del _oauth_pending[request.state]
            return OAuthCompleteResponse(
                success=False,
                message="User mismatch. Please start the login flow again."
            )

        # Check expiration
        if time.time() - flow_data["created_at"] > OAUTH_PENDING_TIMEOUT:
            _cleanup_oauth_flow(flow_data)
            del _oauth_pending[request.state]
            return OAuthCompleteResponse(
                success=False,
                message="OAuth flow expired. Please start the login flow again."
            )

        # Remove from pending (we'll handle cleanup after completion)
        del _oauth_pending[request.state]

    master_fd = flow_data["master_fd"]
    proc = flow_data["proc"]
    config_dir = flow_data["config_dir"]

    # Send code to claude setup-token
    try:
        # Write the code to the PTY
        os.write(master_fd, (request.code + "\n").encode())

        # Read output to check for success
        output = b''
        start_time = time.time()
        while time.time() - start_time < 30:  # 30 second timeout
            ready, _, _ = select.select([master_fd], [], [], 0.5)
            if ready:
                try:
                    data = os.read(master_fd, 4096)
                    if data:
                        output += data
                except:
                    break

            # Check if process completed
            if proc.poll() is not None:
                break

            # Check for success indicators
            decoded = output.decode('utf-8', errors='ignore')
            clean = re.sub(r'\x1b\[[0-9;]*[a-zA-Z]', '', decoded)
            if 'success' in clean.lower() or 'authenticated' in clean.lower():
                break
            if 'error' in clean.lower() or 'failed' in clean.lower() or 'invalid' in clean.lower():
                break

        # Close PTY and wait for process
        try:
            os.close(master_fd)
        except:
            pass
        try:
            proc.wait(timeout=5)
        except:
            proc.terminate()

        # Read credentials from the config dir
        creds_file = Path(config_dir) / ".credentials.json"
        if creds_file.exists():
            credentials_json = creds_file.read_text()
            # Validate it's proper JSON
            json.loads(credentials_json)

            # Clean up config directory AFTER reading credentials
            shutil.rmtree(config_dir, ignore_errors=True)
            logger.debug(f"Cleaned up OAuth config dir: {config_dir}")

            logger.info(f"OAuth completed successfully for user {request.user_id[:20]}...")
            return OAuthCompleteResponse(
                success=True,
                credentials_json=credentials_json,
                message="Login successful! Your credentials have been saved."
            )
        else:
            # Clean up config directory on failure too
            shutil.rmtree(config_dir, ignore_errors=True)

            # Parse error from output
            decoded = output.decode('utf-8', errors='ignore')
            clean = re.sub(r'\x1b\[[0-9;]*[a-zA-Z]', '', decoded)
            clean = re.sub(r'\x1b\[\?[0-9]+[a-zA-Z]', '', clean)
            logger.error(f"OAuth failed - no credentials file. Output: {clean[-500:]}")
            return OAuthCompleteResponse(
                success=False,
                message="Authentication failed. Please check your code and try again."
            )

    except Exception as e:
        logger.error(f"OAuth completion error: {e}")
        # Clean up everything on error
        try:
            os.close(master_fd)
        except:
            pass
        try:
            proc.terminate()
        except:
            pass
        shutil.rmtree(config_dir, ignore_errors=True)
        return OAuthCompleteResponse(
            success=False,
            message=f"Authentication error: {e}"
        )


@app.delete("/v1/users/{user_id}")
async def delete_user(user_id: str):
    """
    Remove all stored credentials for a user (logout).

    This cleans up the user's credential directory used for Pro/Max authentication.
    Should be called when a user logs out from the bridge.
    """
    try:
        await credentials_manager.cleanup_user(user_id)
        logger.info(f"Cleaned up credentials for user: {user_id}")
        return {"status": "deleted", "user_id": user_id}
    except Exception as e:
        logger.error(f"Failed to cleanup user {user_id}: {e}")
        raise HTTPException(status_code=500, detail=f"Failed to cleanup user: {str(e)}")


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=PORT)
