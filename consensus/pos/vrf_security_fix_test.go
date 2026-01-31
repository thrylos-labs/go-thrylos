// vrf_security_fix_test.go
// Simple tests demonstrating the VRF security fixes

package pos

import (
	"testing"
	"time"
)

// TestMinimumRevealDelay tests that reveals before minimum delay are rejected
func TestMinimumRevealDelay(t *testing.T) {
	crm := NewCommitRevealManager(10) // 10 slots deadline

	// Create a test VRF proof
	vrfProof := &VRFProof{
		Output: make([]byte, 32),
		Proof:  make([]byte, 81),
	}

	// Generate nonce
	nonce, err := generateSecureNonce()
	if err != nil {
		t.Fatalf("Failed to generate nonce: %v", err)
	}

	// Commit VRF
	_, err = crm.CommitVRF(
		vrfProof,
		nonce,
		"validator1",
		100,
		10,
		"block123",
	)
	if err != nil {
		t.Fatalf("Failed to commit VRF: %v", err)
	}

	// Try to reveal immediately (should fail)
	_, err = crm.RevealVRF(
		vrfProof,
		nonce,
		"validator1",
		100,
		10,
	)

	if err == nil {
		t.Error("Expected error when revealing too early, got nil")
	}

	if err != nil {
		t.Logf("✓ Correctly rejected early reveal: %v", err)
	}
}

// TestSlashableValidators tests identification of validators who didn't reveal
func TestSlashableValidators(t *testing.T) {
	crm := NewCommitRevealManager(1) // Very short deadline for testing

	// Create and commit a VRF
	vrfProof := &VRFProof{
		Output: make([]byte, 32),
		Proof:  make([]byte, 81),
	}

	nonce, _ := generateSecureNonce()

	_, err := crm.CommitVRF(
		vrfProof,
		nonce,
		"validator1",
		100,
		10,
		"block123",
	)
	if err != nil {
		t.Fatalf("Failed to commit: %v", err)
	}

	// Wait for deadline to pass
	time.Sleep(10 * time.Second)

	// Check for slashable validators
	slashable := crm.GetSlashableValidators(time.Now().Unix())

	if len(slashable) != 1 {
		t.Errorf("Expected 1 slashable validator, got %d", len(slashable))
	}

	if len(slashable) > 0 && slashable[0] != "validator1" {
		t.Errorf("Expected validator1 to be slashable, got %s", slashable[0])
	}

	if len(slashable) == 1 {
		t.Logf("✓ Correctly identified slashable validator: %s", slashable[0])
	}
}

// TestSecureNonceUniqueness tests that nonces are unique
func TestSecureNonceUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	iterations := 100

	for i := 0; i < iterations; i++ {
		nonce, err := generateSecureNonce()
		if err != nil {
			t.Fatalf("Failed to generate nonce: %v", err)
		}

		key := string(nonce)
		if seen[key] {
			t.Error("Generated duplicate nonce!")
		}
		seen[key] = true
	}

	t.Logf("✓ Generated %d unique nonces", iterations)
}

// TestCompleteCommitRevealFlow tests the full workflow
func TestCompleteCommitRevealFlow(t *testing.T) {
	crm := NewCommitRevealManager(10)

	vrfProof := &VRFProof{
		Output: make([]byte, 32),
		Proof:  make([]byte, 81),
	}
	copy(vrfProof.Output, []byte("test_output_12345678901234567890"))

	nonce, _ := generateSecureNonce()

	// Step 1: Commit
	commitment, err := crm.CommitVRF(
		vrfProof,
		nonce,
		"validator1",
		100,
		10,
		"block123",
	)
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
	t.Log("✓ Step 1: Commitment created")

	// Step 2: Wait minimum delay
	time.Sleep(13 * time.Second) // Wait 13 seconds (> 2 slots * 6 seconds)
	t.Log("✓ Step 2: Waited minimum delay")

	// Step 3: Reveal
	reveal, err := crm.RevealVRF(
		vrfProof,
		nonce,
		"validator1",
		100,
		10,
	)
	if err != nil {
		t.Fatalf("Reveal failed: %v", err)
	}
	t.Log("✓ Step 3: Reveal successful")

	// Step 4: Verify
	err = crm.VerifyReveal(commitment, reveal)
	if err != nil {
		t.Fatalf("Verification failed: %v", err)
	}
	t.Log("✓ Step 4: Verification passed")

	t.Log("✓ Complete commit-reveal flow successful!")
}

// BenchmarkNonceGeneration benchmarks the secure nonce generation
func BenchmarkNonceGeneration(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = generateSecureNonce()
	}
}
