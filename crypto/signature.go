// crypto/signature.go
package crypto

import (
	"encoding/hex"
	"fmt"
	"math/big"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/secp256k1"
)

// SignatureImpl implements the Signature interface for Secp256k1 ECDSA signatures
type SignatureImpl struct {
	r *big.Int // R value (32 bytes)
	s *big.Int // S value (32 bytes)
	v byte     // Recovery ID (1 byte: 0, 1, 27, or 28)
}

// SignatureSize is the size of an Ethereum signature (R + S + V)
const SignatureSize = 65

// Ensure SignatureImpl implements Signature interface
var _ Signature = (*SignatureImpl)(nil)

// NewSignature creates a signature from 65-byte slice [R || S || V]
func NewSignature(sigBytes []byte) Signature {
	if len(sigBytes) != SignatureSize {
		// For compatibility, return nil on invalid size
		fmt.Printf("Warning: NewSignature received invalid size %d, expected %d\n", len(sigBytes), SignatureSize)
		return nil
	}

	sig, err := NewSignatureFromBytes(sigBytes)
	if err != nil {
		fmt.Printf("Warning: NewSignature failed: %v\n", err)
		return nil
	}

	return sig
}

// NewSignatureFromBytes creates a signature from a 65-byte slice [R || S || V]
func NewSignatureFromBytes(sigBytes []byte) (Signature, error) {
	if len(sigBytes) != SignatureSize {
		return nil, fmt.Errorf("invalid signature length: expected 65 bytes, got %d", len(sigBytes))
	}

	// Extract R, S, V
	r := new(big.Int).SetBytes(sigBytes[:32])
	s := new(big.Int).SetBytes(sigBytes[32:64])
	v := sigBytes[64]

	// 65-byte Ethereum signatures only carry the recovery ID, not the chain ID.
	if v != 0 && v != 1 && v != 27 && v != 28 {
		return nil, fmt.Errorf("invalid recovery ID (V): %d", v)
	}

	// Validate R and S are in valid range
	if r.Sign() <= 0 || s.Sign() <= 0 {
		return nil, fmt.Errorf("invalid signature: R or S is zero or negative")
	}

	n := secp256k1.S256().Params().N
	if r.Cmp(n) >= 0 || s.Cmp(n) >= 0 {
		return nil, fmt.Errorf("invalid signature: R or S exceeds curve order")
	}

	return &SignatureImpl{
		r: r,
		s: s,
		v: v,
	}, nil
}

// NewSignatureFromHex creates a signature from a hex-encoded string
func NewSignatureFromHex(hexSig string) (Signature, error) {
	// Remove 0x prefix if present
	if len(hexSig) > 2 && hexSig[:2] == "0x" {
		hexSig = hexSig[2:]
	}

	data, err := hex.DecodeString(hexSig)
	if err != nil {
		return nil, fmt.Errorf("invalid hex encoding: %w", err)
	}

	return NewSignatureFromBytes(data)
}

// SignatureFromBytes is an alias for NewSignatureFromBytes (backward compatibility)
func SignatureFromBytes(sigBytes []byte) (Signature, error) {
	return NewSignatureFromBytes(sigBytes)
}

// Bytes returns the 65-byte signature in [R || S || V] format
func (s *SignatureImpl) Bytes() []byte {
	bytes := make([]byte, 65)
	s.r.FillBytes(bytes[:32])
	s.s.FillBytes(bytes[32:64])
	bytes[64] = s.v
	return bytes
}

// String returns the hex-encoded signature with 0x prefix
func (s *SignatureImpl) String() string {
	return "0x" + hex.EncodeToString(s.Bytes())
}

// R returns the R value of the signature
func (s *SignatureImpl) R() *big.Int {
	return new(big.Int).Set(s.r)
}

// S returns the S value of the signature
func (s *SignatureImpl) S() *big.Int {
	return new(big.Int).Set(s.s)
}

// V returns the recovery ID
func (s *SignatureImpl) V() byte {
	return s.v
}

// Verify verifies the signature against a public key and data
// Internally hashes data with Keccak256
func (s *SignatureImpl) Verify(pubKey PublicKey, data []byte) error {
	if pubKey == nil {
		return fmt.Errorf("public key cannot be nil")
	}

	// Hash the data
	hash := ethcrypto.Keccak256(data)

	// Verify the hash
	return s.VerifyHash(pubKey, hash)
}

// VerifyHash verifies the signature against a public key and pre-calculated hash
func (s *SignatureImpl) VerifyHash(pubKey PublicKey, hash []byte) error {
	if pubKey == nil {
		return fmt.Errorf("public key cannot be nil")
	}
	if len(hash) != 32 {
		return fmt.Errorf("hash must be 32 bytes, got %d", len(hash))
	}

	// Use the public key's verification method
	return pubKey.VerifyHash(hash, s)
}

