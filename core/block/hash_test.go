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

	const want = "387949e3ae39e1e631fc4081ad2075eb7775a5b443c91e86bb87d54d77a44f7d"

	if got != want {
		t.Fatalf("CanonicalBlockHash mismatch.\n got = %s\nwant = %s", got, want)
	}
}
