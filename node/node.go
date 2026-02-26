// node/node.go
package node

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/thrylos-labs/go-thrylos/api"
	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/consensus/pos"
	"github.com/thrylos-labs/go-thrylos/consensus/rewards"
	"github.com/thrylos-labs/go-thrylos/consensus/validator"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/core/chain"
	"github.com/thrylos-labs/go-thrylos/core/evm"
	"github.com/thrylos-labs/go-thrylos/core/state"
	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/network"
	"github.com/thrylos-labs/go-thrylos/network/p2p"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
	thrylosSync "github.com/thrylos-labs/go-thrylos/sync"
	"github.com/thrylos-labs/go-thrylos/types"
)

// Node represents a blockchain node with PoS consensus and comprehensive state management
type Node struct {
	// Core components
	config     *config.Config
	storage    *storage.BadgerStorage
	worldState *state.WorldState
	blockchain *chain.Blockchain

	// PoS consensus components
	consensusEngine   *pos.ConsensusEngine
	validatorManager  *validator.Manager
	rewardDistributor *rewards.Distributor
	inflationManager  *rewards.InflationManager

	// API server
	apiManager *api.APIManager

	// Node identity and configuration
	nodePrivateKey  crypto.PrivateKey
	nodeAddress     string
	shardID         account.ShardID
	totalShards     int
	isValidatorNode bool

	// Networking for consensus
	broadcastChan chan interface{}
	receiveChan   chan interface{}

	// P2P Networking
	p2pNetwork *network.P2PNetwork
	bridge     *pos.ConsensusBridge
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

	// Context management for graceful shutdown
	ctx        context.Context
	cancelFunc context.CancelFunc

	syncManager *thrylosSync.SyncManager

	genesisValidators []*core.Validator

	evmExecutor *evm.RevmExecutor

	genesisAccount string // Add this line

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
	GenesisSupply     string
	GenesisValidators []*core.Validator
	EnableP2P         bool
	P2PListenPort     int
	BootstrapPeers    []string
	EnableAPI         bool                 `json:"enable_api"`
	APIPort           int                  `json:"api_port"`
	P2PIdentityKey    libp2pcrypto.PrivKey // ← add this
}

func (n *Node) StartAPI() error { // Add (n *Node) - it's a method!
	apiConfig := &api.APIManagerConfig{
		RESTAddr:     n.config.API.RESTAddr, // Changed nodeConfig to n
		EnableTLS:    n.config.API.EnableTLS,
		CertFile:     n.config.API.CertFile,
		KeyFile:      n.config.API.KeyFile,
		EnableFaucet: n.config.API.EnableFaucet,
	}

	n.apiManager = api.NewAPIManagerWithConfig(
		n.worldState, n.blockchain, n.evmExecutor, n.config, apiConfig)

	return n.apiManager.Start()
}

// StartAPI starts the embedded API server using existing APIManager
// func (n *Node) StartAPI() error {
// 	apiConfig := &api.APIManagerConfig{
// 		RESTAddr:     n.config.API.RESTAddr, // Just pass it through!
// 		EnableTLS:    n.config.API.EnableTLS,
// 		CertFile:     n.config.API.CertFile,
// 		KeyFile:      n.config.API.KeyFile,
// 		EnableFaucet: n.config.API.EnableFaucet,
// 	}

// 	n.apiManager = api.NewAPIManagerWithConfig(
// 		n.worldState, n.blockchain, n.evmExecutor, n.config, apiConfig)

// 	return n.apiManager.Start()
// }

// StopAPI gracefully shuts down the API server
func (n *Node) StopAPI() error {
	if n.apiManager != nil {
		return n.apiManager.Stop()
	}
	return nil
}

