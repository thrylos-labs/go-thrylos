// Add missing imports and remove the problematic function
package node

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/crypto"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// CreateTransactionBatch creates multiple transactions with sequential nonces
func (tps *TPSTransactionTester) CreateTransactionBatch(from string, recipients []string, amount int64, gasPrice int64, batchSize int) ([]*core.Transaction, error) {
	// Get starting nonce once
	startingNonce, err := tps.node.blockchain.GetNonce(from)
	if err != nil {
		return nil, fmt.Errorf("failed to get starting nonce: %v", err)
	}

	var transactions []*core.Transaction

	for i := 0; i < batchSize; i++ {
		recipient := recipients[i%len(recipients)]
		nonce := startingNonce + uint64(i)

		tx, err := tps.CreateTestTransactionWithNonce(from, recipient, amount, gasPrice, nonce)
		if err != nil {
			return nil, fmt.Errorf("failed to create transaction %d: %v", i, err)
		}

		transactions = append(transactions, tx)
	}

	return transactions, nil
}

// Remove this problematic function since crypto.NewPrivateKeyFromSeed doesn't exist
// Instead, use crypto.NewPrivateKeyFromBytes with deterministic seed

// createDeterministicPrivateKey creates a deterministic private key from seed
func createDeterministicPrivateKey(seed []byte) (crypto.PrivateKey, error) {
	// Hash the seed to get proper random bytes
	hash := sha256.Sum256(seed)

	// Extend to MLDSA private key size (2560 bytes, not 4864)
	keyBytes := make([]byte, 2560) // Correct MLDSA44 private key size

	// Fill the key bytes using repeated hashing
	for i := 0; i < len(keyBytes); {
		hash = sha256.Sum256(hash[:])
		copied := copy(keyBytes[i:], hash[:])
		i += copied
	}

	// Create private key from deterministic bytes
	return crypto.NewPrivateKeyFromBytes(keyBytes)
}

// Add this if it doesn't exist in your crypto package
func (tps *TPSTransactionTester) generateRecipients(count int) ([]string, error) {
	recipients := make([]string, count)

	for i := 0; i < count; i++ {
		// Generate proper private key and address
		key, err := crypto.NewPrivateKey()
		if err != nil {
			return nil, fmt.Errorf("failed to generate key %d: %v", i, err)
		}

		addr, err := account.GenerateAddress(key.PublicKey())
		if err != nil {
			return nil, fmt.Errorf("failed to generate address %d: %v", i, err)
		}

		recipients[i] = addr
	}

	return recipients, nil
}

// RunCustomSlowTPSTest method (add this to your tps_testing.go if not already there)
func (tps *TPSTransactionTester) RunCustomSlowTPSTest(targetTPS float64, duration time.Duration) (*TPSTestResult, error) {
	fmt.Printf("🚀 Starting Custom Slow TPS Test (Target: %.2f TPS)\n", targetTPS)

	recipients, err := tps.generateRecipients(20)
	if err != nil {
		return nil, fmt.Errorf("failed to generate recipients: %v", err)
	}

	var totalTxs int64
	var successfulTxs int64

	startTime := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	// Get starting nonce ONCE at the beginning
	startingNonce, err := tps.node.blockchain.GetNonce(tps.address)
	if err != nil {
		return nil, fmt.Errorf("failed to get starting nonce: %v", err)
	}

	fmt.Printf("🔍 Starting with nonce: %d\n", startingNonce)

	// Calculate interval for fractional TPS
	var interval time.Duration
	if targetTPS >= 1.0 {
		interval = time.Duration(float64(time.Second) / targetTPS)
	} else {
		// For sub-1 TPS, calculate seconds per transaction
		secondsPerTx := 1.0 / targetTPS
		interval = time.Duration(secondsPerTx * float64(time.Second))
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	fmt.Printf("🎯 Creating transactions every %v (%.2f TPS target)\n", interval, targetTPS)

	lastReportTime := startTime

	for {
		select {
		case <-ticker.C:
			// Use sequential nonce, don't query blockchain each time
			currentNonce := startingNonce + uint64(totalTxs)

			// Create single transaction with sequential nonce
			recipient := recipients[totalTxs%int64(len(recipients))]
			tx, err := tps.CreateTestTransactionWithNonce(tps.address, recipient, 15000000, 1000, currentNonce)
			if err != nil {
				fmt.Printf("❌ Failed to create transaction: %v\n", err)
				totalTxs++
				continue
			}

			// Submit transaction
			if err := tps.node.SubmitTransaction(tx); err == nil {
				successfulTxs++
				// Only log first few successes to avoid spam in tests
				if successfulTxs <= 3 {
					fmt.Printf("✅ TX success (nonce %d): %s\n", currentNonce, tx.Id)
				}
			} else {
				fmt.Printf("🔍 TX failed (nonce %d): %v\n", currentNonce, err)
			}
			totalTxs++

			// Progress reporting every 10 seconds for slow tests
			now := time.Now()
			if now.Sub(lastReportTime) >= 10*time.Second {
				elapsed := now.Sub(startTime)
				currentTPS := float64(successfulTxs) / elapsed.Seconds()
				successRate := float64(successfulTxs) / float64(totalTxs) * 100

				fmt.Printf("📊 Progress: %d/%d txs (%.1f%%), %.3f TPS\n",
					successfulTxs, totalTxs, successRate, currentTPS)

				lastReportTime = now
			}

		case <-ctx.Done():
			goto testComplete
		}
	}

testComplete:
	endTime := time.Now()
	actualDuration := endTime.Sub(startTime)

	result := &TPSTestResult{
		TestName:          fmt.Sprintf("Custom Slow TPS Test (Target: %.2f)", targetTPS),
		Duration:          actualDuration,
		TotalTransactions: totalTxs,
		SuccessfulTxs:     successfulTxs,
		FailedTxs:         totalTxs - successfulTxs,
		AverageTPS:        float64(successfulTxs) / actualDuration.Seconds(),
		ErrorRate:         float64(totalTxs-successfulTxs) / float64(totalTxs) * 100,
		StartTime:         startTime,
		EndTime:           endTime,
	}

	// Only print detailed results in non-test environments
	if !isRunningInTest() {
		fmt.Printf("\n📊 === CUSTOM SLOW TPS RESULTS ===\n")
		fmt.Printf("Target TPS: %.2f\n", targetTPS)
		fmt.Printf("Duration: %v\n", result.Duration.Truncate(time.Millisecond))
		fmt.Printf("Total Transactions: %d\n", result.TotalTransactions)
		fmt.Printf("Successful: %d (%.1f%%)\n", result.SuccessfulTxs,
			float64(result.SuccessfulTxs)/float64(result.TotalTransactions)*100)
		fmt.Printf("Achieved TPS: %.3f\n", result.AverageTPS)
		fmt.Printf("Efficiency: %.1f%% of target\n", (result.AverageTPS/targetTPS)*100)
		fmt.Printf("===================================\n")
	}

	return result, nil
}

// isRunningInTest detects if we're running in a test environment
func isRunningInTest() bool {
	// Check if we're running under go test
	for _, arg := range os.Args {
		if strings.Contains(arg, "test") || strings.Contains(arg, ".test") {
			return true
		}
	}
	return false
}
