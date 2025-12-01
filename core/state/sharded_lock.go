package state

import (
	"hash/fnv"
	"sync"
)

// ShardCount defines the number of locks to use for sharding.
// 64 is a good balance for CPU cache lines and concurrency.
const ShardCount = 64

// ShardedMutex provides granular locking based on string keys (addresses)
type ShardedMutex struct {
	locks [ShardCount]sync.RWMutex
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
