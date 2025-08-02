package node

// node/node.go - Main blockchain node with PoS integration and WorldState

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/consensus/pos"
	"github.com/thrylos-labs/go-thrylos/consensus/rewards"
	"github.com/thrylos-labs/go-thrylos/consensus/validator"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/core/chain"
	"github.com/thrylos-labs/go-thrylos/core/state"
	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/network"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"golang.org/x/crypto/blake2b"
)

// Node represents a blockchain node with PoS consensus and comprehensive state management
type Node struct {
	// Core components
	config     *config.Config
	worldState *state.WorldState
	blockchain *chain.Blockchain

	// PoS consensus components
	consensusEngine   *pos.ConsensusEngine
	validatorManager  *validator.Manager
	rewardDistributor *rewards.Distributor
	inflationManager  *rewards.InflationManager

	// Node identity and configuration
	nodePrivateKey  crypto.PrivateKey
	nodeAddress     string
	shardID         account.ShardID
	totalShards     int
	isValidatorNode bool

	// Networking (simplified for now)
	broadcastChan chan interface{}
	receiveChan   chan interface{}

	// P2P Networking
	p2pNetwork *network.P2PNetwork

	// State management
	isRunning           bool
	lastEpoch           uint64
	lastRewardTime      time.Time
	blockProcessingRate float64

	// Cross-shard support
	crossShardEnabled bool

	// Synchronization
	mu sync.RWMutex

	// Event handlers
	eventHandlers map[string][]func(interface{})

	// ADD THESE NEW FIELDS for graceful shutdown:
	ctx        context.Context
	cancelFunc context.CancelFunc
}

// NodeConfig represents comprehensive node configuration
type NodeConfig struct {
	Config            *config.Config
	PrivateKey        crypto.PrivateKey
	ShardID           account.ShardID
	TotalShards       int
	IsValidator       bool
	DataDir           string
	CrossShardEnabled bool
	GenesisAccount    string
	GenesisSupply     int64
	GenesisValidators []*core.Validator
	EnableP2P         bool
	P2PListenPort     int
	BootstrapPeers    []string
}

// NewNode creates a new blockchain node with full WorldState integration
func NewNode(nodeConfig *NodeConfig) (*Node, error) {
	if nodeConfig == nil {
		return nil, fmt.Errorf("node config cannot be nil")
	}

	// Generate node address from private key
	nodeAddress, err := account.GenerateAddress(nodeConfig.PrivateKey.PublicKey())
	if err != nil {
		return nil, fmt.Errorf("failed to generate node address: %v", err)
	}

	// Initialize WorldState with shard configuration
	worldState := state.NewWorldState(nodeConfig.ShardID, nodeConfig.TotalShards, nodeConfig.Config)

	// Initialize Blockchain with WorldState
	blockchainConfig := &chain.BlockchainConfig{
		Config:            nodeConfig.Config,
		WorldState:        worldState,
		ShardID:           nodeConfig.ShardID,
		TotalShards:       nodeConfig.TotalShards,
		MaxReorgDepth:     100,
		CrossShardEnabled: nodeConfig.CrossShardEnabled,
	}

	bc, err := chain.NewBlockchain(blockchainConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create blockchain: %v", err)
	}

	// Initialize PoS components
	validatorManager := validator.NewManager(nodeConfig.Config, worldState)
	rewardDistributor := rewards.NewDistributor(nodeConfig.Config, worldState)
	inflationManager := rewards.NewInflationManager(nodeConfig.Config, worldState)

	// Initialize networking channels
	broadcastChan := make(chan interface{}, 1000)
	receiveChan := make(chan interface{}, 1000)

	// Initialize consensus engine
	consensusEngine := pos.NewConsensusEngine(
		nodeConfig.Config,
		worldState,
		nodeConfig.PrivateKey,
		broadcastChan,
		receiveChan,
	)

	// Set consensus engine in blockchain
	bc.SetConsensusEngine(consensusEngine)

	// Initialize P2P network if enabled
	var p2pNetwork *network.P2PNetwork
	if nodeConfig.EnableP2P {
		// Since your config now has P2P field, you can use the simple method
		p2pNet, err := network.NewP2PNetwork(nodeConfig.Config)
		if err != nil {
			return nil, fmt.Errorf("failed to create P2P network: %v", err)
		}
		p2pNetwork = p2pNet
	}

	node := &Node{
		config:            nodeConfig.Config,
		worldState:        worldState,
		blockchain:        bc,
		p2pNetwork:        p2pNetwork,
		consensusEngine:   consensusEngine,
		validatorManager:  validatorManager,
		rewardDistributor: rewardDistributor,
		inflationManager:  inflationManager,
		nodePrivateKey:    nodeConfig.PrivateKey,
		nodeAddress:       nodeAddress,
		shardID:           nodeConfig.ShardID,
		totalShards:       nodeConfig.TotalShards,
		isValidatorNode:   nodeConfig.IsValidator,
		crossShardEnabled: nodeConfig.CrossShardEnabled,
		broadcastChan:     broadcastChan,
		receiveChan:       receiveChan,
		lastRewardTime:    time.Now(),
		eventHandlers:     make(map[string][]func(interface{})),
	}

	// Store genesis configuration for initialization
	node.storeGenesisConfig(nodeConfig)

	return node, nil
}

