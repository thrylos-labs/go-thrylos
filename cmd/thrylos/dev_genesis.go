//go:build dev
// +build dev

package main

import (
	"crypto/sha256"
	"fmt"
	"time"

	// ✅ Import for Decompression (Fixes Signature Mismatch)
	ethcrypto "github.com/ethereum/go-ethereum/crypto"

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
	// Internal wrapper usage
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

		// --- FIX 1: KEY DECOMPRESSION (Solves Signature Mismatch) ---

		// 1. Get default key bytes (likely 33 bytes compressed)
		defaultPubBytes := priv.PublicKey().Bytes()

		// 2. Decompress to 65 bytes using go-ethereum
		// This ensures the key format matches what the consensus engine expects.
		ecdsaPub, err := ethcrypto.DecompressPubkey(defaultPubBytes)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to decompress pubkey for node %d: %w", nodeID, err)
		}

		// 3. Serialize back to 65 uncompressed bytes
		uncompressedPubBytes := ethcrypto.FromECDSAPub(ecdsaPub)

		// -----------------------------------------------------------

		addr, err := account.GenerateAddress(priv.PublicKey())
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to derive address for node %d: %w", nodeID, err)
		}

		meta := metadata[nodeID]
		validator := &core.Validator{
			Address:        addr,
			Pubkey:         uncompressedPubBytes, // Store the 65-byte version
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

func startDevNode(nodeID int, dataDir string, p2pPort int, bootstrapPeers []string, isValidator bool, cfg *config.Config) (*node.Node, error) {
	allValidators, allPrivateKeys, allAddresses, err := createAllValidators(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create dev validators: %w", err)
	}

	if nodeID < 1 || nodeID > len(allPrivateKeys) {
		return nil, fmt.Errorf("invalid nodeID %d", nodeID)
	}

	nodePrivateKey := allPrivateKeys[nodeID-1]

	// --- FIX 2: STATIC GENESIS ACCOUNT (Solves "Fetching blocks 0 to 0" loop) ---
	// We force ALL nodes to recognize the FIRST validator (Node 1) as the Genesis Account.
	// This ensures everyone generates the exact same Genesis Block Hash.
	genesisAccount := allAddresses[0]
	// -----------------------------------------------------------------------------

	nodeConfig := &node.NodeConfig{
		Config:            cfg,
		PrivateKey:        nodePrivateKey,
		ShardID:           account.ShardID(0),
		TotalShards:       1,
		IsValidator:       isValidator,
		DataDir:           dataDir,
		GenesisAccount:    genesisAccount, // Static account
		GenesisSupply:     cfg.Genesis.TotalGenesis,
		GenesisValidators: allValidators,
		EnableP2P:         cfg.P2P.Enabled,
		P2PListenPort:     p2pPort,
		BootstrapPeers:    bootstrapPeers,
		EnableAPI:         cfg.API.EnableAPI,
	}

	n, err := node.NewNode(nodeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dev node: %w", err)
	}

	if err := n.Start(); err != nil {
		return nil, fmt.Errorf("failed to start dev node: %w", err)
	}

	fmt.Printf("✅ Dev node %d started. Genesis Account: %s\n", nodeID, genesisAccount)
	return n, nil
}
