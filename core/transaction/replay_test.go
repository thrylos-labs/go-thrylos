// core/transaction/replay_test.go
// Comprehensive tests for replay protection
// AUDIT FIX: CertiK Audit Finding #2 - Test Coverage

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
				ShardID:              "shard-1", // Added ShardID
			},
			wantErr: false,
		},
		{
			name: "missing chain ID",
			rp: &ReplayProtection{
				FinalizedBlockHash:   "hash123",
				FinalizedBlockHeight: 100,
				ShardID:              "shard-1", // Added ShardID
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
				ShardID:              "shard-1", // Added ShardID
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
				ShardID:              "shard-1", // Added ShardID
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

// ============================================================================
// REPLAY CACHE TESTS
// ============================================================================

func TestReplayCache_AddAndHas(t *testing.T) {
	cache := NewReplayCache(1000, time.Minute)
	defer cache.Stop()

	txHash := "tx123"
	expiresAt := time.Now().Add(time.Hour)

	// Add transaction
	cache.Add(txHash, 1, "shard-1", expiresAt)

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

	// Add transaction
	cache.Add(txHash, 1, "shard-1", expiresAt)

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

	// Add transaction
	cache.Add(txHash, nonce, shardID, expiresAt)

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
		cache.Add("tx"+string(rune(i)), uint64(i), "shard-1", time.Now().Add(time.Hour))
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
// INTEGRATION TESTS
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
	metrics := &ReplayProtectionMetrics{} // ✅ Removed unused config

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

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Add("tx"+string(rune(i%1000)), uint64(i), "shard-1", expiresAt)
	}
}

func BenchmarkReplayCache_Has(b *testing.B) {
	cache := NewReplayCache(10000, time.Minute)
	defer cache.Stop()

	// Pre-populate cache
	expiresAt := time.Now().Add(time.Hour)
	for i := 0; i < 1000; i++ {
		cache.Add("tx"+string(rune(i)), uint64(i), "shard-1", expiresAt)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Has("tx" + string(rune(i%1000)))
	}
}
