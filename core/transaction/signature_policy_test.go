package transaction

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/crypto/secp256k1"
	thryloscrypto "github.com/thrylos-labs/go-thrylos/crypto"
)

func TestValidateCanonicalSignatureRejectsHighS(t *testing.T) {
	privateKey, err := thryloscrypto.NewPrivateKey()
	if err != nil {
		t.Fatalf("failed to create private key: %v", err)
	}

	hash := make([]byte, 32)
	hash[31] = 1

	signature, err := privateKey.SignHash(hash)
	if err != nil {
		t.Fatalf("failed to sign hash: %v", err)
	}

	parsed, err := thryloscrypto.SignatureFromBytes(signature.Bytes())
	if err != nil {
		t.Fatalf("failed to parse signature: %v", err)
	}

	highS := new(big.Int).Sub(secp256k1.S256().Params().N, parsed.S())
	highSBytes := make([]byte, 65)
	parsed.R().FillBytes(highSBytes[:32])
	highS.FillBytes(highSBytes[32:64])
	highSBytes[64] = parsed.V() ^ 1

	err = validateCanonicalSignature(highSBytes)
	if err == nil {
		t.Fatal("expected high-S signature to be rejected")
	}
	if !strings.Contains(err.Error(), "high-S") {
		t.Fatalf("expected high-S rejection error, got %v", err)
	}
}
