// consensus/pos/slashing_evidence_test.go
// Tests for H-01: Signature verification on slashing evidence

package pos

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/types"
)

// MockValidatorRegistry for testing
type MockValidatorRegistry struct {
	validators map[string]*core.Validator
}

func NewMockValidatorRegistry() *MockValidatorRegistry {
	return &MockValidatorRegistry{
		validators: make(map[string]*core.Validator),
	}
}

func (m *MockValidatorRegistry) GetValidator(address string) (*core.Validator, error) {
	v, ok := m.validators[address]
	if !ok {
		return nil, nil
	}
	return v, nil
}

func (m *MockValidatorRegistry) AddValidator(address string, publicKey []byte) {
	m.validators[address] = &core.Validator{
		Address: address,
		Pubkey:  publicKey,
		Active:  true,
	}
}

// Helper to create properly signed attestation
func createSignedAttestation(
	validatorAddr string,
	slot uint64,
	blockHash string,
	privateKey ed25519.PrivateKey,
) *types.Attestation {
	// Create attestation
	att := &types.Attestation{
		ValidatorAddress: validatorAddr,
		Slot:             slot,
		BlockHash:        blockHash,
		Timestamp:        time.Now().Unix(),
	}

	// Create signature
	evidence := &SlashingEvidence{}
	message := evidence.hashAttestation(att)
	signature := ed25519.Sign(privateKey, message)

	att.Signature = signature

	return att
}

// Test 1: Reject evidence with fake signatures
func TestSlashingEvidence_RejectFakeSignatures(t *testing.T) {
	// Generate real keypair for validator
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	// Create registry
	registry := NewMockValidatorRegistry()
	registry.AddValidator("validator1", publicKey)

	// Create evidence with FAKE signatures (not signed by validator)
	att1 := &types.Attestation{
		ValidatorAddress: "validator1",
		Slot:             100,
		BlockHash:        "block_a",
		Signature:        []byte("fake_signature_1"), // INVALID!
		Timestamp:        time.Now().Unix(),
	}

	att2 := &types.Attestation{
		ValidatorAddress: "validator1",
		Slot:             100,
		BlockHash:        "block_b",
		Signature:        []byte("fake_signature_2"), // INVALID!
		Timestamp:        time.Now().Unix(),
	}

	evidence := NewSlashingEvidence(
		EvidenceDoubleVoting,
		"validator1",
		&DoubleVoteEvidence{
			Attestation1: att1,
			Attestation2: att2,
		},
		"reporter1",
	)

	// Should FAIL validation due to invalid signatures
	err = evidence.Validate(registry)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid signature")
}

// Test 2: Accept evidence with valid signatures
func TestSlashingEvidence_AcceptValidSignatures(t *testing.T) {
	// Generate keypair
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	// Create registry
	registry := NewMockValidatorRegistry()
	registry.AddValidator("validator1", publicKey)

	// Create PROPERLY SIGNED attestations
	att1 := createSignedAttestation("validator1", 100, "block_a", privateKey)
	att2 := createSignedAttestation("validator1", 100, "block_b", privateKey)

	evidence := NewSlashingEvidence(
		EvidenceDoubleVoting,
		"validator1",
		&DoubleVoteEvidence{
			Attestation1: att1,
			Attestation2: att2,
		},
		"reporter1",
	)

	// Should PASS validation with valid signatures
	err = evidence.Validate(registry)
	assert.NoError(t, err)
}