// Start starts the blockchain node with full initialization
func (n *Node) Start() error {
	if n.isRunning {
		return fmt.Errorf("node is already running")
	}

	// Initialize genesis state if needed
	if err := n.initializeGenesis(); err != nil {
		return fmt.Errorf("failed to initialize genesis: %v", err)
	}

	// Register this node as validator if configured
	if n.isValidatorNode {
		if err := n.registerAsValidator(); err != nil {
			return fmt.Errorf("failed to register as validator: %v", err)
		}
	}

	// Start consensus engine
	if err := n.consensusEngine.Start(); err != nil {
		return fmt.Errorf("failed to start consensus engine: %v", err)
	}

	// Start P2P network if enabled
	if n.p2pNetwork != nil {
		if err := n.p2pNetwork.Start(); err != nil {
			return fmt.Errorf("failed to start P2P network: %v", err)
		}

		// Start P2P message processing
		go n.processP2PMessages()
	}

	// Start background processes
	go n.rewardDistributionLoop()
	go n.blockProductionLoop()
	go n.networkingLoop()
	go n.crossShardLoop()
	go n.maintenanceLoop()

	// Start event processing
	go n.eventProcessingLoop()

	n.isRunning = true
	n.triggerEvent("node_started", map[string]interface{}{
		"address":     n.nodeAddress,
		"shard_id":    n.shardID,
		"validator":   n.isValidatorNode,
		"cross_shard": n.crossShardEnabled,
		"p2p_enabled": n.p2pNetwork != nil,
	})

	fmt.Printf("Node started successfully:\n")
	fmt.Printf("  Address: %s\n", n.nodeAddress)
	fmt.Printf("  Shard: %d/%d\n", n.shardID, n.totalShards)
	fmt.Printf("  Validator: %t\n", n.isValidatorNode)
	fmt.Printf("  Cross-shard: %t\n", n.crossShardEnabled)
	if n.p2pNetwork != nil {
		fmt.Printf("  P2P: enabled on port %d\n", n.config.P2P.ListenPort)
	} else {
		fmt.Printf("  P2P: disabled\n")
	}

	return nil
}

// / Update your Stop method to stop P2P services:
func (n *Node) Stop() error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if !n.isRunning {
		return fmt.Errorf("node is not running")
	}

	fmt.Println("🛑 Stopping node gracefully...")

	// Cancel all goroutines
	if n.cancelFunc != nil {
		n.cancelFunc()
	}

	// Stop P2P network
	if n.p2pNetwork != nil {
		if err := n.p2pNetwork.Stop(); err != nil {
			fmt.Printf("Error stopping P2P network: %v\n", err)
		}
	}

	// Stop consensus engine
	if err := n.consensusEngine.Stop(); err != nil {
		return fmt.Errorf("failed to stop consensus engine: %v", err)
	}

	// Give goroutines time to stop
	time.Sleep(2 * time.Second)

	// Perform final cleanup
	n.blockchain.Cleanup()
	n.worldState.Cleanup()

	n.isRunning = false

	fmt.Println("✅ Node stopped gracefully")
	return nil
}

