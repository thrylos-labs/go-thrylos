// core/math/safemath_gas_test.go
// Tests for uint64 gas calculation safety (CRITICAL-01 fix)

package math

import (
	"math"
	"testing"
)

// ============================================================================
// UINT64 ADD TESTS
// ============================================================================

// func TestAdd64_Normal(t *testing.T) {
// 	tests := []struct {
// 		name string
// 		a, b uint64
// 		want uint64
// 	}{
// 		{"zero + zero", 0, 0, 0},
// 		{"small numbers", 100, 200, 300},
// 		{"large numbers", 1000000000, 2000000000, 3000000000},
// 		{"max safe value", math.MaxUint64 / 2, math.MaxUint64 / 2, math.MaxUint64 - 1},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			got, err := Add64(tt.a, tt.b)
// 			if err != nil {
// 				t.Errorf("Add64(%d, %d) unexpected error: %v", tt.a, tt.b, err)
// 			}
// 			if got != tt.want {
// 				t.Errorf("Add64(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
// 			}
// 		})
// 	}
// }

// func TestAdd64_Overflow(t *testing.T) {
// 	tests := []struct {
// 		name string
// 		a, b uint64
// 	}{
// 		{"max + 1", math.MaxUint64, 1},
// 		{"max + max", math.MaxUint64, math.MaxUint64},
// 		{"near max overflow", math.MaxUint64 - 100, 200},
// 		{"gas limit attack", math.MaxUint64 - 1000, 2000}, // Simulates attacker crafted values
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			_, err := Add64(tt.a, tt.b)
// 			if err == nil {
// 				t.Errorf("Add64(%d, %d) expected overflow error, got nil", tt.a, tt.b)
// 			}
// 		})
// 	}
// }

// // ============================================================================
// // UINT64 MULTIPLY TESTS
// // ============================================================================

// func TestMul64_Normal(t *testing.T) {
// 	tests := []struct {
// 		name string
// 		a, b uint64
// 		want uint64
// 	}{
// 		{"zero * anything", 0, 12345, 0},
// 		{"anything * zero", 12345, 0, 0},
// 		{"small numbers", 10, 20, 200},
// 		{"gas price calculation", 21000, 50, 1050000}, // 21000 gas * 50 gwei
// 		{"large safe multiply", 1000000, 1000000, 1000000000000},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			got, err := Mul64(tt.a, tt.b)
// 			if err != nil {
// 				t.Errorf("Mul64(%d, %d) unexpected error: %v", tt.a, tt.b, err)
// 			}
// 			if got != tt.want {
// 				t.Errorf("Mul64(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
// 			}
// 		})
// 	}
// }

// func TestMul64_Overflow(t *testing.T) {
// 	tests := []struct {
// 		name string
// 		a, b uint64
// 	}{
// 		{"max * 2", math.MaxUint64, 2},
// 		{"sqrt(max) * sqrt(max) + 1", 4294967296, 4294967296},    // Overflows
// 		{"large gas * high price", math.MaxUint64 / 100, 1000},   // Gas price attack
// 		{"attacker crafted values", 18446744073709551615 / 2, 3}, // Specific attack
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			_, err := Mul64(tt.a, tt.b)
// 			if err == nil {
// 				t.Errorf("Mul64(%d, %d) expected overflow error, got nil", tt.a, tt.b)
// 			}
// 		})
// 	}
// }

// ============================================================================
// UINT64 SUBTRACT TESTS
// ============================================================================

// func TestSub64_Normal(t *testing.T) {
// 	tests := []struct {
// 		name string
// 		a, b uint64
// 		want uint64
// 	}{
// 		{"same values", 100, 100, 0},
// 		{"normal subtraction", 200, 100, 100},
// 		{"large numbers", 1000000000, 500000000, 500000000},
// 		{"max - 1", math.MaxUint64, 1, math.MaxUint64 - 1},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			got, err := Sub64(tt.a, tt.b)
// 			if err != nil {
// 				t.Errorf("Sub64(%d, %d) unexpected error: %v", tt.a, tt.b, err)
// 			}
// 			if got != tt.want {
// 				t.Errorf("Sub64(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
// 			}
// 		})
// 	}
// }

// func TestSub64_Underflow(t *testing.T) {
// 	tests := []struct {
// 		name string
// 		a, b uint64
// 	}{
// 		{"0 - 1", 0, 1},
// 		{"small - large", 100, 200},
// 		{"1 - max", 1, math.MaxUint64},
// 		{"gas remaining attack", 1000, 2000}, // Trying to use more gas than available
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			_, err := Sub64(tt.a, tt.b)
// 			if err == nil {
// 				t.Errorf("Sub64(%d, %d) expected underflow error, got nil", tt.a, tt.b)
// 			}
// 		})
// 	}
// }

