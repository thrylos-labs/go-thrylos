package main

import (
	"context"
	"log"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

var (
	rpcURL = "http://127.0.0.1:8081"
	client *ethclient.Client
)

func main() {
	// 1. Connect to local blockchain
	var err error
	client, err = ethclient.Dial(rpcURL)
	if err != nil {
		log.Fatal("Failed to connect to blockchain:", err)
	}
	defer client.Close()

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	blockNumber, err := client.BlockNumber(ctx)
	if err != nil {
		log.Printf("⚠️  Could not get block number (is node running?): %v", err)
	} else {
		log.Printf("✓ Connected to blockchain (block: %d)", blockNumber)
	}

	// 2. Setup Router
	r := gin.Default()

	// Enable CORS for local development (allows frontend to talk to this)
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:3000"},
		AllowMethods:     []string{"GET", "POST", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		AllowCredentials: true,
	}))

	// 3. Define Endpoints
	r.GET("/health", func(c *gin.Context) {
		bn, _ := client.BlockNumber(context.Background())
		c.JSON(200, gin.H{
			"status":       "ok",
			"blockchain":   "connected",
			"block_height": bn,
		})
	})

	r.GET("/api/stats", handleStats)
	r.GET("/api/validators", handleValidators)
	r.GET("/api/validators/:address", handleValidatorDetail)
	r.GET("/api/staking/:address", handleStakingInfo)
	r.GET("/api/stakes/:address", handleUserStakes)

	// 4. Start Server
	log.Println("API Server starting on :8080")
	r.Run(":8080")
}

// --- Handlers (Mock Data as per Guide) ---

func handleStats(c *gin.Context) {
	blockNumber, _ := client.BlockNumber(context.Background())
	c.JSON(200, gin.H{
		"total_staked":      "12400000000000000000000000",
		"total_supply":      "23000000000000000000000000",
		"staking_ratio":     0.54,
		"active_validators": 1,
		"current_height":    blockNumber,
		"apy":               12.5,
		"network_status":    "healthy",
	})
}

func handleValidators(c *gin.Context) {
	validators := []map[string]interface{}{
		{
			"address":    "0x5FbDB2315678afecb367f032d93F642f64180aa3",
			"name":       "Local Validator",
			"stake":      "10000000000000000000000",
			"delegators": 0,
			"apy":        12.5,
			"uptime":     100.0,
			"status":     "active",
			"commission": 10.0,
			"rank":       1,
		},
	}
	c.JSON(200, validators)
}

func handleValidatorDetail(c *gin.Context) {
	address := c.Param("address")
	c.JSON(200, gin.H{
		"address":         address,
		"name":            "Local Validator",
		"stake":           "10000000000000000000000",
		"delegators":      0,
		"apy":             12.5,
		"uptime":          100.0,
		"status":          "active",
		"commission":      10.0,
		"blocks_proposed": 145,
		"blocks_missed":   0,
	})
}

func handleStakingInfo(c *gin.Context) {
	address := c.Param("address")
	c.JSON(200, gin.H{
		"address":       address,
		"total_staked":  "0",
		"total_rewards": "0",
		"stakes":        []interface{}{},
	})
}

func handleUserStakes(c *gin.Context) {
	address := c.Param("address")
	c.JSON(200, gin.H{
		"address": address,
		"stakes":  []interface{}{},
	})
}