// SubmitTransaction submits a transaction to the network
func (n *Node) SubmitTransaction(tx *core.Transaction) error {
	if tx == nil {
		return fmt.Errorf("transaction cannot be nil")
	}

	// Add transaction through blockchain
	if err := n.blockchain.AddTransaction(tx); err != nil {
		return fmt.Errorf("failed to submit transaction: %v", err)
	}

	// Broadcast transaction to P2P network
	if err := n.BroadcastTransaction(tx); err != nil {
		fmt.Printf("Failed to broadcast transaction to P2P network: %v\n", err)
	}

	n.triggerEvent("transaction_submitted", tx)
	return nil
}

func (n *Node) processP2PMessages() {
	if n.p2pNetwork == nil {
		return
	}

	for {
		select {
		case block := <-n.p2pNetwork.BlockChan:
			// Process received block
			if err := n.blockchain.AddBlock(block); err != nil {
				fmt.Printf("Failed to process P2P block: %v\n", err)
			} else {
				fmt.Printf("Processed block %s from P2P network\n", block.Hash)
			}

		case tx := <-n.p2pNetwork.TransactionChan:
			// Process received transaction
			if err := n.blockchain.AddTransaction(tx); err != nil {
				fmt.Printf("Failed to process P2P transaction: %v\n", err)
			} else {
				fmt.Printf("Processed transaction %s from P2P network\n", tx.Id)
			}

		// case attestation := <-n.p2pNetwork.AttestationChan:
		// 	// Forward attestation to consensus engine
		// 	if n.consensusEngine != nil {
		// 		fmt.Printf("Received attestation from P2P network\n")
		// 		// You can add specific attestation processing here
		// 	}

		// case vote := <-n.p2pNetwork.VoteChan:
		// 	// Forward vote to consensus engine
		// 	if n.consensusEngine != nil {
		// 		fmt.Printf("Received vote from P2P network\n")
		// 		// You can add specific vote processing here
		// 	}

		case <-n.ctx.Done():
			fmt.Println("P2P message processing stopped")
			return
		}
	}
}

// BroadcastBlock broadcasts a block to the P2P network
func (n *Node) BroadcastBlock(block *core.Block) error {
	if n.p2pNetwork != nil {
		return n.p2pNetwork.BroadcastBlock(block)
	}
	return nil
}

func (n *Node) SyncWithPeers() error {
	if n.p2pNetwork != nil {
		return n.p2pNetwork.SyncBlockchain()
	}
	return fmt.Errorf("P2P network not enabled")
}

// BroadcastTransaction broadcasts a transaction to the P2P network
func (n *Node) BroadcastTransaction(tx *core.Transaction) error {
	if n.p2pNetwork != nil {
		return n.p2pNetwork.BroadcastTransaction(tx)
	}
	return nil
}

// Simple approach: Create transaction without hash, let blockchain handle it
func (n *Node) CreateTransaction(from, to string, amount int64, gasPrice int64) (*core.Transaction, error) {
	nonce, err := n.blockchain.GetNonce(from)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %v", err)
	}

	// Generate simple transaction ID
	timestamp := time.Now().UnixNano()
	txID := fmt.Sprintf("tx_%d_%d", timestamp, nonce)

	tx := &core.Transaction{
		Id:        txID,
		From:      from,
		To:        to,
		Amount:    amount,
		Gas:       21000,
		GasPrice:  gasPrice,
		Nonce:     nonce,
		Timestamp: time.Now().Unix(),
		// Don't set Hash - let blockchain calculate it
	}

	// Create simple signature based on transaction content (not hash)
	signature, err := n.signTransactionContent(tx)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %v", err)
	}
	tx.Signature = signature

	return tx, nil
}

// Sign based on transaction content, not the hash field
func (n *Node) signTransactionContent(tx *core.Transaction) ([]byte, error) {
	// Sign the transaction content (similar to what blockchain might expect)
	signData := fmt.Sprintf("%s%s%s%d%d%d%d%d",
		tx.Id,
		tx.From,
		tx.To,
		tx.Amount,
		tx.Gas,
		tx.GasPrice,
		tx.Nonce,
		tx.Timestamp,
	)

	hash := blake2b.Sum256([]byte(signData))

	// Sign with node private key
	signature := n.nodePrivateKey.Sign(hash[:])
	if signature == nil {
		return nil, fmt.Errorf("failed to sign transaction: signature is nil")
	}

	return signature.Bytes(), nil
}

