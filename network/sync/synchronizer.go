package sync

// network/sync/synchronizer.go - Block synchronization implementation
import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/thrylos-labs/go-thrylos/core/chain"
	"github.com/thrylos-labs/go-thrylos/network"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// Synchronizer handles block synchronization between peers
type Synchronizer struct {
	blockchain *chain.Blockchain
	p2pNetwork *network.P2PNetwork
	config     *SyncConfig

	// Request management
	activeRequests map[string]*BlockRequest
	requestQueue   chan *BlockRequest
	responseQueue  chan *BlockResponse

	// State
	isRunning  bool
	ctx        context.Context
	cancelFunc context.CancelFunc
	mu         sync.RWMutex

	// Metrics
	totalRequests   int64
	successfulReqs  int64
	failedRequests  int64
	avgResponseTime time.Duration
}

// SyncConfig contains synchronization configuration
type SyncConfig struct {
	MaxConcurrentRequests int
	RequestTimeout        time.Duration
	MaxBlocksPerRequest   int
	SyncInterval          time.Duration
	StateSnapshot         bool
	FastSync              bool
	PruneOldStates        bool
}

// BlockRequest represents a request for blocks
type BlockRequest struct {
	ID         string
	PeerID     string
	StartBlock int64
	EndBlock   int64
	Timestamp  time.Time
	Retries    int
	MaxRetries int
	Response   chan *BlockResponse
}

// BlockResponse represents a response to a block request
type BlockResponse struct {
	RequestID string
	PeerID    string
	Blocks    []*core.Block
	Error     error
	Timestamp time.Time
}

// BlockSyncStatus represents the status of block synchronization
type BlockSyncStatus struct {
	IsActive        bool
	StartHeight     int64
	CurrentHeight   int64
	TargetHeight    int64
	SyncedBlocks    int64
	PendingRequests int
	LastBlockTime   time.Time
	SyncSpeed       float64 // blocks per second
}

// NewSynchronizer creates a new block synchronizer
func NewSynchronizer(
	blockchain *chain.Blockchain,
	p2pNetwork *network.P2PNetwork,
	config *SyncConfig,
) *Synchronizer {
	ctx, cancelFunc := context.WithCancel(context.Background())

	return &Synchronizer{
		blockchain:     blockchain,
		p2pNetwork:     p2pNetwork,
		config:         config,
		activeRequests: make(map[string]*BlockRequest),
		requestQueue:   make(chan *BlockRequest, 100),
		responseQueue:  make(chan *BlockResponse, 100),
		ctx:            ctx,
		cancelFunc:     cancelFunc,
	}
}

// Start begins the synchronization process
func (s *Synchronizer) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isRunning {
		return fmt.Errorf("synchronizer already running")
	}

	// Start worker goroutines
	for i := 0; i < s.config.MaxConcurrentRequests; i++ {
		go s.requestWorker()
	}

	go s.responseHandler()
	go s.timeoutMonitor()

	s.isRunning = true
	fmt.Println("📡 Block synchronizer started")

	return nil
}

// Stop gracefully shuts down the synchronizer
func (s *Synchronizer) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isRunning {
		return nil
	}

	if s.cancelFunc != nil {
		s.cancelFunc()
	}

	// Close channels
	close(s.requestQueue)
	close(s.responseQueue)

	s.isRunning = false
	fmt.Println("🛑 Block synchronizer stopped")

	return nil
}

