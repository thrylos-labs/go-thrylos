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
