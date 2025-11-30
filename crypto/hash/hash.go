package hash

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"hash"
	"log"
	"sync"

	"golang.org/x/crypto/blake2b"
)

const HashSize = 32

type Hash [HashSize]byte

func NullHash() Hash {
	return Hash{}
}
func NewHash(data []byte) Hash {
	h := blake2b.Sum256(data)
	var hash Hash
	copy(hash[:], h[:HashSize])
	return hash
}

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

func FromBytes(data []byte) (Hash, error) {
	if len(data) != HashSize {
		return Hash{}, fmt.Errorf("Hash should be %d bytes, but it is %v bytes", HashSize, len(data))
	}
	var h Hash
	copy(h[:], data[:HashSize])
	return h, nil
}

func (h *Hash) String() string {
	return hex.EncodeToString(h[:])
}

func (h *Hash) Bytes() []byte {
	return h[:]
}

func (h *Hash) Equal(other Hash) bool {
	return bytes.Equal(h[:], other[:])
}

// // Initialize a cache with a mutex for concurrent access control
var (
	addressCache = make(map[string]string)
	cacheMutex   sync.RWMutex
)

// Use a global hash pool for BLAKE2b hashers to reduce allocation overhead
var blake2bHasherPool = sync.Pool{
	New: func() interface{} {
		hasher, err := blake2b.New256(nil)
		if err != nil {
			log.Printf("ERROR: Cannot initialize BLAKE2b hasher: %v", err)
			// Return nil and handle at call site
			return nil
		}
		return hasher
	},
}

func HashData(data []byte) ([]byte, error) {
	hasher := blake2bHasherPool.Get()
	if hasher == nil {
		// Emergency fallback - try creating new hasher
		h, err := blake2b.New256(nil)
		if err != nil {
			return nil, fmt.Errorf("critical: cannot create hasher: %v", err)
		}
		hasher = h
	}
	h := hasher.(hash.Hash)
	defer blake2bHasherPool.Put(h)
	h.Reset()
	h.Write(data)
	return h.Sum(nil), nil
}
