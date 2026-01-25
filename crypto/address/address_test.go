// crypto/address/address_test.go
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

func TestFromPublicKey(t *testing.T) {
	// Generate a key pair
	privateKey, err := ethcrypto.GenerateKey()
	require.NoError(t, err)

	publicKey := privateKey.Public().(*ecdsa.PublicKey)
	pubKeyBytes := ethcrypto.FromECDSAPub(publicKey)

	// Derive address from public key
	addr, err := FromPublicKey(pubKeyBytes)
	require.NoError(t, err)
	require.NotNil(t, addr)

	// Compare with go-ethereum's derivation
	ethAddr := ethcrypto.PubkeyToAddress(*publicKey)
	expectedAddr := FromEthereumAddress(ethAddr)

	require.True(t, addr.Equal(expectedAddr))

	// Test with invalid public key length
	_, err = FromPublicKey([]byte{0x01, 0x02, 0x03})
	require.Error(t, err)

	// Test with wrong prefix
	wrongPrefix := make([]byte, 65)
	wrongPrefix[0] = 0x05
	_, err = FromPublicKey(wrongPrefix)
	require.Error(t, err)
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
			name:    "valid checksummed address",
			address: validAddr.ToChecksumAddress(),
			valid:   true,
		},
		{
			name:    "invalid - no 0x prefix",
			address: strings.TrimPrefix(validAddrStr, "0x"),
			valid:   false,
		},
		{
			name:    "invalid - wrong prefix",
			address: "eth1234567890123456789012345678901234567890",
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

func TestEIP55Checksum(t *testing.T) {
	// Test with a known address and its checksum
	// Example from EIP-55: 0x5aAeb6053f3E94C9b9A09f33669435E7Ef1BeAed
	knownAddr := "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed"
	expectedChecksum := "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"

	checksummed := ToChecksumAddress(knownAddr)
	require.Equal(t, expectedChecksum, checksummed)

	// Test our Address type's method
	addr, err := FromString(knownAddr)
	require.NoError(t, err)
	require.Equal(t, expectedChecksum, addr.ToChecksumAddress())

	// Test checksum validation
	require.True(t, ValidateChecksum(expectedChecksum))
	require.False(t, ValidateChecksum(knownAddr)) // lowercase is not checksummed

	// Test IsChecksummed
	require.True(t, IsChecksummed(expectedChecksum))
	require.False(t, IsChecksummed(knownAddr))
}

func TestChecksumAddress(t *testing.T) {
	// More EIP-55 test vectors
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed",
			expected: "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed",
		},
		{
			input:    "0xfb6916095ca1df60bb79ce92ce3ea74c37c5d359",
			expected: "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359",
		},
		{
			input:    "0xdbf03b407c01e7cd3cbea99509d93f8dddc8c6fb",
			expected: "0xdbF03B407c01E7cD3CBea99509d93f8DDDC8C6FB",
		},
		{
			input:    "0xd1220a0cf47c7b9be7a2e6ba89f429762e7b9adb",
			expected: "0xD1220A0cf47c7B9Be7A2E6BA89F429762e7b9aDb",
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			checksummed := ToChecksumAddress(tt.input)
			require.Equal(t, tt.expected, checksummed)

			// Also test the Address method
			addr, _ := FromString(tt.input)
			require.Equal(t, tt.expected, addr.ToChecksumAddress())
		})
	}
}

func TestCreateAddress(t *testing.T) {
	// Test CREATE address calculation
	// Known test case from Ethereum
	sender, _ := FromString("0x6ac7ea33f8831ea9dcc53393aaa88b25a785dbf0")
	nonce := uint64(0)

	contractAddr := CreateAddress(sender, nonce)
	require.NotNil(t, contractAddr)

	// The contract address should be deterministic
	contractAddr2 := CreateAddress(sender, nonce)
	require.True(t, contractAddr.Equal(contractAddr2))

	// Different nonce should give different address
	contractAddr3 := CreateAddress(sender, nonce+1)
	require.False(t, contractAddr.Equal(contractAddr3))

	// Test with multiple nonces
	for i := uint64(0); i < 10; i++ {
		addr := CreateAddress(sender, i)
		require.NotNil(t, addr)
		require.False(t, addr.IsZero())
	}
}

