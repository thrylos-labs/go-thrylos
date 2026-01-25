// crypto/hash/hash.go
// Package hash provides Ethereum-compatible hashing (Keccak256) for Thrylos
//
// This implementation uses Keccak256 exclusively for full Ethereum/MetaMask compatibility.
// All Blake2b functions have been removed as part of the cryptography standardization.
package hash

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"sync"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

const HashSize = 32

// Hash represents a 32-byte Keccak256 hash
type Hash [HashSize]byte

// ============================================================================
// Core Hash Functions
// ============================================================================

// NullHash returns a zero-valued hash
func NullHash() Hash {
	return Hash{}
}

// NewHash creates a Keccak256 hash from data (Ethereum standard)
func NewHash(data []byte) Hash {
	hashBytes := ethcrypto.Keccak256(data)
	var hash Hash
	copy(hash[:], hashBytes)
	return hash
}

// Keccak256 computes the Keccak256 hash of data
// This is an alias for NewHash but returns []byte for compatibility
func Keccak256(data []byte) []byte {
	return ethcrypto.Keccak256(data)
}

// Keccak256Hash computes Keccak256 and returns Hash type
func Keccak256Hash(data []byte) Hash {
	return NewHash(data)
}

// ============================================================================
// Hash Creation and Parsing
// ============================================================================

// FromString creates a Hash from a hex string (with or without 0x prefix)
func FromString(str string) (Hash, error) {
	// Remove 0x prefix if present
	if len(str) >= 2 && str[:2] == "0x" {
		str = str[2:]
	}

	data, err := hex.DecodeString(str)
	if err != nil {
		return Hash{}, fmt.Errorf("invalid hex string: %w", err)
	}

	return FromBytes(data)
}

// FromBytes creates a Hash from bytes
func FromBytes(data []byte) (Hash, error) {
	if len(data) != HashSize {
		return Hash{}, fmt.Errorf("hash must be %d bytes, got %d", HashSize, len(data))
	}

	var h Hash
	copy(h[:], data)
	return h, nil
}

// MustFromString creates a Hash from a hex string, panics on error
func MustFromString(str string) Hash {
	h, err := FromString(str)
	if err != nil {
		panic(fmt.Sprintf("invalid hash string: %v", err))
	}
	return h
}

// MustFromBytes creates a Hash from bytes, panics on error
func MustFromBytes(data []byte) Hash {
	h, err := FromBytes(data)
	if err != nil {
		panic(fmt.Sprintf("invalid hash bytes: %v", err))
	}
	return h
}

// ============================================================================
// Hash Methods
// ============================================================================

// String returns the hex string representation of the hash (without 0x prefix)
func (h Hash) String() string {
	return hex.EncodeToString(h[:])
}

// Hex returns the hash as a 0x-prefixed hex string (Ethereum format)
func (h Hash) Hex() string {
	return "0x" + h.String()
}

// Bytes returns the byte slice representation of the hash
func (h Hash) Bytes() []byte {
	return h[:]
}

// Equal checks if two hashes are identical
func (h Hash) Equal(other Hash) bool {
	return bytes.Equal(h[:], other[:])
}

// IsZero checks if the hash is all zeros
func (h Hash) IsZero() bool {
	for _, b := range h {
		if b != 0 {
			return false
		}
	}
	return true
}

// ShortString returns a shortened version of the hash for display (first 8 chars)
func (h Hash) ShortString() string {
	if h.IsZero() {
		return "0x0000..."
	}
	fullHex := h.String()
	if len(fullHex) < 8 {
		return "0x" + fullHex
	}
	return "0x" + fullHex[:8] + "..."
}

// Copy creates a copy of the hash
func (h Hash) Copy() Hash {
	var copy Hash
	copy = h
	return copy
}

// SetBytes sets the hash to the given bytes
func (h *Hash) SetBytes(b []byte) error {
	if len(b) != HashSize {
		return fmt.Errorf("invalid hash size: got %d, want %d", len(b), HashSize)
	}
	copy(h[:], b)
	return nil
}

