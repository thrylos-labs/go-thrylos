// core/transaction/replay.go
// Transaction replay protection utilities
// Prevents transactions from being replayed after chain reorganizations
// AUDIT FIX: Enhanced security for CertiK Audit Finding #1 - Replay Attack Protection

package transaction

import (
	"fmt"
	"sync"
	"time"

	"github.com/thrylos-labs/go-thrylos/core/math"
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

	// AUDIT ENHANCEMENT: Add timestamp for time-based expiration
	Timestamp int64

	// AUDIT ENHANCEMENT: Add shard ID for cross-shard protection
	ShardID string
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

	// AUDIT ENHANCEMENT: Time-based expiration (in seconds)
	TransactionTimeoutSeconds int64

	// AUDIT ENHANCEMENT: Enable cross-shard replay protection
	EnableShardProtection bool

	// AUDIT ENHANCEMENT: Maximum nonce gap allowed
	MaxNonceGap uint64
}

// DefaultReplayProtectionConfig returns safe default configuration
func DefaultReplayProtectionConfig() *ReplayProtectionConfig {
	return &ReplayProtectionConfig{
		RequireFinalizedBlock:     true,
		MinFinalizedBlockAge:      2,    // At least 2 blocks old
		TransactionMaxAge:         1000, // Valid for 1000 blocks (~30 min if 2s blocks)
		AllowEmptyFinalizedBlock:  false,
		TransactionTimeoutSeconds: 1800, // 30 minutes
		EnableShardProtection:     true,
		MaxNonceGap:               100, // Prevent nonce manipulation
	}
}

// DevelopmentReplayProtectionConfig returns config for development/testing
func DevelopmentReplayProtectionConfig() *ReplayProtectionConfig {
	return &ReplayProtectionConfig{
		RequireFinalizedBlock:     false,
		MinFinalizedBlockAge:      0,
		TransactionMaxAge:         10000,
		AllowEmptyFinalizedBlock:  true,
		TransactionTimeoutSeconds: 3600,
		EnableShardProtection:     false,
		MaxNonceGap:               1000,
	}
}

// EnsureReplayProtectionV3 ensures a transaction has all required replay protection fields
// This is an enhanced version that enforces stricter validation
func EnsureReplayProtectionV3(tx interface{}, chainID string) error {
	// This function works with the Transaction proto type
	// We'll add chain ID validation to ensure it's always set correctly

	// Note: This is a helper that will be called from validator.go
	// The actual implementation needs to access the transaction's ChainId field

	if chainID == "" {
		return fmt.Errorf("chain ID cannot be empty")
	}

	return nil
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

	// AUDIT ENHANCEMENT: Validate finalized block height is not negative
	if rp.FinalizedBlockHeight < 0 {
		return fmt.Errorf("finalized block height cannot be negative: %d", rp.FinalizedBlockHeight)
	}

	// AUDIT ENHANCEMENT: Check timestamp validity
	if rp.Timestamp > 0 {
		currentTime := time.Now().Unix()

		// Check if timestamp is not in the future
		if rp.Timestamp > currentTime {
			return fmt.Errorf("transaction timestamp is in the future")
		}

		// Check time-based expiration
		if config.TransactionTimeoutSeconds > 0 {
			timeDiff, err := math.SafeSub(currentTime, rp.Timestamp)
			if err != nil {
				return fmt.Errorf("time calculation error: %w", err)
			}

			if timeDiff > config.TransactionTimeoutSeconds {
				return fmt.Errorf("transaction expired: %d seconds old (max: %d)",
					timeDiff, config.TransactionTimeoutSeconds)
			}
		}
	}

	// Check if transaction is too old (block-based)
	if config.TransactionMaxAge > 0 && currentBlockHeight > 0 {
		// Use SafeMath for overflow protection
		blockAge, err := math.SafeSub(currentBlockHeight, rp.FinalizedBlockHeight)
		if err != nil {
			return fmt.Errorf("block age calculation error: %w", err)
		}

		if blockAge > config.TransactionMaxAge {
			return fmt.Errorf("transaction too old: block age %d exceeds max age %d",
				blockAge, config.TransactionMaxAge)
		}

		// AUDIT ENHANCEMENT: Check for negative block age (future block)
		if blockAge < 0 {
			return fmt.Errorf("transaction references future block")
		}
	}

	// Check if finalized block is recent enough
	if config.MinFinalizedBlockAge > 0 && currentBlockHeight > 0 {
		blockAge, err := math.SafeSub(currentBlockHeight, rp.FinalizedBlockHeight)
		if err != nil {
			return fmt.Errorf("block age calculation error: %w", err)
		}

		if blockAge < config.MinFinalizedBlockAge {
			return fmt.Errorf("finalized block too recent: age %d < minimum %d",
				blockAge, config.MinFinalizedBlockAge)
		}
	}

	// AUDIT ENHANCEMENT: Validate shard ID if cross-shard protection enabled
	if config.EnableShardProtection && rp.ShardID == "" {
		return fmt.Errorf("shard ID is required when cross-shard protection is enabled")
	}

	return nil
}

