// core/math/safemath.go
// ENHANCED VERSION - Comprehensive Safe Arithmetic Operations
// SECURITY: All balance, token, gas, and stake operations MUST use these functions
// AUDIT RESOLUTION: Addresses CertiK audit finding #1 - Integer Overflow Protection

package math

import (
	"fmt"
	"math"
	"math/big"

	"github.com/thrylos-labs/go-thrylos/core/security"
)

// ============================================================================
// ERROR TYPES - Explicit error handling for different overflow scenarios
// ============================================================================

type OverflowError struct {
	Operation string
	A, B      interface{}
	Message   string
}

func (e *OverflowError) Error() string {
	return fmt.Sprintf("overflow in %s(%v, %v): %s", e.Operation, e.A, e.B, e.Message)
}

type UnderflowError struct {
	Operation string
	A, B      interface{}
	Message   string
}

func (e *UnderflowError) Error() string {
	return fmt.Sprintf("underflow in %s(%v, %v): %s", e.Operation, e.A, e.B, e.Message)
}

// ============================================================================
// UINT64 SAFE OPERATIONS (FOR GAS CALCULATIONS)
// ============================================================================
// CRITICAL: These prevent gas calculation overflows that could bypass gas limits
// or cause consensus failures

// Add64 adds two uint64 values with overflow protection
// Returns error if operation would overflow
// USE THIS FOR: gas_used + base_gas, totalGas calculations
// COMPLEXITY: O(1)
// SECURITY: Logs potential attack attempts
func Add64(a, b uint64) (uint64, error) {
	// Check for overflow: if a + b would exceed MaxUint64
	// Mathematically: a + b > MaxUint64
	// Rearranged to avoid overflow in check: a > MaxUint64 - b
	if a > math.MaxUint64-b {
		// Log potential attack for security monitoring
		security.LogGasOverflowAttempt("Add64", a, b)
		return 0, &OverflowError{
			Operation: "Add64",
			A:         a,
			B:         b,
			Message:   fmt.Sprintf("%d + %d exceeds MaxUint64 (%d)", a, b, uint64(math.MaxUint64)),
		}
	}
	return a + b, nil
}

// Add64Safe is an alias for Add64 for backward compatibility
func Add64Safe(a, b uint64) (uint64, error) {
	return Add64(a, b)
}

// Mul64 multiplies two uint64 values with overflow protection
// Returns error if operation would overflow
// USE THIS FOR: gasLimit * gasPrice, gas * priorityFee calculations
// COMPLEXITY: O(1)
// SECURITY: Prevents multiplication overflow attacks
func Mul64(a, b uint64) (uint64, error) {
	// Handle zero cases (no overflow possible)
	if a == 0 || b == 0 {
		return 0, nil
	}

	// Perform multiplication
	result := a * b

	// Check for overflow by reversing the operation
	// If a * b overflowed, then result / b != a
	if result/b != a {
		// Log potential attack
		security.LogGasOverflowAttempt("Mul64", a, b)
		return 0, &OverflowError{
			Operation: "Mul64",
			A:         a,
			B:         b,
			Message:   fmt.Sprintf("%d * %d exceeds MaxUint64 (%d)", a, b, uint64(math.MaxUint64)),
		}
	}

	return result, nil
}

// Mul64Safe is an alias for Mul64 for backward compatibility
func Mul64Safe(a, b uint64) (uint64, error) {
	return Mul64(a, b)
}

// Sub64 subtracts two uint64 values with underflow protection
// Returns error if b > a (would underflow)
// USE THIS FOR: gas_limit - gas_used calculations
// COMPLEXITY: O(1)
// SECURITY: Prevents underflow that could lead to wraparound
func Sub64(a, b uint64) (uint64, error) {
	if b > a {
		return 0, &UnderflowError{
			Operation: "Sub64",
			A:         a,
			B:         b,
			Message:   fmt.Sprintf("%d - %d would be negative (underflow)", a, b),
		}
	}
	return a - b, nil
}

// Sub64Safe is an alias for Sub64 for backward compatibility
func Sub64Safe(a, b uint64) (uint64, error) {
	return Sub64(a, b)
}