// ============================================================================
// UINT64 DIVIDE TESTS
// ============================================================================

// func TestDiv64_Normal(t *testing.T) {
// 	tests := []struct {
// 		name string
// 		a, b uint64
// 		want uint64
// 	}{
// 		{"even division", 100, 10, 10},
// 		{"truncated division", 100, 3, 33},
// 		{"divide by self", 12345, 12345, 1},
// 		{"large numbers", math.MaxUint64, 2, math.MaxUint64 / 2},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			got, err := Div64(tt.a, tt.b)
// 			if err != nil {
// 				t.Errorf("Div64(%d, %d) unexpected error: %v", tt.a, tt.b, err)
// 			}
// 			if got != tt.want {
// 				t.Errorf("Div64(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
// 			}
// 		})
// 	}
// }

// func TestDiv64_DivisionByZero(t *testing.T) {
// 	_, err := Div64(100, 0)
// 	if err == nil {
// 		t.Error("Div64(100, 0) expected division by zero error, got nil")
// 	}
// }

// ============================================================================
// ADDMANY64 TESTS
// ============================================================================

func TestAddMany64_Normal(t *testing.T) {
	tests := []struct {
		name   string
		values []uint64
		want   uint64
	}{
		{"empty", []uint64{}, 0},
		{"single value", []uint64{100}, 100},
		{"multiple values", []uint64{100, 200, 300}, 600},
		{"gas aggregation", []uint64{21000, 21000, 21000, 21000}, 84000},
		{"block gas total", []uint64{50000, 100000, 75000, 125000}, 350000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AddMany64(tt.values...)
			if err != nil {
				t.Errorf("AddMany64(%v) unexpected error: %v", tt.values, err)
			}
			if got != tt.want {
				t.Errorf("AddMany64(%v) = %d, want %d", tt.values, got, tt.want)
			}
		})
	}
}

func TestAddMany64_Overflow(t *testing.T) {
	tests := []struct {
		name   string
		values []uint64
	}{
		{"overflow in middle", []uint64{math.MaxUint64 / 2, math.MaxUint64 / 2, 100}},
		{"overflow at end", []uint64{100, 200, math.MaxUint64 - 200}},
		{"multiple large values", []uint64{math.MaxUint64 / 3, math.MaxUint64 / 3, math.MaxUint64/3 + 1}}, // Fixed: +1 causes overflow
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := AddMany64(tt.values...)
			if err == nil {
				t.Errorf("AddMany64(%v) expected overflow error, got nil", tt.values)
			}
		})
	}
}

// ============================================================================
// MULADD64 TESTS
// ============================================================================

func TestMulAdd64_Normal(t *testing.T) {
	tests := []struct {
		name    string
		a, b, c uint64
		want    uint64
	}{
		{"gas cost with base fee", 21000, 50, 1000, 1051000}, // (21000 * 50) + 1000
		{"simple calculation", 10, 20, 30, 230},              // (10 * 20) + 30
		{"zero multiplication", 0, 100, 50, 50},              // (0 * 100) + 50
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MulAdd64(tt.a, tt.b, tt.c)
			if err != nil {
				t.Errorf("MulAdd64(%d, %d, %d) unexpected error: %v", tt.a, tt.b, tt.c, err)
			}
			if got != tt.want {
				t.Errorf("MulAdd64(%d, %d, %d) = %d, want %d", tt.a, tt.b, tt.c, got, tt.want)
			}
		})
	}
}

func TestMulAdd64_Overflow(t *testing.T) {
	tests := []struct {
		name    string
		a, b, c uint64
	}{
		{"multiply overflow", math.MaxUint64, 2, 100},
		{"add overflow", 100, 100, math.MaxUint64 - 5000}, // 100*100 = 10000, +MaxUint64-5000 overflows
		{"both overflow", math.MaxUint64 / 2, 3, 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := MulAdd64(tt.a, tt.b, tt.c)
			if err == nil {
				t.Errorf("MulAdd64(%d, %d, %d) expected overflow error, got nil", tt.a, tt.b, tt.c)
			}
		})
	}
}

// ============================================================================
// GAS HELPER FUNCTION TESTS
// ============================================================================

func TestEstimateTotalGas(t *testing.T) {
	// Normal case
	gasValues := []uint64{21000, 21000, 50000, 100000}
	total, err := EstimateTotalGas(gasValues)
	if err != nil {
		t.Errorf("EstimateTotalGas unexpected error: %v", err)
	}
	if total != 192000 {
		t.Errorf("EstimateTotalGas = %d, want 192000", total)
	}

	// Overflow case
	overflowValues := []uint64{math.MaxUint64, 1}
	_, err = EstimateTotalGas(overflowValues)
	if err == nil {
		t.Error("EstimateTotalGas expected overflow error for maxuint64+1")
	}
}

