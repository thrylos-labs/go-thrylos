package math

import (
	"fmt"
	"math/big"
)

const MaxUint256Bytes = 32

// ParseUint256Decimal parses a base-10 unsigned integer string.
func ParseUint256Decimal(s string) (*big.Int, error) {
	if s == "" {
		return big.NewInt(0), nil
	}

	value, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, fmt.Errorf("invalid uint256 decimal value: %q", s)
	}
	if value.Sign() < 0 {
		return nil, fmt.Errorf("uint256 value cannot be negative")
	}

	return value, nil
}

// ParseUint256Bytes parses a canonical unsigned big-endian uint256 byte slice.
func ParseUint256Bytes(raw []byte) (*big.Int, error) {
	if err := ValidateCanonicalUint256Bytes(raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return big.NewInt(0), nil
	}

	return new(big.Int).SetBytes(raw), nil
}

// ParseUint256Compat prefers canonical bytes and falls back to the legacy decimal string.
func ParseUint256Compat(raw []byte, decimal string) (*big.Int, error) {
	if len(raw) > 0 {
		return ParseUint256Bytes(raw)
	}

	return ParseUint256Decimal(decimal)
}

// ValidateCanonicalUint256Bytes enforces a 32-byte max length and canonical encoding.
func ValidateCanonicalUint256Bytes(raw []byte) error {
	if len(raw) > MaxUint256Bytes {
		return fmt.Errorf("uint256 byte length %d exceeds max %d", len(raw), MaxUint256Bytes)
	}
	if len(raw) > 1 && raw[0] == 0 {
		return fmt.Errorf("uint256 byte encoding is not canonical")
	}

	return nil
}

// BigIntToUint256Bytes converts a non-negative integer to canonical uint256 bytes.
func BigIntToUint256Bytes(value *big.Int) ([]byte, error) {
	if value == nil || value.Sign() == 0 {
		return nil, nil
	}
	if value.Sign() < 0 {
		return nil, fmt.Errorf("uint256 value cannot be negative")
	}

	raw := value.Bytes()
	if len(raw) > MaxUint256Bytes {
		return nil, fmt.Errorf("uint256 byte length %d exceeds max %d", len(raw), MaxUint256Bytes)
	}

	return raw, nil
}

func CanonicalizeUint256Bytes(raw []byte) ([]byte, error) {
	value, err := ParseUint256Bytes(raw)
	if err != nil {
		return nil, err
	}

	return BigIntToUint256Bytes(value)
}

func CanonicalizeUint256ByteMap(raw map[string][]byte) (map[string][]byte, error) {
	if raw == nil {
		return make(map[string][]byte), nil
	}

	out := make(map[string][]byte, len(raw))
	for key, value := range raw {
		canonical, err := CanonicalizeUint256Bytes(value)
		if err != nil {
			return nil, fmt.Errorf("invalid uint256 value for key %q: %w", key, err)
		}
		out[key] = canonical
	}

	return out, nil
}

// NormalizeUint256Compat is the read path: prefer bytes, heal decimal fallback.
func NormalizeUint256Compat(raw *[]byte, decimal *string) error {
	if raw == nil || decimal == nil {
		return fmt.Errorf("uint256 fields cannot be nil")
	}

	if len(*raw) > 0 {
		value, err := ParseUint256Bytes(*raw)
		if err != nil {
			return err
		}
		*decimal = value.String()
		return nil
	}

	value, err := ParseUint256Decimal(*decimal)
	if err != nil {
		return err
	}
	canonical, err := BigIntToUint256Bytes(value)
	if err != nil {
		return err
	}
	*raw = canonical
	return nil
}

// SyncUint256ForWrite is the write path: prefer the legacy decimal field and backfill bytes.
func SyncUint256ForWrite(raw *[]byte, decimal *string) error {
	if raw == nil || decimal == nil {
		return fmt.Errorf("uint256 fields cannot be nil")
	}

	if *decimal != "" {
		value, err := ParseUint256Decimal(*decimal)
		if err != nil {
			return err
		}
		canonical, err := BigIntToUint256Bytes(value)
		if err != nil {
			return err
		}
		*raw = canonical
		return nil
	}

	if len(*raw) > 0 {
		value, err := ParseUint256Bytes(*raw)
		if err != nil {
			return err
		}
		*decimal = value.String()
	}

	return nil
}

