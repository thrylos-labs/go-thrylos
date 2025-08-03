package sync

// network/sync/state_sync.go - World state synchronization implementation
import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/thrylos-labs/go-thrylos/core/state"
	"github.com/thrylos-labs/go-thrylos/network"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// StateSyncer handles world state synchronization
type StateSyncer struct {
	worldState *state.WorldState
	p2pNetwork *network.P2PNetwork
	config     *SyncConfig

	// State sync management
	snapshots    map[string]*StateSnapshot
	syncRequests map[string]*StateSyncRequest

	// State
	isRunning    bool
	isSyncing    bool
	lastSyncTime time.Time
	ctx          context.Context
	cancelFunc   context.CancelFunc
	mu           sync.RWMutex

	// Metrics
	snapshotsReceived int64
	snapshotsApplied  int64
	syncErrors        int64
	lastSnapshotSize  int64
}

// StateSnapshot represents a snapshot of the world state
type StateSnapshot struct {
	Height         int64
	StateRoot      string
	Timestamp      int64
	Accounts       map[string]*core.Account
	Validators     map[string]*core.Validator
	Stakes         map[string]map[string]int64
	CrossShardData map[string]interface{}
	Metadata       map[string]string
	Checksum       string
	CompressedData []byte
	Size           int64
}

// StateSyncRequest represents a request for state synchronization
type StateSyncRequest struct {
	ID          string
	PeerID      string
	Height      int64
	RequestedAt time.Time
	CompletedAt time.Time
	Status      StateSyncStatus
	Error       error
	Snapshot    *StateSnapshot
}

// StateSyncStatus represents the status of state sync
type StateSyncStatus int

const (
	StateSyncPending StateSyncStatus = iota
	StateSyncInProgress
	StateSyncCompleted
	StateSyncFailed
)

// StateSyncMetrics contains metrics for state synchronization
type StateSyncMetrics struct {
	TotalRequests       int64
	SuccessfulSyncs     int64
	FailedSyncs         int64
	AverageSnapshotSize int64
	AverageSyncTime     time.Duration
	LastSyncTime        time.Time
}

// NewStateSyncer creates a new state synchronizer
func NewStateSyncer(
	worldState *state.WorldState,
	p2pNetwork *network.P2PNetwork,
	config *SyncConfig,
) *StateSyncer {
	ctx, cancelFunc := context.WithCancel(context.Background())

	return &StateSyncer{
		worldState:   worldState,
		p2pNetwork:   p2pNetwork,
		config:       config,
		snapshots:    make(map[string]*StateSnapshot),
		syncRequests: make(map[string]*StateSyncRequest),
		ctx:          ctx,
		cancelFunc:   cancelFunc,
	}
}

// Start begins state synchronization services
func (ss *StateSyncer) Start() error {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	if ss.isRunning {
		return fmt.Errorf("state syncer already running")
	}

	// Start background processes
	go ss.snapshotManager()
	go ss.syncRequestProcessor()
	go ss.metricsCollector()

	ss.isRunning = true
	fmt.Println("🌐 State syncer started")

	return nil
}

// Stop gracefully shuts down state synchronization
func (ss *StateSyncer) Stop() error {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	if !ss.isRunning {
		return nil
	}

	if ss.cancelFunc != nil {
		ss.cancelFunc()
	}

	ss.isRunning = false
	fmt.Println("🛑 State syncer stopped")

	return nil
}

