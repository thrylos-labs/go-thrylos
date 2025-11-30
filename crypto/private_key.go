package crypto

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

type PrivateKeyImpl struct {
	key *ecdsa.PrivateKey
}

// Update interface assertion
var _ PrivateKey = (*PrivateKeyImpl)(nil)

func NewPrivateKey() (PrivateKey, error) {
	k, err := ethcrypto.GenerateKey()
	if err != nil {
		return nil, err
	}
	return &PrivateKeyImpl{key: k}, nil
}

func NewPrivateKeyFromBytes(keyData []byte) (PrivateKey, error) {
	k, err := ethcrypto.ToECDSA(keyData)
	if err != nil {
		return nil, err
	}
	return &PrivateKeyImpl{key: k}, nil
}

// Sign signs data using Ethereum-standard Keccak256 + Secp256k1
func (p *PrivateKeyImpl) Sign(data []byte) Signature {
	// Ethereum signs the hash, not the raw data usually.
	// We assume 'data' is the raw message/transaction bytes, so we hash it.
	hash := ethcrypto.Keccak256(data)

	// Sign returns the signature in [R || S || V] format (65 bytes)
	sig, err := ethcrypto.Sign(hash, p.key)
	if err != nil {
		fmt.Printf("Error signing data: %v\n", err)
		return nil
	}
	return NewSignature(sig)
}

func (p *PrivateKeyImpl) PublicKey() PublicKey {
	return &publicKey{pubKey: &p.key.PublicKey}
}

func (p *PrivateKeyImpl) Bytes() []byte {
	return ethcrypto.FromECDSA(p.key)
}

func (p *PrivateKeyImpl) String() string {
	if p.key == nil {
		return "PrivateKey(nil)"
	}
	return hex.EncodeToString(p.Bytes())
}

func (p *PrivateKeyImpl) Marshal() ([]byte, error) {
	return p.Bytes(), nil
}

func (p *PrivateKeyImpl) Unmarshal(data []byte) error {
	k, err := ethcrypto.ToECDSA(data)
	if err != nil {
		return err
	}
	p.key = k
	return nil
}

func (p *PrivateKeyImpl) Equal(other *PrivateKey) bool {
	if other == nil {
		return false
	}
	otherInt := *other
	if otherInt == nil {
		return p.key == nil
	}
	return bytes.Equal(p.Bytes(), otherInt.Bytes())
}