// NewNode creates a new blockchain node with full WorldState integration
func NewNode(nodeConfig *NodeConfig) (*Node, error) {
	if nodeConfig == nil {
		return nil, fmt.Errorf("node config cannot be nil")
	}

	// Initialize context and cancel function
	ctx, cancelFunc := context.WithCancel(context.Background())

	// Generate node address from private key
	nodeAddress, err := account.GenerateAddress(nodeConfig.PrivateKey.PublicKey())
	if err != nil {
		return nil, fmt.Errorf("failed to generate node address: %v", err)
	}

	// Initialize storage first
	dataDir := filepath.Join(nodeConfig.DataDir, fmt.Sprintf("shard-%d", nodeConfig.ShardID))
	storage, err := storage.NewBadgerStorage(dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize storage: %v", err)
	}

	// Initialize WorldState with existing storage - PASS THE STORAGE
	worldState, err := state.NewWorldState(dataDir, nodeConfig.ShardID, nodeConfig.TotalShards, nodeConfig.Config, storage)
	if err != nil {
		storage.Close() // Clean up storage if WorldState creation fails
		cancelFunc()    // Prevent context leak
		return nil, fmt.Errorf("failed to initialize world state: %v", err)
	}

	// nitialize revm Executor ===
	revmExecutor, err := evm.NewRevmExecutor(nodeConfig.Config, worldState)
	if err != nil {
		storage.Close()
		cancelFunc()
		return nil, fmt.Errorf("failed to create revm executor: %v", err)
	}

	// Initialize PoS components
	validatorManager := validator.NewManager(nodeConfig.Config, worldState)
	rewardDistributor := rewards.NewDistributor(nodeConfig.Config, worldState)
	inflationManager := rewards.NewInflationManager(nodeConfig.Config, worldState)

	// Initialize Blockchain with WorldState
	blockchainConfig := &chain.BlockchainConfig{
		Config:            nodeConfig.Config,
		WorldState:        worldState,
		ValidatorManager:  validatorManager,
		ShardID:           nodeConfig.ShardID,
		TotalShards:       nodeConfig.TotalShards,
		MaxReorgDepth:     100,
		CrossShardEnabled: nodeConfig.CrossShardEnabled,
	}

	bc, err := chain.NewBlockchain(blockchainConfig)

	if err != nil {
		storage.Close() // Clean up storage
		cancelFunc()    // Prevent context leak
		return nil, fmt.Errorf("failed to create blockchain: %v", err)
	}

	// Initialize networking channels for consensus
	broadcastChan := make(chan interface{}, 1000)
	receiveChan := make(chan interface{}, 1000)

	// Initialize consensus engine
	// UPDATE: Pass the blockchain instance (bc)
	consensusEngine := pos.NewConsensusEngine(
		nodeConfig.Config,
		bc, // <-- Added this
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
		p2pNet, err := network.NewP2PNetworkWithConfig(
			nodeConfig.Config,
			nodeConfig.P2PListenPort,
			nodeConfig.BootstrapPeers,
			nodeConfig.EnableP2P,
			nodeConfig.P2PIdentityKey, // ← add this

		)
		if err != nil {
			storage.Close() // Clean up storage
			cancelFunc()    // Prevent context leak
			return nil, fmt.Errorf("failed to create P2P network: %v", err)
		}
		p2pNetwork = p2pNet
	}

	var bridge *pos.ConsensusBridge
	if p2pNetwork != nil {
		bridge = pos.NewConsensusBridge(consensusEngine, p2pNetwork)
	}

	var syncManager *thrylosSync.SyncManager
	if p2pNetwork != nil {
		syncManager = thrylosSync.NewSyncManager(nodeConfig.Config, bc, worldState, p2pNetwork)
	}

	node := &Node{
		config:            nodeConfig.Config,
		storage:           storage,
		worldState:        worldState,
		evmExecutor:       revmExecutor,
		blockchain:        bc,
		p2pNetwork:        p2pNetwork,
		bridge:            bridge,
		syncManager:       syncManager,
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
		ctx:               ctx,
		cancelFunc:        cancelFunc,
		genesisValidators: nodeConfig.GenesisValidators,
		genesisAccount:    nodeConfig.GenesisAccount,
	}

	if nodeConfig.EnableAPI {
		apiPort := nodeConfig.APIPort
		if apiPort == 0 {
			apiPort = parsePortFromAddr(nodeConfig.Config.API.RESTAddr)
		}

		// Create API config
		apiConfig := &api.APIManagerConfig{
			RESTAddr:     nodeConfig.Config.API.RESTAddr, // Changed from Port
			EnableTLS:    nodeConfig.Config.API.EnableTLS,
			CertFile:     nodeConfig.Config.API.CertFile,
			KeyFile:      nodeConfig.Config.API.KeyFile,
			EnableFaucet: nodeConfig.Config.API.EnableFaucet,
		}

		// Start API manager
		node.apiManager = api.NewAPIManagerWithConfig(
			worldState,
			bc,
			revmExecutor,
			nodeConfig.Config,
			apiConfig,
		)
	}

	// Store genesis configuration for initialization
	node.storeGenesisConfig(nodeConfig)

	return node, nil
}