// RequestStateSnapshot requests a state snapshot from a peer
func (ss *StateSyncer) RequestStateSnapshot(peerID string, height int64) (*StateSnapshot, error) {
	if !ss.isRunning {
		return nil, fmt.Errorf("state syncer not running")
	}

	requestID := fmt.Sprintf("state-sync-%s-%d-%d", peerID, height, time.Now().UnixNano())

	request := &StateSyncRequest{
		ID:          requestID,
		PeerID:      peerID,
		Height:      height,
		RequestedAt: time.Now(),
		Status:      StateSyncPending,
	}

	ss.mu.Lock()
	ss.syncRequests[requestID] = request
	ss.mu.Unlock()

	// Send request to peer
	snapshot, err := ss.requestSnapshotFromPeer(peerID, height)
	if err != nil {
		request.Status = StateSyncFailed
		request.Error = err
		ss.mu.Lock()
		ss.syncErrors++
		ss.mu.Unlock()
		return nil, fmt.Errorf("failed to get snapshot from peer %s: %v", peerID, err)
	}

	// Validate snapshot
	if err := ss.validateSnapshot(snapshot); err != nil {
		request.Status = StateSyncFailed
		request.Error = err
		return nil, fmt.Errorf("invalid snapshot: %v", err)
	}

	request.Status = StateSyncCompleted
	request.CompletedAt = time.Now()
	request.Snapshot = snapshot

	ss.mu.Lock()
	ss.snapshotsReceived++
	ss.lastSnapshotSize = snapshot.Size
	ss.mu.Unlock()

	return snapshot, nil
}

// ApplyStateSnapshot applies a state snapshot to the local world state
func (ss *StateSyncer) ApplyStateSnapshot(snapshot *StateSnapshot) error {
	if snapshot == nil {
		return fmt.Errorf("snapshot cannot be nil")
	}

	ss.mu.Lock()
	ss.isSyncing = true
	ss.mu.Unlock()

	defer func() {
		ss.mu.Lock()
		ss.isSyncing = false
		ss.mu.Unlock()
	}()

	fmt.Printf("🔄 Applying state snapshot at height %d\n", snapshot.Height)

	// Create backup of current state
	backup := ss.createStateBackup()

	// Apply snapshot data
	if err := ss.applySnapshotData(snapshot); err != nil {
		// Restore from backup on failure
		ss.restoreFromBackup(backup)
		return fmt.Errorf("failed to apply snapshot: %v", err)
	}

	// Verify state consistency
	if err := ss.verifyStateConsistency(snapshot); err != nil {
		// Restore from backup on failure
		ss.restoreFromBackup(backup)
		return fmt.Errorf("state consistency check failed: %v", err)
	}

	ss.mu.Lock()
	ss.snapshotsApplied++
	ss.lastSyncTime = time.Now()
	ss.mu.Unlock()

	fmt.Printf("✅ State snapshot applied successfully\n")
	return nil
}

// SyncWorldState synchronizes the entire world state with peers
func (ss *StateSyncer) SyncWorldState() error {
	if !ss.isRunning {
		return fmt.Errorf("state syncer not running")
	}

	// Get available peers
	peers := ss.getAvailablePeers()
	if len(peers) == 0 {
		return fmt.Errorf("no peers available for state sync")
	}

	// Get target height from best peer
	targetHeight := ss.getMaxPeerHeight(peers)
	currentHeight := ss.worldState.GetCurrentHeight()

	if currentHeight >= targetHeight {
		fmt.Printf("✅ State is up to date (height: %d)\n", currentHeight)
		return nil
	}

	fmt.Printf("🌐 Starting state sync from height %d to %d\n", currentHeight, targetHeight)

	// Try to get snapshot from best peer
	bestPeer := ss.getBestPeerForSync(peers)
	snapshot, err := ss.RequestStateSnapshot(bestPeer, targetHeight)
	if err != nil {
		return fmt.Errorf("failed to get state snapshot: %v", err)
	}

	// Apply snapshot
	if err := ss.ApplyStateSnapshot(snapshot); err != nil {
		return fmt.Errorf("failed to apply state snapshot: %v", err)
	}

	return nil
}