// Alternative: Try to match blockchain's exact hash format
func (n *Node) CreateTransactionMatchingFormat(from, to string, amount int64, gasPrice int64) (*core.Transaction, error) {
	nonce, err := n.blockchain.GetNonce(from)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %v", err)
	}

	timestamp := time.Now().UnixNano()
	txID := fmt.Sprintf("tx_%d_%d", timestamp, nonce)

	tx := &core.Transaction{
		Id:        txID,
		From:      from,
		To:        to,
		Amount:    amount,
		Gas:       21000,
		GasPrice:  gasPrice,
		Nonce:     nonce,
		Timestamp: time.Now().Unix(),
	}

	// Try different hash formats to match what blockchain expects
	// Format 1: Simple concatenation (most common)
	hashData1 := fmt.Sprintf("%s%s%s%d%d%d%d%d",
		tx.Id, tx.From, tx.To, tx.Amount, tx.Gas, tx.GasPrice, tx.Nonce, tx.Timestamp)

	// Format 2: JSON-like format
	// hashData2 := fmt.Sprintf(`{"id":"%s","from":"%s","to":"%s","amount":%d,"gas":%d,"gasPrice":%d,"nonce":%d,"timestamp":%d}`,
	// 	tx.Id, tx.From, tx.To, tx.Amount, tx.Gas, tx.GasPrice, tx.Nonce, tx.Timestamp)

	// Try format 1 first
	hash := blake2b.Sum256([]byte(hashData1))
	tx.Hash = fmt.Sprintf("%x", hash)

	// Sign the transaction
	signature, err := n.signTransactionContent(tx)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %v", err)
	}
	tx.Signature = signature

	return tx, nil
}

// Option 2: Simplified hash calculation that might match blockchain validation
func (n *Node) calculateSimpleTransactionHash(tx *core.Transaction) string {
	// Try a simpler hash format that matches what blockchain expects
	hashData := fmt.Sprintf("%s%s%d%d%d%d",
		tx.From,
		tx.To,
		tx.Amount,
		tx.Gas,
		tx.GasPrice,
		tx.Nonce,
		// Note: Don't include timestamp if blockchain doesn't expect it
	)

	hash := blake2b.Sum256([]byte(hashData))
	return fmt.Sprintf("%x", hash)
}

// Option 3: Set hash to empty and let blockchain calculate it
func (n *Node) CreateTransactionSimple(from, to string, amount int64, gasPrice int64) (*core.Transaction, error) {
	nonce, err := n.blockchain.GetNonce(from)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %v", err)
	}

	// Create transaction and let blockchain handle the hash
	tx := &core.Transaction{
		From:      from,
		To:        to,
		Amount:    amount,
		Gas:       21000,
		GasPrice:  gasPrice,
		Nonce:     nonce,
		Timestamp: time.Now().Unix(),
		// Let blockchain calculate hash during validation
	}

	return tx, nil
}

// Keep your existing helper methods:
func (n *Node) calculateTransactionHash(tx *core.Transaction) (string, error) {
	// Create hash data from transaction fields
	hashData := fmt.Sprintf("%s%s%d%d%d%d%d",
		tx.From,
		tx.To,
		tx.Amount,
		tx.Gas,
		tx.GasPrice,
		tx.Nonce,
		tx.Timestamp,
	)

	// Use Blake2b for hashing (same as your consensus engine)
	hash := blake2b.Sum256([]byte(hashData))
	return fmt.Sprintf("%x", hash), nil
}

func (n *Node) signTransaction(tx *core.Transaction) ([]byte, error) {
	// Create signature data from transaction fields
	signData := fmt.Sprintf("%s%s%s%d%d%d%d%d",
		tx.Id,
		tx.From,
		tx.To,
		tx.Amount,
		tx.Gas,
		tx.GasPrice,
		tx.Nonce,
		tx.Timestamp,
	)

	hash := blake2b.Sum256([]byte(signData))

	// Sign with node private key
	signature := n.nodePrivateKey.Sign(hash[:])
	if signature == nil {
		return nil, fmt.Errorf("failed to sign transaction: signature is nil")
	}

	return signature.Bytes(), nil
}

