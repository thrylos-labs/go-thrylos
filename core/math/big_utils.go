package math

import (
	"math/big"
)

type BigIntInput interface {
	~string | ~[]byte
}

// ParseBigInt parses either a decimal string or canonical uint256 bytes.
// Returns 0 if empty/invalid.
func ParseBigInt[T BigIntInput](v T) *big.Int {
	switch value := any(v).(type) {
	case string:
		if value == "" {
			return big.NewInt(0)
		}
		val, ok := new(big.Int).SetString(value, 10)
		if !ok {
			return big.NewInt(0)
		}
		return val
	case []byte:
		if len(value) == 0 {
			return big.NewInt(0)
		}
		if err := ValidateCanonicalUint256Bytes(value); err != nil {
			return big.NewInt(0)
		}
		return new(big.Int).SetBytes(value)
	default:
		return big.NewInt(0)
	}
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
