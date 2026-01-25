// crypto/address/address.go
// Package address provides Ethereum-compatible addresses for Thrylos
//
// Uses standard Ethereum addresses (20 bytes) derived from secp256k1 public keys
// for full MetaMask and Ethereum tooling compatibility.
//
// Address Derivation: Keccak256(uncompressed_pubkey[1:])[12:]
// Checksum Format: EIP-55 mixed-case checksumming
package address

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/fxamacker/cbor/v2"
)

const (
	// AddressLength is the Ethereum address length (20 bytes)
	AddressLength = 20

	// AddressPrefix is the Ethereum hex prefix
	AddressPrefix = "0x"
)

// Address represents a 20-byte Ethereum-compatible address
type Address [AddressLength]byte

// ============================================================================
// Address Creation Functions
// ============================================================================

// NullAddress creates a zeroed Address (0x0000...0000)
func NullAddress() *Address {
	return &Address{}
}

// FromString converts a 0x hex address string to an Address
func FromString(addr string) (*Address, error) {
	if err := Validate(addr); err != nil {
		return nil, fmt.Errorf("invalid address format: %w", err)
	}

	// Remove 0x prefix (case insensitive)
	addr = strings.TrimPrefix(addr, "0x")
	addr = strings.TrimPrefix(addr, "0X")

	// Convert to lowercase for decoding
	addr = strings.ToLower(addr)

	// Decode hex
	decoded, err := hex.DecodeString(addr)
	if err != nil {
		return nil, fmt.Errorf("failed to decode hex address: %w", err)
	}

	if len(decoded) != AddressLength {
		return nil, fmt.Errorf("decoded address has wrong length: expected %d, got %d", AddressLength, len(decoded))
	}

	var address Address
	copy(address[:], decoded)
	return &address, nil
}

// FromBytes creates an Address from raw bytes
func FromBytes(addressBytes []byte) (*Address, error) {
	if len(addressBytes) != AddressLength {
		return nil, fmt.Errorf("address bytes must be exactly %d bytes, got %d", AddressLength, len(addressBytes))
	}

	var address Address
	copy(address[:], addressBytes)
	return &address, nil
}

// FromEthereumAddress converts a go-ethereum common.Address to our Address
func FromEthereumAddress(ethAddr common.Address) *Address {
	var address Address
	copy(address[:], ethAddr.Bytes())
	return &address
}

// FromPublicKey derives an Ethereum address from a public key
// Public key must be uncompressed (65 bytes: 0x04 || X || Y)
// Address = Keccak256(pubkey[1:])[12:]
func FromPublicKey(pubKeyBytes []byte) (*Address, error) {
	if len(pubKeyBytes) != 65 {
		return nil, fmt.Errorf("public key must be 65 bytes (uncompressed), got %d", len(pubKeyBytes))
	}

	if pubKeyBytes[0] != 0x04 {
		return nil, fmt.Errorf("public key must start with 0x04 (uncompressed format)")
	}

	// Hash the public key coordinates (skip the 0x04 prefix)
	hash := ethcrypto.Keccak256(pubKeyBytes[1:])

	// Take the last 20 bytes
	var address Address
	copy(address[:], hash[12:])

	return &address, nil
}

// ============================================================================
// Validation Functions
// ============================================================================

// Validate checks if a string is a valid Ethereum address
func Validate(addr string) error {
	if len(addr) == 0 {
		return fmt.Errorf("address cannot be empty")
	}

	// Check for 0x prefix
	if !strings.HasPrefix(strings.ToLower(addr), "0x") {
		return fmt.Errorf("address must start with 0x prefix")
	}

	// Remove 0x prefix for validation
	addrHex := strings.TrimPrefix(addr, "0x")
	addrHex = strings.TrimPrefix(addrHex, "0X") // Handle uppercase 0X

	// Check length (40 hex chars = 20 bytes)
	if len(addrHex) != 40 {
		return fmt.Errorf("address must be 40 hex characters (20 bytes), got %d", len(addrHex))
	}

	// Verify it's valid hex
	_, err := hex.DecodeString(addrHex)
	if err != nil {
		return fmt.Errorf("address contains invalid hex characters: %w", err)
	}

	return nil
}

// IsValid is a convenience function for address validation
func IsValid(addr string) bool {
	return Validate(addr) == nil
}

// ValidateChecksum validates an EIP-55 checksummed address
func ValidateChecksum(addr string) bool {
	if !IsValid(addr) {
		return false
	}

	// Get the checksummed version
	checksummed := ToChecksumAddress(addr)

	// Compare with original
	return addr == checksummed
}

