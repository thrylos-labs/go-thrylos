// Package hash provides Ethereum-compatible hashing (Keccak256) for Thrylos
//
// Updated to use Keccak256 throughout for full MetaMask compatibility.
// Previous BLAKE2b implementation preserved in comments for reference.
package hash

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/ethereum/go-ethereum/crypto"
)

const HashSize = 32

type Hash [HashSize]byte

// NullHash returns a zero-valued hash
func NullHash() Hash {
	return Hash{}
}

// NewHash creates a Keccak256 hash from data (Ethereum standard)
func NewHash(data []byte) Hash {
	hashBytes := crypto.Keccak256(data)
	var hash Hash
	copy(hash[:], hashBytes)
	return hash
}

// FromString creates a Hash from a hex string
func FromString(str string) (Hash, error) {
	data, err := hex.DecodeString(str)
	if err != nil {
		return Hash{}, err
	}
	if len(data) != HashSize {
		return Hash{}, fmt.Errorf("Hash should be %d bytes, but it is %v bytes", HashSize, len(data))
	}
	return FromBytes(data)
}

// FromBytes creates a Hash from bytes
func FromBytes(data []byte) (Hash, error) {
	if len(data) != HashSize {
		return Hash{}, fmt.Errorf("Hash should be %d bytes, but it is %v bytes", HashSize, len(data))
	}
	var h Hash
	copy(h[:], data[:HashSize])
	return h, nil
}

// String returns the hex string representation of the hash
func (h *Hash) String() string {
	return hex.EncodeToString(h[:])
}

// Bytes returns the byte slice representation of the hash
func (h *Hash) Bytes() []byte {
	return h[:]
}

// Equal checks if two hashes are identical
func (h *Hash) Equal(other Hash) bool {
	return bytes.Equal(h[:], other[:])
}

// HashData computes the Keccak256 hash of data
// This is the primary hash function used throughout Thrylos
func HashData(data []byte) ([]byte, error) {
	return crypto.Keccak256(data), nil
}

// HashMultiple hashes multiple data slices efficiently
// All data is concatenated and hashed together
func HashMultiple(data ...[]byte) Hash {
	// Concatenate all data
	var combined []byte
	for _, d := range data {
		combined = append(combined, d...)
	}
	return NewHash(combined)
}

// Keccak256State pool for efficient reuse
var keccakStatePool = sync.Pool{
	New: func() interface{} {
		return crypto.NewKeccakState()
	},
}

// HashDataWithPool uses a pooled Keccak hasher for better performance
// Use this for high-frequency hashing operations
func HashDataWithPool(data []byte) ([]byte, error) {
	state := keccakStatePool.Get().(crypto.KeccakState)
	defer keccakStatePool.Put(state)

	state.Reset()
	state.Write(data)

	result := make([]byte, HashSize)
	state.Read(result)
	return result, nil
}

// ============================================================================
// Backward Compatibility (Optional - Remove if not needed)
// ============================================================================

// If you have existing data hashed with BLAKE2b, you can add these functions
// to handle old hashes during migration. Remove this section if not needed.

/*
import "golang.org/x/crypto/blake2b"

// LegacyBlake2bHash computes BLAKE2b-256 hash for backward compatibility
// DEPRECATED: Use NewHash (Keccak256) for all new code
func LegacyBlake2bHash(data []byte) Hash {
	h := blake2b.Sum256(data)
	var hash Hash
	copy(hash[:], h[:])
	return hash
}

// IsLegacyHash checks if a hash might be from the old BLAKE2b system
// Returns true if the hash exists in your database but fails Keccak256 verification
func IsLegacyHash(hash Hash, data []byte) bool {
	// Compute both hashes
	keccak := NewHash(data)
	blake := LegacyBlake2bHash(data)

	// If Keccak doesn't match but BLAKE2b does, it's a legacy hash
	return !hash.Equal(keccak) && hash.Equal(blake)
}
*/

// ============================================================================
// Utility Functions
// ============================================================================

// IsZero checks if the hash is all zeros
func (h *Hash) IsZero() bool {
	for _, b := range h {
		if b != 0 {
			return false
		}
	}
	return true
}

// Hex returns the hash as a 0x-prefixed hex string (Ethereum format)
func (h *Hash) Hex() string {
	return "0x" + h.String()
}

// ShortString returns a shortened version of the hash for display (first 8 chars)
func (h *Hash) ShortString() string {
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
func (h *Hash) Copy() Hash {
	var copy Hash
	copy = *h
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
// Common Hash Operations
// ============================================================================

// DoubleHash computes Keccak256(Keccak256(data))
// Used in some blockchain protocols for extra security
func DoubleHash(data []byte) Hash {
	first := crypto.Keccak256(data)
	return NewHash(first)
}

// HashChain computes a hash chain: Hash(Hash(...Hash(data)))
// Used for proof-of-work or commitment schemes
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

// CombineHashes combines multiple hashes into one
// Order matters: Hash(h1 || h2) != Hash(h2 || h1)
func CombineHashes(hashes ...Hash) Hash {
	var combined []byte
	for _, h := range hashes {
		combined = append(combined, h[:]...)
	}
	return NewHash(combined)
}

// ============================================================================
// Merkle Tree Helpers
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
