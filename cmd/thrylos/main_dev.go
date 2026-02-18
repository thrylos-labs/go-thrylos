// cmd/thrylos/main_dev.go
//go:build dev
// +build dev

package main

import (
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
)

func main() {
	var nodeID = flag.Int("node", 1, "Node ID (1, 2, 3)")
	var p2pPort = flag.Int("p2p-port", 9000, "P2P listen port")
	var bootstraps = flag.String("bootstrap", "", "Comma-separated bootstrap peers")
	var dataDir = flag.String("data", "", "Data directory (default: ./data-nodeN)")
	var validator = flag.Bool("validator", true, "Run as validator")
	var enableAPI = flag.Bool("api", true, "Enable embedded API server")

	flag.Parse()

	if *nodeID < 1 || *nodeID > 4 {
		log.Fatalf("Node ID must be 1-4 in dev mode")
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
	cfg.Network.ChainID = "thrylos-devnet-1337"

	// Fixed genesis timestamp for deterministic genesis across all nodes
	cfg.GenesisTimestamp = time.Now().Unix()

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
	node, err := startDevNode(*nodeID, *dataDir, *p2pPort, bootstrapPeers, *validator, cfg)
	if err != nil {
		log.Fatalf("dev node failed: %v", err)
	}

	// ✅ NEW: Start embedded API server
	if *enableAPI {
		if err := node.StartAPI(); err != nil { // No apiConfig arg needed
			log.Printf("⚠️  Failed to start API server: %v", err)
		}
	}

	log.Println("✅ Node running with embedded API")
	select {} // keep node running
}
