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

	// Force dev environment
	cfg.Environment = "development"
	cfg.Network.ChainID = config.GetChainIDForEnvironment(cfg.Environment)

	// -------------------------------------------------------------------------
	// PORT CONFIGURATION
	// -------------------------------------------------------------------------
	cfg.API.EnableAPI = true
	cfg.API.EnableTLS = false
	cfg.API.EnableFaucet = true
	cfg.API.RESTAddr = ":8081"

	// ✅ REMOVED: cfg.Consensus.MinStake = "0"
	// The minimum stake is controlled by cfg.Staking.MinValidatorStake
	// which is already set in config.go and genesis.json
	// -------------------------------------------------------------------------

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

	// Start the Node
	if err := startDevNode(*nodeID, *dataDir, *p2pPort, bootstrapPeers, *validator, cfg); err != nil {
		log.Fatalf("dev node failed: %v", err)
	}

	select {} // keep node running
}
