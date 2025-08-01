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

	// Test that the address has the correct 0x format
	addrStr := address.String()
	require.True(t, strings.HasPrefix(addrStr, "0x"), "Address should start with 0x")
	require.Equal(t, 42, len(addrStr), "Address should be 42 characters long")

	// Test that it's valid hex
	require.NoError(t, Validate(addrStr), "Address should be valid")

	// Test deterministic generation - same seed should produce same address
	seed2 := rand.New(rand.NewSource(1234))
	pk2, _, err := mldsa.GenerateKey(seed2)
	require.NoError(t, err)

	address2, err := New(pk2)
	require.NoError(t, err)
	require.Equal(t, address.String(), address2.String(), "Same seed should produce same address")
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		address string
		valid   bool
	}{
		{
			name:    "valid address",
			address: "0x4a7b3c8d9e2f1a6b5c4d3e2f1a9b8c7d6e5f4321",
			valid:   true,
		},
		{
			name:    "valid address uppercase",
			address: "0x4A7B3C8D9E2F1A6B5C4D3E2F1A9B8C7D6E5F4321",
			valid:   true,
		},
		{
			name:    "valid address mixed case",
			address: "0x4a7B3c8D9e2F1a6B5c4D3e2F1a9B8c7D6e5F4321",
			valid:   true,
		},
		{
			name:    "invalid - no 0x prefix",
			address: "4a7b3c8d9e2f1a6b5c4d3e2f1a9b8c7d6e5f4321",
			valid:   false,
		},
		{
			name:    "invalid - wrong prefix",
			address: "0y4a7b3c8d9e2f1a6b5c4d3e2f1a9b8c7d6e5f4321",
			valid:   false,
		},
		{
			name:    "invalid - too short",
			address: "0x4a7b3c8d9e2f1a6b5c4d3e2f1a9b8c7d6e5f43",
			valid:   false,
		},
		{
			name:    "invalid - too long",
			address: "0x4a7b3c8d9e2f1a6b5c4d3e2f1a9b8c7d6e5f43210",
			valid:   false,
		},
		{
			name:    "invalid - non-hex character",
			address: "0x4a7b3c8d9e2f1a6b5c4d3e2f1a9b8c7d6e5f432g",
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
	validAddress := "0x4a7b3c8d9e2f1a6b5c4d3e2f1a9b8c7d6e5f4321"

	address, err := FromString(validAddress)
	require.NoError(t, err)
	require.NotNil(t, address)
	require.Equal(t, validAddress, address.String())

	// Test case insensitive parsing
	upperAddress := "0x4A7B3C8D9E2F1A6B5C4D3E2F1A9B8C7D6E5F4321"
	address2, err := FromString(upperAddress)
	require.NoError(t, err)
	require.Equal(t, strings.ToLower(upperAddress), address2.String())

	// Test invalid address
	_, err = FromString("invalid")
	require.Error(t, err)
}

func TestFromBytes(t *testing.T) {
	// Test valid 20-byte input
	bytes20 := []byte{
		0x4a, 0x7b, 0x3c, 0x8d, 0x9e, 0x2f, 0x1a, 0x6b,
		0x5c, 0x4d, 0x3e, 0x2f, 0x1a, 0x9b, 0x8c, 0x7d,
		0x6e, 0x5f, 0x43, 0x21,
	}

	address, err := FromBytes(bytes20)
	require.NoError(t, err)
	require.NotNil(t, address)
	require.Equal(t, "0x4a7b3c8d9e2f1a6b5c4d3e2f1a9b8c7d6e5f4321", address.String())

	// Test invalid byte length
	_, err = FromBytes([]byte{0x01, 0x02})
	require.Error(t, err)

	_, err = FromBytes(make([]byte, 21))
	require.Error(t, err)
}

func TestAddressMethods(t *testing.T) {
	addr := "0x4a7b3c8d9e2f1a6b5c4d3e2f1a9b8c7d6e5f4321"
	address, err := FromString(addr)
	require.NoError(t, err)

	// Test String()
	require.Equal(t, addr, address.String())

	// Test Hex()
	require.Equal(t, "4a7b3c8d9e2f1a6b5c4d3e2f1a9b8c7d6e5f4321", address.Hex())

	// Test Bytes()
	bytes := address.Bytes()
	require.Equal(t, 20, len(bytes))

	// Test IsZero()
	require.False(t, address.IsZero())

	zeroAddr := NullAddress()
	require.True(t, zeroAddr.IsZero())

	// Test Equal()
	address2, _ := FromString(addr)
	require.True(t, address.Equal(address2))

	differentAddr, _ := FromString("0x1111111111111111111111111111111111111111")
	require.False(t, address.Equal(differentAddr))

	// Test ToLower()
	upperAddr, _ := FromString("0x4A7B3C8D9E2F1A6B5C4D3E2F1A9B8C7D6E5F4321")
	require.Equal(t, addr, upperAddr.ToLower())
}

func TestAddressCopy(t *testing.T) {
	original, _ := FromString("0x4a7b3c8d9e2f1a6b5c4d3e2f1a9b8c7d6e5f4321")
	copied := original.Copy()

	require.True(t, original.Equal(copied))
	require.NotSame(t, original, copied) // Different memory addresses

	// Modify original, copy should remain unchanged
	original.Set(make([]byte, 20))
	require.False(t, original.Equal(copied))
}

func TestAddressJSON(t *testing.T) {
	addr := "0x4a7b3c8d9e2f1a6b5c4d3e2f1a9b8c7d6e5f4321"
	address, _ := FromString(addr)

	// Test MarshalJSON
	jsonData, err := address.MarshalJSON()
	require.NoError(t, err)
	require.Equal(t, `"`+addr+`"`, string(jsonData))

	// Test UnmarshalJSON
	var newAddress Address
	err = newAddress.UnmarshalJSON(jsonData)
	require.NoError(t, err)
	require.Equal(t, addr, newAddress.String())
}

func TestAddressCBOR(t *testing.T) {
	addr := "0x4a7b3c8d9e2f1a6b5c4d3e2f1a9b8c7d6e5f4321"
	address, _ := FromString(addr)

	// Test Marshal
	data, err := address.Marshal()
	require.NoError(t, err)
	require.NotEmpty(t, data)

	// Test Unmarshal
	var newAddress Address
	err = newAddress.Unmarshal(data)
	require.NoError(t, err)
	require.Equal(t, addr, newAddress.String())
}

func TestUtilityFunctions(t *testing.T) {
	seed := rand.New(rand.NewSource(1234))
	pk, _, err := mldsa.GenerateKey(seed)
	require.NoError(t, err)

	// Test GenerateAddress
	addrStr, err := GenerateAddress(pk)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(addrStr, "0x"))
	require.Equal(t, 42, len(addrStr))

	// Test ParseAddress
	parsed, err := ParseAddress(addrStr)
	require.NoError(t, err)
	require.Equal(t, addrStr, parsed.String())

	// Test FormatAddress
	formatted, err := FormatAddress(parsed.Bytes())
	require.NoError(t, err)
	require.Equal(t, addrStr, formatted)
}

func TestNullAddress(t *testing.T) {
	nullAddr := NullAddress()
	require.NotNil(t, nullAddr)
	require.True(t, nullAddr.IsZero())
	require.Equal(t, "0x0000000000000000000000000000000000000000", nullAddr.String())
}

func TestConvertToAddress(t *testing.T) {
	seed := rand.New(rand.NewSource(1234))
	pk, _, err := mldsa.GenerateKey(seed)
	require.NoError(t, err)

	// Test ConvertToAddress (renamed from ConvertToBech32Address)
	addrStr, err := ConvertToAddress(pk)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(addrStr, "0x"))
	require.NoError(t, Validate(addrStr))
}
