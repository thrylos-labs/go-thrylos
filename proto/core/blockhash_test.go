package core

import (
	"strings"
	"testing"
)

func TestCanonicalBlockHash_Golden(t *testing.T) {
	header := &BlockHeader{
		Index:      1,
		PrevHash:   "0x" + strings.Repeat("0", 64),
		Timestamp:  1700000000,
		Validator:  "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		TxRoot:     "0x" + strings.Repeat("11", 32),
		StateRoot:  "0x" + strings.Repeat("22", 32),
		GasUsed:    21000,
		GasLimit:   10000000,
		Slot:       42,
		Epoch:      1,
		TotalFees:  123456,
		MerkleRoot: "0x" + strings.Repeat("33", 32),
	}

	block := &Block{
		Header: header,
	}

	got, err := CanonicalBlockHash(block)
	if err != nil {
		t.Fatalf("CanonicalBlockHash() error = %v", err)
	}

	const want = "a0abe0dddfa50d4d7d26aec040e4fe6234953d49051c4656788e38e054248f79"
	if got != want {
		t.Fatalf("CanonicalBlockHash() = %s, want %s", got, want)
	}
}

func TestCanonicalBlockHash_NilBlock(t *testing.T) {
	if _, err := CanonicalBlockHash(nil); err == nil {
		t.Fatalf("expected error for nil block, got nil")
	}
}

func TestCanonicalBlockHash_NilHeader(t *testing.T) {
	b := &Block{}
	if _, err := CanonicalBlockHash(b); err == nil {
		t.Fatalf("expected error for nil header, got nil")
	}
}
