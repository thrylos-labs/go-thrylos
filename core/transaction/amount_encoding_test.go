package transaction

import (
	"testing"

	"github.com/thrylos-labs/go-thrylos/config"
	thryloscrypto "github.com/thrylos-labs/go-thrylos/crypto"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

func TestCreateTransactionPopulatesByteAmountFields(t *testing.T) {
	cfg := config.DefaultConfig()
	validator := NewValidator(0, 1, cfg)

	privateKey, err := thryloscrypto.NewPrivateKey()
	if err != nil {
		t.Fatalf("failed to create private key: %v", err)
	}

	from := privateKey.PublicKey().Address()

	tx, err := validator.CreateTransaction(from.String(), from.String(), "001", 21000, "05", 0, core.TransactionType_TRANSFER, nil, privateKey)
	if err != nil {
		t.Fatalf("failed to create transaction: %v", err)
	}

	if len(tx.Amount) == 0 {
		t.Fatal("expected amount bytes to be populated")
	}
	if len(tx.GasPrice) == 0 {
		t.Fatal("expected gas price bytes to be populated")
	}
	if tx.EncodingVersion != 2 {
		t.Fatalf("expected new transaction encoding version 2, got %d", tx.EncodingVersion)
	}
	if len(tx.Amount) != 1 || tx.Amount[0] != 0x01 {
		t.Fatalf("expected canonical amount bytes, got %v", tx.Amount)
	}
	if len(tx.GasPrice) != 1 || tx.GasPrice[0] != 0x05 {
		t.Fatalf("expected canonical gas price bytes, got %v", tx.GasPrice)
	}
}

func TestTransactionHashDiffersAcrossEncodingVersions(t *testing.T) {
	cfg := config.DefaultConfig()
	validator := NewValidator(0, 1, cfg)

	legacyTx := &core.Transaction{
		Id:        "tx-1",
		From:      "from",
		To:        "to",
		Amount:    []byte("001"),
		Gas:       21000,
		GasPrice:  []byte("05"),
		Nonce:     7,
		Type:      core.TransactionType_TRANSFER,
		Timestamp: 1234567890,
	}

	canonicalTx := &core.Transaction{
		Id:              legacyTx.Id,
		From:            legacyTx.From,
		To:              legacyTx.To,
		Amount:          []byte{0x01},
		Gas:             legacyTx.Gas,
		GasPrice:        []byte{0x05},
		Nonce:           legacyTx.Nonce,
		Type:            legacyTx.Type,
		Timestamp:       legacyTx.Timestamp,
		EncodingVersion: 2,
	}

	legacyHash, err := validator.CalculateTransactionHash(legacyTx)
	if err != nil {
		t.Fatalf("failed to hash legacy transaction: %v", err)
	}
	canonicalHash, err := validator.CalculateTransactionHash(canonicalTx)
	if err != nil {
		t.Fatalf("failed to hash canonical transaction: %v", err)
	}

	if legacyHash == canonicalHash {
		t.Fatal("expected different hashes across transaction encoding versions")
	}
}
