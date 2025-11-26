// validator_overflow_test.go
// Tests for integer overflow protection in transaction validation

package transaction

import (
	"math"
	"testing"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/proto/core"
)

// TestSafeMultiply tests the safe multiplication helper
func TestSafeMultiply(t *testing.T) {
	tests := []struct {
		name      string
		a         int64
		b         int64
		wantError bool
	}{
		{
			name:      "Normal multiplication",
			a:         100,
			b:         200,
			wantError: false,
		},
		{
			name:      "Zero multiplication",
			a:         0,
			b:         math.MaxInt64,
			wantError: false,
		},
		{
			name:      "Overflow - MaxInt64 * 2",
			a:         math.MaxInt64,
			b:         2,
			wantError: true,
		},
		{
			name:      "Overflow - large numbers",
			a:         math.MaxInt64 / 2,
			b:         3,
			wantError: true,
		},
		{
			name:      "Edge case - just under overflow",
			a:         math.MaxInt64 / 2,
			b:         2,
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := safeMultiply(tt.a, tt.b, "test multiplication")

			if tt.wantError && err == nil {
				t.Errorf("Expected error for %d * %d, but got result: %d", tt.a, tt.b, result)
			}

			if !tt.wantError && err != nil {
				t.Errorf("Unexpected error for %d * %d: %v", tt.a, tt.b, err)
			}

			if !tt.wantError && err == nil {
				expected := tt.a * tt.b
				if result != expected {
					t.Errorf("Expected %d, got %d", expected, result)
				}
			}
		})
	}
}

// TestSafeAdd tests the safe addition helper
func TestSafeAdd(t *testing.T) {
	tests := []struct {
		name      string
		a         int64
		b         int64
		wantError bool
	}{
		{
			name:      "Normal addition",
			a:         100,
			b:         200,
			wantError: false,
		},
		{
			name:      "Zero addition",
			a:         0,
			b:         math.MaxInt64,
			wantError: false,
		},
		{
			name:      "Overflow - MaxInt64 + 1",
			a:         math.MaxInt64,
			b:         1,
			wantError: true,
		},
		{
			name:      "Overflow - large numbers",
			a:         math.MaxInt64 / 2,
			b:         math.MaxInt64/2 + 2,
			wantError: true,
		},
		{
			name:      "Edge case - just under overflow",
			a:         math.MaxInt64 - 1,
			b:         1,
			wantError: false,
		},
		{
			name:      "Underflow - negative numbers",
			a:         math.MinInt64,
			b:         -1,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := safeAdd(tt.a, tt.b, "test addition")

			if tt.wantError && err == nil {
				t.Errorf("Expected error for %d + %d, but got result: %d", tt.a, tt.b, result)
			}

			if !tt.wantError && err != nil {
				t.Errorf("Unexpected error for %d + %d: %v", tt.a, tt.b, err)
			}

			if !tt.wantError && err == nil {
				expected := tt.a + tt.b
				if result != expected {
					t.Errorf("Expected %d, got %d", expected, result)
				}
			}
		})
	}
}

// TestValidateAmountNonNegative tests negative amount validation
func TestValidateAmountNonNegative(t *testing.T) {
	tests := []struct {
		name      string
		amount    int64
		fieldName string
		wantError bool
	}{
		{
			name:      "Positive amount",
			amount:    100,
			fieldName: "test amount",
			wantError: false,
		},
		{
			name:      "Zero amount",
			amount:    0,
			fieldName: "test amount",
			wantError: false,
		},
		{
			name:      "Negative amount",
			amount:    -1,
			fieldName: "test amount",
			wantError: true,
		},
		{
			name:      "Large negative amount",
			amount:    math.MinInt64,
			fieldName: "test amount",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAmountNonNegative(tt.amount, tt.fieldName)

			if tt.wantError && err == nil {
				t.Errorf("Expected error for amount %d, but got none", tt.amount)
			}

			if !tt.wantError && err != nil {
				t.Errorf("Unexpected error for amount %d: %v", tt.amount, err)
			}
		})
	}
}

