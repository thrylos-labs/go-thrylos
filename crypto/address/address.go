package address

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
	"strings"

	"github.com/btcsuite/btcutil/bech32"
	"github.com/fxamacker/cbor/v2"
	"github.com/thrylos-labs/go-thrylos/crypto/hash"
)

const (
	// Address format constants
	AddressPrefix     = "tl" // Thrylos prefix
	AddressByteLength = 8    // 8 bytes for shorter addresses (~18 characters)
)

// Address represents an 8-byte Thrylos address
type Address [AddressByteLength]byte

// New creates an Address from an Ed25519 public key using Blake2b hash
func New(pubKey ed25519.PublicKey) (*Address, error) {
	if pubKey == nil || len(pubKey) == 0 {
		return nil, fmt.Errorf("public key cannot be nil or empty")
	}

	if len(pubKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid Ed25519 public key size: got %d, want %d", len(pubKey), ed25519.PublicKeySize)
	}

	// Hash the public key with Blake2b-256
	hashBytes := hash.NewHash(pubKey)

	// Take the first 8 bytes of the hash (more secure than taking last bytes)
	var address Address
	copy(address[:], hashBytes[:AddressByteLength])

	return &address, nil
}

// NullAddress creates a zeroed Address
func NullAddress() *Address {
	return &Address{}
}

// FromString converts a tl1 address string to an Address
func FromString(addr string) (*Address, error) {
	if err := Validate(addr); err != nil {
		return nil, fmt.Errorf("invalid address format: %v", err)
	}

	// Decode Bech32 address
	_, data, err := bech32.Decode(addr)
	if err != nil {
		return nil, fmt.Errorf("failed to decode bech32 address: %v", err)
	}

	// Convert from 5-bit groups back to 8-bit bytes
	converted, err := bech32.ConvertBits(data, 5, 8, false)
	if err != nil {
		return nil, fmt.Errorf("failed to convert address bits: %v", err)
	}

	if len(converted) != AddressByteLength {
		return nil, fmt.Errorf("converted address has wrong length: expected %d, got %d", AddressByteLength, len(converted))
	}

	var address Address
	copy(address[:], converted)
	return &address, nil
}

// FromBytes creates an Address from raw bytes
func FromBytes(addressBytes []byte) (*Address, error) {
	if len(addressBytes) != AddressByteLength {
		return nil, fmt.Errorf("address bytes must be exactly %d bytes, got %d", AddressByteLength, len(addressBytes))
	}

	var address Address
	copy(address[:], addressBytes)
	return &address, nil
}

// Validate checks if a string is a valid tl1 address
func Validate(addr string) error {
	if len(addr) == 0 {
		return fmt.Errorf("address cannot be empty")
	}

	// Decode and validate with Bech32
	prefix, data, err := bech32.Decode(addr)
	if err != nil {
		return fmt.Errorf("invalid bech32 format: %v", err)
	}

	// Check prefix
	if prefix != AddressPrefix {
		return fmt.Errorf("address must start with '%s', got '%s'", AddressPrefix, prefix)
	}

	// Convert from 5-bit groups back to 8-bit bytes to validate length
	converted, err := bech32.ConvertBits(data, 5, 8, false)
	if err != nil {
		return fmt.Errorf("invalid address data: %v", err)
	}

	// Check converted data length
	if len(converted) != AddressByteLength {
		return fmt.Errorf("address must contain exactly %d bytes, got %d", AddressByteLength, len(converted))
	}

	return nil
}

// IsValid is a convenience function for address validation
func IsValid(addr string) bool {
	return Validate(addr) == nil
}

// ConvertToAddress creates a tl1 address string from Ed25519 public key
func ConvertToAddress(pubKey ed25519.PublicKey) (string, error) {
	addr, err := New(pubKey)
	if err != nil {
		return "", fmt.Errorf("failed to create address: %v", err)
	}
	return addr.String(), nil
}

// Bytes returns the raw 8-byte address
func (a *Address) Bytes() []byte {
	if a == nil {
		return nil
	}
	return a[:]
}

// String returns the tl1 bech32 string representation
func (a *Address) String() string {
	if a == nil {
		// Return a proper null address in bech32 format
		nullBytes := make([]byte, AddressByteLength)
		// Convert to 5-bit groups for bech32
		conv, err := bech32.ConvertBits(nullBytes, 8, 5, true)
		if err != nil {
			panic(fmt.Sprintf("failed to convert null address bits: %v", err))
		}
		encoded, err := bech32.Encode(AddressPrefix, conv)
		if err != nil {
			panic(fmt.Sprintf("failed to encode null address: %v", err))
		}
		return encoded
	}

	// Convert 8-bit bytes to 5-bit groups for bech32
	conv, err := bech32.ConvertBits(a[:], 8, 5, true)
	if err != nil {
		panic(fmt.Sprintf("failed to convert address bits %x: %v", a[:], err))
	}

	encoded, err := bech32.Encode(AddressPrefix, conv)
	if err != nil {
		panic(fmt.Sprintf("failed to encode address %x: %v", a[:], err))
	}

	return encoded
}

// Hex returns the hex string representation (for debugging)
func (a *Address) Hex() string {
	if a == nil {
		return "0000000000000000"
	}
	return fmt.Sprintf("%x", a[:])
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

	if len(slice) != AddressByteLength {
		return fmt.Errorf("unmarshaled data has incorrect length: expected %d, got %d", AddressByteLength, len(slice))
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
	if len(addressBytes) != AddressByteLength {
		return fmt.Errorf("address bytes must be exactly %d bytes, got %d", AddressByteLength, len(addressBytes))
	}
	copy(a[:], addressBytes)
	return nil
}

// SetFromString sets the address from a tl1 string
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

// Hash returns the Blake2b hash of the address (useful for maps)
func (a *Address) Hash() []byte {
	if a == nil {
		return nil
	}
	h := hash.NewHash(a[:])
	return h.Bytes()
}

// ToLower returns the address in lowercase (bech32 is case-insensitive by design)
func (a *Address) ToLower() string {
	return strings.ToLower(a.String())
}

// ToUpper returns the address in uppercase (bech32 is case-insensitive by design)
func (a *Address) ToUpper() string {
	return strings.ToUpper(a.String())
}

// Normalize returns the address in lowercase for consistent storage
func (a *Address) Normalize() string {
	return a.ToLower()
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

// FormatAddress formats raw bytes as a tl1 address string
func FormatAddress(addressBytes []byte) (string, error) {
	addr, err := FromBytes(addressBytes)
	if err != nil {
		return "", err
	}
	return addr.String(), nil
}

// GetAddressPrefix returns the address prefix for Thrylos
func GetAddressPrefix() string {
	return AddressPrefix
}

// GetAddressByteLength returns the byte length of Thrylos addresses
func GetAddressByteLength() int {
	return AddressByteLength
}

// EstimateAddressLength estimates the string length of a Thrylos address
func EstimateAddressLength() int {
	// With proper bit conversion, 8 bytes produces 22 characters
	return 22
}

// AddressMetrics provides information about the address format
func AddressMetrics() map[string]interface{} {
	return map[string]interface{}{
		"format":               "Bech32",
		"prefix":               AddressPrefix,
		"byte_length":          AddressByteLength,
		"estimated_str_length": EstimateAddressLength(),
		"checksum":             "Built-in Bech32 checksum",
		"case_sensitive":       false,  // Bech32 is case-insensitive
		"collision_resistance": "2^64", // 8 bytes = 64 bits
		"example":              "tl1rn5evt8jyynf7ldr5fk",
		"crypto_scheme":        "Ed25519", // Updated for Ed25519
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
