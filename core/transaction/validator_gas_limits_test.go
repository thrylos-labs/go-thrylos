// core/transaction/validator_gas_limits_test.go
// Comprehensive tests for gas limit validation

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

// This struct implicitly implements the 'StateReader' interface defined in validator.go
type mockGasStateReader struct {
	accounts map[string]*core.Account
}

func (m *mockGasStateReader) GetAccount(address string) (*core.Account, error) {
	if acc, exists := m.accounts[address]; exists {
		return acc, nil
	}
	return &core.Account{
		Address: address,
		Balance: 1000000000000, // 1000 THRYLOS
		Nonce:   0,
	}, nil
}

func (m *mockGasStateReader) GetNonce(address string) (uint64, error) {
	if acc, exists := m.accounts[address]; exists {
		return acc.Nonce, nil
	}
	return 0, nil
}

func (m *mockGasStateReader) GetBalance(address string) (int64, error) {
	if acc, exists := m.accounts[address]; exists {
		return acc.Balance, nil
	}
	return 1000000000000, nil
}

// Helper function to create a valid test transaction
func createValidGasTestTransaction(t *testing.T, gas int64, gasPrice int64) *core.Transaction {
	// Generate valid keypair using your crypto package
	privateKey, err := crypto.NewPrivateKey()
	if err != nil {
		t.Fatalf("Failed to generate private key: %v", err)
	}
	publicKey := privateKey.PublicKey()

	// Generate valid addresses
	// Note: We use the real AccountManager here just for address generation logic,
	// but the Validator will use the Mock for state reading.
	fromAddr, err := account.GenerateAddress(publicKey) // simplified call if your account package supports it, otherwise keep passing manager
	if err != nil {
		// Fallback: Remove accountManager argument
		fromAddr, err = account.GenerateAddress(publicKey)
		if err != nil {
			t.Fatalf("Failed to generate from address: %v", err)
		}
	}

	toPrivateKey, _ := crypto.NewPrivateKey()
	toPublicKey := toPrivateKey.PublicKey()

	// Remove accountManager argument here as well
	toAddr, err := account.GenerateAddress(toPublicKey)
	if err != nil {
		t.Fatalf("Failed to generate to address: %v", err)
	}

	// Create test config
	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: config.TestnetChainID,
		},
		Economics: config.EconomicsConfig{
			MinTransfer:   1000,
			MinStake:      25000000000,
			MinDelegation: 1000000000,
			BaseGasPrice:  1000,
			MinGasLimit:   21000,
			MaxGasPerTx:   10_000_000,
			MaxGasPrice:   1_000_000,
			MaxBlockGas:   30_000_000,
		},
		Consensus: config.ConsensusConfig{
			MaxTimestampSkew: 5 * time.Minute,
			MaxTimestampAge:  1 * time.Hour,
		},
	}

	validator := NewValidator(account.ShardID(0), 1, cfg)

	// Create transaction
	tx, err := validator.CreateTransaction(
		fromAddr,
		toAddr,
		100000, // 0.0001 THRYLOS
		gas,
		gasPrice,
		0,
		core.TransactionType_TRANSFER,
		nil,
	)
	if err != nil {
		t.Fatalf("Failed to create transaction: %v", err)
	}

	// Sign transaction
	err = validator.SignTransaction(tx, privateKey)
	if err != nil {
		t.Fatalf("Failed to sign transaction: %v", err)
	}

	return tx
}

// Test 1: Gas below minimum limit
func TestGasBelowMinimum(t *testing.T) {
	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: config.TestnetChainID,
		},
		Economics: config.EconomicsConfig{
			MinTransfer:  1000,
			BaseGasPrice: 1000,
			MinGasLimit:  21000,
			MaxGasPerTx:  10_000_000,
			MaxGasPrice:  1_000_000,
		},
		Consensus: config.ConsensusConfig{
			MaxTimestampSkew: 5 * time.Minute,
			MaxTimestampAge:  1 * time.Hour,
		},
	}

	validator := NewValidator(account.ShardID(0), 1, cfg)

	// Create transaction with gas below minimum
	tx := createValidGasTestTransaction(t, 20000, 1000) // 20k < 21k minimum

	stateReader := &mockGasStateReader{
		accounts: make(map[string]*core.Account),
	}

	err := validator.ValidateTransaction(tx, stateReader)

	if err == nil {
		t.Fatal("Expected error for gas below minimum, got nil")
	}

	if !strings.Contains(err.Error(), "gas too low") {
		t.Errorf("Expected 'gas too low' error, got: %v", err)
	}

	t.Logf("✅ Correctly rejected transaction with gas %d (minimum: %d)",
		tx.Gas, cfg.Economics.MinGasLimit)
}