// Test 3: Reject evidence for non-existent validator
func TestSlashingEvidence_RejectNonExistentValidator(t *testing.T) {
	// Create empty registry
	registry := NewMockValidatorRegistry()

	// Generate keypair (but don't add to registry)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	// Create signed attestations
	att1 := createSignedAttestation("unknown_validator", 100, "block_a", privateKey)
	att2 := createSignedAttestation("unknown_validator", 100, "block_b", privateKey)

	evidence := NewSlashingEvidence(
		EvidenceDoubleVoting,
		"unknown_validator",
		&DoubleVoteEvidence{
			Attestation1: att1,
			Attestation2: att2,
		},
		"reporter1",
	)

	// Should FAIL because validator doesn't exist
	err = evidence.Validate(registry)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// Test 4: Reject evidence with mismatched signatures
func TestSlashingEvidence_RejectMismatchedSignatures(t *testing.T) {
	// Generate TWO different keypairs
	publicKey1, privateKey1, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	_, privateKey2, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	// Register validator with publicKey1
	registry := NewMockValidatorRegistry()
	registry.AddValidator("validator1", publicKey1)

	// Sign att1 with privateKey1 (correct)
	att1 := createSignedAttestation("validator1", 100, "block_a", privateKey1)

	// Sign att2 with privateKey2 (WRONG KEY!)
	att2 := createSignedAttestation("validator1", 100, "block_b", privateKey2)

	evidence := NewSlashingEvidence(
		EvidenceDoubleVoting,
		"validator1",
		&DoubleVoteEvidence{
			Attestation1: att1,
			Attestation2: att2,
		},
		"reporter1",
	)

	// Should FAIL because att2 signature doesn't match validator's public key
	err = evidence.Validate(registry)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid signature")
}

// Test 5: Reject evidence with same block hash (not conflicting)
func TestSlashingEvidence_RejectSameBlockHash(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	registry := NewMockValidatorRegistry()
	registry.AddValidator("validator1", publicKey)

	// Both attestations for SAME block (not double voting!)
	att1 := createSignedAttestation("validator1", 100, "block_a", privateKey)
	att2 := createSignedAttestation("validator1", 100, "block_a", privateKey) // Same hash!

	evidence := NewSlashingEvidence(
		EvidenceDoubleVoting,
		"validator1",
		&DoubleVoteEvidence{
			Attestation1: att1,
			Attestation2: att2,
		},
		"reporter1",
	)

	// Should FAIL because blocks have same hash
	err = evidence.Validate(registry)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "same block")
}

// Test 6: Reject evidence with different slots
func TestSlashingEvidence_RejectDifferentSlots(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	registry := NewMockValidatorRegistry()
	registry.AddValidator("validator1", publicKey)

	// Different slots
	att1 := createSignedAttestation("validator1", 100, "block_a", privateKey)
	att2 := createSignedAttestation("validator1", 101, "block_b", privateKey) // Different slot!

	evidence := NewSlashingEvidence(
		EvidenceDoubleVoting,
		"validator1",
		&DoubleVoteEvidence{
			Attestation1: att1,
			Attestation2: att2,
		},
		"reporter1",
	)

	// Should FAIL because slots are different
	err = evidence.Validate(registry)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "different slots")
}

// Test 7: Reject old evidence (stale)
func TestSlashingEvidence_RejectStaleEvidence(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	registry := NewMockValidatorRegistry()
	registry.AddValidator("validator1", publicKey)

	att1 := createSignedAttestation("validator1", 100, "block_a", privateKey)
	att2 := createSignedAttestation("validator1", 100, "block_b", privateKey)

	evidence := NewSlashingEvidence(
		EvidenceDoubleVoting,
		"validator1",
		&DoubleVoteEvidence{
			Attestation1: att1,
			Attestation2: att2,
		},
		"reporter1",
	)

	// Set timestamp to 30 days ago (too old)
	evidence.Timestamp = time.Now().Unix() - (86400 * 30)

	// Should FAIL because evidence is too old
	err = evidence.Validate(registry)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too old")
}