// ValidateUint256Compat is the strict validation path for externally supplied data.
func ValidateUint256Compat(raw []byte, decimal string) (*big.Int, error) {
	if len(raw) > 0 && decimal != "" {
		rawValue, err := ParseUint256Bytes(raw)
		if err != nil {
			return nil, err
		}
		decimalValue, err := ParseUint256Decimal(decimal)
		if err != nil {
			return nil, err
		}
		if rawValue.Cmp(decimalValue) != 0 {
			return nil, fmt.Errorf("uint256 byte and decimal representations do not match")
		}
		return rawValue, nil
	}

	return ParseUint256Compat(raw, decimal)
}

// NormalizeUint256MapCompat is the read path: prefer bytes, heal decimal fallback.
func NormalizeUint256MapCompat(raw map[string][]byte, decimal map[string]string) (map[string][]byte, map[string]string, error) {
	if raw == nil {
		raw = make(map[string][]byte)
	}
	if decimal == nil {
		decimal = make(map[string]string)
	}

	normalizedRaw := make(map[string][]byte, len(raw)+len(decimal))
	normalizedDecimal := make(map[string]string, len(raw)+len(decimal))

	keys := make(map[string]struct{}, len(raw)+len(decimal))
	for key := range raw {
		keys[key] = struct{}{}
	}
	for key := range decimal {
		keys[key] = struct{}{}
	}

	for key := range keys {
		rawValue := raw[key]
		decimalValue := decimal[key]

		if len(rawValue) > 0 {
			parsedRaw, err := ParseUint256Bytes(rawValue)
			if err != nil {
				return nil, nil, fmt.Errorf("invalid uint256 value for key %q: %w", key, err)
			}
			normalizedRaw[key] = rawValue
			normalizedDecimal[key] = parsedRaw.String()
			continue
		}

		parsedDecimal, err := ParseUint256Decimal(decimalValue)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid uint256 value for key %q: %w", key, err)
		}
		canonical, err := BigIntToUint256Bytes(parsedDecimal)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid uint256 value for key %q: %w", key, err)
		}
		normalizedRaw[key] = canonical
		normalizedDecimal[key] = decimalValue
		if decimalValue == "" {
			normalizedDecimal[key] = parsedDecimal.String()
		}
	}

	return normalizedRaw, normalizedDecimal, nil
}

// SyncUint256MapForWrite is the write path: prefer legacy decimal values and backfill bytes.
func SyncUint256MapForWrite(raw map[string][]byte, decimal map[string]string) (map[string][]byte, map[string]string, error) {
	if raw == nil {
		raw = make(map[string][]byte)
	}
	if decimal == nil {
		decimal = make(map[string]string)
	}

	normalizedRaw := make(map[string][]byte, len(raw)+len(decimal))
	normalizedDecimal := make(map[string]string, len(raw)+len(decimal))

	keys := make(map[string]struct{}, len(raw)+len(decimal))
	for key := range raw {
		keys[key] = struct{}{}
	}
	for key := range decimal {
		keys[key] = struct{}{}
	}

	for key := range keys {
		rawValue := raw[key]
		decimalValue := decimal[key]

		if decimalValue != "" {
			parsedDecimal, err := ParseUint256Decimal(decimalValue)
			if err != nil {
				return nil, nil, fmt.Errorf("invalid uint256 value for key %q: %w", key, err)
			}
			canonical, err := BigIntToUint256Bytes(parsedDecimal)
			if err != nil {
				return nil, nil, fmt.Errorf("invalid uint256 value for key %q: %w", key, err)
			}
			normalizedRaw[key] = canonical
			normalizedDecimal[key] = decimalValue
			continue
		}

		parsedRaw, err := ParseUint256Bytes(rawValue)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid uint256 value for key %q: %w", key, err)
		}
		normalizedRaw[key] = rawValue
		normalizedDecimal[key] = parsedRaw.String()
	}

	return normalizedRaw, normalizedDecimal, nil
}