// MarshalText implements encoding.TextMarshaler
func (h Hash) MarshalText() ([]byte, error) {
	return []byte(h.Hex()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler
func (h *Hash) UnmarshalText(text []byte) error {
	hash, err := FromString(string(text))
	if err != nil {
		return err
	}
	*h = hash
	return nil
}

// ============================================================================
// Batch Hashing Functions
// ============================================================================

// HashData computes the Keccak256 hash of data
func HashData(data []byte) ([]byte, error) {
	return ethcrypto.Keccak256(data), nil
}

// HashMultiple hashes multiple data slices efficiently
// All data is concatenated and hashed together
func HashMultiple(data ...[]byte) Hash {
	if len(data) == 0 {
		return NewHash([]byte{})
	}

	// Concatenate all data
	var combined []byte
	for _, d := range data {
		combined = append(combined, d...)
	}
	return NewHash(combined)
}

// HashChain hashes data with itself multiple times
// Useful for computational proofs
func HashChain(data []byte, iterations int) Hash {
	if iterations < 1 {
		return NewHash(data)
	}

	current := NewHash(data)
	for i := 1; i < iterations; i++ {
		current = NewHash(current[:])
	}
	return current
}

// ============================================================================
// Performance-Optimized Hashing
// ============================================================================

// Keccak256State pool for efficient reuse
var keccakStatePool = sync.Pool{
	New: func() interface{} {
		return ethcrypto.NewKeccakState()
	},
}

// HashDataWithPool uses a pooled Keccak hasher for better performance
// Use this for high-frequency hashing operations
func HashDataWithPool(data []byte) ([]byte, error) {
	state := keccakStatePool.Get().(ethcrypto.KeccakState)
	defer keccakStatePool.Put(state)

	state.Reset()
	state.Write(data)

	result := make([]byte, HashSize)
	state.Read(result)
	return result, nil
}

// HashWithPool creates a Hash using the pooled hasher
func HashWithPool(data []byte) (Hash, error) {
	hashBytes, err := HashDataWithPool(data)
	if err != nil {
		return Hash{}, err
	}
	return FromBytes(hashBytes)
}

// ============================================================================
// Advanced Hash Operations
// ============================================================================

// DoubleHash computes Keccak256(Keccak256(data))
// Used in some blockchain protocols for extra security
func DoubleHash(data []byte) Hash {
	first := ethcrypto.Keccak256(data)
	return NewHash(first)
}

// CombineHashes combines multiple hashes into one
// Order matters: Hash(h1 || h2) != Hash(h2 || h1)
func CombineHashes(hashes ...Hash) Hash {
	if len(hashes) == 0 {
		return NullHash()
	}

	var combined []byte
	for _, h := range hashes {
		combined = append(combined, h[:]...)
	}
	return NewHash(combined)
}

// XORHashes performs XOR operation on two hashes
// Useful for commitment schemes
func XORHashes(a, b Hash) Hash {
	var result Hash
	for i := 0; i < HashSize; i++ {
		result[i] = a[i] ^ b[i]
	}
	return result
}

// ============================================================================
// Merkle Tree Functions
// ============================================================================

// MerkleParent computes the parent hash of two child hashes
// Standard Merkle tree operation: parent = Hash(left || right)
func MerkleParent(left, right Hash) Hash {
	combined := make([]byte, 0, HashSize*2)
	combined = append(combined, left[:]...)
	combined = append(combined, right[:]...)
	return NewHash(combined)
}

// MerkleRoot computes the Merkle root of a list of hashes
// If odd number of hashes, the last one is duplicated
func MerkleRoot(hashes []Hash) Hash {
	if len(hashes) == 0 {
		return NullHash()
	}
	if len(hashes) == 1 {
		return hashes[0]
	}

	// Build tree level by level
	currentLevel := make([]Hash, len(hashes))
	copy(currentLevel, hashes)

	for len(currentLevel) > 1 {
		nextLevel := make([]Hash, 0, (len(currentLevel)+1)/2)

		for i := 0; i < len(currentLevel); i += 2 {
			if i+1 < len(currentLevel) {
				// Pair exists
				parent := MerkleParent(currentLevel[i], currentLevel[i+1])
				nextLevel = append(nextLevel, parent)
			} else {
				// Odd number, duplicate last hash
				parent := MerkleParent(currentLevel[i], currentLevel[i])
				nextLevel = append(nextLevel, parent)
			}
		}

		currentLevel = nextLevel
	}

	return currentLevel[0]
}

// MerkleRootFromBytes computes Merkle root from byte slices
// Convenience function that hashes data first
func MerkleRootFromBytes(data [][]byte) Hash {
	if len(data) == 0 {
		return NullHash()
	}

	// Hash all data
	hashes := make([]Hash, len(data))
	for i, d := range data {
		hashes[i] = NewHash(d)
	}

	return MerkleRoot(hashes)
}

// ============================================================================
// Empty/Zero Hash Utilities
// ============================================================================

// EmptyHash returns the hash of empty data
func EmptyHash() Hash {
	return NewHash([]byte{})
}

// ZeroHash returns a zero-valued hash (all zeros)
func ZeroHash() Hash {
	return NullHash()
}

// IsEmptyHash checks if the hash equals Hash([]byte{})
func IsEmptyHash(h Hash) bool {
	return h.Equal(EmptyHash())
}

// ============================================================================
// REMOVED: All Blake2b Functions
// ============================================================================

// LegacyBlake2bHash panics if called - forces migration to Keccak256
// REMOVED: Blake2b is no longer supported
func LegacyBlake2bHash(data []byte) Hash {
	panic("FATAL: Blake2b has been removed from Thrylos. All code must use Keccak256 (NewHash/Keccak256Hash). " +
		"This is part of the Secp256k1+Keccak256 standardization. " +
		"Please update your code to use NewHash() instead.")
}

// Blake2bHash panics if called - guards against accidental use
func Blake2bHash(data []byte) Hash {
	panic("FATAL: Blake2b is no longer supported. Use NewHash() or Keccak256Hash() instead.")
}

// HashDataBlake2b panics if called - guards against accidental use
func HashDataBlake2b(data []byte) ([]byte, error) {
	panic("FATAL: Blake2b is no longer supported. Use HashData() (Keccak256) instead.")
}

// ============================================================================
// Performance Monitoring (Optional)
// ============================================================================

var (
	hashMetrics = struct {
		sync.RWMutex
		totalHashes  uint64
		pooledHashes uint64
		directHashes uint64
	}{}
)

// IncrementHashMetrics increments hash operation counters
func IncrementHashMetrics(pooled bool) {
	hashMetrics.Lock()
	defer hashMetrics.Unlock()

	hashMetrics.totalHashes++
	if pooled {
		hashMetrics.pooledHashes++
	} else {
		hashMetrics.directHashes++
	}
}

// GetHashMetrics returns performance metrics for hash operations
func GetHashMetrics() map[string]uint64 {
	hashMetrics.RLock()
	defer hashMetrics.RUnlock()

	return map[string]uint64{
		"total_hashes":  hashMetrics.totalHashes,
		"pooled_hashes": hashMetrics.pooledHashes,
		"direct_hashes": hashMetrics.directHashes,
	}
}

// ResetHashMetrics resets performance metrics
func ResetHashMetrics() {
	hashMetrics.Lock()
	defer hashMetrics.Unlock()

	hashMetrics.totalHashes = 0
	hashMetrics.pooledHashes = 0
	hashMetrics.directHashes = 0
}

// ============================================================================
// Utility Functions
// ============================================================================

// HashToUint64 converts a hash to a uint64 (for random selection, etc.)
func HashToUint64(h Hash) uint64 {
	// Use first 8 bytes
	return uint64(h[0]) | uint64(h[1])<<8 | uint64(h[2])<<16 | uint64(h[3])<<24 |
		uint64(h[4])<<32 | uint64(h[5])<<40 | uint64(h[6])<<48 | uint64(h[7])<<56
}

// HashFromUint64 creates a hash from a uint64 (for testing)
func HashFromUint64(n uint64) Hash {
	var h Hash
	h[0] = byte(n)
	h[1] = byte(n >> 8)
	h[2] = byte(n >> 16)
	h[3] = byte(n >> 24)
	h[4] = byte(n >> 32)
	h[5] = byte(n >> 40)
	h[6] = byte(n >> 48)
	h[7] = byte(n >> 56)
	return h
}

// CompareHashes compares two hashes lexicographically
// Returns: -1 if a < b, 0 if a == b, 1 if a > b
func CompareHashes(a, b Hash) int {
	return bytes.Compare(a[:], b[:])
}

// MinHash returns the smaller of two hashes
func MinHash(a, b Hash) Hash {
	if CompareHashes(a, b) <= 0 {
		return a
	}
	return b
}

// MaxHash returns the larger of two hashes
func MaxHash(a, b Hash) Hash {
	if CompareHashes(a, b) >= 0 {
		return a
	}
	return b
}

// ============================================================================
// Constants for Common Hashes
// ============================================================================

var (
	// EmptyHashValue is the hash of empty data
	EmptyHashValue = NewHash([]byte{})

	// ZeroHashValue is all zeros
	ZeroHashValue = Hash{}
)

// ============================================================================
// MIGRATION NOTES
// ============================================================================
//
// This file has been updated as part of Thrylos cryptography standardization:
//
// REMOVED:
// - All Blake2b functions (blake2b.Sum256, etc.)
// - Dual-crypto compatibility code
// - Legacy hash migration helpers
//
// ADDED:
// - Panic guards for Blake2b functions (forces migration)
// - Keccak256-only implementation
// - Better performance with pooled hashers
// - More utility functions
//
// CHANGED:
// - All hashing now uses Keccak256 exclusively
// - Better error messages
// - Improved documentation
//
// If you see panics about Blake2b, update your code to use:
// - NewHash(data) instead of blake2b.Sum256(data)
// - Keccak256(data) instead of HashDataBlake2b(data)
// - HashMultiple(...) for multiple data items
//
// ============================================================================
