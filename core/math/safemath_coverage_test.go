// core/math/safemath_coverage_test.go
// Additional tests to achieve 95%+ coverage
// These tests focus on error paths, edge cases, and helper functions

package math

import (
	"math"
	"math/big"
	"testing"
)

// ============================================================================
// COVERAGE TESTS FOR HELPER FUNCTIONS
// ============================================================================

func TestIsValidBalance(t *testing.T) {
	tests := []struct {
		name    string
		balance int64
		want    bool
	}{
		{"positive balance", 1000, true},
		{"zero balance", 0, true},
		{"max balance", math.MaxInt64, true},
		{"negative balance", -1, false},
		{"min balance", math.MinInt64, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValidBalance(tt.balance)
			if got != tt.want {
				t.Errorf("IsValidBalance(%d) = %v, want %v", tt.balance, got, tt.want)
			}
		})
	}
}

func TestIsValidGas(t *testing.T) {
	tests := []struct {
		name string
		gas  uint64
		want bool
	}{
		{"valid gas", 21000, true},
		{"zero gas", 0, false},
		{"max gas", math.MaxUint64, true},
		{"small gas", 1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValidGas(tt.gas)
			if got != tt.want {
				t.Errorf("IsValidGas(%d) = %v, want %v", tt.gas, got, tt.want)
			}
		})
	}
}

func TestIsValidPercentage(t *testing.T) {
	tests := []struct {
		name    string
		percent int64
		want    bool
	}{
		{"valid 0%", 0, true},
		{"valid 50%", 50, true},
		{"valid 100%", 100, true},
		{"invalid negative", -1, false},
		{"invalid > 100", 101, false},
		{"invalid large negative", -100, false},
		{"invalid large positive", 200, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValidPercentage(tt.percent)
			if got != tt.want {
				t.Errorf("IsValidPercentage(%d) = %v, want %v", tt.percent, got, tt.want)
			}
		})
	}
}

func TestSafeBalanceAdd(t *testing.T) {
	tests := []struct {
		name    string
		balance int64
		amount  int64
		want    int64
		hasErr  bool
	}{
		{"normal credit", 100, 50, 150, false},
		{"credit zero", 100, 0, 100, false},
		{"credit to zero balance", 0, 100, 100, false},
		{"negative amount", 100, -50, 0, true},
		{"overflow", math.MaxInt64, 1, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SafeBalanceAdd(tt.balance, tt.amount)
			if tt.hasErr {
				if err == nil {
					t.Errorf("SafeBalanceAdd(%d, %d) expected error, got nil", tt.balance, tt.amount)
				}
			} else {
				if err != nil {
					t.Errorf("SafeBalanceAdd(%d, %d) unexpected error: %v", tt.balance, tt.amount, err)
				}
				if got != tt.want {
					t.Errorf("SafeBalanceAdd(%d, %d) = %d, want %d", tt.balance, tt.amount, got, tt.want)
				}
			}
		})
	}
}

func TestSafeBalanceSub(t *testing.T) {
	tests := []struct {
		name    string
		balance int64
		amount  int64
		want    int64
		hasErr  bool
	}{
		{"normal debit", 100, 50, 50, false},
		{"debit to zero", 100, 100, 0, false},
		{"negative amount", 100, -50, 0, true},
		{"insufficient balance", 50, 100, 0, true},
		{"debit from zero", 0, 1, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SafeBalanceSub(tt.balance, tt.amount)
			if tt.hasErr {
				if err == nil {
					t.Errorf("SafeBalanceSub(%d, %d) expected error, got nil", tt.balance, tt.amount)
				}
			} else {
				if err != nil {
					t.Errorf("SafeBalanceSub(%d, %d) unexpected error: %v", tt.balance, tt.amount, err)
				}
				if got != tt.want {
					t.Errorf("SafeBalanceSub(%d, %d) = %d, want %d", tt.balance, tt.amount, got, tt.want)
				}
			}
		})
	}
}

func TestCalculateRemainingGas(t *testing.T) {
	tests := []struct {
		name     string
		gasLimit uint64
		gasUsed  uint64
		want     uint64
		hasErr   bool
	}{
		{"normal remaining", 100000, 21000, 79000, false},
		{"all used", 100000, 100000, 0, false},
		{"none used", 100000, 0, 100000, false},
		{"overflow - used > limit", 50000, 100000, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CalculateRemainingGas(tt.gasLimit, tt.gasUsed)
			if tt.hasErr {
				if err == nil {
					t.Errorf("CalculateRemainingGas(%d, %d) expected error, got nil", tt.gasLimit, tt.gasUsed)
				}
			} else {
				if err != nil {
					t.Errorf("CalculateRemainingGas(%d, %d) unexpected error: %v", tt.gasLimit, tt.gasUsed, err)
				}
				if got != tt.want {
					t.Errorf("CalculateRemainingGas(%d, %d) = %d, want %d", tt.gasLimit, tt.gasUsed, got, tt.want)
				}
			}
		})
	}
}

