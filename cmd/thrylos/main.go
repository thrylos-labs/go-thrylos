// File: cmd/thrylos/main.go
package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cloudflare/circl/sign/mldsa/mldsa44"
	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/node"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// getConsistentNodePrivateKey generates a deterministic private key for development
func getConsistentNodePrivateKey() (crypto.PrivateKey, error) {
	// Use a fixed seed for development (ensures same address every time)
	seed := "thrylos-development-node-key-for-testing-2024"

	// Hash the seed to get proper random bytes
	hash := sha256.Sum256([]byte(seed))

	// Create a deterministic reader from the hash
	reader := bytes.NewReader(hash[:])

	// Generate MLDSA key with deterministic seed
	_, mldsaKey, err := mldsa44.GenerateKey(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate deterministic key: %v", err)
	}

	// Return the private key using your existing constructor
	return crypto.NewPrivateKeyFromMLDSA(mldsaKey), nil
}

// Alternative approach using raw bytes if the above doesn't work:
func getConsistentNodePrivateKeyFromBytes() (crypto.PrivateKey, error) {
	// Create a fixed 32-byte seed
	seed := make([]byte, 32)
	copy(seed, []byte("thrylos-dev-node-consistent-key"))

	// Hash it to ensure proper distribution
	hash := sha256.Sum256(seed)

	// Extend to MLDSA private key size (4864 bytes)
	keyBytes := make([]byte, mldsa44.PrivateKeySize)

	// Fill the key bytes using repeated hashing
	for i := 0; i < len(keyBytes); {
		hash = sha256.Sum256(hash[:])
		copied := copy(keyBytes[i:], hash[:])
		i += copied
	}

	// Create private key from bytes
	return crypto.NewPrivateKeyFromBytes(keyBytes)
}

func main() {
	fmt.Println("🚀 Starting Thrylos Node...")

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Test protobuf generation
	testAccount := &core.Account{
		Address:      "tl1example123",
		Balance:      1000000000, // 1 THRYLOS
		Nonce:        0,
		StakedAmount: 0,
		DelegatedTo:  make(map[string]int64),
		Rewards:      0,
	}

	fmt.Printf("✅ Protobuf working! Test account: %+v\n", testAccount)

	// Generate consistent node private key for development
	nodePrivateKey, err := getConsistentNodePrivateKey()
	if err != nil {
		// Fallback to bytes approach if the first method fails
		nodePrivateKey, err = getConsistentNodePrivateKeyFromBytes()
		if err != nil {
			log.Fatalf("Failed to generate consistent node private key: %v", err)
		}
	}

	// Generate node address
	nodeAddress, err := account.GenerateAddress(nodePrivateKey.PublicKey())
	if err != nil {
		log.Fatalf("Failed to generate node address: %v", err)
	}

	fmt.Printf("🔑 Node address: %s\n", nodeAddress)

	// Configure node with P2P support
	nodeConfig := &node.NodeConfig{
		Config:            cfg,
		PrivateKey:        nodePrivateKey,
		ShardID:           0,
		TotalShards:       1,
		IsValidator:       true,
		DataDir:           "./data",
		CrossShardEnabled: false,
		GenesisAccount:    nodeAddress,
		GenesisSupply:     1000000000000,

		// P2P Configuration
		EnableP2P:      true,
		P2PListenPort:  9000,
		BootstrapPeers: []string{},

		GenesisValidators: []*core.Validator{
			{
				Address:        nodeAddress,
				Pubkey:         nodePrivateKey.PublicKey().Bytes(),
				Stake:          cfg.Staking.MinValidatorStake,
				SelfStake:      cfg.Staking.MinValidatorStake,
				DelegatedStake: 0,
				Commission:     0.1,
				Active:         true,
				Delegators:     make(map[string]int64),
				CreatedAt:      time.Now().Unix(),
				UpdatedAt:      time.Now().Unix(),
			},
		},
	}

	// Initialize node with P2P support
	thrylosNode, err := node.NewNode(nodeConfig)
	if err != nil {
		log.Fatalf("Failed to initialize node: %v", err)
	}

	fmt.Printf("✅ Node initialized! Address: %s\n", nodeAddress)

	// Start the node (this will start P2P services automatically)
	if err := thrylosNode.Start(); err != nil {
		log.Fatalf("Failed to start node: %v", err)
	}

	fmt.Printf("✅ Node started successfully!\n")

	// Add basic event handlers for monitoring
	thrylosNode.AddEventHandler("block_produced", func(data interface{}) {
		if block, ok := data.(*core.Block); ok {
			if len(block.Transactions) > 0 {
				fmt.Printf("📦 Block #%d: %d transactions, gas: %d\n",
					block.Header.Index, len(block.Transactions), block.Header.GasUsed)
			}
		}
	})

	thrylosNode.AddEventHandler("transaction_submitted", func(data interface{}) {
		if tx, ok := data.(*core.Transaction); ok {
			// Only log every 10th transaction to avoid spam
			if tx.Nonce%10 == 0 {
				fmt.Printf("💰 TX #%d: %s -> %s (%.3f THRYLOS)\n",
					tx.Nonce, tx.From[:8]+"...", tx.To[:8]+"...", float64(tx.Amount)/1000000000)
			}
		}
	})

	// Print initial status
	printNodeStatus(thrylosNode)

	// Graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	fmt.Println("🎉 Thrylos Node running! Press Ctrl+C to stop.")
	fmt.Println("📊 Node status will be printed every 30 seconds...")
	fmt.Println("🧪 Run TPS tests with: go test ./node -v -run=TestTPS")

	// Status reporting ticker
	statusTicker := time.NewTicker(30 * time.Second)
	defer statusTicker.Stop()

	for {
		select {
		case <-c:
			fmt.Println("\n🛑 Shutting down Thrylos Node...")

			// Stop the node gracefully
			if err := thrylosNode.Stop(); err != nil {
				log.Printf("Error stopping node: %v", err)
			}

			fmt.Println("👋 Goodbye!")
			return

		case <-statusTicker.C:
			printNodeStatus(thrylosNode)
		}
	}
}

