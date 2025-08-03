package network

// // network/sync.go - Main synchronization coordinator
// import (
// 	"context"
// 	"fmt"
// 	"sync"
// 	"time"

// 	"github.com/thrylos-labs/go-thrylos/config"
// 	"github.com/thrylos-labs/go-thrylos/core/chain"
// 	"github.com/thrylos-labs/go-thrylos/core/state"
// 	"github.com/thrylos-labs/go-thrylos/network/sync"
// )

// // SyncStatus represents the current synchronization state
// type SyncStatus int

// const (
// 	SyncStatusIdle SyncStatus = iota
// 	SyncStatusSyncing
// 	SyncStatusCatchingUp
// 	SyncStatusComplete
// 	SyncStatusFailed
// )

// // SyncManager coordinates all synchronization activities
// type SyncManager struct {
// 	config       *config.Config
// 	blockchain   *chain.Blockchain
// 	worldState   *state.WorldState
// 	p2pNetwork   *P2PNetwork
// 	synchronizer *sync.Synchronizer
// 	stateSyncer  *sync.StateSyncer

// 	// State
// 	status        SyncStatus
// 	currentHeight int64
// 	targetHeight  int64
// 	lastSyncTime  time.Time
// 	syncPeers     map[string]*SyncPeer

// 	// Control
// 	ctx        context.Context
// 	cancelFunc context.CancelFunc
// 	mu         sync.RWMutex

// 	// Metrics
// 	syncStartTime   time.Time
// 	blocksProcessed int64
// 	syncErrors      int64
// 	avgBlockTime    time.Duration
// }

// // SyncPeer represents a peer we can sync with
// type SyncPeer struct {
// 	PeerID       string
// 	Height       int64
// 	LastSeen     time.Time
// 	Latency      time.Duration
// 	Reliability  float64
// 	IsResponsive bool
// }

// // SyncConfig contains synchronization configuration
// type SyncConfig struct {
// 	MaxConcurrentRequests int
// 	RequestTimeout        time.Duration
// 	MaxBlocksPerRequest   int
// 	SyncInterval          time.Duration
// 	StateSnapshot         bool
// 	FastSync              bool
// 	PruneOldStates        bool
// }

// // NewSyncManager creates a new synchronization manager
// func NewSyncManager(
// 	config *config.Config,
// 	blockchain *chain.Blockchain,
// 	worldState *state.WorldState,
// 	p2pNetwork *P2PNetwork,
// ) *SyncManager {
// 	ctx, cancelFunc := context.WithCancel(context.Background())

// 	// Create sync configuration
// 	syncConfig := &SyncConfig{
// 		MaxConcurrentRequests: 5,
// 		RequestTimeout:        30 * time.Second,
// 		MaxBlocksPerRequest:   100,
// 		SyncInterval:          5 * time.Second,
// 		StateSnapshot:         true,
// 		FastSync:              true,
// 		PruneOldStates:        false,
// 	}

// 	sm := &SyncManager{
// 		config:     config,
// 		blockchain: blockchain,
// 		worldState: worldState,
// 		p2pNetwork: p2pNetwork,
// 		status:     SyncStatusIdle,
// 		syncPeers:  make(map[string]*SyncPeer),
// 		ctx:        ctx,
// 		cancelFunc: cancelFunc,
// 	}

// 	// Initialize synchronizer and state syncer
// 	sm.synchronizer = sync.NewSynchronizer(blockchain, p2pNetwork, syncConfig)
// 	sm.stateSyncer = sync.NewStateSyncer(worldState, p2pNetwork, syncConfig)

// 	return sm
// }

// // Start begins synchronization operations
// func (sm *SyncManager) Start() error {
// 	sm.mu.Lock()
// 	defer sm.mu.Unlock()

// 	if sm.status != SyncStatusIdle {
// 		return fmt.Errorf("sync manager already running")
// 	}

// 	// Start synchronizer and state syncer
// 	if err := sm.synchronizer.Start(); err != nil {
// 		return fmt.Errorf("failed to start synchronizer: %v", err)
// 	}

// 	if err := sm.stateSyncer.Start(); err != nil {
// 		return fmt.Errorf("failed to start state syncer: %v", err)
// 	}

// 	// Start background processes
// 	go sm.syncLoop()
// 	go sm.peerDiscoveryLoop()
// 	go sm.metricsLoop()

// 	sm.status = SyncStatusIdle
// 	fmt.Println("📡 Sync manager started")

// 	return nil
// }

// // Stop gracefully shuts down synchronization
// func (sm *SyncManager) Stop() error {
// 	sm.mu.Lock()
// 	defer sm.mu.Unlock()

// 	if sm.cancelFunc != nil {
// 		sm.cancelFunc()
// 	}