// CreateSnapshot creates a snapshot of the current world state
func (ss *StateSyncer) CreateSnapshot() (*StateSnapshot, error) {
	currentHeight := ss.worldState.GetCurrentHeight()
	stateRoot := ss.worldState.GetStateRoot()

	snapshot := &StateSnapshot{
		Height:         currentHeight,
		StateRoot:      stateRoot,
		Timestamp:      time.Now().Unix(),
		Accounts:       make(map[string]*core.Account),
		Validators:     make(map[string]*core.Validator),
		Stakes:         make(map[string]map[string]int64),
		CrossShardData: make(map[string]interface{}),
		Metadata:       make(map[string]string),
	}

	// Export accounts
	accounts := ss.worldState.ExportAccounts()
	for addr, account := range accounts {
		snapshot.Accounts[addr] = account
	}

	// Export validators
	validators := ss.worldState.ExportValidators()
	for addr, validator := range validators {
		snapshot.Validators[addr] = validator
	}

	// Export staking data
	stakes := ss.worldState.ExportStakes()
	snapshot.Stakes = stakes

	// Add metadata
	snapshot.Metadata["created_at"] = fmt.Sprintf("%d", time.Now().Unix())
	snapshot.Metadata["node_version"] = "thrylos-v2"
	snapshot.Metadata["accounts_count"] = fmt.Sprintf("%d", len(snapshot.Accounts))
	snapshot.Metadata["validators_count"] = fmt.Sprintf("%d", len(snapshot.Validators))

	// Calculate checksum
	snapshot.Checksum = ss.calculateChecksum(snapshot)

	// Compress data if configured
	if ss.config.StateSnapshot {
		compressedData, err := ss.compressSnapshot(snapshot)
		if err != nil {
			return nil, fmt.Errorf("failed to compress snapshot: %v", err)
		}
		snapshot.CompressedData = compressedData
		snapshot.Size = int64(len(compressedData))
	}

	return snapshot, nil
}

// Background processes

func (ss *StateSyncer) snapshotManager() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ss.cleanupOldSnapshots()
		case <-ss.ctx.Done():
			return
		}
	}
}

func (ss *StateSyncer) syncRequestProcessor() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ss.processTimeoutRequests()
		case <-ss.ctx.Done():
			return
		}
	}
}

func (ss *StateSyncer) metricsCollector() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ss.updateMetrics()
		case <-ss.ctx.Done():
			return
		}
	}
}

// Helper methods

func (ss *StateSyncer) requestSnapshotFromPeer(peerID string, height int64) (*StateSnapshot, error) {
	if ss.p2pNetwork == nil {
		return nil, fmt.Errorf("P2P network not available")
	}

	// This would be implemented based on your P2P protocol
	// For now, return a mock implementation
	return ss.p2pNetwork.RequestStateSnapshot(peerID, height)
}

func (ss *StateSyncer) validateSnapshot(snapshot *StateSnapshot) error {
	if snapshot == nil {
		return fmt.Errorf("snapshot is nil")
	}

	if snapshot.Height <= 0 {
		return fmt.Errorf("invalid snapshot height: %d", snapshot.Height)
	}

	if snapshot.StateRoot == "" {
		return fmt.Errorf("snapshot state root is empty")
	}

	// Verify checksum
	expectedChecksum := ss.calculateChecksum(snapshot)
	if snapshot.Checksum != expectedChecksum {
		return fmt.Errorf("snapshot checksum mismatch: expected %s, got %s",
			expectedChecksum, snapshot.Checksum)
	}

	// Additional validation
	if len(snapshot.Accounts) == 0 {
		return fmt.Errorf("snapshot contains no accounts")
	}

	return nil
}

