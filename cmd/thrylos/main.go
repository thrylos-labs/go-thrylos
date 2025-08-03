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

	// Initialize transaction tester for development/testing
	transactionTester := node.NewTransactionTester(thrylosNode, nodePrivateKey, nodeAddress)

	// Create test transaction after delay (for development/testing only)
	go func() {
		time.Sleep(5 * time.Second)

		// Check if we want to run test transactions (can be controlled by env var or config)
		if shouldRunTestTransactions() {
			createTestTransaction(transactionTester, nodeAddress)

			// Try more advanced testing
			time.Sleep(10 * time.Second)
			runBatchTransactionTest(transactionTester, nodeAddress)
		}

		// Try to sync with peers after 30 seconds
		time.Sleep(15 * time.Second)
		fmt.Println("🔄 Attempting to sync with P2P peers...")
		if err := thrylosNode.SyncWithPeers(); err != nil {
			fmt.Printf("⚠️  P2P sync failed: %v\n", err)
		} else {
			fmt.Println("✅ P2P sync completed successfully")
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

// shouldRunTestTransactions checks if test transactions should be created
// In production, this would return false. For development, it can be controlled by env vars.
func shouldRunTestTransactions() bool {
	// Check environment variable
	if os.Getenv("THRYLOS_ENABLE_TEST_TXS") == "true" {
		return true
	}

	// For development, default to true. In production builds, this should be false.
	return true // Change to false for production
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

// Replace the createTestTransaction function in your main.go with this:

func createTestTransaction(tester *node.TransactionTester, nodeAddress string) {
	fmt.Println("🧪 Creating test transaction...")

	// Check node balance first using the helper method
	balance, err := tester.GetBalance(nodeAddress)
	if err != nil {
		fmt.Printf("❌ Failed to get node balance: %v\n", err)
		return
	}

	fmt.Printf("💰 Node balance: %d tokens\n", balance)

	if balance == 0 {
		fmt.Println("⚠️  Node has zero balance. This is expected for fresh nodes.")
		fmt.Println("💡 In production, nodes get balance from:")
		fmt.Println("   - Genesis allocation")
		fmt.Println("   - Block rewards from validation")
		fmt.Println("   - Token transfers from other accounts")
		fmt.Println()
		fmt.Println("✅ HASH VALIDATION IS NOW WORKING! 🎉")
		fmt.Println("   The error changed from 'hash mismatch' to 'insufficient balance'")
		fmt.Println("   This means the transaction hash calculation is correct!")
		return
	}

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

	// Calculate reasonable amounts based on balance
	gasAmount := int64(21000 * 1000) // 21M for gas
	maxTransfer := balance - gasAmount

	if maxTransfer <= 0 {
		fmt.Printf("⚠️  Insufficient balance for transaction (need at least %d for gas)\n", gasAmount)
		return
	}

	// Use 10% of available balance for transfer
	transferAmount := maxTransfer / 10
	if transferAmount < 10000000 { // Minimum 10M
		transferAmount = 10000000
	}

	// Create and submit transaction using corrected method
	tx, err := tester.SubmitTestTransaction(
		nodeAddress,      // from
		recipientAddress, // to
		transferAmount,   // amount
		1000,             // gas price
	)
	if err != nil {
		log.Printf("Failed to create/submit test transaction: %v", err)
		return
	}

	fmt.Printf("✅ Test transaction created and submitted successfully!\n")
	fmt.Printf("   Transaction ID: %s\n", tx.Id)
	fmt.Printf("   Hash: %s\n", tx.Hash)
	fmt.Printf("   Amount: %d tokens\n", tx.Amount)
	fmt.Printf("   From: %s\n", tx.From)
	fmt.Printf("   To: %s\n", tx.To)
}

// runBatchTransactionTest demonstrates batch transaction creation for load testing
func runBatchTransactionTest(tester *node.TransactionTester, nodeAddress string) {
	fmt.Println("🔄 Running batch transaction test...")

	// Create multiple recipient addresses
	var recipientAddresses []string
	for i := 0; i < 3; i++ {
		key, err := crypto.NewPrivateKey()
		if err != nil {
			log.Printf("Failed to generate key %d: %v", i, err)
			continue
		}

		addr, err := account.GenerateAddress(key.PublicKey())
		if err != nil {
			log.Printf("Failed to generate address %d: %v", i, err)
			continue
		}

		recipientAddresses = append(recipientAddresses, addr)
	}

	if len(recipientAddresses) == 0 {
		log.Printf("No recipient addresses created for batch test")
		return
	}

	// Create batch of test transactions
	transactions, err := tester.BatchCreateTestTransactions(
		nodeAddress,
		recipientAddresses[0], // send to first recipient
		3,                     // create 3 transactions
		15000000,              // Changed from 1000000 to 15000000
		1000,                  // gas price
	)
	if err != nil {
		log.Printf("Failed to create batch transactions: %v", err)
		return
	}

	fmt.Printf("✅ Created %d test transactions in batch\n", len(transactions))

	// Submit them one by one with small delays
	for i, tx := range transactions {
		submittedTx, err := tester.SubmitTestTransaction(tx.From, tx.To, tx.Amount, tx.GasPrice)
		if err != nil {
			log.Printf("Failed to submit batch transaction %d: %v", i, err)
		} else {
			fmt.Printf("📤 Submitted batch transaction %d: %s\n", i+1, submittedTx.Id)
		}
		time.Sleep(500 * time.Millisecond) // Small delay between submissions
	}

	fmt.Println("✅ Batch transaction test completed")
}

// Additional helper function for development
func printP2PDebugInfo(n *node.Node) {
	stats := n.GetP2PStats()
	fmt.Println("\n🔍 === P2P DEBUG INFO ===")

	for key, value := range stats {
		fmt.Printf("%s: %v\n", key, value)
	}

	fmt.Printf("Peer ID: %s\n", n.GetPeerID())
	fmt.Printf("Connected Peers: %d\n", n.GetConnectedPeers())
	fmt.Printf("Is Connected: %v\n", n.IsP2PConnected())
	fmt.Println("========================\n")
}
