#!/bin/bash
set -euo pipefail

# ---------------------------------------------------------------------------
# Thrylos node entrypoint
#
# If BOOTSTRAP_PEERS_FILE is set (and the file exists), read the bootstrap
# peer multiaddr from it rather than relying on a hardcoded BOOTSTRAP_PEERS
# env var. This allows peer IDs to be resolved dynamically at runtime
# (see peer-id-resolver service in docker-compose-testnet.yml).
# ---------------------------------------------------------------------------

if [[ -n "${BOOTSTRAP_PEERS_FILE:-}" && -f "${BOOTSTRAP_PEERS_FILE}" ]]; then
    BOOTSTRAP_PEERS=$(cat "${BOOTSTRAP_PEERS_FILE}" | tr -d '[:space:]')
    export BOOTSTRAP_PEERS
    echo "Bootstrap peers loaded from ${BOOTSTRAP_PEERS_FILE}: ${BOOTSTRAP_PEERS}"
fi

# Hand off to the node binary.
exec thrylos "$@"