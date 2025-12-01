// Package address provides Ethereum-compatible addresses for Thrylos
//
// Uses standard Ethereum 0x addresses (20 bytes) for full Metamask compatibility.
// This replaces the previous tl1 bech32 format to avoid user confusion.
package address

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
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

// New creates an Address from an Ed25519 public key (legacy helper for older address scheme).
// New code should prefer secp256k1-based crypto.PublicKey.Address().
func New(pubKey ed25519.PublicKey) (*Address, error) {
	if pubKey == nil || len(pubKey) == 0 {
		return nil, fmt.Errorf("public key cannot be nil or empty")
	}

	if len(pubKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid Ed25519 public key size: got %d, want %d", len(pubKey), ed25519.PublicKeySize)
	}

	// Convert Ed25519 public key to Ethereum address format
	// Take keccak256 hash and use last 20 bytes (Ethereum standard)
	hashBytes := crypto.Keccak256(pubKey)

	var address Address
	copy(address[:], hashBytes[len(hashBytes)-AddressLength:])

	return &address, nil
}

// NullAddress creates a zeroed Address (0x0000...0000)
func NullAddress() *Address {
	return &Address{}
}

// FromString converts a 0x hex address string to an Address
func FromString(addr string) (*Address, error) {
	if err := Validate(addr); err != nil {
		return nil, fmt.Errorf("invalid address format: %v", err)
	}

	// Remove 0x prefix if present
	addr = strings.TrimPrefix(strings.ToLower(addr), "0x")

	// Decode hex
	decoded, err := hex.DecodeString(addr)
	if err != nil {
		return nil, fmt.Errorf("failed to decode hex address: %v", err)
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
	addrHex := strings.TrimPrefix(strings.ToLower(addr), "0x")

	// Check length (40 hex chars = 20 bytes)
	if len(addrHex) != 40 {
		return fmt.Errorf("address must be 40 hex characters (20 bytes), got %d", len(addrHex))
	}

	// Verify it's valid hex
	_, err := hex.DecodeString(addrHex)
	if err != nil {
		return fmt.Errorf("address contains invalid hex characters: %v", err)
	}

	return nil
}

// IsValid is a convenience function for address validation
func IsValid(addr string) bool {
	return Validate(addr) == nil
}

// ConvertToAddress creates a 0x address string from Ed25519 public key
func ConvertToAddress(pubKey ed25519.PublicKey) (string, error) {
	addr, err := New(pubKey)
	if err != nil {
		return "", fmt.Errorf("failed to create address: %v", err)
	}
	return addr.String(), nil
}

// Bytes returns the raw 20-byte address
func (a *Address) Bytes() []byte {
	if a == nil {
		return nil
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

// Hex returns the hex string without 0x prefix (for debugging)
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
		return fmt.Errorf("failed to unmarshal CBOR data: %v", err)
	}

	if len(slice) != AddressLength {
		return fmt.Errorf("unmarshaled data has incorrect length: expected %d, got %d", AddressLength, len(slice))
	}

	copy(a[:], slice)
	return nil
}

// MarshalJSON implements json.Marshaler interface
func (a *Address) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`"%s"`, a.String())), nil
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
		return fmt.Errorf("failed to parse address from JSON: %v", err)
	}

	copy(a[:], addr[:])
	return nil
}

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

// Hash returns the keccak256 hash of the address (Ethereum standard)
func (a *Address) Hash() []byte {
	if a == nil {
		return nil
	}
	return crypto.Keccak256(a[:])
}

// ToLower returns the address in lowercase (already lowercase by default)
func (a *Address) ToLower() string {
	return strings.ToLower(a.String())
}

// ToUpper returns the address in uppercase
func (a *Address) ToUpper() string {
	return strings.ToUpper(a.String())
}

// Normalize returns the address in lowercase for consistent storage
func (a *Address) Normalize() string {
	return a.ToLower()
}

// ToChecksumAddress returns the EIP-55 checksum address
func (a *Address) ToChecksumAddress() string {
	if a == nil {
		return "0x0000000000000000000000000000000000000000"
	}
	return a.ToEthereumAddress().Hex()
}

// Utility functions for compatibility

// GenerateAddress generates an address from Ed25519 public key (wrapper function)
func GenerateAddress(pubKey ed25519.PublicKey) (string, error) {
	addr, err := New(pubKey)
	if err != nil {
		return "", err
	}
	return addr.String(), nil
}

// ParseAddress parses a string address and returns the Address object
func ParseAddress(addrStr string) (*Address, error) {
	return FromString(addrStr)
}

// FormatAddress formats raw bytes as a 0x address string
func FormatAddress(addressBytes []byte) (string, error) {
	addr, err := FromBytes(addressBytes)
	if err != nil {
		return "", err
	}
	return addr.String(), nil
}

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
		"checksum":             "EIP-55 checksum support",
		"case_sensitive":       false,
		"collision_resistance": "2^160", // 20 bytes = 160 bits
		"example":              "0x742d35cc6634c0532925a3b844bc9e7595f0beef",
		"crypto_scheme":        "Ed25519 (Ethereum-compatible)",
		"compatibility":        "Full Ethereum/Metamask compatibility",
	}
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
		return nil, fmt.Errorf("failed to parse address: %v", err)
	}
	return parsed.Bytes(), nil
}

// ChecksumAddress converts an address to EIP-55 checksum format
func ChecksumAddress(addr string) (string, error) {
	parsed, err := FromString(addr)
	if err != nil {
		return "", err
	}
	return parsed.ToChecksumAddress(), nil
}