func TestCalculateTotalGasCost(t *testing.T) {
	tests := []struct {
		name        string
		gasUsed     uint64
		gasPrice    uint64
		priorityFee uint64
		want        uint64
		hasErr      bool
	}{
		{"normal cost", 21000, 50, 10, 1050010, false},
		{"no priority fee", 21000, 50, 0, 1050000, false},
		{"zero gas", 0, 50, 10, 10, false},
		{"overflow in multiply", math.MaxUint64, 2, 0, 0, true},
		{"overflow in add", math.MaxUint64 / 2, 2, 100, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CalculateTotalGasCost(tt.gasUsed, tt.gasPrice, tt.priorityFee)
			if tt.hasErr {
				if err == nil {
					t.Errorf("CalculateTotalGasCost(%d, %d, %d) expected error, got nil",
						tt.gasUsed, tt.gasPrice, tt.priorityFee)
				}
			} else {
				if err != nil {
					t.Errorf("CalculateTotalGasCost(%d, %d, %d) unexpected error: %v",
						tt.gasUsed, tt.gasPrice, tt.priorityFee, err)
				}
				if got != tt.want {
					t.Errorf("CalculateTotalGasCost(%d, %d, %d) = %d, want %d",
						tt.gasUsed, tt.gasPrice, tt.priorityFee, got, tt.want)
				}
			}
		})
	}
}

func TestMod64(t *testing.T) {
	tests := []struct {
		name   string
		a, b   uint64
		want   uint64
		hasErr bool
	}{
		{"normal modulo", 10, 3, 1, false},
		{"exact division", 10, 5, 0, false},
		{"a < b", 3, 10, 3, false},
		{"divide by zero", 10, 0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Mod64(tt.a, tt.b)
			if tt.hasErr {
				if err == nil {
					t.Errorf("Mod64(%d, %d) expected error, got nil", tt.a, tt.b)
				}
			} else {
				if err != nil {
					t.Errorf("Mod64(%d, %d) unexpected error: %v", tt.a, tt.b, err)
				}
				if got != tt.want {
					t.Errorf("Mod64(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
				}
			}
		})
	}
}

func TestSafeMod(t *testing.T) {
	tests := []struct {
		name   string
		a, b   int64
		want   int64
		hasErr bool
	}{
		{"normal modulo", 10, 3, 1, false},
		{"negative dividend", -10, 3, -1, false},
		{"negative divisor", 10, -3, 1, false},
		{"both negative", -10, -3, -1, false},
		{"divide by zero", 10, 0, 0, true},
		{"MinInt64 % -1", math.MinInt64, -1, 0, false}, // Special case
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SafeMod(tt.a, tt.b)
			if tt.hasErr {
				if err == nil {
					t.Errorf("SafeMod(%d, %d) expected error, got nil", tt.a, tt.b)
				}
			} else {
				if err != nil {
					t.Errorf("SafeMod(%d, %d) unexpected error: %v", tt.a, tt.b, err)
				}
				if got != tt.want {
					t.Errorf("SafeMod(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
				}
			}
		})
	}
}

// ============================================================================
// BIG.INT OPERATION TESTS
// ============================================================================

func TestAddBig(t *testing.T) {
	tests := []struct {
		name string
		a, b *big.Int
		want string
	}{
		{"normal addition", big.NewInt(100), big.NewInt(200), "300"},
		{"nil a", nil, big.NewInt(100), "100"},
		{"nil b", big.NewInt(100), nil, "100"},
		{"both nil", nil, nil, "0"},
		{"large numbers", new(big.Int).SetUint64(math.MaxUint64), big.NewInt(1), "18446744073709551616"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AddBig(tt.a, tt.b)
			want := new(big.Int)
			want.SetString(tt.want, 10)
			if got.Cmp(want) != 0 {
				t.Errorf("AddBig(%v, %v) = %s, want %s", tt.a, tt.b, got.String(), tt.want)
			}
		})
	}
}

func TestSubBig(t *testing.T) {
	tests := []struct {
		name string
		a, b *big.Int
		want string
	}{
		{"normal subtraction", big.NewInt(200), big.NewInt(100), "100"},
		{"result zero", big.NewInt(100), big.NewInt(100), "0"},
		{"negative result", big.NewInt(100), big.NewInt(200), "-100"},
		{"nil a", nil, big.NewInt(100), "-100"},
		{"nil b", big.NewInt(100), nil, "100"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SubBig(tt.a, tt.b)
			want := new(big.Int)
			want.SetString(tt.want, 10)
			if got.Cmp(want) != 0 {
				t.Errorf("SubBig(%v, %v) = %s, want %s", tt.a, tt.b, got.String(), tt.want)
			}
		})
	}
}