func parsePortFromAddr(addr string) int {
	if addr == "" {
		return 8080
	}
	if addr[0] == ':' {
		if port, err := strconv.Atoi(addr[1:]); err == nil {
			return port
		}
	}
	return 8080
}

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

	// Reset jail status AFTER consensus engine starts
	if os.Getenv("THRYLOS_ENVIRONMENT") == "development" && n.consensusEngine != nil {
		log.Println("🔓 Development mode: Resetting jail status for all validators...")
		validators := n.consensusEngine.GetAllValidators()
		for _, v := range validators {
			v.JailUntil = 0
			v.Active = true
			if err := n.blockchain.GetWorldState().UpdateValidator(v); err != nil {
				log.Printf("⚠️ Failed to reset jail status for validator %s: %v", v.Address, err)
			} else {
				log.Printf("✅ Reset jail status for validator %s (Active: true, JailUntil: 0)", v.Address)
			}
			slashingModule := n.consensusEngine.GetSlashingModule()
			if slashingModule != nil {
				slashingModule.ClearJailStatus(v.Address)
				log.Printf("✅ Cleared slashing info for validator %s", v.Address)
			}
		}
		log.Println("✅ All validators unjailed and reactivated for development")
	}

	// Connect P2P to consensus engine
	if n.p2pNetwork != nil && n.consensusEngine != nil {
		n.p2pNetwork.SetConsensusEngine(n.consensusEngine)
	}

	// Start bridge
	if n.bridge != nil {
		if err := n.bridge.Start(); err != nil {
			return fmt.Errorf("failed to start consensus bridge: %v", err)
		}
		fmt.Println("🌉 P2P <-> Consensus bridge started")
	}

	// Start API server
	if n.apiManager != nil {
		if err := n.apiManager.Start(); err != nil {
			return fmt.Errorf("failed to start API server: %v", err)
		}
	}

	// Start P2P network
	if n.p2pNetwork != nil {
		nodeID := os.Getenv("NODE_ID")
		log.Printf("🔍 DEBUG Node startup: NODE_ID=%q", nodeID)

		// Block proposals immediately for non-node-1 before anything else
		if nodeID != "1" && nodeID != "" {
			n.consensusEngine.SetSyncing(true)
		}

		if err := n.p2pNetwork.Start(); err != nil {
			return fmt.Errorf("failed to start P2P network: %v", err)
		}
		go n.processP2PMessages()
		go n.processP2PMessageBus()

		// Genesis sync for non-node-1 nodes
		if nodeID != "1" && nodeID != "" {
			log.Println("⏳ Waiting for P2P peers before syncing genesis...")
			time.Sleep(15 * time.Second)
			log.Println("📡 Attempting genesis sync now that P2P is running...")
			if err := n.syncGenesisFromNetwork(); err != nil {
				return fmt.Errorf("genesis sync failed: %v", err)
			}
			log.Println("✅ Genesis synced successfully!")
		}

		// Announce validator AFTER P2P is up AND genesis is synced (all nodes)
		if n.consensusEngine != nil {
			validator, err := n.consensusEngine.GetLocalValidator()
			if err == nil && validator != nil {
				if err := n.p2pNetwork.AnnounceValidator(validator); err != nil {
					log.Printf("⚠️ Failed to announce validator: %v", err)
				} else {
					log.Printf("✅ Announced validator %s to network", validator.Address)
				}
				go func() {
					for i := 0; i < 6; i++ {
						time.Sleep(10 * time.Second)
						peers := n.p2pNetwork.GetConnectedPeers()
						log.Printf("🔁 Re-announcing validator %s (attempt %d, peers: %d)", validator.Address, i+1, peers)
						n.p2pNetwork.AnnounceValidator(validator)
					}
				}()
				n.p2pNetwork.RequestValidatorSync()
			}
		}

		// Chain sync for non-node-1 only
		if nodeID != "1" && nodeID != "" {
			n.syncManager.Start()
			go func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("❌ PANIC in SyncToNetworkTip: %v", r)
					}
				}()
				interval := 2 * time.Second
				for {
					time.Sleep(interval)
					log.Printf("🔄 SyncToNetworkTip attempting...")
					if err := n.syncManager.SyncToNetworkTip(); err != nil {
						log.Printf("⚠️ SyncToNetworkTip: %v", err)
						interval = 2 * time.Second // back-off on error: retry fast
					} else if !n.consensusEngine.IsSyncing() {
						interval = 30 * time.Second // fully synced: poll slowly
					}
					// No exit - keep syncing in background forever
				}
			}()
			log.Println("⏳ Waiting for chain sync to complete...")
			if err := n.waitForChainSync(30 * time.Second); err != nil {
				log.Printf("⚠️ Chain sync incomplete: %v, proceeding anyway", err)
			}
			n.consensusEngine.SetSyncing(false)

		} else {
			n.syncManager.Start()
			go func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("❌ PANIC in SyncToNetworkTip (node1): %v", r)
					}
				}()
				interval := 5 * time.Second
				for {
					time.Sleep(interval)
					if err := n.syncManager.SyncToNetworkTip(); err != nil {
						log.Printf("⚠️ SyncToNetworkTip (node1): %v", err)
						interval = 5 * time.Second
					} else if !n.consensusEngine.IsSyncing() {
						interval = 30 * time.Second
					}
				}
			}()
		}
	}

	// Start background processes
	go n.rewardDistributionLoop()
	go n.blockProductionLoop()
	go n.networkingLoop()
	go n.crossShardLoop()
	go n.maintenanceLoop()
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
		stats := n.p2pNetwork.GetNetworkStats()
		if port, ok := stats["listen_port"]; ok {
			fmt.Printf("  P2P: enabled on port %v\n", port)
		} else {
			fmt.Printf("  P2P: enabled\n")
		}
	} else {
		fmt.Printf("  P2P: disabled\n")
	}

	return nil
}

// Stop gracefully shuts down the P2P manager
func (n *Node) Stop() error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if !n.isRunning {
		return fmt.Errorf("node is not running")
	}

	fmt.Println("🛑 Stopping node gracefully...")

	// Cancel all goroutines first
	if n.cancelFunc != nil {
		n.cancelFunc()
	}

	// Stop sync manager
	if n.syncManager != nil {
		if err := n.syncManager.Stop(); err != nil {
			fmt.Printf("Error stopping sync manager: %v\n", err)
		}
	}

	// Stop P2P network
	if n.p2pNetwork != nil {
		if err := n.p2pNetwork.Stop(); err != nil {
			fmt.Printf("Error stopping P2P network: %v\n", err)
		}
	}

	// Stop API server
	if n.apiManager != nil {
		if err := n.apiManager.Stop(); err != nil {
			fmt.Printf("Error stopping API server: %v\n", err)
		}
	}

	// Stop consensus engine
	if err := n.consensusEngine.Stop(); err != nil {
		return fmt.Errorf("failed to stop consensus engine: %v", err)
	}

	if n.bridge != nil {
		if err := n.bridge.Stop(); err != nil {
			return fmt.Errorf("failed to stop bridge: %v", err)
		}
	}

	if n.evmExecutor != nil {
		fmt.Println("Closing revm executor...")
		n.evmExecutor.Close()
	}

	// Give goroutines time to stop gracefully
	time.Sleep(2 * time.Second)

	// Perform final cleanup
	n.blockchain.Cleanup()
	n.worldState.Cleanup()

	// Close storage LAST (after everything else)
	if n.storage != nil {
		if err := n.storage.Close(); err != nil {
			fmt.Printf("Error closing storage: %v\n", err)
		}
	}

	n.isRunning = false

	fmt.Println("✅ Node stopped gracefully")
	return nil
}

