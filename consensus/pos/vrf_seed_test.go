package pos

import (
	"fmt"
	"testing"
)

func TestVRFVerifier_SlidingWindow(t *testing.T) {
	verifier := NewVRFVerifier()

	// 1. Simulate filling the cache with 10,000 unique outputs
	// We verify that the map grows correctly
	for i := 0; i < 10000; i++ {
		// Create dummy VRF proof
		proof := &VRFProof{
			Output: []byte(fmt.Sprintf("output-%d", i)),
			Proof:  []byte("proof"),
		}

		err := verifier.VerifyVRFWithContext(proof, 1, 1, "prev", 100)
		if err != nil {
			t.Fatalf("Failed to verify valid proof %d: %v", i, err)
		}
	}

	if len(verifier.seenOutputs) != 10000 {
		t.Errorf("Expected map size 10000, got %d", len(verifier.seenOutputs))
	}

	// 2. Add the 10,001st item
	// This should trigger the eviction of the very first item ("output-0")
	proofNew := &VRFProof{
		Output: []byte("output-10000"),
		Proof:  []byte("proof"),
	}
	err := verifier.VerifyVRFWithContext(proofNew, 1, 1, "prev", 100)
	if err != nil {
		t.Fatal(err)
	}

	// 3. Verify Constraints
	// Size should still be 10,000 (capped)
	if len(verifier.seenOutputs) != 10000 {
		t.Errorf("Map size grew beyond limit! Got %d", len(verifier.seenOutputs))
	}

	// "output-0" (the oldest) should be GONE
	if verifier.seenOutputs["output-0"] {
		t.Error("Security Vulnerability: Oldest item was NOT evicted")
	}

	// "output-1" (the second oldest) should still be THERE
	if !verifier.seenOutputs["output-1"] {
		t.Error("Security Vulnerability: Cache wipe occurred? 'output-1' is missing")
	}

	// "output-10000" (the newest) should be THERE
	if !verifier.seenOutputs["output-10000"] {
		t.Error("Newest item was not added")
	}
}
