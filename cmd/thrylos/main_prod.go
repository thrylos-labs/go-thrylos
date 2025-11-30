//go:build !dev
// +build !dev

package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/node"
)

func main() {
	// CLI flags for production
	dataDir := flag.String("data", "", "Data directory (default from config)")
	p2pPort := flag.Int("p2p-port", 9000, "P2P listen port")
	bootstrapStr := flag.String("bootstrap", "", "Comma-separated bootstrap peers")
	isValidator := flag.Bool("validator", false, "Run this node as a validator")
	validatorKeyPath := flag.String("validator-key", "", "Path to hex-encoded validator private key file")
	envFlag := flag.String("env", "", "Environment (mainnet|testnet|devnet|production|development). Overrides THRYLOS_ENVIRONMENT")

	flag.Parse()

	// Load base config
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// Decide environment: CLI > env var > default
	env := strings.ToLower(strings.TrimSpace(*envFlag))
	if env == "" {
		env = strings.ToLower(strings.TrimSpace(os.Getenv("THRYLOS_ENVIRONMENT")))
	}
	if env == "" {
		env = "production"
	}
	cfg.Environment = env

	// Derive chain ID from environment
	cfg.Network.ChainID = config.GetChainIDForEnvironment(env)

	log.Printf("Starting Thrylos in %q environment (chain-id=%s)", env, cfg.Network.ChainID)

	// Override data dir if provided
	if *dataDir != "" {
		cfg.DataDir = *dataDir
	}

	// Prepare bootstrap peers
	var bootstrapPeers []string
	if *bootstrapStr != "" {
		for _, p := range strings.Split(*bootstrapStr, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				bootstrapPeers = append(bootstrapPeers, p)
			}
		}
	} else {
		bootstrapPeers = append(bootstrapPeers, cfg.P2P.BootstrapPeers...)
	}

	// Enforce safe API settings in production-like environments
	if isProductionLikeEnvironment(env) {
		if cfg.API.EnableAPI && !cfg.API.EnableTLS {
			log.Fatalf("API is enabled but TLS is disabled in %q environment; aborting startup", env)
		}
		// Never enable faucet in production-like env
		cfg.API.EnableFaucet = false
	}

	// Decide validator key path: CLI flag wins, then config.Validator
	keyPath := strings.TrimSpace(*validatorKeyPath)
	if keyPath == "" && cfg.Validator.Enabled {
		keyPath = strings.TrimSpace(cfg.Validator.KeyFilePath)
	}
	if keyPath == "" {
		log.Fatalf("no validator key provided; use -validator-key or config.validator.key_file_path")
	}

	// Load validator/node private key
	privKey, err := loadPrivateKeyFromFile(keyPath)
	if err != nil {
		log.Fatalf("failed to load validator private key: %v", err)
	}

	log.Printf("Loaded validator private key from %s", keyPath)

	// Build NodeConfig for production (no deterministic keys, no built-in faucet)
	nodeConfig := &node.NodeConfig{
		Config:            cfg,
		PrivateKey:        privKey,
		ShardID:           account.ShardID(0),
		TotalShards:       1,
		IsValidator:       *isValidator,
		DataDir:           cfg.DataDir,
		CrossShardEnabled: false,

		// Genesis configuration: use config-based genesis; no shared deterministic validators
		GenesisAccount:    cfg.Genesis.Accounts[0].Address,
		GenesisSupply:     cfg.Genesis.TotalGenesis,
		GenesisValidators: nil, // Node.initializeGenesis will create a validator ONLY if IsValidatorNode==true

		// P2P
		EnableP2P:      cfg.P2P.Enabled,
		P2PListenPort:  *p2pPort,
		BootstrapPeers: bootstrapPeers,

		// API (TLS enforced above)
		EnableAPI: cfg.API.EnableAPI,
		APIPort:   0, // let node.parsePortFromAddr derive it from cfg.API.RESTAddr
	}

	// Create node
	thrylosNode, err := node.NewNode(nodeConfig)
	if err != nil {
		log.Fatalf("failed to create node: %v", err)
	}

	log.Printf("Node created; starting event loop (validator=%v)", *isValidator)

	// Start node
	if err := thrylosNode.Start(); err != nil {
		log.Fatalf("node failed to start: %v", err)
	}

	log.Printf("Node started successfully")
	select {} // keep process alive; node manages shutdown via signals internally if implemented
}

// loadPrivateKeyFromFile expects a hex-encoded ed25519 private key (64 bytes raw).
func loadPrivateKeyFromFile(path string) (crypto.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading key file %s: %w", path, err)
	}

	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, fmt.Errorf("key file %s is empty", path)
	}

	raw, err := hex.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("failed to hex-decode key in %s: %w", path, err)
	}

	priv, err := crypto.NewPrivateKeyFromBytes(raw)
	if err != nil {
		return nil, fmt.Errorf("failed to construct private key from bytes: %w", err)
	}

	return priv, nil
}

func isProductionLikeEnvironment(env string) bool {
	switch strings.ToLower(env) {
	case "production", "prod", "mainnet":
		return true
	default:
		return false
	}
}