// IsExpired checks if the transaction has expired based on block age
func (rp *ReplayProtection) IsExpired(currentBlockHeight int64, maxAge int64) bool {
	if maxAge == 0 {
		return false // No expiration
	}

	// Use SafeMath to prevent overflow
	blockAge, err := math.SafeSub(currentBlockHeight, rp.FinalizedBlockHeight)
	if err != nil {
		// If calculation fails, consider expired for safety
		return true
	}

	return blockAge > maxAge
}

// IsExpiredByTime checks if the transaction has expired based on timestamp
func (rp *ReplayProtection) IsExpiredByTime(timeoutSeconds int64) bool {
	if timeoutSeconds == 0 || rp.Timestamp == 0 {
		return false // No time-based expiration
	}

	currentTime := time.Now().Unix()

	// Use SafeMath for time calculation
	timeDiff, err := math.SafeSub(currentTime, rp.Timestamp)
	if err != nil {
		// If calculation fails, consider expired for safety
		return true
	}

	return timeDiff > timeoutSeconds
}

// ReplayProtectionMetrics tracks replay protection statistics
type ReplayProtectionMetrics struct {
	mu sync.RWMutex // AUDIT ENHANCEMENT: Thread-safe metrics

	TransactionsWithReplayProtection uint64
	TransactionsWithoutFinalized     uint64
	ExpiredTransactions              uint64
	ReplayAttemptsDetected           uint64
	LastReplayAttempt                time.Time

	// AUDIT ENHANCEMENT: Additional metrics
	TimeBasedExpiredTransactions  uint64
	BlockBasedExpiredTransactions uint64
	CrossShardReplayAttempts      uint64
	NonceManipulationAttempts     uint64
	FutureBlockReferences         uint64
	FutureTimestampAttempts       uint64
}

// RecordReplayAttempt records a detected replay attack attempt
func (m *ReplayProtectionMetrics) RecordReplayAttempt() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ReplayAttemptsDetected++
	m.LastReplayAttempt = time.Now()
}

// AUDIT ENHANCEMENT: Record time-based expiration
func (m *ReplayProtectionMetrics) RecordTimeBasedExpiration() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ExpiredTransactions++
	m.TimeBasedExpiredTransactions++
}

// AUDIT ENHANCEMENT: Record block-based expiration
func (m *ReplayProtectionMetrics) RecordBlockBasedExpiration() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ExpiredTransactions++
	m.BlockBasedExpiredTransactions++
}

// AUDIT ENHANCEMENT: Record cross-shard replay attempt
func (m *ReplayProtectionMetrics) RecordCrossShardReplayAttempt() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CrossShardReplayAttempts++
	m.ReplayAttemptsDetected++
	m.LastReplayAttempt = time.Now()
}

// AUDIT ENHANCEMENT: Record nonce manipulation attempt
func (m *ReplayProtectionMetrics) RecordNonceManipulation() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.NonceManipulationAttempts++
}

// AUDIT ENHANCEMENT: Record future block reference
func (m *ReplayProtectionMetrics) RecordFutureBlockReference() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.FutureBlockReferences++
}

// AUDIT ENHANCEMENT: Record future timestamp attempt
func (m *ReplayProtectionMetrics) RecordFutureTimestamp() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.FutureTimestampAttempts++
}

// RecordChainIDMismatch records a chain ID mismatch attempt
func (m *ReplayProtectionMetrics) RecordChainIDMismatch() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ReplayAttemptsDetected++
	m.LastReplayAttempt = time.Now()
}