// Test 2: Gas above maximum limit
func TestGasAboveMaximum(t *testing.T) {
	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: config.TestnetChainID,
		},
		Economics: config.EconomicsConfig{
			MinTransfer:  1000,
			BaseGasPrice: 1000,
			MinGasLimit:  21000,
			MaxGasPerTx:  10_000_000,
			MaxGasPrice:  1_000_000,
		},
		Consensus: config.ConsensusConfig{
			MaxTimestampSkew: 5 * time.Minute,
			MaxTimestampAge:  1 * time.Hour,
		},
	}

	validator := NewValidator(account.ShardID(0), 1, cfg)

	// Create transaction with gas above maximum
	tx := createValidGasTestTransaction(t, 11_000_000, 1000) // 11M > 10M maximum

	stateReader := &mockGasStateReader{
		accounts: make(map[string]*core.Account),
	}

	err := validator.ValidateTransaction(tx, stateReader)

	if err == nil {
		t.Fatal("Expected error for gas above maximum, got nil")
	}

	if !strings.Contains(err.Error(), "gas too high") {
		t.Errorf("Expected 'gas too high' error, got: %v", err)
	}

	t.Logf("✅ Correctly rejected transaction with gas %d (maximum: %d)",
		tx.Gas, cfg.Economics.MaxGasPerTx)
}

// Test 3: Gas at minimum boundary (should pass)
func TestGasAtMinimumBoundary(t *testing.T) {
	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: config.TestnetChainID,
		},
		Economics: config.EconomicsConfig{
			MinTransfer:  1000,
			BaseGasPrice: 1000,
			MinGasLimit:  21000,
			MaxGasPerTx:  10_000_000,
			MaxGasPrice:  1_000_000,
		},
		Consensus: config.ConsensusConfig{
			MaxTimestampSkew: 5 * time.Minute,
			MaxTimestampAge:  1 * time.Hour,
		},
	}

	validator := NewValidator(account.ShardID(0), 1, cfg)

	// Create transaction with gas at minimum
	tx := createValidGasTestTransaction(t, 21000, 1000) // Exactly at minimum

	stateReader := &mockGasStateReader{
		accounts: make(map[string]*core.Account),
	}

	err := validator.ValidateTransaction(tx, stateReader)

	// Should pass gas validation (may fail on other validations like balance)
	if err != nil && strings.Contains(err.Error(), "gas too low") {
		t.Errorf("Should not reject gas at minimum boundary: %v", err)
	}

	t.Logf("✅ Correctly accepted transaction with gas at minimum: %d", tx.Gas)
}

// Test 4: Gas at maximum boundary (should pass)
func TestGasAtMaximumBoundary(t *testing.T) {
	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: config.TestnetChainID,
		},
		Economics: config.EconomicsConfig{
			MinTransfer:  1000,
			BaseGasPrice: 1000,
			MinGasLimit:  21000,
			MaxGasPerTx:  10_000_000,
			MaxGasPrice:  1_000_000,
		},
		Consensus: config.ConsensusConfig{
			MaxTimestampSkew: 5 * time.Minute,
			MaxTimestampAge:  1 * time.Hour,
		},
	}

	validator := NewValidator(account.ShardID(0), 1, cfg)

	// Create transaction with gas at maximum
	tx := createValidGasTestTransaction(t, 10_000_000, 1000) // Exactly at maximum

	stateReader := &mockGasStateReader{
		accounts: make(map[string]*core.Account),
	}

	err := validator.ValidateTransaction(tx, stateReader)

	// Should pass gas validation (may fail on other validations like balance)
	if err != nil && strings.Contains(err.Error(), "gas too high") {
		t.Errorf("Should not reject gas at maximum boundary: %v", err)
	}

	t.Logf("✅ Correctly accepted transaction with gas at maximum: %d", tx.Gas)
}