// Alternative simple approach - just create a dummy signature
func (n *Node) CreateTransactionWithDummySignature(from, to string, amount int64, gasPrice int64) (*core.Transaction, error) {
	nonce, err := n.blockchain.GetNonce(from)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %v", err)
	}

	timestamp := time.Now().UnixNano()
	hashData := fmt.Sprintf("%s_%s_%d_%d_%d", from, to, amount, nonce, timestamp)
	hash := blake2b.Sum256([]byte(hashData))
	hashString := fmt.Sprintf("%x", hash)

	tx := &core.Transaction{
		Id:        fmt.Sprintf("tx_%d_%d", timestamp, nonce),
		Hash:      hashString,
		From:      from,
		To:        to,
		Amount:    amount,
		Gas:       21000,
		GasPrice:  gasPrice,
		Nonce:     nonce,
		Timestamp: time.Now().Unix(),
		Signature: []byte("dummy_signature"), // Simple placeholder signature
	}

	return tx, nil
}

// RegisterValidator registers this node as a validator
func (n *Node) RegisterValidator(stake int64, commission float64) error {
	pubkey := n.nodePrivateKey.PublicKey().Bytes()

	validator := &core.Validator{
		Address:        n.nodeAddress,
		Pubkey:         pubkey,
		Stake:          stake,
		SelfStake:      stake,
		DelegatedStake: 0,
		Commission:     commission,
		Active:         true,
		Delegators:     make(map[string]int64),
		CreatedAt:      time.Now().Unix(),
		UpdatedAt:      time.Now().Unix(),
	}

	if err := n.blockchain.AddValidator(validator); err != nil {
		return fmt.Errorf("failed to register validator: %v", err)
	}

	n.triggerEvent("validator_registered", validator)
	return nil
}

// Stake delegates tokens to a validator
func (n *Node) Stake(validatorAddr string, amount int64) error {
	stakingManager := n.blockchain.GetStakingManager()
	if stakingManager == nil {
		return fmt.Errorf("staking manager not available")
	}

	if err := stakingManager.Delegate(n.nodeAddress, validatorAddr, amount); err != nil {
		return fmt.Errorf("staking failed: %v", err)
	}

	n.triggerEvent("tokens_staked", map[string]interface{}{
		"delegator": n.nodeAddress,
		"validator": validatorAddr,
		"amount":    amount,
	})

	return nil
}

// Unstake removes delegation from a validator
func (n *Node) Unstake(validatorAddr string, amount int64) error {
	stakingManager := n.blockchain.GetStakingManager()
	if stakingManager == nil {
		return fmt.Errorf("staking manager not available")
	}

	if err := stakingManager.Undelegate(n.nodeAddress, validatorAddr, amount); err != nil {
		return fmt.Errorf("unstaking failed: %v", err)
	}

	n.triggerEvent("tokens_unstaked", map[string]interface{}{
		"delegator": n.nodeAddress,
		"validator": validatorAddr,
		"amount":    amount,
	})

	return nil
}

// InitiateCrossShardTransfer initiates a cross-shard transfer
func (n *Node) InitiateCrossShardTransfer(to string, amount int64) (*state.CrossShardTransfer, error) {
	if !n.crossShardEnabled {
		return nil, fmt.Errorf("cross-shard transfers not enabled")
	}

	nonce, err := n.blockchain.GetNonce(n.nodeAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %v", err)
	}

	transfer, err := n.blockchain.InitiateCrossShardTransfer(n.nodeAddress, to, amount, nonce)
	if err != nil {
		return nil, fmt.Errorf("failed to initiate cross-shard transfer: %v", err)
	}

	n.triggerEvent("cross_shard_transfer_initiated", transfer)
	return transfer, nil
}