// TestValidateTransferOverflowProtection tests that validateTransfer catches overflows
func TestValidateTransferOverflowProtection(t *testing.T) {
	cfg := &config.Config{
		Economics: config.EconomicsConfig{
			MinTransfer: 1,
		},
	}

	validator := NewValidator(account.ShardID(0), 1, cfg)

	// Create a sender with some balance
	sender := &core.Account{
		Address: "test-sender",
		Balance: 1000000,
		Nonce:   0,
	}

	tests := []struct {
		name      string
		tx        *core.Transaction
		wantError bool
		errorMsg  string
	}{
		{
			name: "Normal valid transfer",
			tx: &core.Transaction{
				Amount:   100,
				Gas:      21000,
				GasPrice: 1,
				From:     "test-sender",
				To:       "test-recipient",
			},
			wantError: false,
		},
		{
			name: "Negative amount",
			tx: &core.Transaction{
				Amount:   -100,
				Gas:      21000,
				GasPrice: 1,
				From:     "test-sender",
				To:       "test-recipient",
			},
			wantError: true,
			errorMsg:  "amount cannot be negative",
		},
		{
			name: "Negative gas",
			tx: &core.Transaction{
				Amount:   100,
				Gas:      -21000,
				GasPrice: 1,
				From:     "test-sender",
				To:       "test-recipient",
			},
			wantError: true,
			errorMsg:  "gas cannot be negative",
		},
		{
			name: "Negative gas price",
			tx: &core.Transaction{
				Amount:   100,
				Gas:      21000,
				GasPrice: -1,
				From:     "test-sender",
				To:       "test-recipient",
			},
			wantError: true,
			errorMsg:  "gas price cannot be negative",
		},
		{
			name: "Gas calculation overflow",
			tx: &core.Transaction{
				Amount:   100,
				Gas:      math.MaxInt64,
				GasPrice: 2,
				From:     "test-sender",
				To:       "test-recipient",
			},
			wantError: true,
			errorMsg:  "gas cost calculation would overflow",
		},
		{
			name: "Total cost overflow",
			tx: &core.Transaction{
				Amount:   math.MaxInt64,
				Gas:      21000,
				GasPrice: 1,
				From:     "test-sender",
				To:       "test-recipient",
			},
			wantError: true,
			errorMsg:  "total cost calculation would overflow",
		},
		{
			name: "Self transfer",
			tx: &core.Transaction{
				Amount:   100,
				Gas:      21000,
				GasPrice: 1,
				From:     "test-sender",
				To:       "test-sender",
			},
			wantError: true,
			errorMsg:  "cannot transfer to self",
		},
		{
			name: "Insufficient balance",
			tx: &core.Transaction{
				Amount:   2000000,
				Gas:      21000,
				GasPrice: 1,
				From:     "test-sender",
				To:       "test-recipient",
			},
			wantError: true,
			errorMsg:  "insufficient balance",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.validateTransfer(tt.tx, sender)

			if tt.wantError && err == nil {
				t.Errorf("Expected error containing '%s', but got none", tt.errorMsg)
			}

			if !tt.wantError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if tt.wantError && err != nil {
				// Check error message contains expected text
				if tt.errorMsg != "" && !contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error containing '%s', got: %v", tt.errorMsg, err)
				}
			}
		})
	}
}

// TestValidateStakeOverflowProtection tests that validateStake catches overflows
func TestValidateStakeOverflowProtection(t *testing.T) {
	cfg := &config.Config{
		Economics: config.EconomicsConfig{
			MinStake: 1000,
		},
	}

	validator := NewValidator(account.ShardID(0), 1, cfg)

	sender := &core.Account{
		Address: "test-sender",
		Balance: 1000000,
		Nonce:   0,
	}

	tests := []struct {
		name      string
		tx        *core.Transaction
		wantError bool
	}{
		{
			name: "Valid stake",
			tx: &core.Transaction{
				Amount:   10000,
				Gas:      21000,
				GasPrice: 1,
			},
			wantError: false,
		},
		{
			name: "Negative stake amount",
			tx: &core.Transaction{
				Amount:   -10000,
				Gas:      21000,
				GasPrice: 1,
			},
			wantError: true,
		},
		{
			name: "Overflow in total cost",
			tx: &core.Transaction{
				Amount:   math.MaxInt64,
				Gas:      21000,
				GasPrice: 1,
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.validateStake(tt.tx, sender)

			if tt.wantError && err == nil {
				t.Errorf("Expected error but got none")
			}

			if !tt.wantError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

// TestCalculateGasCost tests the gas cost calculation
func TestCalculateGasCost(t *testing.T) {
	cfg := &config.Config{}
	validator := NewValidator(account.ShardID(0), 1, cfg)

	tests := []struct {
		name      string
		gas       int64
		gasPrice  int64
		wantError bool
	}{
		{
			name:      "Normal gas cost",
			gas:       21000,
			gasPrice:  100,
			wantError: false,
		},
		{
			name:      "Zero gas",
			gas:       0,
			gasPrice:  100,
			wantError: false,
		},
		{
			name:      "Negative gas",
			gas:       -21000,
			gasPrice:  100,
			wantError: true,
		},
		{
			name:      "Negative gas price",
			gas:       21000,
			gasPrice:  -100,
			wantError: true,
		},
		{
			name:      "Overflow",
			gas:       math.MaxInt64,
			gasPrice:  2,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := validator.calculateGasCost(tt.gas, tt.gasPrice)

			if tt.wantError && err == nil {
				t.Errorf("Expected error but got result: %d", result)
			}

			if !tt.wantError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !tt.wantError && err == nil {
				expected := tt.gas * tt.gasPrice
				if result != expected {
					t.Errorf("Expected %d, got %d", expected, result)
				}
			}
		})
	}
}

// TestCalculateTotalCost tests the total cost calculation
func TestCalculateTotalCost(t *testing.T) {
	cfg := &config.Config{}
	validator := NewValidator(account.ShardID(0), 1, cfg)

	tests := []struct {
		name      string
		amount    int64
		gas       int64
		gasPrice  int64
		wantError bool
	}{
		{
			name:      "Normal total cost",
			amount:    1000,
			gas:       21000,
			gasPrice:  100,
			wantError: false,
		},
		{
			name:      "Negative amount",
			amount:    -1000,
			gas:       21000,
			gasPrice:  100,
			wantError: true,
		},
		{
			name:      "Gas calculation overflow",
			amount:    1000,
			gas:       math.MaxInt64,
			gasPrice:  2,
			wantError: true,
		},
		{
			name:      "Total cost overflow",
			amount:    math.MaxInt64,
			gas:       21000,
			gasPrice:  1,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := validator.calculateTotalCost(tt.amount, tt.gas, tt.gasPrice)

			if tt.wantError && err == nil {
				t.Errorf("Expected error but got result: %d", result)
			}

			if !tt.wantError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !tt.wantError && err == nil {
				expectedGasCost := tt.gas * tt.gasPrice
				expected := tt.amount + expectedGasCost
				if result != expected {
					t.Errorf("Expected %d, got %d", expected, result)
				}
			}
		})
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) &&
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
			findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
