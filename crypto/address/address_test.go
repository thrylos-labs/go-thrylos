package address

import (
	"math/rand"
	"strings"
	"testing"

	mldsa "github.com/cloudflare/circl/sign/mldsa/mldsa44"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	seed := rand.New(rand.NewSource(1234))
	pk, _, err := mldsa.GenerateKey(seed)
	if err != nil {
		t.Fatalf("Failed to generate keys: %v", err)
	}

	address, err := New(pk)
	require.NoError(t, err)
	require.NotNil(t, address)

	// Test that the address has the correct tl1 format
	addrStr := address.String()
	require.True(t, strings.HasPrefix(addrStr, "tl1"), "Address should start with tl1")
	require.Equal(t, 22, len(addrStr), "Address should be 22 characters long")

	// Test that it's valid bech32
	require.NoError(t, Validate(addrStr), "First address should be valid")

	// Test deterministic generation - same seed should produce same address
	seed2 := rand.New(rand.NewSource(1234))
	pk2, _, err := mldsa.GenerateKey(seed2)
	require.NoError(t, err)

	address2, err := New(pk2)
	require.NoError(t, err)

	addrStr2 := address2.String()

	// Validate the second address separately
	require.NoError(t, Validate(addrStr2), "Second address should be valid")

	// Then compare them
	require.Equal(t, addrStr, addrStr2, "Same seed should produce same address")
}

func TestValidate(t *testing.T) {
	// Generate a valid address for testing
	seed := rand.New(rand.NewSource(1234))
	pk, _, _ := mldsa.GenerateKey(seed)
	validAddr, _ := New(pk)
	validAddrStr := validAddr.String()

	tests := []struct {
		name    string
		address string
		valid   bool
	}{
		{
			name:    "valid address",
			address: validAddrStr,
			valid:   true,
		},
		{
			name:    "valid address lowercase",
			address: strings.ToLower(validAddrStr),
			valid:   true,
		},
		{
			name:    "valid address uppercase",
			address: strings.ToUpper(validAddrStr),
			valid:   true,
		},
		{
			name:    "invalid - wrong prefix",
			address: "eth1k2x4p9m6v8q3w7f5t2d4",
			valid:   false,
		},
		{
			name:    "invalid - no prefix",
			address: "k2x4p9m6v8q3w7f5t2d4g9h7",
			valid:   false,
		},
		{
			name:    "invalid - wrong checksum",
			address: "tl1k2x4p9m6v8q3w7f5t2d5", // Changed last character
			valid:   false,
		},
		{
			name:    "invalid - too short",
			address: "tl1k2x4p9m6v8",
			valid:   false,
		},
		{
			name:    "invalid - invalid bech32 character",
			address: "tl1k2x4p9m6v8q3w7f5t2b4", // 'b' is not valid in bech32
			valid:   false,
		},
		{
			name:    "invalid - empty",
			address: "",
			valid:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.address)
			if tt.valid {
				require.NoError(t, err, "Expected address to be valid")
				require.True(t, IsValid(tt.address), "IsValid should return true")
			} else {
				require.Error(t, err, "Expected address to be invalid")
				require.False(t, IsValid(tt.address), "IsValid should return false")
			}
		})
	}
}

func TestFromString(t *testing.T) {
	// Generate a valid address for testing
	seed := rand.New(rand.NewSource(1234))
	pk, _, _ := mldsa.GenerateKey(seed)
	validAddr, _ := New(pk)
	validAddress := validAddr.String()

	address, err := FromString(validAddress)
	require.NoError(t, err)
	require.NotNil(t, address)
	require.Equal(t, strings.ToLower(validAddress), strings.ToLower(address.String()))

	// Test case insensitive parsing
	upperAddress := strings.ToUpper(validAddress)
	address2, err := FromString(upperAddress)
	require.NoError(t, err)
	require.Equal(t, strings.ToLower(validAddress), strings.ToLower(address2.String()))

	// Test invalid address
	_, err = FromString("invalid")
	require.Error(t, err)
}

func TestFromBytes(t *testing.T) {
	// Test valid 8-byte input
	bytes8 := []byte{
		0x4a, 0x7b, 0x3c, 0x8d, 0x9e, 0x2f, 0x1a, 0x6b,
	}

	address, err := FromBytes(bytes8)
	require.NoError(t, err)
	require.NotNil(t, address)

	// Verify it creates a valid bech32 address
	addrStr := address.String()
	require.True(t, strings.HasPrefix(addrStr, "tl1"))
	require.NoError(t, Validate(addrStr))

	// Test invalid byte length
	_, err = FromBytes([]byte{0x01, 0x02})
	require.Error(t, err)

	_, err = FromBytes(make([]byte, 12)) // Wrong length (12 instead of 8)
	require.Error(t, err)

	_, err = FromBytes(make([]byte, 20)) // Old Ethereum length
	require.Error(t, err)
}

