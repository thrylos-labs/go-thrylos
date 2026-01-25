// crypto/interface.go
package crypto

import (
	"crypto/ecdsa"
	"math/big"

	"github.com/thrylos-labs/go-thrylos/crypto/address"
)

// PrivateKey represents a Secp256k1 private key (32 bytes)
type PrivateKey interface {
	// Bytes returns the 32-byte private key
	Bytes() []byte

	// String returns hex-encoded private key with 0x prefix
	String() string

	// Sign calculates a Keccak256 hash of msg and signs it
	// Returns 65-byte signature [R || S || V]
	Sign(msg []byte) (Signature, error)

	// SignHash signs a pre-calculated 32-byte Keccak256 hash
	// This is the preferred method to avoid double-hashing
	SignHash(hash []byte) (Signature, error)

	// PublicKey derives the corresponding Secp256k1 public key
	PublicKey() PublicKey

	// Address derives the Ethereum-style address (20 bytes)
	Address() *address.Address

	// ToECDSA converts to Go's native ECDSA private key
	ToECDSA() *ecdsa.PrivateKey

	// Marshal serializes the private key
	Marshal() ([]byte, error)

	// Unmarshal deserializes the private key
	Unmarshal([]byte) error

	// Equal checks if two private keys are equal (constant time)
	Equal(other PrivateKey) bool

	// Zeroize securely clears the private key from memory
	Zeroize()
}

// PublicKey represents a Secp256k1 public key
// Can be 33 bytes (compressed) or 65 bytes (uncompressed)
type PublicKey interface {
	// Bytes returns the compressed public key (33 bytes)
	Bytes() []byte

	// BytesUncompressed returns the uncompressed public key (65 bytes)
	BytesUncompressed() []byte

	// String returns hex-encoded compressed public key with 0x prefix
	String() string

	// Address derives the Ethereum-style address (Keccak256(pubkey[1:])[12:])
	Address() *address.Address

	// Verify calculates Keccak256 hash of data and verifies signature
	Verify(data []byte, signature Signature) error

	// VerifyHash verifies signature against a pre-calculated 32-byte Keccak256 hash
	// This is the preferred method to avoid double-hashing
	VerifyHash(hash []byte, signature Signature) error

	// ToECDSA converts to Go's native ECDSA public key
	ToECDSA() *ecdsa.PublicKey

	// Marshal serializes the public key
	Marshal() ([]byte, error)

	// Unmarshal deserializes the public key
	Unmarshal([]byte) error

	// Equal checks if two public keys are equal
	Equal(other PublicKey) bool

	// IsOnCurve verifies the public key is on the secp256k1 curve
	IsOnCurve() bool
}

// Signature represents a 65-byte ECDSA signature in Ethereum format [R || S || V]
// R and S are the ECDSA signature values (32 bytes each)
// V is the recovery ID (1 byte: 0, 1, 27, or 28)
type Signature interface {
	// Bytes returns the 65-byte signature [R || S || V]
	Bytes() []byte

	// String returns hex-encoded signature with 0x prefix
	String() string

	// R returns the R value of the signature
	R() *big.Int

	// S returns the S value of the signature
	S() *big.Int

	// V returns the recovery ID (0, 1, 27, or 28)
	V() byte

	// Verify verifies the signature against a public key and data
	// Internally hashes data with Keccak256
	Verify(pubKey PublicKey, data []byte) error

	// VerifyHash verifies the signature against a public key and pre-calculated hash
	VerifyHash(pubKey PublicKey, hash []byte) error

	// Recover recovers the public key from the signature and hash
	Recover(hash []byte) (PublicKey, error)

	// IsNormalized checks if the signature uses low-s normalization
	// (prevents signature malleability)
	IsNormalized() bool

	// Normalize returns a normalized version of the signature (low-s)
	Normalize() Signature

	// IsValid performs basic validation on the signature
	IsValid() bool

	// WithChainID applies EIP-155 chain ID encoding to the recovery ID
	// v = chainID * 2 + 35 + {0, 1}
	WithChainID(chainID uint64) Signature

	// ExtractChainID extracts the chain ID from an EIP-155 signature
	// Returns (chainID, hasChainID)
	ExtractChainID() (uint64, bool)

	// RecoveryID returns the normalized recovery ID (0 or 1)
	RecoveryID() byte

	// Marshal serializes the signature
	Marshal() ([]byte, error)

	// Unmarshal deserializes the signature
	Unmarshal([]byte) error

	// Equal checks if two signatures are equal
	Equal(other Signature) bool

	// Clone creates a deep copy of the signature
	Clone() Signature
}

// KeyGenerator provides key generation operations
type KeyGenerator interface {
	// GenerateKey creates a new random Secp256k1 private key
	GenerateKey() (PrivateKey, error)

	// PrivateKeyFromBytes creates a private key from 32 bytes
	PrivateKeyFromBytes(data []byte) (PrivateKey, error)

	// PrivateKeyFromHex creates a private key from hex string
	PrivateKeyFromHex(hex string) (PrivateKey, error)

	// PublicKeyFromBytes creates a public key from bytes
	// Accepts both compressed (33 bytes) and uncompressed (65 bytes)
	PublicKeyFromBytes(data []byte) (PublicKey, error)

	// PublicKeyFromHex creates a public key from hex string
	PublicKeyFromHex(hex string) (PublicKey, error)
}

// Signer combines private key operations
type Signer interface {
	// Sign signs a message and returns the signature
	Sign(msg []byte) (Signature, error)

	// SignHash signs a pre-calculated hash
	SignHash(hash []byte) (Signature, error)

	// PublicKey returns the public key
	PublicKey() PublicKey

	// Address returns the Ethereum address
	Address() *address.Address
}

// Verifier combines public key verification operations
type Verifier interface {
	// Verify verifies a signature against data
	Verify(data []byte, sig Signature) error

	// VerifyHash verifies a signature against a hash
	VerifyHash(hash []byte, sig Signature) error

	// RecoverSigner recovers the signer's public key
	RecoverSigner(hash []byte, sig Signature) (PublicKey, error)

	// Address returns the Ethereum address
	Address() *address.Address
}

// REMOVED: All Ed25519 interfaces and types
// REMOVED: Blake2b-specific functions
// REMOVED: VerifyWithSalt (not needed with Secp256k1 + proper hashing)

// MIGRATION NOTES:
// 1. Signature is now 65 bytes instead of 64 bytes
// 2. All signing/verification uses Keccak256 internally
// 3. Added public key recovery for Secp256k1
// 4. Added EIP-155 chain ID support for signatures
// 5. Removed VerifyWithSalt - use proper message hashing instead
// 6. Added signature normalization to prevent malleability
// 7. All methods now return errors for better error handling