func (ss *StateSyncer) applySnapshotData(snapshot *StateSnapshot) error {
	// Clear current state
	if err := ss.worldState.Clear(); err != nil {
		return fmt.Errorf("failed to clear world state: %v", err)
	}

	// Import accounts
	for addr, account := range snapshot.Accounts {
		if err := ss.worldState.SetAccount(addr, account); err != nil {
			return fmt.Errorf("failed to set account %s: %v", addr, err)
		}
	}

	// Import validators
	for addr, validator := range snapshot.Validators {
		if err := ss.worldState.SetValidator(addr, validator); err != nil {
			return fmt.Errorf("failed to set validator %s: %v", addr, err)
		}
	}

	// Import staking data
	for delegator, stakes := range snapshot.Stakes {
		for validator, amount := range stakes {
			if err := ss.worldState.SetStake(delegator, validator, amount); err != nil {
				return fmt.Errorf("failed to set stake %s->%s: %v", delegator, validator, err)
			}
		}
	}

	// Set state root and height
	if err := ss.worldState.SetStateRoot(snapshot.StateRoot); err != nil {
		return fmt.Errorf("failed to set state root: %v", err)
	}

	if err := ss.worldState.SetCurrentHeight(snapshot.Height); err != nil {
		return fmt.Errorf("failed to set current height: %v", err)
	}

	return nil
}

func (ss *StateSyncer) verifyStateConsistency(snapshot *StateSnapshot) error {
	// Verify state root matches
	currentStateRoot := ss.worldState.GetStateRoot()
	if currentStateRoot != snapshot.StateRoot {
		return fmt.Errorf("state root mismatch after applying snapshot")
	}

	// Verify height
	currentHeight := ss.worldState.GetCurrentHeight()
	if currentHeight != snapshot.Height {
		return fmt.Errorf("height mismatch after applying snapshot")
	}

	// Verify account balances
	for addr, expectedAccount := range snapshot.Accounts {
		currentAccount, err := ss.worldState.GetAccount(addr)
		if err != nil {
			return fmt.Errorf("failed to get account %s: %v", addr, err)
		}

		if currentAccount.Balance != expectedAccount.Balance {
			return fmt.Errorf("balance mismatch for account %s", addr)
		}
	}

	return nil
}

func (ss *StateSyncer) createStateBackup() *StateSnapshot {
	backup, err := ss.CreateSnapshot()
	if err != nil {
		fmt.Printf("⚠️ Failed to create state backup: %v\n", err)
		return nil
	}
	return backup
}

func (ss *StateSyncer) restoreFromBackup(backup *StateSnapshot) {
	if backup == nil {
		fmt.Printf("⚠️ No backup available for restore\n")
		return
	}

	if err := ss.ApplyStateSnapshot(backup); err != nil {
		fmt.Printf("❌ Failed to restore from backup: %v\n", err)
	} else {
		fmt.Printf("✅ State restored from backup\n")
	}
}

func (ss *StateSyncer) calculateChecksum(snapshot *StateSnapshot) string {
	// Simple checksum calculation - in production use proper hash
	checksum := fmt.Sprintf("%d-%s-%d", snapshot.Height, snapshot.StateRoot, snapshot.Timestamp)
	return checksum
}

func (ss *StateSyncer) compressSnapshot(snapshot *StateSnapshot) ([]byte, error) {
	// Implement compression logic here
	// For now, return empty data
	return []byte{}, nil
}

func (ss *StateSyncer) getAvailablePeers() []string {
	if ss.p2pNetwork == nil {
		return []string{}
	}
	return ss.p2pNetwork.GetConnectedPeerIDs()
}

func (ss *StateSyncer) getMaxPeerHeight(peers []string) int64 {
	maxHeight := int64(0)
	for _, peerID := range peers {
		if height, err := ss.p2pNetwork.RequestPeerHeight(peerID); err == nil {
			if height > maxHeight {
				maxHeight = height
			}
		}
	}
	return maxHeight
}

func (ss *StateSyncer) getBestPeerForSync(peers []string) string {
	if len(peers) == 0 {
		return ""
	}

	// For now, return first peer
	// In production, implement peer scoring
	return peers[0]
}

func (ss *StateSyncer) cleanupOldSnapshots() {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	// Remove snapshots older than 1 hour
	cutoff := time.Now().Add(-1 * time.Hour).Unix()
	for id, snapshot := range ss.snapshots {
		if snapshot.Timestamp < cutoff {
			delete(ss.snapshots, id)
		}
	}
}

