// core/block_hash.go
package core

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"

	"golang.org/x/crypto/blake2b"
)

func CanonicalBlockHash(block *Block) (string, error) {
	if block == nil {
		return "", fmt.Errorf("block cannot be nil")
	}
	if block.Header == nil {
		return "", fmt.Errorf("block header cannot be nil")
	}

	h := block.Header
	buf := bytes.NewBuffer(nil)

	writeInt64(buf, h.Index)
	writeString(buf, h.PrevHash)
	writeInt64(buf, h.Timestamp)
	writeString(buf, h.Validator)
	writeString(buf, h.TxRoot)
	writeString(buf, h.StateRoot)
	writeInt64(buf, h.GasUsed)
	writeInt64(buf, h.GasLimit)
	writeUint64(buf, h.Slot)
	writeUint64(buf, h.Epoch)
	writeInt64(buf, h.TotalFees)
	writeString(buf, h.MerkleRoot)

	sum := blake2b.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:]), nil
}

func writeInt64(buf *bytes.Buffer, v int64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(v))
	buf.Write(b[:])
}

func writeUint64(buf *bytes.Buffer, v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	buf.Write(b[:])
}

func writeString(buf *bytes.Buffer, s string) {
	data := []byte(s)
	writeUint64(buf, uint64(len(data)))
	buf.Write(data)
}
