// publicKey.go
package crypto

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"fmt"
	"log"

	"github.com/thrylos-labs/go-thrylos/crypto/address"
)

type publicKey struct {
	pubKey ed25519.PublicKey
}

var _ PublicKey = (*publicKey)(nil) // Interface assertion

func NewPublicKey(key ed25519.PublicKey) PublicKey {
	if key == nil || len(key) == 0 {
		// Decide handling: return nil interface or error? Interface allows nil.
		return nil
	}
	// Make a copy to ensure immutability
	keyCopy := make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(keyCopy, key)
	return &publicKey{pubKey: keyCopy}
}

func NewPublicKeyFromBytes(keyData []byte) (PublicKey, error) {
	pub := &publicKey{}
	err := pub.Unmarshal(keyData)
	if err != nil {
		// Log the raw key data on error for debugging
		log.Printf("ERROR: NewPublicKeyFromBytes failed Unmarshal. Input keyData (hex, max 64 bytes): %x", keyData[:min(64, len(keyData))])
		return nil, fmt.Errorf("failed to unmarshal public key data: %w", err)
	}
	// This check might be redundant if Unmarshal errors correctly, but safe to keep
	if pub.pubKey == nil || len(pub.pubKey) == 0 {
		return nil, errors.New("unmarshaling resulted in a nil or empty underlying key")
	}
	return pub, nil
}

func (p *publicKey) Bytes() []byte {
	if p.pubKey == nil || len(p.pubKey) == 0 {
		return nil
	}
	// Return a copy to ensure immutability
	result := make([]byte, len(p.pubKey))
	copy(result, p.pubKey)
	return result
}

// String returns a hex-encoded representation.
func (p *publicKey) String() string {
	if p.pubKey == nil || len(p.pubKey) == 0 {
		return "PubKey(nil)"
	}
	return fmt.Sprintf("PubKeyHex:%x", p.Bytes())
}

func (p *publicKey) Address() (*address.Address, error) {
	if p.pubKey == nil || len(p.pubKey) == 0 {
		return nil, errors.New("cannot generate address from nil public key")
	}
	return address.New(p.pubKey)
}

// Verify checks the signature against the message using the public key.
// Takes a pointer to a Signature interface value. Returns nil error on success.
func (p *publicKey) Verify(data []byte, sigPtr *Signature) error {
	// 1. Check interface pointer itself is not nil
	if sigPtr == nil {
		return errors.New("signature argument (pointer) cannot be nil")
	}
	// 2. Dereference the pointer to get the interface value
	sigInt := *sigPtr
	if sigInt == nil {
		return errors.New("signature interface value cannot be nil")
	}
	// 3. Check underlying key
	if p.pubKey == nil || len(p.pubKey) == 0 {
		return errors.New("cannot verify with nil or empty public key")
	}

	// 4. Type assert the interface value sigInt to concrete type *signature
	ed25519Sig, ok := sigInt.(*signature)
	if !ok {
		return fmt.Errorf("invalid signature type: expected *crypto.signature, got %T", sigInt)
	}

	sigBytes := ed25519Sig.Bytes() // Use the Bytes() method which should return a copy
	if len(sigBytes) != ed25519.SignatureSize {
		return fmt.Errorf("invalid signature size: got %d, want %d", len(sigBytes), ed25519.SignatureSize)
	}

	// Ed25519 verification
	isValid := ed25519.Verify(p.pubKey, data, sigBytes)

	if !isValid {
		return errors.New("invalid signature: ed25519 verification failed")
	}
	return nil // Success
}

func (p *publicKey) Marshal() ([]byte, error) {
	if p.pubKey == nil || len(p.pubKey) == 0 {
		return nil, errors.New("cannot marshal nil or empty public key")
	}
	// Return raw bytes directly, NO CBOR encoding
	keyBytes := p.Bytes() // Returns a copy of the raw ed25519 bytes
	if keyBytes == nil {
		return nil, errors.New("failed to get public key bytes for marshaling")
	}
	log.Printf("DEBUG: [publicKey.Marshal] Returning %d raw bytes.", len(keyBytes))
	return keyBytes, nil
}

func (p *publicKey) Unmarshal(data []byte) error {
	// Use the input 'data' directly as the raw key bytes
	keyBytes := data
	log.Printf("DEBUG: [publicKey.Unmarshal] Received %d raw bytes to unmarshal directly.", len(keyBytes))

	if len(keyBytes) == 0 {
		return errors.New("input key data is empty")
	}
	// Check against the expected raw key size for ed25519
	if len(keyBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid public key size: got %d, want %d", len(keyBytes), ed25519.PublicKeySize)
	}

	// Create new key and copy data
	p.pubKey = make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(p.pubKey, keyBytes)

	log.Printf("DEBUG: [publicKey.Unmarshal] Ed25519 public key unmarshal successful.")
	return nil
}

// Equal takes a pointer to a PublicKey interface value.
func (p *publicKey) Equal(other *PublicKey) bool {
	// 1. Check interface pointer itself
	if other == nil {
		return false // p (receiver) isn't nil
	}
	// 2. Dereference pointer
	otherInt := *other
	if otherInt == nil {
		// Is p also effectively nil?
		return p.pubKey == nil || len(p.pubKey) == 0
	}
	// 3. Compare bytes via the interface Bytes() method
	return bytes.Equal(p.Bytes(), otherInt.Bytes())
}
