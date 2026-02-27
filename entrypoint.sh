#!/bin/bash
set -euo pipefail

# Set defaults
DATA_DIR="${DATA_DIR:-/app/data}"
NODE_ID="${NODE_ID:-1}"
P2P_PORT="${P2P_PORT:-9000}"
BOOTSTRAP_PEERS="${BOOTSTRAP_PEERS:-}"

# Create directories
mkdir -p "$DATA_DIR" /app/config /app/keys

# FIND-04: If BOOTSTRAP_PEERS_FILE is set and exists, read the bootstrap
# peer multiaddr from it rather than relying on a hardcoded BOOTSTRAP_PEERS.
if [[ -n "${BOOTSTRAP_PEERS_FILE:-}" && -f "${BOOTSTRAP_PEERS_FILE}" ]]; then
    BOOTSTRAP_PEERS=$(cat "${BOOTSTRAP_PEERS_FILE}" | tr -d '[:space:]')
    export BOOTSTRAP_PEERS
    echo "Bootstrap peers loaded from ${BOOTSTRAP_PEERS_FILE}: ${BOOTSTRAP_PEERS}"
fi

# Build command line arguments
ARGS=(
    "-node=${NODE_ID}"
    "-data=${DATA_DIR}"
    "-p2p-port=${P2P_PORT}"
)

if [[ -n "${BOOTSTRAP_PEERS}" ]]; then
    ARGS+=("-bootstrap=${BOOTSTRAP_PEERS}")
fi

echo "🚀 Starting Node ${NODE_ID}..."
exec thrylos "${ARGS[@]}"