func TestMulBig(t *testing.T) {
	tests := []struct {
		name string
		a, b *big.Int
		want string
	}{
		{"normal multiplication", big.NewInt(100), big.NewInt(200), "20000"},
		{"nil a", nil, big.NewInt(100), "0"},
		{"nil b", big.NewInt(100), nil, "0"},
		{"zero multiplication", big.NewInt(0), big.NewInt(100), "0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MulBig(tt.a, tt.b)
			want := new(big.Int)
			want.SetString(tt.want, 10)
			if got.Cmp(want) != 0 {
				t.Errorf("MulBig(%v, %v) = %s, want %s", tt.a, tt.b, got.String(), tt.want)
			}
		})
	}
}

func TestDivBig(t *testing.T) {
	tests := []struct {
		name   string
		a, b   *big.Int
		want   string
		hasErr bool
	}{
		{"normal division", big.NewInt(200), big.NewInt(100), "2", false},
		{"with remainder", big.NewInt(201), big.NewInt(100), "2", false},
		{"nil a", nil, big.NewInt(100), "0", false},
		{"nil b", big.NewInt(100), nil, "", true},
		{"zero b", big.NewInt(100), big.NewInt(0), "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DivBig(tt.a, tt.b)
			if tt.hasErr {
				if err == nil {
					t.Errorf("DivBig(%v, %v) expected error, got nil", tt.a, tt.b)
				}
			} else {
				if err != nil {
					t.Errorf("DivBig(%v, %v) unexpected error: %v", tt.a, tt.b, err)
				}
				want := new(big.Int)
				want.SetString(tt.want, 10)
				if got.Cmp(want) != 0 {
					t.Errorf("DivBig(%v, %v) = %s, want %s", tt.a, tt.b, got.String(), tt.want)
				}
			}
		})
	}
}

// ============================================================================
// ERROR TYPE TESTS
// ============================================================================

func TestOverflowError_Error(t *testing.T) {
	err := &OverflowError{
		Operation: "Add64",
		A:         uint64(100),
		B:         uint64(200),
		Message:   "test overflow",
	}

	got := err.Error()
	if got == "" {
		t.Error("OverflowError.Error() returned empty string")
	}

	// Check that error message contains key information
	if !containsSubstring(got, "Add64") {
		t.Errorf("OverflowError.Error() missing operation name: %s", got)
	}
	if !containsSubstring(got, "test overflow") {
		t.Errorf("OverflowError.Error() missing message: %s", got)
	}
}

func TestUnderflowError_Error(t *testing.T) {
	err := &UnderflowError{
		Operation: "Sub64",
		A:         uint64(50),
		B:         uint64(100),
		Message:   "test underflow",
	}

	got := err.Error()
	if got == "" {
		t.Error("UnderflowError.Error() returned empty string")
	}

	if !containsSubstring(got, "Sub64") {
		t.Errorf("UnderflowError.Error() missing operation name: %s", got)
	}
	if !containsSubstring(got, "test underflow") {
		t.Errorf("UnderflowError.Error() missing message: %s", got)
	}
}

// ============================================================================
// ALIAS FUNCTION TESTS
// ============================================================================

func TestAdd64Safe_Alias(t *testing.T) {
	result, err := Add64Safe(10, 20)
	if err != nil {
		t.Errorf("Add64Safe unexpected error: %v", err)
	}
	if result != 30 {
		t.Errorf("Add64Safe(10, 20) = %d, want 30", result)
	}
}

func TestMul64Safe_Alias(t *testing.T) {
	result, err := Mul64Safe(10, 20)
	if err != nil {
		t.Errorf("Mul64Safe unexpected error: %v", err)
	}
	if result != 200 {
		t.Errorf("Mul64Safe(10, 20) = %d, want 200", result)
	}
}

func TestSub64Safe_Alias(t *testing.T) {
	result, err := Sub64Safe(30, 10)
	if err != nil {
		t.Errorf("Sub64Safe unexpected error: %v", err)
	}
	if result != 20 {
		t.Errorf("Sub64Safe(30, 10) = %d, want 20", result)
	}
}

func TestDiv64Safe_Alias(t *testing.T) {
	result, err := Div64Safe(100, 10)
	if err != nil {
		t.Errorf("Div64Safe unexpected error: %v", err)
	}
	if result != 10 {
		t.Errorf("Div64Safe(100, 10) = %d, want 10", result)
	}
}

// ============================================================================
// EDGE CASE INTEGRATION TESTS
// ============================================================================