// ============================================================================
// Address Methods
// ============================================================================

// Bytes returns the raw 20-byte address
func (a *Address) Bytes() []byte {
	if a == nil {
		return make([]byte, AddressLength)
	}
	return a[:]
}

// String returns the 0x hex string representation (lowercase)
func (a *Address) String() string {
	if a == nil {
		return "0x0000000000000000000000000000000000000000"
	}
	return "0x" + hex.EncodeToString(a[:])
}

// Hex returns the hex string without 0x prefix
func (a *Address) Hex() string {
	if a == nil {
		return "0000000000000000000000000000000000000000"
	}
	return hex.EncodeToString(a[:])
}

// ToEthereumAddress converts to go-ethereum common.Address
func (a *Address) ToEthereumAddress() common.Address {
	if a == nil {
		return common.Address{}
	}
	return common.BytesToAddress(a[:])
}

// IsZero checks if the address is all zeros
func (a *Address) IsZero() bool {
	if a == nil {
		return true
	}
	for _, b := range a {
		if b != 0 {
			return false
		}
	}
	return true
}

// Equal checks if two addresses are identical
func (a *Address) Equal(other *Address) bool {
	if a == nil && other == nil {
		return true
	}
	if a == nil || other == nil {
		return false
	}
	return bytes.Equal(a[:], other[:])
}

// Compare checks if two Addresses are identical (legacy method name)
func (a *Address) Compare(other Address) bool {
	return bytes.Equal(a[:], other[:])
}

// Hash returns the keccak256 hash of the address
func (a *Address) Hash() []byte {
	if a == nil {
		return nil
	}
	return ethcrypto.Keccak256(a[:])
}

// ============================================================================
// EIP-55 Checksum Support
// ============================================================================

// ToChecksumAddress returns the EIP-55 checksummed address
// https://eips.ethereum.org/EIPS/eip-55
func (a *Address) ToChecksumAddress() string {
	if a == nil {
		return "0x0000000000000000000000000000000000000000"
	}
	return ToChecksumAddress(a.String())
}

// ToChecksumAddress converts any address string to EIP-55 checksum format
func ToChecksumAddress(addr string) string {
	// Remove 0x prefix if present
	addr = strings.TrimPrefix(strings.ToLower(addr), "0x")

	// Hash the lowercase address
	hash := ethcrypto.Keccak256([]byte(addr))

	// Build checksummed address
	result := make([]byte, 42)
	result[0] = '0'
	result[1] = 'x'

	for i := 0; i < len(addr); i++ {
		char := addr[i]
		// If it's a letter (a-f) and corresponding hash nibble is >= 8, capitalize
		if char >= 'a' && char <= 'f' {
			// Get the hash nibble for this position
			hashByte := hash[i/2]
			var nibble byte
			if i%2 == 0 {
				nibble = hashByte >> 4
			} else {
				nibble = hashByte & 0x0f
			}

			if nibble >= 8 {
				char = char - 32 // Convert to uppercase
			}
		}
		result[i+2] = char
	}

	return string(result)
}

// IsChecksummed checks if an address is properly checksummed
func IsChecksummed(addr string) bool {
	if !IsValid(addr) {
		return false
	}
	return addr == ToChecksumAddress(addr)
}

// ============================================================================
// Contract Address Derivation (CREATE and CREATE2)
// ============================================================================

// CreateAddress calculates the address for a contract created with CREATE
// Address = Keccak256(RLP(sender_address, nonce))[12:]
func CreateAddress(sender *Address, nonce uint64) *Address {
	if sender == nil {
		return NullAddress()
	}

	// RLP encode [address, nonce]
	rlp := rlpEncodeCreateAddress(sender.Bytes(), nonce)

	// Hash and take last 20 bytes
	hash := ethcrypto.Keccak256(rlp)

	var addr Address
	copy(addr[:], hash[12:])
	return &addr
}

// CreateAddress2 calculates the address for a contract created with CREATE2
// Address = Keccak256(0xff || sender || salt || keccak256(init_code))[12:]
func CreateAddress2(sender *Address, salt [32]byte, initCodeHash [32]byte) *Address {
	if sender == nil {
		return NullAddress()
	}

	// Build: 0xff || sender || salt || initCodeHash
	data := make([]byte, 1+20+32+32)
	data[0] = 0xff
	copy(data[1:], sender.Bytes())
	copy(data[21:], salt[:])
	copy(data[53:], initCodeHash[:])

	// Hash and take last 20 bytes
	hash := ethcrypto.Keccak256(data)

	var addr Address
	copy(addr[:], hash[12:])
	return &addr
}

