package address

import (
	"bytes"
	"fmt"
	"strings"

	mldsa "github.com/cloudflare/circl/sign/mldsa/mldsa44"
	"github.com/fxamacker/cbor/v2"
	"github.com/thrylos-labs/go-thrylos/crypto/hash"
)

const (
	// Address format constants
	AddressPrefix     = "0x"
	AddressLength     = 42 // "0x" + 40 hex characters
	AddressByteLength = 20 // 20 bytes = 40 hex characters
)

// Address represents a 20-byte Ethereum-compatible address
type Address [AddressByteLength]byte

// New creates an Address from a public key using Blake2b hash
func New(pubKey *mldsa.PublicKey) (*Address, error) {
	if pubKey == nil {
		return nil, fmt.Errorf("public key cannot be nil")
	}

	pubKeyBytes := pubKey.Bytes()
	if len(pubKeyBytes) == 0 {
		return nil, fmt.Errorf("public key bytes cannot be empty")
	}

	// Hash the public key with Blake2b-256
	hashBytes := hash.NewHash(pubKeyBytes)

	// Take the last 20 bytes of the hash (Ethereum standard)
	var address Address
	copy(address[:], hashBytes[len(hashBytes)-20:])

	return &address, nil
}

// NullAddress creates a zeroed Address
func NullAddress() *Address {
	return &Address{}
}

// FromString converts a 0x address string to an Address
func FromString(addr string) (*Address, error) {
	if err := Validate(addr); err != nil {
		return nil, fmt.Errorf("invalid address format: %v", err)
	}

	// Remove "0x" prefix
	hexPart := addr[2:]

	var address Address
	for i := 0; i < len(hexPart); i += 2 {
		var b byte
		n, err := fmt.Sscanf(hexPart[i:i+2], "%02x", &b)
		if err != nil || n != 1 {
			return nil, fmt.Errorf("invalid hex at position %d-%d: %v", i, i+1, err)
		}
		address[i/2] = b
	}

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

// Validate checks if a string is a valid 0x address
func Validate(addr string) error {
	if len(addr) != AddressLength {
		return fmt.Errorf("address must be exactly %d characters long, got %d", AddressLength, len(addr))
	}

	if !strings.HasPrefix(addr, AddressPrefix) {
		return fmt.Errorf("address must start with '%s', got '%s'", AddressPrefix, addr[:2])
	}

	// Validate hex characters
	hexPart := addr[2:]
	for i, char := range hexPart {
		if !isHexChar(char) {
			return fmt.Errorf("address contains invalid hex character '%c' at position %d", char, i+2)
		}
	}

	return nil
}

// IsValid is a convenience function for address validation
func IsValid(addr string) bool {
	return Validate(addr) == nil
}

// isHexChar checks if a character is a valid hex digit
func isHexChar(char rune) bool {
	return (char >= '0' && char <= '9') ||
		(char >= 'a' && char <= 'f') ||
		(char >= 'A' && char <= 'F')
}

// ConvertToBech32Address is now ConvertToAddress for 0x format
func ConvertToAddress(pubKey *mldsa.PublicKey) (string, error) {
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

// String returns the 0x-prefixed hex string representation
func (a *Address) String() string {
	if a == nil {
		return "0x0000000000000000000000000000000000000000"
	}
	return fmt.Sprintf("%s%x", AddressPrefix, a[:])
}

// Hex returns the hex string without 0x prefix
func (a *Address) Hex() string {
	if a == nil {
		return "0000000000000000000000000000000000000000"
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

// Hash returns the Blake2b hash of the address (useful for maps)
func (a *Address) Hash() []byte {
	if a == nil {
		return nil
	}
	h := hash.NewHash(a[:])
	return h.Bytes()
}

// ToLower returns the address in lowercase (for consistent storage)
func (a *Address) ToLower() string {
	return strings.ToLower(a.String())
}

// Utility functions for compatibility

// GenerateAddress generates an address from public key (wrapper function)
func GenerateAddress(pubKey *mldsa.PublicKey) (string, error) {
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
