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
if [ "$NODE_ID" = "1" ]; then
    echo "👑 Node $NODE_ID starting as Initial Validator"
else
    echo "🌐 Node $NODE_ID connecting to bootstrap peers: $BOOTSTRAP_PEERS"
fi

# Execute the main binary
exec thrylos "${ARGS[@]}"