// 	if sm.synchronizer != nil {
// 		sm.synchronizer.Stop()
// 	}

// 	if sm.stateSyncer != nil {
// 		sm.stateSyncer.Stop()
// 	}

// 	sm.status = SyncStatusIdle
// 	fmt.Println("🛑 Sync manager stopped")

// 	return nil
// }

// // SyncWithPeers initiates synchronization with available peers
// func (sm *SyncManager) SyncWithPeers() error {
// 	sm.mu.Lock()
// 	defer sm.mu.Unlock()

// 	if sm.status == SyncStatusSyncing {
// 		return fmt.Errorf("sync already in progress")
// 	}

// 	// Discover available peers
// 	if err := sm.discoverSyncPeers(); err != nil {
// 		return fmt.Errorf("failed to discover peers: %v", err)
// 	}

// 	if len(sm.syncPeers) == 0 {
// 		return fmt.Errorf("no peers available for sync")
// 	}

// 	// Determine if we need to sync
// 	sm.currentHeight = sm.blockchain.GetHeight()
// 	sm.targetHeight = sm.getMaxPeerHeight()

// 	if sm.currentHeight >= sm.targetHeight {
// 		fmt.Printf("✅ Node is up to date (height: %d)\n", sm.currentHeight)
// 		return nil
// 	}

// 	fmt.Printf("🔄 Starting sync from height %d to %d\n", sm.currentHeight, sm.targetHeight)

// 	sm.status = SyncStatusSyncing
// 	sm.syncStartTime = time.Now()

// 	// Start synchronization
// 	go sm.performSync()

// 	return nil
// }

// // ForceSync forces a synchronization regardless of current state
// func (sm *SyncManager) ForceSync() error {
// 	sm.mu.Lock()
// 	sm.status = SyncStatusIdle
// 	sm.mu.Unlock()

// 	return sm.SyncWithPeers()
// }

// // syncLoop main synchronization loop
// func (sm *SyncManager) syncLoop() {
// 	ticker := time.NewTicker(5 * time.Second)
// 	defer ticker.Stop()

// 	for {
// 		select {
// 		case <-ticker.C:
// 			sm.checkSyncStatus()
// 		case <-sm.ctx.Done():
// 			return
// 		}
// 	}
// }

// // peerDiscoveryLoop discovers and maintains sync peers
// func (sm *SyncManager) peerDiscoveryLoop() {
// 	ticker := time.NewTicker(30 * time.Second)
// 	defer ticker.Stop()

// 	for {
// 		select {
// 		case <-ticker.C:
// 			sm.discoverSyncPeers()
// 			sm.updatePeerMetrics()
// 		case <-sm.ctx.Done():
// 			return
// 		}
// 	}
// }

// // metricsLoop updates synchronization metrics
// func (sm *SyncManager) metricsLoop() {
// 	ticker := time.NewTicker(10 * time.Second)
// 	defer ticker.Stop()

// 	for {
// 		select {
// 		case <-ticker.C:
// 			sm.updateSyncMetrics()
// 		case <-sm.ctx.Done():
// 			return
// 		}
// 	}
// }

// // performSync executes the actual synchronization
// func (sm *SyncManager) performSync() {
// 	defer func() {
// 		sm.mu.Lock()
// 		if sm.status == SyncStatusSyncing {
// 			sm.status = SyncStatusComplete
// 		}
// 		sm.mu.Unlock()
// 	}()

// 	// Phase 1: Fast sync (if enabled and far behind)
// 	blocksBehind := sm.targetHeight - sm.currentHeight
// 	if blocksBehind > 1000 && sm.stateSyncer.IsFastSyncEnabled() {
// 		fmt.Printf("🚀 Starting fast sync (%d blocks behind)\n", blocksBehind)
// 		if err := sm.performFastSync(); err != nil {
// 			fmt.Printf("❌ Fast sync failed: %v\n", err)
// 			sm.setStatus(SyncStatusFailed)
// 			return
// 		}
// 	}

// 	// Phase 2: Block sync
// 	fmt.Printf("🔗 Starting block sync\n")
// 	if err := sm.performBlockSync(); err != nil {
// 		fmt.Printf("❌ Block sync failed: %v\n", err)
// 		sm.setStatus(SyncStatusFailed)
// 		return
// 	}

// 	// Phase 3: State sync (if needed)
// 	if sm.stateSyncer.IsStateSyncNeeded() {
// 		fmt.Printf("🌐 Starting state sync\n")
// 		if err := sm.performStateSync(); err != nil {
// 			fmt.Printf("❌ State sync failed: %v\n", err)
// 			sm.setStatus(SyncStatusFailed)
// 			return
// 		}
// 	}

// 	fmt.Printf("✅ Synchronization completed successfully\n")
// 	sm.setStatus(SyncStatusComplete)
// }

