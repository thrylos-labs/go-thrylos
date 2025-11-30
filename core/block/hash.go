package block

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/thrylos-labs/go-thrylos/crypto/hash"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// CanonicalBlockHash calculates the canonical hash of a block header + metadata.
// This is the single source of truth for block hashing across the codebase.
func CanonicalBlockHash(b *core.Block) (string, error) {
	if b == nil || b.Header == nil {
		return "", fmt.Errorf("cannot hash nil block or header")
	}

	var buf bytes.Buffer
	h := b.Header

	// Index
	indexBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(indexBytes, uint64(h.Index))
	buf.Write(indexBytes)

	// Previous hash
	buf.WriteString(h.PrevHash)

	// Timestamp
	tsBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(tsBytes, uint64(h.Timestamp))
	buf.Write(tsBytes)

	// Validator
	buf.WriteString(h.Validator)

	// Tx root
	buf.WriteString(h.TxRoot)

	// State root
	buf.WriteString(h.StateRoot)

	// Gas used
	gasUsedBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(gasUsedBytes, uint64(h.GasUsed))
	buf.Write(gasUsedBytes)

	// Gas limit
	gasLimitBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(gasLimitBytes, uint64(h.GasLimit))
	buf.Write(gasLimitBytes)

	hashBytes, err := hash.HashData(buf.Bytes())
	if err != nil {
		return "", fmt.Errorf("failed to hash block: %w", err)
	}

	return fmt.Sprintf("%x", hashBytes), nil
}