// rlpEncodeCreateAddress encodes [address, nonce] in RLP format
// Simplified RLP encoding specifically for CREATE address derivation
func rlpEncodeCreateAddress(address []byte, nonce uint64) []byte {
	// Calculate nonce encoding
	nonceBytes := encodeRLPUint(nonce)

	// List length
	listLen := 1 + len(address) + len(nonceBytes)

	var rlp []byte

	// List header
	if listLen < 56 {
		rlp = append(rlp, byte(0xc0+listLen))
	} else {
		// Long list encoding (not needed for our use case but included for completeness)
		lenBytes := toBytes(uint64(listLen))
		rlp = append(rlp, byte(0xf7+len(lenBytes)))
		rlp = append(rlp, lenBytes...)
	}

	// Address (always 20 bytes, so 0x80 + 20 = 0x94)
	rlp = append(rlp, 0x94)
	rlp = append(rlp, address...)

	// Nonce
	rlp = append(rlp, nonceBytes...)

	return rlp
}

// encodeRLPUint encodes a uint64 in RLP format
func encodeRLPUint(n uint64) []byte {
	if n == 0 {
		return []byte{0x80}
	}
	if n < 0x80 {
		return []byte{byte(n)}
	}

	// Convert to bytes (big-endian)
	bytes := toBytes(n)

	// Add length prefix
	result := make([]byte, 0, 1+len(bytes))
	result = append(result, byte(0x80+len(bytes)))
	result = append(result, bytes...)

	return result
}

// toBytes converts uint64 to big-endian bytes (without leading zeros)
func toBytes(n uint64) []byte {
	if n == 0 {
		return []byte{0}
	}

	// Count bytes needed
	var bytes []byte
	for n > 0 {
		bytes = append([]byte{byte(n)}, bytes...)
		n >>= 8
	}
	return bytes
}

// ============================================================================
// Serialization Methods
// ============================================================================

// Marshal encodes the Address using CBOR
func (a *Address) Marshal() ([]byte, error) {
	if a == nil {
		return nil, fmt.Errorf("cannot marshal nil address")
	}
	return cbor.Marshal(a[:])
}

// Unmarshal decodes CBOR data into the Address
func (a *Address) Unmarshal(data []byte) error {
	if a == nil {
		return fmt.Errorf("cannot unmarshal into nil address")
	}

	var slice []byte
	if err := cbor.Unmarshal(data, &slice); err != nil {
		return fmt.Errorf("failed to unmarshal CBOR data: %w", err)
	}

	if len(slice) != AddressLength {
		return fmt.Errorf("unmarshaled data has incorrect length: expected %d, got %d", AddressLength, len(slice))
	}

	copy(a[:], slice)
	return nil
}

// MarshalJSON implements json.Marshaler interface
// Returns checksummed address for JSON
func (a *Address) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`"%s"`, a.ToChecksumAddress())), nil
}

// UnmarshalJSON implements json.Unmarshaler interface
func (a *Address) UnmarshalJSON(data []byte) error {
	if len(data) < 2 {
		return fmt.Errorf("invalid JSON data for address")
	}

	// Remove quotes from JSON string
	addrStr := string(data[1 : len(data)-1])

	addr, err := FromString(addrStr)
	if err != nil {
		return fmt.Errorf("failed to parse address from JSON: %w", err)
	}

	copy(a[:], addr[:])
	return nil
}