// // performFastSync executes fast synchronization using state snapshots
// func (sm *SyncManager) performFastSync() error {
// 	// Get best peer for fast sync
// 	bestPeer := sm.getBestSyncPeer()
// 	if bestPeer == nil {
// 		return fmt.Errorf("no suitable peer for fast sync")
// 	}

// 	// Request state snapshot from peer
// 	snapshot, err := sm.stateSyncer.RequestStateSnapshot(bestPeer.PeerID, sm.targetHeight)
// 	if err != nil {
// 		return fmt.Errorf("failed to get state snapshot: %v", err)
// 	}

// 	// Apply snapshot
// 	if err := sm.stateSyncer.ApplyStateSnapshot(snapshot); err != nil {
// 		return fmt.Errorf("failed to apply state snapshot: %v", err)
// 	}

// 	// Update current height
// 	sm.mu.Lock()
// 	sm.currentHeight = snapshot.Height
// 	sm.mu.Unlock()

// 	fmt.Printf("✅ Fast sync completed, jumped to height %d\n", snapshot.Height)
// 	return nil
// }

// // performBlockSync synchronizes blocks incrementally
// func (sm *SyncManager) performBlockSync() error {
// 	for sm.currentHeight < sm.targetHeight {
// 		// Check if we should stop
// 		select {
// 		case <-sm.ctx.Done():
// 			return fmt.Errorf("sync cancelled")
// 		default:
// 		}

// 		// Calculate how many blocks to request
// 		remaining := sm.targetHeight - sm.currentHeight
// 		batchSize := int64(100) // Max blocks per batch
// 		if remaining < batchSize {
// 			batchSize = remaining
// 		}

// 		// Request blocks from best peer
// 		bestPeer := sm.getBestSyncPeer()
// 		if bestPeer == nil {
// 			return fmt.Errorf("no available peers")
// 		}

// 		blocks, err := sm.synchronizer.RequestBlocks(
// 			bestPeer.PeerID,
// 			sm.currentHeight+1,
// 			sm.currentHeight+batchSize,
// 		)
// 		if err != nil {
// 			fmt.Printf("⚠️ Failed to get blocks from %s: %v\n", bestPeer.PeerID, err)
// 			sm.markPeerUnreliable(bestPeer.PeerID)
// 			continue
// 		}

// 		// Process blocks
// 		for _, block := range blocks {
// 			if err := sm.blockchain.AddBlock(block); err != nil {
// 				return fmt.Errorf("failed to add block %d: %v", block.Header.Index, err)
// 			}
// 			sm.currentHeight = block.Header.Index
// 			sm.blocksProcessed++
// 		}

// 		// Update progress
// 		progress := float64(sm.currentHeight-sm.syncStartTime.Unix()) / float64(sm.targetHeight-sm.syncStartTime.Unix()) * 100
// 		fmt.Printf("📊 Sync progress: %.1f%% (%d/%d)\n", progress, sm.currentHeight, sm.targetHeight)
// 	}

// 	return nil
// }

// // performStateSync synchronizes world state
// func (sm *SyncManager) performStateSync() error {
// 	return sm.stateSyncer.SyncWorldState()
// }

// // checkSyncStatus checks if synchronization is needed
// func (sm *SyncManager) checkSyncStatus() {
// 	sm.mu.RLock()
// 	status := sm.status
// 	sm.mu.RUnlock()

// 	if status != SyncStatusIdle {
// 		return
// 	}

// 	// Check if we've fallen behind
// 	if err := sm.discoverSyncPeers(); err != nil {
// 		return
// 	}

// 	currentHeight := sm.blockchain.GetHeight()
// 	maxPeerHeight := sm.getMaxPeerHeight()

// 	// If we're behind by more than 5 blocks, start sync
// 	if maxPeerHeight > currentHeight+5 {
// 		fmt.Printf("📉 Detected lag: current=%d, max_peer=%d\n", currentHeight, maxPeerHeight)
// 		go sm.SyncWithPeers()
// 	}
// }

// // discoverSyncPeers discovers peers available for synchronization
// func (sm *SyncManager) discoverSyncPeers() error {
// 	if sm.p2pNetwork == nil {
// 		return fmt.Errorf("P2P network not available")
// 	}

// 	// Get connected peers
// 	connectedPeers := sm.p2pNetwork.GetConnectedPeerIDs()

// 	sm.mu.Lock()
// 	defer sm.mu.Unlock()

// 	// Update peer list
// 	newPeers := make(map[string]*SyncPeer)

// 	for _, peerID := range connectedPeers {
// 		// Get peer height
// 		height, err := sm.p2pNetwork.RequestPeerHeight(peerID)
// 		if err != nil {
// 			continue
// 		}

