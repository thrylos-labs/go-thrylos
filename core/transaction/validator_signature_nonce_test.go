// validator_signature_nonce_test.go
// Tests for signature verification and nonce validation security fixes

package transaction

import (
	"testing"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/proto/core"
)

// TestSignatureVerification tests the comprehensive signature verification
func TestSignatureVerification(t *testing.T) {
	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: "thrylos-testnet-1",
		},
		Economics: config.EconomicsConfig{
			MinTransfer:  1,
			BaseGasPrice: 1,
		},
		Consensus: config.ConsensusConfig{
			MaxTimestampSkew: 5 * time.Minute,
			MaxTimestampAge:  1 * time.Hour,
		},
	}

	validator := NewValidator(account.ShardID(0), 1, cfg)

	// Generate a key pair for Alice
	alicePrivKey, err := crypto.NewPrivateKey()
	if err != nil {
		t.Fatalf("Failed to generate Alice's private key: %v", err)
	}
	alicePubKey := alicePrivKey.PublicKey()
	aliceAddress, err := account.GenerateAddress(alicePubKey)
	if err != nil {
		t.Fatalf("Failed to generate Alice's address: %v", err)
	}

	// Generate a key pair for Bob (attacker)
	bobPrivKey, err := crypto.NewPrivateKey()
	if err != nil {
		t.Fatalf("Failed to generate Bob's private key: %v", err)
	}
	bobPubKey := bobPrivKey.PublicKey()
	bobAddress, err := account.GenerateAddress(bobPubKey)
	if err != nil {
		t.Fatalf("Failed to generate Bob's address: %v", err)
	}

	tests := []struct {
		name           string
		setupTx        func() *core.Transaction
		signWithKey    crypto.PrivateKey
		verifyWithKey  crypto.PublicKey
		wantError      bool
		errorSubstring string
	}{
		{
			name: "Valid signature with correct key",
			setupTx: func() *core.Transaction {
				tx, err := validator.CreateTransaction(
					aliceAddress,
					bobAddress, // Use valid Bob address
					1000,
					21000,
					1,
					0,
					core.TransactionType_TRANSFER,
					nil,
				)
				if err != nil {
					t.Fatalf("Failed to create transaction: %v", err)
				}
				return tx
			},
			signWithKey:   alicePrivKey,
			verifyWithKey: alicePubKey,
			wantError:     false,
		},
		{
			name: "Attack: Using someone else's signature",
			setupTx: func() *core.Transaction {
				// Create transaction from Alice's address
				tx, err := validator.CreateTransaction(
					aliceAddress,
					bobAddress, // Use valid Bob address
					1000,
					21000,
					1,
					0,
					core.TransactionType_TRANSFER,
					nil,
				)
				if err != nil {
					t.Fatalf("Failed to create transaction: %v", err)
				}
				return tx
			},
			signWithKey:    bobPrivKey, // Bob signs it
			verifyWithKey:  bobPubKey,  // Try to verify with Bob's key
			wantError:      true,
			errorSubstring: "sender address", // Should detect address mismatch
		},
		{
			name: "Attack: Modified transaction after signing",
			setupTx: func() *core.Transaction {
				tx, err := validator.CreateTransaction(
					aliceAddress,
					bobAddress, // Use valid Bob address
					1000,
					21000,
					1,
					0,
					core.TransactionType_TRANSFER,
					nil,
				)
				if err != nil {
					t.Fatalf("Failed to create transaction: %v", err)
				}
				// Sign with Alice's key
				err = validator.SignTransaction(tx, alicePrivKey)
				if err != nil {
					t.Fatalf("Failed to sign transaction: %v", err)
				}

				// Attacker modifies amount after signing
				tx.Amount = 1000000 // Changed from 1000 to 1000000

				return tx
			},
			signWithKey:    alicePrivKey,
			verifyWithKey:  alicePubKey,
			wantError:      true,
			errorSubstring: "verification failed", // Signature won't match modified data
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := tt.setupTx()

			// Sign the transaction (unless already signed in setupTx)
			if len(tx.Signature) == 0 {
				err := validator.SignTransaction(tx, tt.signWithKey)
				if err != nil {
					t.Fatalf("Failed to sign transaction: %v", err)
				}
			}

			// Verify the signature
			err := validator.VerifyTransactionSignature(tx, tt.verifyWithKey)

			if tt.wantError && err == nil {
				t.Errorf("Expected error containing '%s', but got none", tt.errorSubstring)
			}

			if !tt.wantError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if tt.wantError && err != nil {
				// Check if error contains expected substring
				if tt.errorSubstring != "" && !containsSubstring(err.Error(), tt.errorSubstring) {
					t.Errorf("Expected error containing '%s', got: %v", tt.errorSubstring, err)
				}
			}
		})
	}
}