// MarshalText implements encoding.TextMarshaler
func (a *Address) MarshalText() ([]byte, error) {
	return []byte(a.ToChecksumAddress()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler
func (a *Address) UnmarshalText(text []byte) error {
	addr, err := FromString(string(text))
	if err != nil {
		return err
	}
	copy(a[:], addr[:])
	return nil
}

// ============================================================================
// Utility Methods
// ============================================================================

// Set sets the address to the given bytes
func (a *Address) Set(addressBytes []byte) error {
	if len(addressBytes) != AddressLength {
		return fmt.Errorf("address bytes must be exactly %d bytes, got %d", AddressLength, len(addressBytes))
	}
	copy(a[:], addressBytes)
	return nil
}

// SetFromString sets the address from a 0x string
func (a *Address) SetFromString(addr string) error {
	parsed, err := FromString(addr)
	if err != nil {
		return err
	}
	copy(a[:], parsed[:])
	return nil
}

// Copy creates a copy of the address
func (a *Address) Copy() *Address {
	if a == nil {
		return nil
	}
	var copy Address
	copy = *a
	return &copy
}

// ToLower returns the address in lowercase
func (a *Address) ToLower() string {
	return strings.ToLower(a.String())
}

// ToUpper returns the address in uppercase (not recommended, use checksummed)
func (a *Address) ToUpper() string {
	return strings.ToUpper(a.String())
}

// Normalize returns the address in lowercase for consistent storage
func (a *Address) Normalize() string {
	return a.ToLower()
}

// ShortString returns a shortened version for display
func (a *Address) ShortString() string {
	if a == nil || a.IsZero() {
		return "0x0000...0000"
	}
	str := a.String()
	if len(str) < 10 {
		return str
	}
	return str[:6] + "..." + str[len(str)-4:]
}

// ============================================================================
// Package-Level Utility Functions
// ============================================================================

// ParseAddress parses a string address and returns the Address object
func ParseAddress(addrStr string) (*Address, error) {
	return FromString(addrStr)
}

// FormatAddress formats raw bytes as a checksummed 0x address string
func FormatAddress(addressBytes []byte) (string, error) {
	addr, err := FromBytes(addressBytes)
	if err != nil {
		return "", err
	}
	return addr.ToChecksumAddress(), nil
}

// NormalizeAddress converts address to lowercase for consistent storage
func NormalizeAddress(addr string) (string, error) {
	if err := Validate(addr); err != nil {
		return "", err
	}
	return strings.ToLower(addr), nil
}

// AddressToBytes converts a string address to its byte representation
func AddressToBytes(addr string) ([]byte, error) {
	parsed, err := FromString(addr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse address: %w", err)
	}
	return parsed.Bytes(), nil
}

// ChecksumAddress converts an address to EIP-55 checksum format
func ChecksumAddress(addr string) (string, error) {
	if err := Validate(addr); err != nil {
		return "", err
	}
	return ToChecksumAddress(addr), nil
}

// IsNullAddress checks if the address is the null/zero address
func IsNullAddress(addr string) bool {
	parsed, err := FromString(addr)
	if err != nil {
		return false
	}
	return parsed.IsZero()
}

// CreateNullAddressString returns the null address as a string
func CreateNullAddressString() string {
	return NullAddress().String()
}

// CompareAddresses compares two addresses for equality (case-insensitive)
func CompareAddresses(a, b string) (bool, error) {
	addrA, err := FromString(a)
	if err != nil {
		return false, err
	}

	addrB, err := FromString(b)
	if err != nil {
		return false, err
	}

	return addrA.Equal(addrB), nil
}

// ============================================================================
// Informational Functions
// ============================================================================

// GetAddressPrefix returns the address prefix for Ethereum compatibility
func GetAddressPrefix() string {
	return AddressPrefix
}

// GetAddressByteLength returns the byte length of addresses
func GetAddressByteLength() int {
	return AddressLength
}

// EstimateAddressLength estimates the string length of an address (0x + 40 hex chars)
func EstimateAddressLength() int {
	return 42 // "0x" + 40 hex characters
}

// AddressMetrics provides information about the address format
func AddressMetrics() map[string]interface{} {
	return map[string]interface{}{
		"format":               "Ethereum Hex",
		"prefix":               AddressPrefix,
		"byte_length":          AddressLength,
		"estimated_str_length": EstimateAddressLength(),
		"checksum":             "EIP-55 mixed-case checksum",
		"case_sensitive":       true,    // For checksummed addresses
		"collision_resistance": "2^160", // 20 bytes = 160 bits
		"derivation":           "Keccak256(pubkey[1:])[12:]",
		"example":              "0x742d35Cc6634C0532925a3b844Bc9e7595f0bEEb",
		"crypto_scheme":        "secp256k1 (Ethereum standard)",
		"compatibility":        "Full Ethereum/MetaMask/EIP-55 compatibility",
		"create_address":       "Keccak256(RLP(sender, nonce))[12:]",
		"create2_address":      "Keccak256(0xff || sender || salt || init_code_hash)[12:]",
	}
}

// ============================================================================
// REMOVED: All Ed25519 address derivation
// REMOVED: All bech32 (tl1) address formats
// ADDED: Proper Ethereum address derivation from secp256k1 public keys
// ADDED: EIP-55 checksum support
// ADDED: CREATE and CREATE2 address calculation
// ADDED: Full JSON/CBOR serialization with checksums
// ============================================================================