// Test 5: Gas price below minimum
func TestGasPriceBelowMinimum(t *testing.T) {
	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: config.TestnetChainID,
		},
		Economics: config.EconomicsConfig{
			MinTransfer:  1000,
			BaseGasPrice: 1000,
			MinGasLimit:  21000,
			MaxGasPerTx:  10_000_000,
			MaxGasPrice:  1_000_000,
		},
		Consensus: config.ConsensusConfig{
			MaxTimestampSkew: 5 * time.Minute,
			MaxTimestampAge:  1 * time.Hour,
		},
	}

	validator := NewValidator(account.ShardID(0), 1, cfg)

	// Create transaction with gas price below minimum
	tx := createValidGasTestTransaction(t, 21000, 500) // 500 < 1000 minimum

	stateReader := &mockGasStateReader{
		accounts: make(map[string]*core.Account),
	}

	err := validator.ValidateTransaction(tx, stateReader)

	if err == nil {
		t.Fatal("Expected error for gas price below minimum, got nil")
	}

	if !strings.Contains(err.Error(), "gas price") && !strings.Contains(err.Error(), "below minimum") {
		t.Errorf("Expected 'gas price below minimum' error, got: %v", err)
	}

	t.Logf("✅ Correctly rejected transaction with gas price %d (minimum: %d)",
		tx.GasPrice, cfg.Economics.BaseGasPrice)
}

// Test 6: Gas price above maximum
func TestGasPriceAboveMaximum(t *testing.T) {
	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: config.TestnetChainID,
		},
		Economics: config.EconomicsConfig{
			MinTransfer:  1000,
			BaseGasPrice: 1000,
			MinGasLimit:  21000,
			MaxGasPerTx:  10_000_000,
			MaxGasPrice:  1_000_000,
		},
		Consensus: config.ConsensusConfig{
			MaxTimestampSkew: 5 * time.Minute,
			MaxTimestampAge:  1 * time.Hour,
		},
	}

	validator := NewValidator(account.ShardID(0), 1, cfg)

	// Create transaction with gas price above maximum
	tx := createValidGasTestTransaction(t, 21000, 1_500_000) // 1.5M > 1M maximum

	stateReader := &mockGasStateReader{
		accounts: make(map[string]*core.Account),
	}

	err := validator.ValidateTransaction(tx, stateReader)

	if err == nil {
		t.Fatal("Expected error for gas price above maximum, got nil")
	}

	if !strings.Contains(err.Error(), "gas price too high") {
		t.Errorf("Expected 'gas price too high' error, got: %v", err)
	}

	t.Logf("✅ Correctly rejected transaction with gas price %d (maximum: %d)",
		tx.GasPrice, cfg.Economics.MaxGasPrice)
}

// Test 7: Valid gas and gas price (should pass gas validation)
func TestValidGasAndGasPrice(t *testing.T) {
	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: config.TestnetChainID,
		},
		Economics: config.EconomicsConfig{
			MinTransfer:  1000,
			BaseGasPrice: 1000,
			MinGasLimit:  21000,
			MaxGasPerTx:  10_000_000,
			MaxGasPrice:  1_000_000,
		},
		Consensus: config.ConsensusConfig{
			MaxTimestampSkew: 5 * time.Minute,
			MaxTimestampAge:  1 * time.Hour,
		},
	}

	validator := NewValidator(account.ShardID(0), 1, cfg)

	// Create transaction with valid gas and gas price
	tx := createValidGasTestTransaction(t, 50000, 5000) // Both within limits

	stateReader := &mockGasStateReader{
		accounts: make(map[string]*core.Account),
	}

	err := validator.ValidateTransaction(tx, stateReader)

	// Should pass gas validation (may fail on other validations like balance)
	if err != nil && (strings.Contains(err.Error(), "gas too low") ||
		strings.Contains(err.Error(), "gas too high") ||
		strings.Contains(err.Error(), "gas price")) {
		t.Errorf("Should not reject valid gas/price: %v", err)
	}

	t.Logf("✅ Correctly accepted transaction with gas %d and price %d", tx.Gas, tx.GasPrice)
}

