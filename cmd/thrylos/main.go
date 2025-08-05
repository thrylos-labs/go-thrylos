package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
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
	fmt.Println("🚀 Starting Thrylos TPS Testing Node...")

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

	// Initialize TPS transaction tester (MOVED UP)
	tpsTester := node.NewTPSTransactionTester(thrylosNode, nodePrivateKey, nodeAddress)

	// NOW we can test transaction creation (MOVED AFTER tpsTester initialization)
	fmt.Println("🧪 Testing transaction creation fix...")
	tpsTester.DebugTransactionCreation()

	// Add event handlers for TPS testing
	thrylosNode.AddEventHandler("block_produced", func(data interface{}) {
		if block, ok := data.(*core.Block); ok {
			if len(block.Transactions) > 0 {
				fmt.Printf("📦 Block #%d: %d transactions, gas: %d\n",
					block.Header.Index, len(block.Transactions), block.Header.GasUsed)
			}
		}
	})

	thrylosNode.AddEventHandler("transaction_submitted", func(data interface{}) {
		// Only log every 10th transaction to avoid spam
		if tx, ok := data.(*core.Transaction); ok {
			// Extract nonce from transaction to mod by 10
			if tx.Nonce%10 == 0 {
				fmt.Printf("💰 TX #%d: %s -> %s (%.3f THRYLOS)\n",
					tx.Nonce, tx.From[:8]+"...", tx.To[:8]+"...", float64(tx.Amount)/1000000000)
			}
		}
	})

	// Print initial status
	printNodeStatus(thrylosNode)

	// Check if we should run TPS tests
	if shouldRunTPSTests() {
		// Start TPS testing after a brief delay
		go func() {
			time.Sleep(10 * time.Second) // Allow node to stabilize

			// Check balance before testing
			balance, err := tpsTester.GetBalance(nodeAddress)
			if err != nil {
				fmt.Printf("❌ Failed to get balance for TPS testing: %v\n", err)
				return
			}

			fmt.Printf("💰 Balance available for TPS testing: %d tokens (%.3f THRYLOS)\n",
				balance, float64(balance)/1000000000)

			if balance < 1000000000 { // Less than 1 THRYLOS
				fmt.Printf("⚠️  Insufficient balance for meaningful TPS testing\n")
				return
			}

			// Run TPS tests based on environment variables or defaults
			runTPSTestSuite(tpsTester)
		}()
	}

	// Graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	fmt.Println("🎉 Go Thrylos TPS Testing Node running! Press Ctrl+C to stop.")
	fmt.Println("📊 Node status will be printed every 30 seconds...")
	fmt.Println("🧪 TPS testing will begin in 10 seconds...")

	// Status reporting ticker
	statusTicker := time.NewTicker(30 * time.Second)
	defer statusTicker.Stop()

	for {
		select {
		case <-c:
			fmt.Println("\n🛑 Shutting down Thrylos TPS Testing Node...")

			// Print final TPS test results
			results := tpsTester.GetTestResults()
			if len(results) > 0 {
				fmt.Printf("\n📊 FINAL TPS TEST SUMMARY\n")
				fmt.Printf("Total tests completed: %d\n", len(results))

				var totalTxs int64
				var bestTPS float64

				for _, result := range results {
					totalTxs += result.TotalTransactions
					if result.AverageTPS > bestTPS {
						bestTPS = result.AverageTPS
					}
				}

				fmt.Printf("Total transactions processed: %d\n", totalTxs)
				fmt.Printf("Best TPS achieved: %.2f\n", bestTPS)
			}

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

// shouldRunTPSTests checks if TPS tests should be run
func shouldRunTPSTests() bool {
	// Check environment variable
	if os.Getenv("THRYLOS_ENABLE_TPS_TESTS") == "false" {
		return false
	}

	// Default to true for TPS testing build
	return true
}

// runTPSTestSuite runs a comprehensive suite of TPS tests
func runTPSTestSuite(tester *node.TPSTransactionTester) {
	fmt.Printf("\n🧪 === STARTING TPS TEST SUITE ===\n")

	// Test 1: Quick validation test
	fmt.Printf("\n1️⃣ Running quick validation test...\n")
	quickConfig := node.DefaultTPSTestConfig()
	quickConfig.TestName = "Quick Validation"
	quickConfig.Duration = 30 * time.Second
	quickConfig.TargetTPS = 5
	quickConfig.WarmupDuration = 5 * time.Second
	quickConfig.TransactionAmount = 15000000 // 0.015 THRYLOS - above minimum

	result, err := tester.RunTPSTest(quickConfig)
	if err != nil {
		fmt.Printf("❌ Quick test failed: %v\n", err)
		return
	}

	if result.SuccessfulTxs < 10 { // Lower threshold since we fixed the issue
		fmt.Printf("⚠️  Low transaction success rate, stopping tests\n")
		return
	}

	// Test 2: Standard TPS test
	fmt.Printf("\n2️⃣ Running standard TPS test...\n")
	standardConfig := node.DefaultTPSTestConfig()
	standardConfig.TestName = "Standard TPS Test"
	standardConfig.Duration = 60 * time.Second
	standardConfig.TargetTPS = getTargetTPS("THRYLOS_TARGET_TPS", 10)
	standardConfig.WarmupDuration = 10 * time.Second
	standardConfig.TransactionAmount = 15000000 // Valid amount

	_, err = tester.RunTPSTest(standardConfig)
	if err != nil {
		fmt.Printf("❌ Standard test failed: %v\n", err)
	}

	// Test 3: Stress test (optional)
	if os.Getenv("THRYLOS_RUN_STRESS_TEST") == "true" {
		fmt.Printf("\n3️⃣ Running stress test...\n")
		maxTPS := getTargetTPS("THRYLOS_MAX_TPS", 50)
		stepSize := getTargetTPS("THRYLOS_STEP_SIZE", 5)
		stepDuration := getDuration("THRYLOS_STEP_DURATION", 30*time.Second)

		_, err = tester.RunStressTest(maxTPS, stepSize, stepDuration)
		if err != nil {
			fmt.Printf("❌ Stress test failed: %v\n", err)
		}
	}

	// Test 4: Sustained load test (optional)
	if os.Getenv("THRYLOS_RUN_SUSTAINED_TEST") == "true" {
		fmt.Printf("\n4️⃣ Running sustained load test...\n")
		sustainedTPS := getTargetTPS("THRYLOS_SUSTAINED_TPS", 15)
		sustainedDuration := getDuration("THRYLOS_SUSTAINED_DURATION", 300*time.Second)

		_, err = tester.RunSustainedLoadTest(sustainedTPS, sustainedDuration)
		if err != nil {
			fmt.Printf("❌ Sustained test failed: %v\n", err)
		}
	}

	fmt.Printf("\n✅ TPS TEST SUITE COMPLETED\n")

	// Print summary of all tests
	results := tester.GetTestResults()
	printTestSuiteSummary(results)
}

// Helper functions for environment variable parsing
func getTargetTPS(envVar string, defaultValue int) int {
	if val := os.Getenv(envVar); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultValue
}

func getDuration(envVar string, defaultValue time.Duration) time.Duration {
	if val := os.Getenv(envVar); val != "" {
		if parsed, err := time.ParseDuration(val); err == nil {
			return parsed
		}
	}
	return defaultValue
}

func printTestSuiteSummary(results []node.TPSTestResult) {
	if len(results) == 0 {
		return
	}

	fmt.Printf("\n📊 === TPS TEST SUITE SUMMARY ===\n")
	fmt.Printf("Tests completed: %d\n", len(results))

	var totalTxs int64
	var totalSuccessful int64
	var bestTPS float64
	var bestLatency time.Duration = time.Hour // Start with high value
	var totalDuration time.Duration

	for i, result := range results {
		fmt.Printf("\n%d. %s:\n", i+1, result.TestName)
		fmt.Printf("   TPS: %.2f (Peak: %.2f)\n", result.AverageTPS, result.PeakTPS)
		fmt.Printf("   Success: %d/%d (%.1f%%)\n",
			result.SuccessfulTxs, result.TotalTransactions,
			float64(result.SuccessfulTxs)/float64(result.TotalTransactions)*100)
		fmt.Printf("   Latency: %v (Max: %v)\n",
			result.AverageLatency.Truncate(time.Millisecond),
			result.MaxLatency.Truncate(time.Millisecond))

		totalTxs += result.TotalTransactions
		totalSuccessful += result.SuccessfulTxs
		totalDuration += result.Duration

		if result.AverageTPS > bestTPS {
			bestTPS = result.AverageTPS
		}

		if result.AverageLatency < bestLatency {
			bestLatency = result.AverageLatency
		}
	}

	fmt.Printf("\n🏆 OVERALL PERFORMANCE:\n")
	fmt.Printf("Total Transactions: %d\n", totalTxs)
	fmt.Printf("Overall Success Rate: %.2f%%\n", float64(totalSuccessful)/float64(totalTxs)*100)
	fmt.Printf("Best TPS Achieved: %.2f\n", bestTPS)
	fmt.Printf("Best Average Latency: %v\n", bestLatency.Truncate(time.Millisecond))
	fmt.Printf("Total Testing Time: %v\n", totalDuration.Truncate(time.Second))

	// Performance rating
	rating := "🥉 Bronze"
	if bestTPS >= 50 {
		rating = "🥇 Gold"
	} else if bestTPS >= 25 {
		rating = "🥈 Silver"
	}

	fmt.Printf("Performance Rating: %s\n", rating)
	fmt.Printf("===============================\n")
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
