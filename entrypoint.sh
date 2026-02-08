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

# Generate genesis if node 1 and doesn't exist
# Note: In development mode, main_dev.go uses hardcoded deterministic keys,
# so this file is mostly for config consistency, not validator identity.
if [ "$NODE_ID" = "1" ] && [ ! -f /app/config/genesis.json ]; then
    echo "🔨 Generating genesis file..."
    gen-genesis
else
    echo "✅ Genesis check complete."
fi

# Wait for genesis file (Increased timeout to 30s)
echo "⏳ Waiting for genesis file..."
for i in {1..30}; do
    if [ -f /app/config/genesis.json ]; then
        echo "✅ Genesis file found!"
        break
    fi
    echo "Waiting for genesis.json... ($i/30)"
    sleep 1
done

if [ ! -f /app/config/genesis.json ]; then
    echo "❌ Error: Timed out waiting for genesis.json"
    exit 1
fi

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
    echo "🌐 Node $NODE_ID connecting to bootstrap peers"
fi

# Execute the main binary
exec thrylos "${ARGS[@]}"