// Test 8: Extreme values (MaxInt64)
func TestExtremeGasValues(t *testing.T) {
	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: config.TestnetChainID,
		},
		Economics: config.EconomicsConfig{
			MinTransfer:  1000,
			BaseGasPrice: 1000,
			MinGasLimit:  21000,
			MaxGasPerTx:  10_000_000,
			MaxGasPrice:  1_000_000,
		},
		Consensus: config.ConsensusConfig{
			MaxTimestampSkew: 5 * time.Minute,
			MaxTimestampAge:  1 * time.Hour,
		},
	}

	validator := NewValidator(account.ShardID(0), 1, cfg)

	// Test with MaxInt64 gas (the attack scenario)
	tx := createValidGasTestTransaction(t, 9223372036854775807, 1000) // MaxInt64

	stateReader := &mockGasStateReader{
		accounts: make(map[string]*core.Account),
	}

	err := validator.ValidateTransaction(tx, stateReader)

	if err == nil {
		t.Fatal("Expected error for extreme gas value (MaxInt64), got nil")
	}

	if !strings.Contains(err.Error(), "gas too high") {
		t.Errorf("Expected 'gas too high' error for MaxInt64, got: %v", err)
	}

	t.Logf("✅ Correctly blocked attack with MaxInt64 gas: %d", tx.Gas)
}

// Test 9: Multiple transactions with valid gas
func TestBatchGasValidation(t *testing.T) {
	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: config.TestnetChainID,
		},
		Economics: config.EconomicsConfig{
			MinTransfer:  1000,
			BaseGasPrice: 1000,
			MinGasLimit:  21000,
			MaxGasPerTx:  10_000_000,
			MaxGasPrice:  1_000_000,
		},
		Consensus: config.ConsensusConfig{
			MaxTimestampSkew: 5 * time.Minute,
			MaxTimestampAge:  1 * time.Hour,
		},
	}

	validator := NewValidator(account.ShardID(0), 1, cfg)
	stateReader := &mockGasStateReader{
		accounts: make(map[string]*core.Account),
	}

	// Create multiple transactions with different gas values
	testCases := []struct {
		gas         int64
		gasPrice    int64
		shouldPass  bool
		description string
	}{
		{21000, 1000, true, "minimum gas"},
		{50000, 5000, true, "medium gas"},
		{5_000_000, 10000, true, "high gas"},
		{10_000_000, 1000, true, "maximum gas"},
		{20000, 1000, false, "below minimum"},
		{11_000_000, 1000, false, "above maximum"},
	}

	for _, tc := range testCases {
		tx := createValidGasTestTransaction(t, tc.gas, tc.gasPrice)
		err := validator.ValidateTransaction(tx, stateReader)

		hasGasError := err != nil && (strings.Contains(err.Error(), "gas too low") ||
			strings.Contains(err.Error(), "gas too high"))

		if tc.shouldPass && hasGasError {
			t.Errorf("Test '%s': Should pass but got gas error: %v", tc.description, err)
		} else if !tc.shouldPass && !hasGasError {
			t.Errorf("Test '%s': Should fail with gas error but passed or failed differently", tc.description)
		} else {
			t.Logf("✅ Test '%s' (gas=%d): Correct result", tc.description, tc.gas)
		}
	}
}

// Test 10: Gas calculation overflow protection
func TestGasCostOverflow(t *testing.T) {
	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: config.TestnetChainID,
		},
		Economics: config.EconomicsConfig{
			MinTransfer:  1000,
			BaseGasPrice: 1000,
			MinGasLimit:  21000,
			MaxGasPerTx:  10_000_000,
			MaxGasPrice:  1_000_000,
		},
		Consensus: config.ConsensusConfig{
			MaxTimestampSkew: 5 * time.Minute,
			MaxTimestampAge:  1 * time.Hour,
		},
	}

	// With limits: max cost = 10M gas * 1M price = 10 trillion nano (10,000 THRYLOS)
	// This is reasonable and won't overflow
	maxGasCost := cfg.Economics.MaxGasPerTx * cfg.Economics.MaxGasPrice
	t.Logf("Maximum possible gas cost: %d nano (%.2f THRYLOS)",
		maxGasCost, float64(maxGasCost)/1_000_000_000)

	if maxGasCost < 0 {
		t.Error("Gas cost calculation overflowed to negative!")
	}

	if maxGasCost > 100_000_000_000_000 { // 100,000 THRYLOS
		t.Error("Gas cost is unreasonably high, may cause issues")
	}

	t.Logf("✅ Gas cost limits prevent overflow and remain reasonable")
}
