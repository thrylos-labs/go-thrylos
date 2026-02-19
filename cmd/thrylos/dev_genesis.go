//go:build dev
// +build dev

package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/node"
	"github.com/thrylos-labs/go-thrylos/proto/core"
)

// Deterministically derive a private key per-node for dev networks only.
func getNodeSpecificPrivateKey(nodeID int) (crypto.PrivateKey, error) {
	seedStr := fmt.Sprintf("thrylos-development-node-key-%d-2024", nodeID)
	hash := sha256.Sum256([]byte(seedStr))
	// Use internal wrapper
	return crypto.NewPrivateKeyFromBytes(hash[:])
}

func createAllValidators(cfg *config.Config) ([]*core.Validator, []crypto.PrivateKey, []string, error) {
	validators := make([]*core.Validator, 0, 4)
	privateKeys := make([]crypto.PrivateKey, 0, 4)
	addresses := make([]string, 0, 4)

	metadata := map[int]struct {
		Name        string
		Description string
		Website     string
		Commission  float64
	}{
		1: {"Iron Peak", "Description 1", "https://thrylos.org", 0.05},
		2: {"Storm Rider", "Description 2", "https://thrylos.org", 0.08},
		3: {"Crystal Weaver", "Description 3", "https://thrylos.org", 0.10},
		4: {"Shadow Walker", "Description 4", "https://thrylos.org", 0.05},
	}

	for nodeID := 1; nodeID <= 4; nodeID++ {
		priv, err := getNodeSpecificPrivateKey(nodeID)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to derive dev private key for node %d: %w", nodeID, err)
		}

		// --- STEP 1: GET COMPRESSED BYTES (33 BYTES) ---
		pubKey := priv.PublicKey()
		pubBytes := pubKey.Bytes() // This is 33 bytes per your crypto implementation

		// --- STEP 2: GENERATE ADDRESS ---
		// Passing the pubKey object is best; it handles internal formatting.
		addr, err := account.GenerateAddress(pubKey)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to derive address for node %d: %w", nodeID, err)
		}

		// ✅ DEBUG: Verify alignment
		// Note: len(pubBytes) should be 33
		fmt.Printf("🔑 Node %d | PubKeyLen: %d | Address: %s\n", nodeID, len(pubBytes), addr)

		meta := metadata[nodeID]
		validator := &core.Validator{
			Address:        addr,
			Pubkey:         pubBytes, // Consistently storing 33 bytes now
			Stake:          "3000000000000000000000",
			SelfStake:      "3000000000000000000000",
			DelegatedStake: "0",
			Commission:     meta.Commission,
			Active:         true,
			Delegators:     make(map[string]string),
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

func getNodeP2PKey(nodeID int) (libp2pcrypto.PrivKey, error) {
	seedStr := fmt.Sprintf("thrylos-development-p2p-key-%d-2024", nodeID)
	hash := sha256.Sum256([]byte(seedStr))
	privKey, _, err := libp2pcrypto.GenerateEd25519Key(
		bytes.NewReader(hash[:]),
	)
	return privKey, err
}

func startDevNode(nodeID int, dataDir string, p2pPort int, bootstrapPeers []string, isValidator bool, cfg *config.Config) (*node.Node, error) {
	allValidators, allPrivateKeys, allAddresses, err := createAllValidators(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create dev validators: %w", err)
	}

	if nodeID < 1 || nodeID > len(allPrivateKeys) {
		return nil, fmt.Errorf("invalid nodeID %d", nodeID)
	}

	// Derive stable P2P identity key for this node
	p2pKey, err := getNodeP2PKey(nodeID)
	if err != nil {
		return nil, fmt.Errorf("failed to derive p2p key for node %d: %w", nodeID, err)
	}

nodePrivateKey := allPrivateKeys[nodeID-1]
genesisAccount := allAddresses[0]

nodeConfig := &node.NodeConfig{
    Config:            cfg,
    PrivateKey:        nodePrivateKey,
    ShardID:           account.ShardID(0),
    TotalShards:       1,
    IsValidator:       isValidator,
    DataDir:           dataDir,
    GenesisAccount:    genesisAccount,
    GenesisSupply:     cfg.Genesis.TotalGenesis,
    GenesisValidators: allValidators,
    EnableP2P:         cfg.P2P.Enabled,
    P2PListenPort:     p2pPort,
    BootstrapPeers:    bootstrapPeers,
    EnableAPI:         cfg.API.EnableAPI,
    P2PIdentityKey:    p2pKey,
}

	}

	n, err := node.NewNode(nodeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dev node: %w", err)
	}

	// Remove the temporary peer ID log — it's now predictable
	// log.Printf("🔑 Node 1 Peer ID: %s", n.GetP2PHost().ID().String())

	if err := n.Start(); err != nil {
		return nil, fmt.Errorf("failed to start dev node: %w", err)
	}

	return n, nil
}
