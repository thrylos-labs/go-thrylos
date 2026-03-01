package pos

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	coremath "github.com/thrylos-labs/go-thrylos/core/math"
	"github.com/thrylos-labs/go-thrylos/core/state"
	"github.com/thrylos-labs/go-thrylos/crypto"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
	"github.com/thrylos-labs/go-thrylos/types"
)

func TestConsensusSignatureSecurity(t *testing.T) {
	// 1. Setup Temporary Storage
	tmpDir, err := os.MkdirTemp("", "thrylos-sig-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	badgerStore, err := storage.NewBadgerStorage(tmpDir)
	require.NoError(t, err)
	defer badgerStore.Close()

	// 2. Setup Config with specific ChainID
	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: "thrylos-mainnet-v1", // Original Chain
		},
		Staking: config.StakingConfig{
			// ✅ FIX: Use string for BigInt field
			MinValidatorStake: "0",
		},
	}

	// 3. Initialize WorldState
	ws, err := state.NewWorldState(tmpDir, 0, 1, cfg, badgerStore)
	require.NoError(t, err)

	// 4. Create a Validator Key
	privKey, err := crypto.NewPrivateKey()
	require.NoError(t, err)
	pubKey := privKey.PublicKey()

	addrStr, err := account.GenerateAddress(pubKey)
	require.NoError(t, err)

	// 5. Register Validator in WorldState (Required for verification lookup)
	val := &core.Validator{
		Address: addrStr,
		Pubkey:  pubKey.Bytes(),
		Active:  true,
		// ✅ FIX: Use string for BigInt field
		Stake: coremath.ParseBigInt("1000").Bytes(),
	}
	err = ws.AddValidator(val)
	require.NoError(t, err)

	// 6. Initialize Consensus Engine
	engine := &ConsensusEngine{
		config:         cfg,
		worldState:     ws,
		nodePrivateKey: privKey,
	}

	// =================================================================
	// TEST CASE 1: Valid Signature Flow (Standard)
	// =================================================================
	attestation := &types.Attestation{
		ValidatorAddress: addrStr,
		BlockHash:        "0x1234567890abcdef",
		BlockHeight:      100,
		Epoch:            10,
		Slot:             320,
		Timestamp:        time.Now().Unix(),
	}

	// Sign the attestation (Internally uses SignHash over Blake2b digest)
	sigBytes, err := engine.signAttestation(attestation)
	require.NoError(t, err, "Signing should succeed")
	attestation.Signature = sigBytes

	// Verify the attestation (Internally uses VerifyHash)
	err = engine.verifyAttestationSignature(attestation)
	assert.NoError(t, err, "Verification should succeed for valid signature")

	// =================================================================
	// TEST CASE 2: Replay Protection (ChainID Mismatch)
	// =================================================================
	// Simulate a different network (e.g., Testnet trying to replay Mainnet msg)
	engine.config.Network.ChainID = "thrylos-testnet-v1"

	err = engine.verifyAttestationSignature(attestation)
	assert.Error(t, err, "Verification MUST fail when ChainID differs")
	assert.Contains(t, err.Error(), "invalid signature", "Error should indicate signature invalidity due to ChainID mismatch")

	// Restore correct ChainID
	engine.config.Network.ChainID = "thrylos-mainnet-v1"

	// =================================================================
	// TEST CASE 3: Data Tampering
	// =================================================================
	// Modify data after signing
	attestation.BlockHash = "0xDEADBEEF"

	err = engine.verifyAttestationSignature(attestation)
	assert.Error(t, err, "Verification MUST fail if data is tampered")

	fmt.Println("✅ Consensus Signature & Replay Protection Tests Passed")
}
