package transaction

import (
	"strings"
	"testing"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/proto/core"
)

// mockStakingStateReader is a specific mock for staking tests
// It simulates a wealthy account to ensure failures are due to Limits, not Balance.
type mockStakingStateReader struct {
	accounts map[string]*core.Account
}

func (m *mockStakingStateReader) GetAccount(address string) (*core.Account, error) {
	if acc, exists := m.accounts[address]; exists {
		return acc, nil
	}
	// Return a "Whale" account by default so we don't fail on balance checks
	// Balance: 1,000,000 THRYLOS
	return &core.Account{
		Address:      address,
		Balance:      1_000_000 * 1_000_000_000,
		Nonce:        0,
		StakedAmount: 5000 * 1_000_000_000, // Already staked 5000 for unstake tests
		DelegatedTo: map[string]int64{
			"tl1validator": 100 * 1_000_000_000, // Delegated 100 for undelegate tests
		},
	}, nil
}

// Helper to setup a Validator with the SECURE economics (2,500 Min Stake)
func setupSecureValidator() (*Validator, *config.Config) {
	baseUnit := int64(1_000_000_000)

	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: config.TestnetChainID,
		},
		Economics: config.EconomicsConfig{
			// Basic Fees
			BaseGasPrice: 10,
			MinGasLimit:  21000,
			MaxGasPerTx:  10_000_000,
			MaxGasPrice:  10000,

			// STAKING LIMITS (The Key Test Targets)
			MinStake:      baseUnit * 2500, // 2,500 THRYLOS (Validator Stake)
			MinDelegation: baseUnit / 10,   // 0.1 THRYLOS
		},
		Consensus: config.ConsensusConfig{
			MaxTimestampSkew: 15 * time.Second,
			MaxTimestampAge:  2 * time.Hour,
		},
	}

	return NewValidator(account.ShardID(0), 1, cfg), cfg
}

// Test 1: Verify the Security Barrier (Sybil Attack Protection)
// Should REJECT a stake of 2,499 THRYLOS
func TestStakeBelowSecurityMinimum(t *testing.T) {
	validator, cfg := setupSecureValidator()

	// Create keys
	privKey, _ := crypto.NewPrivateKey()
	addr, _ := account.GenerateAddress(privKey.PublicKey())

	// Amount: 2,499 THRYLOS (Just below the 2,500 limit)
	// FIXED: Explicitly cast to int64
	amount := int64(2500*1_000_000_000) - 1

	tx, err := validator.CreateTransaction(
		addr,
		"", // No recipient for staking
		amount,
		50000, // Gas
		10,    // Gas Price
		0,
		core.TransactionType_STAKE,
		nil,
	)
	if err != nil {
		t.Fatalf("Failed to create tx: %v", err)
	}

	// Validate
	reader := &mockStakingStateReader{accounts: make(map[string]*core.Account)}
	err = validator.ValidateTransaction(tx, reader)

	if err == nil {
		t.Fatal("❌ Security Risk: Accepted stake below 2,500 THRYLOS minimum!")
	}

	if !strings.Contains(err.Error(), "below minimum") {
		t.Errorf("Expected 'below minimum' error, got: %v", err)
	}

	t.Logf("✅ Successfully blocked Sybil attack attempt (Stake: %d < Limit: %d)",
		amount, cfg.Economics.MinStake)
}

// Test 2: Verify Exact Minimum Stake
// Should ACCEPT a stake of 2,500 THRYLOS
func TestStakeAtSecurityMinimum(t *testing.T) {
	validator, _ := setupSecureValidator()

	privKey, _ := crypto.NewPrivateKey()
	addr, _ := account.GenerateAddress(privKey.PublicKey())

	// Amount: Exactly 2,500 THRYLOS
	amount := int64(2500 * 1_000_000_000)

	tx, err := validator.CreateTransaction(
		addr,
		"",
		amount,
		50000,
		10,
		0,
		core.TransactionType_STAKE,
		nil,
	)
	if err != nil {
		t.Fatalf("Failed to create tx: %v", err)
	}
	validator.SignTransaction(tx, privKey)

	reader := &mockStakingStateReader{accounts: make(map[string]*core.Account)}
	err = validator.ValidateTransaction(tx, reader)

	if err != nil {
		t.Errorf("❌ Rejected valid stake of exactly 2,500 THRYLOS: %v", err)
	} else {
		t.Log("✅ Successfully accepted valid stake of 2,500 THRYLOS")
	}
}

