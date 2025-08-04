// api/integration.go

// Integration helpers and production deployment utilities

// APIManager for easy lifecycle management of the HTTP server
// Integration examples showing how to wire the API into your blockchain node
// Production deployment tips (rate limiting, caching, authentication, monitoring)
// Configuration helpers and testing utilities
// Demonstrates best practices for running APIs in production environments

package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/thrylos-labs/go-thrylos/core/state"
)

// APIManager manages the HTTP API server lifecycle
type APIManager struct {
	server     *Server
	worldState *state.WorldState
	ctx        context.Context
	cancel     context.CancelFunc
}

// NewAPIManager creates a new API manager
func NewAPIManager(worldState *state.WorldState, port int) *APIManager {
	ctx, cancel := context.WithCancel(context.Background())

	return &APIManager{
		server:     NewServer(worldState, port),
		worldState: worldState,
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Start starts the API server in a goroutine
func (am *APIManager) Start() error {
	go func() {
		if err := am.server.Start(); err != nil && err != http.ErrServerClosed {
			log.Printf("❌ API server error: %v", err)
		}
	}()

	// Wait a moment for server to start
	time.Sleep(100 * time.Millisecond)

	log.Printf("✅ API server started successfully")
	return nil
}

// Stop gracefully stops the API server
func (am *APIManager) Stop() error {
	am.cancel()
	return am.server.Stop()
}

// Example integration with your blockchain node
func IntegrateWithNode() {
	// This would be called from your main blockchain node initialization

	// Assuming you have a WorldState instance from your node setup
	// worldState := state.NewWorldState(...)

	// Start API server on port 8080
	// apiManager := NewAPIManager(worldState, 8080)
	// apiManager.Start()

	fmt.Println("📝 Integration example - add this to your node startup:")
	fmt.Println(`
	// In your main node initialization
	apiManager := api.NewAPIManager(worldState, 8080)
	if err := apiManager.Start(); err != nil {
		log.Fatalf("Failed to start API server: %v", err)
	}
	
	// Graceful shutdown
	defer apiManager.Stop()
	`)
}

// WalletExample demonstrates how a wallet would use the polling API
func WalletExample() {
	fmt.Println("🔗 Wallet Integration Example:")
	fmt.Println(`
	// Wallet connects to your blockchain
	client := api.NewClient("http://localhost:8080")
	
	// Create smart poller for multiple wallet addresses
	poller := api.NewSmartPoller(client)
	
	// Add user's addresses
	poller.AddAddress("tl1user123...", func(oldBalance, newBalance int64) {
		fmt.Printf("Main wallet: %d → %d\n", oldBalance, newBalance)
		// Update UI
	})
	
	poller.AddAddress("tl1savings456...", func(oldBalance, newBalance int64) {
		fmt.Printf("Savings: %d → %d\n", oldBalance, newBalance)
		// Update UI
	})
	
	// After user sends transaction
	poller.SetAggressiveMode(30 * time.Second)
	`)
}

// TestAPIEndpoints provides a simple test function
func TestAPIEndpoints(baseURL string) error {
	client := NewClient(baseURL)

	fmt.Printf("🧪 Testing API endpoints at %s\n", baseURL)

	// Test health endpoint
	resp, err := client.httpClient.Get(baseURL + "/api/v1/health")
	if err != nil {
		return fmt.Errorf("health check failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode == 200 {
		fmt.Println("✅ Health check passed")
	} else {
		fmt.Printf("❌ Health check failed: status %d\n", resp.StatusCode)
	}

	// Test status endpoint
	resp, err = client.httpClient.Get(baseURL + "/api/v1/status")
	if err != nil {
		return fmt.Errorf("status check failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode == 200 {
		fmt.Println("✅ Status endpoint passed")
	} else {
		fmt.Printf("❌ Status endpoint failed: status %d\n", resp.StatusCode)
	}

	return nil
}

// Production deployment helper
func ProductionDeploymentTips() {
	fmt.Println("🚀 Production Deployment Tips:")
	fmt.Println("")
	fmt.Println("1. **Rate Limiting**: Add rate limiting middleware to prevent abuse")
	fmt.Println("2. **Authentication**: Add API key authentication for sensitive endpoints")
	fmt.Println("3. **Caching**: Cache frequently requested data (balances, validator info)")
	fmt.Println("4. **Monitoring**: Add metrics collection and health monitoring")
	fmt.Println("5. **Load Balancing**: Run multiple API instances behind a load balancer")
	fmt.Println("6. **HTTPS**: Always use HTTPS in production")
	fmt.Println("7. **CORS**: Configure CORS properly for web wallet integration")
	fmt.Println("")
	fmt.Println("Example rate limiting middleware:")
	fmt.Println("func (s *Server) rateLimitMiddleware(next http.Handler) http.Handler {")
	fmt.Println("    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {")
	fmt.Println("        // Implement rate limiting logic")
	fmt.Println("        next.ServeHTTP(w, r)")
	fmt.Println("    })")
	fmt.Println("}")
	fmt.Println("")
	fmt.Println("Example caching:")
	fmt.Println("type CachedServer struct {")
	fmt.Println("    *Server")
	fmt.Println("    cache map[string]interface{}")
	fmt.Println("    cacheMu sync.RWMutex")
	fmt.Println("    cacheTTL time.Duration")
	fmt.Println("}")
	fmt.Println("")
}

// Configuration for production
type APIConfig struct {
	Port                 int           `json:"port"`
	EnableCORS           bool          `json:"enable_cors"`
	AllowedOrigins       []string      `json:"allowed_origins"`
	RateLimitPerMinute   int           `json:"rate_limit_per_minute"`
	EnableAuthentication bool          `json:"enable_authentication"`
	CacheTTL             time.Duration `json:"cache_ttl"`
	MaxRequestSize       int64         `json:"max_request_size"`
}

// DefaultAPIConfig returns sensible defaults
func DefaultAPIConfig() *APIConfig {
	return &APIConfig{
		Port:                 8080,
		EnableCORS:           true,
		AllowedOrigins:       []string{"*"}, // Restrict in production
		RateLimitPerMinute:   60,
		EnableAuthentication: false, // Enable in production
		CacheTTL:             30 * time.Second,
		MaxRequestSize:       1024 * 1024, // 1MB
	}
}
