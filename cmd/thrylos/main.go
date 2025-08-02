package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/node"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// Update your main.go to include the P2P configuration fields:

func main() {
	fmt.Println("🚀 Starting Thrylos V2...")

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

	// Generate node private key
	nodePrivateKey, err := crypto.NewPrivateKey()
	if err != nil {
		log.Fatalf("Failed to generate node private key: %v", err)
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
		BootstrapPeers: []string{
			// Add bootstrap peer addresses here if you have them
			// Example: "/ip4/127.0.0.1/tcp/9001/p2p/12D3KooW..."
		},

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

	// Add P2P-specific event handlers
	thrylosNode.AddEventHandler("block_produced", func(data interface{}) {
		if block, ok := data.(*core.Block); ok {
			fmt.Printf("📦 Block produced and broadcasted: #%d with %d transactions\n",
				block.Header.Index, len(block.Transactions))
		}
	})

	thrylosNode.AddEventHandler("transaction_submitted", func(data interface{}) {
		if tx, ok := data.(*core.Transaction); ok {
			fmt.Printf("💰 Transaction submitted and broadcasted: %s -> %s (amount: %d)\n",
				tx.From, tx.To, tx.Amount)
		}
	})

	thrylosNode.AddEventHandler("validator_registered", func(data interface{}) {
		if validator, ok := data.(*core.Validator); ok {
			fmt.Printf("👑 Validator registered: %s (stake: %d)\n",
				validator.Address, validator.Stake)
		}
	})

	// Print initial status with P2P info
	printNodeStatus(thrylosNode)

	// Create test transaction after delay
	go func() {
		time.Sleep(5 * time.Second)
		createTestTransaction(thrylosNode, nodeAddress)

		// Try to sync with peers after 30 seconds
		time.Sleep(25 * time.Second)
		fmt.Println("🔄 Attempting to sync with P2P peers...")
		if err := thrylosNode.SyncWithPeers(); err != nil {
			fmt.Printf("⚠️  P2P sync failed: %v\n", err)
		}
	}()

	// Graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	fmt.Println("🎉 Go Thrylos node running! Press Ctrl+C to stop.")
	fmt.Println("📊 Node status will be printed every 30 seconds...")

	// Status reporting ticker
	statusTicker := time.NewTicker(30 * time.Second)
	defer statusTicker.Stop()

	for {
		select {
		case <-c:
			fmt.Println("\n🛑 Shutting down Thrylos V2...")

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

// Update printNodeStatus to include P2P information:
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

	// P2P stats
	if p2pStats, ok := status["p2p"].(map[string]interface{}); ok {
		fmt.Printf("P2P Peer ID: %v\n", p2pStats["peer_id"])
		fmt.Printf("P2P Port: %v\n", p2pStats["listen_port"])
		fmt.Printf("Connected Peers: %v\n", p2pStats["connected_peers"])
		fmt.Printf("P2P Connected: %v\n", status["p2p_connected"])
	} else {
		fmt.Printf("P2P: %v\n", status["p2p"])
	}

	fmt.Println("===================\n")
}

// createTestTransaction creates a test transaction to demonstrate functionality
func createTestTransaction(n *node.Node, nodeAddress string) {
	fmt.Println("🧪 Creating test transaction...")

	// Create recipient address
	recipientKey, err := crypto.NewPrivateKey()
	if err != nil {
		log.Printf("Failed to generate recipient key: %v", err)
		return
	}

	recipientAddress, err := account.GenerateAddress(recipientKey.PublicKey())
	if err != nil {
		log.Printf("Failed to generate recipient address: %v", err)
		return
	}

	// Create transaction with sufficient amount
	tx, err := n.CreateTransaction(
		nodeAddress,      // from
		recipientAddress, // to
		10000000,         // amount (10 THRYLOS - meets minimum)
		1000,             // gas price
	)
	if err != nil {
		log.Printf("Failed to create test transaction: %v", err)
		return
	}

	// Submit transaction (this will also broadcast via P2P)
	if err := n.SubmitTransaction(tx); err != nil {
		log.Printf("Failed to submit test transaction: %v", err)
		return
	}

	fmt.Printf("✅ Test transaction created and broadcasted: %s -> %s (amount: %d)\n",
		tx.From, tx.To, tx.Amount)
}