func (n *Node) waitForChainSync(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	stableCount := 0
	lastHeight := int64(-1)

	for time.Now().Before(deadline) {
		currentHeight := n.blockchain.GetHeight()
		networkTip := n.syncManager.GetMaxPeerHeight()

		log.Printf("⏳ Chain sync: height=%d, networkTip=%d, stable=%d/3",
			currentHeight, networkTip, stableCount)

		// Must be at network tip AND stable
		if currentHeight >= networkTip && networkTip > 0 && currentHeight == lastHeight {
			stableCount++
			if stableCount >= 3 {
				log.Printf("✅ Chain stable at height %d", currentHeight)
				return nil
			}
		} else {
			stableCount = 0
		}

		lastHeight = currentHeight
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timeout")
}

// SubmitTransaction accepts a transaction from external sources (e.g., wallets via RPC)
func (n *Node) SubmitTransaction(tx *core.Transaction) error {
	if tx == nil {
		return fmt.Errorf("transaction cannot be nil")
	}

	// Add transaction through blockchain (includes validation)
	if err := n.blockchain.AddTransaction(tx); err != nil {
		return fmt.Errorf("failed to submit transaction: %v", err)
	}

	// Broadcast transaction to P2P network
	if err := n.BroadcastTransaction(tx); err != nil {
		fmt.Printf("Failed to broadcast transaction to P2P network: %v\n", err)
		// Don't return error here - transaction is still added locally
	}

	n.triggerEvent("transaction_submitted", tx)
	return nil
}

// P2P Message Processing

func (n *Node) processP2PMessages() {
	if n.p2pNetwork == nil {
		return
	}

	for {
		select {
		case block := <-n.p2pNetwork.BlockChan:
			localHeight := n.blockchain.GetHeight()
			if block.Header.Index > localHeight+1 {
				log.Printf("⏭️ Dropping out-of-order block %d (local height: %d), sync will catch up",
					block.Header.Index, localHeight)
				continue
			}

			if n.consensusEngine.IsSyncing() {
				// During sync: trust incoming blocks unconditionally
				if err := n.blockchain.AddBlockFromSync(block); err != nil {
					fmt.Printf("Failed to process sync block: %v\n", err)
				} else {
					fmt.Printf("Processed sync block %s from P2P network\n", block.Hash)
				}
			} else {
				// Live block: validate through consensus engine including SimulateStateRoot
				if err := n.blockchain.AddBlock(block); err != nil {
					fmt.Printf("Failed to process live block: %v\n", err)
				} else {
					fmt.Printf("Processed live block %s from P2P network\n", block.Hash)
				}
			}

		case tx := <-n.p2pNetwork.TransactionChan:
			// Process received transaction
			if err := n.blockchain.AddTransaction(tx); err != nil {
				fmt.Printf("Failed to process P2P transaction: %v\n", err)
			} else {
				fmt.Printf("Processed transaction %s from P2P network\n", tx.Id)
			}

		case attestation := <-n.p2pNetwork.AttestationChan:
			// Forward attestation to consensus engine
			if n.consensusEngine != nil {
				fmt.Printf("Received attestation from P2P network\n")
				n.receiveChan <- attestation
			}

		case vote := <-n.p2pNetwork.VoteChan:
			// Forward vote to consensus engine
			if n.consensusEngine != nil {
				fmt.Printf("Received vote from P2P network\n")
				n.receiveChan <- vote
			}

		case <-n.ctx.Done():
			fmt.Println("P2P message processing stopped")
			return
		}
	}
}

// P2P Broadcasting Methods

func (n *Node) BroadcastBlock(block *core.Block) error {
	if n.p2pNetwork != nil {
		return n.p2pNetwork.BroadcastBlock(block)
	}
	return nil
}

func (n *Node) BroadcastTransaction(tx *core.Transaction) error {
	if n.p2pNetwork != nil {
		return n.p2pNetwork.BroadcastTransaction(tx)
	}
	return nil
}

func (n *Node) SyncWithPeers() error {
	if n.syncManager != nil {
		return n.syncManager.SyncWithPeers()
	}
	return fmt.Errorf("sync manager not available")
}

// Validator Operations

func (n *Node) RegisterValidator(stake string, commission float64) error {
	pubkey := n.nodePrivateKey.PublicKey().Bytes()

	validator := &core.Validator{
		Address: n.nodeAddress,
		Pubkey:  pubkey,

		// Fix 1: Assign string directly (matches protobuf)
		Stake:     stake,
		SelfStake: stake,

		// Fix 2: Use string "0" instead of integer 0
		DelegatedStake: "0",

		Commission: commission,
		Active:     true,

		// Fix 3: Use map[string]string (matches protobuf)
		Delegators: make(map[string]string),

		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	}

	if err := n.blockchain.AddValidator(validator); err != nil {
		return fmt.Errorf("failed to register validator: %v", err)
	}

	n.triggerEvent("validator_registered", validator)
	return nil
}

func (n *Node) Stake(validatorAddr string, amount *big.Int) error {
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
		"amount":    amount.String(), // ✅ Convert to string for event
	})

	return nil
}

func (n *Node) Unstake(validatorAddr string, amount *big.Int) error {
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
		"amount":    amount.String(), // ✅ Convert to string for event
	})

	return nil
}

// Cross-shard Operations

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

// Background Process Loops

func (n *Node) rewardDistributionLoop() {
	epochDuration := 24 * time.Hour
	if n.config.Consensus.BlockTime > 0 {
		epochDuration = time.Duration(n.config.Consensus.BlockTime*100) * time.Second
	}

	ticker := time.NewTicker(epochDuration)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if !n.isRunning {
				return
			}
			n.lastEpoch++
			if err := n.distributeEpochRewards(n.lastEpoch); err != nil {
				fmt.Printf("Failed to distribute epoch %d rewards: %v\n", n.lastEpoch, err)
			}
		case <-n.ctx.Done():
			fmt.Println("Reward distribution loop stopped")
			return
		}
	}
}

func (n *Node) blockProductionLoop() {
	ticker := time.NewTicker(time.Duration(n.config.Consensus.BlockTime) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if !n.isRunning {
				return
			}
			if n.isValidator() && n.isMyTurn() {
				if err := n.produceBlock(); err != nil {
					fmt.Printf("Failed to produce block: %v\n", err)
				}
			}
		case <-n.ctx.Done():
			fmt.Println("Block production loop stopped")
			return
		}
	}
}