// Div64 divides two uint64 values with protection
// Returns error if dividing by zero
// COMPLEXITY: O(1)
func Div64(a, b uint64) (uint64, error) {
	if b == 0 {
		return 0, fmt.Errorf("division by zero: %d / 0", a)
	}
	return a / b, nil
}

// Div64Safe is an alias for Div64 for backward compatibility
func Div64Safe(a, b uint64) (uint64, error) {
	return Div64(a, b)
}

// Mod64 calculates a modulo b with protection
// Returns error if dividing by zero
// COMPLEXITY: O(1)
func Mod64(a, b uint64) (uint64, error) {
	if b == 0 {
		return 0, fmt.Errorf("modulo by zero: %d %% 0", a)
	}
	return a % b, nil
}

// ============================================================================
// ADVANCED UINT64 OPERATIONS
// ============================================================================

// AddMany64 adds multiple uint64 values with overflow protection
// Returns error if any intermediate operation would overflow
// USE THIS FOR: totalGas = gas1 + gas2 + gas3 + ...
// COMPLEXITY: O(n) where n is the number of values
func AddMany64(values ...uint64) (uint64, error) {
	if len(values) == 0 {
		return 0, nil
	}

	result := values[0]
	for i := 1; i < len(values); i++ {
		newResult, err := Add64(result, values[i])
		if err != nil {
			return 0, fmt.Errorf("overflow at position %d: %w", i, err)
		}
		result = newResult
	}
	return result, nil
}

// MulAdd64 performs (a * b) + c with overflow protection
// Useful for: (gasLimit * gasPrice) + baseFee calculations
// COMPLEXITY: O(1)
// SECURITY: Two-stage overflow check
func MulAdd64(a, b, c uint64) (uint64, error) {
	// Stage 1: Multiply with overflow check
	product, err := Mul64(a, b)
	if err != nil {
		return 0, fmt.Errorf("multiplication overflow in MulAdd64: %w", err)
	}

	// Stage 2: Add with overflow check
	result, err := Add64(product, c)
	if err != nil {
		return 0, fmt.Errorf("addition overflow in MulAdd64 after multiplication: %w", err)
	}

	return result, nil
}

// MulDiv64 performs (a * b) / c with overflow protection
// Useful for: proportional calculations like reward distribution
// COMPLEXITY: O(1)
// SECURITY: Uses big.Int internally to prevent intermediate overflow
func MulDiv64(a, b, c uint64) (uint64, error) {
	if c == 0 {
		return 0, fmt.Errorf("division by zero in MulDiv64")
	}

	// Use big.Int to handle intermediate overflow
	aBig := new(big.Int).SetUint64(a)
	bBig := new(big.Int).SetUint64(b)
	cBig := new(big.Int).SetUint64(c)

	// Calculate (a * b) / c
	result := new(big.Int).Mul(aBig, bBig)
	result.Div(result, cBig)

	// Check if result fits in uint64
	if !result.IsUint64() {
		return 0, &OverflowError{
			Operation: "MulDiv64",
			A:         a,
			B:         b,
			Message:   fmt.Sprintf("result (%s) exceeds MaxUint64", result.String()),
		}
	}

	return result.Uint64(), nil
}

// Pow64 calculates a^b with overflow protection
// USE THIS FOR: exponential calculations (use with extreme caution)
// COMPLEXITY: O(log b)
// WARNING: Can overflow very quickly
func Pow64(base, exp uint64) (uint64, error) {
	if exp == 0 {
		return 1, nil
	}
	if base == 0 {
		return 0, nil
	}
	if base == 1 {
		return 1, nil
	}

	// Quick overflow check for common cases
	if base > 1 && exp > 63 {
		return 0, &OverflowError{
			Operation: "Pow64",
			A:         base,
			B:         exp,
			Message:   "exponent too large, would certainly overflow",
		}
	}

	result := uint64(1)
	for i := uint64(0); i < exp; i++ {
		newResult, err := Mul64(result, base)
		if err != nil {
			return 0, fmt.Errorf("overflow in Pow64 at exponent %d: %w", i+1, err)
		}
		result = newResult
	}

	return result, nil
}

