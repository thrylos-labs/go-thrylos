//go:build dev
// +build dev

package main

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/node" // UNCOMMENTED: Required for NodeConfig and NewNode
	"github.com/thrylos-labs/go-thrylos/proto/core"
)

// Deterministically derive a secp256k1 private key per-node for dev networks only.
func getNodeSpecificPrivateKey(nodeID int) (crypto.PrivateKey, error) {
	seedStr := fmt.Sprintf("thrylos-development-node-key-%d-2024", nodeID)

	// FIX: SHA256 produces exactly 32 bytes (256 bits), which fits Secp256k1 requirements.
	hash := sha256.Sum256([]byte(seedStr))

	return crypto.NewPrivateKeyFromBytes(hash[:])
}

// createAllValidators generates a shared genesis validator set for dev networks.
func createAllValidators(cfg *config.Config) ([]*core.Validator, []crypto.PrivateKey, []string, error) {
	validators := make([]*core.Validator, 0, 3)
	privateKeys := make([]crypto.PrivateKey, 0, 3)
	addresses := make([]string, 0, 3)

	metadata := map[int]struct {
		Name        string
		Description string
		Website     string
		Commission  float64
	}{
		1: {
			Name:        "Iron Peak",
			Description: "Standing strong as the unshakeable foundation of Thrylos",
			Website:     "https://thrylos.org",
			Commission:  0.05,
		},
		2: {
			Name:        "Storm Rider",
			Description: "Harnessing the power of digital storms to drive the network forward",
			Website:     "https://thrylos.org",
			Commission:  0.08,
		},
		3: {
			Name:        "Crystal Weaver",
			Description: "Weaving perfect crystal-like consensus across the network",
			Website:     "https://thrylos.org",
			Commission:  0.10,
		},
	}

	for nodeID := 1; nodeID <= 1; nodeID++ {
		priv, err := getNodeSpecificPrivateKey(nodeID)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to derive dev private key for node %d: %w", nodeID, err)
		}

		// Use the public key interface to generate the address
		addr, err := account.GenerateAddress(priv.PublicKey())
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to derive address for node %d: %w", nodeID, err)
		}

		meta := metadata[nodeID]

		validator := &core.Validator{
			Address:        addr,
			Pubkey:         priv.PublicKey().Bytes(),
			Stake:          "3000000000000000000000",
			SelfStake:      "3000000000000000000000",
			DelegatedStake: "0", // Fix: String "0" instead of int 0
			Commission:     meta.Commission,
			Active:         true,
			Delegators:     make(map[string]string), // Fix: map[string]string instead of int64
			Name:           meta.Name,
			Description:    meta.Description,
			Website:        meta.Website,
			CreatedAt:      time.Now().Unix(),
			UpdatedAt:      time.Now().Unix(),
		}

		validators = append(validators, validator)
		privateKeys = append(privateKeys, priv)
		addresses = append(addresses, addr)
	}

	return validators, privateKeys, addresses, nil
}

// startDevNode wires deterministic keys + shared genesis into the NodeConfig and starts the node.
func startDevNode(nodeID int, dataDir string, p2pPort int, bootstrapPeers []string, isValidator bool, cfg *config.Config) error {
	allValidators, allPrivateKeys, allAddresses, err := createAllValidators(cfg)
	if err != nil {
		return fmt.Errorf("failed to create dev validators: %w", err)
	}

	if nodeID < 1 || nodeID > len(allPrivateKeys) {
		return fmt.Errorf("invalid nodeID %d (expected 1..%d)", nodeID, len(allPrivateKeys))
	}

	nodePrivateKey := allPrivateKeys[nodeID-1]
	nodeAddress := allAddresses[nodeID-1]

	fmt.Printf("🔑 Dev node %d address: %s\n", nodeID, nodeAddress)
	fmt.Printf("👥 Dev genesis validators: %v\n", allAddresses)

	nodeConfig := &node.NodeConfig{
		Config:            cfg,
		PrivateKey:        nodePrivateKey,
		ShardID:           account.ShardID(0),
		TotalShards:       1,
		IsValidator:       isValidator,
		DataDir:           dataDir,
		CrossShardEnabled: false,

		GenesisAccount:    cfg.Genesis.Accounts[0].Address,
		GenesisSupply:     cfg.Genesis.TotalGenesis,
		GenesisValidators: allValidators,

		EnableP2P:      cfg.P2P.Enabled,
		P2PListenPort:  p2pPort,
		BootstrapPeers: bootstrapPeers,

		EnableAPI: cfg.API.EnableAPI,
		APIPort:   0, // let node determine from cfg.API.RESTAddr
	}

	n, err := node.NewNode(nodeConfig)
	if err != nil {
		return fmt.Errorf("failed to create dev node: %w", err)
	}

	if err := n.Start(); err != nil {
		return fmt.Errorf("failed to start dev node: %w", err)
	}

	fmt.Printf("✅ Dev node %d started successfully\n", nodeID)
	return nil
}