// TestChainIDInSignature tests that chain ID is included in signature
// This prevents replay attacks across different chains
func TestChainIDInSignature(t *testing.T) {
	// Create two validators with different chain IDs
	cfgTestnet := &config.Config{
		Network: config.NetworkConfig{
			ChainID: "thrylos-testnet-1",
		},
		Economics: config.EconomicsConfig{
			MinTransfer:  1,
			BaseGasPrice: 1,
		},
		Consensus: config.ConsensusConfig{
			MaxTimestampSkew: 5 * time.Minute,
			MaxTimestampAge:  1 * time.Hour,
		},
	}

	cfgMainnet := &config.Config{
		Network: config.NetworkConfig{
			ChainID: "thrylos-mainnet-1",
		},
		Economics: config.EconomicsConfig{
			MinTransfer:  1,
			BaseGasPrice: 1,
		},
		Consensus: config.ConsensusConfig{
			MaxTimestampSkew: 5 * time.Minute,
			MaxTimestampAge:  1 * time.Hour,
		},
	}

	validatorTestnet := NewValidator(account.ShardID(0), 1, cfgTestnet)
	validatorMainnet := NewValidator(account.ShardID(0), 1, cfgMainnet)

	// Generate key pair
	privKey, err := crypto.NewPrivateKey()
	if err != nil {
		t.Fatalf("Failed to generate private key: %v", err)
	}
	pubKey := privKey.PublicKey()
	address, err := account.GenerateAddress(pubKey)
	if err != nil {
		t.Fatalf("Failed to generate address: %v", err)
	}

	// Generate recipient address
	recipientPrivKey, err := crypto.NewPrivateKey()
	if err != nil {
		t.Fatalf("Failed to generate recipient private key: %v", err)
	}
	recipientPubKey := recipientPrivKey.PublicKey()
	recipientAddress, err := account.GenerateAddress(recipientPubKey)
	if err != nil {
		t.Fatalf("Failed to generate recipient address: %v", err)
	}

	// Create and sign transaction on testnet
	txTestnet, err := validatorTestnet.CreateTransaction(
		address,
		recipientAddress, // Use valid recipient address
		1000,
		21000,
		1,
		0,
		core.TransactionType_TRANSFER,
		nil,
	)
	if err != nil {
		t.Fatalf("Failed to create testnet transaction: %v", err)
	}

	err = validatorTestnet.SignTransaction(txTestnet, privKey)
	if err != nil {
		t.Fatalf("Failed to sign testnet transaction: %v", err)
	}

	// Signature should be valid on testnet
	err = validatorTestnet.VerifyTransactionSignature(txTestnet, pubKey)
	if err != nil {
		t.Errorf("Testnet signature should be valid on testnet, got error: %v", err)
	}

	// Signature should be INVALID on mainnet (different chain ID)
	err = validatorMainnet.VerifyTransactionSignature(txTestnet, pubKey)
	if err == nil {
		t.Error("Expected error when verifying testnet signature on mainnet, but got none (REPLAY ATTACK POSSIBLE!)")
	}
}