// ============================================================================
// CHECKED OPERATIONS (RETURN ZERO ON OVERFLOW INSTEAD OF ERROR)
// ============================================================================
// These are useful when you want to saturate at max value rather than error

// Add64Saturating adds a and b, returning MaxUint64 on overflow
func Add64Saturating(a, b uint64) uint64 {
	result, err := Add64(a, b)
	if err != nil {
		return math.MaxUint64
	}
	return result
}

// Mul64Saturating multiplies a and b, returning MaxUint64 on overflow
func Mul64Saturating(a, b uint64) uint64 {
	result, err := Mul64(a, b)
	if err != nil {
		return math.MaxUint64
	}
	return result
}

// Sub64Saturating subtracts b from a, returning 0 on underflow
func Sub64Saturating(a, b uint64) uint64 {
	if b > a {
		return 0
	}
	return a - b
}

// ============================================================================
// CONVENIENCE WRAPPERS (PANIC ON OVERFLOW)
// ============================================================================
// WARNING: Only use these when overflow is provably impossible

// MustAdd64 is a convenience wrapper that panics on overflow
// WARNING: Only use when overflow is mathematically impossible
func MustAdd64(a, b uint64) uint64 {
	result, err := Add64(a, b)
	if err != nil {
		panic(fmt.Sprintf("MustAdd64 failed: %v", err))
	}
	return result
}

// MustMul64 is a convenience wrapper that panics on overflow
// WARNING: Only use when overflow is mathematically impossible
func MustMul64(a, b uint64) uint64 {
	result, err := Mul64(a, b)
	if err != nil {
		panic(fmt.Sprintf("MustMul64 failed: %v", err))
	}
	return result
}

// MustSub64 is a convenience wrapper that panics on underflow
// WARNING: Only use when underflow is mathematically impossible
func MustSub64(a, b uint64) uint64 {
	result, err := Sub64(a, b)
	if err != nil {
		panic(fmt.Sprintf("MustSub64 failed: %v", err))
	}
	return result
}

// ============================================================================
// INT64 SAFE OPERATIONS (FOR BALANCES/STAKES/SIGNED AMOUNTS)
// ============================================================================

// SafeAdd adds two int64 values with overflow protection
// Returns error if operation would overflow or underflow
// COMPLEXITY: O(1)
// SECURITY: Handles both positive and negative overflow
func SafeAdd(a, b int64) (int64, error) {
	// Check for overflow when both positive
	// If a > 0 and b > 0, then a + b > MaxInt64 if a > MaxInt64 - b
	if a > 0 && b > 0 && a > math.MaxInt64-b {
		return 0, &OverflowError{
			Operation: "SafeAdd",
			A:         a,
			B:         b,
			Message:   fmt.Sprintf("%d + %d exceeds MaxInt64 (%d)", a, b, int64(math.MaxInt64)),
		}
	}

	// Check for underflow when both negative
	// If a < 0 and b < 0, then a + b < MinInt64 if a < MinInt64 - b
	if a < 0 && b < 0 && a < math.MinInt64-b {
		return 0, &UnderflowError{
			Operation: "SafeAdd",
			A:         a,
			B:         b,
			Message:   fmt.Sprintf("%d + %d below MinInt64 (%d)", a, b, int64(math.MinInt64)),
		}
	}

	return a + b, nil
}

// SafeSub subtracts two int64 values with underflow protection
// Returns error if operation would overflow or underflow
// COMPLEXITY: O(1)
func SafeSub(a, b int64) (int64, error) {
	// Subtracting a positive number can cause underflow
	// a - b < MinInt64 when b > 0 and a < MinInt64 + b
	if b > 0 && a < math.MinInt64+b {
		return 0, &UnderflowError{
			Operation: "SafeSub",
			A:         a,
			B:         b,
			Message:   fmt.Sprintf("%d - %d below MinInt64 (%d)", a, b, int64(math.MinInt64)),
		}
	}

	// Subtracting a negative number can cause overflow
	// a - b > MaxInt64 when b < 0 and a > MaxInt64 + b
	if b < 0 && a > math.MaxInt64+b {
		return 0, &OverflowError{
			Operation: "SafeSub",
			A:         a,
			B:         b,
			Message:   fmt.Sprintf("%d - (%d) exceeds MaxInt64 (%d)", a, b, int64(math.MaxInt64)),
		}
	}

	// b == 0: no overflow possible
	return a - b, nil
}