// Test 8: Test surround voting evidence
func TestSlashingEvidence_SurroundVoting(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	registry := NewMockValidatorRegistry()
	registry.AddValidator("validator1", publicKey)

	// Create inner and outer attestations
	innerAtt := createSignedAttestation("validator1", 100, "inner_block", privateKey)
	outerAtt := createSignedAttestation("validator1", 105, "outer_block", privateKey)

	evidence := NewSlashingEvidence(
		EvidenceSurroundVoting,
		"validator1",
		&SurroundVoteEvidence{
			InnerAttestation: innerAtt,
			OuterAttestation: outerAtt,
		},
		"reporter1",
	)

	// Should PASS with valid signatures
	err = evidence.Validate(registry)
	assert.NoError(t, err)
}

// Test 9: Reject surround voting with fake signatures
func TestSlashingEvidence_SurroundVoting_FakeSignatures(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	registry := NewMockValidatorRegistry()
	registry.AddValidator("validator1", publicKey)

	// Create attestations with fake signatures
	innerAtt := &types.Attestation{
		ValidatorAddress: "validator1",
		Slot:             100,
		BlockHash:        "inner_block",
		Signature:        []byte("fake_inner"), // INVALID!
		Timestamp:        time.Now().Unix(),
	}

	outerAtt := &types.Attestation{
		ValidatorAddress: "validator1",
		Slot:             105,
		BlockHash:        "outer_block",
		Signature:        []byte("fake_outer"), // INVALID!
		Timestamp:        time.Now().Unix(),
	}

	evidence := NewSlashingEvidence(
		EvidenceSurroundVoting,
		"validator1",
		&SurroundVoteEvidence{
			InnerAttestation: innerAtt,
			OuterAttestation: outerAtt,
		},
		"reporter1",
	)

	// Should FAIL due to invalid signatures
	err = evidence.Validate(registry)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid signature")
}

// Test 10: Verify evidence ID is deterministic
func TestSlashingEvidence_DeterministicID(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	registry := NewMockValidatorRegistry()
	registry.AddValidator("validator1", publicKey)

	// Create two identical pieces of evidence
	att1a := createSignedAttestation("validator1", 100, "block_a", privateKey)
	att2a := createSignedAttestation("validator1", 100, "block_b", privateKey)

	att1b := createSignedAttestation("validator1", 100, "block_a", privateKey)
	att2b := createSignedAttestation("validator1", 100, "block_b", privateKey)

	evidence1 := NewSlashingEvidence(
		EvidenceDoubleVoting,
		"validator1",
		&DoubleVoteEvidence{
			Attestation1: att1a,
			Attestation2: att2a,
		},
		"reporter1",
	)

	evidence2 := NewSlashingEvidence(
		EvidenceDoubleVoting,
		"validator1",
		&DoubleVoteEvidence{
			Attestation1: att1b,
			Attestation2: att2b,
		},
		"reporter2", // Different reporter
	)

	// IDs should be the same (deterministic based on validator, slot, type)
	assert.Equal(t, evidence1.ID, evidence2.ID)
}

// Test 11: Test that validation requires registry
func TestSlashingEvidence_RequiresRegistry(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	att1 := createSignedAttestation("validator1", 100, "block_a", privateKey)
	att2 := createSignedAttestation("validator1", 100, "block_b", privateKey)

	evidence := NewSlashingEvidence(
		EvidenceDoubleVoting,
		"validator1",
		&DoubleVoteEvidence{
			Attestation1: att1,
			Attestation2: att2,
		},
		"reporter1",
	)

	// Should FAIL when registry is nil
	err = evidence.Validate(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "registry required")
}

// Benchmark signature verification
func BenchmarkSignatureVerification(b *testing.B) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)

	registry := NewMockValidatorRegistry()
	registry.AddValidator("validator1", publicKey)

	att1 := createSignedAttestation("validator1", 100, "block_a", privateKey)
	att2 := createSignedAttestation("validator1", 100, "block_b", privateKey)

	evidence := NewSlashingEvidence(
		EvidenceDoubleVoting,
		"validator1",
		&DoubleVoteEvidence{
			Attestation1: att1,
			Attestation2: att2,
		},
		"reporter1",
	)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = evidence.Validate(registry)
	}
}
