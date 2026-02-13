// consensus/pos/vrf.go
// Simplified VRF implementation using only Go standard library

package pos

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"fmt"

	"github.com/thrylos-labs/go-thrylos/crypto"
)

// VRFProof contains the VRF output and proof
type VRFProof struct {
	Output []byte // VRF output (32 bytes)
	Proof  []byte // VRF proof (64 bytes - Ed25519 signature)
}

// GenerateVRFProof generates a deterministic random output with proof
// This uses Ed25519 signatures as a VRF (deterministic and verifiable)
func GenerateVRFProof(privateKey crypto.PrivateKey, input []byte) (*VRFProof, error) {
	// Derive deterministic Ed25519 key from secp256k1 public key
	pubKeyBytes := privateKey.PublicKey().Bytes()
	hash := sha256.Sum256(pubKeyBytes)
	ed25519PrivKey := ed25519.NewKeyFromSeed(hash[:])

	// Sign with Ed25519
	signature := ed25519.Sign(ed25519PrivKey, input)
	output := sha256.Sum256(signature)

	return &VRFProof{
		Output: output[:],
		Proof:  signature,
	}, nil
}

// VerifyVRFProof verifies a VRF proof
func VerifyVRFProof(publicKey []byte, alpha []byte, proof *VRFProof) (bool, []byte, error) {
	if proof == nil || len(proof.Proof) == 0 {
		return false, nil, errors.New("empty proof")
	}

	if len(proof.Proof) != ed25519.SignatureSize {
		return false, nil, fmt.Errorf("invalid proof length: expected %d, got %d",
			ed25519.SignatureSize, len(proof.Proof))
	}

	if len(publicKey) != ed25519.PublicKeySize {
		return false, nil, fmt.Errorf("invalid public key length: expected %d, got %d",
			ed25519.PublicKeySize, len(publicKey))
	}

	ed25519PubKey := ed25519.PublicKey(publicKey)

	// Verify the Ed25519 signature
	if !ed25519.Verify(ed25519PubKey, alpha, proof.Proof) {
		return false, nil, errors.New("signature verification failed")
	}

	// ✅ FIX: Use SHA256 to match GenerateVRFProof
	output := sha256.Sum256(proof.Proof)

	// Verify output matches
	if !bytes.Equal(output[:], proof.Output) {
		return false, nil, errors.New("output mismatch")
	}

	return true, output[:], nil
}

// VRFProofSize returns the size of a VRF proof in bytes
func VRFProofSize() int {
	return 64 // Ed25519 signature size
}

// VRFOutputSize returns the size of VRF output in bytes
func VRFOutputSize() int {
	return 32
}

// deriveVRFPrivateKey derives an Ed25519 private key from secp256k1 private key
// This allows validators to use VRF without registering separate keys
func deriveVRFPrivateKey(secp256k1PrivKey []byte) []byte {
	hash := sha512.Sum512(append(secp256k1PrivKey, []byte("THRYLOS_VRF_PRIVKEY_V1")...))
	return hash[:32]
}

// deriveVRFPublicKey derives an Ed25519 public key from a secp256k1 public key
// This allows us to verify VRF proofs using existing validator keys
func deriveVRFPublicKey(secp256k1PubKey []byte) []byte {
	hash := sha512.Sum512(append(secp256k1PubKey, []byte("THRYLOS_VRF_PUBKEY_V1")...))
	return hash[:32]
}
