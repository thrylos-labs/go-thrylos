package math

import (
	"math/big"
)

// ParseBigInt parses a string to *big.Int. Returns 0 if empty/invalid.
func ParseBigInt(s string) *big.Int {
	if s == "" {
		return big.NewInt(0)
	}
	val, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return big.NewInt(0)
	}
	return val
}

// BigIntToString converts *big.Int to string. Handles nil safely.
func BigIntToString(b *big.Int) string {
	if b == nil {
		return "0"
	}
	return b.String()
}

// Add adds two big ints
func Add(a, b *big.Int) *big.Int {
	return new(big.Int).Add(a, b)
}

// Sub subtracts b from a
func Sub(a, b *big.Int) *big.Int {
	return new(big.Int).Sub(a, b)
}

// Mul multiplies two big ints
func Mul(a, b *big.Int) *big.Int {
	return new(big.Int).Mul(a, b)
}

// IsZero checks if big int is 0
func IsZero(b *big.Int) bool {
	return b.Sign() == 0
}

// Cmp compares two big ints
func Cmp(a, b *big.Int) int {
	return a.Cmp(b)
}
