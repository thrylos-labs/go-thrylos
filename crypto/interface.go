// crypto/interface.go
package crypto

import "github.com/thrylos-labs/go-thrylos/crypto/address"

type PrivateKey interface {
	Bytes() []byte
	String() string

	// Sign calculates a Keccak256 hash of msg and signs it
	Sign(msg []byte) Signature

	// [FIX L-02] SignHash signs a pre-calculated 32-byte hash
	SignHash(hash []byte) (Signature, error)

	PublicKey() PublicKey
	Marshal() ([]byte, error)
	Unmarshal([]byte) error
	Equal(other *PrivateKey) bool
}

type PublicKey interface {
	Bytes() []byte
	Address() (*address.Address, error)
	String() string

	// Verify calculates Keccak256 hash of data and verifies signature
	Verify(data []byte, signature *Signature) error

	// [FIX L-02] VerifyHash verifies signature against a pre-calculated hash
	VerifyHash(hash []byte, signature *Signature) error

	Marshal() ([]byte, error)
	Unmarshal([]byte) error
	Equal(other *PublicKey) bool
}

type Signature interface {
	Bytes() []byte
	Verify(pubKey *PublicKey, data []byte) error
	VerifyWithSalt(pubKey *PublicKey, data, salt []byte) error
	String() string
	Marshal() ([]byte, error)
	Unmarshal([]byte) error
	Equal(other Signature) bool
}