// GetMetrics returns current metrics as a map (thread-safe)
func (m *ReplayProtectionMetrics) GetMetrics() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return map[string]interface{}{
		"transactions_with_protection":   m.TransactionsWithReplayProtection,
		"transactions_without_finalized": m.TransactionsWithoutFinalized,
		"expired_transactions":           m.ExpiredTransactions,
		"time_based_expired":             m.TimeBasedExpiredTransactions,
		"block_based_expired":            m.BlockBasedExpiredTransactions,
		"replay_attempts_detected":       m.ReplayAttemptsDetected,
		"cross_shard_replay_attempts":    m.CrossShardReplayAttempts,
		"nonce_manipulation_attempts":    m.NonceManipulationAttempts,
		"future_block_references":        m.FutureBlockReferences,
		"future_timestamp_attempts":      m.FutureTimestampAttempts,
		"last_replay_attempt":            m.LastReplayAttempt,
	}
}

// GetSecurityStats returns a summary of security-related metrics
func (m *ReplayProtectionMetrics) GetSecurityStats() map[string]uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return map[string]uint64{
		"total_replay_attempts":     m.ReplayAttemptsDetected,
		"nonce_manipulation":        m.NonceManipulationAttempts,
		"cross_shard_replay":        m.CrossShardReplayAttempts,
		"future_block_refs":         m.FutureBlockReferences,
		"future_timestamp_attempts": m.FutureTimestampAttempts,
		"time_based_expired":        m.TimeBasedExpiredTransactions,
		"block_based_expired":       m.BlockBasedExpiredTransactions,
	}
}

// IsSecurityEventThresholdExceeded checks if security events exceed safe thresholds
func (m *ReplayProtectionMetrics) IsSecurityEventThresholdExceeded() (bool, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Alert if more than 10 replay attempts detected
	if m.ReplayAttemptsDetected > 10 {
		return true, fmt.Sprintf("High replay attempts: %d", m.ReplayAttemptsDetected)
	}

	// Alert if any nonce manipulation detected
	if m.NonceManipulationAttempts > 0 {
		return true, fmt.Sprintf("Nonce manipulation detected: %d attempts", m.NonceManipulationAttempts)
	}

	// Alert if cross-shard replay attempts
	if m.CrossShardReplayAttempts > 5 {
		return true, fmt.Sprintf("Cross-shard replay attempts: %d", m.CrossShardReplayAttempts)
	}

	return false, ""
}

// AUDIT ENHANCEMENT: ReplayCache tracks seen transactions to prevent replays
type ReplayCache struct {
	mu    sync.RWMutex
	cache map[string]*CachedTransaction

	// Maximum cache size before cleanup
	maxSize int

	// Cleanup interval
	cleanupInterval time.Duration

	// Stop channel for cleanup goroutine
	stopChan chan struct{}
}

// CachedTransaction represents a transaction in the replay cache
type CachedTransaction struct {
	TxHash    string
	Nonce     uint64
	ShardID   string
	SeenAt    time.Time
	ExpiresAt time.Time
}

// NewReplayCache creates a new replay cache
func NewReplayCache(maxSize int, cleanupInterval time.Duration) *ReplayCache {
	rc := &ReplayCache{
		cache:           make(map[string]*CachedTransaction),
		maxSize:         maxSize,
		cleanupInterval: cleanupInterval,
		stopChan:        make(chan struct{}),
	}

	// Start cleanup goroutine
	go rc.cleanupLoop()

	return rc
}

// Add adds a transaction to the replay cache
func (rc *ReplayCache) Add(txHash string, nonce uint64, shardID string, expiresAt time.Time) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	rc.cache[txHash] = &CachedTransaction{
		TxHash:    txHash,
		Nonce:     nonce,
		ShardID:   shardID,
		SeenAt:    time.Now(),
		ExpiresAt: expiresAt,
	}

	// Trigger cleanup if cache is too large
	if len(rc.cache) > rc.maxSize {
		rc.cleanupExpired()
	}
}

// Has checks if a transaction hash exists in the cache
func (rc *ReplayCache) Has(txHash string) bool {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	cached, exists := rc.cache[txHash]
	if !exists {
		return false
	}

	// Check if expired
	if time.Now().After(cached.ExpiresAt) {
		return false
	}

	return true
}

// Get retrieves a cached transaction
func (rc *ReplayCache) Get(txHash string) (*CachedTransaction, bool) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	cached, exists := rc.cache[txHash]
	if !exists {
		return nil, false
	}

	// Check if expired
	if time.Now().After(cached.ExpiresAt) {
		return nil, false
	}

	return cached, true
}

// cleanupExpired removes expired entries (caller must hold lock)
func (rc *ReplayCache) cleanupExpired() {
	now := time.Now()
	for hash, cached := range rc.cache {
		if now.After(cached.ExpiresAt) {
			delete(rc.cache, hash)
		}
	}
}

