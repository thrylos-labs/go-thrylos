//go:build dev
// +build dev

package main

import (
	"flag"
	"fmt"
	"log"
	"strings"

	"github.com/thrylos-labs/go-thrylos/config"
)

// Dev entrypoint that uses deterministic keys & shared genesis validators
func main() {
	var nodeID = flag.Int("node", 1, "Node ID (1, 2, 3)")
	var p2pPort = flag.Int("p2p-port", 9000, "P2P listen port")
	var bootstraps = flag.String("bootstrap", "", "Comma-separated bootstrap peers")
	var dataDir = flag.String("data", "", "Data directory (default: ./data-nodeN)")
	var validator = flag.Bool("validator", true, "Run as validator")

	flag.Parse()

	if *nodeID < 1 || *nodeID > 3 {
		log.Fatalf("Node ID must be 1, 2, or 3 in dev mode")
	}

	if *dataDir == "" {
		*dataDir = fmt.Sprintf("./data-node%d", *nodeID)
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// -------------------------------------------------------------------------
	// [SECURITY FIX] Enforce Compromised Key Check
	// -------------------------------------------------------------------------
	// This checks if 'server.key' exists in the root (and kills the process if so)
	// and verifies that the configured key path is not the compromised file.
	// We pass cfg.Consensus.PrivateKeyPath assuming your config struct has this field.
	// If your dev setup generates keys dynamically inside startDevNode without
	// updating cfg, this check still protects against the root 'server.key' file.
	EnforceSecurityChecks(cfg.Consensus.PrivateKeyPath)
	// -------------------------------------------------------------------------

	// Force dev environment for this build
	cfg.Environment = "development"
	cfg.Network.ChainID = config.GetChainIDForEnvironment(cfg.Environment)

	// Dev: enable faucet & HTTP API by default
	cfg.API.EnableAPI = true
	cfg.API.EnableTLS = false
	cfg.API.EnableFaucet = true

	// Prepare bootstrap peers
	var bootstrapPeers []string
	if *bootstraps != "" {
		for _, p := range strings.Split(*bootstraps, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				bootstrapPeers = append(bootstrapPeers, p)
			}
		}
	} else {
		bootstrapPeers = append(bootstrapPeers, cfg.P2P.BootstrapPeers...)
	}

	if err := startDevNode(*nodeID, *dataDir, *p2pPort, bootstrapPeers, *validator, cfg); err != nil {
		log.Fatalf("dev node failed: %v", err)
	}

	select {} // keep node running
}
