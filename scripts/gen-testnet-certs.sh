#!/bin/bash
set -euo pipefail

NODES=("node1" "node2" "node3" "node4")
IPS=("172.25.0.10" "172.25.0.11" "172.25.0.12" "172.25.0.13")

for i in "${!NODES[@]}"; do
  NODE="${NODES[$i]}"
  IP="${IPS[$i]}"
  DIR="certs/${NODE}"

  mkdir -p "${DIR}"

  echo "Generating cert for ${NODE} (${IP})..."

  openssl req -x509 -newkey ec \
    -pkeyopt ec_paramgen_curve:P-256 \
    -keyout "${DIR}/server.key" \
    -out "${DIR}/server.crt" \
    -days 365 -nodes \
    -subj "/CN=thrylos-${NODE}" \
    -addext "subjectAltName=IP:${IP},IP:127.0.0.1,DNS:localhost,DNS:${NODE}"

  echo "  -> ${DIR}/server.crt"
  echo "  -> ${DIR}/server.key"
done

echo ""
echo "Done. Certificates written to certs/node{1-4}/."
echo "These files are gitignored and will not be committed."