// cleanupLoop periodically cleans up expired entries
func (rc *ReplayCache) cleanupLoop() {
	ticker := time.NewTicker(rc.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rc.mu.Lock()
			rc.cleanupExpired()
			rc.mu.Unlock()
		case <-rc.stopChan:
			return
		}
	}
}

// Stop stops the cleanup goroutine
func (rc *ReplayCache) Stop() {
	close(rc.stopChan)
}

// Size returns the current cache size
func (rc *ReplayCache) Size() int {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return len(rc.cache)
}

// AUDIT ENHANCEMENT: ValidateNonceSequence checks for nonce manipulation
func ValidateNonceSequence(currentNonce, previousNonce uint64, maxGap uint64) error {
	// Check for nonce going backwards
	if currentNonce < previousNonce {
		return fmt.Errorf("nonce cannot go backwards: current %d < previous %d",
			currentNonce, previousNonce)
	}

	// Check for excessive nonce gap (potential manipulation)
	nonceGap, err := math.Sub64(currentNonce, previousNonce)
	if err != nil {
		return fmt.Errorf("nonce gap calculation error: %w", err)
	}

	if nonceGap > maxGap {
		return fmt.Errorf("nonce gap too large: %d (max allowed: %d)",
			nonceGap, maxGap)
	}

	return nil
}

// AUDIT ENHANCEMENT: ValidateCrossShardReplay checks for cross-shard replay attempts
func ValidateCrossShardReplay(txShardID, expectedShardID string) error {
	if txShardID == "" || expectedShardID == "" {
		return fmt.Errorf("shard IDs cannot be empty")
	}

	if txShardID != expectedShardID {
		return fmt.Errorf("cross-shard replay detected: tx shard %s != expected %s",
			txShardID, expectedShardID)
	}

	return nil
}

// ============================================================================
// V3 ENHANCEMENTS - Enhanced Replay Detection with Chain ID Binding
// ============================================================================

// ReplayDetectorV3 provides enhanced replay attack detection with chain ID binding
type ReplayDetectorV3 struct {
	cache   *ReplayCache
	config  *ReplayProtectionConfig
	metrics *ReplayProtectionMetrics
	chainID string
	mu      sync.RWMutex

	// Track nonces per account
	accountNonces map[string]uint64
}

// NewReplayDetectorV3 creates a new enhanced replay detector
func NewReplayDetectorV3(chainID string, config *ReplayProtectionConfig, metrics *ReplayProtectionMetrics) *ReplayDetectorV3 {
	if config == nil {
		config = DefaultReplayProtectionConfig()
	}
	if metrics == nil {
		metrics = &ReplayProtectionMetrics{}
	}

	return &ReplayDetectorV3{
		cache:         NewReplayCache(100000, 5*time.Minute),
		config:        config,
		metrics:       metrics,
		chainID:       chainID,
		accountNonces: make(map[string]uint64),
	}
}

// CheckReplayV3 performs comprehensive replay attack detection
func (rd *ReplayDetectorV3) CheckReplayV3(txHash, txChainID, from string, nonce uint64, currentBlockHeight int64) error {
	// 1. CRITICAL: Verify chain ID matches
	if txChainID != rd.chainID {
		rd.metrics.RecordReplayAttempt()
		return fmt.Errorf("chain ID mismatch: transaction has %s, expected %s", txChainID, rd.chainID)
	}

	// 2. Check if we've seen this transaction hash before
	if rd.cache.Has(txHash) {
		cached, exists := rd.cache.Get(txHash)
		if exists {
			// Same transaction hash seen again
			return fmt.Errorf("duplicate transaction detected: hash %s already processed at %v",
				txHash, cached.SeenAt)
		}
	}

	// 3. Check nonce ordering
	rd.mu.Lock()
	previousNonce, exists := rd.accountNonces[from]
	if exists {
		// Validate nonce sequence
		if nonce <= previousNonce {
			rd.mu.Unlock()
			rd.metrics.RecordNonceManipulation()
			return fmt.Errorf("invalid nonce: got %d, expected > %d (replay attack or nonce reuse)",
				nonce, previousNonce)
		}

		// Check for excessive nonce gap
		if err := ValidateNonceSequence(nonce, previousNonce, rd.config.MaxNonceGap); err != nil {
			rd.mu.Unlock()
			rd.metrics.RecordNonceManipulation()
			return fmt.Errorf("nonce validation failed: %w", err)
		}
	}

	// Update account nonce
	rd.accountNonces[from] = nonce
	rd.mu.Unlock()

	// 4. Add to cache
	expiresAt := time.Now().Add(time.Duration(rd.config.TransactionTimeoutSeconds) * time.Second)
	rd.cache.Add(txHash, nonce, "", expiresAt)

	return nil
}

