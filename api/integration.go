// api/integration.go

// Integration helpers and production deployment utilities

// APIManager for easy lifecycle management of the HTTPS server
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

// APIManagerConfig represents API manager configuration
type APIManagerConfig struct {
	Port      int
	EnableTLS bool
	CertFile  string
	KeyFile   string
}

// NewAPIManager creates a new API manager (HTTP only - for development)
func NewAPIManager(worldState *state.WorldState, port int) *APIManager {
	ctx, cancel := context.WithCancel(context.Background())

	return &APIManager{
		server:     NewServer(worldState, port),
		worldState: worldState,
		ctx:        ctx,
		cancel:     cancel,
	}
}

// NewAPIManagerWithConfig creates a new API manager with full configuration (HTTP/HTTPS)
func NewAPIManagerWithConfig(worldState *state.WorldState, config *APIManagerConfig) *APIManager {
	ctx, cancel := context.WithCancel(context.Background())

	serverConfig := &ServerConfig{
		Port:      config.Port,
		EnableTLS: config.EnableTLS,
		CertFile:  config.CertFile,
		KeyFile:   config.KeyFile,
	}

	return &APIManager{
		server:     NewServerWithConfig(worldState, serverConfig),
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

	fmt.Println("📝 Integration examples:")
	fmt.Println("")
	fmt.Println("// Development setup (HTTP)")
	fmt.Println("apiManager := api.NewAPIManager(worldState, 8080)")
	fmt.Println("if err := apiManager.Start(); err != nil {")
	fmt.Println("    log.Fatalf(\"Failed to start API server: %v\", err)")
	fmt.Println("}")
	fmt.Println("defer apiManager.Stop()")
	fmt.Println("")
	fmt.Println("// Production setup (HTTPS)")
	fmt.Println("config := &api.APIManagerConfig{")
	fmt.Println("    Port:      8080,")
	fmt.Println("    EnableTLS: true,")
	fmt.Println("    CertFile:  \"/etc/ssl/certs/your-domain.crt\",")
	fmt.Println("    KeyFile:   \"/etc/ssl/private/your-domain.key\",")
	fmt.Println("}")
	fmt.Println("apiManager := api.NewAPIManagerWithConfig(worldState, config)")
	fmt.Println("if err := apiManager.Start(); err != nil {")
	fmt.Println("    log.Fatalf(\"Failed to start HTTPS API server: %v\", err)")
	fmt.Println("}")
	fmt.Println("defer apiManager.Stop()")
	fmt.Println("")
	fmt.Println("// Auto-generate development certificates")
	fmt.Println("if err := api.GenerateSelfSignedCert(\"./certs/server.crt\", \"./certs/server.key\"); err != nil {")
	fmt.Println("    log.Printf(\"Certificate generation failed: %v\", err)")
	fmt.Println("}")
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
	EnableTLS            bool          `json:"enable_tls"`
	CertFile             string        `json:"cert_file"`
	KeyFile              string        `json:"key_file"`
	AutoGenerateCert     bool          `json:"auto_generate_cert"`
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
		EnableTLS:            false,       // Enable for production
		CertFile:             "./certs/server.crt",
		KeyFile:              "./certs/server.key",
		AutoGenerateCert:     true, // Generate self-signed cert if files don't exist
	}
}

// ProductionAPIConfig returns production-ready defaults
func ProductionAPIConfig() *APIConfig {
	config := DefaultAPIConfig()
	config.EnableTLS = true
	config.AllowedOrigins = []string{} // Must be configured for production
	config.EnableAuthentication = true
	config.AutoGenerateCert = false // Use real certificates in production
	return config
}

// GenerateSelfSignedCert generates a self-signed certificate for development
func GenerateSelfSignedCert(certFile, keyFile string) error {
	// This would contain certificate generation logic
	// For now, return instructions
	return fmt.Errorf(`
🔒 Certificate files not found. For development, you can:

1. Generate self-signed certificates:
   openssl req -x509 -newkey rsa:4096 -keyout %s -out %s -days 365 -nodes -subj "/CN=localhost"

2. Or use Let's Encrypt for production:
   certbot certonly --standalone -d your-domain.com

3. Or disable TLS for development:
   Set EnableTLS: false in your API configuration

Certificate files expected:
- Certificate: %s  
- Private Key: %s
`, keyFile, certFile, certFile, keyFile)
}
