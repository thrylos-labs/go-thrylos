package block

import (
	"bytes"
	"encoding/binary"
	"fmt"

	coremath "github.com/thrylos-labs/go-thrylos/core/math"
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

	if h.StateEncodingVersion >= 2 {
		versionBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(versionBytes, h.StateEncodingVersion)
		buf.Write(versionBytes)
	}

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

	slotBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(slotBytes, h.Slot)
	buf.Write(slotBytes)

	// Epoch
	epochBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(epochBytes, h.Epoch)
	buf.Write(epochBytes)

	if h.StateEncodingVersion >= 2 {
		totalFees, err := coremath.ParseUint256Compat(h.TotalFeesBytes, h.TotalFees)
		if err != nil {
			return "", fmt.Errorf("failed to parse total fees: %w", err)
		}
		totalFeesBytes, err := coremath.BigIntToUint256Bytes(totalFees)
		if err != nil {
			return "", fmt.Errorf("failed to encode total fees: %w", err)
		}
		totalFeeLen := make([]byte, 2)
		binary.BigEndian.PutUint16(totalFeeLen, uint16(len(totalFeesBytes)))
		buf.Write(totalFeeLen)
		buf.Write(totalFeesBytes)
	} else {
		// TotalFees
		buf.WriteString(h.TotalFees)
	}

	// MerkleRoot
	buf.WriteString(h.MerkleRoot)

	// VRF Output
	buf.Write(h.VrfOutput)

	// VRF Proof
	buf.Write(h.VrfProof)

	hashBytes, err := hash.HashData(buf.Bytes())
	if err != nil {
		return "", fmt.Errorf("failed to hash block: %w", err)
	}

	return fmt.Sprintf("%x", hashBytes), nil
}