// rewardDistributionLoop handles periodic reward distribution
func (n *Node) rewardDistributionLoop() {
	// Use a default epoch duration if not configured
	epochDuration := 24 * time.Hour // Default to 24 hours
	if n.config.Consensus.BlockTime > 0 {
		// Calculate epoch as multiple of block time (e.g., 100 blocks per epoch)
		epochDuration = time.Duration(n.config.Consensus.BlockTime*100) * time.Second
	}

	ticker := time.NewTicker(epochDuration)
	defer ticker.Stop()

	for n.isRunning {
		select {
		case <-ticker.C:
			n.lastEpoch++
			if err := n.distributeEpochRewards(n.lastEpoch); err != nil {
				fmt.Printf("Failed to distribute epoch %d rewards: %v\n", n.lastEpoch, err)
			}
		}
	}
}

// distributeEpochRewards distributes rewards for an epoch
func (n *Node) distributeEpochRewards(epoch uint64) error {
	// Calculate inflation rewards - use a simple calculation if method doesn't exist
	var inflationRewards int64
	if n.inflationManager != nil {
		// Try to get inflation rate from config or use default
		inflationRate := float64(0.05) // 5% annual inflation
		if n.config.Economics.InflationRate > 0 {
			inflationRate = n.config.Economics.InflationRate
		}

		// Calculate rewards based on total supply and inflation rate
		totalSupply := n.worldState.GetTotalSupply()
		// Daily rewards (assuming daily epochs)
		inflationRewards = int64(float64(totalSupply) * inflationRate / 365.0)
	} else {
		// Fallback calculation
		inflationRewards = 1000 // Default reward amount
	}

	// Distribute through staking manager
	stakingManager := n.blockchain.GetStakingManager()
	if stakingManager == nil {
		return fmt.Errorf("staking manager not available")
	}

	if err := stakingManager.DistributeRewards(inflationRewards); err != nil {
		return fmt.Errorf("reward distribution failed: %v", err)
	}

	n.triggerEvent("epoch_rewards_distributed", map[string]interface{}{
		"epoch":   epoch,
		"rewards": inflationRewards,
	})

	fmt.Printf("Epoch %d: Distributed %d tokens in rewards\n", epoch, inflationRewards)
	return nil
}

// blockProductionLoop handles block production for validators
func (n *Node) blockProductionLoop() {
	ticker := time.NewTicker(time.Duration(n.config.Consensus.BlockTime) * time.Second)
	defer ticker.Stop()

	for n.isRunning {
		select {
		case <-ticker.C:
			if n.isValidator() && n.isMyTurn() {
				if err := n.produceBlock(); err != nil {
					fmt.Printf("Failed to produce block: %v\n", err)
				}
			}
		}
	}
}

// produceBlock produces a new block
func (n *Node) produceBlock() error {
	// Create block through blockchain
	block, err := n.blockchain.CreateBlock(n.nodeAddress, n.nodePrivateKey)
	if err != nil {
		return fmt.Errorf("failed to create block: %v", err)
	}

	// Add block to blockchain
	if err := n.blockchain.AddBlock(block); err != nil {
		return fmt.Errorf("failed to add block: %v", err)
	}

	// Broadcast block
	n.broadcastChan <- &pos.BlockProposal{
		Block: block,
		// Remove fields that don't exist in the actual pos.BlockProposal struct
	}

	n.triggerEvent("block_produced", block)
	n.updateBlockProcessingRate()

	fmt.Printf("Produced block %d with %d transactions\n",
		block.Header.Index, len(block.Transactions))

	return nil
}

// networkingLoop handles network communication
func (n *Node) networkingLoop() {
	for n.isRunning {
		select {
		case msg := <-n.broadcastChan:
			n.handleOutgoingMessage(msg)
		case msg := <-n.receiveChan:
			n.handleIncomingMessage(msg)
		}
	}
}