func (ss *StateSyncer) processTimeoutRequests() {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	timeout := 5 * time.Minute
	now := time.Now()

	for id, request := range ss.syncRequests {
		if request.Status == StateSyncPending && now.Sub(request.RequestedAt) > timeout {
			request.Status = StateSyncFailed
			request.Error = fmt.Errorf("request timeout")
			ss.syncErrors++
		}
	}
}

func (ss *StateSyncer) updateMetrics() {
	// Update internal metrics
	ss.mu.Lock()
	defer ss.mu.Unlock()

	// Calculate average snapshot size
	if ss.snapshotsReceived > 0 {
		// This is a simplified calculation
		// In production, maintain a rolling average
	}
}

// Public API methods

func (ss *StateSyncer) IsFastSyncEnabled() bool {
	return ss.config.FastSync
}

func (ss *StateSyncer) IsStateSyncNeeded() bool {
	// Determine if state sync is needed based on current state
	currentHeight := ss.worldState.GetCurrentHeight()

	// Check if we have peers with higher height
	peers := ss.getAvailablePeers()
	maxPeerHeight := ss.getMaxPeerHeight(peers)

	return maxPeerHeight > currentHeight+100 // Sync if 100+ blocks behind
}

func (ss *StateSyncer) GetSyncMetrics() *StateSyncMetrics {
	ss.mu.RLock()
	defer ss.mu.RUnlock()

	totalRequests := ss.snapshotsReceived + ss.syncErrors

	return &StateSyncMetrics{
		TotalRequests:       totalRequests,
		SuccessfulSyncs:     ss.snapshotsApplied,
		FailedSyncs:         ss.syncErrors,
		AverageSnapshotSize: ss.lastSnapshotSize,
		LastSyncTime:        ss.lastSyncTime,
	}
}

func (ss *StateSyncer) GetStats() map[string]interface{} {
	ss.mu.RLock()
	defer ss.mu.RUnlock()

	return map[string]interface{}{
		"is_running":         ss.isRunning,
		"is_syncing":         ss.isSyncing,
		"snapshots_received": ss.snapshotsReceived,
		"snapshots_applied":  ss.snapshotsApplied,
		"sync_errors":        ss.syncErrors,
		"last_snapshot_size": ss.lastSnapshotSize,
		"last_sync_time":     ss.lastSyncTime,
		"active_requests":    len(ss.syncRequests),
		"cached_snapshots":   len(ss.snapshots),
	}
}

func (ss *StateSyncer) IsSyncing() bool {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	return ss.isSyncing
}

func (ss *StateSyncer) GetLastSyncTime() time.Time {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	return ss.lastSyncTime
}

// StateComparator compares states between peers
type StateComparator struct {
	worldState *state.WorldState
}

func NewStateComparator(worldState *state.WorldState) *StateComparator {
	return &StateComparator{
		worldState: worldState,
	}
}

func (sc *StateComparator) CompareStates(localSnapshot, remoteSnapshot *StateSnapshot) *StateComparisonResult {
	result := &StateComparisonResult{
		LocalHeight:    localSnapshot.Height,
		RemoteHeight:   remoteSnapshot.Height,
		AccountDiffs:   make(map[string]*AccountDiff),
		ValidatorDiffs: make(map[string]*ValidatorDiff),
		Timestamp:      time.Now(),
	}

	// Compare heights
	result.HeightDiff = remoteSnapshot.Height - localSnapshot.Height

	// Compare accounts
	sc.compareAccounts(localSnapshot.Accounts, remoteSnapshot.Accounts, result)

	// Compare validators
	sc.compareValidators(localSnapshot.Validators, remoteSnapshot.Validators, result)

	// Compare state roots
	result.StateRootMatch = localSnapshot.StateRoot == remoteSnapshot.StateRoot

	return result
}

