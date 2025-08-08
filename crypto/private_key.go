// privateKey.go
package crypto

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
)

// --- Exported PrivateKey Implementation Struct ---
type PrivateKeyImpl struct {
	// Ed25519 private key (64 bytes: 32 bytes seed + 32 bytes public key)
	PrivKey ed25519.PrivateKey
}

// Update interface assertion to use the new exported type name
var _ PrivateKey = (*PrivateKeyImpl)(nil)

// --- Functions ---

func NewPrivateKey() (PrivateKey, error) {
	// Ed25519 key generation
	_, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate ed25519 key: %w", err)
	}

	// Make a copy to ensure immutability
	privKeyCopy := make(ed25519.PrivateKey, ed25519.PrivateKeySize)
	copy(privKeyCopy, privKey)

	return &PrivateKeyImpl{
		PrivKey: privKeyCopy,
	}, nil
}

func NewPrivateKeyFromEd25519(key ed25519.PrivateKey) PrivateKey {
	if key == nil || len(key) != ed25519.PrivateKeySize {
		return nil
	}

	// Make a copy to ensure immutability
	keyCopy := make(ed25519.PrivateKey, ed25519.PrivateKeySize)
	copy(keyCopy, key)

	return &PrivateKeyImpl{
		PrivKey: keyCopy,
	}
}

func NewPrivateKeyFromBytes(keyData []byte) (PrivateKey, error) {
	priv := &PrivateKeyImpl{}
	err := priv.Unmarshal(keyData)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal private key data: %w", err)
	}
	if priv.PrivKey == nil || len(priv.PrivKey) == 0 {
		return nil, errors.New("unmarshaling resulted in a nil or empty underlying key")
	}
	return priv, nil
}

// --- Methods ---

func (p *PrivateKeyImpl) Bytes() []byte {
	if p.PrivKey == nil || len(p.PrivKey) == 0 {
		return nil
	}
	// Return a copy to ensure immutability
	result := make([]byte, len(p.PrivKey))
	copy(result, p.PrivKey)
	return result
}

func (p *PrivateKeyImpl) String() string {
	if p.PrivKey == nil || len(p.PrivKey) == 0 {
		return "PrivateKey(nil)"
	}
	return fmt.Sprintf("PrivateKey(len:%d)", len(p.PrivKey))
}

func (p *PrivateKeyImpl) Sign(data []byte) Signature {
	if p.PrivKey == nil || len(p.PrivKey) == 0 {
		log.Fatalf("cannot sign with nil or empty private key")
		return nil
	}

	// Ed25519 signing
	sigBytes := ed25519.Sign(p.PrivKey, data)

	sig := NewSignature(sigBytes)
	if sig == nil {
		log.Fatalf("internal error: NewSignature failed for correctly sized bytes")
		return nil
	}
	return sig
}

func (p *PrivateKeyImpl) PublicKey() PublicKey {
	if p.PrivKey == nil || len(p.PrivKey) == 0 {
		return nil
	}

	// Extract public key from private key (Ed25519 private key contains public key)
	pubKey := p.PrivKey.Public().(ed25519.PublicKey)

	// Make a copy to ensure immutability
	pubKeyCopy := make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(pubKeyCopy, pubKey)

	return &publicKey{pubKey: pubKeyCopy}
}

func (p *PrivateKeyImpl) Marshal() ([]byte, error) {
	if p.PrivKey == nil || len(p.PrivKey) == 0 {
		return nil, errors.New("cannot marshal nil or empty private key")
	}
	// Return raw bytes directly
	keyBytes := p.Bytes()
	if keyBytes == nil {
		return nil, errors.New("failed to get private key bytes for marshaling")
	}
	log.Printf("DEBUG: [PrivateKeyImpl.Marshal] Returning %d raw bytes.", len(keyBytes))
	return keyBytes, nil
}

// Unmarshal populates the private key from its raw binary representation.
func (p *PrivateKeyImpl) Unmarshal(data []byte) error {
	log.Printf("DEBUG: [PrivKey Unmarshal] Input RAW data len: %d", len(data))

	// Use the input 'data' directly as the raw key bytes
	keyBytes := data

	if len(keyBytes) == 0 {
		return errors.New("input key data is empty")
	}
	// Check against the expected raw key size for ed25519
	if len(keyBytes) != ed25519.PrivateKeySize {
		err := fmt.Errorf("invalid private key size: got %d, want %d", len(keyBytes), ed25519.PrivateKeySize)
		log.Printf("ERROR: [PrivKey Unmarshal] %v", err)
		return err
	}

	// Create new key and copy data
	p.PrivKey = make(ed25519.PrivateKey, ed25519.PrivateKeySize)
	copy(p.PrivKey, keyBytes)

	log.Printf("DEBUG: [PrivKey Unmarshal] Ed25519 private key unmarshal succeeded.")
	return nil
}

func (p *PrivateKeyImpl) Equal(other *PrivateKey) bool {
	if other == nil {
		return false
	}
	otherInt := *other
	if otherInt == nil {
		return p.PrivKey == nil || len(p.PrivKey) == 0
	}
	return bytes.Equal(p.Bytes(), otherInt.Bytes())
}
