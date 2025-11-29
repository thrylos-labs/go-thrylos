// core/transaction/replay_test.go
package transaction

import (
	"testing"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/proto/core"
)

func TestReplayProtection_Validate(t *testing.T) {
	cfg := DefaultReplayProtectionConfig()

	tests := []struct {
		name          string
		rp            *ReplayProtection
		currentHeight int64
		wantErr       bool
		errContains   string
	}{
		{
			name: "valid replay protection",
			rp: &ReplayProtection{
				ChainID:              "thrylos-1",
				FinalizedBlockHash:   "abc123",
				FinalizedBlockHeight: 100,
			},
			currentHeight: 200,
			wantErr:       false,
		},
		{
			name: "missing chain ID",
			rp: &ReplayProtection{
				ChainID:              "",
				FinalizedBlockHash:   "abc123",
				FinalizedBlockHeight: 100,
			},
			currentHeight: 200,
			wantErr:       true,
			errContains:   "chain ID is required",
		},
		{
			name: "missing finalized block hash",
			rp: &ReplayProtection{
				ChainID:              "thrylos-1",
				FinalizedBlockHash:   "",
				FinalizedBlockHeight: 100,
			},
			currentHeight: 200,
			wantErr:       true,
			errContains:   "finalized block hash is required",
		},
		{
			name: "transaction too old",
			rp: &ReplayProtection{
				ChainID:              "thrylos-1",
				FinalizedBlockHash:   "abc123",
				FinalizedBlockHeight: 100,
			},
			currentHeight: 1200, // 1100 blocks later, exceeds max age of 1000
			wantErr:       true,
			errContains:   "transaction too old",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.rp.Validate(cfg, tt.currentHeight)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error containing '%s', got nil", tt.errContains)
				} else if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("Expected error containing '%s', got '%s'", tt.errContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error, got: %v", err)
				}
			}
		})
	}
}

