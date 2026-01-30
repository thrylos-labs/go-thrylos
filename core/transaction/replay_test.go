// core/transaction/replay_test.go
// Comprehensive tests for replay protection
// AUDIT FIX: CertiK Audit Finding #2 - Test Coverage
// M-1 FIX: Added comprehensive reorg protection tests

package transaction

import (
	"testing"
	"time"
)

// ============================================================================
// REPLAY PROTECTION VALIDATION TESTS
// ============================================================================

func TestReplayProtection_Validate_ChainID(t *testing.T) {
	config := DefaultReplayProtectionConfig()

	tests := []struct {
		name    string
		rp      *ReplayProtection
		wantErr bool
	}{
		{
			name: "valid chain ID",
			rp: &ReplayProtection{
				ChainID:              "thrylos-mainnet",
				FinalizedBlockHash:   "hash123",
				FinalizedBlockHeight: 100,
				Timestamp:            time.Now().Unix(),
				ShardID:              "shard-1",
			},
			wantErr: false,
		},
		{
			name: "missing chain ID",
			rp: &ReplayProtection{
				FinalizedBlockHash:   "hash123",
				FinalizedBlockHeight: 100,
				ShardID:              "shard-1",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.rp.Validate(config, 150)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestReplayProtection_Validate_BlockAge(t *testing.T) {
	config := DefaultReplayProtectionConfig()
	config.TransactionMaxAge = 100

	tests := []struct {
		name            string
		finalizedHeight int64
		currentHeight   int64
		wantErr         bool
	}{
		{
			name:            "within age limit",
			finalizedHeight: 100,
			currentHeight:   150,
			wantErr:         false,
		},
		{
			name:            "at age limit",
			finalizedHeight: 100,
			currentHeight:   200,
			wantErr:         false,
		},
		{
			name:            "exceeds age limit",
			finalizedHeight: 100,
			currentHeight:   201,
			wantErr:         true,
		},
		{
			name:            "future block reference",
			finalizedHeight: 200,
			currentHeight:   100,
			wantErr:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rp := &ReplayProtection{
				ChainID:              "thrylos-mainnet",
				FinalizedBlockHash:   "hash123",
				FinalizedBlockHeight: tt.finalizedHeight,
				Timestamp:            time.Now().Unix(),
				ShardID:              "shard-1",
			}

			err := rp.Validate(config, tt.currentHeight)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestReplayProtection_Validate_TimeBasedExpiration(t *testing.T) {
	config := DefaultReplayProtectionConfig()
	config.TransactionTimeoutSeconds = 60 // 60 seconds

	tests := []struct {
		name      string
		timestamp int64
		wantErr   bool
	}{
		{
			name:      "fresh transaction",
			timestamp: time.Now().Unix(),
			wantErr:   false,
		},
		{
			name:      "recent transaction",
			timestamp: time.Now().Unix() - 30,
			wantErr:   false,
		},
		{
			name:      "expired transaction",
			timestamp: time.Now().Unix() - 120,
			wantErr:   true,
		},
		{
			name:      "future timestamp",
			timestamp: time.Now().Unix() + 60,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rp := &ReplayProtection{
				ChainID:              "thrylos-mainnet",
				FinalizedBlockHash:   "hash123",
				FinalizedBlockHeight: 100,
				Timestamp:            tt.timestamp,
				ShardID:              "shard-1",
			}

			err := rp.Validate(config, 150)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestReplayProtection_Validate_ShardProtection(t *testing.T) {
	config := DefaultReplayProtectionConfig()
	config.EnableShardProtection = true

	tests := []struct {
		name    string
		shardID string
		wantErr bool
	}{
		{
			name:    "valid shard ID",
			shardID: "shard-1",
			wantErr: false,
		},
		{
			name:    "missing shard ID",
			shardID: "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rp := &ReplayProtection{
				ChainID:              "thrylos-mainnet",
				FinalizedBlockHash:   "hash123",
				FinalizedBlockHeight: 100,
				Timestamp:            time.Now().Unix(),
				ShardID:              tt.shardID,
			}

			err := rp.Validate(config, 150)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// ============================================================================
// EXPIRATION TESTS
// ============================================================================

func TestReplayProtection_IsExpired(t *testing.T) {
	tests := []struct {
		name            string
		finalizedHeight int64
		currentHeight   int64
		maxAge          int64
		want            bool
	}{
		{
			name:            "not expired",
			finalizedHeight: 100,
			currentHeight:   150,
			maxAge:          100,
			want:            false,
		},
		{
			name:            "just expired",
			finalizedHeight: 100,
			currentHeight:   201,
			maxAge:          100,
			want:            true,
		},
		{
			name:            "no expiration",
			finalizedHeight: 100,
			currentHeight:   1000,
			maxAge:          0,
			want:            false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rp := &ReplayProtection{
				FinalizedBlockHeight: tt.finalizedHeight,
			}

			got := rp.IsExpired(tt.currentHeight, tt.maxAge)
			if got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReplayProtection_IsExpiredByTime(t *testing.T) {
	now := time.Now().Unix()

	tests := []struct {
		name           string
		timestamp      int64
		timeoutSeconds int64
		want           bool
	}{
		{
			name:           "fresh transaction",
			timestamp:      now,
			timeoutSeconds: 60,
			want:           false,
		},
		{
			name:           "expired transaction",
			timestamp:      now - 120,
			timeoutSeconds: 60,
			want:           true,
		},
		{
			name:           "no timeout",
			timestamp:      now - 1000,
			timeoutSeconds: 0,
			want:           false,
		},
		{
			name:           "no timestamp",
			timestamp:      0,
			timeoutSeconds: 60,
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rp := &ReplayProtection{
				Timestamp: tt.timestamp,
			}

			got := rp.IsExpiredByTime(tt.timeoutSeconds)
			if got != tt.want {
				t.Errorf("IsExpiredByTime() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ============================================================================
// METRICS TESTS
// ============================================================================

func TestReplayProtectionMetrics_ThreadSafety(t *testing.T) {
	metrics := &ReplayProtectionMetrics{}

	// Run concurrent operations
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				metrics.RecordReplayAttempt()
				metrics.RecordTimeBasedExpiration()
				metrics.RecordBlockBasedExpiration()
				metrics.RecordCrossShardReplayAttempt()
				metrics.RecordNonceManipulation()
				metrics.RecordReorgReplayAttempt()
				metrics.RecordDeepReorg()
				metrics.GetMetrics()
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify metrics were recorded (should be > 0)
	m := metrics.GetMetrics()
	if m["replay_attempts_detected"].(uint64) == 0 {
		t.Error("Expected replay attempts to be recorded")
	}
}

func TestReplayProtectionMetrics_RecordReplayAttempt(t *testing.T) {
	metrics := &ReplayProtectionMetrics{}

	initialCount := metrics.ReplayAttemptsDetected
	metrics.RecordReplayAttempt()

	if metrics.ReplayAttemptsDetected != initialCount+1 {
		t.Errorf("Expected ReplayAttemptsDetected to increment")
	}

	if metrics.LastReplayAttempt.IsZero() {
		t.Error("Expected LastReplayAttempt to be set")
	}
}

// M-1 FIX: Test reorg-specific metrics
func TestReplayProtectionMetrics_ReorgMetrics(t *testing.T) {
	metrics := &ReplayProtectionMetrics{}

	// Record reorg replay attempt
	metrics.RecordReorgReplayAttempt()
	if metrics.ReorgReplayAttempts != 1 {
		t.Errorf("Expected ReorgReplayAttempts to be 1, got %d", metrics.ReorgReplayAttempts)
	}
	if metrics.ReplayAttemptsDetected != 1 {
		t.Errorf("Expected ReplayAttemptsDetected to be 1, got %d", metrics.ReplayAttemptsDetected)
	}

	// Record deep reorg
	metrics.RecordDeepReorg()
	if metrics.DeepReorgDetected != 1 {
		t.Errorf("Expected DeepReorgDetected to be 1, got %d", metrics.DeepReorgDetected)
	}

	// Check security stats
	stats := metrics.GetSecurityStats()
	if stats["reorg_replay_attempts"] != 1 {
		t.Errorf("Expected reorg_replay_attempts to be 1, got %d", stats["reorg_replay_attempts"])
	}
}

// ============================================================================
// REPLAY CACHE TESTS
// ============================================================================

func TestReplayCache_AddAndHas(t *testing.T) {
	cache := NewReplayCache(1000, time.Minute)
	defer cache.Stop()

	txHash := "tx123"
	expiresAt := time.Now().Add(time.Hour)
	blockHeight := int64(100)

	// Add transaction
	cache.Add(txHash, 1, "shard-1", expiresAt, blockHeight)

	// Check it exists
	if !cache.Has(txHash) {
		t.Error("Expected transaction to exist in cache")
	}

	// Check non-existent transaction
	if cache.Has("nonexistent") {
		t.Error("Expected non-existent transaction to not be in cache")
	}
}

func TestReplayCache_Expiration(t *testing.T) {
	cache := NewReplayCache(1000, time.Millisecond*100)
	defer cache.Stop()

	txHash := "tx123"
	expiresAt := time.Now().Add(time.Millisecond * 200)
	blockHeight := int64(100)

	// Add transaction
	cache.Add(txHash, 1, "shard-1", expiresAt, blockHeight)

	// Should exist initially
	if !cache.Has(txHash) {
		t.Error("Expected transaction to exist")
	}

	// Wait for expiration
	time.Sleep(time.Millisecond * 300)

	// Should not exist after expiration
	if cache.Has(txHash) {
		t.Error("Expected transaction to be expired")
	}
}

func TestReplayCache_Get(t *testing.T) {
	cache := NewReplayCache(1000, time.Minute)
	defer cache.Stop()

	txHash := "tx123"
	nonce := uint64(42)
	shardID := "shard-1"
	expiresAt := time.Now().Add(time.Hour)
	blockHeight := int64(100)

	// Add transaction
	cache.Add(txHash, nonce, shardID, expiresAt, blockHeight)

	// Retrieve it
	cached, exists := cache.Get(txHash)
	if !exists {
		t.Fatal("Expected transaction to exist")
	}

	if cached.Nonce != nonce {
		t.Errorf("Expected nonce %d, got %d", nonce, cached.Nonce)
	}

	if cached.ShardID != shardID {
		t.Errorf("Expected shardID %s, got %s", shardID, cached.ShardID)
	}

	// M-1 FIX: Check block height is tracked
	if cached.SeenAtBlockHeight != blockHeight {
		t.Errorf("Expected block height %d, got %d", blockHeight, cached.SeenAtBlockHeight)
	}
}

func TestReplayCache_Size(t *testing.T) {
	cache := NewReplayCache(1000, time.Minute)
	defer cache.Stop()

	initialSize := cache.Size()
	if initialSize != 0 {
		t.Errorf("Expected initial size 0, got %d", initialSize)
	}

	// Add transactions
	for i := 0; i < 10; i++ {
		cache.Add("tx"+string(rune(i)), uint64(i), "shard-1", time.Now().Add(time.Hour), int64(100+i))
	}

	size := cache.Size()
	if size != 10 {
		t.Errorf("Expected size 10, got %d", size)
	}
}

// ============================================================================
// NONCE VALIDATION TESTS
// ============================================================================

func TestValidateNonceSequence(t *testing.T) {
	tests := []struct {
		name          string
		currentNonce  uint64
		previousNonce uint64
		maxGap        uint64
		wantErr       bool
	}{
		{
			name:          "sequential nonce",
			currentNonce:  2,
			previousNonce: 1,
			maxGap:        100,
			wantErr:       false,
		},
		{
			name:          "nonce with small gap",
			currentNonce:  10,
			previousNonce: 5,
			maxGap:        100,
			wantErr:       false,
		},
		{
			name:          "nonce goes backward",
			currentNonce:  5,
			previousNonce: 10,
			maxGap:        100,
			wantErr:       true,
		},
		{
			name:          "nonce gap too large",
			currentNonce:  200,
			previousNonce: 1,
			maxGap:        100,
			wantErr:       true,
		},
		{
			name:          "same nonce",
			currentNonce:  5,
			previousNonce: 5,
			maxGap:        100,
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNonceSequence(tt.currentNonce, tt.previousNonce, tt.maxGap)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateNonceSequence() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// ============================================================================
// CROSS-SHARD REPLAY TESTS
// ============================================================================

func TestValidateCrossShardReplay(t *testing.T) {
	tests := []struct {
		name            string
		txShardID       string
		expectedShardID string
		wantErr         bool
	}{
		{
			name:            "matching shard IDs",
			txShardID:       "shard-1",
			expectedShardID: "shard-1",
			wantErr:         false,
		},
		{
			name:            "mismatched shard IDs",
			txShardID:       "shard-1",
			expectedShardID: "shard-2",
			wantErr:         true,
		},
		{
			name:            "empty tx shard ID",
			txShardID:       "",
			expectedShardID: "shard-1",
			wantErr:         true,
		},
		{
			name:            "empty expected shard ID",
			txShardID:       "shard-1",
			expectedShardID: "",
			wantErr:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCrossShardReplay(tt.txShardID, tt.expectedShardID)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCrossShardReplay() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// ============================================================================
// M-1 FIX: REPLAY DETECTOR V3 TESTS (REORG PROTECTION)
// ============================================================================

func TestReplayDetectorV3_CheckReplayV3_ChainIDMismatch(t *testing.T) {
	detector := NewReplayDetectorV3("thrylos-mainnet", nil, nil)
	defer detector.Stop()

	// Try with wrong chain ID
	err := detector.CheckReplayV3("tx123", "wrong-chain", "addr1", 1, 100)
	if err == nil {
		t.Error("Expected chain ID mismatch error")
	}

	// Verify metric was recorded
	if detector.metrics.ReplayAttemptsDetected == 0 {
		t.Error("Expected replay attempt to be recorded")
	}
}

func TestReplayDetectorV3_CheckReplayV3_DuplicateTransaction(t *testing.T) {
	detector := NewReplayDetectorV3("thrylos-mainnet", nil, nil)
	defer detector.Stop()

	txHash := "tx123"
	chainID := "thrylos-mainnet"
	from := "addr1"
	nonce := uint64(1)
	blockHeight := int64(100)

	// First submission should succeed
	err := detector.CheckReplayV3(txHash, chainID, from, nonce, blockHeight)
	if err != nil {
		t.Errorf("First submission should succeed: %v", err)
	}

	// Second submission with same hash should fail (REORG PROTECTION)
	err = detector.CheckReplayV3(txHash, chainID, from, nonce+1, blockHeight+1)
	if err == nil {
		t.Error("Expected duplicate transaction error (replay attack)")
	}

	// Verify reorg replay metric was recorded
	if detector.metrics.ReorgReplayAttempts == 0 {
		t.Error("Expected reorg replay attempt to be recorded")
	}
}

func TestReplayDetectorV3_CheckReplayV3_NonceReplay(t *testing.T) {
	detector := NewReplayDetectorV3("thrylos-mainnet", nil, nil)
	defer detector.Stop()

	chainID := "thrylos-mainnet"
	from := "addr1"
	blockHeight := int64(100)

	// Submit transaction with nonce 5
	err := detector.CheckReplayV3("tx1", chainID, from, 5, blockHeight)
	if err != nil {
		t.Errorf("First submission should succeed: %v", err)
	}

	// Try to submit with nonce 3 (lower than previous)
	// This simulates replay after reorg
	err = detector.CheckReplayV3("tx2", chainID, from, 3, blockHeight+1)
	if err == nil {
		t.Error("Expected nonce replay error (prevents reorg replay)")
	}

	// Verify nonce manipulation was recorded
	if detector.metrics.NonceManipulationAttempts == 0 {
		t.Error("Expected nonce manipulation to be recorded")
	}
}

func TestReplayDetectorV3_CheckReplayV3_NonceSequence(t *testing.T) {
	detector := NewReplayDetectorV3("thrylos-mainnet", nil, nil)
	defer detector.Stop()

	chainID := "thrylos-mainnet"
	from := "addr1"
	blockHeight := int64(100)

	// Submit transaction with nonce 1
	err := detector.CheckReplayV3("tx1", chainID, from, 1, blockHeight)
	if err != nil {
		t.Errorf("Nonce 1 should succeed: %v", err)
	}

	// Submit with nonce 2 (valid increment)
	err = detector.CheckReplayV3("tx2", chainID, from, 2, blockHeight+1)
	if err != nil {
		t.Errorf("Nonce 2 should succeed: %v", err)
	}

	// Submit with nonce 5 (valid but larger gap)
	err = detector.CheckReplayV3("tx3", chainID, from, 5, blockHeight+2)
	if err != nil {
		t.Errorf("Nonce 5 should succeed: %v", err)
	}
}

func TestReplayDetectorV3_CheckReplayV3_ExcessiveNonceGap(t *testing.T) {
	config := DefaultReplayProtectionConfig()
	config.MaxNonceGap = 10 // Small gap for testing

	detector := NewReplayDetectorV3("thrylos-mainnet", config, nil)
	defer detector.Stop()

	chainID := "thrylos-mainnet"
	from := "addr1"
	blockHeight := int64(100)

	// Submit transaction with nonce 1
	err := detector.CheckReplayV3("tx1", chainID, from, 1, blockHeight)
	if err != nil {
		t.Errorf("Nonce 1 should succeed: %v", err)
	}

	// Try to submit with nonce 50 (exceeds max gap of 10)
	err = detector.CheckReplayV3("tx2", chainID, from, 50, blockHeight+1)
	if err == nil {
		t.Error("Expected excessive nonce gap error")
	}

	// Verify nonce manipulation was recorded
	if detector.metrics.NonceManipulationAttempts == 0 {
		t.Error("Expected nonce manipulation to be recorded")
	}
}

func TestReplayDetectorV3_CheckReorgDepth(t *testing.T) {
	config := DefaultReplayProtectionConfig()
	config.MaxReorgDepth = 100

	detector := NewReplayDetectorV3("thrylos-mainnet", config, nil)
	defer detector.Stop()

	tests := []struct {
		name            string
		txBlockHeight   int64
		currentHeight   int64
		wantErr         bool
		expectDeepReorg bool
	}{
		{
			name:            "within reorg depth",
			txBlockHeight:   100,
			currentHeight:   150,
			wantErr:         false,
			expectDeepReorg: false,
		},
		{
			name:            "at reorg depth limit",
			txBlockHeight:   100,
			currentHeight:   200,
			wantErr:         false,
			expectDeepReorg: false,
		},
		{
			name:            "exceeds reorg depth",
			txBlockHeight:   100,
			currentHeight:   201,
			wantErr:         true,
			expectDeepReorg: true,
		},
		{
			name:            "far exceeds reorg depth",
			txBlockHeight:   100,
			currentHeight:   1000,
			wantErr:         true,
			expectDeepReorg: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initialDeepReorg := detector.metrics.DeepReorgDetected

			err := detector.CheckReorgDepth(tt.txBlockHeight, tt.currentHeight)
			if (err != nil) != tt.wantErr {
				t.Errorf("CheckReorgDepth() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.expectDeepReorg {
				if detector.metrics.DeepReorgDetected == initialDeepReorg {
					t.Error("Expected deep reorg metric to be incremented")
				}
			}
		})
	}
}

func TestReplayDetectorV3_GetNonce(t *testing.T) {
	detector := NewReplayDetectorV3("thrylos-mainnet", nil, nil)
	defer detector.Stop()

	from := "addr1"

	// Initially, no nonce should exist
	_, exists := detector.GetNonce(from)
	if exists {
		t.Error("Expected no nonce for new account")
	}

	// Submit a transaction
	err := detector.CheckReplayV3("tx1", "thrylos-mainnet", from, 5, 100)
	if err != nil {
		t.Errorf("Transaction should succeed: %v", err)
	}

	// Now nonce should exist
	nonce, exists := detector.GetNonce(from)
	if !exists {
		t.Error("Expected nonce to exist after transaction")
	}
	if nonce != 5 {
		t.Errorf("Expected nonce 5, got %d", nonce)
	}
}

// ============================================================================
// M-1 FIX: INTEGRATION TESTS FOR REORG SCENARIOS
// ============================================================================

func TestReplayDetectorV3_Integration_SimpleReorg(t *testing.T) {
	detector := NewReplayDetectorV3("thrylos-mainnet", nil, nil)
	defer detector.Stop()

	chainID := "thrylos-mainnet"
	from := "addr1"

	// Step 1: Process transaction at block 100
	err := detector.CheckReplayV3("tx1", chainID, from, 1, 100)
	if err != nil {
		t.Fatalf("First transaction should succeed: %v", err)
	}

	// Step 2: Simulate reorg back to block 95
	// Chain reorganizes, but our cache still has tx1

	// Step 3: Attacker tries to replay tx1
	err = detector.CheckReplayV3("tx1", chainID, from, 2, 96)
	if err == nil {
		t.Error("Replay attack should be detected (cache hit)")
	}

	// Verify it was recorded as reorg replay attempt
	if detector.metrics.ReorgReplayAttempts == 0 {
		t.Error("Expected reorg replay attempt to be recorded")
	}
}

func TestReplayDetectorV3_Integration_DeepReorgWithOldTransaction(t *testing.T) {
	config := DefaultReplayProtectionConfig()
	config.MaxReorgDepth = 100

	detector := NewReplayDetectorV3("thrylos-mainnet", config, nil)
	defer detector.Stop()

	chainID := "thrylos-mainnet"
	from := "addr1"

	// Process transaction at block 100
	err := detector.CheckReplayV3("tx1", chainID, from, 1, 100)
	if err != nil {
		t.Fatalf("First transaction should succeed: %v", err)
	}

	// Simulate deep reorg: current block is now 250
	// Transaction is now 150 blocks old (exceeds MaxReorgDepth of 100)
	err = detector.CheckReorgDepth(100, 250)
	if err == nil {
		t.Error("Transaction should be rejected due to reorg depth")
	}

	// Verify deep reorg was recorded
	if detector.metrics.DeepReorgDetected == 0 {
		t.Error("Expected deep reorg to be recorded")
	}
}

func TestReplayDetectorV3_Integration_MultipleAccountsAfterReorg(t *testing.T) {
	detector := NewReplayDetectorV3("thrylos-mainnet", nil, nil)
	defer detector.Stop()

	chainID := "thrylos-mainnet"
	blockHeight := int64(100)

	// Process transactions from multiple accounts
	accounts := []string{"addr1", "addr2", "addr3"}
	for i, account := range accounts {
		err := detector.CheckReplayV3("tx"+string(rune(i)), chainID, account, uint64(i+1), blockHeight)
		if err != nil {
			t.Errorf("Transaction for %s should succeed: %v", account, err)
		}
	}

	// Simulate reorg - try to replay with lower nonces
	for i, account := range accounts {
		err := detector.CheckReplayV3("tx_replay"+string(rune(i)), chainID, account, uint64(i), blockHeight+1)
		if err == nil {
			t.Errorf("Replay for %s should be rejected (nonce too low)", account)
		}
	}

	// Verify all were recorded as nonce manipulation
	if detector.metrics.NonceManipulationAttempts != 3 {
		t.Errorf("Expected 3 nonce manipulation attempts, got %d", detector.metrics.NonceManipulationAttempts)
	}
}

func TestReplayDetectorV3_Integration_CacheExpirationDuringReorg(t *testing.T) {
	config := DefaultReplayProtectionConfig()
	config.TransactionTimeoutSeconds = 1 // 1 second for fast test

	detector := NewReplayDetectorV3("thrylos-mainnet", config, nil)
	defer detector.Stop()

	chainID := "thrylos-mainnet"
	from := "addr1"
	blockHeight := int64(100)

	// Process transaction
	err := detector.CheckReplayV3("tx1", chainID, from, 1, blockHeight)
	if err != nil {
		t.Fatalf("First transaction should succeed: %v", err)
	}

	// Wait for cache to expire
	time.Sleep(2 * time.Second)

	// After expiration, cache won't block, but nonce still will
	err = detector.CheckReplayV3("tx1", chainID, from, 1, blockHeight+10)
	if err == nil {
		t.Error("Should still be rejected due to nonce check (even though cache expired)")
	}
}

// ============================================================================
// INTEGRATION TESTS (EXISTING)
// ============================================================================

func TestReplayProtection_Integration_ChainReorg(t *testing.T) {
	config := DefaultReplayProtectionConfig()

	// Simulate transaction during block 100
	rp := &ReplayProtection{
		ChainID:              "thrylos-mainnet",
		FinalizedBlockHash:   "block100hash",
		FinalizedBlockHeight: 100,
		Timestamp:            time.Now().Unix(),
		ShardID:              "shard-1",
	}

	// Validate at block 150 - should pass
	if err := rp.Validate(config, 150); err != nil {
		t.Errorf("Validation at block 150 should pass: %v", err)
	}

	// Simulate chain reorg - transaction references old finalized block
	// At block 1150, transaction is too old
	if err := rp.Validate(config, 1150); err == nil {
		t.Error("Expected validation to fail for old transaction after reorg")
	}
}

func TestReplayProtection_Integration_CrossShardAttack(t *testing.T) {
	metrics := &ReplayProtectionMetrics{}

	// Transaction from shard-1
	txShardID := "shard-1"
	expectedShardID := "shard-2"

	// Attempt cross-shard replay
	err := ValidateCrossShardReplay(txShardID, expectedShardID)
	if err == nil {
		t.Error("Expected cross-shard replay to be detected")
	}

	// Record the attempt
	metrics.RecordCrossShardReplayAttempt()

	// Verify metrics
	m := metrics.GetMetrics()
	if m["cross_shard_replay_attempts"].(uint64) == 0 {
		t.Error("Expected cross-shard replay attempt to be recorded")
	}
}

func TestReplayProtection_Integration_NonceManipulation(t *testing.T) {
	metrics := &ReplayProtectionMetrics{}
	maxGap := uint64(100)

	// Attempt to manipulate nonce with large gap
	err := ValidateNonceSequence(200, 1, maxGap)
	if err == nil {
		t.Error("Expected nonce manipulation to be detected")
	}

	// Record the attempt
	metrics.RecordNonceManipulation()

	// Verify metrics
	m := metrics.GetMetrics()
	if m["nonce_manipulation_attempts"].(uint64) == 0 {
		t.Error("Expected nonce manipulation attempt to be recorded")
	}
}

// ============================================================================
// HELPER FUNCTION TESTS
// ============================================================================

func TestValidateChainIDMatch(t *testing.T) {
	tests := []struct {
		name            string
		txChainID       string
		expectedChainID string
		wantErr         bool
	}{
		{
			name:            "exact match",
			txChainID:       "thrylos-mainnet",
			expectedChainID: "thrylos-mainnet",
			wantErr:         false,
		},
		{
			name:            "case insensitive match",
			txChainID:       "Thrylos-MainNet",
			expectedChainID: "thrylos-mainnet",
			wantErr:         false,
		},
		{
			name:            "mismatch",
			txChainID:       "thrylos-testnet",
			expectedChainID: "thrylos-mainnet",
			wantErr:         true,
		},
		{
			name:            "empty tx chain ID",
			txChainID:       "",
			expectedChainID: "thrylos-mainnet",
			wantErr:         true,
		},
		{
			name:            "empty expected chain ID",
			txChainID:       "thrylos-mainnet",
			expectedChainID: "",
			wantErr:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateChainIDMatch(tt.txChainID, tt.expectedChainID)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateChainIDMatch() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateTransactionTimingV3(t *testing.T) {
	config := DefaultReplayProtectionConfig()
	config.TransactionTimeoutSeconds = 60

	now := time.Now().Unix()

	tests := []struct {
		name      string
		timestamp int64
		wantErr   bool
	}{
		{
			name:      "current timestamp",
			timestamp: now,
			wantErr:   false,
		},
		{
			name:      "recent timestamp",
			timestamp: now - 30,
			wantErr:   false,
		},
		{
			name:      "expired timestamp",
			timestamp: now - 120,
			wantErr:   true,
		},
		{
			name:      "future timestamp",
			timestamp: now + 60,
			wantErr:   true,
		},
		{
			name:      "zero timestamp",
			timestamp: 0,
			wantErr:   true,
		},
		{
			name:      "negative timestamp",
			timestamp: -1,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTransactionTimingV3(tt.timestamp, config)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateTransactionTimingV3() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateBlockHeightV3(t *testing.T) {
	config := DefaultReplayProtectionConfig()
	config.TransactionMaxAge = 100

	tests := []struct {
		name          string
		txBlockHeight int64
		currentHeight int64
		wantErr       bool
	}{
		{
			name:          "recent block",
			txBlockHeight: 100,
			currentHeight: 150,
			wantErr:       false,
		},
		{
			name:          "at age limit",
			txBlockHeight: 100,
			currentHeight: 200,
			wantErr:       false,
		},
		{
			name:          "exceeds age limit",
			txBlockHeight: 100,
			currentHeight: 201,
			wantErr:       true,
		},
		{
			name:          "future block",
			txBlockHeight: 200,
			currentHeight: 100,
			wantErr:       true,
		},
		{
			name:          "negative block height",
			txBlockHeight: -1,
			currentHeight: 100,
			wantErr:       true,
		},
		{
			name:          "no current height",
			txBlockHeight: 100,
			currentHeight: 0,
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBlockHeightV3(tt.txBlockHeight, tt.currentHeight, config)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateBlockHeightV3() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// ============================================================================
// BENCHMARK TESTS
// ============================================================================

func BenchmarkReplayProtection_Validate(b *testing.B) {
	config := DefaultReplayProtectionConfig()
	rp := &ReplayProtection{
		ChainID:              "thrylos-mainnet",
		FinalizedBlockHash:   "hash123",
		FinalizedBlockHeight: 100,
		Timestamp:            time.Now().Unix(),
		ShardID:              "shard-1",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = rp.Validate(config, 150)
	}
}

func BenchmarkReplayCache_Add(b *testing.B) {
	cache := NewReplayCache(10000, time.Minute)
	defer cache.Stop()

	expiresAt := time.Now().Add(time.Hour)
	blockHeight := int64(100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Add("tx"+string(rune(i%1000)), uint64(i), "shard-1", expiresAt, blockHeight)
	}
}

func BenchmarkReplayCache_Has(b *testing.B) {
	cache := NewReplayCache(10000, time.Minute)
	defer cache.Stop()

	// Pre-populate cache
	expiresAt := time.Now().Add(time.Hour)
	blockHeight := int64(100)
	for i := 0; i < 1000; i++ {
		cache.Add("tx"+string(rune(i)), uint64(i), "shard-1", expiresAt, blockHeight)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Has("tx" + string(rune(i%1000)))
	}
}

func BenchmarkReplayDetectorV3_CheckReplayV3(b *testing.B) {
	detector := NewReplayDetectorV3("thrylos-mainnet", nil, nil)
	defer detector.Stop()

	chainID := "thrylos-mainnet"
	from := "addr1"
	blockHeight := int64(100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		detector.CheckReplayV3("tx"+string(rune(i)), chainID, from, uint64(i), blockHeight+int64(i))
	}
}
