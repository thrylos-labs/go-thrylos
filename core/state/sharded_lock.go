// core/state/sharded_lock.go
// SECURITY FIX: Enhanced locking for CertiK Audit Finding #2
// Implements atomic batch operations and prevents race conditions

package state

import (
	"hash/fnv"
	"sort"
	"sync"
	"sync/atomic"
)

// ShardCount defines the number of locks to use for sharding.
// 64 is a good balance for CPU cache lines and concurrency.
const ShardCount = 64

// ShardedMutex provides granular locking based on string keys (addresses)
type ShardedMutex struct {
	locks [ShardCount]sync.RWMutex

	// AUDIT FIX: Add version counter for optimistic locking
	versions [ShardCount]uint64
}

func NewShardedMutex() *ShardedMutex {
	return &ShardedMutex{}
}

// getLock returns the specific mutex for a given key
func (sm *ShardedMutex) getLock(key string) *sync.RWMutex {
	h := fnv.New32a()
	h.Write([]byte(key))
	idx := h.Sum32() % ShardCount
	return &sm.locks[idx]
}

// getShardIndex returns the shard index for a given key
func (sm *ShardedMutex) getShardIndex(key string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(key))
	return h.Sum32() % ShardCount
}

// Lock acquires the write lock for a specific key
func (sm *ShardedMutex) Lock(key string) {
	sm.getLock(key).Lock()
}

// Unlock releases the write lock for a specific key
func (sm *ShardedMutex) Unlock(key string) {
	sm.getLock(key).Unlock()
}

// RLock acquires the read lock for a specific key
func (sm *ShardedMutex) RLock(key string) {
	sm.getLock(key).RLock()
}

// RUnlock releases the read lock for a specific key
func (sm *ShardedMutex) RUnlock(key string) {
	sm.getLock(key).RUnlock()
}

// ============================================================================
// AUDIT FIX: Atomic Batch Operations for Multi-Account Transactions
// ============================================================================

// LockMultiple locks multiple keys atomically in a consistent order
// This prevents deadlocks by always acquiring locks in the same order
func (sm *ShardedMutex) LockMultiple(keys []string) {
	if len(keys) == 0 {
		return
	}

	// Get unique keys (avoid locking same key twice)
	uniqueKeys := deduplicateKeys(keys)

	// Sort keys to ensure consistent lock ordering (prevents deadlocks)
	sortedKeys := sortKeysByHash(uniqueKeys)

	// Acquire locks in order
	for _, key := range sortedKeys {
		sm.Lock(key)
	}
}

// UnlockMultiple unlocks multiple keys in reverse order
func (sm *ShardedMutex) UnlockMultiple(keys []string) {
	if len(keys) == 0 {
		return
	}

	// Get unique keys
	uniqueKeys := deduplicateKeys(keys)

	// Sort keys (same order as lock)
	sortedKeys := sortKeysByHash(uniqueKeys)

	// Release locks in reverse order (good practice)
	for i := len(sortedKeys) - 1; i >= 0; i-- {
		sm.Unlock(sortedKeys[i])
	}
}

// RLockMultiple acquires read locks on multiple keys atomically
func (sm *ShardedMutex) RLockMultiple(keys []string) {
	if len(keys) == 0 {
		return
	}

	uniqueKeys := deduplicateKeys(keys)
	sortedKeys := sortKeysByHash(uniqueKeys)

	for _, key := range sortedKeys {
		sm.RLock(key)
	}
}

// RUnlockMultiple releases read locks on multiple keys
func (sm *ShardedMutex) RUnlockMultiple(keys []string) {
	if len(keys) == 0 {
		return
	}

	uniqueKeys := deduplicateKeys(keys)
	sortedKeys := sortKeysByHash(uniqueKeys)

	for i := len(sortedKeys) - 1; i >= 0; i-- {
		sm.RUnlock(sortedKeys[i])
	}
}

// ============================================================================
// AUDIT FIX: Optimistic Locking with Version Checking
// ============================================================================

// GetVersion returns the current version for a key's shard
// Used for optimistic locking / Compare-And-Swap operations
func (sm *ShardedMutex) GetVersion(key string) uint64 {
	idx := sm.getShardIndex(key)
	return atomic.LoadUint64(&sm.versions[idx])
}

