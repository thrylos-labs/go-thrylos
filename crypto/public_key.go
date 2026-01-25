// crypto/public_key.go
package crypto

import (
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"

	"github.com/ethereum/go-ethereum/crypto/secp256k1"
	"github.com/thrylos-labs/go-thrylos/crypto/address"
	"github.com/thrylos-labs/go-thrylos/crypto/hash"
)

// PublicKeyImpl implements the PublicKey interface for Secp256k1
type PublicKeyImpl struct {
	pubKey *ecdsa.PublicKey
}

// Ensure PublicKeyImpl implements PublicKey interface
var _ PublicKey = (*PublicKeyImpl)(nil)

// NewPublicKeyFromBytes creates a public key from bytes
// Accepts both compressed (33 bytes) and uncompressed (65 bytes) formats
func NewPublicKeyFromBytes(data []byte) (PublicKey, error) {
	switch len(data) {
	case 33:
		// Compressed format (0x02 or 0x03 prefix)
		return newPublicKeyFromCompressed(data)
	case 65:
		// Uncompressed format (0x04 prefix)
		return newPublicKeyFromUncompressed(data)
	default:
		return nil, fmt.Errorf("invalid public key length: expected 33 or 65 bytes, got %d", len(data))
	}
}

// newPublicKeyFromCompressed parses a 33-byte compressed public key
func newPublicKeyFromCompressed(data []byte) (PublicKey, error) {
	if len(data) != 33 {
		return nil, fmt.Errorf("compressed public key must be 33 bytes")
	}

	// Verify prefix is 0x02 or 0x03
	if data[0] != 0x02 && data[0] != 0x03 {
		return nil, fmt.Errorf("invalid compressed public key prefix: 0x%02x", data[0])
	}

	// Decompress using go-ethereum
	pubKey, err := ethcrypto.DecompressPubkey(data)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress public key: %w", err)
	}

	return &PublicKeyImpl{pubKey: pubKey}, nil
}

// newPublicKeyFromUncompressed parses a 65-byte uncompressed public key
func newPublicKeyFromUncompressed(data []byte) (PublicKey, error) {
	if len(data) != 65 {
		return nil, fmt.Errorf("uncompressed public key must be 65 bytes")
	}

	// Verify prefix is 0x04
	if data[0] != 0x04 {
		return nil, fmt.Errorf("invalid uncompressed public key prefix: expected 0x04, got 0x%02x", data[0])
	}

	// Parse using go-ethereum
	pubKey, err := ethcrypto.UnmarshalPubkey(data)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal public key: %w", err)
	}

	return &PublicKeyImpl{pubKey: pubKey}, nil
}

// NewPublicKeyFromHex creates a public key from a hex-encoded string
func NewPublicKeyFromHex(hexKey string) (PublicKey, error) {
	// Remove 0x prefix if present
	if len(hexKey) > 2 && hexKey[:2] == "0x" {
		hexKey = hexKey[2:]
	}

	data, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("invalid hex encoding: %w", err)
	}

	return NewPublicKeyFromBytes(data)
}

// Bytes returns the compressed public key (33 bytes)
// Format: [prefix (1 byte) || X coordinate (32 bytes)]
// Prefix is 0x02 if Y is even, 0x03 if Y is odd
func (p *PublicKeyImpl) Bytes() []byte {
	if p.pubKey == nil {
		return nil
	}
	return ethcrypto.CompressPubkey(p.pubKey)
}

// BytesUncompressed returns the uncompressed public key (65 bytes)
// Format: [0x04 || X coordinate (32 bytes) || Y coordinate (32 bytes)]
func (p *PublicKeyImpl) BytesUncompressed() []byte {
	if p.pubKey == nil {
		return nil
	}
	return ethcrypto.FromECDSAPub(p.pubKey)
}

// String returns the hex-encoded compressed public key with 0x prefix
func (p *PublicKeyImpl) String() string {
	if p.pubKey == nil {
		return "PublicKey(nil)"
	}
	return "0x" + hex.EncodeToString(p.Bytes())
}

// StringUncompressed returns the hex-encoded uncompressed public key with 0x prefix
func (p *PublicKeyImpl) StringUncompressed() string {
	if p.pubKey == nil {
		return "PublicKey(nil)"
	}
	return "0x" + hex.EncodeToString(p.BytesUncompressed())
}

// Address derives the Ethereum-style address (20 bytes)
// Address = Keccak256(uncompressed_pubkey[1:])[12:]
// NOTE: This method does NOT return an error to match the interface
func (p *PublicKeyImpl) Address() *address.Address {
	if p.pubKey == nil {
		return address.NullAddress()
	}

	// Use go-ethereum's address derivation (it handles Keccak256 internally)
	ethAddr := ethcrypto.PubkeyToAddress(*p.pubKey)

	// Convert to our Address type
	return address.FromEthereumAddress(ethAddr)
}

// Verify calculates Keccak256 hash of data and verifies signature
func (p *PublicKeyImpl) Verify(data []byte, sig Signature) error {
	if p.pubKey == nil {
		return fmt.Errorf("cannot verify with nil public key")
	}
	if sig == nil {
		return fmt.Errorf("signature is nil")
	}

	// Hash the data
	hash := hash.Keccak256(data)

	// Verify the hash
	return p.VerifyHash(hash, sig)
}

