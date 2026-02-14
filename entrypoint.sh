#!/bin/bash
set -e

echo "🚀 Starting Node ${NODE_ID:-1}..."

# Set defaults
DATA_DIR="${DATA_DIR:-/app/data}"
NODE_ID="${NODE_ID:-1}"
P2P_PORT="${P2P_PORT:-9000}"
BOOTSTRAP_PEERS="${BOOTSTRAP_PEERS:-}"

# Create directories
mkdir -p "$DATA_DIR" /app/config /app/keys

# Build command line arguments for dev mode
ARGS=(
    "-node=$NODE_ID"
    "-data=$DATA_DIR"
    "-p2p-port=$P2P_PORT"
    "-validator"
)

if [ -n "$BOOTSTRAP_PEERS" ]; then
    ARGS+=("-bootstrap=$BOOTSTRAP_PEERS")
fi

echo "🔥 Launching Thrylos..."
# If not node-1, wait for P2P port 9000 on node-1 to be reachable before starting
if [ "$NODE_ID" != "1" ]; then
    echo "⏳ Waiting for node-1 P2P..."
    while ! nc -z 172.25.0.10 9000; do
      sleep 1
    done
    echo "✅ Node-1 P2P reachable"
fi

exec thrylos "${ARGS[@]}"