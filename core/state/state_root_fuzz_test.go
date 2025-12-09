package state

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/thrylos-labs/go-thrylos/config"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
)

// TestStateRootDeterminism_WithBadger uses a real (temp) BadgerDB to ensure full integration accuracy.
// It verifies that regardless of map iteration order, the state root remains identical.
func TestStateRootDeterminism_WithBadger(t *testing.T) {
	// 1. Setup Temp DB
	t.Log("Setting up temp BadgerDB for fuzz test...")
	dir := t.TempDir()

	// Initialize Storage
	badgerStorage, err := storage.NewBadgerStorage(dir)
	require.NoError(t, err)
	defer badgerStorage.Close()

	// Initialize WorldState
	cfg, _ := config.Load()
	ws, err := NewWorldState(dir, 0, 1, cfg, badgerStorage)
	require.NoError(t, err)

	// 2. Generate Data
	seed := time.Now().UnixNano()
	t.Logf("Random Seed: %d", seed)
	rng := rand.New(rand.NewSource(seed))

	// Create Validators with VALID HEX addresses
	for i := 0; i < 10; i++ {
		addr := fmt.Sprintf("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa%04x", i)

		// Fix: Convert int64 stake to string
		stakeVal := rng.Int63n(1000000000) + 1000000

		val := &core.Validator{
			Address: addr,
			Pubkey:  []byte(fmt.Sprintf("pubkey-%d", i)),
			Stake:   fmt.Sprintf("%d", stakeVal), // ✅ Fix: Convert to string
			Active:  true,
		}
		ws.validators[addr] = val
	}

	// Create Accounts with Delegations and VALID HEX addresses
	for i := 0; i < 50; i++ {
		addr := fmt.Sprintf("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb%04x", i)

		// Fix: Convert int64 balance/rewards to string
		balanceVal := rng.Int63n(1000000000)
		rewardsVal := rng.Int63n(100000)

		acc := &core.Account{
			Address:     addr,
			Balance:     fmt.Sprintf("%d", balanceVal), // ✅ Fix: Convert to string
			Nonce:       rng.Uint64(),
			DelegatedTo: make(map[string]string),       // ✅ Fix: map[string]string
			Rewards:     fmt.Sprintf("%d", rewardsVal), // ✅ Fix: Convert to string
		}

		// Random delegations pointing to valid validator addresses
		totalDelegated := int64(0)
		for j := 0; j < rng.Intn(5); j++ {
			valIndex := rng.Intn(10)
			valAddr := fmt.Sprintf("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa%04x", valIndex)

			if _, exists := acc.DelegatedTo[valAddr]; !exists {
				amount := rng.Int63n(100000) + 1
				acc.DelegatedTo[valAddr] = fmt.Sprintf("%d", amount) // ✅ Fix: Convert to string
				totalDelegated += amount
			}
		}

		// Ensure StakedAmount is >= TotalDelegated
		// Fix: Convert total staked calculation to string
		stakedVal := totalDelegated + rng.Int63n(50000)
		acc.StakedAmount = fmt.Sprintf("%d", stakedVal) // ✅ Fix: Convert to string

		// Save to AccountManager
		err := ws.accountManager.UpdateAccount(acc)
		require.NoError(t, err)
	}

	// 3. Fuzz Test: Calculate StateRoot repeatedly
	t.Log("Calculating initial State Root...")
	err = ws.updateStateRoot()
	require.NoError(t, err)
	initialRoot := ws.stateRoot
	t.Logf("Initial Root: %s", initialRoot)

	for i := 0; i < 100; i++ {
		// Clear current root to force recalculation logic
		ws.stateRoot = ""

		err := ws.updateStateRoot()
		require.NoError(t, err)

		if ws.stateRoot != initialRoot {
			t.Fatalf("NON-DETERMINISM DETECTED at iteration %d!\nExpected: %s\nGot:      %s\nMap sorting failed.", i, initialRoot, ws.stateRoot)
		}
	}

	t.Log("✅ SUCCESS: State Root is deterministic across 100 iterations with complex nested maps.")
}
