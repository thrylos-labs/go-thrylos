package pos

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVRFSeedGeneration(t *testing.T) {
	vsg := NewVRFSeedGenerator()

	seed1 := vsg.GenerateSeed(1, 100, "hash1", 1000)
	seed2 := vsg.GenerateSeed(1, 100, "hash2", 1000) // Different hash

	// Seeds should be different (unpredictable)
	assert.NotEqual(t, seed1, seed2)

	// Seeds should pass validation
	assert.NoError(t, vsg.ValidateSeed(seed1))
	assert.NoError(t, vsg.ValidateSeed(seed2))
}

func TestVRFGrindingDetection(t *testing.T) {
	vv := NewVRFVerifier()

	vrfProof := &VRFProof{Output: []byte("test")}

	// First time - OK
	err := vv.VerifyVRFWithContext(vrfProof, 1, 1, "hash", 1000)
	assert.NoError(t, err)

	// Second time with same output - BLOCKED
	err = vv.VerifyVRFWithContext(vrfProof, 1, 1, "hash", 1000)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}
