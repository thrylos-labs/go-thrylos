// File: cmd/thrylos/main.go
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/node"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// getNodeSpecificPrivateKey generates a deterministic Ed25519 private key for each node
func getNodeSpecificPrivateKey(nodeID int) (crypto.PrivateKey, error) {
	// Different seed for each node
	seedStr := fmt.Sprintf("thrylos-development-node-key-%d-2024", nodeID)

	// Hash the seed to get proper random bytes for Ed25519 seed (32 bytes)
	hash := sha256.Sum256([]byte(seedStr))

	// Ed25519 uses a 32-byte seed
	seed := hash[:]

	// Generate Ed25519 key from deterministic seed
	ed25519PrivKey := ed25519.NewKeyFromSeed(seed)

	// Return the private key using your existing constructor
	return crypto.NewPrivateKeyFromEd25519(ed25519PrivKey), nil
}

// Alternative approach using direct key generation if deterministic isn't needed
func getNodeSpecificPrivateKeyRandom(nodeID int) (crypto.PrivateKey, error) {
	// Generate a random Ed25519 key pair
	_, ed25519PrivKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate Ed25519 key: %v", err)
	}

	// Return the private key using your existing constructor
	return crypto.NewPrivateKeyFromEd25519(ed25519PrivKey), nil
}

// getNodeSpecificPrivateKeyFromBytes creates private key from raw bytes
func getNodeSpecificPrivateKeyFromBytes(nodeID int) (crypto.PrivateKey, error) {
	// Create a node-specific seed
	seedStr := fmt.Sprintf("thrylos-dev-node-key-%d", nodeID)

	// Hash to get 32 bytes for Ed25519 seed
	hash := sha256.Sum256([]byte(seedStr))

	// Create Ed25519 private key from seed
	ed25519PrivKey := ed25519.NewKeyFromSeed(hash[:])

	// Convert to your private key interface using the raw bytes
	return crypto.NewPrivateKeyFromBytes(ed25519PrivKey)
}

// createAllValidators generates all validator info for shared genesis
func createAllValidators(cfg *config.Config) ([]*core.Validator, []crypto.PrivateKey, []string, error) {
	validators := make([]*core.Validator, 0)
	privateKeys := make([]crypto.PrivateKey, 0)
	addresses := make([]string, 0)

	// Create validators for nodes 1, 2, and 3
	for nodeID := 1; nodeID <= 3; nodeID++ {
		// Generate private key for this node
		privateKey, err := getNodeSpecificPrivateKey(nodeID)
		if err != nil {
			// Fallback to bytes method
			privateKey, err = getNodeSpecificPrivateKeyFromBytes(nodeID)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("failed to generate key for node %d: %v", nodeID, err)
			}
		}

		// Generate address
		address, err := account.GenerateAddress(privateKey.PublicKey())
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to generate address for node %d: %v", nodeID, err)
		}

		// Create validator
		validator := &core.Validator{
			Address:        address,
			Pubkey:         privateKey.PublicKey().Bytes(),
			Stake:          cfg.Staking.MinValidatorStake,
			SelfStake:      cfg.Staking.MinValidatorStake,
			DelegatedStake: 0,
			Commission:     0.1,
			Active:         true,
			Delegators:     make(map[string]int64),
			CreatedAt:      time.Now().Unix(),
			UpdatedAt:      time.Now().Unix(),
		}

		validators = append(validators, validator)
		privateKeys = append(privateKeys, privateKey)
		addresses = append(addresses, address)
	}

	return validators, privateKeys, addresses, nil
}

