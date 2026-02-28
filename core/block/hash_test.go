package block

import (
	"testing"

	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

func TestCanonicalBlockHash_GenesisLikeBlock(t *testing.T) {
	block := &core.Block{
		Header: &core.BlockHeader{
			Index:     0,
			PrevHash:  "",
			Timestamp: 1700000000,
			Validator: "0x10e3e2c14476050a52fb92019838fa54cb460c07",
			TxRoot:    "",
			StateRoot: "1210194a3ffad70df3bf3a73eac96a51d7176c29507943e68d965a33de458fd8",
			GasUsed:   0,
			GasLimit:  10000000,
		},
		Transactions: nil,
	}

	got, err := CanonicalBlockHash(block)
	if err != nil {
		t.Fatalf("CanonicalBlockHash returned error: %v", err)
	}

	const want = "12ff54feb3cf6b1cee32b04e747866938e74ee4c7263a7b26aa83610194715e9"

	if got != want {
		t.Fatalf("CanonicalBlockHash mismatch.\n got = %s\nwant = %s", got, want)
	}
}

func TestCanonicalBlockHash_StateEncodingVersionAffectsHash(t *testing.T) {
	legacy := &core.Block{
		Header: &core.BlockHeader{
			Index:     1,
			PrevHash:  "prev",
			Timestamp: 1700000001,
			Validator: "validator",
			TxRoot:    "txroot",
			StateRoot: "stateroot",
			GasUsed:   21000,
			GasLimit:  30000000,
			TotalFees: "001",
		},
	}

	canonical := &core.Block{
		Header: &core.BlockHeader{
			Index:                legacy.Header.Index,
			PrevHash:             legacy.Header.PrevHash,
			Timestamp:            legacy.Header.Timestamp,
			Validator:            legacy.Header.Validator,
			TxRoot:               legacy.Header.TxRoot,
			StateRoot:            legacy.Header.StateRoot,
			GasUsed:              legacy.Header.GasUsed,
			GasLimit:             legacy.Header.GasLimit,
			TotalFees:            "1",
			TotalFeesBytes:       []byte{0x01},
			StateEncodingVersion: 2,
		},
	}

	legacyHash, err := CanonicalBlockHash(legacy)
	if err != nil {
		t.Fatalf("failed to hash legacy block: %v", err)
	}
	canonicalHash, err := CanonicalBlockHash(canonical)
	if err != nil {
		t.Fatalf("failed to hash canonical block: %v", err)
	}

	if legacyHash == canonicalHash {
		t.Fatal("expected state encoding version to affect the block hash")
	}
}