// VerifyHash verifies signature against a pre-calculated 32-byte Keccak256 hash
// This is the preferred method to avoid double-hashing
func (p *PublicKeyImpl) VerifyHash(hash []byte, sig Signature) error {
	if p.pubKey == nil {
		return fmt.Errorf("cannot verify with nil public key")
	}
	if sig == nil {
		return fmt.Errorf("signature is nil")
	}
	if len(hash) != 32 {
		return fmt.Errorf("hash must be 32 bytes, got %d", len(hash))
	}

	sigBytes := sig.Bytes()
	if len(sigBytes) != 65 {
		return fmt.Errorf("signature must be 65 bytes, got %d", len(sigBytes))
	}

	// Recover the public key from the signature
	recoveredPubKey, err := ethcrypto.SigToPub(hash, sigBytes)
	if err != nil {
		return fmt.Errorf("failed to recover public key from signature: %w", err)
	}

	// Compare the recovered public key with our public key
	if recoveredPubKey.X.Cmp(p.pubKey.X) != 0 || recoveredPubKey.Y.Cmp(p.pubKey.Y) != 0 {
		return fmt.Errorf("signature verification failed: public key mismatch")
	}

	// Additional verification using go-ethereum's VerifySignature
	pubKeyBytes := p.BytesUncompressed()
	// VerifySignature expects signature without the recovery byte
	sigWithoutRecovery := sigBytes[:64]

	if !ethcrypto.VerifySignature(pubKeyBytes, hash, sigWithoutRecovery) {
		return fmt.Errorf("signature verification failed")
	}

	return nil
}

// ToECDSA converts to Go's native ECDSA public key
func (p *PublicKeyImpl) ToECDSA() *ecdsa.PublicKey {
	return p.pubKey
}

// Marshal serializes the public key to compressed format (33 bytes)
func (p *PublicKeyImpl) Marshal() ([]byte, error) {
	if p.pubKey == nil {
		return nil, fmt.Errorf("cannot marshal nil public key")
	}
	return p.Bytes(), nil
}

// Unmarshal deserializes a public key from bytes
// Accepts both compressed (33 bytes) and uncompressed (65 bytes)
func (p *PublicKeyImpl) Unmarshal(data []byte) error {
	pubKey, err := NewPublicKeyFromBytes(data)
	if err != nil {
		return err
	}

	impl, ok := pubKey.(*PublicKeyImpl)
	if !ok {
		return fmt.Errorf("invalid public key type")
	}

	p.pubKey = impl.pubKey
	return nil
}

// Equal checks if two public keys are equal
func (p *PublicKeyImpl) Equal(other PublicKey) bool {
	if other == nil {
		return p.pubKey == nil
	}

	// Get bytes from both keys (using compressed format for comparison)
	pBytes := p.Bytes()
	oBytes := other.Bytes()

	if pBytes == nil || oBytes == nil {
		return pBytes == nil && oBytes == nil
	}

	if len(pBytes) != len(oBytes) {
		return false
	}

	// Byte-by-byte comparison
	for i := 0; i < len(pBytes); i++ {
		if pBytes[i] != oBytes[i] {
			return false
		}
	}

	return true
}

// IsOnCurve verifies that the public key point is on the secp256k1 curve
func (p *PublicKeyImpl) IsOnCurve() bool {
	if p.pubKey == nil {
		return false
	}
	return secp256k1.S256().IsOnCurve(p.pubKey.X, p.pubKey.Y)
}

// RecoverPublicKey recovers the public key from a signature and hash
// This is a package-level function that creates a new PublicKey
func RecoverPublicKey(hash []byte, sig Signature) (PublicKey, error) {
	if len(hash) != 32 {
		return nil, fmt.Errorf("hash must be 32 bytes, got %d", len(hash))
	}
	if sig == nil {
		return nil, fmt.Errorf("signature is nil")
	}

	sigBytes := sig.Bytes()
	if len(sigBytes) != 65 {
		return nil, fmt.Errorf("signature must be 65 bytes, got %d", len(sigBytes))
	}

	// Recover public key using go-ethereum
	pubKey, err := ethcrypto.SigToPub(hash, sigBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to recover public key: %w", err)
	}

	return &PublicKeyImpl{pubKey: pubKey}, nil
}

// FromECDSAPublicKey creates a PublicKey from Go's native ECDSA public key
func FromECDSAPublicKey(pubKey *ecdsa.PublicKey) PublicKey {
	if pubKey == nil {
		return nil
	}
	return &PublicKeyImpl{pubKey: pubKey}
}

// IsValidPublicKey checks if bytes represent a valid public key
func IsValidPublicKey(data []byte) bool {
	_, err := NewPublicKeyFromBytes(data)
	return err == nil
}

// CompressPublicKey converts an uncompressed public key to compressed format
func CompressPublicKey(uncompressed []byte) ([]byte, error) {
	if len(uncompressed) != 65 {
		return nil, fmt.Errorf("uncompressed public key must be 65 bytes")
	}

	pubKey, err := NewPublicKeyFromBytes(uncompressed)
	if err != nil {
		return nil, err
	}

	return pubKey.Bytes(), nil
}

// DecompressPublicKey converts a compressed public key to uncompressed format
func DecompressPublicKey(compressed []byte) ([]byte, error) {
	if len(compressed) != 33 {
		return nil, fmt.Errorf("compressed public key must be 33 bytes")
	}

	pubKey, err := NewPublicKeyFromBytes(compressed)
	if err != nil {
		return nil, err
	}

	return pubKey.BytesUncompressed(), nil
}
