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

	"github.com/thrylos-labs/go-thrylos/api"
	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/crypto"
	"github.com/thrylos-labs/go-thrylos/node"
)

func main() {
	// 1. Define flags (including -config, even if just for compatibility)
	configPath := flag.String("config", "", "Path to configuration file (custom paths not supported by current config loader)")
	dataDir := flag.String("data", "", "Data directory (default from config)")
	p2pPort := flag.Int("p2p-port", 9000, "P2P listen port")
	bootstrapStr := flag.String("bootstrap", "", "Comma-separated bootstrap peers")
	isValidator := flag.Bool("validator", false, "Run this node as a validator")
	validatorKeyPath := flag.String("validator-key", "", "Path to hex-encoded validator private key file")
	envFlag := flag.String("env", "", "Environment (mainnet|testnet|devnet|production|development). Overrides THRYLOS_ENVIRONMENT")
	var enableAPI = flag.Bool("api", true, "Enable embedded API server")
	var apiPort = flag.String("api-port", "8080", "API server port")

	flag.Parse()

	// 2. Load Config
	// Note: We call Load() without args because your config package doesn't support paths.
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// [Fix for Unused Variable Error]
	// We log the config path if the user provided one, just so the variable 'configPath' is used.
	if *configPath != "" {
		log.Printf("ℹ️ Note: -config flag passed as '%s', but using default config location due to loader limitations.", *configPath)
	}

	// 3. Environment Setup
	env := strings.ToLower(strings.TrimSpace(*envFlag))
	if env == "" {
		env = strings.ToLower(strings.TrimSpace(os.Getenv("THRYLOS_ENVIRONMENT")))
	}
	if env == "" {
		env = "production"
	}
	cfg.Environment = env
	cfg.Network.ChainID = config.GetChainIDForEnvironment(env)

	log.Printf("Starting Thrylos in %q environment (chain-id=%s)", env, cfg.Network.ChainID)

	if *dataDir != "" {
		cfg.DataDir = *dataDir
	}

	// 4. Bootstrap Peers
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

	// 5. API Safety Check
	if isProductionLikeEnvironment(env) {
		if cfg.API.EnableAPI && !cfg.API.EnableTLS {
			log.Fatalf("API is enabled but TLS is disabled in %q environment; aborting startup", env)
		}
		cfg.API.EnableFaucet = false
	}

	// 6. Determine Validator Key Path
	keyPath := strings.TrimSpace(*validatorKeyPath)
	if keyPath == "" && cfg.Validator.Enabled {
		keyPath = strings.TrimSpace(cfg.Validator.KeyFilePath)
	}
	if keyPath == "" {
		log.Fatalf("no validator key provided; use -validator-key or config.validator.key_file_path")
	}

	// -------------------------------------------------------------------------
	// [SECURITY FIX] Enforce Security Checks
	// -------------------------------------------------------------------------
	// 1. Verify we aren't using the compromised key.
	// 2. Ensure the dead code in security_check.go is now used.
	EnforceSecurityChecks(keyPath)
	// -------------------------------------------------------------------------

	// 7. Load Private Key
	privKey, err := loadPrivateKeyFromFile(keyPath)
	if err != nil {
		log.Fatalf("failed to load validator private key: %v", err)
	}
	log.Printf("Loaded validator private key from %s", keyPath)

	// 8. Configure Node
	nodeConfig := &node.NodeConfig{
		Config:            cfg,
		PrivateKey:        privKey,
		ShardID:           account.ShardID(0),
		TotalShards:       1,
		IsValidator:       *isValidator,
		DataDir:           cfg.DataDir,
		CrossShardEnabled: false,
		GenesisAccount:    cfg.Genesis.Accounts[0].Address,
		GenesisSupply:     cfg.Genesis.TotalGenesis,
		GenesisValidators: nil,
		EnableP2P:         cfg.P2P.Enabled,
		P2PListenPort:     *p2pPort,
		BootstrapPeers:    bootstrapPeers,
		EnableAPI:         cfg.API.EnableAPI,
		APIPort:           0,
	}

	// 9. Start Node
	thrylosNode, err := node.NewNode(nodeConfig)
	if err != nil {
		log.Fatalf("failed to create node: %v", err)
	}

	log.Printf("Node created; starting event loop (validator=%v)", *isValidator)

	if err := thrylosNode.Start(); err != nil {
		log.Fatalf("node failed to start: %v", err)
	}

	if *enableAPI {
		apiConfig := &api.APIConfig{
			Port:           *apiPort,
			EnableCORS:     true,
			AllowedOrigins: []string{"https://your-frontend.com"},
			EnableFaucet:   false, // Disable faucet in production
			PointsFile:     "points.json",
		}

		if err := node.StartAPI(apiConfig); err != nil {
			log.Fatalf("Failed to start API server: %v", err)
		}
	}

	log.Printf("Node started successfully")
	select {}
}

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
