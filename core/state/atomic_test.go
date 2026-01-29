// core/state/atomic_simple_test.go
// Minimal test for atomic operations without EVM dependencies
// This test file can be run standalone

package state

import (
	"fmt"
	"sync"
	"testing"
)

// ============================================================================
// STANDALONE TEST - No external dependencies
// ============================================================================

func TestAtomicOperations_Standalone(t *testing.T) {
	t.Run("ShardedMutex_LockMultiple", func(t *testing.T) {
		sm := NewShardedMutex()

		// Lock multiple keys
		keys := []string{"alice", "bob", "carol"}
		sm.LockMultiple(keys)

		// Do something
		// ...

		sm.UnlockMultiple(keys)

		t.Log("✅ LockMultiple works")
	})

	t.Run("ShardedMutex_Versions", func(t *testing.T) {
		sm := NewShardedMutex()

		// Get initial version
		v1 := sm.GetVersion("alice")

		// Increment version
		v2 := sm.IncrementVersion("alice")

		if v2 <= v1 {
			t.Errorf("Version should increase: %d -> %d", v1, v2)
		}

		t.Logf("✅ Version tracking works: %d -> %d", v1, v2)
	})

	t.Run("ShardedMutex_CAS", func(t *testing.T) {
		sm := NewShardedMutex()

		v1 := sm.GetVersion("alice")

		// CAS should succeed
		if !sm.CompareAndSwapVersion("alice", v1) {
			t.Error("CAS should succeed with correct version")
		}

		// CAS should fail with old version
		if sm.CompareAndSwapVersion("alice", v1) {
			t.Error("CAS should fail with old version")
		}

		t.Log("✅ Compare-And-Swap works")
	})

	t.Run("AtomicBatch_Basic", func(t *testing.T) {
		sm := NewShardedMutex()

		batch := sm.BeginBatch([]string{"alice", "bob"})
		batch.Lock()

		// Simulate work
		// ...

		batch.Commit()

		t.Log("✅ AtomicBatch works")
	})
}

// ============================================================================
// CONCURRENCY TEST - The most important one
// ============================================================================

func TestConcurrentAccess_Simple(t *testing.T) {
	// Simple in-memory account storage
	type Account struct {
		Balance int64
		mu      sync.Mutex
	}

	accounts := map[string]*Account{
		"alice": {Balance: 1000},
		"bob":   {Balance: 0},
		"carol": {Balance: 0},
	}

	sm := NewShardedMutex()

	// Function to transfer money atomically
	transfer := func(from, to string, amount int64) error {
		sm.LockMultiple([]string{from, to})
		defer sm.UnlockMultiple([]string{from, to})

		if accounts[from].Balance < amount {
			return fmt.Errorf("insufficient balance")
		}

		accounts[from].Balance -= amount
		accounts[to].Balance += amount
		return nil
	}

	// Try to transfer 600 twice simultaneously
	var wg sync.WaitGroup
	errors := make([]error, 2)

	wg.Add(2)

	// Transfer 1: Alice -> Bob (600)
	go func() {
		defer wg.Done()
		errors[0] = transfer("alice", "bob", 600)
	}()

	// Transfer 2: Alice -> Carol (600)
	go func() {
		defer wg.Done()
		errors[1] = transfer("alice", "carol", 600)
	}()

	wg.Wait()

	// CRITICAL TEST: One should succeed, one should fail
	successCount := 0
	for _, err := range errors {
		if err == nil {
			successCount++
		}
	}

	if successCount != 1 {
		t.Errorf("Expected exactly 1 success, got %d", successCount)
		t.Errorf("Error 1: %v", errors[0])
		t.Errorf("Error 2: %v", errors[1])
	}

	// Verify Alice has exactly 400
	if accounts["alice"].Balance != 400 {
		t.Errorf("Alice should have 400, got %d", accounts["alice"].Balance)
	}

	// Verify total is 1000
	total := accounts["alice"].Balance + accounts["bob"].Balance + accounts["carol"].Balance
	if total != 1000 {
		t.Errorf("Total should be 1000, got %d", total)
	}

	t.Logf("✅ Concurrent transfer test passed")
	t.Logf("   Alice: %d, Bob: %d, Carol: %d",
		accounts["alice"].Balance,
		accounts["bob"].Balance,
		accounts["carol"].Balance)
}

// ============================================================================
// STRESS TEST - Many concurrent operations
// ============================================================================

func TestStressTest_ManyTransfers(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test")
	}

	type Account struct {
		Balance int64
	}

	// 10 accounts with 1000 each
	accounts := make(map[string]*Account)
	for i := 0; i < 10; i++ {
		accounts[fmt.Sprintf("account%d", i)] = &Account{Balance: 1000}
	}

	sm := NewShardedMutex()

	transfer := func(from, to string, amount int64) error {
		sm.LockMultiple([]string{from, to})
		defer sm.UnlockMultiple([]string{from, to})

		if accounts[from].Balance < amount {
			return fmt.Errorf("insufficient balance")
		}

		accounts[from].Balance -= amount
		accounts[to].Balance += amount
		return nil
	}

	// Do 100 concurrent transfers
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			from := fmt.Sprintf("account%d", idx%10)
			to := fmt.Sprintf("account%d", (idx+1)%10)
			transfer(from, to, 10) // May fail, that's ok
		}(i)
	}

	wg.Wait()

	// Verify total supply unchanged
	total := int64(0)
	for _, acc := range accounts {
		total += acc.Balance
	}

	if total != 10000 {
		t.Errorf("Total supply should be 10000, got %d", total)
	}

	t.Logf("✅ Stress test passed: Total supply preserved at %d", total)
}

// ============================================================================
// BENCHMARK
// ============================================================================

func BenchmarkAtomicLocking(b *testing.B) {
	sm := NewShardedMutex()
	keys := []string{"alice", "bob"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sm.LockMultiple(keys)
		// Simulate work
		sm.UnlockMultiple(keys)
	}
}
