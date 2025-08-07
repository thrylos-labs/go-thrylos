#!/bin/bash

# Enhanced debugging script for multi-node setup
set -e

# Kill any existing processes
echo "Stopping any existing nodes..."
pkill -f "thrylos.*--node" 2>/dev/null || true
sleep 2

# Build the binary
echo "Building Thrylos..."
go build -o bin/thrylos ./cmd/thrylos

# Create logs directory
mkdir -p logs

echo "=== Starting Node 1 (Bootstrap) ==="
./bin/thrylos --node=1 --port=9001 > logs/node1.log 2>&1 &
NODE1_PID=$!
echo "Node 1 started with PID: $NODE1_PID"

# Wait longer and check multiple times
echo "Waiting for Node 1 to initialize..."
for i in {1..30}; do
    sleep 1
    if [ -f logs/node1.log ]; then
        echo "=== Node 1 Log Contents (attempt $i) ==="
        tail -n 20 logs/node1.log
        echo "================================="
        
        # Try multiple grep patterns to find the Peer ID
        NODE1_PEER_ID=$(grep -o "Peer ID: [A-Za-z0-9]*" logs/node1.log 2>/dev/null | tail -1 | cut -d' ' -f3 || true)
        
        if [ -z "$NODE1_PEER_ID" ]; then
            # Alternative pattern
            NODE1_PEER_ID=$(grep -o "12D3[A-Za-z0-9]*" logs/node1.log 2>/dev/null | tail -1 || true)
        fi
        
        if [ -n "$NODE1_PEER_ID" ] && [ "$NODE1_PEER_ID" != "ID:" ]; then
            echo "✅ Found Node 1 Peer ID: $NODE1_PEER_ID"
            break
        fi
    fi
    
    if [ $i -eq 30 ]; then
        echo "❌ Failed to get Node 1 Peer ID after 30 seconds"
        echo "=== Full Node 1 Log ==="
        cat logs/node1.log 2>/dev/null || echo "No log file found"
        echo "======================="
        exit 1
    fi
done

# Verify Node 1 is actually listening
echo "Checking if Node 1 is listening on port 9001..."
if ! lsof -i :9001 >/dev/null 2>&1; then
    echo "❌ Node 1 is not listening on port 9001"
    echo "=== Node 1 Log ==="
    cat logs/node1.log
    exit 1
fi

echo "✅ Node 1 is listening on port 9001"

# Start Node 2
echo "=== Starting Node 2 ==="
BOOTSTRAP_ADDR="/ip4/127.0.0.1/tcp/9001/p2p/$NODE1_PEER_ID"
echo "Using bootstrap address: $BOOTSTRAP_ADDR"

./bin/thrylos --node=2 --port=9002 --bootstrap="$BOOTSTRAP_ADDR" > logs/node2.log 2>&1 &
NODE2_PID=$!
echo "Node 2 started with PID: $NODE2_PID"

# Start Node 3  
echo "=== Starting Node 3 ==="
./bin/thrylos --node=3 --port=9003 --bootstrap="$BOOTSTRAP_ADDR" > logs/node3.log 2>&1 &
NODE3_PID=$!
echo "Node 3 started with PID: $NODE3_PID"

echo ""
echo "🎉 All nodes started successfully!"
echo "Node 1 PID: $NODE1_PID (Port: 9001) - Bootstrap"
echo "Node 2 PID: $NODE2_PID (Port: 9002)"
echo "Node 3 PID: $NODE3_PID (Port: 9003)"
echo "Bootstrap Peer ID: $NODE1_PEER_ID"
echo ""

# Wait a bit for connections to establish
echo "Waiting 10 seconds for peer connections..."
sleep 10

echo "=== Connection Status ==="
echo "Node 1 connections:"
grep -i "connected.*peer" logs/node1.log | tail -5 || echo "No connection info found"
echo ""
echo "Node 2 connections:"  
grep -i "connected.*peer" logs/node2.log | tail -5 || echo "No connection info found"
echo ""
echo "Node 3 connections:"
grep -i "connected.*peer" logs/node3.log | tail -5 || echo "No connection info found"
echo ""

echo "=== Real-time Monitoring Commands ==="
echo "Monitor all logs: tail -f logs/*.log"
echo "Monitor Node 1: tail -f logs/node1.log"
echo "Monitor Node 2: tail -f logs/node2.log"  
echo "Monitor Node 3: tail -f logs/node3.log"
echo ""
echo "Check processes: ps aux | grep thrylos"
echo "Check ports: lsof -i :9001-9003"
echo "Stop all nodes: pkill -f 'thrylos.*--node'"
echo ""

# Interactive monitoring
echo "Press Enter to stop all nodes, or Ctrl+C to keep running..."
read

# Cleanup
echo "Stopping all nodes..."
kill $NODE1_PID $NODE2_PID $NODE3_PID 2>/dev/null || true
echo "All nodes stopped."