func (sc *StateComparator) compareAccounts(
	localAccounts, remoteAccounts map[string]*core.Account,
	result *StateComparisonResult,
) {
	// Check for differences in accounts
	allAddresses := make(map[string]bool)

	// Collect all addresses
	for addr := range localAccounts {
		allAddresses[addr] = true
	}
	for addr := range remoteAccounts {
		allAddresses[addr] = true
	}

	// Compare each account
	for addr := range allAddresses {
		localAcc, hasLocal := localAccounts[addr]
		remoteAcc, hasRemote := remoteAccounts[addr]

		if !hasLocal && hasRemote {
			// Account only exists remotely
			result.AccountDiffs[addr] = &AccountDiff{
				Type:          DiffTypeAdded,
				RemoteAccount: remoteAcc,
			}
		} else if hasLocal && !hasRemote {
			// Account only exists locally
			result.AccountDiffs[addr] = &AccountDiff{
				Type:         DiffTypeRemoved,
				LocalAccount: localAcc,
			}
		} else if hasLocal && hasRemote {
			// Account exists in both, check for differences
			if localAcc.Balance != remoteAcc.Balance || localAcc.Nonce != remoteAcc.Nonce {
				result.AccountDiffs[addr] = &AccountDiff{
					Type:          DiffTypeModified,
					LocalAccount:  localAcc,
					RemoteAccount: remoteAcc,
				}
			}
		}
	}
}

func (sc *StateComparator) compareValidators(
	localValidators, remoteValidators map[string]*core.Validator,
	result *StateComparisonResult,
) {
	// Similar logic to compareAccounts but for validators
	allAddresses := make(map[string]bool)

	for addr := range localValidators {
		allAddresses[addr] = true
	}
	for addr := range remoteValidators {
		allAddresses[addr] = true
	}

	for addr := range allAddresses {
		localVal, hasLocal := localValidators[addr]
		remoteVal, hasRemote := remoteValidators[addr]

		if !hasLocal && hasRemote {
			result.ValidatorDiffs[addr] = &ValidatorDiff{
				Type:            DiffTypeAdded,
				RemoteValidator: remoteVal,
			}
		} else if hasLocal && !hasRemote {
			result.ValidatorDiffs[addr] = &ValidatorDiff{
				Type:           DiffTypeRemoved,
				LocalValidator: localVal,
			}
		} else if hasLocal && hasRemote {
			if sc.validatorsDiffer(localVal, remoteVal) {
				result.ValidatorDiffs[addr] = &ValidatorDiff{
					Type:            DiffTypeModified,
					LocalValidator:  localVal,
					RemoteValidator: remoteVal,
				}
			}
		}
	}
}

func (sc *StateComparator) validatorsDiffer(local, remote *core.Validator) bool {
	return local.Stake != remote.Stake ||
		local.DelegatedStake != remote.DelegatedStake ||
		local.Commission != remote.Commission ||
		local.Active != remote.Active
}

// Data structures for state comparison

type StateComparisonResult struct {
	LocalHeight    int64
	RemoteHeight   int64
	HeightDiff     int64
	StateRootMatch bool
	AccountDiffs   map[string]*AccountDiff
	ValidatorDiffs map[string]*ValidatorDiff
	Timestamp      time.Time
}

type DiffType int

const (
	DiffTypeAdded DiffType = iota
	DiffTypeRemoved
	DiffTypeModified
)

type AccountDiff struct {
	Type          DiffType
	LocalAccount  *core.Account
	RemoteAccount *core.Account
}

type ValidatorDiff struct {
	Type            DiffType
	LocalValidator  *core.Validator
	RemoteValidator *core.Validator
}

// StatePruner handles pruning of old state data
type StatePruner struct {
	worldState *state.WorldState
	config     *PruningConfig
	isRunning  bool
	ctx        context.Context
	cancelFunc context.CancelFunc
	mu         sync.RWMutex
}

type PruningConfig struct {
	Enabled          bool
	RetentionBlocks  int64
	PruningInterval  time.Duration
	MaxStatesToPrune int
	PreservSnapshots bool
}

