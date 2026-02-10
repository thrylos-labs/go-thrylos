package pos

import (
	"crypto/ed25519"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrylos-labs/go-thrylos/crypto"
)

// TestVRFBasicFunctionality tests basic VRF proof generation and verification
// TestVRFBasicFunctionality tests basic VRF proof generation and verification
func TestVRFBasicFunctionality(t *testing.T) {
	// Generate a test private key
	privKey, err := crypto.NewPrivateKey()
	require.NoError(t, err)

	// Create VRF input (slot + epoch)
	slot := uint64(100)
	epoch := uint64(10)
	input := make([]byte, 16)
	binary.BigEndian.PutUint64(input[0:8], slot)
	binary.BigEndian.PutUint64(input[8:16], epoch)

	// Generate VRF proof
	proof, err := GenerateVRFProof(privKey, input)
	require.NoError(t, err)
	require.NotNil(t, proof)
	require.Equal(t, 32, len(proof.Output), "VRF output should be 32 bytes")
	require.Equal(t, 64, len(proof.Proof), "Ed25519 VRF proof should be 64 bytes")

	// Derive public key for verification
	// FIXED: Need to derive Ed25519 public key from Ed25519 private key, not from secp256k1
	privKeyBytes := privKey.Bytes()
	ed25519PrivKey := ed25519.NewKeyFromSeed(privKeyBytes)
	vrfPubKey := ed25519PrivKey.Public().(ed25519.PublicKey)

	// Verify the proof
	valid, output, err := VerifyVRFProof(vrfPubKey, input, proof)
	require.NoError(t, err)
	require.True(t, valid, "Valid proof should verify")
	require.Equal(t, proof.Output, output, "Output should match")
}

// TestVRFDeterminism verifies that VRF is deterministic
func TestVRFDeterminism(t *testing.T) {
	privKey, err := crypto.NewPrivateKey()
	require.NoError(t, err)

	slot := uint64(100)
	epoch := uint64(10)
	input := make([]byte, 16)
	binary.BigEndian.PutUint64(input[0:8], slot)
	binary.BigEndian.PutUint64(input[8:16], epoch)

	// Generate proof twice with same input
	proof1, err := GenerateVRFProof(privKey, input)
	require.NoError(t, err)

	proof2, err := GenerateVRFProof(privKey, input)
	require.NoError(t, err)

	// Should produce identical outputs (deterministic)
	assert.Equal(t, proof1.Output, proof2.Output, "VRF should be deterministic")
	assert.Equal(t, proof1.Proof, proof2.Proof, "VRF proofs should be identical")
}

// TestVRFDifferentInputs verifies different inputs produce different outputs
func TestVRFDifferentInputs(t *testing.T) {
	privKey, err := crypto.NewPrivateKey()
	require.NoError(t, err)

	// Input 1: slot 100, epoch 10
	input1 := make([]byte, 16)
	binary.BigEndian.PutUint64(input1[0:8], 100)
	binary.BigEndian.PutUint64(input1[8:16], 10)

	// Input 2: slot 101, epoch 10
	input2 := make([]byte, 16)
	binary.BigEndian.PutUint64(input2[0:8], 101)
	binary.BigEndian.PutUint64(input2[8:16], 10)

	proof1, err := GenerateVRFProof(privKey, input1)
	require.NoError(t, err)

	proof2, err := GenerateVRFProof(privKey, input2)
	require.NoError(t, err)

	// Different inputs should produce different outputs
	assert.NotEqual(t, proof1.Output, proof2.Output, "Different inputs should produce different outputs")
}

// TestVRFDifferentKeys verifies different keys produce different outputs
func TestVRFDifferentKeys(t *testing.T) {
	privKey1, err := crypto.NewPrivateKey()
	require.NoError(t, err)

	privKey2, err := crypto.NewPrivateKey()
	require.NoError(t, err)

	input := make([]byte, 16)
	binary.BigEndian.PutUint64(input[0:8], 100)
	binary.BigEndian.PutUint64(input[8:16], 10)

	proof1, err := GenerateVRFProof(privKey1, input)
	require.NoError(t, err)

	proof2, err := GenerateVRFProof(privKey2, input)
	require.NoError(t, err)

	// Different keys should produce different outputs
	assert.NotEqual(t, proof1.Output, proof2.Output, "Different keys should produce different outputs")
}

// TestVRFInvalidProof verifies that invalid proofs are rejected
func TestVRFInvalidProof(t *testing.T) {
	privKey, err := crypto.NewPrivateKey()
	require.NoError(t, err)

	input := make([]byte, 16)
	binary.BigEndian.PutUint64(input[0:8], 100)
	binary.BigEndian.PutUint64(input[8:16], 10)

	proof, err := GenerateVRFProof(privKey, input)
	require.NoError(t, err)

	ed25519PrivKey := ed25519.NewKeyFromSeed(privKey.Bytes())
	vrfPubKey := ed25519PrivKey.Public().(ed25519.PublicKey)

	// Tamper with proof
	tamperedProof := &VRFProof{
		Output: proof.Output,
		Proof:  make([]byte, len(proof.Proof)),
	}
	copy(tamperedProof.Proof, proof.Proof)
	tamperedProof.Proof[0] ^= 0xFF // Flip bits

	// Verification should fail
	valid, _, err := VerifyVRFProof(vrfPubKey, input, tamperedProof)
	assert.False(t, valid, "Tampered proof should not verify")
	assert.Error(t, err, "Should return error for invalid proof")
}

