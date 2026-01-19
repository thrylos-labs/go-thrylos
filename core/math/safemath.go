// core/math/safemath.go
// Safe arithmetic operations to prevent integer overflow/underflow vulnerabilities
// CRITICAL: All balance, token, and GAS operations MUST use these functions
// SECURITY FIX: Added uint64 functions for gas calculations (CRITICAL-01)

package math

import (
	"fmt"
	"math"
	"math/big"

	"github.com/thrylos-labs/go-thrylos/core/security"
)

// ============================================================================
// UINT64 SAFE OPERATIONS (FOR GAS CALCULATIONS)
// ============================================================================
// These are critical for preventing gas calculation overflows that could
// bypass gas limits or cause consensus failures

// Add64 adds two uint64 values with overflow protection
// Returns error if operation would overflow
// USE THIS FOR: gas_used + base_gas, totalGas calculations
func Add64(a, b uint64) (uint64, error) {
	if a > math.MaxUint64-b {
		// Log potential attack
		security.LogGasOverflowAttempt("Add64", a, b)
		return 0, fmt.Errorf("uint64 overflow: %d + %d exceeds MaxUint64", a, b)
	}
	return a + b, nil
}

// Mul64 multiplies two uint64 values with overflow protection
// Returns error if operation would overflow
// USE THIS FOR: gasLimit * gasPrice, gas * priorityFee calculations
func Mul64(a, b uint64) (uint64, error) {
	if a == 0 || b == 0 {
		return 0, nil
	}

	result := a * b
	if result/b != a {
		// Log potential attack
		security.LogGasOverflowAttempt("Mul64", a, b)
		return 0, fmt.Errorf("uint64 overflow: %d * %d exceeds MaxUint64", a, b)
	}

	return result, nil
}

// Sub64 subtracts two uint64 values with underflow protection
// Returns error if b > a (would underflow)
// USE THIS FOR: gas_limit - gas_used calculations
func Sub64(a, b uint64) (uint64, error) {
	if b > a {
		return 0, fmt.Errorf("uint64 underflow: %d - %d would be negative", a, b)
	}
	return a - b, nil
}

// Div64 divides two uint64 values with protection
// Returns error if dividing by zero
func Div64(a, b uint64) (uint64, error) {
	if b == 0 {
		return 0, fmt.Errorf("division by zero")
	}
	return a / b, nil
}

// AddMany64 adds multiple uint64 values with overflow protection
// Returns error if any intermediate operation would overflow
// USE THIS FOR: totalGas = gas1 + gas2 + gas3 + ...
func AddMany64(values ...uint64) (uint64, error) {
	if len(values) == 0 {
		return 0, nil
	}

	result := values[0]
	for i := 1; i < len(values); i++ {
		newResult, err := Add64(result, values[i])
		if err != nil {
			return 0, fmt.Errorf("overflow at position %d: %v", i, err)
		}
		result = newResult
	}
	return result, nil
}

// MulAdd64 performs (a * b) + c with overflow protection
// Useful for: (gasLimit * gasPrice) + baseFee calculations
func MulAdd64(a, b, c uint64) (uint64, error) {
	// First multiply
	product, err := Mul64(a, b)
	if err != nil {
		return 0, fmt.Errorf("multiplication overflow: %v", err)
	}

	// Then add
	result, err := Add64(product, c)
	if err != nil {
		return 0, fmt.Errorf("addition overflow after multiplication: %v", err)
	}

	return result, nil
}

// ============================================================================
// CONVENIENCE WRAPPERS (USE WITH CAUTION)
// ============================================================================
// These panic on overflow. Only use when overflow is truly impossible.

// MustAdd64 is a convenience wrapper that panics on overflow
// WARNING: Only use when overflow is provably impossible
func MustAdd64(a, b uint64) uint64 {
	result, err := Add64(a, b)
	if err != nil {
		panic(fmt.Sprintf("MustAdd64 failed: %v", err))
	}
	return result
}

// MustMul64 is a convenience wrapper that panics on overflow
// WARNING: Only use when overflow is provably impossible
func MustMul64(a, b uint64) uint64 {
	result, err := Mul64(a, b)
	if err != nil {
		panic(fmt.Sprintf("MustMul64 failed: %v", err))
	}
	return result
}

// MustSub64 is a convenience wrapper that panics on underflow
// WARNING: Only use when underflow is provably impossible
func MustSub64(a, b uint64) uint64 {
	result, err := Sub64(a, b)
	if err != nil {
		panic(fmt.Sprintf("MustSub64 failed: %v", err))
	}
	return result
}

// ============================================================================
// INT64 SAFE OPERATIONS (EXISTING - FOR BALANCES/STAKES)
// ============================================================================

// SafeAdd adds two int64 values with overflow protection
// Returns error if operation would overflow or underflow
func SafeAdd(a, b int64) (int64, error) {
	// Check for overflow when both positive
	if a > 0 && b > 0 && a > math.MaxInt64-b {
		return 0, fmt.Errorf("integer overflow: %d + %d exceeds MaxInt64", a, b)
	}

	// Check for underflow when both negative
	if a < 0 && b < 0 && a < math.MinInt64-b {
		return 0, fmt.Errorf("integer underflow: %d + %d below MinInt64", a, b)
	}

	return a + b, nil
}