// crossShardLoop handles cross-shard operations
func (n *Node) crossShardLoop() {
	if !n.crossShardEnabled {
		return
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for n.isRunning {
		select {
		case <-ticker.C:
			n.processCrossShardTransfers()
		}
	}
}

// processCrossShardTransfers processes pending cross-shard transfers
func (n *Node) processCrossShardTransfers() {
	csm := n.blockchain.GetCrossShardManager()
	if csm == nil {
		return
	}

	pendingTransfers := csm.GetPendingTransfers()
	for _, transfer := range pendingTransfers {
		// Complete transfers destined for this shard
		if transfer.ToShard == n.shardID && transfer.Status == "pending" {
			if err := n.blockchain.CompleteCrossShardTransfer(transfer.Hash); err != nil {
				fmt.Printf("Failed to complete cross-shard transfer %s: %v\n", transfer.Hash, err)
			} else {
				n.triggerEvent("cross_shard_transfer_completed", transfer)
			}
		}
	}
}

// maintenanceLoop performs periodic maintenance
func (n *Node) maintenanceLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for n.isRunning {
		select {
		case <-ticker.C:
			n.performMaintenance()
		}
	}
}

// performMaintenance performs routine maintenance tasks
func (n *Node) performMaintenance() {
	// Cleanup old data
	n.blockchain.Cleanup()

	// Validate state consistency
	if err := n.blockchain.ValidateStateConsistency(); err != nil {
		fmt.Printf("State consistency check failed: %v\n", err)
		n.triggerEvent("state_inconsistency_detected", err)
	}

	// Update validator performance metrics
	n.updateValidatorMetrics()

	n.triggerEvent("maintenance_completed", time.Now())
}

// eventProcessingLoop processes events
func (n *Node) eventProcessingLoop() {
	blockChan := n.blockchain.GetBlockAddedChannel()
	txChan := n.blockchain.GetTransactionAddedChannel()

	for n.isRunning {
		select {
		case block := <-blockChan:
			n.triggerEvent("block_added", block)
		case tx := <-txChan:
			n.triggerEvent("transaction_added", tx)
		}
	}
}

// Helper methods

func (n *Node) storeGenesisConfig(config *NodeConfig) {
	// Store genesis configuration for later use
	// This would typically be stored in the node's configuration
}

func (n *Node) initializeGenesis() error {
	// Check if genesis already exists
	if n.blockchain.GetHeight() >= 0 {
		return nil // Genesis already initialized
	}

	// Initialize through blockchain
	genesisValidators := []*core.Validator{}
	if n.isValidatorNode {
		// Add self as genesis validator
		genesisValidators = append(genesisValidators, &core.Validator{
			Address:        n.nodeAddress,
			Pubkey:         n.nodePrivateKey.PublicKey().Bytes(),
			Stake:          n.config.Staking.MinValidatorStake,
			SelfStake:      n.config.Staking.MinValidatorStake,
			DelegatedStake: 0,
			Commission:     0.1, // 10% commission
			Active:         true,
			Delegators:     make(map[string]int64),
			CreatedAt:      time.Now().Unix(),
			UpdatedAt:      time.Now().Unix(),
		})
	}

	return n.blockchain.InitializeGenesis(
		n.nodeAddress, // Genesis account
		n.nodeAddress, // Genesis validator
		n.config.Economics.GenesisSupply,
		genesisValidators,
		n.nodePrivateKey,
	)
}

func (n *Node) registerAsValidator() error {
	return n.RegisterValidator(n.config.Staking.MinValidatorStake, 0.1)
}

func (n *Node) handleOutgoingMessage(msg interface{}) {
	switch m := msg.(type) {
	case *pos.BlockProposal:
		fmt.Printf("Broadcasting block proposal: %s\n", m.Block.Hash)
	case *pos.Attestation:
		fmt.Printf("Broadcasting attestation\n")
	case *pos.Vote:
		fmt.Printf("Broadcasting vote\n")
	}
}

func (n *Node) handleIncomingMessage(msg interface{}) {
	// Forward to consensus engine
	fmt.Printf("Received message: %T\n", msg)
}

func (n *Node) isValidator() bool {
	validator, err := n.blockchain.GetValidator(n.nodeAddress)
	if err != nil {
		return false
	}
	return validator.Active
}

func (n *Node) isMyTurn() bool {
	// Simple round-robin logic based on current time and validator list
	// In a real implementation, this would use proper consensus slot calculation

	validators := n.blockchain.GetActiveValidators()
	if len(validators) == 0 {
		return false
	}

	// Find our validator index
	myIndex := -1
	for i, validator := range validators {
		if validator.Address == n.nodeAddress {
			myIndex = i
			break
		}
	}

	if myIndex == -1 {
		return false // We're not a validator
	}

	// Simple time-based slot assignment (in real implementation, use proper consensus rules)
	currentSlot := time.Now().Unix() / int64(n.config.Consensus.BlockTime)
	assignedValidator := currentSlot % int64(len(validators))

	return int64(myIndex) == assignedValidator
}

