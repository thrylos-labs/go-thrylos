package crypto

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
)

type signature struct {
	sig []byte
}

// Ethereum signatures are 65 bytes (R + S + V)
const SignatureSize = 65

var _ Signature = (*signature)(nil)

func NewSignature(sigBytes []byte) Signature {
	if len(sigBytes) != SignatureSize {
		fmt.Printf("Error: NewSignature received invalid size %d, expected %d\n", len(sigBytes), SignatureSize)
		return nil
	}
	s := make([]byte, SignatureSize)
	copy(s, sigBytes)
	return &signature{sig: s}
}

func SignatureFromBytes(sigBytes []byte) (Signature, error) {
	if len(sigBytes) != SignatureSize {
		return nil, fmt.Errorf("invalid signature length: got %d, want %d", len(sigBytes), SignatureSize)
	}
	s := make([]byte, SignatureSize)
	copy(s, sigBytes)
	return &signature{sig: s}, nil
}

func (s *signature) Bytes() []byte {
	if s.sig == nil {
		return nil
	}
	b := make([]byte, len(s.sig))
	copy(b, s.sig)
	return b
}

// Verify delegates verification to the PublicKey implementation
// This avoids type casting circular dependency issues
func (s *signature) Verify(pubKey *PublicKey, data []byte) error {
	if pubKey == nil || *pubKey == nil {
		return errors.New("public key cannot be nil")
	}

	// Use the interface method on the key to verify this signature
	// We cast 's' (this signature) to the interface type Signature
	var sigInterface Signature = s
	return (*pubKey).Verify(data, &sigInterface)
}

func (s *signature) VerifyWithSalt(pubKey *PublicKey, data, salt []byte) error {
	// Ethereum/Secp256k1 doesn't use salt, so we ignore it and call standard verify
	return s.Verify(pubKey, data)
}

func (s *signature) String() string {
	if s.sig == nil {
		return "Signature(nil)"
	}
	return hex.EncodeToString(s.sig)
}

func (s *signature) Marshal() ([]byte, error) {
	if s.sig == nil {
		return nil, errors.New("cannot marshal nil signature")
	}
	// Return raw bytes directly for compatibility
	return s.Bytes(), nil
}

func (s *signature) Unmarshal(data []byte) error {
	if len(data) != SignatureSize {
		return fmt.Errorf("invalid signature size: got %d, want %d", len(data), SignatureSize)
	}
	s.sig = make([]byte, SignatureSize)
	copy(s.sig, data)
	return nil
}

func (s *signature) Equal(other Signature) bool {
	if other == nil {
		return false
	}
	return bytes.Equal(s.Bytes(), other.Bytes())
}
