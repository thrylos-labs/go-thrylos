package pos

import (
	"os"
	"testing"

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

func u(v string) []byte {
	return coremath.ParseBigInt(v).Bytes()
}

// setupConsensusEngine creates a test consensus engine with minimal configuration
func setupConsensusEngine(t *testing.T) *ConsensusEngine {
	// 1. Setup temporary storage
	tmpDir, err := os.MkdirTemp("", "thrylos-vrf-test-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	badgerStore, err := storage.NewBadgerStorage(tmpDir)
	require.NoError(t, err)
	t.Cleanup(func() { badgerStore.Close() })

	// 2. Create config
	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: "thrylos-testnet-vrf",
		},
		Consensus: config.ConsensusConfig{
			MaxBlockSize:       1000000,
			MaxTxPerBlock:      100,
			CheckpointInterval: 10,
			FinalizationEpochs: 2,
			MaxReorgDepth:      100,
		},
		Staking: config.StakingConfig{
			MinValidatorStake: "1000000",
		},
	}

	// 3. Initialize WorldState
	ws, err := state.NewWorldState(tmpDir, 0, 1, cfg, badgerStore)
	require.NoError(t, err)

	// 4. Create a validator key for the consensus engine
	privKey, err := crypto.NewPrivateKey()
	require.NoError(t, err)
	pubKey := privKey.PublicKey()

	nodeAddr, err := account.GenerateAddress(pubKey)
	require.NoError(t, err)

	// 5. Create consensus engine
	ce := &ConsensusEngine{
		worldState:      ws,
		config:          cfg,
		nodeAddress:     nodeAddr,
		nodePrivateKey:  privKey,
		currentSlot:     0,
		currentEpoch:    0,
		attestations:    make(map[string]*types.Attestation),
		slashingManager: nil, // Not needed for VRF test
	}

	return ce
}

func TestVRFValidatorSelection(t *testing.T) {
	ce := setupConsensusEngine(t)

	// Create validators with different stakes
	val1 := &core.Validator{
		Address: "val1",
		Stake:   u("1000000"), // 1M
		Active:  true,
		Pubkey:  []byte("pubkey1"),
	}
	val2 := &core.Validator{
		Address: "val2",
		Stake:   u("5000000"), // 5M (5x more stake)
		Active:  true,
		Pubkey:  []byte("pubkey2"),
	}
	val3 := &core.Validator{
		Address: "val3",
		Stake:   u("10000000"), // 10M (10x more stake)
		Active:  true,
		Pubkey:  []byte("pubkey3"),
	}

	validators := []*core.Validator{val1, val2, val3}

	// Select for multiple slots to get statistical distribution
	selections := make(map[string]int)
	for slot := uint64(0); slot < 1000; slot++ {
		selected, err := ce.selectValidatorWithVRF(validators, slot)
		require.NoError(t, err)
		require.NotNil(t, selected)
		selections[selected.Address]++
	}

	// Log results
	t.Logf("val1 (1M stake):  %d selections (%.1f%%)", selections["val1"], float64(selections["val1"])/10.0)
	t.Logf("val2 (5M stake):  %d selections (%.1f%%)", selections["val2"], float64(selections["val2"])/10.0)
	t.Logf("val3 (10M stake): %d selections (%.1f%%)", selections["val3"], float64(selections["val3"])/10.0)

	// Verify validator with most stake gets selected most often
	assert.Greater(t, selections["val3"], selections["val1"],
		"val3 with 10x stake should be selected more than val1")
	assert.Greater(t, selections["val3"], selections["val2"],
		"val3 with 2x stake should be selected more than val2")
	assert.Greater(t, selections["val2"], selections["val1"],
		"val2 with 5x stake should be selected more than val1")

	// Verify all validators get selected at least once (non-zero probability)
	assert.Greater(t, selections["val1"], 0, "val1 should be selected at least once")
	assert.Greater(t, selections["val2"], 0, "val2 should be selected at least once")
	assert.Greater(t, selections["val3"], 0, "val3 should be selected at least once")

	// All selections should add up to 1000
	totalSelections := selections["val1"] + selections["val2"] + selections["val3"]
	assert.Equal(t, 1000, totalSelections, "Should have exactly 1000 selections")
}

