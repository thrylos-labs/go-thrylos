// File: node/tps_simple_test.go
// A minimal test to verify compilation and basic functionality

package node

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTPSCompilation verifies that TPS testing code compiles correctly
func TestTPSCompilation(t *testing.T) {
	t.Log("Testing TPS code compilation...")

	// Test deterministic private key generation
	privateKey, err := generateTestPrivateKey()
	require.NoError(t, err, "Should generate test private key")
	assert.NotNil(t, privateKey, "Private key should not be nil")

	// Test key properties
	keyBytes := privateKey.Bytes()
	assert.NotEmpty(t, keyBytes, "Private key bytes should not be empty")
	assert.Equal(t, 2560, len(keyBytes), "Private key should be 2560 bytes (MLDSA44)")

	// Test public key derivation
	publicKey := privateKey.PublicKey()
	assert.NotNil(t, publicKey, "Public key should not be nil")

	pubKeyBytes := publicKey.Bytes()
	assert.NotEmpty(t, pubKeyBytes, "Public key bytes should not be empty")

	t.Log("✅ TPS compilation test passed")
}

// TestTPSDataStructures tests TPS result structures
func TestTPSDataStructures(t *testing.T) {
	t.Log("Testing TPS data structures...")

	// Create a test result
	result := &TPSTestResult{
		TestName:          "Test Compilation",
		Duration:          10 * time.Second,
		TotalTransactions: 100,
		SuccessfulTxs:     95,
		FailedTxs:         5,
		AverageTPS:        9.5,
		ErrorRate:         5.0,
		StartTime:         time.Now().Add(-10 * time.Second),
		EndTime:           time.Now(),
	}

	// Validate structure
	assert.Equal(t, "Test Compilation", result.TestName)
	assert.Equal(t, int64(100), result.TotalTransactions)
	assert.Equal(t, int64(95), result.SuccessfulTxs)
	assert.Equal(t, int64(5), result.FailedTxs)
	assert.Equal(t, 9.5, result.AverageTPS)
	assert.Equal(t, 5.0, result.ErrorRate)
	assert.True(t, result.EndTime.After(result.StartTime))

	t.Log("✅ TPS data structures test passed")
}

// TestRecipientGeneration tests recipient address generation
func TestRecipientGeneration(t *testing.T) {
	t.Log("Testing recipient generation...")

	// This is a placeholder test since we need a node to test generateRecipients
	// We'll test the helper function for deterministic key creation instead

	seed1 := []byte("test-seed-1")
	seed2 := []byte("test-seed-2")

	key1, err := createDeterministicPrivateKey(seed1)
	require.NoError(t, err)

	key2, err := createDeterministicPrivateKey(seed2)
	require.NoError(t, err)

	// Same seed should produce same key
	key1Again, err := createDeterministicPrivateKey(seed1)
	require.NoError(t, err)

	// Verify deterministic behavior
	assert.Equal(t, key1.Bytes(), key1Again.Bytes(), "Same seed should produce same key")
	assert.NotEqual(t, key1.Bytes(), key2.Bytes(), "Different seeds should produce different keys")

	t.Log("✅ Recipient generation test passed")
}
