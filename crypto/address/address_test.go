package address

import (
	"crypto/ecdsa"
	"strings"
	"testing"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

// Helper function to generate a secp256k1 key pair and address
func generateTestAddress(t *testing.T) (*Address, *ecdsa.PrivateKey) {
	privateKey, err := ethcrypto.GenerateKey()
	require.NoError(t, err, "Failed to generate secp256k1 key")

	publicKey := privateKey.Public().(*ecdsa.PublicKey)
	ethAddr := ethcrypto.PubkeyToAddress(*publicKey)

	address := FromEthereumAddress(ethAddr)
	return address, privateKey
}

func TestFromEthereumAddress(t *testing.T) {
	address, _ := generateTestAddress(t)
	require.NotNil(t, address)

	// Test that the address has the correct 0x format
	addrStr := address.String()
	require.True(t, strings.HasPrefix(addrStr, "0x"), "Address should start with 0x")
	require.Equal(t, 42, len(addrStr), "Address should be 42 characters long (0x + 40 hex)")

	// Test that it's valid hex
	require.NoError(t, Validate(addrStr), "Address should be valid")

	// Test deterministic generation - same key should produce same address
	privateKey, err := ethcrypto.GenerateKey()
	require.NoError(t, err)

	publicKey := privateKey.Public().(*ecdsa.PublicKey)
	ethAddr1 := ethcrypto.PubkeyToAddress(*publicKey)
	ethAddr2 := ethcrypto.PubkeyToAddress(*publicKey)

	address1 := FromEthereumAddress(ethAddr1)
	address2 := FromEthereumAddress(ethAddr2)

	require.Equal(t, address1.String(), address2.String(), "Same key should produce same address")
}

func TestValidate(t *testing.T) {
	// Generate a valid address for testing
	validAddr, _ := generateTestAddress(t)
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
			name:    "valid address without 0x",
			address: strings.TrimPrefix(validAddrStr, "0x"),
			valid:   false, // Must have 0x prefix
		},
		{
			name:    "invalid - wrong prefix",
			address: "eth1234567890123456789012345678901234567890",
			valid:   false,
		},
		{
			name:    "invalid - no prefix",
			address: "1234567890123456789012345678901234567890",
			valid:   false,
		},
		{
			name:    "invalid - too short",
			address: "0x12345678",
			valid:   false,
		},
		{
			name:    "invalid - too long",
			address: "0x123456789012345678901234567890123456789012345",
			valid:   false,
		},
		{
			name:    "invalid - non-hex character",
			address: "0x123456789012345678901234567890123456zzzz",
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
	validAddr, _ := generateTestAddress(t)
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

	// Test old tl1 format (should fail)
	_, err = FromString("tl1k2x4p9m6v8q3w7f5t2d4")
	require.Error(t, err)
}

func TestFromBytes(t *testing.T) {
	// Test valid 20-byte input (Ethereum address length)
	bytes20 := []byte{
		0x4a, 0x7b, 0x3c, 0x8d, 0x9e, 0x2f, 0x1a, 0x6b,
		0x5c, 0x7d, 0x8e, 0x9f, 0x0a, 0x1b, 0x2c, 0x3d,
		0x4e, 0x5f, 0x6a, 0x7b,
	}

	address, err := FromBytes(bytes20)
	require.NoError(t, err)
	require.NotNil(t, address)

	// Verify it creates a valid hex address
	addrStr := address.String()
	require.True(t, strings.HasPrefix(addrStr, "0x"))
	require.NoError(t, Validate(addrStr))
	require.Equal(t, 42, len(addrStr)) // 0x + 40 hex chars

	// Test invalid byte length
	_, err = FromBytes([]byte{0x01, 0x02})
	require.Error(t, err)

	_, err = FromBytes(make([]byte, 8)) // Wrong length
	require.Error(t, err)

	_, err = FromBytes(make([]byte, 12)) // Wrong length
	require.Error(t, err)
}

func TestAddressMethods(t *testing.T) {
	// Generate a test address
	address, _ := generateTestAddress(t)
	addr := address.String()

	// Test String()
	require.True(t, strings.HasPrefix(addr, "0x"))
	require.Equal(t, 42, len(addr)) // 0x + 40 hex chars

	// Test Hex()
	hex := address.Hex()
	require.Equal(t, 40, len(hex))    // 20 bytes = 40 hex characters
	require.NotContains(t, hex, "0x") // Should be raw hex without prefix

	// Test Bytes()
	bytes := address.Bytes()
	require.Equal(t, 20, len(bytes)) // 20 bytes for Ethereum format

	// Test IsZero()
	require.False(t, address.IsZero())

	zeroAddr := NullAddress()
	require.True(t, zeroAddr.IsZero())

	// Test Equal()
	address2, _ := FromString(addr)
	require.True(t, address.Equal(address2))

	// Create different address
	differentAddr, _ := generateTestAddress(t)
	require.False(t, address.Equal(differentAddr))

	// Test ToLower() and ToUpper()
	upperAddr := strings.ToUpper(addr)
	lowerAddr := strings.ToLower(addr)
	parsedUpper, _ := FromString(upperAddr)
	require.Equal(t, lowerAddr, parsedUpper.ToLower())
}

func TestAddressCopy(t *testing.T) {
	original, _ := generateTestAddress(t)
	copied := original.Copy()

	require.True(t, original.Equal(copied))
	require.NotSame(t, original, copied) // Different memory addresses

	// Modify original, copy should remain unchanged
	original.Set(make([]byte, 20)) // 20 bytes for Ethereum format
	require.False(t, original.Equal(copied))
}

func TestAddressJSON(t *testing.T) {
	address, _ := generateTestAddress(t)
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
	address, _ := generateTestAddress(t)

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
	address, _ := generateTestAddress(t)
	addrStr := address.String()

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
	require.True(t, strings.HasPrefix(nullStr, "0x"))
	require.NoError(t, Validate(nullStr)) // Should be valid hex address
	require.Equal(t, "0x0000000000000000000000000000000000000000", nullStr)
}

func TestAddressMetrics(t *testing.T) {
	metrics := AddressMetrics()

	require.Equal(t, "Ethereum Hex", metrics["format"])
	require.Equal(t, "0x", metrics["prefix"])
	require.Equal(t, 20, metrics["byte_length"])
	require.Equal(t, 42, metrics["estimated_str_length"])
	require.Equal(t, "2^160", metrics["collision_resistance"])
	require.Equal(t, false, metrics["case_sensitive"])
	require.Equal(t, "secp256k1 (Ethereum standard)", metrics["crypto_scheme"])
	require.Equal(t, "Full Ethereum/MetaMask compatibility", metrics["compatibility"])
}

func TestAddressLengthConsistency(t *testing.T) {
	// Generate multiple addresses and verify consistent length
	for i := 0; i < 10; i++ {
		address, _ := generateTestAddress(t)
		addrStr := address.String()

		require.Equal(t, 42, len(addrStr), "Address length should be consistent")
		require.True(t, strings.HasPrefix(addrStr, "0x"))
		require.NoError(t, Validate(addrStr))
	}
}

func TestHexCaseInsensitivity(t *testing.T) {
	address, _ := generateTestAddress(t)
	original := address.String()

	// Test that different cases are treated as equivalent
	lower := strings.ToLower(original)
	upper := strings.ToUpper(original)

	lowerAddr, err := FromString(lower)
	require.NoError(t, err)

	upperAddr, err := FromString(upper)
	require.NoError(t, err)

	// Should be equal when normalized
	require.Equal(t, strings.ToLower(lowerAddr.String()), strings.ToLower(upperAddr.String()))
}

func TestAddressDeterminism(t *testing.T) {
	// Test that the same private key always produces the same address
	privateKey, err := ethcrypto.GenerateKey()
	require.NoError(t, err)

	publicKey := privateKey.Public().(*ecdsa.PublicKey)

	// Generate address multiple times from same public key
	ethAddr1 := ethcrypto.PubkeyToAddress(*publicKey)
	ethAddr2 := ethcrypto.PubkeyToAddress(*publicKey)
	ethAddr3 := ethcrypto.PubkeyToAddress(*publicKey)

	addr1 := FromEthereumAddress(ethAddr1)
	addr2 := FromEthereumAddress(ethAddr2)
	addr3 := FromEthereumAddress(ethAddr3)

	// All should be identical
	require.Equal(t, addr1.String(), addr2.String())
	require.Equal(t, addr1.String(), addr3.String())
	require.True(t, addr1.Equal(addr2))
	require.True(t, addr1.Equal(addr3))
}

func TestAddressWithDifferentKeys(t *testing.T) {
	// Test that different keys produce different addresses
	addr1, _ := generateTestAddress(t)
	addr2, _ := generateTestAddress(t)

	// Should be different (extremely unlikely to be the same)
	require.NotEqual(t, addr1.String(), addr2.String())
	require.False(t, addr1.Equal(addr2))
}

func TestAddressRoundTrip(t *testing.T) {
	// Test full round trip: key -> address -> string -> address -> bytes
	originalAddr, _ := generateTestAddress(t)

	// Address to string
	addrStr := originalAddr.String()

	// String to address
	parsedAddr, err := FromString(addrStr)
	require.NoError(t, err)

	// Compare addresses
	require.True(t, originalAddr.Equal(parsedAddr))

	// Address to bytes
	originalBytes := originalAddr.Bytes()
	parsedBytes := parsedAddr.Bytes()

	// Compare bytes
	require.Equal(t, originalBytes, parsedBytes)

	// Bytes back to address
	bytesAddr, err := FromBytes(originalBytes)
	require.NoError(t, err)

	require.True(t, originalAddr.Equal(bytesAddr))
}

func TestEthereumAddressConversion(t *testing.T) {
	address, _ := generateTestAddress(t)

	// Test conversion to go-ethereum Address
	ethAddr := address.ToEthereumAddress()
	require.NotNil(t, ethAddr)

	// Convert back and should be equal
	converted := FromEthereumAddress(ethAddr)
	require.True(t, address.Equal(converted))
}

func TestChecksumAddress(t *testing.T) {
	address, _ := generateTestAddress(t)

	// Test checksum address (EIP-55)
	checksumAddr := address.ToChecksumAddress()
	require.True(t, strings.HasPrefix(checksumAddr, "0x"))
	require.Equal(t, 42, len(checksumAddr))

	// Should be valid
	require.NoError(t, Validate(checksumAddr))

	// Test ChecksumAddress function
	checksumAddr2, err := ChecksumAddress(address.String())
	require.NoError(t, err)
	require.Equal(t, checksumAddr, checksumAddr2)
}

func TestAddressSet(t *testing.T) {
	address, _ := generateTestAddress(t)

	// Test Set method
	newBytes := []byte{
		0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88,
		0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00,
		0x11, 0x22, 0x33, 0x44,
	}

	err := address.Set(newBytes)
	require.NoError(t, err)
	require.Equal(t, newBytes, address.Bytes())

	// Test Set with invalid length
	err = address.Set([]byte{0x01, 0x02})
	require.Error(t, err)
}

func TestAddressSetFromString(t *testing.T) {
	address, _ := generateTestAddress(t)
	newAddress, _ := generateTestAddress(t)

	newAddrStr := newAddress.String()
	err := address.SetFromString(newAddrStr)
	require.NoError(t, err)
	require.Equal(t, newAddrStr, address.String())

	// Test with invalid string
	err = address.SetFromString("invalid")
	require.Error(t, err)
}

func TestAddressHash(t *testing.T) {
	address, _ := generateTestAddress(t)

	hash := address.Hash()
	require.NotNil(t, hash)
	require.Equal(t, 32, len(hash)) // Keccak256 produces 32-byte hash

	// Test nil address
	var nilAddr *Address
	require.Nil(t, nilAddr.Hash())
}

func TestAddressNormalize(t *testing.T) {
	address, _ := generateTestAddress(t)

	normalized := address.Normalize()
	require.True(t, strings.HasPrefix(normalized, "0x"))
	require.Equal(t, normalized, strings.ToLower(normalized))
}

func TestNormalizeAddress(t *testing.T) {
	address, _ := generateTestAddress(t)
	addrStr := address.String()

	// Test with uppercase
	upperAddr := strings.ToUpper(addrStr)
	normalized, err := NormalizeAddress(upperAddr)
	require.NoError(t, err)
	require.Equal(t, strings.ToLower(addrStr), normalized)

	// Test with invalid address
	_, err = NormalizeAddress("invalid")
	require.Error(t, err)
}

func TestAddressToBytes(t *testing.T) {
	address, _ := generateTestAddress(t)
	addrStr := address.String()

	bytes, err := AddressToBytes(addrStr)
	require.NoError(t, err)
	require.Equal(t, address.Bytes(), bytes)
	require.Equal(t, 20, len(bytes))

	// Test with invalid address
	_, err = AddressToBytes("invalid")
	require.Error(t, err)
}

func TestIsNullAddress(t *testing.T) {
	nullAddr := NullAddress()
	require.True(t, IsNullAddress(nullAddr.String()))

	regularAddr, _ := generateTestAddress(t)
	require.False(t, IsNullAddress(regularAddr.String()))

	// Test with invalid address
	require.False(t, IsNullAddress("invalid"))
}

func TestCreateNullAddressString(t *testing.T) {
	nullStr := CreateNullAddressString()
	require.Equal(t, "0x0000000000000000000000000000000000000000", nullStr)
	require.NoError(t, Validate(nullStr))
}

func TestGetAddressPrefix(t *testing.T) {
	require.Equal(t, "0x", GetAddressPrefix())
}

func TestGetAddressByteLength(t *testing.T) {
	require.Equal(t, 20, GetAddressByteLength())
}

func TestEstimateAddressLength(t *testing.T) {
	require.Equal(t, 42, EstimateAddressLength())
}

func TestAddressCompare(t *testing.T) {
	addr1, _ := generateTestAddress(t)
	addr2, _ := generateTestAddress(t)

	// Test Compare with same address
	require.True(t, addr1.Compare(*addr1))

	// Test Compare with different address
	require.False(t, addr1.Compare(*addr2))

	// Test with copy
	addr1Copy := *addr1
	require.True(t, addr1.Compare(addr1Copy))
}

func TestSecp256k1Compatibility(t *testing.T) {
	// Test that addresses generated from secp256k1 keys are valid Ethereum addresses
	privateKey, err := ethcrypto.GenerateKey()
	require.NoError(t, err)

	publicKey := privateKey.Public().(*ecdsa.PublicKey)
	ethAddr := ethcrypto.PubkeyToAddress(*publicKey)

	// Create our Address from the Ethereum address
	address := FromEthereumAddress(ethAddr)

	// Verify it's a valid Ethereum address format
	addrStr := address.String()
	require.True(t, strings.HasPrefix(addrStr, "0x"))
	require.Equal(t, 42, len(addrStr))
	require.NoError(t, Validate(addrStr))

	// Verify conversion back to Ethereum address matches
	convertedEthAddr := address.ToEthereumAddress()
	require.Equal(t, ethAddr.Hex(), convertedEthAddr.Hex())
}

func BenchmarkGenerateAddress(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = generateTestAddress(nil)
	}
}

func BenchmarkFromString(b *testing.B) {
	address, _ := generateTestAddress(nil)
	addrStr := address.String()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = FromString(addrStr)
	}
}

func BenchmarkValidate(b *testing.B) {
	address, _ := generateTestAddress(nil)
	addrStr := address.String()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Validate(addrStr)
	}
}

func BenchmarkAddressString(b *testing.B) {
	address, _ := generateTestAddress(nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = address.String()
	}
}

func BenchmarkAddressEqual(b *testing.B) {
	addr1, _ := generateTestAddress(nil)
	addr2, _ := generateTestAddress(nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = addr1.Equal(addr2)
	}
}
