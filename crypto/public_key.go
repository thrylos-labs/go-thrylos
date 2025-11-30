package crypto

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/hex"
	"errors"
	"fmt"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/thrylos-labs/go-thrylos/crypto/address"
)

type publicKey struct {
	pubKey *ecdsa.PublicKey
}

var _ PublicKey = (*publicKey)(nil)

func NewPublicKeyFromBytes(data []byte) (PublicKey, error) {
	k, err := ethcrypto.UnmarshalPubkey(data)
	if err != nil {
		return nil, err
	}
	return &publicKey{pubKey: k}, nil
}

func (p *publicKey) Address() (*address.Address, error) {
	// This generates a true Ethereum address
	ethAddr := ethcrypto.PubkeyToAddress(*p.pubKey)

	// Convert to internal Address type
	var addr address.Address
	copy(addr[:], ethAddr.Bytes())
	return &addr, nil
}

func (p *publicKey) Verify(data []byte, sig *Signature) error {
	if sig == nil || *sig == nil {
		return errors.New("signature is nil")
	}

	// Standard EVM verification uses Keccak256 hash
	hash := ethcrypto.Keccak256(data)
	sigBytes := (*sig).Bytes()

	// SigToPub returns the public key that created the signature
	// This handles the ECDSA recovery logic
	recoveredPub, err := ethcrypto.SigToPub(hash, sigBytes)
	if err != nil {
		return fmt.Errorf("failed to recover public key: %v", err)
	}

	// Compare the recovered public key with this public key
	// We compare the X and Y coordinates of the curve points
	if recoveredPub.X.Cmp(p.pubKey.X) != 0 || recoveredPub.Y.Cmp(p.pubKey.Y) != 0 {
		return fmt.Errorf("signature verification failed: public key mismatch")
	}

	return nil
}

func (p *publicKey) Bytes() []byte {
	return ethcrypto.FromECDSAPub(p.pubKey)
}

func (p *publicKey) String() string {
	return hex.EncodeToString(p.Bytes())
}

func (p *publicKey) Marshal() ([]byte, error) {
	return p.Bytes(), nil
}

func (p *publicKey) Unmarshal(data []byte) error {
	k, err := ethcrypto.UnmarshalPubkey(data)
	if err != nil {
		return err
	}
	p.pubKey = k
	return nil
}

func (p *publicKey) Equal(other *PublicKey) bool {
	if other == nil {
		return false
	}
	otherInt := *other
	if otherInt == nil {
		return p.pubKey == nil
	}
	return bytes.Equal(p.Bytes(), otherInt.Bytes())
}