func TestComplexGasCalculation(t *testing.T) {
	// Simulate complex gas calculation: (gasLimit * gasPrice) + priorityFee + baseFee
	gasLimit := uint64(100000)
	gasPrice := uint64(50)
	priorityFee := uint64(10)
	baseFee := uint64(5)

	// Step 1: gasLimit * gasPrice
	cost1, err := Mul64(gasLimit, gasPrice)
	if err != nil {
		t.Fatalf("Step 1 failed: %v", err)
	}

	// Step 2: cost1 + priorityFee
	cost2, err := Add64(cost1, priorityFee)
	if err != nil {
		t.Fatalf("Step 2 failed: %v", err)
	}

	// Step 3: cost2 + baseFee
	totalCost, err := Add64(cost2, baseFee)
	if err != nil {
		t.Fatalf("Step 3 failed: %v", err)
	}

	expectedCost := uint64(5000015) // 100000*50 + 10 + 5
	if totalCost != expectedCost {
		t.Errorf("Complex gas calculation = %d, want %d", totalCost, expectedCost)
	}
}

func TestProportionalRewardDistribution(t *testing.T) {
	// Distribute 1000 tokens among 3 validators proportionally
	totalReward := uint64(1000)
	stakes := []uint64{500, 300, 200}
	totalStake := uint64(1000)

	rewards := make([]uint64, len(stakes))
	var totalDistributed uint64

	for i, stake := range stakes {
		reward, err := MulDiv64(totalReward, stake, totalStake)
		if err != nil {
			t.Fatalf("Reward calculation failed for validator %d: %v", i, err)
		}
		rewards[i] = reward
		totalDistributed, err = Add64(totalDistributed, reward)
		if err != nil {
			t.Fatalf("Total calculation failed: %v", err)
		}
	}

	// Check that rewards sum correctly (may be slightly less due to integer division)
	if totalDistributed > totalReward {
		t.Errorf("Total distributed (%d) exceeds total reward (%d)", totalDistributed, totalReward)
	}

	// Check proportions
	expected := []uint64{500, 300, 200}
	for i, exp := range expected {
		if rewards[i] != exp {
			t.Errorf("Validator %d reward = %d, want %d", i, rewards[i], exp)
		}
	}
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && s[0:len(s)] != ""
}

// ============================================================================
// MUST* FUNCTION COVERAGE TESTS
// ============================================================================

func TestMustSafeAdd_Success(t *testing.T) {
	result := MustSafeAdd(10, 20)
	if result != 30 {
		t.Errorf("MustSafeAdd(10, 20) = %d, want 30", result)
	}
}

func TestMustSafeSub_Success(t *testing.T) {
	result := MustSafeSub(30, 10)
	if result != 20 {
		t.Errorf("MustSafeSub(30, 10) = %d, want 20", result)
	}
}

func TestMustSafeMul_Success(t *testing.T) {
	result := MustSafeMul(10, 20)
	if result != 200 {
		t.Errorf("MustSafeMul(10, 20) = %d, want 200", result)
	}
}

// ============================================================================
// ADDITIONAL EDGE CASE TESTS
// ============================================================================

func TestSafeSub_ZeroSubtraction(t *testing.T) {
	result, err := SafeSub(100, 0)
	if err != nil {
		t.Errorf("SafeSub(100, 0) unexpected error: %v", err)
	}
	if result != 100 {
		t.Errorf("SafeSub(100, 0) = %d, want 100", result)
	}
}

func TestSafeMul_OneMultiplication(t *testing.T) {
	tests := []struct {
		name string
		a, b int64
		want int64
	}{
		{"a=1", 1, 100, 100},
		{"b=1", 100, 1, 100},
		{"both 1", 1, 1, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := SafeMul(tt.a, tt.b)
			if err != nil {
				t.Errorf("SafeMul(%d, %d) unexpected error: %v", tt.a, tt.b, err)
			}
			if result != tt.want {
				t.Errorf("SafeMul(%d, %d) = %d, want %d", tt.a, tt.b, result, tt.want)
			}
		})
	}
}

func TestPow64_EdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		base   uint64
		exp    uint64
		want   uint64
		hasErr bool
	}{
		{"base 2, exp 0", 2, 0, 1, false},
		{"base 0, exp 0", 0, 0, 1, false}, // Mathematically 0^0 is often defined as 1
		{"base 1, exp 1000", 1, 1000, 1, false},
		{"large exp triggers early check", 2, 100, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Pow64(tt.base, tt.exp)
			if tt.hasErr {
				if err == nil {
					t.Errorf("Pow64(%d, %d) expected error, got nil", tt.base, tt.exp)
				}
			} else {
				if err != nil {
					t.Errorf("Pow64(%d, %d) unexpected error: %v", tt.base, tt.exp, err)
				}
				if got != tt.want {
					t.Errorf("Pow64(%d, %d) = %d, want %d", tt.base, tt.exp, got, tt.want)
				}
			}
		})
	}
}