func (n *Node) updateBlockProcessingRate() {
	// Calculate processing rate
	currentTime := time.Now()
	if !n.lastRewardTime.IsZero() {
		duration := currentTime.Sub(n.lastRewardTime)
		n.blockProcessingRate = 1.0 / duration.Seconds()
	}
	n.lastRewardTime = currentTime
}

func (n *Node) updateValidatorMetrics() {
	// Update validator performance metrics
	// This would include uptime, block production rate, etc.
}

// Event system

func (n *Node) AddEventHandler(eventType string, handler func(interface{})) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.eventHandlers[eventType] == nil {
		n.eventHandlers[eventType] = make([]func(interface{}), 0)
	}
	n.eventHandlers[eventType] = append(n.eventHandlers[eventType], handler)
}

func (n *Node) triggerEvent(eventType string, data interface{}) {
	n.mu.RLock()
	handlers := n.eventHandlers[eventType]
	// Make a copy of the handlers slice to avoid holding the lock
	handlersCopy := make([]func(interface{}), len(handlers))
	copy(handlersCopy, handlers)
	n.mu.RUnlock()

	// Execute handlers without holding any locks
	for _, handler := range handlersCopy {
		go handler(data)
	}
}

// Public API methods

func (n *Node) GetNodeStatus() map[string]interface{} {
	n.mu.RLock()
	defer n.mu.RUnlock()

	blockchainStats := n.blockchain.GetStats()
	worldStateStatus := n.worldState.GetStatus()

	status := map[string]interface{}{
		"running":               n.isRunning,
		"node_address":          n.nodeAddress,
		"shard_id":              n.shardID,
		"total_shards":          n.totalShards,
		"is_validator":          n.isValidator(),
		"cross_shard_enabled":   n.crossShardEnabled,
		"last_epoch":            n.lastEpoch,
		"block_processing_rate": n.blockProcessingRate,
		"blockchain":            blockchainStats,
		"world_state":           worldStateStatus,
	}

	// Add consensus stats
	if n.consensusEngine != nil {
		status["consensus"] = n.consensusEngine.GetStats()
	}

	return status
}

func (n *Node) GetBalance(address string) (int64, error) {
	return n.blockchain.GetBalance(address)
}

func (n *Node) GetAccount(address string) (*core.Account, error) {
	return n.blockchain.GetAccount(address)
}

func (n *Node) GetValidator(address string) (*core.Validator, error) {
	return n.blockchain.GetValidator(address)
}

func (n *Node) GetActiveValidators() []*core.Validator {
	return n.blockchain.GetActiveValidators()
}

func (n *Node) GetPendingTransactions() []*core.Transaction {
	return n.blockchain.GetPendingTransactions()
}

func (n *Node) GetCurrentBlock() *core.Block {
	return n.blockchain.GetCurrentBlock()
}

func (n *Node) GetBlockByHash(hash string) (*core.Block, error) {
	return n.blockchain.GetBlock(hash)
}

func (n *Node) GetBlockByIndex(index int64) (*core.Block, error) {
	return n.blockchain.GetBlockByIndex(index)
}

func (n *Node) GetDelegations(address string) (map[string]int64, error) {
	stakingManager := n.blockchain.GetStakingManager()
	if stakingManager == nil {
		return nil, fmt.Errorf("staking manager not available")
	}
	return stakingManager.GetDelegations(address)
}

func (n *Node) CreateSnapshot() *state.StateSnapshot {
	return n.blockchain.CreateSnapshot()
}

func (n *Node) RestoreFromSnapshot(snapshot *state.StateSnapshot) error {
	return n.blockchain.RestoreFromSnapshot(snapshot)
}

func (n *Node) GetShardInfo() map[string]interface{} {
	return n.blockchain.GetShardInfo()
}

func (n *Node) IsHealthy() bool {
	return n.isRunning && n.blockchain.IsHealthy()
}
