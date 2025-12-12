#!/bin/bash
# tools/stress/double_sign_test.sh

# ⚠️ WARNING: THIS WILL GET YOUR VALIDATOR SLASHED. USE WITH TEST KEYS ONLY.

set -e

# 1. Build Node
go build -o bin/thrylos cmd/thrylos/main_prod.go

# 2. Generate a shared victim key
echo "🔑 Generating victim validator key..."
./bin/thrylos keygen --out victim.key
VICTIM_ADDR=$(./bin/thrylos inspect-key --file victim.key | grep "Address:" | awk '{print $2}')
echo "Victim Address: $VICTIM_ADDR"

# 3. Start Node A (The "Legitimate" Node)
echo "🚀 Starting Node A (Port 9000)..."
./bin/thrylos \
  --data-dir ./data/node_a \
  --p2p-port 9000 \
  --api-port 8080 \
  --validator \
  --validator-key victim.key \
  --env testnet &
PID_A=$!

sleep 5

# 4. Start Node B (The "Doppelgänger" - Same Key, Different Port)
echo "😈 Starting Node B (Port 9001) - Doppelgänger Attack..."
# It connects to Node A, ensuring they gossip conflicting votes instantly
./bin/thrylos \
  --data-dir ./data/node_b \
  --p2p-port 9001 \
  --api-port 8081 \
  --bootstrap "/ip4/127.0.0.1/tcp/9000/p2p/$(curl -s localhost:8080/api/v1/status | jq -r .p2p.peer_id)" \
  --validator \
  --validator-key victim.key \
  --env testnet &
PID_B=$!

echo "⏳ Waiting for consensus engine to detect double sign..."
sleep 30

# 5. Check Slashing Status
echo "🔍 Checking status..."
STATUS=$(curl -s http://localhost:8080/api/v1/validator/$VICTIM_ADDR)
IS_JAILED=$(echo $STATUS | jq -r .status)

echo "Validator Status: $IS_JAILED"

# Cleanup
kill $PID_A $PID_B
rm -rf ./data/node_a ./data/node_b

if [ "$IS_JAILED" == "jailed" ] || [ "$IS_JAILED" == "slashed" ]; then
    echo "✅ SUCCESS: Validator was successfully slashed for double signing!"
    exit 0
else
    echo "❌ FAILURE: Validator is still active. Slashing logic failed."
    exit 1
fi