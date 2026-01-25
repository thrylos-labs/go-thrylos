// crypto/private_key.go
package crypto

import (
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/secp256k1"
	"github.com/thrylos-labs/go-thrylos/crypto/address"
)

// PrivateKeyImpl implements the PrivateKey interface for Secp256k1
type PrivateKeyImpl struct {
	key *ecdsa.PrivateKey
}

// Ensure PrivateKeyImpl implements PrivateKey interface
var _ PrivateKey = (*PrivateKeyImpl)(nil)

// NewPrivateKey generates a new random Secp256k1 private key
func NewPrivateKey() (PrivateKey, error) {
	key, err := ecdsa.GenerateKey(secp256k1.S256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %w", err)
	}
	return &PrivateKeyImpl{key: key}, nil
}

// NewPrivateKeyFromBytes creates a private key from a 32-byte slice
func NewPrivateKeyFromBytes(keyData []byte) (PrivateKey, error) {
	if len(keyData) != 32 {
		return nil, fmt.Errorf("invalid private key length: expected 32 bytes, got %d", len(keyData))
	}

	key, err := ethcrypto.ToECDSA(keyData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	// Validate the key is in valid range [1, n-1]
	d := new(big.Int).SetBytes(keyData)
	if d.Cmp(big.NewInt(0)) <= 0 || d.Cmp(secp256k1.S256().Params().N) >= 0 {
		return nil, fmt.Errorf("private key out of valid range")
	}

	return &PrivateKeyImpl{key: key}, nil
}

// NewPrivateKeyFromHex creates a private key from a hex-encoded string
func NewPrivateKeyFromHex(hexKey string) (PrivateKey, error) {
	// Remove 0x prefix if present
	if len(hexKey) > 2 && hexKey[:2] == "0x" {
		hexKey = hexKey[2:]
	}

	keyData, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("invalid hex encoding: %w", err)
	}

	return NewPrivateKeyFromBytes(keyData)
}

// Bytes returns the 32-byte representation of the private key
func (p *PrivateKeyImpl) Bytes() []byte {
	if p.key == nil {
		return nil
	}
	return ethcrypto.FromECDSA(p.key)
}

// String returns the hex-encoded private key with 0x prefix
func (p *PrivateKeyImpl) String() string {
	if p.key == nil {
		return "PrivateKey(nil)"
	}
	return "0x" + hex.EncodeToString(p.Bytes())
}

// Sign calculates a Keccak256 hash of msg and signs it
// Returns a 65-byte signature [R || S || V]
func (p *PrivateKeyImpl) Sign(msg []byte) (Signature, error) {
	if p.key == nil {
		return nil, fmt.Errorf("cannot sign with nil private key")
	}

	// Calculate Keccak256 hash of the message
	hash := ethcrypto.Keccak256(msg)

	// Sign the hash
	return p.SignHash(hash)
}

// SignHash signs a pre-calculated 32-byte Keccak256 hash
// This is the preferred method to avoid double-hashing
func (p *PrivateKeyImpl) SignHash(hash []byte) (Signature, error) {
	if p.key == nil {
		return nil, fmt.Errorf("cannot sign with nil private key")
	}

	if len(hash) != 32 {
		return nil, fmt.Errorf("hash must be 32 bytes, got %d", len(hash))
	}

	// Use go-ethereum's signing which handles low-s normalization automatically
	sigBytes, err := ethcrypto.Sign(hash, p.key)
	if err != nil {
		return nil, fmt.Errorf("failed to sign hash: %w", err)
	}

	// sigBytes is [R || S || V] (65 bytes)
	// go-ethereum uses V = 0 or 1 (not 27/28)
	return NewSignatureFromBytes(sigBytes)
}

// PublicKey derives the corresponding Secp256k1 public key
func (p *PrivateKeyImpl) PublicKey() PublicKey {
	if p.key == nil {
		return nil
	}
	return &PublicKeyImpl{pubKey: &p.key.PublicKey}
}

// Address derives the Ethereum-style address (20 bytes)
func (p *PrivateKeyImpl) Address() *address.Address {
	if p.key == nil {
		return address.NullAddress()
	}
	pubKey := p.PublicKey()
	return pubKey.Address()
}

// ToECDSA converts to Go's native ECDSA private key
func (p *PrivateKeyImpl) ToECDSA() *ecdsa.PrivateKey {
	return p.key
}

// Marshal serializes the private key to 32 bytes
func (p *PrivateKeyImpl) Marshal() ([]byte, error) {
	if p.key == nil {
		return nil, fmt.Errorf("cannot marshal nil private key")
	}
	return p.Bytes(), nil
}

// Unmarshal deserializes a 32-byte private key
func (p *PrivateKeyImpl) Unmarshal(data []byte) error {
	if len(data) != 32 {
		return fmt.Errorf("invalid private key length: expected 32 bytes, got %d", len(data))
	}

	key, err := ethcrypto.ToECDSA(data)
	if err != nil {
		return fmt.Errorf("failed to unmarshal private key: %w", err)
	}

	p.key = key
	return nil
}

// Equal checks if two private keys are equal (constant time comparison)
func (p *PrivateKeyImpl) Equal(other PrivateKey) bool {
	if other == nil {
		return p.key == nil
	}

	// Get bytes from both keys
	pBytes := p.Bytes()
	oBytes := other.Bytes()

	if pBytes == nil || oBytes == nil {
		return pBytes == nil && oBytes == nil
	}

	if len(pBytes) != len(oBytes) {
		return false
	}

	// Constant-time comparison
	var result byte
	for i := 0; i < len(pBytes); i++ {
		result |= pBytes[i] ^ oBytes[i]
	}

	return result == 0
}

// Zeroize securely clears the private key from memory
func (p *PrivateKeyImpl) Zeroize() {
	if p.key != nil && p.key.D != nil {
		p.key.D.SetInt64(0)
		p.key = nil
	}
}

// FromECDSA creates a PrivateKey from Go's native ECDSA private key
func FromECDSA(key *ecdsa.PrivateKey) PrivateKey {
	if key == nil {
		return nil
	}
	return &PrivateKeyImpl{key: key}
}

// PrivateKeyFromString creates a private key from a hex string (with or without 0x prefix)
func PrivateKeyFromString(hexKey string) (PrivateKey, error) {
	return NewPrivateKeyFromHex(hexKey)
}

// IsValidPrivateKey checks if a byte slice is a valid private key
func IsValidPrivateKey(keyData []byte) bool {
	if len(keyData) != 32 {
		return false
	}

	// Check if key is in valid range [1, n-1]
	d := new(big.Int).SetBytes(keyData)
	n := secp256k1.S256().Params().N

	return d.Cmp(big.NewInt(0)) > 0 && d.Cmp(n) < 0
}

// Helper function to validate private key before operations
func (p *PrivateKeyImpl) validate() error {
	if p.key == nil {
		return fmt.Errorf("private key is nil")
	}
	if p.key.D == nil {
		return fmt.Errorf("private key D value is nil")
	}

	// Validate key is in valid range
	n := secp256k1.S256().Params().N
	if p.key.D.Cmp(big.NewInt(0)) <= 0 || p.key.D.Cmp(n) >= 0 {
		return fmt.Errorf("private key out of valid range")
	}

	return nil
}

// REMOVED: All Ed25519-specific code
// REMOVED: Any Blake2b hashing
// CHANGED: Sign() now returns (Signature, error) instead of just Signature
// ADDED: Proper error handling throughout
// ADDED: Zeroize() for secure memory cleanup
// ADDED: Validation methods
// ADDED: Helper constructors (FromECDSA, PrivateKeyFromString)
