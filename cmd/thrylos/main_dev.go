// cmd/thrylos/main_dev.go
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
	// enableAPI flag removed — API is started inside node.Start() when EnableAPI=true in config

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

	cfg.Environment = "development"
	cfg.Network.ChainID = "thrylos-devnet-1337"

	if cfg.GenesisTimestamp == 0 {
		cfg.GenesisTimestamp = 1772016307
	}

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

	devNode, err := startDevNode(*nodeID, *dataDir, *p2pPort, bootstrapPeers, *validator, cfg)
	if err != nil {
		log.Fatalf("dev node failed: %v", err)
	}

	log.Println("✅ Node running")
	waitForShutdown(devNode.Stop)
}
