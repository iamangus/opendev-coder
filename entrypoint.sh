#!/bin/sh
set -e

# ---------------------------------------------------------------------------
# Required
# ---------------------------------------------------------------------------
if [ -z "$REPO_URL" ]; then
    echo "error: REPO_URL environment variable is required" >&2
    exit 1
fi

# ---------------------------------------------------------------------------
# Optionally embed a token for private repositories.
# Supports HTTPS URLs (http:// or https://).
# Example: https://github.com/user/repo.git  →  https://TOKEN@github.com/user/repo.git
# ---------------------------------------------------------------------------
CLONE_URL="$REPO_URL"
if [ -n "$GIT_TOKEN" ]; then
    CLONE_URL=$(printf '%s' "$REPO_URL" | sed 's|^\(https\?://\)|\1'"$GIT_TOKEN"'@|')
fi

# ---------------------------------------------------------------------------
# Clone
# ---------------------------------------------------------------------------
CLONE_DIR="/repo"

echo "Cloning repository..."
if [ -n "$REPO_BRANCH" ]; then
    git clone --depth 1 --branch "$REPO_BRANCH" --single-branch "$CLONE_URL" "$CLONE_DIR"
else
    git clone --depth 1 "$CLONE_URL" "$CLONE_DIR"
fi
echo "Clone complete."

# ---------------------------------------------------------------------------
# Start the MCP server
# ---------------------------------------------------------------------------
MCP_MODE="${MCP_MODE:-http}"
MCP_ADDR="${MCP_ADDR:-:8080}"

exec /usr/local/bin/code-mcp \
    --dir  "$CLONE_DIR" \
    --mode "$MCP_MODE" \
    --addr "$MCP_ADDR"