// TestVRFWrongMessage verifies proof fails with wrong input
func TestVRFWrongMessage(t *testing.T) {
	privKey, err := crypto.NewPrivateKey()
	require.NoError(t, err)

	input1 := make([]byte, 16)
	binary.BigEndian.PutUint64(input1[0:8], 100)
	binary.BigEndian.PutUint64(input1[8:16], 10)

	input2 := make([]byte, 16)
	binary.BigEndian.PutUint64(input2[0:8], 101)
	binary.BigEndian.PutUint64(input2[8:16], 10)

	// Generate proof for input1
	proof, err := GenerateVRFProof(privKey, input1)
	require.NoError(t, err)

	ed25519PrivKey := ed25519.NewKeyFromSeed(privKey.Bytes())
	vrfPubKey := ed25519PrivKey.Public().(ed25519.PublicKey)

	// Try to verify with input2
	valid, _, err := VerifyVRFProof(vrfPubKey, input2, proof)
	assert.False(t, valid, "Proof should not verify with wrong input")
	assert.Error(t, err, "Should return error for wrong input")
}

// TestVRFKeyDerivation tests the key derivation functions
// TestVRFKeyDerivation tests the key derivation functions
func TestVRFKeyDerivation(t *testing.T) {
	privKey, err := crypto.NewPrivateKey()
	require.NoError(t, err)

	privKeyBytes := privKey.Bytes()

	// Generate Ed25519 key pair from seed
	ed25519PrivKey1 := ed25519.NewKeyFromSeed(privKeyBytes)
	vrfPubKey1 := ed25519PrivKey1.Public().(ed25519.PublicKey)

	// Check sizes
	assert.Equal(t, 64, len(ed25519PrivKey1), "Ed25519 private key should be 64 bytes")
	assert.Equal(t, 32, len(vrfPubKey1), "Ed25519 public key should be 32 bytes")

	// Derivation should be deterministic - generate again from same seed
	ed25519PrivKey2 := ed25519.NewKeyFromSeed(privKeyBytes)
	vrfPubKey2 := ed25519PrivKey2.Public().(ed25519.PublicKey)

	assert.Equal(t, ed25519PrivKey1, ed25519PrivKey2, "Private key derivation should be deterministic")
	assert.Equal(t, vrfPubKey1, vrfPubKey2, "Public key derivation should be deterministic")
}

// TestVRFOutputDistribution tests that outputs are uniformly distributed
func TestVRFOutputDistribution(t *testing.T) {
	privKey, err := crypto.NewPrivateKey()
	require.NoError(t, err)

	// Generate 100 VRF outputs with different slots
	outputs := make([][]byte, 100)
	for i := 0; i < 100; i++ {
		input := make([]byte, 16)
		binary.BigEndian.PutUint64(input[0:8], uint64(i))
		binary.BigEndian.PutUint64(input[8:16], 1)

		proof, err := GenerateVRFProof(privKey, input)
		require.NoError(t, err)
		outputs[i] = proof.Output
	}

	// Check for uniqueness (no duplicates in 100 random outputs)
	seen := make(map[string]bool)
	for _, output := range outputs {
		key := string(output)
		assert.False(t, seen[key], "Should not have duplicate outputs")
		seen[key] = true
	}

	// Check that first bytes are distributed (not all the same)
	firstBytes := make(map[byte]int)
	for _, output := range outputs {
		firstBytes[output[0]]++
	}

	// Should have at least 10 different first byte values in 100 samples
	assert.GreaterOrEqual(t, len(firstBytes), 10, "Outputs should be well distributed")
}

// TestVRFProofSizes tests that proof sizes are correct
func TestVRFProofSizes(t *testing.T) {
	assert.Equal(t, 64, VRFProofSize(), "VRF proof should be 64 bytes (Ed25519 signature)")
	assert.Equal(t, 32, VRFOutputSize(), "VRF output should be 32 bytes")
}

// BenchmarkVRFGeneration benchmarks VRF proof generation
func BenchmarkVRFGeneration(b *testing.B) {
	privKey, _ := crypto.NewPrivateKey()
	input := make([]byte, 16)
	binary.BigEndian.PutUint64(input[0:8], 100)
	binary.BigEndian.PutUint64(input[8:16], 10)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := GenerateVRFProof(privKey, input)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkVRFVerification benchmarks VRF proof verification
func BenchmarkVRFVerification(b *testing.B) {
	privKey, _ := crypto.NewPrivateKey()
	input := make([]byte, 16)
	binary.BigEndian.PutUint64(input[0:8], 100)
	binary.BigEndian.PutUint64(input[8:16], 10)

	proof, _ := GenerateVRFProof(privKey, input)

	ed25519PrivKey := ed25519.NewKeyFromSeed(privKey.Bytes())
	vrfPubKey := ed25519PrivKey.Public().(ed25519.PublicKey)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		valid, _, err := VerifyVRFProof(vrfPubKey, input, proof)
		if err != nil || !valid {
			b.Fatal("Verification failed")
		}
	}
}
