#!/bin/bash
set -e

# Configuration
NODE_ID=${HOSTNAME##*-} # Extracts number from "node-1", "node-2"
SHARED_DIR="/app/network-config"

echo "🚀 Starting Node $NODE_ID..."

# --- BOOTNODE (Node 1) LOGIC ---
if [ "$NODE_ID" == "1" ]; then
    if [ ! -f "$SHARED_DIR/genesis.json" ]; then
        echo "⚡ Node 1: Generating network keys and genesis..."
        # Generate keys for 4 validators
        gen-genesis -n 4 -o "$SHARED_DIR/keys" -g "$SHARED_DIR/genesis.json"
        echo "✅ Genesis generation complete."
    else
        echo "✅ Genesis already exists, skipping generation."
    fi
fi

# --- ALL NODES WAIT FOR GENESIS ---
echo "⏳ Waiting for genesis file..."
while [ ! -f "$SHARED_DIR/genesis.json" ]; do
  sleep 1
done

# Copy genesis to local config
cp "$SHARED_DIR/genesis.json" /app/config/genesis.json

# Copy my specific key
KEY_FILE="$SHARED_DIR/keys/validator_$NODE_ID.key"
if [ ! -f "$KEY_FILE" ]; then
    echo "❌ Error: Key file $KEY_FILE not found!"
    exit 1
fi
cp "$KEY_FILE" /app/config/validator.key

# --- STARTUP COMMAND ---
echo "🔥 Launching Thrylos..."

# Base flags for all nodes
FLAGS="--p2p-port 9000 --data /app/data --env development"

if [ "$NODE_ID" == "1" ]; then
    # Node 1 starts as the ONLY active validator to establish the canonical chain
    echo "👑 Node 1 starting as Initial Validator"
    FLAGS="$FLAGS --validator --validator-key /app/config/validator.key"
else
    # Other nodes start as observers (non-validators) to sync first
    echo "👀 Node $NODE_ID starting as Observer"
    
    # Wait for Node 1 to be ready
    while ! nc -z node-1 9000; do   
      sleep 1
    done
fi

# Execute the binary with the constructed flags
exec thrylos $FLAGS