package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/thrylos-labs/go-thrylos/api"
)

var (
	apiPort       = flag.String("port", "8080", "API server port")
	rpcURL        = flag.String("node", "http://127.0.0.1:8081", "Node RPC URL")
	client        *ethclient.Client
	pointsManager *api.PointsManager // ✅ Global Points Manager
)

func main() {
	flag.Parse()

	// 1. Initialize Points System (Loads points.json)
	pointsManager = api.NewPointsManager("points.json")
	log.Println("🏆 Points Manager initialized")

	// 2. Connect to local blockchain
	var err error
	client, err = ethclient.Dial(*rpcURL)
	if err != nil {
		log.Printf("⚠️  Failed to connect to blockchain at %s: %v", *rpcURL, err)
	} else {
		defer client.Close()
		log.Printf("✓ Connected to blockchain at %s", *rpcURL)
	}

	// 3. Setup Router
	r := gin.Default()

	// Enable CORS
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:3000", "*"}, // Add your frontend URL
		AllowMethods:     []string{"GET", "POST", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		AllowCredentials: true,
	}))

	// 4. Define Endpoints

	// System Health
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok", "node": *rpcURL})
	})

	// Blockchain Data (Mock/Real)
	r.GET("/api/stats", handleStats)
	r.GET("/api/validators", handleValidators)
	r.GET("/api/validators/:address", handleValidatorDetail)
	r.GET("/api/staking/:address", handleStakingInfo)
	r.GET("/api/stakes/:address", handleUserStakes)

	// ✅ NEW: Points & Gamification Endpoints
	r.GET("/api/v1/points", handleGetPoints)
	r.GET("/api/v1/leaderboard", handleLeaderboard)

	// ✅ NEW: Faucet (Awards Tokens + Points)
	r.POST("/fund", handleFund) // Supports POST body
	r.GET("/fund", handleFund)  // Supports GET query param (easier for testing)

	// 5. Start Server
	addr := fmt.Sprintf(":%s", *apiPort)
	log.Printf("🚀 API Server starting on %s", addr)
	log.Println("📝 Points Endpoints Ready: /api/v1/points, /api/v1/leaderboard, /fund")

	if err := r.Run(addr); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

// --- ✅ NEW HANDLERS FOR POINTS ---

// handleGetPoints: Returns user points and estimated token share
func handleGetPoints(c *gin.Context) {
	address := c.Query("address")
	if address == "" {
		c.JSON(400, gin.H{"error": "Address required"})
		return
	}

	// 1. Get data from PointsManager
	user := pointsManager.GetUserPoints(address)

	// 2. (Optional) Sync with real chain data if connected
	// In a real app, you would check 'client' here to see if they staked
	// and call pointsManager.RecordDelegation(address) if they did.

	c.JSON(200, gin.H{
		"address":     user.Address,
		"points":      user.TotalPoints,
		"rank":        "Member", // You can calculate rank dynamically later
		"streak":      user.CurrentStreak,
		"next_faucet": user.LastFaucet.Add(24 * time.Hour),
	})
}

// handleLeaderboard: Returns top 50 users
func handleLeaderboard(c *gin.Context) {
	leaderboard := pointsManager.GetLeaderboard(50)
	c.JSON(200, leaderboard)
}

// handleFund: The Faucet Logic
func handleFund(c *gin.Context) {
	// Support both GET query and POST JSON
	address := c.Query("address")
	if address == "" {
		var json struct {
			Address string `json:"address"`
		}
		if err := c.BindJSON(&json); err == nil {
			address = json.Address
		}
	}

	if address == "" {
		c.JSON(400, gin.H{"error": "Address is required"})
		return
	}

	// 1. Award Points (Limit: Once per 24h)
	newTotal, success := pointsManager.AwardFaucet(address)

	if !success {
		// Option A: Fail the request if they claimed recently
		c.JSON(429, gin.H{
			"error":      "You can only use the faucet once every 24 hours.",
			"next_claim": pointsManager.GetUserPoints(address).LastFaucet.Add(24 * time.Hour),
		})
		return
	}

	// 2. Send Real Tokens (TODO: Add your Transaction Signing Logic Here)
	// For now, we mock the success so the UI updates
	// In production, call: sendTokens(address, 100)
	txHash := "0xMOCK_TX_HASH_" + fmt.Sprintf("%d", time.Now().UnixNano())

	log.Printf("💧 Faucet used by %s. Points: %d", address, newTotal)

	c.JSON(200, gin.H{
		"status":  "success",
		"message": "Sent 100 THR",
		"txHash":  txHash,
		"points":  newTotal,
	})
}

// --- EXISTING MOCK HANDLERS (Unchanged) ---

func handleStats(c *gin.Context) {
	response := gin.H{
		"total_staked":      "12400000000000000000000000",
		"total_supply":      "23000000000000000000000000",
		"staking_ratio":     0.54,
		"active_validators": 1,
		"apy":               12.5,
		"network_status":    "healthy",
	}
	if client != nil {
		bn, err := client.BlockNumber(context.Background())
		if err == nil {
			response["current_height"] = bn
		}
	}
	c.JSON(200, response)
}

func handleValidators(c *gin.Context) {
	c.JSON(200, []map[string]interface{}{{
		"address": "0x5FbDB2315678afecb367f032d93F642f64180aa3",
		"name":    "Local Validator",
		"stake":   "10000000000000000000000",
		"status":  "active",
		"apy":     12.5,
	}})
}

func handleValidatorDetail(c *gin.Context) {
	c.JSON(200, gin.H{"address": c.Param("address"), "name": "Local Validator", "uptime": 100.0})
}

func handleStakingInfo(c *gin.Context) {
	c.JSON(200, gin.H{"address": c.Param("address"), "total_staked": "0", "stakes": []interface{}{}})
}

func handleUserStakes(c *gin.Context) {
	c.JSON(200, gin.H{"address": c.Param("address"), "stakes": []interface{}{}})
}
