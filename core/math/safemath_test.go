// core/math/safemath_test.go
// COMPREHENSIVE TEST SUITE for Enhanced SafeMath
// AUDIT RESOLUTION: Tests for CertiK audit finding #1

package math

import (
	"math"
	"math/big"
	"testing"
)

// ============================================================================
// UINT64 ADDITION TESTS
// ============================================================================

func TestAdd64_Normal(t *testing.T) {
	tests := []struct {
		name string
		a, b uint64
		want uint64
	}{
		{"small numbers", 1, 2, 3},
		{"zero + zero", 0, 0, 0},
		{"zero + number", 0, 100, 100},
		{"number + zero", 100, 0, 100},
		{"large numbers", 1000000000, 2000000000, 3000000000},
		{"near max", math.MaxUint64 - 10, 5, math.MaxUint64 - 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Add64(tt.a, tt.b)
			if err != nil {
				t.Errorf("Add64(%d, %d) unexpected error: %v", tt.a, tt.b, err)
			}
			if got != tt.want {
				t.Errorf("Add64(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestAdd64_Overflow(t *testing.T) {
	tests := []struct {
		name string
		a, b uint64
	}{
		{"max + 1", math.MaxUint64, 1},
		{"max + max", math.MaxUint64, math.MaxUint64},
		{"near max + near max", math.MaxUint64 - 1, math.MaxUint64 - 1},
		{"half max + half max + 1", math.MaxUint64/2 + 1, math.MaxUint64/2 + 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Add64(tt.a, tt.b)
			if err == nil {
				t.Errorf("Add64(%d, %d) expected overflow error, got nil", tt.a, tt.b)
			}
			// Check that it's specifically an OverflowError
			if _, ok := err.(*OverflowError); !ok {
				t.Errorf("Add64(%d, %d) expected *OverflowError, got %T", tt.a, tt.b, err)
			}
		})
	}
}

func TestAdd64Saturating(t *testing.T) {
	tests := []struct {
		name string
		a, b uint64
		want uint64
	}{
		{"normal addition", 10, 20, 30},
		{"overflow to max", math.MaxUint64, 1, math.MaxUint64},
		{"double max to max", math.MaxUint64, math.MaxUint64, math.MaxUint64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Add64Saturating(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("Add64Saturating(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// ============================================================================
// UINT64 MULTIPLICATION TESTS
// ============================================================================

func TestMul64_Normal(t *testing.T) {
	tests := []struct {
		name string
		a, b uint64
		want uint64
	}{
		{"small numbers", 2, 3, 6},
		{"zero * number", 0, 100, 0},
		{"number * zero", 100, 0, 0},
		{"one * number", 1, 12345, 12345},
		{"number * one", 12345, 1, 12345},
		{"large safe multiplication", 1000000, 1000000, 1000000000000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Mul64(tt.a, tt.b)
			if err != nil {
				t.Errorf("Mul64(%d, %d) unexpected error: %v", tt.a, tt.b, err)
			}
			if got != tt.want {
				t.Errorf("Mul64(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestMul64_Overflow(t *testing.T) {
	tests := []struct {
		name string
		a, b uint64
	}{
		{"max * 2", math.MaxUint64, 2},
		{"max * max", math.MaxUint64, math.MaxUint64},
		{"large * large", math.MaxUint64 / 2, 3},
		{"sqrt(max) * sqrt(max) + 1", 4294967296, 4294967296}, // Approximately sqrt(MaxUint64)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Mul64(tt.a, tt.b)
			if err == nil {
				t.Errorf("Mul64(%d, %d) expected overflow error, got nil", tt.a, tt.b)
			}
			if _, ok := err.(*OverflowError); !ok {
				t.Errorf("Mul64(%d, %d) expected *OverflowError, got %T", tt.a, tt.b, err)
			}
		})
	}
}

func TestMul64Saturating(t *testing.T) {
	tests := []struct {
		name string
		a, b uint64
		want uint64
	}{
		{"normal multiplication", 10, 20, 200},
		{"overflow to max", math.MaxUint64, 2, math.MaxUint64},
		{"zero multiplication", 0, math.MaxUint64, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Mul64Saturating(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("Mul64Saturating(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// ============================================================================
// UINT64 SUBTRACTION TESTS
// ============================================================================

func TestSub64_Normal(t *testing.T) {
	tests := []struct {
		name string
		a, b uint64
		want uint64
	}{
		{"simple subtraction", 10, 3, 7},
		{"equal values", 100, 100, 0},
		{"subtract zero", 50, 0, 50},
		{"from max", math.MaxUint64, 1, math.MaxUint64 - 1},
		{"large subtraction", 1000000000, 999999999, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Sub64(tt.a, tt.b)
			if err != nil {
				t.Errorf("Sub64(%d, %d) unexpected error: %v", tt.a, tt.b, err)
			}
			if got != tt.want {
				t.Errorf("Sub64(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestSub64_Underflow(t *testing.T) {
	tests := []struct {
		name string
		a, b uint64
	}{
		{"1 - 2", 1, 2},
		{"0 - 1", 0, 1},
		{"small - large", 10, 100},
		{"0 - max", 0, math.MaxUint64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Sub64(tt.a, tt.b)
			if err == nil {
				t.Errorf("Sub64(%d, %d) expected underflow error, got nil", tt.a, tt.b)
			}
			if _, ok := err.(*UnderflowError); !ok {
				t.Errorf("Sub64(%d, %d) expected *UnderflowError, got %T", tt.a, tt.b, err)
			}
		})
	}
}

func TestSub64Saturating(t *testing.T) {
	tests := []struct {
		name string
		a, b uint64
		want uint64
	}{
		{"normal subtraction", 100, 50, 50},
		{"underflow to zero", 10, 20, 0},
		{"zero from zero", 0, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Sub64Saturating(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("Sub64Saturating(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// ============================================================================
// UINT64 DIVISION TESTS
// ============================================================================

func TestDiv64_Normal(t *testing.T) {
	tests := []struct {
		name string
		a, b uint64
		want uint64
	}{
		{"simple division", 10, 2, 5},
		{"no remainder", 100, 10, 10},
		{"with remainder", 10, 3, 3},
		{"divide by 1", 12345, 1, 12345},
		{"zero divided", 0, 5, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Div64(tt.a, tt.b)
			if err != nil {
				t.Errorf("Div64(%d, %d) unexpected error: %v", tt.a, tt.b, err)
			}
			if got != tt.want {
				t.Errorf("Div64(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestDiv64_DivisionByZero(t *testing.T) {
	tests := []uint64{0, 1, 100, math.MaxUint64}

	for _, a := range tests {
		t.Run("divide by zero", func(t *testing.T) {
			_, err := Div64(a, 0)
			if err == nil {
				t.Errorf("Div64(%d, 0) expected division by zero error, got nil", a)
			}
		})
	}
}

// ============================================================================
// UINT64 ADVANCED OPERATIONS TESTS
// ============================================================================

func TestAddMany64(t *testing.T) {
	tests := []struct {
		name   string
		values []uint64
		want   uint64
		hasErr bool
	}{
		{"empty slice", []uint64{}, 0, false},
		{"single value", []uint64{42}, 42, false},
		{"two values", []uint64{10, 20}, 30, false},
		{"multiple values", []uint64{1, 2, 3, 4, 5}, 15, false},
		{"with zeros", []uint64{10, 0, 20, 0, 30}, 60, false},
		{"overflow case", []uint64{math.MaxUint64, 1}, 0, true},
		{"overflow in middle", []uint64{math.MaxUint64 / 2, math.MaxUint64 / 2, math.MaxUint64 / 2}, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AddMany64(tt.values...)
			if tt.hasErr {
				if err == nil {
					t.Errorf("AddMany64(%v) expected error, got nil", tt.values)
				}
			} else {
				if err != nil {
					t.Errorf("AddMany64(%v) unexpected error: %v", tt.values, err)
				}
				if got != tt.want {
					t.Errorf("AddMany64(%v) = %d, want %d", tt.values, got, tt.want)
				}
			}
		})
	}
}

func TestMulAdd64(t *testing.T) {
	tests := []struct {
		name    string
		a, b, c uint64
		want    uint64
		hasErr  bool
	}{
		{"simple case", 2, 3, 4, 10, false},
		{"zero multiplication", 0, 100, 50, 50, false},
		{"zero addition", 5, 4, 0, 20, false},
		{"mul overflow", math.MaxUint64, 2, 0, 0, true},
		{"add overflow after mul", math.MaxUint64 / 2, 2, 100, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MulAdd64(tt.a, tt.b, tt.c)
			if tt.hasErr {
				if err == nil {
					t.Errorf("MulAdd64(%d, %d, %d) expected error, got nil", tt.a, tt.b, tt.c)
				}
			} else {
				if err != nil {
					t.Errorf("MulAdd64(%d, %d, %d) unexpected error: %v", tt.a, tt.b, tt.c, err)
				}
				if got != tt.want {
					t.Errorf("MulAdd64(%d, %d, %d) = %d, want %d", tt.a, tt.b, tt.c, got, tt.want)
				}
			}
		})
	}
}

func TestMulDiv64(t *testing.T) {
	tests := []struct {
		name    string
		a, b, c uint64
		want    uint64
		hasErr  bool
	}{
		{"simple case", 10, 5, 2, 25, false},
		{"exact division", 100, 200, 10, 2000, false},
		{"with remainder", 10, 3, 2, 15, false},
		{"divide by zero", 10, 5, 0, 0, true},
		{"large intermediate", math.MaxUint64 / 2, 2, 2, math.MaxUint64 / 2, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MulDiv64(tt.a, tt.b, tt.c)
			if tt.hasErr {
				if err == nil {
					t.Errorf("MulDiv64(%d, %d, %d) expected error, got nil", tt.a, tt.b, tt.c)
				}
			} else {
				if err != nil {
					t.Errorf("MulDiv64(%d, %d, %d) unexpected error: %v", tt.a, tt.b, tt.c, err)
				}
				if got != tt.want {
					t.Errorf("MulDiv64(%d, %d, %d) = %d, want %d", tt.a, tt.b, tt.c, got, tt.want)
				}
			}
		})
	}
}

func TestPow64(t *testing.T) {
	tests := []struct {
		name   string
		base   uint64
		exp    uint64
		want   uint64
		hasErr bool
	}{
		{"2^3", 2, 3, 8, false},
		{"2^10", 2, 10, 1024, false},
		{"10^6", 10, 6, 1000000, false},
		{"any^0", 999, 0, 1, false},
		{"0^any", 0, 100, 0, false},
		{"1^any", 1, 100, 1, false},
		{"2^64 overflow", 2, 64, 0, true},
		{"large base overflow", 1000, 20, 0, true},
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

// ============================================================================
// INT64 ADDITION TESTS
// ============================================================================

func TestSafeAdd_Normal(t *testing.T) {
	tests := []struct {
		name string
		a, b int64
		want int64
	}{
		{"positive numbers", 10, 20, 30},
		{"negative numbers", -10, -20, -30},
		{"mixed signs", 10, -5, 5},
		{"zero cases", 0, 100, 100},
		{"near max", math.MaxInt64 - 10, 5, math.MaxInt64 - 5},
		{"near min", math.MinInt64 + 10, -5, math.MinInt64 + 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SafeAdd(tt.a, tt.b)
			if err != nil {
				t.Errorf("SafeAdd(%d, %d) unexpected error: %v", tt.a, tt.b, err)
			}
			if got != tt.want {
				t.Errorf("SafeAdd(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestSafeAdd_Overflow(t *testing.T) {
	tests := []struct {
		name string
		a, b int64
	}{
		{"max + 1", math.MaxInt64, 1},
		{"max + max", math.MaxInt64, math.MaxInt64},
		{"near max overflow", math.MaxInt64 - 1, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := SafeAdd(tt.a, tt.b)
			if err == nil {
				t.Errorf("SafeAdd(%d, %d) expected overflow error, got nil", tt.a, tt.b)
			}
			if _, ok := err.(*OverflowError); !ok {
				t.Errorf("SafeAdd(%d, %d) expected *OverflowError, got %T", tt.a, tt.b, err)
			}
		})
	}
}

func TestSafeAdd_Underflow(t *testing.T) {
	tests := []struct {
		name string
		a, b int64
	}{
		{"min + -1", math.MinInt64, -1},
		{"min + min", math.MinInt64, math.MinInt64},
		{"near min underflow", math.MinInt64 + 1, -2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := SafeAdd(tt.a, tt.b)
			if err == nil {
				t.Errorf("SafeAdd(%d, %d) expected underflow error, got nil", tt.a, tt.b)
			}
			if _, ok := err.(*UnderflowError); !ok {
				t.Errorf("SafeAdd(%d, %d) expected *UnderflowError, got %T", tt.a, tt.b, err)
			}
		})
	}
}

// ============================================================================
// INT64 MULTIPLICATION TESTS
// ============================================================================

func TestSafeMul_Normal(t *testing.T) {
	tests := []struct {
		name string
		a, b int64
		want int64
	}{
		{"positive * positive", 10, 20, 200},
		{"negative * negative", -10, -20, 200},
		{"positive * negative", 10, -20, -200},
		{"zero cases", 0, 100, 0},
		{"one cases", 1, 100, 100},
		{"negative one", -1, 100, -100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SafeMul(tt.a, tt.b)
			if err != nil {
				t.Errorf("SafeMul(%d, %d) unexpected error: %v", tt.a, tt.b, err)
			}
			if got != tt.want {
				t.Errorf("SafeMul(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestSafeMul_EdgeCases(t *testing.T) {
	tests := []struct {
		name string
		a, b int64
	}{
		{"MinInt64 * -1", math.MinInt64, -1},
		{"-1 * MinInt64", -1, math.MinInt64},
		{"max * 2", math.MaxInt64, 2},
		{"large overflow", math.MaxInt64 / 2, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := SafeMul(tt.a, tt.b)
			if err == nil {
				t.Errorf("SafeMul(%d, %d) expected overflow error, got nil", tt.a, tt.b)
			}
		})
	}
}

// ============================================================================
// INT64 DIVISION TESTS
// ============================================================================

func TestSafeDiv_Normal(t *testing.T) {
	tests := []struct {
		name string
		a, b int64
		want int64
	}{
		{"positive / positive", 100, 10, 10},
		{"negative / negative", -100, -10, 10},
		{"positive / negative", 100, -10, -10},
		{"zero / number", 0, 10, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SafeDiv(tt.a, tt.b)
			if err != nil {
				t.Errorf("SafeDiv(%d, %d) unexpected error: %v", tt.a, tt.b, err)
			}
			if got != tt.want {
				t.Errorf("SafeDiv(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestSafeDiv_EdgeCases(t *testing.T) {
	// Division by zero
	_, err := SafeDiv(100, 0)
	if err == nil {
		t.Error("SafeDiv(100, 0) expected division by zero error")
	}

	// MinInt64 / -1 overflow
	_, err = SafeDiv(math.MinInt64, -1)
	if err == nil {
		t.Error("SafeDiv(MinInt64, -1) expected overflow error")
	}
	if _, ok := err.(*OverflowError); !ok {
		t.Errorf("SafeDiv(MinInt64, -1) expected *OverflowError, got %T", err)
	}
}

// ============================================================================
// SPECIAL INT64 OPERATIONS TESTS
// ============================================================================

func TestSafeAbs(t *testing.T) {
	tests := []struct {
		name   string
		input  int64
		want   int64
		hasErr bool
	}{
		{"positive", 42, 42, false},
		{"negative", -42, 42, false},
		{"zero", 0, 0, false},
		{"max", math.MaxInt64, math.MaxInt64, false},
		{"min int64", math.MinInt64, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SafeAbs(tt.input)
			if tt.hasErr {
				if err == nil {
					t.Errorf("SafeAbs(%d) expected error, got nil", tt.input)
				}
			} else {
				if err != nil {
					t.Errorf("SafeAbs(%d) unexpected error: %v", tt.input, err)
				}
				if got != tt.want {
					t.Errorf("SafeAbs(%d) = %d, want %d", tt.input, got, tt.want)
				}
			}
		})
	}
}

func TestSafeNeg(t *testing.T) {
	tests := []struct {
		name   string
		input  int64
		want   int64
		hasErr bool
	}{
		{"positive", 42, -42, false},
		{"negative", -42, 42, false},
		{"zero", 0, 0, false},
		{"max", math.MaxInt64, -math.MaxInt64, false},
		{"min int64", math.MinInt64, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SafeNeg(tt.input)
			if tt.hasErr {
				if err == nil {
					t.Errorf("SafeNeg(%d) expected error, got nil", tt.input)
				}
			} else {
				if err != nil {
					t.Errorf("SafeNeg(%d) unexpected error: %v", tt.input, err)
				}
				if got != tt.want {
					t.Errorf("SafeNeg(%d) = %d, want %d", tt.input, got, tt.want)
				}
			}
		})
	}
}

func TestSafePercentage(t *testing.T) {
	tests := []struct {
		name    string
		amount  int64
		percent int64
		want    int64
		hasErr  bool
	}{
		{"10% of 100", 100, 10, 10, false},
		{"50% of 200", 200, 50, 100, false},
		{"0% of anything", 1000, 0, 0, false},
		{"100% of value", 50, 100, 50, false},
		{"invalid negative %", 100, -10, 0, true},
		{"invalid > 100%", 100, 150, 0, true},
		{"zero amount", 0, 50, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SafePercentage(tt.amount, tt.percent)
			if tt.hasErr {
				if err == nil {
					t.Errorf("SafePercentage(%d, %d) expected error, got nil", tt.amount, tt.percent)
				}
			} else {
				if err != nil {
					t.Errorf("SafePercentage(%d, %d) unexpected error: %v", tt.amount, tt.percent, err)
				}
				if got != tt.want {
					t.Errorf("SafePercentage(%d, %d) = %d, want %d", tt.amount, tt.percent, got, tt.want)
				}
			}
		})
	}
}

// ============================================================================
// BIG.INT TESTS
// ============================================================================

func TestSafePercentageBig(t *testing.T) {
	tests := []struct {
		name    string
		amount  string
		percent int64
		want    string
		hasErr  bool
	}{
		{"10% of 1000", "1000", 10, "100", false},
		{"50% of large", "1000000000000", 50, "500000000000", false},
		{"0% of anything", "999999", 0, "0", false},
		{"invalid percent", "100", 150, "", true},
		{"nil amount", "", 50, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var amount *big.Int
			if tt.amount != "" {
				amount = new(big.Int)
				amount.SetString(tt.amount, 10)
			}

			got, err := SafePercentageBig(amount, tt.percent)
			if tt.hasErr {
				if err == nil {
					t.Errorf("SafePercentageBig(%s, %d) expected error, got nil", tt.amount, tt.percent)
				}
			} else {
				if err != nil {
					t.Errorf("SafePercentageBig(%s, %d) unexpected error: %v", tt.amount, tt.percent, err)
				}
				want := new(big.Int)
				want.SetString(tt.want, 10)
				if got.Cmp(want) != 0 {
					t.Errorf("SafePercentageBig(%s, %d) = %s, want %s", tt.amount, tt.percent, got.String(), tt.want)
				}
			}
		})
	}
}

// ============================================================================
// CONVERSION TESTS
// ============================================================================

func TestUint64ToInt64(t *testing.T) {
	tests := []struct {
		name   string
		input  uint64
		want   int64
		hasErr bool
	}{
		{"small value", 100, 100, false},
		{"zero", 0, 0, false},
		{"max valid", math.MaxInt64, math.MaxInt64, false},
		{"overflow", uint64(math.MaxInt64) + 1, 0, true},
		{"max uint64", math.MaxUint64, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Uint64ToInt64(tt.input)
			if tt.hasErr {
				if err == nil {
					t.Errorf("Uint64ToInt64(%d) expected error, got nil", tt.input)
				}
			} else {
				if err != nil {
					t.Errorf("Uint64ToInt64(%d) unexpected error: %v", tt.input, err)
				}
				if got != tt.want {
					t.Errorf("Uint64ToInt64(%d) = %d, want %d", tt.input, got, tt.want)
				}
			}
		})
	}
}

func TestInt64ToUint64(t *testing.T) {
	tests := []struct {
		name   string
		input  int64
		want   uint64
		hasErr bool
	}{
		{"positive", 100, 100, false},
		{"zero", 0, 0, false},
		{"max int64", math.MaxInt64, uint64(math.MaxInt64), false},
		{"negative", -1, 0, true},
		{"min int64", math.MinInt64, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Int64ToUint64(tt.input)
			if tt.hasErr {
				if err == nil {
					t.Errorf("Int64ToUint64(%d) expected error, got nil", tt.input)
				}
			} else {
				if err != nil {
					t.Errorf("Int64ToUint64(%d) unexpected error: %v", tt.input, err)
				}
				if got != tt.want {
					t.Errorf("Int64ToUint64(%d) = %d, want %d", tt.input, got, tt.want)
				}
			}
		})
	}
}

// ============================================================================
// GAS CALCULATION TESTS
// ============================================================================

func TestCalculateGasCost(t *testing.T) {
	tests := []struct {
		name     string
		gasUsed  uint64
		gasPrice uint64
		want     uint64
		hasErr   bool
	}{
		{"normal calculation", 21000, 50, 1050000, false},
		{"zero gas", 0, 100, 0, false},
		{"zero price", 1000, 0, 0, false},
		{"overflow", math.MaxUint64, 2, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CalculateGasCost(tt.gasUsed, tt.gasPrice)
			if tt.hasErr {
				if err == nil {
					t.Errorf("CalculateGasCost(%d, %d) expected error, got nil", tt.gasUsed, tt.gasPrice)
				}
			} else {
				if err != nil {
					t.Errorf("CalculateGasCost(%d, %d) unexpected error: %v", tt.gasUsed, tt.gasPrice, err)
				}
				if got != tt.want {
					t.Errorf("CalculateGasCost(%d, %d) = %d, want %d", tt.gasUsed, tt.gasPrice, got, tt.want)
				}
			}
		})
	}
}

func TestValidateGasLimit(t *testing.T) {
	tests := []struct {
		name       string
		gasLimit   uint64
		maxAllowed uint64
		hasErr     bool
	}{
		{"valid limit", 21000, 100000, false},
		{"at max", 100000, 100000, false},
		{"exceeds max", 100001, 100000, true},
		{"zero limit", 0, 100000, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateGasLimit(tt.gasLimit, tt.maxAllowed)
			if tt.hasErr {
				if err == nil {
					t.Errorf("ValidateGasLimit(%d, %d) expected error, got nil", tt.gasLimit, tt.maxAllowed)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateGasLimit(%d, %d) unexpected error: %v", tt.gasLimit, tt.maxAllowed, err)
				}
			}
		})
	}
}

// ============================================================================
// BENCHMARK TESTS
// ============================================================================

func BenchmarkAdd64(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Add64(12345, 67890)
	}
}

func BenchmarkMul64(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Mul64(12345, 67890)
	}
}

func BenchmarkSafeAdd(b *testing.B) {
	for i := 0; i < b.N; i++ {
		SafeAdd(12345, 67890)
	}
}

func BenchmarkSafeMul(b *testing.B) {
	for i := 0; i < b.N; i++ {
		SafeMul(12345, 67890)
	}
}