// SafeMul multiplies two int64 values with overflow protection
// Returns error if operation would overflow or underflow
// COMPLEXITY: O(1)
// SECURITY: Handles edge cases like MinInt64 * -1
func SafeMul(a, b int64) (int64, error) {
	// Special cases where multiplication is safe
	if a == 0 || b == 0 {
		return 0, nil
	}
	if a == 1 {
		return b, nil
	}
	if b == 1 {
		return a, nil
	}

	// Edge case: MinInt64 * -1 would overflow to MaxInt64 + 1
	if a == -1 && b == math.MinInt64 {
		return 0, &OverflowError{
			Operation: "SafeMul",
			A:         a,
			B:         b,
			Message:   fmt.Sprintf("(-1) * MinInt64 would exceed MaxInt64"),
		}
	}
	if b == -1 && a == math.MinInt64 {
		return 0, &OverflowError{
			Operation: "SafeMul",
			A:         a,
			B:         b,
			Message:   fmt.Sprintf("MinInt64 * (-1) would exceed MaxInt64"),
		}
	}

	// Perform multiplication
	result := a * b

	// Check for overflow by reversing the operation
	// If overflow occurred, result / b != a
	if result/b != a {
		return 0, &OverflowError{
			Operation: "SafeMul",
			A:         a,
			B:         b,
			Message:   fmt.Sprintf("%d * %d exceeds safe int64 range", a, b),
		}
	}

	return result, nil
}

// SafeDiv divides two int64 values with protection
// Returns error if dividing by zero or overflow edge case
// COMPLEXITY: O(1)
// SECURITY: Handles MinInt64 / -1 edge case
func SafeDiv(a, b int64) (int64, error) {
	if b == 0 {
		return 0, fmt.Errorf("division by zero: %d / 0", a)
	}

	// Edge case: MinInt64 / -1 would overflow
	// Because MinInt64 = -9223372036854775808
	// And MaxInt64 = 9223372036854775807
	// So -MinInt64 = 9223372036854775808 which is > MaxInt64
	if a == math.MinInt64 && b == -1 {
		return 0, &OverflowError{
			Operation: "SafeDiv",
			A:         a,
			B:         b,
			Message:   "MinInt64 / -1 would exceed MaxInt64",
		}
	}

	return a / b, nil
}

// SafeMod calculates a modulo b with protection
// COMPLEXITY: O(1)
func SafeMod(a, b int64) (int64, error) {
	if b == 0 {
		return 0, fmt.Errorf("modulo by zero: %d %% 0", a)
	}

	// Edge case: MinInt64 % -1 can cause issues on some platforms
	if a == math.MinInt64 && b == -1 {
		return 0, nil // Mathematically correct result
	}

	return a % b, nil
}

// SafeAbs returns the absolute value with overflow protection
// COMPLEXITY: O(1)
// SECURITY: MinInt64 has no positive equivalent
func SafeAbs(a int64) (int64, error) {
	if a == math.MinInt64 {
		return 0, &OverflowError{
			Operation: "SafeAbs",
			A:         a,
			B:         nil,
			Message:   "absolute value of MinInt64 would exceed MaxInt64",
		}
	}
	if a < 0 {
		return -a, nil
	}
	return a, nil
}

// SafeNeg negates an int64 with overflow protection
// COMPLEXITY: O(1)
func SafeNeg(a int64) (int64, error) {
	if a == math.MinInt64 {
		return 0, &OverflowError{
			Operation: "SafeNeg",
			A:         a,
			B:         nil,
			Message:   "negation of MinInt64 would exceed MaxInt64",
		}
	}
	return -a, nil
}