// RequestBlocks requests a range of blocks from a specific peer
func (s *Synchronizer) RequestBlocks(peerID string, startBlock, endBlock int64) ([]*core.Block, error) {
	if !s.isRunning {
		return nil, fmt.Errorf("synchronizer not running")
	}

	// Validate request
	if startBlock > endBlock {
		return nil, fmt.Errorf("invalid block range: start=%d end=%d", startBlock, endBlock)
	}

	if endBlock-startBlock > int64(s.config.MaxBlocksPerRequest) {
		return nil, fmt.Errorf("request too large: max %d blocks", s.config.MaxBlocksPerRequest)
	}

	// Create request
	req := &BlockRequest{
		ID:         fmt.Sprintf("%s-%d-%d-%d", peerID, startBlock, endBlock, time.Now().UnixNano()),
		PeerID:     peerID,
		StartBlock: startBlock,
		EndBlock:   endBlock,
		Timestamp:  time.Now(),
		MaxRetries: 3,
		Response:   make(chan *BlockResponse, 1),
	}

	// Add to active requests
	s.mu.Lock()
	s.activeRequests[req.ID] = req
	s.totalRequests++
	s.mu.Unlock()

	// Queue request
	select {
	case s.requestQueue <- req:
	case <-time.After(5 * time.Second):
		s.removeActiveRequest(req.ID)
		return nil, fmt.Errorf("request queue full")
	case <-s.ctx.Done():
		s.removeActiveRequest(req.ID)
		return nil, fmt.Errorf("synchronizer shutting down")
	}

	// Wait for response
	select {
	case response := <-req.Response:
		s.removeActiveRequest(req.ID)
		if response.Error != nil {
			s.mu.Lock()
			s.failedRequests++
			s.mu.Unlock()
			return nil, response.Error
		}

		s.mu.Lock()
		s.successfulReqs++
		responseTime := time.Since(req.Timestamp)
		s.updateAverageResponseTime(responseTime)
		s.mu.Unlock()

		return response.Blocks, nil

	case <-time.After(s.config.RequestTimeout):
		s.removeActiveRequest(req.ID)
		s.mu.Lock()
		s.failedRequests++
		s.mu.Unlock()
		return nil, fmt.Errorf("request timeout")

	case <-s.ctx.Done():
		s.removeActiveRequest(req.ID)
		return nil, fmt.Errorf("synchronizer shutting down")
	}
}

// RequestSingleBlock requests a single block by hash or height
func (s *Synchronizer) RequestSingleBlock(peerID string, identifier string) (*core.Block, error) {
	// For now, assume identifier is height as string
	// In production, you'd need to handle both hash and height

	// This is a simplified implementation
	// You would implement specific single block request protocol
	blocks, err := s.RequestBlocks(peerID, 0, 1) // Placeholder
	if err != nil {
		return nil, err
	}

	if len(blocks) == 0 {
		return nil, fmt.Errorf("block not found")
	}

	return blocks[0], nil
}

// SyncToHeight synchronizes blockchain to a specific height
func (s *Synchronizer) SyncToHeight(targetHeight int64, preferredPeers []string) error {
	currentHeight := s.blockchain.GetHeight()

	if currentHeight >= targetHeight {
		return nil // Already at or past target
	}

	fmt.Printf("🔄 Syncing from height %d to %d (%d blocks)\n",
		currentHeight, targetHeight, targetHeight-currentHeight)

	// Sync in batches
	for currentHeight < targetHeight {
		// Calculate batch size
		remaining := targetHeight - currentHeight
		batchSize := int64(s.config.MaxBlocksPerRequest)
		if remaining < batchSize {
			batchSize = remaining
		}

		// Try each preferred peer
		var blocks []*core.Block
		var err error

		for _, peerID := range preferredPeers {
			blocks, err = s.RequestBlocks(peerID, currentHeight+1, currentHeight+batchSize)
			if err == nil {
				break
			}
			fmt.Printf("⚠️ Failed to get blocks from peer %s: %v\n", peerID, err)
		}

		if err != nil {
			return fmt.Errorf("failed to get blocks from any peer: %v", err)
		}

		// Process blocks
		for _, block := range blocks {
			if err := s.blockchain.AddBlock(block); err != nil {
				return fmt.Errorf("failed to add block %d: %v", block.Header.Index, err)
			}
			currentHeight = block.Header.Index
		}

		// Progress update
		progress := float64(currentHeight-s.blockchain.GetHeight()) /
			float64(targetHeight-s.blockchain.GetHeight()) * 100
		fmt.Printf("📊 Sync progress: %.1f%% (%d/%d)\n", progress, currentHeight, targetHeight)
	}

	fmt.Printf("✅ Sync completed to height %d\n", targetHeight)
	return nil
}

// Worker goroutines

