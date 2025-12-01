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
	// Use a unique dir
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
		// Use 'aa...' prefix for validators to distinguish them
		addr := fmt.Sprintf("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa%04x", i)
		val := &core.Validator{
			Address: addr,
			Pubkey:  []byte(fmt.Sprintf("pubkey-%d", i)),
			Stake:   rng.Int63n(1000000000) + 1000000, // Ensure valid stake amount
			Active:  true,
		}
		ws.validators[addr] = val
	}

	// Create Accounts with Delegations and VALID HEX addresses
	for i := 0; i < 50; i++ {
		// Use 'bb...' prefix for accounts
		addr := fmt.Sprintf("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb%04x", i)

		acc := &core.Account{
			Address:     addr,
			Balance:     rng.Int63n(1000000000),
			Nonce:       rng.Uint64(),
			DelegatedTo: make(map[string]int64),
			Rewards:     rng.Int63n(100000),
		}

		// Random delegations pointing to valid validator addresses
		totalDelegated := int64(0)
		for j := 0; j < rng.Intn(5); j++ {
			valIndex := rng.Intn(10)
			valAddr := fmt.Sprintf("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa%04x", valIndex)

			// Ensure we don't overwrite existing delegation to same validator (which would mess up total calc)
			if _, exists := acc.DelegatedTo[valAddr]; !exists {
				amount := rng.Int63n(100000) + 1 // Non-zero amount
				acc.DelegatedTo[valAddr] = amount
				totalDelegated += amount
			}
		}

		// Ensure StakedAmount is >= TotalDelegated (Business Logic Constraint)
		// We add some extra to the staked amount to simulate self-stake or just surplus
		acc.StakedAmount = totalDelegated + rng.Int63n(50000)

		// Save to AccountManager (which validates address format and saves to DB)
		err := ws.accountManager.UpdateAccount(acc)
		require.NoError(t, err)
	}

	// 3. Fuzz Test: Calculate StateRoot repeatedly
	// If map iteration order affects the hash, this loop will fail.

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