func TestReplayProtection_IsExpired(t *testing.T) {
	tests := []struct {
		name          string
		rp            *ReplayProtection
		currentHeight int64
		maxAge        int64
		want          bool
	}{
		{
			name: "not expired",
			rp: &ReplayProtection{
				FinalizedBlockHeight: 100,
			},
			currentHeight: 200,
			maxAge:        1000,
			want:          false,
		},
		{
			name: "expired",
			rp: &ReplayProtection{
				FinalizedBlockHeight: 100,
			},
			currentHeight: 1200,
			maxAge:        1000,
			want:          true,
		},
		{
			name: "no expiration",
			rp: &ReplayProtection{
				FinalizedBlockHeight: 100,
			},
			currentHeight: 10000,
			maxAge:        0,
			want:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.rp.IsExpired(tt.currentHeight, tt.maxAge)
			if got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSignatureWithReplayProtection(t *testing.T) {
	// Setup
	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: "thrylos-testnet-1",
		},
	}

	validator := NewValidator(account.BeaconShardID, 1, cfg)

	// Generate key pair
	privateKey, err := crypto.NewPrivateKey()
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	// Get the correct address from the public key
	fromAddress, err := account.GenerateAddress(privateKey.PublicKey())
	if err != nil {
		t.Fatalf("Failed to generate address: %v", err)
	}

	// Create test transaction
	tx := &core.Transaction{
		Id:        "test-tx-1",
		From:      fromAddress, // Use generated address
		To:        "tl10987654321fedcba",
		Amount:    1000,
		Gas:       21000,
		GasPrice:  10,
		Nonce:     1,
		Type:      core.TransactionType_TRANSFER,
		Timestamp: 1234567890,
		Data:      []byte{},
	}

	finalizedBlockHash := "finalized-block-abc123"

	// Sign with replay protection
	err = validator.SignTransactionWithReplayProtection(tx, privateKey, finalizedBlockHash)
	if err != nil {
		t.Fatalf("Failed to sign transaction with replay protection: %v", err)
	}

	if len(tx.Signature) == 0 {
		t.Fatal("Signature is empty after signing")
	}

	// Verify with same finalized block hash
	err = validator.VerifyTransactionSignatureWithReplayProtection(tx, privateKey.PublicKey(), finalizedBlockHash)
	if err != nil {
		t.Errorf("Failed to verify transaction with correct finalized block: %v", err)
	}

	// Try to verify with different finalized block hash (simulate replay on different fork)
	differentBlockHash := "different-finalized-block-xyz789"
	err = validator.VerifyTransactionSignatureWithReplayProtection(tx, privateKey.PublicKey(), differentBlockHash)
	if err == nil {
		t.Error("Expected verification to fail with different finalized block hash (replay attack should be detected)")
	}

	// Verify metrics were updated
	metrics := validator.GetReplayProtectionMetrics()
	txWithProtection := metrics["transactions_with_protection"].(uint64)
	if txWithProtection != 1 {
		t.Errorf("Expected 1 transaction with replay protection, got %d", txWithProtection)
	}

	replayAttempts := metrics["replay_attempts_detected"].(uint64)
	if replayAttempts != 1 {
		t.Errorf("Expected 1 replay attempt detected, got %d", replayAttempts)
	}
}

func TestBackwardCompatibility(t *testing.T) {
	// Ensure old signing method still works alongside new method
	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: "thrylos-testnet-1",
		},
	}

	validator := NewValidator(account.BeaconShardID, 1, cfg)

	privateKey, err := crypto.NewPrivateKey()
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	// Get the correct address from the public key
	fromAddress, err := account.GenerateAddress(privateKey.PublicKey())
	if err != nil {
		t.Fatalf("Failed to generate address: %v", err)
	}

	tx := &core.Transaction{
		Id:        "test-tx-2",
		From:      fromAddress, // Use generated address
		To:        "tl10987654321fedcba",
		Amount:    1000,
		Gas:       21000,
		GasPrice:  10,
		Nonce:     1,
		Type:      core.TransactionType_TRANSFER,
		Timestamp: 1234567890,
		Data:      []byte{},
	}

	// Sign with old method (v1 - no finalized block)
	err = validator.SignTransaction(tx, privateKey)
	if err != nil {
		t.Fatalf("Failed to sign transaction with legacy method: %v", err)
	}

	// Verify with old method should work
	err = validator.VerifyTransactionSignature(tx, privateKey.PublicKey())
	if err != nil {
		t.Errorf("Failed to verify legacy transaction: %v", err)
	}

	// Verify with new method should fail (different hash due to v2 protocol)
	err = validator.VerifyTransactionSignatureWithReplayProtection(tx, privateKey.PublicKey(), "any-block-hash")
	if err == nil {
		t.Error("Expected verification to fail when mixing v1 signature with v2 verification")
	}
}

func TestDevelopmentModeReplayProtection(t *testing.T) {
	// Test that development mode allows empty finalized blocks
	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: "thrylos-devnet-1",
		},
	}

	devConfig := DevelopmentReplayProtectionConfig()
	validator := NewValidatorWithReplayConfig(account.BeaconShardID, 1, cfg, devConfig)

	privateKey, err := crypto.NewPrivateKey()
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	// Get the correct address from the public key
	fromAddress, err := account.GenerateAddress(privateKey.PublicKey())
	if err != nil {
		t.Fatalf("Failed to generate address: %v", err)
	}

	tx := &core.Transaction{
		Id:        "test-tx-dev",
		From:      fromAddress, // Use generated address
		To:        "tl10987654321fedcba",
		Amount:    500,
		Gas:       21000,
		GasPrice:  10,
		Nonce:     1,
		Type:      core.TransactionType_TRANSFER,
		Timestamp: 1234567890,
		Data:      []byte{},
	}

	// Sign with empty finalized block (should work in dev mode)
	err = validator.SignTransactionWithReplayProtection(tx, privateKey, "")
	if err != nil {
		t.Errorf("Dev mode should allow empty finalized block, got error: %v", err)
	}

	// Verify with empty finalized block (should work in dev mode)
	err = validator.VerifyTransactionSignatureWithReplayProtection(tx, privateKey.PublicKey(), "")
	if err != nil {
		t.Errorf("Dev mode verification should work with empty finalized block, got error: %v", err)
	}
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) &&
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
			findInString(s, substr)))
}

func findInString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
