// core/math/safemath_test.go
package math

import (
	"math"
	"testing"
)

// Test SafeAdd overflow scenarios
func TestSafeAdd_Overflow(t *testing.T) {
	tests := []struct {
		name string
		a    int64
		b    int64
	}{
		{"max plus one", math.MaxInt64, 1},
		{"max plus max", math.MaxInt64, math.MaxInt64},
		{"large positive overflow", math.MaxInt64 / 2, math.MaxInt64/2 + 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := SafeAdd(tt.a, tt.b)
			if err == nil {
				t.Errorf("SafeAdd(%d, %d) expected overflow error, got nil", tt.a, tt.b)
			}
		})
	}
}

// Test SafeAdd underflow scenarios
func TestSafeAdd_Underflow(t *testing.T) {
	tests := []struct {
		name string
		a    int64
		b    int64
	}{
		{"min plus neg one", math.MinInt64, -1},
		{"min plus min", math.MinInt64, math.MinInt64},
		{"large negative underflow", math.MinInt64 / 2, math.MinInt64/2 - 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := SafeAdd(tt.a, tt.b)
			if err == nil {
				t.Errorf("SafeAdd(%d, %d) expected underflow error, got nil", tt.a, tt.b)
			}
		})
	}
}

// Test SafeAdd success scenarios
func TestSafeAdd_Success(t *testing.T) {
	tests := []struct {
		name     string
		a        int64
		b        int64
		expected int64
	}{
		{"small positive", 100, 200, 300},
		{"zero plus positive", 0, 100, 100},
		{"negative plus positive", -50, 100, 50},
		{"large safe addition", math.MaxInt64 / 2, math.MaxInt64 / 2, math.MaxInt64 - 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := SafeAdd(tt.a, tt.b)
			if err != nil {
				t.Errorf("SafeAdd(%d, %d) unexpected error: %v", tt.a, tt.b, err)
			}
			if result != tt.expected {
				t.Errorf("SafeAdd(%d, %d) = %d, expected %d", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

// Test SafeSub underflow scenarios
func TestSafeSub_Underflow(t *testing.T) {
	tests := []struct {
		name string
		a    int64
		b    int64
	}{
		{"min minus one", math.MinInt64, 1},
		{"min minus positive", math.MinInt64, 100},
		{"very negative minus positive", math.MinInt64 + 100, 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := SafeSub(tt.a, tt.b)
			if err == nil {
				t.Errorf("SafeSub(%d, %d) expected underflow error, got nil", tt.a, tt.b)
			}
		})
	}
}

// Test SafeSub overflow scenarios
func TestSafeSub_Overflow(t *testing.T) {
	tests := []struct {
		name string
		a    int64
		b    int64
	}{
		{"max minus negative one", math.MaxInt64, -1},
		{"max minus negative large", math.MaxInt64, -100},
		{"large positive minus MinInt64", math.MaxInt64 / 2, math.MinInt64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := SafeSub(tt.a, tt.b)
			if err == nil {
				t.Errorf("SafeSub(%d, %d) expected overflow error, got nil", tt.a, tt.b)
			}
		})
	}
}

// Test SafeSub success scenarios
func TestSafeSub_Success(t *testing.T) {
	tests := []struct {
		name     string
		a        int64
		b        int64
		expected int64
	}{
		{"simple subtraction", 300, 100, 200},
		{"zero minus negative", 0, -100, 100},
		{"negative minus negative", -50, -100, 50},
		{"equal values", 100, 100, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := SafeSub(tt.a, tt.b)
			if err != nil {
				t.Errorf("SafeSub(%d, %d) unexpected error: %v", tt.a, tt.b, err)
			}
			if result != tt.expected {
				t.Errorf("SafeSub(%d, %d) = %d, expected %d", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

// Test SafeMul overflow scenarios
func TestSafeMul_Overflow(t *testing.T) {
	tests := []struct {
		name string
		a    int64
		b    int64
	}{
		{"max times two", math.MaxInt64, 2},
		{"large multiplication", math.MaxInt64 / 2, 3},
		{"edge case -1 * MinInt64", -1, math.MinInt64},
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

// Test SafeMul success scenarios
func TestSafeMul_Success(t *testing.T) {
	tests := []struct {
		name     string
		a        int64
		b        int64
		expected int64
	}{
		{"simple multiplication", 100, 200, 20000},
		{"multiply by zero", math.MaxInt64, 0, 0},
		{"multiply by one", 12345, 1, 12345},
		{"negative multiplication", -100, 50, -5000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := SafeMul(tt.a, tt.b)
			if err != nil {
				t.Errorf("SafeMul(%d, %d) unexpected error: %v", tt.a, tt.b, err)
			}
			if result != tt.expected {
				t.Errorf("SafeMul(%d, %d) = %d, expected %d", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

// Test SafeDiv division by zero
func TestSafeDiv_DivisionByZero(t *testing.T) {
	_, err := SafeDiv(100, 0)
	if err == nil {
		t.Error("SafeDiv(100, 0) expected division by zero error, got nil")
	}
}

// Test SafeDiv overflow scenario
func TestSafeDiv_Overflow(t *testing.T) {
	_, err := SafeDiv(math.MinInt64, -1)
	if err == nil {
		t.Error("SafeDiv(MinInt64, -1) expected overflow error, got nil")
	}
}

// Test SafeDiv success scenarios
func TestSafeDiv_Success(t *testing.T) {
	tests := []struct {
		name     string
		a        int64
		b        int64
		expected int64
	}{
		{"simple division", 100, 10, 10},
		{"division with remainder", 100, 3, 33},
		{"negative division", -100, 10, -10},
		{"divide by one", 12345, 1, 12345},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := SafeDiv(tt.a, tt.b)
			if err != nil {
				t.Errorf("SafeDiv(%d, %d) unexpected error: %v", tt.a, tt.b, err)
			}
			if result != tt.expected {
				t.Errorf("SafeDiv(%d, %d) = %d, expected %d", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

// Test SafePercentage for slashing calculations
func TestSafePercentage(t *testing.T) {
	tests := []struct {
		name     string
		amount   int64
		percent  int64
		expected int64
		wantErr  bool
	}{
		{"10% of 1000", 1000, 10, 100, false},
		{"5% of 2500", 2500, 5, 125, false},
		{"100% of 500", 500, 100, 500, false},
		{"0% of 1000", 1000, 0, 0, false},
		{"50% overflow", math.MaxInt64, 50, 0, true},
		{"invalid percent negative", 1000, -1, 0, true},
		{"invalid percent over 100", 1000, 101, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := SafePercentage(tt.amount, tt.percent)
			if (err != nil) != tt.wantErr {
				t.Errorf("SafePercentage(%d, %d) error = %v, wantErr %v",
					tt.amount, tt.percent, err, tt.wantErr)
				return
			}
			if !tt.wantErr && result != tt.expected {
				t.Errorf("SafePercentage(%d, %d) = %d, expected %d",
					tt.amount, tt.percent, result, tt.expected)
			}
		})
	}
}

// Test MustSafeAdd panic behavior
func TestMustSafeAdd_Panic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustSafeAdd(MaxInt64, 1) should panic")
		}
	}()
	MustSafeAdd(math.MaxInt64, 1)
}

// Test MustSafeAdd success
func TestMustSafeAdd_Success(t *testing.T) {
	result := MustSafeAdd(100, 200)
	if result != 300 {
		t.Errorf("MustSafeAdd(100, 200) = %d, expected 300", result)
	}
}

// Benchmark SafeAdd vs regular addition
func BenchmarkSafeAdd(b *testing.B) {
	for i := 0; i < b.N; i++ {
		SafeAdd(1000000, 2000000)
	}
}

func BenchmarkRegularAdd(b *testing.B) {
	var result int64
	for i := 0; i < b.N; i++ {
		result = 1000000 + 2000000
	}
	_ = result
}