func (n *Node) networkingLoop() {
	for {
		select {
		case msg := <-n.broadcastChan:
			n.handleOutgoingMessage(msg)
		case msg := <-n.receiveChan:
			n.handleIncomingMessage(msg)
		case <-n.ctx.Done():
			fmt.Println("Networking loop stopped")
			return
		}
	}
}

func (n *Node) crossShardLoop() {
	if !n.crossShardEnabled {
		return
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if !n.isRunning {
				return
			}
			n.processCrossShardTransfers()
		case <-n.ctx.Done():
			fmt.Println("Cross-shard loop stopped")
			return
		}
	}
}

func (n *Node) maintenanceLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if !n.isRunning {
				return
			}
			n.performMaintenance()
		case <-n.ctx.Done():
			fmt.Println("Maintenance loop stopped")
			return
		}
	}
}

func (n *Node) eventProcessingLoop() {
	blockChan := n.blockchain.GetBlockAddedChannel()
	txChan := n.blockchain.GetTransactionAddedChannel()

	for {
		select {
		case block := <-blockChan:
			if !n.isRunning {
				return
			}
			n.triggerEvent("block_added", block)
		case tx := <-txChan:
			if !n.isRunning {
				return
			}
			n.triggerEvent("transaction_added", tx)
		case <-n.ctx.Done():
			fmt.Println("Event processing loop stopped")
			return
		}
	}
}

// Block Production
func (n *Node) produceBlock() error {
	fmt.Printf("🔍 Node: Producing block with batching...\n")

	// Get transaction pool stats first
	pendingTxs := n.blockchain.GetPendingTransactions()
	fmt.Printf("🔍 Node: %d pending transactions before block creation\n", len(pendingTxs))

	// Use the new batching method with minimum transaction requirement
	minTxsPerBlock := 1 // Minimum 1 transaction per block
	if len(pendingTxs) > 5 {
		minTxsPerBlock = 3 // Try to get at least 3 if we have more than 5 pending
	}
	if len(pendingTxs) > 10 {
		minTxsPerBlock = 5 // Try to get at least 5 if we have more than 10 pending
	}

	// Create block with batching
	block, err := n.blockchain.CreateBlockWithBatching(n.nodeAddress, n.nodePrivateKey, minTxsPerBlock)
	if err != nil {
		return fmt.Errorf("failed to create block with batching: %v", err)
	}

	fmt.Printf("🔍 Node: Created block with %d transactions (target was %d+)\n",
		len(block.Transactions), minTxsPerBlock)

	// Add block to blockchain
	if err := n.blockchain.AddBlock(block); err != nil {
		return fmt.Errorf("failed to add block: %v", err)
	}

	// Broadcast block via P2P network
	if err := n.BroadcastBlock(block); err != nil {
		fmt.Printf("Failed to broadcast block via P2P: %v\n", err)
	}

	// Also broadcast via consensus layer
	n.broadcastChan <- &pos.BlockProposal{
		Block: block,
	}

	n.triggerEvent("block_produced", block)
	n.updateBlockProcessingRate()

	fmt.Printf("✅ Produced block %d with %d transactions (gas: %d, fees calculated)\n",
		block.Header.Index, len(block.Transactions), block.Header.GasUsed)

	return nil
}

// Reward Distribution

func (n *Node) distributeEpochRewards(epoch uint64) error {
	// 1. Initialize Rewards as BigInt
	inflationRewardsBig := big.NewInt(0)

	if n.inflationManager != nil {
		inflationRate := float64(0.05) // 5% annual inflation
		if n.config.Economics.InflationRate > 0 {
			inflationRate = n.config.Economics.InflationRate
		}

		// Get Total Supply (*big.Int)
		totalSupply := n.worldState.GetTotalSupply()

		// 2. Perform Calculation using BigFloat to preserve precision
		// Formula: (TotalSupply * InflationRate) / 365

		fTotalSupply := new(big.Float).SetInt(totalSupply)
		fRate := big.NewFloat(inflationRate)
		fDivisor := big.NewFloat(365.0)

		// Calculate numerator: Supply * Rate
		fResult := new(big.Float).Mul(fTotalSupply, fRate)

		// Divide by 365
		fResult.Quo(fResult, fDivisor)

		// 3. Convert result back to *big.Int
		inflationRewardsBig, _ = fResult.Int(nil)

	} else {
		// Default reward amount (1000 Tokens)
		// Adjust this if 1000 implies 1000 Wei or 1000 whole tokens
		// Assuming 1000 whole tokens for default:
		// inflationRewardsBig = new(big.Int).Mul(big.NewInt(1000), config.BaseUnit)
		// For now, sticking to the literal 1000 to match your logic:
		inflationRewardsBig = big.NewInt(1000)
	}

	stakingManager := n.blockchain.GetStakingManager()
	if stakingManager == nil {
		return fmt.Errorf("staking manager not available")
	}

	// 4. Distribute Rewards
	// ✅ NOTE: Assuming DistributeRewards now accepts 'string' or '*big.Int'
	// If it accepts string (consistent with other updates):
	if err := stakingManager.DistributeRewards(inflationRewardsBig.String()); err != nil {
		return fmt.Errorf("reward distribution failed: %v", err)
	}

	n.triggerEvent("epoch_rewards_distributed", map[string]interface{}{
		"epoch":   epoch,
		"rewards": inflationRewardsBig.String(), // Pass string for JSON safety
	})

	fmt.Printf("Epoch %d: Distributed %s tokens in rewards\n", epoch, inflationRewardsBig.String())
	return nil
}