// TestNonceValidation tests comprehensive nonce validation
func TestNonceValidation(t *testing.T) {
	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: "thrylos-testnet-1",
		},
	}

	validator := NewValidator(account.ShardID(0), 1, cfg)

	tests := []struct {
		name           string
		txNonce        uint64
		accountNonce   uint64
		address        string
		wantError      bool
		errorSubstring string
	}{
		{
			name:         "Valid nonce - exact match",
			txNonce:      5,
			accountNonce: 5,
			address:      "tl1testaddress",
			wantError:    false,
		},
		{
			name:           "Replay attack - nonce too low",
			txNonce:        3,
			accountNonce:   5,
			address:        "tl1testaddress",
			wantError:      true,
			errorSubstring: "nonce too low",
		},
		{
			name:           "Replay attack - already processed",
			txNonce:        0,
			accountNonce:   5,
			address:        "tl1testaddress",
			wantError:      true,
			errorSubstring: "already processed",
		},
		{
			name:           "Nonce gap - small (should queue)",
			txNonce:        7,
			accountNonce:   5,
			address:        "tl1testaddress",
			wantError:      true,
			errorSubstring: "future processing",
		},
		{
			name:           "Nonce gap - too large",
			txNonce:        1005,
			accountNonce:   0,
			address:        "tl1testaddress",
			wantError:      true,
			errorSubstring: "exceeds maximum allowed gap",
		},
		{
			name:           "Nonce exactly at max gap",
			txNonce:        1000,
			accountNonce:   0,
			address:        "tl1testaddress",
			wantError:      true,
			errorSubstring: "future processing",
		},
		{
			name:           "Nonce just over max gap",
			txNonce:        1001,
			accountNonce:   0,
			address:        "tl1testaddress",
			wantError:      true,
			errorSubstring: "exceeds maximum allowed gap",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.validateNonce(tt.txNonce, tt.accountNonce, tt.address)

			if tt.wantError && err == nil {
				t.Errorf("Expected error containing '%s', but got none", tt.errorSubstring)
			}

			if !tt.wantError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if tt.wantError && err != nil {
				if tt.errorSubstring != "" && !containsSubstring(err.Error(), tt.errorSubstring) {
					t.Errorf("Expected error containing '%s', got: %v", tt.errorSubstring, err)
				}
			}
		})
	}
}

// TestMempoolNonceValidation tests mempool-specific nonce validation
func TestMempoolNonceValidation(t *testing.T) {
	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: "thrylos-testnet-1",
		},
		Economics: config.EconomicsConfig{
			MinTransfer:  1,
			BaseGasPrice: 1,
		},
		Consensus: config.ConsensusConfig{
			MaxTimestampSkew: 5 * time.Minute,
			MaxTimestampAge:  1 * time.Hour,
		},
	}

	validator := NewValidator(account.ShardID(0), 1, cfg)

	// Create mock account manager with correct signature
	accountManager := account.NewAccountManager(account.ShardID(0), 1)

	// Generate valid test address
	testPrivKey, err := crypto.NewPrivateKey()
	if err != nil {
		t.Fatalf("Failed to generate test private key: %v", err)
	}
	testPubKey := testPrivKey.PublicKey()
	testAddress, err := account.GenerateAddress(testPubKey)
	if err != nil {
		t.Fatalf("Failed to generate test address: %v", err)
	}

	// Generate valid recipient address
	recipientPrivKey, err := crypto.NewPrivateKey()
	if err != nil {
		t.Fatalf("Failed to generate recipient private key: %v", err)
	}
	recipientPubKey := recipientPrivKey.PublicKey()
	recipientAddress, err := account.GenerateAddress(recipientPubKey)
	if err != nil {
		t.Fatalf("Failed to generate recipient address: %v", err)
	}

	// Create test account with balance and nonce
	testAccount := &core.Account{
		Address: testAddress,
		Balance: 1000000,
		Nonce:   5,
	}
	accountManager.UpdateAccount(testAccount)

	tests := []struct {
		name           string
		txNonce        uint64
		wantError      bool
		errorSubstring string
	}{
		{
			name:      "Current nonce - should accept",
			txNonce:   5,
			wantError: false,
		},
		{
			name:      "Next nonce - should accept",
			txNonce:   6,
			wantError: false,
		},
		{
			name:      "Future nonce within limit - should accept",
			txNonce:   50,
			wantError: false,
		},
		{
			name:           "Old nonce - should reject",
			txNonce:        4,
			wantError:      true,
			errorSubstring: "nonce too old",
		},
		{
			name:           "Nonce too far in future - should reject",
			txNonce:        106, // 5 + 100 + 1
			wantError:      true,
			errorSubstring: "too far in future",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := &core.Transaction{
				From:     testAddress,      // Use valid generated address
				To:       recipientAddress, // Use valid recipient address
				Amount:   1000,
				Gas:      21000,
				GasPrice: 1,
				Nonce:    tt.txNonce,
				Type:     core.TransactionType_TRANSFER,
			}

			err := validator.ValidateForMempool(tx, accountManager)

			if tt.wantError && err == nil {
				t.Errorf("Expected error containing '%s', but got none", tt.errorSubstring)
			}

			if !tt.wantError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if tt.wantError && err != nil {
				if tt.errorSubstring != "" && !containsSubstring(err.Error(), tt.errorSubstring) {
					t.Errorf("Expected error containing '%s', got: %v", tt.errorSubstring, err)
				}
			}
		})
	}
}