func NewStatePruner(worldState *state.WorldState, config *PruningConfig) *StatePruner {
	ctx, cancelFunc := context.WithCancel(context.Background())

	return &StatePruner{
		worldState: worldState,
		config:     config,
		ctx:        ctx,
		cancelFunc: cancelFunc,
	}
}

func (sp *StatePruner) Start() error {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	if !sp.config.Enabled {
		return nil // Pruning disabled
	}

	if sp.isRunning {
		return fmt.Errorf("state pruner already running")
	}

	go sp.pruningLoop()

	sp.isRunning = true
	fmt.Println("🗑️ State pruner started")

	return nil
}

func (sp *StatePruner) Stop() error {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	if !sp.isRunning {
		return nil
	}

	if sp.cancelFunc != nil {
		sp.cancelFunc()
	}

	sp.isRunning = false
	fmt.Println("🛑 State pruner stopped")

	return nil
}

func (sp *StatePruner) pruningLoop() {
	ticker := time.NewTicker(sp.config.PruningInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := sp.pruneOldStates(); err != nil {
				fmt.Printf("⚠️ State pruning failed: %v\n", err)
			}
		case <-sp.ctx.Done():
			return
		}
	}
}

func (sp *StatePruner) pruneOldStates() error {
	currentHeight := sp.worldState.GetCurrentHeight()
	pruneBeforeHeight := currentHeight - sp.config.RetentionBlocks

	if pruneBeforeHeight <= 0 {
		return nil // Nothing to prune yet
	}

	fmt.Printf("🗑️ Pruning state data before height %d\n", pruneBeforeHeight)

	// Prune old state data
	pruned, err := sp.worldState.PruneStatesBefore(pruneBeforeHeight)
	if err != nil {
		return fmt.Errorf("failed to prune states: %v", err)
	}

	fmt.Printf("✅ Pruned %d old state entries\n", pruned)
	return nil
}

// StateRecovery handles state recovery from corruption
type StateRecovery struct {
	worldState *state.WorldState
	backups    map[int64]*StateSnapshot
	mu         sync.RWMutex
}

func NewStateRecovery(worldState *state.WorldState) *StateRecovery {
	return &StateRecovery{
		worldState: worldState,
		backups:    make(map[int64]*StateSnapshot),
	}
}

func (sr *StateRecovery) CreateRecoveryPoint(height int64) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	// Create snapshot for recovery
	stateSyncer := NewStateSyncer(sr.worldState, nil, &SyncConfig{})
	snapshot, err := stateSyncer.CreateSnapshot()
	if err != nil {
		return fmt.Errorf("failed to create recovery snapshot: %v", err)
	}

	sr.backups[height] = snapshot

	// Keep only last 5 recovery points
	if len(sr.backups) > 5 {
		oldestHeight := int64(0)
		for h := range sr.backups {
			if oldestHeight == 0 || h < oldestHeight {
				oldestHeight = h
			}
		}
		delete(sr.backups, oldestHeight)
	}

	fmt.Printf("💾 Created recovery point at height %d\n", height)
	return nil
}

func (sr *StateRecovery) RecoverToHeight(height int64) error {
	sr.mu.RLock()
	snapshot, exists := sr.backups[height]
	sr.mu.RUnlock()

	if !exists {
		return fmt.Errorf("no recovery point available for height %d", height)
	}

	stateSyncer := NewStateSyncer(sr.worldState, nil, &SyncConfig{})
	if err := stateSyncer.ApplyStateSnapshot(snapshot); err != nil {
		return fmt.Errorf("failed to recover to height %d: %v", height, err)
	}

	fmt.Printf("🔄 Recovered state to height %d\n", height)
	return nil
}

func (sr *StateRecovery) GetAvailableRecoveryPoints() []int64 {
	sr.mu.RLock()
	defer sr.mu.RUnlock()

	heights := make([]int64, 0, len(sr.backups))
	for h := range sr.backups {
		heights = append(heights, h)
	}

	return heights
}
