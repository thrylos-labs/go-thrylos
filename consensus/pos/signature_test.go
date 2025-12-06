package pos

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/state"
	"github.com/thrylos-labs/go-thrylos/crypto"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
	"github.com/thrylos-labs/go-thrylos/types"
)

func TestSignatureChainIDProtection(t *testing.T) {
	// 1. Setup Two Consensus Engines with DIFFERENT ChainIDs
	privKey, _ := crypto.NewPrivateKey()

	// Engine A (Mainnet)
	cfgA := &config.Config{
		Network: config.NetworkConfig{ChainID: "thrylos-mainnet"},
	}
	engineA := &ConsensusEngine{
		config:         cfgA,
		nodePrivateKey: privKey,
	}

	// Engine B (Testnet)
	cfgB := &config.Config{
		Network: config.NetworkConfig{ChainID: "thrylos-testnet"},
	}
	engineB := &ConsensusEngine{
		config:         cfgB,
		nodePrivateKey: privKey, // Same key used on both chains
	}

	// 2. Create a Validator Record
	// We need to mock WorldState lookup. For unit tests, we can mock the interface
	// or just setup a minimal WorldState.
	tmpDir := t.TempDir()
	badgerStore, _ := storage.NewBadgerStorage(tmpDir)
	defer badgerStore.Close()
	ws, _ := state.NewWorldState(tmpDir, 0, 1, cfgA, badgerStore)

	// Add validator to WorldState
	addr, _ := privKey.PublicKey().Address()
	val := &core.Validator{
		Address: addr.String(),
		Pubkey:  privKey.PublicKey().Bytes(),
		Active:  true,
	}
	ws.AddValidator(val)

	// Assign WorldState to engines
	engineA.worldState = ws
	engineB.worldState = ws

	// 3. Create an Attestation
	att := &types.Attestation{
		ValidatorAddress: val.Address,
		BlockHash:        "0x1234",
		BlockHeight:      100,
		Epoch:            5,
		Slot:             10,
		Timestamp:        time.Now().Unix(),
	}

	// 4. Sign with Engine A (Mainnet)
	sigBytes, err := engineA.signAttestation(att)
	require.NoError(t, err)
	att.Signature = sigBytes

	// 5. Verify with Engine A (Should Pass)
	err = engineA.verifyAttestationSignature(att)
	assert.NoError(t, err, "Valid signature on same chain should pass")

	// 6. Verify with Engine B (Should FAIL due to ChainID mismatch)
	err = engineB.verifyAttestationSignature(att)
	assert.Error(t, err, "Signature from different chain must fail")

	// 7. Verify Vote Signature Logic
	vote := &Vote{
		ValidatorAddress: val.Address,
		SourceBlockHash:  "0xAAA",
		TargetBlockHash:  "0xBBB",
		SourceEpoch:      4,
		TargetEpoch:      5,
	}

	// Sign Vote on A
	// We need to implement signVote helper in test or expose it
	hashA, _ := engineA.computeVoteHash(vote)
	vote.Signature = privKey.Sign(hashA).Bytes()

	// Verify on B (Should FAIL)
	err = engineB.verifyVoteSignature(vote)
	assert.Error(t, err, "Vote replay across chains must be rejected")

	fmt.Println("✅ H-02 Fix Verified: Signatures are bound to ChainID")
}
