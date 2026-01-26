# Claude Agent SDK Sidecar
# Uses Claude Code authentication for Pro/Max subscription support

FROM python:3.11-slim

# Install Node.js (required for Claude Code)
RUN apt-get update && apt-get install -y \
    curl \
    && curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y nodejs \
    && rm -rf /var/lib/apt/lists/*

# Install Claude Code CLI
RUN npm install -g @anthropic-ai/claude-code

# Create non-root user for security
RUN useradd -m -u 1000 claude && \
    mkdir -p /home/claude/.claude && \
    chown -R claude:claude /home/claude

WORKDIR /app

# Install Python dependencies
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy application
COPY main.py .

# Security: Run as non-root
USER claude

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8090/health || exit 1

# Expose port
EXPOSE 8090

# Run the sidecar
CMD ["python", "main.py"]
