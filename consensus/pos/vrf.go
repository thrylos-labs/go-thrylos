// consensus/pos/vrf.go
// Simplified VRF implementation using only Go standard library

package pos

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/thrylos-labs/go-thrylos/crypto"
)

// VRFProof contains the VRF output and proof
type VRFProof struct {
	Output []byte // VRF output (32 bytes)
	Proof  []byte // VRF proof (65 bytes - secp256k1 signature)
}

// GenerateVRFProof generates a deterministic random output with proof
// This uses a deterministic secp256k1 signature over a domain-separated hash.
func GenerateVRFProof(privateKey crypto.PrivateKey, input []byte) (*VRFProof, error) {
	if privateKey == nil {
		return nil, errors.New("private key cannot be nil")
	}

	msgHash := computeVRFSigningHash(input)
	signature, err := privateKey.SignHash(msgHash)
	if err != nil {
		return nil, fmt.Errorf("failed to sign VRF input: %w", err)
	}

	proofBytes := signature.Bytes()
	output := sha256.Sum256(proofBytes)

	return &VRFProof{
		Output: output[:],
		Proof:  proofBytes,
	}, nil
}

// VerifyVRFProof verifies a VRF proof
func VerifyVRFProof(publicKey []byte, alpha []byte, proof *VRFProof) (bool, []byte, error) {
	if proof == nil || len(proof.Proof) == 0 {
		return false, nil, errors.New("empty proof")
	}

	if len(proof.Proof) != crypto.SignatureSize {
		return false, nil, fmt.Errorf("invalid proof length: expected %d, got %d",
			crypto.SignatureSize, len(proof.Proof))
	}

	pubKey, err := crypto.NewPublicKeyFromBytes(publicKey)
	if err != nil {
		return false, nil, fmt.Errorf("invalid VRF public key: %w", err)
	}

	signature, err := crypto.SignatureFromBytes(proof.Proof)
	if err != nil {
		return false, nil, fmt.Errorf("invalid VRF proof signature: %w", err)
	}

	msgHash := computeVRFSigningHash(alpha)
	if err := pubKey.VerifyHash(msgHash, signature); err != nil {
		return false, nil, errors.New("signature verification failed")
	}

	output := sha256.Sum256(proof.Proof)

	// Verify output matches
	if !bytes.Equal(output[:], proof.Output) {
		return false, nil, errors.New("output mismatch")
	}

	return true, output[:], nil
}

// VRFProofSize returns the size of a VRF proof in bytes
func VRFProofSize() int {
	return crypto.SignatureSize
}

// VRFOutputSize returns the size of VRF output in bytes
func VRFOutputSize() int {
	return 32
}

func computeVRFSigningHash(input []byte) []byte {
	domainSeparated := append([]byte("THRYLOS_VRF_V1"), input...)
	sum := sha256.Sum256(domainSeparated)
	return sum[:]
}