func main() {
	// Command line arguments
	var nodeID = flag.Int("node", 1, "Node ID (1, 2, 3)")
	var port = flag.Int("port", 9000, "P2P listen port")
	var bootstraps = flag.String("bootstrap", "", "Comma-separated bootstrap peers")
	var dataDir = flag.String("data", "", "Data directory (default: ./data-nodeN)")
	var validator = flag.Bool("validator", true, "Run as validator")

	flag.Parse()

	// Validate node ID
	if *nodeID < 1 || *nodeID > 3 {
		log.Fatalf("Node ID must be 1, 2, or 3")
	}

	// Set default data directory if not provided
	if *dataDir == "" {
		*dataDir = fmt.Sprintf("./data-node%d", *nodeID)
	}

	fmt.Printf("🚀 Starting Thrylos Node %d on port %d (Ed25519)...\n", *nodeID, *port)

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Test protobuf generation
	testAccount := &core.Account{
		Address:      fmt.Sprintf("tl1example%d", *nodeID),
		Balance:      1000000000, // 1 THRYLOS
		Nonce:        0,
		StakedAmount: 0,
		DelegatedTo:  make(map[string]int64),
		Rewards:      0,
	}

	fmt.Printf("✅ Protobuf working! Test account: %+v\n", testAccount)

	// Create ALL validators for shared genesis state
	allValidators, allPrivateKeys, allAddresses, err := createAllValidators(cfg)
	if err != nil {
		log.Fatalf("Failed to create validators: %v", err)
	}

	// Get this node's specific private key and address
	nodePrivateKey := allPrivateKeys[*nodeID-1] // Arrays are 0-indexed
	nodeAddress := allAddresses[*nodeID-1]

	fmt.Printf("🔑 Node %d Ed25519 address: %s\n", *nodeID, nodeAddress)
	fmt.Printf("👥 All validator addresses: %v\n", allAddresses)

	// Debug: Print key sizes for Ed25519
	pubKeyBytes := nodePrivateKey.PublicKey().Bytes()
	privKeyBytes := nodePrivateKey.Bytes()
	fmt.Printf("🔐 Ed25519 Key Sizes - PubKey: %d bytes, PrivKey: %d bytes\n",
		len(pubKeyBytes), len(privKeyBytes))

	// Parse bootstrap peers
	var bootstrapPeers []string
	if *bootstraps != "" {
		bootstrapPeers = strings.Split(*bootstraps, ",")
		fmt.Printf("📡 Bootstrap peers: %v\n", bootstrapPeers)
	} else {
		fmt.Printf("📡 No bootstrap peers (this node will be isolated unless discovered)\n")
	}

	// Configure node with P2P support and SHARED genesis validators
	nodeConfig := &node.NodeConfig{
		Config:            cfg,
		PrivateKey:        nodePrivateKey,
		ShardID:           0,
		TotalShards:       1,
		IsValidator:       *validator,
		DataDir:           *dataDir,
		CrossShardEnabled: false,
		GenesisAccount:    nodeAddress,
		GenesisSupply:     1000000000000,

		// P2P Configuration
		EnableP2P:      true,
		P2PListenPort:  *port,
		BootstrapPeers: bootstrapPeers,

		// API Configuration - Use values from config
		EnableAPI: cfg.API.EnableAPI,                   // From config file
		APIPort:   parsePortFromAddr(cfg.API.RESTAddr), // Extract port from ":8080"

		GenesisValidators: allValidators,
	}

	// Initialize node with P2P support
	thrylosNode, err := node.NewNode(nodeConfig)
	if err != nil {
		log.Fatalf("Failed to initialize node: %v", err)
	}

	fmt.Printf("✅ Node %d initialized! Address: %s\n", *nodeID, nodeAddress)

	// Start the node (this will start P2P services automatically)
	if err := thrylosNode.Start(); err != nil {
		log.Fatalf("Failed to start node: %v", err)
	}

	fmt.Printf("✅ Node %d started successfully with Ed25519!\n", *nodeID)
	fmt.Printf("🏛️  Node %d knows about %d validators in genesis\n", *nodeID, len(allValidators))

	// Add basic event handlers for monitoring
	thrylosNode.AddEventHandler("block_produced", func(data interface{}) {
		if block, ok := data.(*core.Block); ok {
			if len(block.Transactions) > 0 {
				fmt.Printf("📦 Node %d - Block #%d: %d transactions, gas: %d (Ed25519)\n",
					*nodeID, block.Header.Index, len(block.Transactions), block.Header.GasUsed)
			}
		}
	})

	thrylosNode.AddEventHandler("transaction_submitted", func(data interface{}) {
		if tx, ok := data.(*core.Transaction); ok {
			// Only log every 10th transaction to avoid spam
			if tx.Nonce%10 == 0 {
				fmt.Printf("💰 Node %d - TX #%d: %s -> %s (%.3f THRYLOS)\n",
					*nodeID, tx.Nonce, tx.From[:8]+"...", tx.To[:8]+"...", float64(tx.Amount)/1000000000)
			}
		}
	})

	// Print initial status
	printNodeStatus(thrylosNode, *nodeID)

	// Graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	fmt.Printf("🎉 Thrylos Node %d running with Ed25519! Press Ctrl+C to stop.\n", *nodeID)
	fmt.Println("📊 Node status will be printed every 30 seconds...")
	fmt.Println("🧪 Run TPS tests with: go test ./node -v -run=TestTPS")

	// Status reporting ticker
	statusTicker := time.NewTicker(30 * time.Second)
	defer statusTicker.Stop()

	for {
		select {
		case <-c:
			fmt.Printf("\n🛑 Shutting down Thrylos Node %d...\n", *nodeID)

			// Stop the node gracefully
			if err := thrylosNode.Stop(); err != nil {
				log.Printf("Error stopping node: %v", err)
			}

			fmt.Println("👋 Goodbye!")
			return

		case <-statusTicker.C:
			printNodeStatus(thrylosNode, *nodeID)
		}
	}
}

func parsePortFromAddr(addr string) int {
	if addr == "" {
		return 8080 // default
	}
	if addr[0] == ':' {
		if port, err := strconv.Atoi(addr[1:]); err == nil {
			return port
		}
	}
	return 8080 // fallback default
}

// printNodeStatus displays comprehensive node status including P2P information
func printNodeStatus(n *node.Node, nodeID int) {
	status := n.GetNodeStatus()

	fmt.Printf("\n📊 === NODE %d STATUS (Ed25519) ===\n", nodeID)
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
			fmt.Printf("⚠️  Node %d: No P2P peers connected\n", nodeID)
		} else {
			fmt.Printf("✅ Node %d: P2P network active\n", nodeID)
		}
	} else {
		fmt.Printf("P2P: disabled or error\n")
	}

	fmt.Printf("Crypto Scheme: Ed25519\n")
	fmt.Println("===================\n")
}
