//go:build dev
// +build dev

package main

import (
	"flag"
	"fmt"
	"log"
	"strings"

	"github.com/thrylos-labs/go-thrylos/config"
	// "github.com/thrylos-labs/go-thrylos/api" // Remove if not used
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
	// 1. Enable API so the server starts.
	cfg.API.EnableAPI = true
	cfg.API.EnableTLS = false
	cfg.API.EnableFaucet = true

	// 2. MOVE Internal REST API to 8081.
	// This is the crucial fix. It frees up Port 8545.
	// The node's Ethereum RPC service (which is separate) should then be able
	// to bind to 8545 default.
	cfg.API.RESTAddr = ":8081"
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