func TestVRFValidatorSelection_SingleValidator(t *testing.T) {
	ce := setupConsensusEngine(t)

	// Test with only one validator
	val := &core.Validator{
		Address: "only_validator",
		Stake:   u("1000000"),
		Active:  true,
		Pubkey:  []byte("pubkey"),
	}

	validators := []*core.Validator{val}

	// Should always select the only validator
	for slot := uint64(0); slot < 10; slot++ {
		selected, err := ce.selectValidatorWithVRF(validators, slot)
		require.NoError(t, err)
		assert.Equal(t, "only_validator", selected.Address)
	}
}

func TestVRFValidatorSelection_EqualStake(t *testing.T) {
	ce := setupConsensusEngine(t)

	// Create validators with equal stakes
	val1 := &core.Validator{
		Address: "val1",
		Stake:   u("1000000"),
		Active:  true,
		Pubkey:  []byte("pubkey1"),
	}
	val2 := &core.Validator{
		Address: "val2",
		Stake:   u("1000000"), // Same stake
		Active:  true,
		Pubkey:  []byte("pubkey2"),
	}

	validators := []*core.Validator{val1, val2}

	// Select for multiple slots
	selections := make(map[string]int)
	for slot := uint64(0); slot < 1000; slot++ {
		selected, err := ce.selectValidatorWithVRF(validators, slot)
		require.NoError(t, err)
		selections[selected.Address]++
	}

	t.Logf("val1: %d selections (%.1f%%)", selections["val1"], float64(selections["val1"])/10.0)
	t.Logf("val2: %d selections (%.1f%%)", selections["val2"], float64(selections["val2"])/10.0)

	// With equal stake, distribution should be roughly equal (within 30% tolerance)
	ratio := float64(selections["val1"]) / float64(selections["val2"])
	assert.InDelta(t, 1.0, ratio, 0.3, "Equal stake validators should have similar selection rates")
}

func TestVRFValidatorSelection_Deterministic(t *testing.T) {
	ce := setupConsensusEngine(t)

	validators := []*core.Validator{
		{Address: "val1", Stake: u("1000000"), Active: true, Pubkey: []byte("pubkey1")},
		{Address: "val2", Stake: u("2000000"), Active: true, Pubkey: []byte("pubkey2")},
	}

	// Same slot should always select the same validator
	slot := uint64(100)

	selected1, err := ce.selectValidatorWithVRF(validators, slot)
	require.NoError(t, err)

	selected2, err := ce.selectValidatorWithVRF(validators, slot)
	require.NoError(t, err)

	selected3, err := ce.selectValidatorWithVRF(validators, slot)
	require.NoError(t, err)

	// All three selections should be the same
	assert.Equal(t, selected1.Address, selected2.Address, "Selection should be deterministic")
	assert.Equal(t, selected2.Address, selected3.Address, "Selection should be deterministic")
}

func TestVRFValidatorSelection_DifferentSlotsGiveDifferentResults(t *testing.T) {
	ce := setupConsensusEngine(t)

	validators := []*core.Validator{
		{Address: "val1", Stake: u("1000000"), Active: true, Pubkey: []byte("pubkey1")},
		{Address: "val2", Stake: u("1000000"), Active: true, Pubkey: []byte("pubkey2")},
	}

	// Different slots should (likely) give different results
	selections := make(map[string]bool)
	for slot := uint64(0); slot < 100; slot++ {
		selected, err := ce.selectValidatorWithVRF(validators, slot)
		require.NoError(t, err)
		selections[selected.Address] = true
	}

	// With 100 slots and 2 validators, we should see both validators selected
	assert.Len(t, selections, 2, "Both validators should be selected across different slots")
}

func TestVRFValidatorSelection_NoValidators(t *testing.T) {
	ce := setupConsensusEngine(t)

	// Test with empty validator list
	validators := []*core.Validator{}

	_, err := ce.selectValidatorWithVRF(validators, 0)
	assert.Error(t, err, "Should error with no validators")
	assert.Contains(t, err.Error(), "no validators", "Error should mention no validators")
}

func TestVRFValidatorSelection_ZeroStake(t *testing.T) {
	ce := setupConsensusEngine(t)

	// Validator with zero stake
	val := &core.Validator{
		Address: "zero_stake",
		Stake:   nil,
		Active:  true,
		Pubkey:  []byte("pubkey"),
	}

	validators := []*core.Validator{val}

	// Should still work (stake defaults to 1 to prevent division by zero)
	selected, err := ce.selectValidatorWithVRF(validators, 0)
	require.NoError(t, err)
	assert.Equal(t, "zero_stake", selected.Address)
}