func TestCreateAddress2(t *testing.T) {
	// Test CREATE2 address calculation
	sender, _ := FromString("0x0000000000000000000000000000000000000000")

	var salt [32]byte
	for i := range salt {
		salt[i] = byte(i)
	}

	var initCodeHash [32]byte
	for i := range initCodeHash {
		initCodeHash[i] = byte(255 - i)
	}

	// Calculate CREATE2 address
	contractAddr := CreateAddress2(sender, salt, initCodeHash)
	require.NotNil(t, contractAddr)
	require.False(t, contractAddr.IsZero())

	// Should be deterministic
	contractAddr2 := CreateAddress2(sender, salt, initCodeHash)
	require.True(t, contractAddr.Equal(contractAddr2))

	// Different salt should give different address
	var salt2 [32]byte
	copy(salt2[:], salt[:])
	salt2[0] = 0xFF

	contractAddr3 := CreateAddress2(sender, salt2, initCodeHash)
	require.False(t, contractAddr.Equal(contractAddr3))

	// Different init code hash should give different address
	var initCodeHash2 [32]byte
	copy(initCodeHash2[:], initCodeHash[:])
	initCodeHash2[31] = 0xFF // Change last byte instead of first

	contractAddr4 := CreateAddress2(sender, salt, initCodeHash2)
	require.False(t, contractAddr.Equal(contractAddr4))
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

	// Test checksummed address
	checksummed := validAddr.ToChecksumAddress()
	address3, err := FromString(checksummed)
	require.NoError(t, err)
	require.True(t, address.Equal(address3))

	// Test invalid address
	_, err = FromString("invalid")
	require.Error(t, err)
}

func TestCompareAddresses(t *testing.T) {
	addr1, _ := generateTestAddress(t)
	addr2, _ := generateTestAddress(t)

	// Test with same address
	equal, err := CompareAddresses(addr1.String(), addr1.String())
	require.NoError(t, err)
	require.True(t, equal)

	// Test with different addresses
	equal, err = CompareAddresses(addr1.String(), addr2.String())
	require.NoError(t, err)
	require.False(t, equal)

	// Test case insensitivity
	addr1Lower := strings.ToLower(addr1.String())
	addr1Upper := strings.ToUpper(addr1.String())

	equal, err = CompareAddresses(addr1Lower, addr1Upper)
	require.NoError(t, err)
	require.True(t, equal)

	// Test with invalid address
	_, err = CompareAddresses("invalid", addr2.String())
	require.Error(t, err)
}

func TestCreateAddressDeterminism(t *testing.T) {
	// Test that CREATE addresses are deterministic
	sender, _ := generateTestAddress(t)

	// Generate same address multiple times
	addresses := make([]*Address, 10)
	for i := 0; i < 10; i++ {
		addresses[i] = CreateAddress(sender, 5)
	}

	// All should be identical
	for i := 1; i < 10; i++ {
		require.True(t, addresses[0].Equal(addresses[i]))
	}
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

	_, err = FromBytes(make([]byte, 8))
	require.Error(t, err)
}

func TestAddressMethods(t *testing.T) {
	// Generate a test address
	address, _ := generateTestAddress(t)
	addr := address.String()

	// Test String()
	require.True(t, strings.HasPrefix(addr, "0x"))
	require.Equal(t, 42, len(addr))

	// Test Hex()
	hex := address.Hex()
	require.Equal(t, 40, len(hex))
	require.NotContains(t, hex, "0x")

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

	differentAddr, _ := generateTestAddress(t)
	require.False(t, address.Equal(differentAddr))

	// Test ShortString()
	shortStr := address.ShortString()
	require.Contains(t, shortStr, "0x")
	require.Contains(t, shortStr, "...")
	require.Less(t, len(shortStr), len(addr))
}

func TestAddressCopy(t *testing.T) {
	original, _ := generateTestAddress(t)
	copied := original.Copy()

	require.True(t, original.Equal(copied))
	require.NotSame(t, original, copied)

	// Modify original, copy should remain unchanged
	original.Set(make([]byte, 20))
	require.False(t, original.Equal(copied))
}