func TestAddressMethods(t *testing.T) {
	// Generate a test address
	seed := rand.New(rand.NewSource(1234))
	pk, _, _ := mldsa.GenerateKey(seed)
	address, _ := New(pk)
	addr := address.String()

	// Test String()
	require.True(t, strings.HasPrefix(addr, "tl1"))
	require.Equal(t, 22, len(addr))

	// Test Hex()
	hex := address.Hex()
	require.Equal(t, 16, len(hex))     // 8 bytes = 16 hex characters
	require.NotContains(t, hex, "tl1") // Should be raw hex

	// Test Bytes()
	bytes := address.Bytes()
	require.Equal(t, 8, len(bytes)) // 8 bytes for new format

	// Test IsZero()
	require.False(t, address.IsZero())

	zeroAddr := NullAddress()
	require.True(t, zeroAddr.IsZero())

	// Test Equal()
	address2, _ := FromString(addr)
	require.True(t, address.Equal(address2))

	// Create different address
	seed2 := rand.New(rand.NewSource(5678))
	pk2, _, _ := mldsa.GenerateKey(seed2)
	differentAddr, _ := New(pk2)
	require.False(t, address.Equal(differentAddr))

	// Test ToLower() and ToUpper()
	upperAddr := strings.ToUpper(addr)
	lowerAddr := strings.ToLower(addr)
	parsedUpper, _ := FromString(upperAddr)
	require.Equal(t, lowerAddr, parsedUpper.ToLower())
}

func TestAddressCopy(t *testing.T) {
	seed := rand.New(rand.NewSource(1234))
	pk, _, _ := mldsa.GenerateKey(seed)
	original, _ := New(pk)
	copied := original.Copy()

	require.True(t, original.Equal(copied))
	require.NotSame(t, original, copied) // Different memory addresses

	// Modify original, copy should remain unchanged
	original.Set(make([]byte, 8)) // 8 bytes for new format
	require.False(t, original.Equal(copied))
}

func TestAddressJSON(t *testing.T) {
	seed := rand.New(rand.NewSource(1234))
	pk, _, _ := mldsa.GenerateKey(seed)
	address, _ := New(pk)
	addr := address.String()

	// Test MarshalJSON
	jsonData, err := address.MarshalJSON()
	require.NoError(t, err)
	require.Equal(t, `"`+addr+`"`, string(jsonData))

	// Test UnmarshalJSON
	var newAddress Address
	err = newAddress.UnmarshalJSON(jsonData)
	require.NoError(t, err)
	require.Equal(t, strings.ToLower(addr), strings.ToLower(newAddress.String()))
}

func TestAddressCBOR(t *testing.T) {
	seed := rand.New(rand.NewSource(1234))
	pk, _, _ := mldsa.GenerateKey(seed)
	address, _ := New(pk)

	// Test Marshal
	data, err := address.Marshal()
	require.NoError(t, err)
	require.NotEmpty(t, data)

	// Test Unmarshal
	var newAddress Address
	err = newAddress.Unmarshal(data)
	require.NoError(t, err)
	require.Equal(t, address.String(), newAddress.String())
}

func TestUtilityFunctions(t *testing.T) {
	seed := rand.New(rand.NewSource(1234))
	pk, _, err := mldsa.GenerateKey(seed)
	require.NoError(t, err)

	// Test GenerateAddress
	addrStr, err := GenerateAddress(pk)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(addrStr, "tl1"))
	require.Equal(t, 22, len(addrStr))

	// Test ParseAddress
	parsed, err := ParseAddress(addrStr)
	require.NoError(t, err)
	require.Equal(t, strings.ToLower(addrStr), strings.ToLower(parsed.String()))

	// Test FormatAddress
	formatted, err := FormatAddress(parsed.Bytes())
	require.NoError(t, err)
	require.Equal(t, strings.ToLower(addrStr), strings.ToLower(formatted))
}

func TestNullAddress(t *testing.T) {
	nullAddr := NullAddress()
	require.NotNil(t, nullAddr)
	require.True(t, nullAddr.IsZero())

	nullStr := nullAddr.String()
	require.True(t, strings.HasPrefix(nullStr, "tl1"))
	require.NoError(t, Validate(nullStr)) // Should be valid bech32
}

func TestConvertToAddress(t *testing.T) {
	seed := rand.New(rand.NewSource(1234))
	pk, _, err := mldsa.GenerateKey(seed)
	require.NoError(t, err)

	// Test ConvertToAddress
	addrStr, err := ConvertToAddress(pk)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(addrStr, "tl1"))
	require.NoError(t, Validate(addrStr))
	require.Equal(t, 22, len(addrStr))
}

func TestAddressMetrics(t *testing.T) {
	metrics := AddressMetrics()

	require.Equal(t, "Bech32", metrics["format"])
	require.Equal(t, "tl", metrics["prefix"])
	require.Equal(t, 8, metrics["byte_length"])
	require.Equal(t, 22, metrics["estimated_str_length"])
	require.Equal(t, "2^64", metrics["collision_resistance"])
	require.Equal(t, false, metrics["case_sensitive"])
}

func TestAddressLengthConsistency(t *testing.T) {
	// Generate multiple addresses and verify consistent length
	for i := 0; i < 10; i++ {
		seed := rand.New(rand.NewSource(int64(i)))
		pk, _, err := mldsa.GenerateKey(seed)
		require.NoError(t, err)

		address, err := New(pk)
		require.NoError(t, err)

		addrStr := address.String()
		require.Equal(t, 22, len(addrStr), "Address length should be consistent")
		require.True(t, strings.HasPrefix(addrStr, "tl1"))
		require.NoError(t, Validate(addrStr))
	}
}

func TestBech32CaseInsensitivity(t *testing.T) {
	seed := rand.New(rand.NewSource(1234))
	pk, _, _ := mldsa.GenerateKey(seed)
	address, _ := New(pk)
	original := address.String()

	// Test that different cases are treated as equivalent
	lower := strings.ToLower(original)
	upper := strings.ToUpper(original)

	lowerAddr, err := FromString(lower)
	require.NoError(t, err)

	upperAddr, err := FromString(upper)
	require.NoError(t, err)

	// Should be equal when normalized
	require.Equal(t, lowerAddr.String(), upperAddr.String())
}