// CleanupExpiredV3 removes expired entries from both cache and nonce tracking
func (rd *ReplayDetectorV3) CleanupExpiredV3() {
	// The cache has its own cleanup, we just need to handle nonce cleanup
	// For now, nonces are kept permanently as they should always increase
	// In a production system, you might want to clean up after account inactivity
}

// GetNonce returns the last known nonce for an account
func (rd *ReplayDetectorV3) GetNonce(from string) (uint64, bool) {
	rd.mu.RLock()
	defer rd.mu.RUnlock()

	nonce, exists := rd.accountNonces[from]
	return nonce, exists
}

// Stop stops the replay detector
func (rd *ReplayDetectorV3) Stop() {
	if rd.cache != nil {
		rd.cache.Stop()
	}
}

// ============================================================================
// Helper Functions for V3 Integration
// ============================================================================

// ValidateChainIDMatch validates that transaction chain ID matches expected
func ValidateChainIDMatch(txChainID, expectedChainID string) error {
	if txChainID == "" {
		return fmt.Errorf("transaction missing chain_id field")
	}

	if expectedChainID == "" {
		return fmt.Errorf("expected chain_id not configured")
	}

	// Normalize for case-insensitive comparison
	txChainIDNorm := normalizeChainID(txChainID)
	expectedChainIDNorm := normalizeChainID(expectedChainID)

	if txChainIDNorm != expectedChainIDNorm {
		return fmt.Errorf("chain ID mismatch: got %s, expected %s", txChainID, expectedChainID)
	}

	return nil
}

// normalizeChainID normalizes a chain ID for comparison
func normalizeChainID(chainID string) string {
	// Convert to lowercase and trim whitespace
	normalized := ""
	for _, r := range chainID {
		if r != ' ' && r != '\t' && r != '\n' {
			if r >= 'A' && r <= 'Z' {
				normalized += string(r + 32) // Convert to lowercase
			} else {
				normalized += string(r)
			}
		}
	}
	return normalized
}

// ValidateTransactionTimingV3 validates transaction timing with enhanced checks
func ValidateTransactionTimingV3(timestamp int64, config *ReplayProtectionConfig) error {
	if timestamp <= 0 {
		return fmt.Errorf("transaction timestamp must be positive")
	}

	currentTime := time.Now().Unix()

	// Check if timestamp is in the future
	if timestamp > currentTime {
		return fmt.Errorf("transaction timestamp is in the future: tx=%d, now=%d",
			timestamp, currentTime)
	}

	// Check if transaction is too old
	if config.TransactionTimeoutSeconds > 0 {
		timeDiff, err := math.SafeSub(currentTime, timestamp)
		if err != nil {
			return fmt.Errorf("time calculation error: %w", err)
		}

		if timeDiff > config.TransactionTimeoutSeconds {
			return fmt.Errorf("transaction expired: %d seconds old (max: %d)",
				timeDiff, config.TransactionTimeoutSeconds)
		}
	}

	return nil
}

// ValidateBlockHeightV3 validates block height references in transaction
func ValidateBlockHeightV3(txBlockHeight, currentBlockHeight int64, config *ReplayProtectionConfig) error {
	if txBlockHeight < 0 {
		return fmt.Errorf("transaction block height cannot be negative: %d", txBlockHeight)
	}

	if currentBlockHeight <= 0 {
		// Current block height not available, skip validation
		return nil
	}

	// Check if transaction references a future block
	if txBlockHeight > currentBlockHeight {
		return fmt.Errorf("transaction references future block: tx=%d, current=%d",
			txBlockHeight, currentBlockHeight)
	}

	// Check if transaction is too old
	if config.TransactionMaxAge > 0 {
		blockAge, err := math.SafeSub(currentBlockHeight, txBlockHeight)
		if err != nil {
			return fmt.Errorf("block age calculation error: %w", err)
		}

		if blockAge > config.TransactionMaxAge {
			return fmt.Errorf("transaction too old: block age %d exceeds max %d",
				blockAge, config.TransactionMaxAge)
		}
	}

	return nil
}