func TestAddressJSON(t *testing.T) {
	address, _ := generateTestAddress(t)

	// Test MarshalJSON (should return checksummed)
	jsonData, err := address.MarshalJSON()
	require.NoError(t, err)

	checksummed := address.ToChecksumAddress()
	require.Equal(t, `"`+checksummed+`"`, string(jsonData))

	// Test UnmarshalJSON
	var newAddress Address
	err = newAddress.UnmarshalJSON(jsonData)
	require.NoError(t, err)
	require.True(t, address.Equal(&newAddress))
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

func TestAddressTextMarshaling(t *testing.T) {
	address, _ := generateTestAddress(t)

	// Test MarshalText
	text, err := address.MarshalText()
	require.NoError(t, err)
	require.Equal(t, address.ToChecksumAddress(), string(text))

	// Test UnmarshalText
	var newAddress Address
	err = newAddress.UnmarshalText(text)
	require.NoError(t, err)
	require.True(t, address.Equal(&newAddress))
}

func TestNullAddress(t *testing.T) {
	nullAddr := NullAddress()
	require.NotNil(t, nullAddr)
	require.True(t, nullAddr.IsZero())

	nullStr := nullAddr.String()
	require.True(t, strings.HasPrefix(nullStr, "0x"))
	require.NoError(t, Validate(nullStr))
	require.Equal(t, "0x0000000000000000000000000000000000000000", nullStr)
}

func TestAddressMetrics(t *testing.T) {
	metrics := AddressMetrics()

	require.Equal(t, "Ethereum Hex", metrics["format"])
	require.Equal(t, "0x", metrics["prefix"])
	require.Equal(t, 20, metrics["byte_length"])
	require.Equal(t, 42, metrics["estimated_str_length"])
	require.Equal(t, "EIP-55 mixed-case checksum", metrics["checksum"])
	require.Equal(t, true, metrics["case_sensitive"]) // For checksummed addresses
	require.Equal(t, "2^160", metrics["collision_resistance"])
	require.Equal(t, "Keccak256(pubkey[1:])[12:]", metrics["derivation"])
	require.Contains(t, metrics, "create_address")
	require.Contains(t, metrics, "create2_address")
}

func TestSecp256k1Compatibility(t *testing.T) {
	privateKey, err := ethcrypto.GenerateKey()
	require.NoError(t, err)

	publicKey := privateKey.Public().(*ecdsa.PublicKey)
	ethAddr := ethcrypto.PubkeyToAddress(*publicKey)

	address := FromEthereumAddress(ethAddr)

	addrStr := address.String()
	require.True(t, strings.HasPrefix(addrStr, "0x"))
	require.Equal(t, 42, len(addrStr))
	require.NoError(t, Validate(addrStr))

	convertedEthAddr := address.ToEthereumAddress()
	require.Equal(t, ethAddr.Hex(), convertedEthAddr.Hex())
}

// Benchmarks
func BenchmarkFromPublicKey(b *testing.B) {
	privateKey, _ := ethcrypto.GenerateKey()
	publicKey := privateKey.Public().(*ecdsa.PublicKey)
	pubKeyBytes := ethcrypto.FromECDSAPub(publicKey)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = FromPublicKey(pubKeyBytes)
	}
}

func BenchmarkToChecksumAddress(b *testing.B) {
	addr, _ := generateTestAddress(nil)
	addrStr := addr.String()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ToChecksumAddress(addrStr)
	}
}

func BenchmarkCreateAddress(b *testing.B) {
	sender, _ := generateTestAddress(nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = CreateAddress(sender, uint64(i))
	}
}

func BenchmarkCreateAddress2(b *testing.B) {
	sender, _ := generateTestAddress(nil)
	var salt [32]byte
	var initCodeHash [32]byte

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = CreateAddress2(sender, salt, initCodeHash)
	}
}

func BenchmarkValidateChecksum(b *testing.B) {
	addr, _ := generateTestAddress(nil)
	checksummed := addr.ToChecksumAddress()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ValidateChecksum(checksummed)
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

func BenchmarkAddressEqual(b *testing.B) {
	addr1, _ := generateTestAddress(nil)
	addr2, _ := generateTestAddress(nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = addr1.Equal(addr2)
	}
}