// Message Handling

func (n *Node) handleOutgoingMessage(msg interface{}) {
	switch m := msg.(type) {
	case *pos.BlockProposal:
		fmt.Printf("Broadcasting block proposal: %s\n", m.Block.Hash)
		// Also broadcast via P2P if available
		if err := n.BroadcastBlock(m.Block); err != nil {
			fmt.Printf("Failed to broadcast block via P2P: %v\n", err)
		}
	case *types.Attestation:
		fmt.Printf("Broadcasting attestation\n")
		if n.p2pNetwork != nil {
			if err := n.p2pNetwork.BroadcastAttestation(m); err != nil {
				fmt.Printf("Failed to broadcast attestation: %v\n", err)
			}
		}
	case *pos.Vote:
		fmt.Printf("Broadcasting vote\n")
		if n.p2pNetwork != nil {
			if err := n.p2pNetwork.BroadcastVote(m); err != nil {
				fmt.Printf("Failed to broadcast vote: %v\n", err)
			}
		}
	}
}

func (n *Node) handleIncomingMessage(msg interface{}) {
	fmt.Printf("Received message: %T\n", msg)
	// Process incoming messages from consensus
}

// Cross-shard Processing

func (n *Node) processCrossShardTransfers() {
	csm := n.blockchain.GetCrossShardManager()
	if csm == nil {
		return
	}

	pendingTransfers := csm.GetPendingTransfers()
	for _, transfer := range pendingTransfers {
		if transfer.ToShard == n.shardID && transfer.Status == "pending" {
			if err := n.blockchain.CompleteCrossShardTransfer(transfer.Hash); err != nil {
				fmt.Printf("Failed to complete cross-shard transfer %s: %v\n", transfer.Hash, err)
			} else {
				n.triggerEvent("cross_shard_transfer_completed", transfer)
			}
		}
	}
}

// Maintenance

func (n *Node) performMaintenance() {
	n.blockchain.Cleanup()

	if err := n.blockchain.ValidateStateConsistency(); err != nil {
		fmt.Printf("State consistency check failed: %v\n", err)
		n.triggerEvent("state_inconsistency_detected", err)
	}

	n.updateValidatorMetrics()
	n.triggerEvent("maintenance_completed", time.Now())
}

func (n *Node) storeGenesisConfig(config *NodeConfig) {
	// Store genesis configuration for later use
}

func (n *Node) initializeGenesis() error {
	// ✅ CHECK NODE_ID FIRST, before checking if genesis exists
	nodeID := os.Getenv("NODE_ID")

	// Check if genesis already exists (from previous run or sync)
	if n.blockchain.GetGenesisBlock() != nil {
		fmt.Printf("🏛️  Genesis block already exists\n")
		return nil
	}

	// ✅ Non-leader nodes: Don't create genesis here, will sync after P2P starts
	if nodeID != "1" && nodeID != "" {
		fmt.Printf("⏳ Node %s will sync genesis after P2P network starts...\n", nodeID)
		return nil // Don't create genesis, don't attempt sync yet
	}

	// Node 1: Create genesis
	fmt.Printf("🏗️  Node 1 initializing blockchain genesis...\n")

	// Get genesis data from node config
	genesisAccount := n.genesisAccount
	genesisSupply := n.config.Economics.GenesisSupply

	// Fallback to config accounts if genesisAccount is empty
	if genesisAccount == "" && len(n.config.Genesis.Accounts) > 0 {
		genesisAccount = n.config.Genesis.Accounts[0].Address
	}

	// Prepare genesis validators
	genesisValidators := n.genesisValidators

	// If no genesis validators provided, create one for this node
	if len(genesisValidators) == 0 && n.isValidatorNode {
		genesisValidators = []*core.Validator{
			{
				Address:        n.nodeAddress,
				Pubkey:         n.nodePrivateKey.PublicKey().Bytes(),
				Stake:          n.config.Staking.MinValidatorStake,
				SelfStake:      n.config.Staking.MinValidatorStake,
				DelegatedStake: "0",
				Commission:     0.1,
				Active:         true,
				Delegators:     make(map[string]string),
				CreatedAt:      time.Now().Unix(),
				UpdatedAt:      time.Now().Unix(),
			},
		}
	}

	// Initialize blockchain genesis
	if err := n.blockchain.InitializeGenesis(
		genesisAccount,
		n.nodeAddress,
		genesisSupply,
		genesisValidators,
		n.nodePrivateKey,
	); err != nil {
		if err.Error() == "genesis block already exists" {
			fmt.Printf("✅ Genesis already initialized\n")
			return nil
		}
		return fmt.Errorf("failed to initialize blockchain genesis: %v", err)
	}

	fmt.Printf("✅ Blockchain genesis initialized successfully\n")
	return nil
}

