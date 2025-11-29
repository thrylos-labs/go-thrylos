// core/transaction/replay.go
// Transaction replay protection utilities
// Prevents transactions from being replayed after chain reorganizations

package transaction

import (
	"fmt"
	"time"
)

// ReplayProtection contains information to prevent transaction replay attacks
type ReplayProtection struct {
	// ChainID prevents replay across different chains
	ChainID string

	// FinalizedBlockHash ties the transaction to a specific finalized state
	// This prevents replay after deep reorganizations
	FinalizedBlockHash string

	// FinalizedBlockHeight is the height of the finalized block
	FinalizedBlockHeight int64

	// MaxBlockAge is the maximum age (in blocks) before a transaction expires
	// This prevents replay of old transactions even without reorgs
	MaxBlockAge int64
}

// ReplayProtectionConfig defines replay protection parameters
type ReplayProtectionConfig struct {
	// RequireFinalizedBlock determines if finalized block hash is mandatory
	// Should be true for production, false for development/testing
	RequireFinalizedBlock bool

	// MinFinalizedBlockAge is the minimum age of finalized block to use
	// Prevents using very recent blocks that might not be truly finalized
	MinFinalizedBlockAge int64

	// TransactionMaxAge is how many blocks a transaction remains valid
	// After this, it cannot be replayed even on different forks
	TransactionMaxAge int64

	// AllowEmptyFinalizedBlock allows transactions without finalized block hash
	// Only for development - should be false in production
	AllowEmptyFinalizedBlock bool
}

// DefaultReplayProtectionConfig returns safe default configuration
func DefaultReplayProtectionConfig() *ReplayProtectionConfig {
	return &ReplayProtectionConfig{
		RequireFinalizedBlock:    true,
		MinFinalizedBlockAge:     2,    // At least 2 blocks old
		TransactionMaxAge:        1000, // Valid for 1000 blocks (~30 min if 2s blocks)
		AllowEmptyFinalizedBlock: false,
	}
}

// DevelopmentReplayProtectionConfig returns config for development/testing
func DevelopmentReplayProtectionConfig() *ReplayProtectionConfig {
	return &ReplayProtectionConfig{
		RequireFinalizedBlock:    false,
		MinFinalizedBlockAge:     0,
		TransactionMaxAge:        10000,
		AllowEmptyFinalizedBlock: true,
	}
}

// Validate checks if the replay protection is valid
func (rp *ReplayProtection) Validate(config *ReplayProtectionConfig, currentBlockHeight int64) error {
	// Check chain ID is present
	if rp.ChainID == "" {
		return fmt.Errorf("chain ID is required for replay protection")
	}

	// Check finalized block hash if required
	if config.RequireFinalizedBlock && !config.AllowEmptyFinalizedBlock {
		if rp.FinalizedBlockHash == "" {
			return fmt.Errorf("finalized block hash is required for replay protection")
		}

		if rp.FinalizedBlockHeight == 0 {
			return fmt.Errorf("finalized block height is required")
		}
	}

	// Check if transaction is too old
	if config.TransactionMaxAge > 0 && currentBlockHeight > 0 {
		blockAge := currentBlockHeight - rp.FinalizedBlockHeight
		if blockAge > config.TransactionMaxAge {
			return fmt.Errorf("transaction too old: block age %d exceeds max age %d",
				blockAge, config.TransactionMaxAge)
		}
	}

	// Check if finalized block is recent enough
	if config.MinFinalizedBlockAge > 0 && currentBlockHeight > 0 {
		blockAge := currentBlockHeight - rp.FinalizedBlockHeight
		if blockAge < config.MinFinalizedBlockAge {
			return fmt.Errorf("finalized block too recent: age %d < minimum %d",
				blockAge, config.MinFinalizedBlockAge)
		}
	}

	return nil
}

// IsExpired checks if the transaction has expired based on block age
func (rp *ReplayProtection) IsExpired(currentBlockHeight int64, maxAge int64) bool {
	if maxAge == 0 {
		return false // No expiration
	}

	blockAge := currentBlockHeight - rp.FinalizedBlockHeight
	return blockAge > maxAge
}

// ReplayProtectionMetrics tracks replay protection statistics
type ReplayProtectionMetrics struct {
	TransactionsWithReplayProtection uint64
	TransactionsWithoutFinalized     uint64
	ExpiredTransactions              uint64
	ReplayAttemptsDetected           uint64
	LastReplayAttempt                time.Time
}

// RecordReplayAttempt records a detected replay attack attempt
func (m *ReplayProtectionMetrics) RecordReplayAttempt() {
	m.ReplayAttemptsDetected++
	m.LastReplayAttempt = time.Now()
}

// GetMetrics returns current metrics as a map
func (m *ReplayProtectionMetrics) GetMetrics() map[string]interface{} {
	return map[string]interface{}{
		"transactions_with_protection":   m.TransactionsWithReplayProtection,
		"transactions_without_finalized": m.TransactionsWithoutFinalized,
		"expired_transactions":           m.ExpiredTransactions,
		"replay_attempts_detected":       m.ReplayAttemptsDetected,
		"last_replay_attempt":            m.LastReplayAttempt,
	}
}