// SafePercentage calculates percentage with overflow protection
// Specifically for slashing penalties: amount * percent / 100
// COMPLEXITY: O(1)
// USE THIS FOR: Calculating slashing amounts, fee percentages
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
		return 0, fmt.Errorf("percentage calculation overflow: %w", err)
	}

	result, err := SafeDiv(product, 100)
	if err != nil {
		return 0, fmt.Errorf("percentage calculation division error: %w", err)
	}

	return result, nil
}

// ============================================================================
// INT64 CONVENIENCE WRAPPERS (PANIC ON OVERFLOW)
// ============================================================================

// MustSafeAdd is a convenience wrapper that panics on overflow
// WARNING: Use only in contexts where overflow is provably impossible
func MustSafeAdd(a, b int64) int64 {
	result, err := SafeAdd(a, b)
	if err != nil {
		panic(fmt.Sprintf("MustSafeAdd failed: %v", err))
	}
	return result
}

// MustSafeSub is a convenience wrapper that panics on underflow
// WARNING: Use only in contexts where underflow is provably impossible
func MustSafeSub(a, b int64) int64 {
	result, err := SafeSub(a, b)
	if err != nil {
		panic(fmt.Sprintf("MustSafeSub failed: %v", err))
	}
	return result
}

// MustSafeMul is a convenience wrapper that panics on overflow
// WARNING: Use only in contexts where overflow is provably impossible
func MustSafeMul(a, b int64) int64 {
	result, err := SafeMul(a, b)
	if err != nil {
		panic(fmt.Sprintf("MustSafeMul failed: %v", err))
	}
	return result
}

// ============================================================================
// BIG.INT SAFE OPERATIONS (FOR LARGE VALUES)
// ============================================================================

// SafePercentageBig calculates a percentage of a BigInt amount
// amount: The total amount (*big.Int)
// percent: The percentage to calculate (int64, e.g., 5 for 5%)
// Returns: (amount * percent) / 100
// COMPLEXITY: O(1)
// SECURITY: No overflow possible with big.Int
func SafePercentageBig(amount *big.Int, percent int64) (*big.Int, error) {
	if amount == nil {
		return nil, fmt.Errorf("amount cannot be nil")
	}
	if percent < 0 || percent > 100 {
		return nil, fmt.Errorf("invalid percentage: %d (must be 0-100)", percent)
	}

	// Create BigInt for percentage
	percentBig := big.NewInt(percent)

	// Multiply: amount * percent
	product := new(big.Int).Mul(amount, percentBig)

	// Divide: product / 100
	divisor := big.NewInt(100)
	result := new(big.Int).Div(product, divisor)

	return result, nil
}

// AddBig adds two big.Int values (always safe, no overflow)
// COMPLEXITY: O(n) where n is the number of digits
func AddBig(a, b *big.Int) *big.Int {
	if a == nil && b == nil {
		return big.NewInt(0)
	}
	if a == nil {
		return new(big.Int).Set(b)
	}
	if b == nil {
		return new(big.Int).Set(a)
	}
	return new(big.Int).Add(a, b)
}

// SubBig subtracts two big.Int values (always safe)
// COMPLEXITY: O(n) where n is the number of digits
func SubBig(a, b *big.Int) *big.Int {
	if a == nil {
		a = big.NewInt(0)
	}
	if b == nil {
		b = big.NewInt(0)
	}
	return new(big.Int).Sub(a, b)
}

// MulBig multiplies two big.Int values (always safe)
// COMPLEXITY: O(n*m) where n,m are the number of digits
func MulBig(a, b *big.Int) *big.Int {
	if a == nil || b == nil {
		return big.NewInt(0)
	}
	return new(big.Int).Mul(a, b)
}

// DivBig divides two big.Int values with zero check
// COMPLEXITY: O(n*m) where n,m are the number of digits
func DivBig(a, b *big.Int) (*big.Int, error) {
	if b == nil || b.Sign() == 0 {
		return nil, fmt.Errorf("division by zero")
	}
	if a == nil {
		return big.NewInt(0), nil
	}
	return new(big.Int).Div(a, b), nil
}

