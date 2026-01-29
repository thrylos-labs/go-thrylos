// core/state/atomic_updates.go
// SECURITY FIX: Atomic state updates for CertiK Audit Finding #2
// Add these functions to your worldstate.go or create as a new file

package state

import (
	"fmt"

	"github.com/thrylos-labs/go-thrylos/proto/core"
)

// ============================================================================
// AUDIT FIX: Atomic Multi-Account Updates
// ============================================================================

// AtomicUpdateAccounts updates multiple accounts atomically
// This prevents race conditions in multi-account transactions (like transfers)
func (ws *WorldState) AtomicUpdateAccounts(accounts []*core.Account) error {
	if len(accounts) == 0 {
		return nil
	}

	// Extract addresses for locking
	addresses := make([]string, len(accounts))
	for i, acc := range accounts {
		addresses[i] = acc.Address
	}

	// Begin atomic batch
	batch := ws.accountMu.BeginBatch(addresses)
	batch.Lock()
	defer batch.Rollback() // Ensure unlock on any error

	// Validate versions haven't changed (optimistic locking check)
	if !batch.ValidateVersions() {
		return fmt.Errorf("state conflict detected: accounts modified by another transaction")
	}

	// Update all accounts
	for _, acc := range accounts {
		if err := ws.accountManager.UpdateAccount(acc); err != nil {
			return fmt.Errorf("failed to update account %s: %w", acc.Address, err)
		}

		// Save to persistent storage
		if err := ws.state.SaveAccount(acc); err != nil {
			return fmt.Errorf("failed to save account %s: %w", acc.Address, err)
		}
	}

	// Commit the batch (increments versions and releases locks)
	batch.Commit()
	return nil
}