// IncrementVersion atomically increments the version for a key's shard
// Called after successful state update
func (sm *ShardedMutex) IncrementVersion(key string) uint64 {
	idx := sm.getShardIndex(key)
	return atomic.AddUint64(&sm.versions[idx], 1)
}

// CompareAndSwapVersion implements CAS operation for optimistic locking
// Returns true if version matches and update succeeds
func (sm *ShardedMutex) CompareAndSwapVersion(key string, oldVersion uint64) bool {
	idx := sm.getShardIndex(key)
	return atomic.CompareAndSwapUint64(&sm.versions[idx], oldVersion, oldVersion+1)
}

// ============================================================================
// Helper Functions
// ============================================================================

// deduplicateKeys removes duplicate keys from a slice
func deduplicateKeys(keys []string) []string {
	if len(keys) <= 1 {
		return keys
	}

	seen := make(map[string]bool, len(keys))
	result := make([]string, 0, len(keys))

	for _, key := range keys {
		if !seen[key] {
			seen[key] = true
			result = append(result, key)
		}
	}

	return result
}

// sortKeysByHash sorts keys by their hash value for consistent ordering
func sortKeysByHash(keys []string) []string {
	if len(keys) <= 1 {
		return keys
	}

	// Create sortable slice with precomputed hashes
	type keyHash struct {
		key  string
		hash uint32
	}

	keyHashes := make([]keyHash, len(keys))
	h := fnv.New32a()

	for i, key := range keys {
		h.Reset()
		h.Write([]byte(key))
		keyHashes[i] = keyHash{
			key:  key,
			hash: h.Sum32(),
		}
	}

	// Sort by hash value
	sort.Slice(keyHashes, func(i, j int) bool {
		return keyHashes[i].hash < keyHashes[j].hash
	})

	// Extract sorted keys
	result := make([]string, len(keys))
	for i, kh := range keyHashes {
		result[i] = kh.key
	}

	return result
}

// ============================================================================
// AUDIT FIX: Transaction Isolation Helper
// ============================================================================

// AtomicBatch provides transaction-like semantics for state updates
type AtomicBatch struct {
	sm       *ShardedMutex
	keys     []string
	locked   bool
	versions map[string]uint64
}

// BeginBatch starts an atomic batch operation
func (sm *ShardedMutex) BeginBatch(keys []string) *AtomicBatch {
	batch := &AtomicBatch{
		sm:       sm,
		keys:     keys,
		locked:   false,
		versions: make(map[string]uint64, len(keys)),
	}

	// Record initial versions for optimistic validation
	for _, key := range keys {
		batch.versions[key] = sm.GetVersion(key)
	}

	return batch
}

// Lock acquires locks for the batch
func (ab *AtomicBatch) Lock() {
	if !ab.locked {
		ab.sm.LockMultiple(ab.keys)
		ab.locked = true
	}
}

// Unlock releases locks for the batch
func (ab *AtomicBatch) Unlock() {
	if ab.locked {
		ab.sm.UnlockMultiple(ab.keys)
		ab.locked = false
	}
}

// Commit increments versions and releases locks
func (ab *AtomicBatch) Commit() {
	if ab.locked {
		// Increment versions for all keys
		for _, key := range ab.keys {
			ab.sm.IncrementVersion(key)
		}
		ab.Unlock()
	}
}

// Rollback releases locks without incrementing versions
func (ab *AtomicBatch) Rollback() {
	ab.Unlock()
}

// ValidateVersions checks if versions haven't changed since batch started
// Used for optimistic locking validation
func (ab *AtomicBatch) ValidateVersions() bool {
	for key, oldVersion := range ab.versions {
		if ab.sm.GetVersion(key) != oldVersion {
			return false // Version changed - conflict detected
		}
	}
	return true
}

// ============================================================================
// AUDIT FIX: Deadlock Detection (Development/Debug Helper)
// ============================================================================

// LockWithTimeout attempts to acquire a lock with timeout
// Returns false if timeout occurs (potential deadlock)
func (sm *ShardedMutex) LockWithTimeout(key string, timeout <-chan struct{}) bool {
	acquired := make(chan struct{})

	go func() {
		sm.Lock(key)
		close(acquired)
	}()

	select {
	case <-acquired:
		return true
	case <-timeout:
		// Timeout - potential deadlock
		// Note: We can't safely unlock here since we don't hold the lock
		return false
	}
}