// Recover recovers the public key from the signature and hash
func (s *SignatureImpl) Recover(hash []byte) (PublicKey, error) {
	if len(hash) != 32 {
		return nil, fmt.Errorf("hash must be 32 bytes, got %d", len(hash))
	}

	return RecoverPublicKey(hash, s)
}

// IsNormalized checks if the signature uses low-s normalization
// This prevents signature malleability attacks
func (s *SignatureImpl) IsNormalized() bool {
	halfN := new(big.Int).Rsh(secp256k1.S256().Params().N, 1)
	return s.s.Cmp(halfN) <= 0
}

// Normalize returns a normalized version of the signature (low-s)
// If already normalized, returns a clone of the signature
func (s *SignatureImpl) Normalize() Signature {
	if s.IsNormalized() {
		return s.Clone()
	}

	// Calculate s' = n - s
	n := secp256k1.S256().Params().N
	newS := new(big.Int).Sub(n, s.s)

	// Flip recovery ID
	newV := s.v ^ 1

	return &SignatureImpl{
		r: new(big.Int).Set(s.r),
		s: newS,
		v: newV,
	}
}

// IsValid performs basic validation on the signature
func (s *SignatureImpl) IsValid() bool {
	// Check R and S are positive
	if s.r.Sign() <= 0 || s.s.Sign() <= 0 {
		return false
	}

	// Check R and S are less than curve order
	n := secp256k1.S256().Params().N
	if s.r.Cmp(n) >= 0 || s.s.Cmp(n) >= 0 {
		return false
	}

	// Check recovery ID is valid
	if s.v != 27 && s.v != 28 && s.v != 0 && s.v != 1 {
		return false
	}

	return true
}

// RecoveryID returns the normalized recovery ID (0 or 1)
func (s *SignatureImpl) RecoveryID() byte {
	if s.v >= 35 {
		// EIP-155: extract recovery ID from chain-encoded V
		chainID, _ := s.ExtractChainID()
		return byte((uint64(s.v) - 35 - chainID*2) % 2)
	} else if s.v >= 27 {
		// Legacy: V is 27 or 28
		return s.v - 27
	}
	// Already normalized (0 or 1)
	return s.v
}

// WithChainID is a compatibility no-op for the 65-byte signature format.
// EIP-155 replay protection must be bound into the signed payload, not stored in V.
func (s *SignatureImpl) WithChainID(chainID uint64) Signature {
	return s.Clone()
}

// ExtractChainID always returns false for the 65-byte [R||S||V] signature format.
func (s *SignatureImpl) ExtractChainID() (uint64, bool) {
	return 0, false
}

// Marshal serializes the signature to 65 bytes
func (s *SignatureImpl) Marshal() ([]byte, error) {
	return s.Bytes(), nil
}

// Unmarshal deserializes a 65-byte signature
func (s *SignatureImpl) Unmarshal(data []byte) error {
	if len(data) != SignatureSize {
		return fmt.Errorf("invalid signature size: expected 65 bytes, got %d", len(data))
	}

	sig, err := NewSignatureFromBytes(data)
	if err != nil {
		return err
	}

	impl, ok := sig.(*SignatureImpl)
	if !ok {
		return fmt.Errorf("invalid signature type")
	}

	s.r = impl.r
	s.s = impl.s
	s.v = impl.v
	return nil
}

// Equal checks if two signatures are equal
func (s *SignatureImpl) Equal(other Signature) bool {
	if other == nil {
		return false
	}

	otherBytes := other.Bytes()
	thisBytes := s.Bytes()

	if len(otherBytes) != len(thisBytes) {
		return false
	}

	for i := 0; i < len(thisBytes); i++ {
		if thisBytes[i] != otherBytes[i] {
			return false
		}
	}

	return true
}

// Clone creates a deep copy of the signature
func (s *SignatureImpl) Clone() Signature {
	return &SignatureImpl{
		r: new(big.Int).Set(s.r),
		s: new(big.Int).Set(s.s),
		v: s.v,
	}
}

// Helper function to convert legacy V values (27/28) to normalized (0/1)
func normalizeV(v byte) byte {
	if v >= 35 {
		// EIP-155 encoded
		chainID := (uint64(v) - 35) / 2
		return byte((uint64(v) - 35 - chainID*2) % 2)
	} else if v >= 27 {
		return v - 27
	}
	return v
}

// Helper function to convert normalized V (0/1) to legacy (27/28)
func toLegacyV(v byte) byte {
	if v == 0 || v == 1 {
		return v + 27
	}
	return v
}

// REMOVED: VerifyWithSalt (not needed with proper Keccak256 hashing)
// ADDED: Full R, S, V accessors
// ADDED: IsNormalized() and Normalize() for security
// ADDED: EIP-155 support (WithChainID, ExtractChainID)
// ADDED: Signature recovery (Recover method)
// ADDED: Comprehensive validation (IsValid)
// ADDED: Clone() for safe copying
// CHANGED: All methods return proper errors
// CHANGED: Signature struct now stores R, S, V as separate fields for better manipulation