// ============================================================================
// GAS ESTIMATION HELPERS
// ============================================================================

// EstimateTotalGas calculates total gas with overflow protection
// Useful for validating blocks or transaction pools
// COMPLEXITY: O(n) where n is the number of gas values
func EstimateTotalGas(gasValues []uint64) (uint64, error) {
	return AddMany64(gasValues...)
}

// CalculateGasCost calculates gas * price with overflow protection
// Returns gas cost or error if overflow
// COMPLEXITY: O(1)
func CalculateGasCost(gasUsed, gasPrice uint64) (uint64, error) {
	return Mul64(gasUsed, gasPrice)
}

// CalculateTotalGasCost calculates (gasUsed * gasPrice) + priorityFee
// COMPLEXITY: O(1)
func CalculateTotalGasCost(gasUsed, gasPrice, priorityFee uint64) (uint64, error) {
	return MulAdd64(gasUsed, gasPrice, priorityFee)
}

// ValidateGasLimit checks if gas limit is within acceptable range
// COMPLEXITY: O(1)
func ValidateGasLimit(gasLimit uint64, maxAllowed uint64) error {
	if gasLimit > maxAllowed {
		return fmt.Errorf("gas limit %d exceeds maximum allowed %d", gasLimit, maxAllowed)
	}
	if gasLimit == 0 {
		return fmt.Errorf("gas limit cannot be zero")
	}
	return nil
}

// CalculateRemainingGas calculates remaining gas after execution
// COMPLEXITY: O(1)
func CalculateRemainingGas(gasLimit, gasUsed uint64) (uint64, error) {
	return Sub64(gasLimit, gasUsed)
}

// ============================================================================
// BALANCE OPERATION HELPERS
// ============================================================================

// SafeBalanceAdd adds amount to balance with overflow protection
// USE THIS FOR: Crediting accounts, minting tokens
func SafeBalanceAdd(balance, amount int64) (int64, error) {
	if amount < 0 {
		return 0, fmt.Errorf("cannot add negative amount: %d", amount)
	}
	return SafeAdd(balance, amount)
}

// SafeBalanceSub subtracts amount from balance with underflow protection
// USE THIS FOR: Debiting accounts, burning tokens
func SafeBalanceSub(balance, amount int64) (int64, error) {
	if amount < 0 {
		return 0, fmt.Errorf("cannot subtract negative amount: %d", amount)
	}
	if amount > balance {
		return 0, fmt.Errorf("insufficient balance: %d < %d", balance, amount)
	}
	return SafeSub(balance, amount)
}

// ============================================================================
// CONVERSION HELPERS WITH OVERFLOW PROTECTION
// ============================================================================

// Uint64ToInt64 converts uint64 to int64 with overflow check
func Uint64ToInt64(val uint64) (int64, error) {
	if val > math.MaxInt64 {
		return 0, &OverflowError{
			Operation: "Uint64ToInt64",
			A:         val,
			B:         nil,
			Message:   fmt.Sprintf("uint64 value %d exceeds MaxInt64", val),
		}
	}
	return int64(val), nil
}

// Int64ToUint64 converts int64 to uint64 with underflow check
func Int64ToUint64(val int64) (uint64, error) {
	if val < 0 {
		return 0, &UnderflowError{
			Operation: "Int64ToUint64",
			A:         val,
			B:         nil,
			Message:   fmt.Sprintf("int64 value %d is negative", val),
		}
	}
	return uint64(val), nil
}

// ============================================================================
// VALIDATION HELPERS
// ============================================================================

// IsValidBalance checks if a balance is in valid range
func IsValidBalance(balance int64) bool {
	return balance >= 0 && balance <= math.MaxInt64
}

// IsValidGas checks if a gas value is in valid range
func IsValidGas(gas uint64) bool {
	return gas > 0 && gas <= math.MaxUint64
}

// IsValidPercentage checks if a percentage is in valid range
func IsValidPercentage(percent int64) bool {
	return percent >= 0 && percent <= 100
}