// syncGenesisFromNetwork attempts to sync genesis block from bootstrap nodes
func (n *Node) syncGenesisFromNetwork() error {
	if n.p2pNetwork == nil {
		return fmt.Errorf("P2P network not initialized")
	}

	maxRetries := 12
	retryDelay := 5 * time.Second

	for attempt := 1; attempt <= maxRetries; attempt++ {
		fmt.Printf("📡 Sync attempt %d/%d...\n", attempt, maxRetries)

		peers := n.p2pNetwork.GetConnectedPeerIDs()
		if len(peers) == 0 {
			fmt.Printf("⏳ No peers yet, waiting %v before retry...\n", retryDelay)
			time.Sleep(retryDelay)
			continue
		}

		for _, peerID := range peers {
			// Request Genesis
			blocks, err := n.p2pNetwork.RequestBlockRange(peerID, 0, 0)
			if err != nil {
				fmt.Printf("⚠️ RequestBlockRange failed for peer %s: %v\n", peerID[:8], err)
				continue
			}
			if len(blocks) == 0 {
				fmt.Printf("⚠️ Peer %s returned 0 blocks\n", peerID[:8])
				continue
			}

			fmt.Printf("📡 Trying %d peers for genesis sync...\n", len(peers))

			genesisBlock := blocks[0]
			fmt.Printf("✅ Received genesis block %s from peer %s\n", genesisBlock.Hash[:8], peerID[:8])

			// 1. Add to Blockchain (Triggering our new AddBlockUnsafe logic)
			fmt.Printf("⏳ Adding genesis block to blockchain...\n")
			addDone := make(chan error, 1)
			go func() { addDone <- n.blockchain.AddBlockFromSync(genesisBlock) }()

			select {
			case err := <-addDone:
				if err != nil {
					fmt.Printf("⚠️  Failed to add genesis block from peer %s: %v\n", peerID[:8], err)
					existing := n.blockchain.GetGenesisBlock()
					if existing != nil {
						fmt.Printf("✅ Genesis block already exists locally (%s), proceeding\n", existing.Hash[:8])
					} else {
						continue
					}
				}
			case <-time.After(10 * time.Second):
				fmt.Printf("⚠️ AddBlock timed out for peer %s, checking local state...\n", peerID[:8])
				existing := n.blockchain.GetGenesisBlock()
				if existing != nil {
					fmt.Printf("✅ Genesis block exists locally despite timeout, proceeding\n")
				} else {
					continue
				}
			}

			// 2. Confirm genesis validators are present (proves peer has real state)
			validators := n.blockchain.GetActiveValidators()
			if len(validators) == 0 {
				fmt.Printf("⚠️  Genesis synced but state empty. Retrying...\n")
				continue
			}

			// 3. Sync world state (account balances + validator stakes) from the same peer.
			// Without this, our world state has correct block hashes but zero balances,
			// causing slashing false-positives and broken reward/faucet logic.
			log.Printf("📸 Requesting world state snapshot from peer %s...", peerID[:8])
			snapshot, err := n.p2pNetwork.RequestStateSnapshot(peerID, 0)
			if err != nil {
				log.Printf("⚠️ World state snapshot failed (peer %s): %v — balances will be zero until next restart",
					peerID[:8], err)
				// Non-fatal: consensus will still function, but balance-dependent operations
				// (staking rewards, faucet, slashing enforcement) will not work correctly.
			} else if snapshot == nil || (len(snapshot.Accounts) == 0 && len(snapshot.Validators) == 0) {
				log.Printf("⚠️ Received empty world state snapshot from peer %s — balances will be zero",
					peerID[:8])
			} else {
				log.Printf("📸 Received snapshot: height=%d, accounts=%d, validators=%d",
					snapshot.Height, len(snapshot.Accounts), len(snapshot.Validators))
				if importErr := n.worldState.ImportWorldState(snapshot.Accounts, snapshot.Validators); importErr != nil {
					// Log but don't abort — the guard fires here if state was already populated
					// (e.g. on a retry after a previous successful import). That's safe to ignore.
					log.Printf("⚠️ ImportWorldState: %v", importErr)
				} else {
					log.Printf("✅ World state imported successfully")
				}
			}

			// 4. Register genesis validators with consensus engine
			if n.consensusEngine != nil {
				fmt.Printf("🔄 Force-registering %d genesis validators...\n", len(validators))
				for _, v := range validators {
					n.consensusEngine.RegisterDiscoveredValidator(v)
				}
			}

			// Force re-initialize the validator set from worldstate
			if err := n.consensusEngine.ReinitializeValidatorSet(); err != nil {
				fmt.Printf("⚠️ Failed to reinitialize validator set: %v\n", err)
			} else {
				fmt.Printf("✅ Validator set reinitialized after genesis sync\n")
			}
			fmt.Printf("✅ Genesis sync complete! (Validators: %d)\n", len(validators))
			return nil
		}
		time.Sleep(retryDelay)
	}

	return fmt.Errorf("failed to sync genesis")
}

func (n *Node) registerAsValidator() error {
	// Check if validator already exists
	_, err := n.blockchain.GetValidator(n.nodeAddress)
	if err == nil {
		fmt.Printf("✅ Validator %s already registered, skipping registration\n", n.nodeAddress)
		return nil
	}

	// ✅ Fix: No changes needed here if you updated the method signature above!
	// Both are now strings.
	return n.RegisterValidator(n.config.Staking.MinValidatorStake, 0.1)
}

func (n *Node) isValidator() bool {
	validator, err := n.blockchain.GetValidator(n.nodeAddress)
	if err != nil {
		return false
	}
	return validator.Active
}

func (n *Node) isMyTurn() bool {
	validators := n.blockchain.GetActiveValidators()
	if len(validators) == 0 {
		return false
	}

	myIndex := -1
	for i, validator := range validators {
		if validator.Address == n.nodeAddress {
			myIndex = i
			break
		}
	}

	if myIndex == -1 {
		return false
	}

	currentSlot := time.Now().Unix() / int64(n.config.Consensus.BlockTime)
	assignedValidator := currentSlot % int64(len(validators))

	return int64(myIndex) == assignedValidator
}

func (n *Node) updateBlockProcessingRate() {
	currentTime := time.Now()
	if !n.lastRewardTime.IsZero() {
		duration := currentTime.Sub(n.lastRewardTime)
		n.blockProcessingRate = 1.0 / duration.Seconds()
	}
	n.lastRewardTime = currentTime
}

func (n *Node) updateValidatorMetrics() {
	// Update validator performance metrics
}