func (s *Synchronizer) requestWorker() {
	for {
		select {
		case req := <-s.requestQueue:
			s.processBlockRequest(req)
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *Synchronizer) processBlockRequest(req *BlockRequest) {
	// Send request to peer via P2P network
	blocks, err := s.sendBlockRequestToPeer(req)

	response := &BlockResponse{
		RequestID: req.ID,
		PeerID:    req.PeerID,
		Blocks:    blocks,
		Error:     err,
		Timestamp: time.Now(),
	}

	// Send response
	select {
	case s.responseQueue <- response:
	case <-time.After(1 * time.Second):
		// Response channel full, drop response
		fmt.Printf("⚠️ Response queue full, dropping response for %s\n", req.ID)
	case <-s.ctx.Done():
		return
	}
}

func (s *Synchronizer) sendBlockRequestToPeer(req *BlockRequest) ([]*core.Block, error) {
	if s.p2pNetwork == nil {
		return nil, fmt.Errorf("P2P network not available")
	}

	// Use P2P network to request blocks
	// This would be implemented based on your P2P protocol
	return s.p2pNetwork.RequestBlockRange(req.PeerID, req.StartBlock, req.EndBlock)
}

func (s *Synchronizer) responseHandler() {
	for {
		select {
		case response := <-s.responseQueue:
			s.handleBlockResponse(response)
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *Synchronizer) handleBlockResponse(response *BlockResponse) {
	s.mu.RLock()
	req, exists := s.activeRequests[response.RequestID]
	s.mu.RUnlock()

	if !exists {
		// Request may have timed out or been cancelled
		return
	}

	// Send response to waiting request
	select {
	case req.Response <- response:
	case <-time.After(1 * time.Second):
		// Request may have timed out
	}
}

func (s *Synchronizer) timeoutMonitor() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.cleanupTimedOutRequests()
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *Synchronizer) cleanupTimedOutRequests() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for id, req := range s.activeRequests {
		if now.Sub(req.Timestamp) > s.config.RequestTimeout {
			delete(s.activeRequests, id)
			s.failedRequests++

			// Send timeout error
			select {
			case req.Response <- &BlockResponse{
				RequestID: req.ID,
				PeerID:    req.PeerID,
				Error:     fmt.Errorf("request timeout"),
				Timestamp: now,
			}:
			default:
			}
		}
	}
}

// Utility methods

func (s *Synchronizer) removeActiveRequest(requestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.activeRequests, requestID)
}

func (s *Synchronizer) updateAverageResponseTime(responseTime time.Duration) {
	// Simple moving average
	if s.avgResponseTime == 0 {
		s.avgResponseTime = responseTime
	} else {
		s.avgResponseTime = (s.avgResponseTime + responseTime) / 2
	}
}

// BlockValidator validates blocks before adding to blockchain
type BlockValidator struct {
	blockchain *chain.Blockchain
}

func NewBlockValidator(blockchain *chain.Blockchain) *BlockValidator {
	return &BlockValidator{
		blockchain: blockchain,
	}
}

func (bv *BlockValidator) ValidateBlock(block *core.Block) error {
	// Check if block already exists
	if existingBlock, _ := bv.blockchain.GetBlock(block.Hash); existingBlock != nil {
		return fmt.Errorf("block already exists: %s", block.Hash)
	}

	// Validate block height sequence
	currentHeight := bv.blockchain.GetHeight()
	expectedHeight := currentHeight + 1
	if block.Header.Index != expectedHeight {
		return fmt.Errorf("invalid block height: expected %d, got %d",
			expectedHeight, block.Header.Index)
	}

	// Validate previous block hash
	if currentHeight >= 0 {
		currentBlock := bv.blockchain.GetCurrentBlock()
		if currentBlock != nil && block.Header.PrevHash != currentBlock.Hash {
			return fmt.Errorf("invalid previous block hash: expected %s, got %s",
				currentBlock.Hash, block.Header.PrevHash)
		}
	}

	// Validate block timestamp
	now := time.Now().Unix()
	if block.Header.Timestamp > now+300 { // Allow 5 minutes in future
		return fmt.Errorf("block timestamp too far in future")
	}

	// Validate transactions in block
	for _, tx := range block.Transactions {
		if err := bv.validateTransaction(tx); err != nil {
			return fmt.Errorf("invalid transaction %s: %v", tx.Id, err)
		}
	}

	return nil
}

func (bv *BlockValidator) validateTransaction(tx *core.Transaction) error {
	// Basic transaction validation
	if tx.Id == "" {
		return fmt.Errorf("transaction ID is empty")
	}

	if tx.From == "" {
		return fmt.Errorf("transaction from address is empty")
	}

	if tx.To == "" {
		return fmt.Errorf("transaction to address is empty")
	}

	if tx.Amount <= 0 {
		return fmt.Errorf("transaction amount must be positive")
	}

	// Additional validation would go here
	// - Signature validation
	// - Balance validation
	// - Nonce validation
	// etc.

	return nil
}

// BatchSynchronizer handles batch synchronization operations
type BatchSynchronizer struct {
	synchronizer *Synchronizer
	batchSize    int
	concurrency  int
}

func NewBatchSynchronizer(synchronizer *Synchronizer, batchSize, concurrency int) *BatchSynchronizer {
	return &BatchSynchronizer{
		synchronizer: synchronizer,
		batchSize:    batchSize,
		concurrency:  concurrency,
	}
}

func (bs *BatchSynchronizer) SyncBlockRange(startHeight, endHeight int64, peers []string) error {
	totalBlocks := endHeight - startHeight + 1

	// Create batches
	batches := make([]BatchRequest, 0)
	for start := startHeight; start <= endHeight; start += int64(bs.batchSize) {
		end := start + int64(bs.batchSize) - 1
		if end > endHeight {
			end = endHeight
		}

		batches = append(batches, BatchRequest{
			StartHeight: start,
			EndHeight:   end,
		})
	}

	fmt.Printf("📦 Created %d batches for %d blocks\n", len(batches), totalBlocks)

	// Process batches concurrently
	results := make(chan BatchResult, len(batches))
	semaphore := make(chan struct{}, bs.concurrency)

	for _, batch := range batches {
		go func(b BatchRequest) {
			semaphore <- struct{}{}        // Acquire
			defer func() { <-semaphore }() // Release

			result := bs.processBatch(b, peers)
			results <- result
		}(batch)
	}

	// Collect results
	successCount := 0
	failureCount := 0

	for i := 0; i < len(batches); i++ {
		result := <-results
		if result.Error != nil {
			fmt.Printf("❌ Batch %d-%d failed: %v\n",
				result.StartHeight, result.EndHeight, result.Error)
			failureCount++
		} else {
			fmt.Printf("✅ Batch %d-%d completed (%d blocks)\n",
				result.StartHeight, result.EndHeight, len(result.Blocks))
			successCount++
		}
	}

	fmt.Printf("📊 Batch sync completed: %d success, %d failed\n", successCount, failureCount)

	if failureCount > 0 {
		return fmt.Errorf("batch sync partially failed: %d/%d batches failed",
			failureCount, len(batches))
	}

	return nil
}

type BatchRequest struct {
	StartHeight int64
	EndHeight   int64
}

type BatchResult struct {
	StartHeight int64
	EndHeight   int64
	Blocks      []*core.Block
	Error       error
}

func (bs *BatchSynchronizer) processBatch(batch BatchRequest, peers []string) BatchResult {
	// Try each peer until successful
	for _, peerID := range peers {
		blocks, err := bs.synchronizer.RequestBlocks(peerID, batch.StartHeight, batch.EndHeight)
		if err == nil {
			return BatchResult{
				StartHeight: batch.StartHeight,
				EndHeight:   batch.EndHeight,
				Blocks:      blocks,
				Error:       nil,
			}
		}

		fmt.Printf("⚠️ Peer %s failed for batch %d-%d: %v\n",
			peerID, batch.StartHeight, batch.EndHeight, err)
	}

	return BatchResult{
		StartHeight: batch.StartHeight,
		EndHeight:   batch.EndHeight,
		Error:       fmt.Errorf("all peers failed for batch %d-%d", batch.StartHeight, batch.EndHeight),
	}
}

// Public API methods

func (s *Synchronizer) GetStats() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return map[string]interface{}{
		"is_running":          s.isRunning,
		"total_requests":      s.totalRequests,
		"successful_requests": s.successfulReqs,
		"failed_requests":     s.failedRequests,
		"active_requests":     len(s.activeRequests),
		"avg_response_time":   s.avgResponseTime.String(),
		"success_rate":        float64(s.successfulReqs) / float64(s.totalRequests) * 100,
	}
}

func (s *Synchronizer) GetActiveRequests() map[string]*BlockRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()

	requests := make(map[string]*BlockRequest)
	for k, v := range s.activeRequests {
		requests[k] = v
	}
	return requests
}

func (s *Synchronizer) IsHealthy() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.isRunning {
		return false
	}

	// Check if success rate is reasonable
	if s.totalRequests > 10 {
		successRate := float64(s.successfulReqs) / float64(s.totalRequests)
		return successRate > 0.7 // 70% success rate threshold
	}

	return true
}