// TestSequentialNonceProcessing tests that transactions must be processed in order
func TestSequentialNonceProcessing(t *testing.T) {
	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: "thrylos-testnet-1",
		},
		Economics: config.EconomicsConfig{
			MinTransfer:  1,
			BaseGasPrice: 1,
		},
		Consensus: config.ConsensusConfig{
			MaxTimestampSkew: 5 * time.Minute,
			MaxTimestampAge:  1 * time.Hour,
		},
	}

	validator := NewValidator(account.ShardID(0), 1, cfg)

	// Simulate account state with correct signature
	accountManager := account.NewAccountManager(account.ShardID(0), 1)

	// Generate valid test address
	testPrivKey, err := crypto.NewPrivateKey()
	if err != nil {
		t.Fatalf("Failed to generate test private key: %v", err)
	}
	testPubKey := testPrivKey.PublicKey()
	testAddress, err := account.GenerateAddress(testPubKey)
	if err != nil {
		t.Fatalf("Failed to generate test address: %v", err)
	}

	// Generate valid recipient address
	recipientPrivKey, err := crypto.NewPrivateKey()
	if err != nil {
		t.Fatalf("Failed to generate recipient private key: %v", err)
	}
	recipientPubKey := recipientPrivKey.PublicKey()
	recipientAddress, err := account.GenerateAddress(recipientPubKey)
	if err != nil {
		t.Fatalf("Failed to generate recipient address: %v", err)
	}

	testAccount := &core.Account{
		Address: testAddress,
		Balance: 1000000,
		Nonce:   0, // Starting nonce
	}
	accountManager.UpdateAccount(testAccount)

	// Try to process transaction with nonce 0 when current nonce is 0
	// This should work
	tx1 := &core.Transaction{
		From:     testAddress,
		To:       recipientAddress,
		Amount:   1000,
		Gas:      21000,
		GasPrice: 1,
		Nonce:    0, // Correct next nonce
		Type:     core.TransactionType_TRANSFER,
	}

	err = validator.validateNonce(tx1.Nonce, testAccount.Nonce, tx1.From)
	if err != nil {
		t.Errorf("Transaction with correct nonce should be accepted, got error: %v", err)
	}

	// Try to process transaction with nonce 2 when current nonce is 0
	// This should fail (gap too large without processing nonce 1 first)
	tx2 := &core.Transaction{
		From:     testAddress,
		To:       recipientAddress,
		Amount:   1000,
		Gas:      21000,
		GasPrice: 1,
		Nonce:    2, // Skipping nonce 1
		Type:     core.TransactionType_TRANSFER,
	}

	err = validator.validateNonce(tx2.Nonce, testAccount.Nonce, tx2.From)
	if err == nil {
		t.Error("Transaction with gap in nonce should be rejected for immediate processing")
	}
}

// Helper function to check if error message contains substring
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && findSubstringInString(s, substr)
}

func findSubstringInString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