// printNodeStatus displays comprehensive node status including P2P information
func printNodeStatus(n *node.Node) {
	status := n.GetNodeStatus()

	fmt.Println("\n📊 === NODE STATUS ===")
	fmt.Printf("Running: %v\n", status["running"])
	fmt.Printf("Address: %s\n", status["node_address"])
	fmt.Printf("Shard: %d/%d\n", status["shard_id"], status["total_shards"])
	fmt.Printf("Validator: %v\n", status["is_validator"])
	fmt.Printf("Epoch: %d\n", status["last_epoch"])

	// Blockchain stats
	if blockchainStats, ok := status["blockchain"].(map[string]interface{}); ok {
		fmt.Printf("Height: %v\n", blockchainStats["height"])
		fmt.Printf("Pending TXs: %v\n", blockchainStats["pending_transactions"])
	}

	// Consensus stats
	if consensusStats, ok := status["consensus"].(map[string]interface{}); ok {
		fmt.Printf("Blocks Proposed: %v\n", consensusStats["blocks_proposed"])
		fmt.Printf("Attestations: %v\n", consensusStats["attestations_made"])
		fmt.Printf("Active Validators: %v\n", consensusStats["active_validators"])
	}

	// P2P stats with more detail
	if p2pStats, ok := status["p2p"].(map[string]interface{}); ok {
		fmt.Printf("P2P Peer ID: %v\n", p2pStats["peer_id"])
		fmt.Printf("P2P Port: %v\n", p2pStats["listen_port"])
		fmt.Printf("Connected Peers: %v\n", p2pStats["connected_peers"])
		fmt.Printf("P2P Messages Sent: %v\n", p2pStats["messages_sent"])
		fmt.Printf("P2P Messages Received: %v\n", p2pStats["messages_received"])

		// Connection health
		connected := n.IsP2PConnected()
		fmt.Printf("P2P Connected: %v\n", connected)
		if !connected {
			fmt.Printf("⚠️  No P2P peers connected\n")
		}
	} else {
		fmt.Printf("P2P: disabled or error\n")
	}

	fmt.Println("===================\n")
}