// Event System

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
	handlersCopy := make([]func(interface{}), len(handlers))
	copy(handlersCopy, handlers)
	n.mu.RUnlock()

	for _, handler := range handlersCopy {
		go handler(data)
	}
}

// Public API Methods

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

	if n.consensusEngine != nil {
		status["consensus"] = n.consensusEngine.GetStats()
	}

	if n.p2pNetwork != nil {
		status["p2p"] = n.p2pNetwork.GetNetworkStats()
	}

	return status
}

func (n *Node) GetBalance(address string) (*big.Int, error) {
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

// GetDelegations returns delegations for a specific address
func (n *Node) GetDelegations(address string) (map[string]string, error) {
	stakingManager := n.blockchain.GetStakingManager()
	if stakingManager == nil {
		return nil, fmt.Errorf("staking manager not available")
	}
	// Now types match directly
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
	isHealthy := n.isRunning && n.blockchain.IsHealthy()

	if n.p2pNetwork != nil {
		isHealthy = isHealthy && n.p2pNetwork.IsHealthy()
	}

	return isHealthy
}

func (n *Node) GetP2PStats() map[string]interface{} {
	if n.p2pNetwork != nil {
		return n.p2pNetwork.GetNetworkStats()
	}
	return map[string]interface{}{
		"enabled": false,
		"error":   "P2P network not enabled",
	}
}

func (n *Node) GetConnectedPeers() int {
	if n.p2pNetwork != nil {
		return n.p2pNetwork.GetConnectedPeers()
	}
	return 0
}

func (n *Node) IsP2PConnected() bool {
	if n.p2pNetwork != nil {
		return n.p2pNetwork.IsConnected()
	}
	return false
}

func (n *Node) GetPeerID() string {
	if n.p2pNetwork != nil {
		return n.p2pNetwork.GetPeerID()
	}
	return ""
}

func (n *Node) ForceP2PSync() error {
	return n.SyncWithPeers()
}

func (n *Node) processP2PMessageBus() {
	if n.p2pNetwork == nil {
		return
	}

	messageBus := n.p2pNetwork.GetMessageBus()
	if messageBus == nil {
		return
	}

	log.Println("📬 P2P MessageBus processor started")

	for msg := range messageBus {
		switch data := msg.Data.(type) {

		// Handle Block Range Requests
		case map[string]int64:
			n.handleGetBlocksFromHeight(msg)

		// Handle String messages (Heartbeats/Status) - Silence the warning
		case string:
			// Check if this is a height request
			if data == "height" && msg.ResponseCh != nil {
				var height int64
				if n.blockchain != nil {
					height = n.blockchain.GetHeight()
				}
				msg.ResponseCh <- network.Response{Data: height}
			}

			// Unknown types
			// Handle ValidatorAnnouncement
		case *core.Validator:
			if n.consensusEngine != nil {
				if err := n.consensusEngine.RegisterDiscoveredValidator(data); err != nil {
					log.Printf("⚠️ Failed to register discovered validator: %v", err)
				}
			}

		// Handle ValidatorSync (slice of validators)
		case []*core.Validator:
			if n.consensusEngine != nil {
				if err := n.consensusEngine.SyncValidators(data); err != nil {
					log.Printf("⚠️ Failed to sync validators: %v", err)
				}
			}

		// This handles GetStateSnapshot requests initiated by peers via ProtocolStateSync.

		case int64:
			// GetStateSnapshot is the only MessageBus path that sends int64 data (the requested height).
			if msg.ResponseCh == nil {
				log.Printf("⚠️ GetStateSnapshot: no response channel, dropping request")
				break
			}

			accounts := n.worldState.ExportAccounts()
			validators := n.worldState.ExportValidators()

			if len(accounts) == 0 {
				log.Printf("⚠️ GetStateSnapshot: world state has no accounts to export")
				msg.ResponseCh <- network.Response{
					Success: false,
					Error:   fmt.Errorf("world state not yet populated"),
				}
				break
			}

			snapshot := &p2p.StateSnapshot{
				Height:     n.blockchain.GetHeight(),
				StateRoot:  n.worldState.GetStateRoot(),
				Timestamp:  time.Now().Unix(),
				Accounts:   accounts,
				Validators: validators,
			}

			log.Printf("📸 Serving world state snapshot: height=%d, accounts=%d, validators=%d",
				snapshot.Height, len(accounts), len(validators))

			msg.ResponseCh <- network.Response{
				Success: true,
				Data:    snapshot,
			}

		// Unknown types
		default:
			log.Printf("⚠️ Unknown MessageBus message. Type: %T", data)
			if msg.ResponseCh != nil {
				close(msg.ResponseCh)
			}
		}
	}
}

// handleGetBlocksFromHeight fetches blocks for a peer
func (n *Node) handleGetBlocksFromHeight(msg network.Message) {
	data, ok := msg.Data.(map[string]int64)
	if !ok {
		if msg.ResponseCh != nil {
			msg.ResponseCh <- network.Response{
				Success: false,
				Error:   fmt.Errorf("invalid data format"),
			}
		}
		return
	}

	startHeight := data["start"]
	endHeight := data["end"]

	log.Printf("📦 Fetching blocks %d to %d for peer", startHeight, endHeight)

	// Fetch blocks
	var blocks []*core.Block
	for height := startHeight; height <= endHeight; height++ {
		block, err := n.blockchain.GetWorldState().GetBlock(height)
		if err != nil {
			log.Printf("⚠️ Block %d not found: %v", height, err)
			break
		}
		blocks = append(blocks, block)
	}

	log.Printf("✅ Sending %d blocks to peer", len(blocks))

	// Send response
	if msg.ResponseCh != nil {
		msg.ResponseCh <- network.Response{
			Success: true,
			Data:    blocks,
		}
	}
}