// func TestCalculateGasCost(t *testing.T) {
// 	// Normal case
// 	cost, err := CalculateGasCost(21000, 50)
// 	if err != nil {
// 		t.Errorf("CalculateGasCost unexpected error: %v", err)
// 	}
// 	if cost != 1050000 {
// 		t.Errorf("CalculateGasCost = %d, want 1050000", cost)
// 	}

// 	// Overflow case
// 	_, err = CalculateGasCost(math.MaxUint64, 2)
// 	if err == nil {
// 		t.Error("CalculateGasCost expected overflow error")
// 	}
// }

// func TestValidateGasLimit(t *testing.T) {
// 	const maxGas = 30000000

// 	// Valid cases
// 	if err := ValidateGasLimit(21000, maxGas); err != nil {
// 		t.Errorf("ValidateGasLimit(21000) unexpected error: %v", err)
// 	}

// 	// Zero gas
// 	if err := ValidateGasLimit(0, maxGas); err == nil {
// 		t.Error("ValidateGasLimit(0) expected error for zero gas")
// 	}

// 	// Exceeds max
// 	if err := ValidateGasLimit(maxGas+1, maxGas); err == nil {
// 		t.Error("ValidateGasLimit(maxGas+1) expected error for exceeding max")
// 	}
// }

// ============================================================================
// MUST WRAPPER TESTS (Should panic on overflow)
// ============================================================================

func TestMustAdd64_Panic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustAdd64 should panic on overflow")
		}
	}()
	MustAdd64(math.MaxUint64, 1) // Should panic
}

func TestMustMul64_Panic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustMul64 should panic on overflow")
		}
	}()
	MustMul64(math.MaxUint64, 2) // Should panic
}

func TestMustSub64_Panic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustSub64 should panic on underflow")
		}
	}()
	MustSub64(0, 1) // Should panic
}

// ============================================================================
// ATTACK SCENARIO TESTS
// ============================================================================

func TestGasAttackScenario_OverflowBypass(t *testing.T) {
	// Simulate an attacker trying to bypass gas limits by overflow
	// Scenario: Attacker crafts tx with gasLimit and priorityFee that overflow

	attackGasLimit := uint64(math.MaxUint64 / 10)
	attackPriorityFee := uint64(100)

	// Without safe math, this might overflow and wrap to small value
	_, err := Mul64(attackGasLimit, attackPriorityFee)
	if err == nil {
		t.Error("Attack scenario: expected overflow to be caught")
	}
}

func TestGasAttackScenario_BlockGasManipulation(t *testing.T) {
	// Scenario: Attacker tries to manipulate total block gas
	// by including transactions that sum to overflow

	tx1Gas := uint64(math.MaxUint64 / 2)
	tx2Gas := uint64(math.MaxUint64 / 2)
	baseGas := uint64(1000)

	// This should overflow
	_, err := AddMany64(tx1Gas, tx2Gas, baseGas)
	if err == nil {
		t.Error("Block gas manipulation: expected overflow to be caught")
	}
}

func TestGasAttackScenario_ConsensusBreak(t *testing.T) {
	// Scenario: Different nodes compute different gas values due to overflow
	// This test verifies consistent behavior across calculations

	gasUsed := uint64(1000000)
	baseGas := uint64(21000)
	priorityFee := uint64(50)
	gasLimit := uint64(1000000)

	// Safe calculation
	step1, err1 := Add64(gasUsed, baseGas)
	if err1 != nil {
		t.Fatalf("Step 1 failed: %v", err1)
	}

	step2, err2 := Mul64(priorityFee, gasLimit)
	if err2 != nil {
		t.Fatalf("Step 2 failed: %v", err2)
	}

	totalGas, err3 := Add64(step1, step2)
	if err3 != nil {
		t.Fatalf("Step 3 failed: %v", err3)
	}

	// Verify result is consistent
	expectedTotal := uint64(51021000) // 1000000 + 21000 + (50 * 1000000)
	if totalGas != expectedTotal {
		t.Errorf("Consensus calculation mismatch: got %d, want %d", totalGas, expectedTotal)
	}
}

// ============================================================================
// BENCHMARK TESTS
// ============================================================================

// func BenchmarkAdd64(b *testing.B) {
// 	for i := 0; i < b.N; i++ {
// 		Add64(12345, 67890)
// 	}
// }

// func BenchmarkMul64(b *testing.B) {
// 	for i := 0; i < b.N; i++ {
// 		Mul64(21000, 50)
// 	}
// }

func BenchmarkAddMany64(b *testing.B) {
	values := []uint64{21000, 21000, 21000, 21000, 21000}
	for i := 0; i < b.N; i++ {
		AddMany64(values...)
	}
}