// Test 3: Verify Delegation Minimum
// Should REJECT 0.09 THRYLOS, ACCEPT 0.1 THRYLOS
func TestDelegationMinimum(t *testing.T) {
	validator, cfg := setupSecureValidator()

	privKey, _ := crypto.NewPrivateKey()
	fromAddr, _ := account.GenerateAddress(privKey.PublicKey())

	valKey, _ := crypto.NewPrivateKey()
	valAddr, _ := account.GenerateAddress(valKey.PublicKey())

	// Subtest A: Too Low (0.09)
	t.Run("Delegation Too Low", func(t *testing.T) {
		// FIXED: Explicitly cast to int64
		amount := int64(1_000_000_000/10) - 1 // < 0.1

		tx, _ := validator.CreateTransaction(
			fromAddr, valAddr, amount, 50000, 10, 0,
			core.TransactionType_DELEGATE, nil,
		)

		reader := &mockStakingStateReader{accounts: make(map[string]*core.Account)}
		err := validator.ValidateTransaction(tx, reader)

		if err == nil {
			t.Error("❌ Security Risk: Accepted delegation below 0.1 minimum")
		} else {
			t.Logf("✅ Correctly rejected delegation of %d (Min: %d)", amount, cfg.Economics.MinDelegation)
		}
	})

	// Subtest B: Valid (0.1)
	t.Run("Delegation Valid", func(t *testing.T) {
		amount := int64(1_000_000_000 / 10) // Exactly 0.1

		tx, _ := validator.CreateTransaction(
			fromAddr, valAddr, amount, 50000, 10, 0,
			core.TransactionType_DELEGATE, nil,
		)
		validator.SignTransaction(tx, privKey)

		reader := &mockStakingStateReader{accounts: make(map[string]*core.Account)}
		err := validator.ValidateTransaction(tx, reader)

		if err != nil {
			t.Errorf("❌ Rejected valid delegation: %v", err)
		} else {
			t.Log("✅ Accepted valid delegation of 0.1 THRYLOS")
		}
	})
}

// Test 4: Prevent Self-Delegation Loop
// Should REJECT if From == To in delegation
func TestSelfDelegationLoop(t *testing.T) {
	validator, _ := setupSecureValidator()

	privKey, _ := crypto.NewPrivateKey()
	addr, _ := account.GenerateAddress(privKey.PublicKey())

	amount := int64(1_000_000_000)

	tx, _ := validator.CreateTransaction(
		addr, addr, // From == To
		amount, 50000, 10, 0,
		core.TransactionType_DELEGATE, nil,
	)

	// FIX: Sign the transaction so it passes structure validation
	if err := validator.SignTransaction(tx, privKey); err != nil {
		t.Fatalf("Failed to sign transaction: %v", err)
	}

	reader := &mockStakingStateReader{accounts: make(map[string]*core.Account)}
	err := validator.ValidateTransaction(tx, reader)

	if err == nil {
		t.Error("❌ Logic Error: Accepted self-delegation")
	} else if !strings.Contains(err.Error(), "cannot delegate to self") {
		t.Errorf("Expected 'cannot delegate to self' error, got: %v", err)
	} else {
		t.Log("✅ Correctly blocked self-delegation")
	}
}

// Test 5: Verify Unstaking Checks
// Should REJECT if trying to unstake more than staked
func TestUnstakeOverflow(t *testing.T) {
	validator, _ := setupSecureValidator()

	privKey, _ := crypto.NewPrivateKey()
	addr, _ := account.GenerateAddress(privKey.PublicKey())

	// Mock reader returns account with 5,000 staked
	reader := &mockStakingStateReader{accounts: make(map[string]*core.Account)}

	// Try to unstake 6,000
	amount := int64(6000 * 1_000_000_000)

	tx, _ := validator.CreateTransaction(
		addr, "",
		amount, 50000, 10, 0,
		core.TransactionType_UNSTAKE, nil,
	)

	// FIX: Sign the transaction so it passes structure validation
	if err := validator.SignTransaction(tx, privKey); err != nil {
		t.Fatalf("Failed to sign transaction: %v", err)
	}

	err := validator.ValidateTransaction(tx, reader)

	if err == nil {
		t.Error("❌ Logic Error: Allowed unstaking more than available stake")
	} else if !strings.Contains(err.Error(), "insufficient staked amount") {
		t.Errorf("Expected 'insufficient staked amount', got: %v", err)
	} else {
		t.Log("✅ Correctly rejected unstake overflow")
	}
}