// 		// Update or create peer
// 		if existingPeer, exists := sm.syncPeers[peerID]; exists {
// 			existingPeer.Height = height
// 			existingPeer.LastSeen = time.Now()
// 			existingPeer.IsResponsive = true
// 			newPeers[peerID] = existingPeer
// 		} else {
// 			newPeers[peerID] = &SyncPeer{
// 				PeerID:       peerID,
// 				Height:       height,
// 				LastSeen:     time.Now(),
// 				Reliability:  1.0,
// 				IsResponsive: true,
// 			}
// 		}
// 	}

// 	sm.syncPeers = newPeers
// 	return nil
// }

// // Helper methods

// func (sm *SyncManager) getMaxPeerHeight() int64 {
// 	sm.mu.RLock()
// 	defer sm.mu.RUnlock()

// 	maxHeight := int64(0)
// 	for _, peer := range sm.syncPeers {
// 		if peer.Height > maxHeight {
// 			maxHeight = peer.Height
// 		}
// 	}
// 	return maxHeight
// }

// func (sm *SyncManager) getBestSyncPeer() *SyncPeer {
// 	sm.mu.RLock()
// 	defer sm.mu.RUnlock()

// 	var bestPeer *SyncPeer
// 	bestScore := float64(0)

// 	for _, peer := range sm.syncPeers {
// 		if !peer.IsResponsive {
// 			continue
// 		}

// 		// Score based on height, reliability, and latency
// 		score := float64(peer.Height) * peer.Reliability
// 		if peer.Latency > 0 {
// 			score = score / peer.Latency.Seconds()
// 		}

// 		if score > bestScore {
// 			bestScore = score
// 			bestPeer = peer
// 		}
// 	}

// 	return bestPeer
// }

// func (sm *SyncManager) markPeerUnreliable(peerID string) {
// 	sm.mu.Lock()
// 	defer sm.mu.Unlock()

// 	if peer, exists := sm.syncPeers[peerID]; exists {
// 		peer.Reliability *= 0.8 // Reduce reliability
// 		peer.IsResponsive = false
// 		sm.syncErrors++
// 	}
// }

// func (sm *SyncManager) updatePeerMetrics() {
// 	sm.mu.Lock()
// 	defer sm.mu.Unlock()

// 	now := time.Now()
// 	for peerID, peer := range sm.syncPeers {
// 		// Mark peers as unresponsive if not seen recently
// 		if now.Sub(peer.LastSeen) > 60*time.Second {
// 			peer.IsResponsive = false
// 		}

// 		// Remove very old peers
// 		if now.Sub(peer.LastSeen) > 300*time.Second {
// 			delete(sm.syncPeers, peerID)
// 		}
// 	}
// }

// func (sm *SyncManager) updateSyncMetrics() {
// 	sm.mu.Lock()
// 	defer sm.mu.Unlock()

// 	if sm.status == SyncStatusSyncing && !sm.syncStartTime.IsZero() {
// 		elapsed := time.Since(sm.syncStartTime)
// 		if sm.blocksProcessed > 0 {
// 			sm.avgBlockTime = elapsed / time.Duration(sm.blocksProcessed)
// 		}
// 	}
// }

// func (sm *SyncManager) setStatus(status SyncStatus) {
// 	sm.mu.Lock()
// 	defer sm.mu.Unlock()
// 	sm.status = status
// }

// // Public API methods

// func (sm *SyncManager) GetSyncStatus() map[string]interface{} {
// 	sm.mu.RLock()
// 	defer sm.mu.RUnlock()

// 	return map[string]interface{}{
// 		"status":           sm.status,
// 		"current_height":   sm.currentHeight,
// 		"target_height":    sm.targetHeight,
// 		"blocks_processed": sm.blocksProcessed,
// 		"sync_errors":      sm.syncErrors,
// 		"avg_block_time":   sm.avgBlockTime.String(),
// 		"peer_count":       len(sm.syncPeers),
// 		"last_sync":        sm.lastSyncTime,
// 	}
// }

// func (sm *SyncManager) GetSyncPeers() map[string]*SyncPeer {
// 	sm.mu.RLock()
// 	defer sm.mu.RUnlock()

// 	peers := make(map[string]*SyncPeer)
// 	for k, v := range sm.syncPeers {
// 		peers[k] = v
// 	}
// 	return peers
// }

// func (sm *SyncManager) IsSyncing() bool {
// 	sm.mu.RLock()
// 	defer sm.mu.RUnlock()
// 	return sm.status == SyncStatusSyncing
// }

// func (sm *SyncManager) IsUpToDate() bool {
// 	sm.mu.RLock()
// 	defer sm.mu.RUnlock()
// 	return sm.currentHeight >= sm.targetHeight
// }