// SafeSub subtracts two int64 values with underflow protection
// Returns error if operation would overflow or underflow
func SafeSub(a, b int64) (int64, error) {
	// Check overflow/underflow for a - b

	if b > 0 {
		// Subtracting positive: result gets smaller (more negative)
		// Underflow if a - b < MinInt64
		// Rearrange: a < MinInt64 + b
		if a < math.MinInt64+b {
			return 0, fmt.Errorf("integer underflow: %d - %d below MinInt64", a, b)
		}
	} else if b < 0 {
		// Subtracting negative: result gets larger (more positive)
		// Overflow if a - b > MaxInt64
		// Rearrange: a > MaxInt64 + b (b is negative, so this lowers the threshold)
		if a > math.MaxInt64+b {
			return 0, fmt.Errorf("integer overflow: %d - (%d) exceeds MaxInt64", a, b)
		}
	}
	// b == 0: no overflow possible

	return a - b, nil
}

// SafeMul multiplies two int64 values with overflow protection
// Returns error if operation would overflow or underflow
func SafeMul(a, b int64) (int64, error) {
	// Special cases
	if a == 0 || b == 0 {
		return 0, nil
	}

	// Check if result would overflow
	if a == -1 && b == math.MinInt64 {
		return 0, fmt.Errorf("integer overflow: %d * %d exceeds MaxInt64", a, b)
	}
	if b == -1 && a == math.MinInt64 {
		return 0, fmt.Errorf("integer overflow: %d * %d exceeds MaxInt64", a, b)
	}

	result := a * b
	if result/b != a {
		return 0, fmt.Errorf("integer overflow: %d * %d exceeds safe range", a, b)
	}

	return result, nil
}

// SafeDiv divides two int64 values with protection
// Returns error if dividing by zero or overflow edge case
func SafeDiv(a, b int64) (int64, error) {
	if b == 0 {
		return 0, fmt.Errorf("division by zero")
	}

	// Edge case: MinInt64 / -1 would overflow
	if a == math.MinInt64 && b == -1 {
		return 0, fmt.Errorf("integer overflow: MinInt64 / -1 exceeds MaxInt64")
	}

	return a / b, nil
}

// SafePercentage calculates percentage with overflow protection
// Specifically for slashing penalties: amount * percent / 100
func SafePercentage(amount int64, percent int64) (int64, error) {
	if percent < 0 || percent > 100 {
		return 0, fmt.Errorf("invalid percentage: %d (must be 0-100)", percent)
	}

	if amount == 0 {
		return 0, nil
	}

	// Calculate with overflow protection
	product, err := SafeMul(amount, percent)
	if err != nil {
		return 0, fmt.Errorf("percentage calculation overflow: %v", err)
	}

	result, err := SafeDiv(product, 100)
	if err != nil {
		return 0, fmt.Errorf("percentage calculation division error: %v", err)
	}

	return result, nil
}

// MustSafeAdd is a convenience wrapper that panics on overflow
// Use only in contexts where overflow is truly impossible
func MustSafeAdd(a, b int64) int64 {
	result, err := SafeAdd(a, b)
	if err != nil {
		panic(fmt.Sprintf("SafeAdd failed: %v", err))
	}
	return result
}

// MustSafeSub is a convenience wrapper that panics on underflow
// Use only in contexts where underflow is truly impossible
func MustSafeSub(a, b int64) int64 {
	result, err := SafeSub(a, b)
	if err != nil {
		panic(fmt.Sprintf("SafeSub failed: %v", err))
	}
	return result
}

// ============================================================================
// BIG.INT SAFE OPERATIONS (FOR LARGE VALUES)
// ============================================================================

// SafePercentageBig calculates a percentage of a BigInt amount.
// amount: The total amount (*big.Int)
// percent: The percentage to calculate (int64, e.g., 5 for 5%)
// Returns: (amount * percent) / 100
func SafePercentageBig(amount *big.Int, percent int64) (*big.Int, error) {
	if amount == nil {
		return nil, fmt.Errorf("amount cannot be nil")
	}
	if percent < 0 || percent > 100 {
		return nil, fmt.Errorf("invalid percentage: %d (must be 0-100)", percent)
	}

	// 1. Create BigInt for percentage
	percentBig := big.NewInt(percent)

	// 2. Multiply: amount * percent
	product := new(big.Int).Mul(amount, percentBig)

	// 3. Divide: product / 100
	divisor := big.NewInt(100)
	result := new(big.Int).Div(product, divisor)

	return result, nil
}

// ============================================================================
// GAS ESTIMATION HELPERS
// ============================================================================

// EstimateTotalGas calculates total gas with overflow protection
// Useful for validating blocks or transaction pools
func EstimateTotalGas(gasValues []uint64) (uint64, error) {
	return AddMany64(gasValues...)
}

// CalculateGasCost calculates gas * price with overflow protection
// Returns gas cost or error if overflow
func CalculateGasCost(gasUsed, gasPrice uint64) (uint64, error) {
	return Mul64(gasUsed, gasPrice)
}

// ValidateGasLimit checks if gas limit is within acceptable range
func ValidateGasLimit(gasLimit uint64, maxAllowed uint64) error {
	if gasLimit > maxAllowed {
		return fmt.Errorf("gas limit %d exceeds maximum allowed %d", gasLimit, maxAllowed)
	}
	if gasLimit == 0 {
		return fmt.Errorf("gas limit cannot be zero")
	}
	return nil
}
