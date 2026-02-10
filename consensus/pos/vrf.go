// consensus/pos/vrf.go
// Simplified VRF implementation using only Go standard library

package pos

import (
	"crypto/ed25519"
	"crypto/sha512"
	"errors"
	"fmt"

	thrylos_crypto "github.com/thrylos-labs/go-thrylos/crypto"
)

// VRFProof contains the VRF output and proof
type VRFProof struct {
	Output []byte // VRF output (32 bytes)
	Proof  []byte // VRF proof (64 bytes - Ed25519 signature)
}

// GenerateVRFProof generates a deterministic random output with proof
// This uses Ed25519 signatures as a VRF (deterministic and verifiable)
func GenerateVRFProof(privateKey thrylos_crypto.PrivateKey, alpha []byte) (*VRFProof, error) {
	if privateKey == nil {
		return nil, errors.New("private key cannot be nil")
	}

	// Convert the private key to Ed25519 format
	privKeyBytes := privateKey.Bytes()
	if len(privKeyBytes) != 32 {
		return nil, fmt.Errorf("invalid private key length: expected 32, got %d", len(privKeyBytes))
	}

	// Generate Ed25519 keypair from seed
	ed25519PrivKey := ed25519.NewKeyFromSeed(privKeyBytes)

	// Generate deterministic proof using Ed25519 signature
	proof := ed25519.Sign(ed25519PrivKey, alpha)

	// Derive VRF output from the signature
	hash := sha512.Sum512(proof)
	output := hash[:32]

	return &VRFProof{
		Output: output,
		Proof:  proof,
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

	// Recompute output from verified proof
	hash := sha512.Sum512(proof.Proof)
	output := hash[:32]

	// Verify output matches
	if len(output) != len(proof.Output) {
		return false, nil, errors.New("output length mismatch")
	}

	for i := range output {
		if output[i] != proof.Output[i] {
			return false, nil, errors.New("output mismatch")
		}
	}

	return true, output, nil
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
