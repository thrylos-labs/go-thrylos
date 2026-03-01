// consensus/pos/consensus_chain_traversal_fix.go
// FIX #2: Proper chain traversal for fork choice
// Replace the placeholder isDescendant in consensus.go

package pos

import (
	"fmt"
	"log"
	"sync"
)

// ChainCache caches chain ancestry relationships for performance
type ChainCache struct {
	// Map from ancestor hash -> descendant hash -> is_descendant (bool)
	descendants map[string]map[string]bool
	mu          sync.RWMutex
}

// NewChainCache creates a new chain cache
func NewChainCache() *ChainCache {
	return &ChainCache{
		descendants: make(map[string]map[string]bool),
	}
}

// Get retrieves cached ancestry relationship
func (cc *ChainCache) Get(ancestorHash, descendantHash string) (bool, bool) {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	if descendants, exists := cc.descendants[ancestorHash]; exists {
		if isDesc, cached := descendants[descendantHash]; cached {
			return isDesc, true
		}
	}
	return false, false
}

// Set stores ancestry relationship in cache
func (cc *ChainCache) Set(ancestorHash, descendantHash string, isDescendant bool) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	if cc.descendants[ancestorHash] == nil {
		cc.descendants[ancestorHash] = make(map[string]bool)
	}
	cc.descendants[ancestorHash][descendantHash] = isDescendant
}

// Clear removes all cached data (call periodically to prevent unbounded growth)
func (cc *ChainCache) Clear() {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.descendants = make(map[string]map[string]bool)
}

// ClearAncestor removes all cached data for a specific ancestor
func (cc *ChainCache) ClearAncestor(ancestorHash string) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	delete(cc.descendants, ancestorHash)
}

// ============================================================================
// MAIN FIX: isDescendant implementation
// ============================================================================

// isDescendant checks if blockHash is a descendant of ancestorHash
// This is the FIXED version that properly traverses the chain
func (ce *ConsensusEngine) isDescendant(blockHash, ancestorHash string) bool {
	// Same block is trivially a descendant
	if blockHash == ancestorHash {
		return true
	}

	// Check cache first (performance optimization)
	if cached, found := ce.chainCache.Get(ancestorHash, blockHash); found {
		return cached
	}

	// Not in cache - compute the relationship
	result := ce.computeIsDescendant(blockHash, ancestorHash)

	// Cache the result for future lookups
	ce.chainCache.Set(ancestorHash, blockHash, result)

	return result
}

// computeIsDescendant performs the actual chain traversal
func (ce *ConsensusEngine) computeIsDescendant(blockHash, ancestorHash string) bool {
	// If they're the same, it's a descendant
	if blockHash == ancestorHash {
		return true
	}

	// Start from blockHash and traverse backwards towards ancestor
	currentHash := blockHash
	maxDepth := 1000 // Prevent infinite loops (safety limit)

	for i := 0; i < maxDepth; i++ {
		// Get current block from world state
		block, _ := ce.worldState.GetBlockByHash(currentHash)
		if block == nil {
			// Block not found in chain - not a descendant
			return false
		}

		// Check if this block's parent is the ancestor we're looking for
		if block.Header.PrevHash == ancestorHash {
			// Found it! blockHash is a descendant of ancestorHash
			return true
		}

		// If we've reached genesis (no previous hash), ancestor not found
		if block.Header.PrevHash == "" {
			return false
		}

		// Move to parent block and continue searching
		currentHash = block.Header.PrevHash
	}

	// Exceeded max depth - assume not a descendant (safety fallback)
	// This should rarely happen in practice
	log.Printf("⚠️  WARNING: Chain traversal exceeded max depth (%d) checking if %s is descendant of %s\n",
		maxDepth, blockHash[:8], ancestorHash[:8])
	return false
}

// getChainPath returns the full path from blockHash back to ancestorHash
// This is useful for debugging and understanding fork structures
func (ce *ConsensusEngine) getChainPath(blockHash, ancestorHash string) ([]string, error) {
	if blockHash == ancestorHash {
		return []string{blockHash}, nil
	}

	path := []string{blockHash}
	currentHash := blockHash
	maxDepth := 1000

	for i := 0; i < maxDepth; i++ {
		block, _ := ce.worldState.GetBlockByHash(currentHash)
		if block == nil {
			return nil, fmt.Errorf("block %s not found in chain", currentHash)
		}

		if block.Header.PrevHash == ancestorHash {
			// Found the ancestor - add it and return
			path = append(path, ancestorHash)
			return path, nil
		}

		if block.Header.PrevHash == "" {
			// Reached genesis without finding ancestor
			return nil, fmt.Errorf("ancestor %s not found in chain from %s",
				ancestorHash, blockHash)
		}

		path = append(path, block.Header.PrevHash)
		currentHash = block.Header.PrevHash
	}

	return nil, fmt.Errorf("chain path exceeded max depth")
}

// getCommonAncestor finds the common ancestor of two blocks
// Useful for fork detection and resolution
func (ce *ConsensusEngine) getCommonAncestor(blockHash1, blockHash2 string) (string, error) {
	if blockHash1 == blockHash2 {
		return blockHash1, nil
	}

	// Get paths to genesis for both blocks
	path1 := make(map[string]bool)
	currentHash := blockHash1
	maxDepth := 1000

	// Build set of all ancestors of block1
	for i := 0; i < maxDepth; i++ {
		path1[currentHash] = true

		block, _ := ce.worldState.GetBlockByHash(currentHash)
		if block == nil {
			break
		}

		if block.Header.PrevHash == "" {
			break // Reached genesis
		}

		currentHash = block.Header.PrevHash
	}

	// Traverse block2's chain until we find a block in block1's ancestry
	currentHash = blockHash2
	for i := 0; i < maxDepth; i++ {
		if path1[currentHash] {
			// Found common ancestor!
			return currentHash, nil
		}

		block, _ := ce.worldState.GetBlockByHash(currentHash)
		if block == nil {
			break
		}

		if block.Header.PrevHash == "" {
			break // Reached genesis without finding common ancestor (shouldn't happen)
		}

		currentHash = block.Header.PrevHash
	}

	return "", fmt.Errorf("no common ancestor found between %s and %s",
		blockHash1[:8], blockHash2[:8])
}

// isForkDetected checks if two blocks represent different forks at the same height
func (ce *ConsensusEngine) isForkDetected(blockHash1, blockHash2 string) bool {
	if blockHash1 == blockHash2 {
		return false
	}

	block1, _ := ce.worldState.GetBlockByHash(blockHash1) // FIXED
	block2, _ := ce.worldState.GetBlockByHash(blockHash2)

	if block1 == nil || block2 == nil {
		return false
	}

	if block1.Header.Index == block2.Header.Index {
		return true
	}

	return false
}
