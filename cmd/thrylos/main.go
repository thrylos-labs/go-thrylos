package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/chain"
	"github.com/thrylos-labs/go-thrylos/proto/core"
)

func main() {
	fmt.Println("🚀 Starting Thrylos V2...")

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Test protobuf generation
	testAccount := &core.Account{
		Address:      "tl1example123",
		Balance:      1000000000, // 1 THRYLOS
		Nonce:        0,
		StakedAmount: 0,
		DelegatedTo:  make(map[string]int64),
		Rewards:      0,
	}

	fmt.Printf("✅ Protobuf working! Test account: %+v\n", testAccount)

	// Initialize blockchain
	blockchain, err := chain.NewBlockchain(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize blockchain: %v", err)
	}

	fmt.Printf("✅ Blockchain initialized! Genesis: %s\n", blockchain.Status())

	// Graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	fmt.Println("🎉 Thrylos V2 node running! Press Ctrl+C to stop.")
	<-c

	fmt.Println("🛑 Shutting down Thrylos V2...")
	fmt.Println("👋 Goodbye!")
}
