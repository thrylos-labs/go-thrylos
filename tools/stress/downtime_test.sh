#!/bin/bash
# tools/stress/downtime_test.sh

set -e

# Config: Set strict downtime rules for testing
# We override the default config to slash quickly (e.g., after 10 missed blocks)
cat > config/stress_test.json <<EOF
{
  "consensus": {
    "max_missed_attestations": 10,
    "block_time": "1s"
  }
}
EOF

echo "🛑 Starting Node A (Validator)..."
./bin/thrylos --config config/stress_test.json --validator --node 1 &
PID=$!

echo "🕒 Waiting for node to sync and validate (10s)..."
sleep 10

echo "🔌 PULLING THE PLUG (Simulating crash)..."
kill -STOP $PID  # Pauses the process effectively simulating a network drop/freeze

echo "zzz... Sleeping for 15 blocks (15s)..."
sleep 15

echo "🔋 RESTARTING (Unpausing)..."
kill -CONT $PID

sleep 5

# Check Jailed Status
STATUS=$(curl -s http://localhost:8080/api/v1/validator/$(./bin/thrylos inspect-key --file config/validator.key | grep Address | awk '{print $2}'))
IS_JAILED=$(echo $STATUS | jq -r .status)

echo "Validator Status after Downtime: $IS_JAILED"

# Cleanup
kill $PID
rm config/stress_test.json

if [ "$IS_JAILED" == "jailed" ]; then
    echo "✅ SUCCESS: Validator was jailed for downtime."
    exit 0
else
    echo "❌ FAILURE: Validator was NOT jailed. Check 'downtime_jail_duration' logic."
    exit 1
fi