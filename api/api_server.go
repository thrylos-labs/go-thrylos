// api/server.go
package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/thrylos-labs/go-thrylos/core/state"
)

// APIServer provides REST API endpoints for the blockchain
type APIServer struct {
	router        *gin.Engine
	server        *http.Server
	worldState    *state.WorldState
	pointsManager *PointsManager
	config        *APIConfig
}

// NewAPIServer creates a new API server
func NewAPIServer(worldState *state.WorldState, config *APIConfig) *APIServer {
	if config == nil {
		config = DefaultAPIConfig()
	}

	// Initialize points manager
	pointsManager := NewPointsManager(config.PointsFile)
	log.Println("🏆 Points Manager initialized")

	// Setup Gin router
	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()

	// Enable CORS if configured
	if config.EnableCORS {
		router.Use(cors.New(cors.Config{
			AllowOrigins:     config.AllowedOrigins,
			AllowMethods:     []string{"GET", "POST", "OPTIONS"},
			AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
			AllowCredentials: true,
		}))
	}

	srv := &APIServer{
		router:        router,
		worldState:    worldState,
		pointsManager: pointsManager,
		config:        config,
	}

	// Register all routes
	srv.setupRoutes()

	return srv
}

// setupRoutes registers all API endpoints
func (s *APIServer) setupRoutes() {
	// Health check
	s.router.GET("/health", s.handleHealth)

	// Blockchain stats
	s.router.GET("/api/stats", s.handleStats)
	s.router.GET("/api/validators", s.handleValidators)
	s.router.GET("/api/validators/:address", s.handleValidatorDetail)
	s.router.GET("/api/staking/:address", s.handleStakingInfo)
	s.router.GET("/api/stakes/:address", s.handleUserStakes)

	// Points & Gamification
	s.router.GET("/api/v1/points", s.handleGetPoints)
	s.router.GET("/api/v1/leaderboard", s.handleLeaderboard)

	// Faucet (if enabled)
	if s.config.EnableFaucet {
		s.router.POST("/fund", s.handleFund)
		s.router.GET("/fund", s.handleFund)
	}
}

// Start starts the API server
func (s *APIServer) Start() error {
	addr := fmt.Sprintf(":%s", s.config.Port)

	s.server = &http.Server{
		Addr:         addr,
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("🌐 HTTP API Server starting on port %s", s.config.Port)

	// Block here — APIManager.Start() owns the goroutine
	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Stop gracefully shuts down the API server
func (s *APIServer) Stop() error {
	if s.server == nil {
		return nil
	}

	log.Println("🛑 Shutting down API server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return s.server.Shutdown(ctx)
}

// --- Handler Functions ---

func (s *APIServer) handleHealth(c *gin.Context) {
	c.JSON(200, gin.H{
		"status": "ok",
		"node":   "embedded",
	})
}

func (s *APIServer) handleStats(c *gin.Context) {
	// Get real data from worldState
	validators := s.worldState.GetActiveValidators()

	c.JSON(200, gin.H{
		"total_staked":      "12400000000000000000000000", // TODO: Calculate from validators
		"total_supply":      "23000000000000000000000000",
		"staking_ratio":     0.54,
		"active_validators": len(validators),
		"apy":               12.5,
		"network_status":    "healthy",
	})
}

func (s *APIServer) handleValidators(c *gin.Context) {
	validators := s.worldState.GetActiveValidators()

	result := make([]map[string]interface{}, 0, len(validators))
	for _, v := range validators {
		result = append(result, map[string]interface{}{
			"address": v.Address,
			"name":    "Validator", // Could add to validator struct
			"stake":   v.Stake,
			"status":  "active",
			"apy":     12.5, // TODO: Calculate real APY
		})
	}

	c.JSON(200, result)
}

func (s *APIServer) handleValidatorDetail(c *gin.Context) {
	address := c.Param("address")

	validator, err := s.worldState.GetValidator(address)
	if err != nil {
		c.JSON(404, gin.H{"error": "Validator not found"})
		return
	}

	c.JSON(200, gin.H{
		"address": validator.Address,
		"name":    "Validator",
		"stake":   validator.Stake,
		"uptime":  100.0, // TODO: Track real uptime
	})
}

func (s *APIServer) handleStakingInfo(c *gin.Context) {
	address := c.Param("address")

	// TODO: Get real staking info from worldState
	c.JSON(200, gin.H{
		"address":      address,
		"total_staked": "0",
		"stakes":       []interface{}{},
	})
}

func (s *APIServer) handleUserStakes(c *gin.Context) {
	address := c.Param("address")

	// TODO: Get user's stakes from worldState
	c.JSON(200, gin.H{
		"address": address,
		"stakes":  []interface{}{},
	})
}

func (s *APIServer) handleGetPoints(c *gin.Context) {
	address := c.Query("address")
	if address == "" {
		c.JSON(400, gin.H{"error": "Address required"})
		return
	}

	user := s.pointsManager.GetUserPoints(address)

	c.JSON(200, gin.H{
		"address":     user.Address,
		"points":      user.TotalPoints,
		"rank":        "Member",
		"streak":      user.CurrentStreak,
		"next_faucet": user.LastFaucet.Add(24 * time.Hour),
	})
}

func (s *APIServer) handleLeaderboard(c *gin.Context) {
	leaderboard := s.pointsManager.GetLeaderboard(50)
	c.JSON(200, leaderboard)
}

func (s *APIServer) handleFund(c *gin.Context) {
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

	// Award points (limit: once per 24h)
	newTotal, success := s.pointsManager.AwardFaucet(address)

	if !success {
		c.JSON(429, gin.H{
			"error":      "You can only use the faucet once every 24 hours.",
			"next_claim": s.pointsManager.GetUserPoints(address).LastFaucet.Add(24 * time.Hour),
		})
		return
	}

	// TODO: Send real tokens via worldState transaction
	txHash := fmt.Sprintf("0xFAUCET_%d", time.Now().UnixNano())

	log.Printf("💧 Faucet used by %s. Points: %d", address, newTotal)

	c.JSON(200, gin.H{
		"status":  "success",
		"message": "Sent 100 THR",
		"txHash":  txHash,
		"points":  newTotal,
	})
}
