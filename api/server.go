// api/server.go

// Main HTTPS REST API server for blockchain data access

// Provides clean REST endpoints for wallets and applications to query blockchain state
// Handles account balances, transactions, blocks, validators, and system status
// Uses Gorilla Mux for routing, includes CORS support and logging middleware
// Designed for HTTPS polling approach - simple, reliable, cacheable endpoints
// Serves as the primary interface between external applications and your blockchain node

// Updated for account-based system with staking support

package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gorilla/mux"
	"github.com/rs/cors"
	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/chain"
	"github.com/thrylos-labs/go-thrylos/core/evm"
	"github.com/thrylos-labs/go-thrylos/core/math"
	"github.com/thrylos-labs/go-thrylos/core/state"
	"github.com/thrylos-labs/go-thrylos/proto/core"
)

// Server represents the HTTP API server
type Server struct {
	worldState    *state.WorldState
	blockchain    *chain.Blockchain // <--- ADD THIS
	config        *config.Config    // <--- ADD THIS
	evmExecutor   *evm.RevmExecutor
	pointsManager *PointsManager
	router        *mux.Router
	server        *http.Server
	port          int

	// HTTPS configuration
	enableTLS bool
	certFile  string
	keyFile   string

	// Faucet control
	enableFaucet bool

	// Rate limiting
	rateLimiter     *RateLimiter
	rateLimitConfig *RateLimitConfig
	endpointLimiter *EndpointLimiter
	ethHandler      *EthereumRPCHandler
	peerIDFunc      func() string // FIND-04: returns this node's libp2p peer ID

}

// ServerConfig represents server configuration
type ServerConfig struct {
	Port         int
	EnableTLS    bool
	CertFile     string
	KeyFile      string
	EnableFaucet bool
}

// Response structures for account-based system
type AccountResponse struct {
	Address      string            `json:"address"`
	Balance      int64             `json:"balance"`
	Nonce        uint64            `json:"nonce"`
	StakedAmount int64             `json:"staked_amount"`
	Rewards      int64             `json:"rewards"`
	DelegatedTo  map[string]string `json:"delegated_to"`
}

type DelegationsResponse struct {
	Address     string           `json:"address"`
	Delegations map[string]int64 `json:"delegations"`
	Count       int              `json:"count"`
}

type TransactionResponse struct {
	Hash      string `json:"hash"`
	From      string `json:"from"`
	To        string `json:"to"`
	Amount    string `json:"amount"`
	Nonce     uint64 `json:"nonce"`
	Gas       int64  `json:"gas"`
	GasPrice  string `json:"gas_price"`
	Timestamp int64  `json:"timestamp"`
	Status    string `json:"status"`
	Signature string `json:"signature,omitempty"`
}

type TransactionHistoryResponse struct {
	Address      string                `json:"address"`
	Transactions []TransactionResponse `json:"transactions"`
	Count        int                   `json:"count"`
	Limit        int                   `json:"limit"`
}

// NewServer creates a new API server
func NewServer(worldState *state.WorldState, port int) *Server {
	server := &Server{
		worldState:      worldState,
		port:            port,
		rateLimitConfig: DefaultRateLimitConfig(),
		enableFaucet:    isDevEnvironment(), // HTTP-only dev server
	}

	server.rateLimiter = NewRateLimiter(server.rateLimitConfig)
	server.setupRoutes()
	return server
}

func NewServerWithServerConfig(worldState *state.WorldState, serverConfig *ServerConfig) *Server {
	server := &Server{
		worldState:      worldState,
		port:            serverConfig.Port,
		enableTLS:       serverConfig.EnableTLS,
		certFile:        serverConfig.CertFile,
		keyFile:         serverConfig.KeyFile,
		enableFaucet:    serverConfig.EnableFaucet,
		rateLimitConfig: DefaultRateLimitConfig(),
	}

	server.rateLimiter = NewRateLimiter(server.rateLimitConfig)
	server.setupRoutes()
	return server
}

func NewServerWithConfig(
	worldState *state.WorldState,
	blockchain *chain.Blockchain,
	evmExecutor *evm.RevmExecutor,
	cfg *config.Config, // 4th
	peerIDFunc func() string, // 5th
) *Server {

	// Create rate limit config from main config
	rateLimitConfig := &RateLimitConfig{
		StrictRPS:       float64(cfg.API.RateLimit) / 10,
		StandardRPS:     float64(cfg.API.RateLimit),
		PermissiveRPS:   float64(cfg.API.RateLimit) * 10,
		StrictBurst:     3,
		StandardBurst:   20,
		PermissiveBurst: 200,
		CleanupInterval: 1 * time.Minute,
		MaxIdleTime:     5 * time.Minute,
		Enabled:         true,
	}

	// ✅ FIX: Only initialize PointsManager if Faucet is enabled (Dev Mode)
	var pm *PointsManager
	if cfg.API.EnableFaucet {
		path := os.Getenv("POINTS_FILE_PATH")
		if path == "" {
			path = "points.json"
		}
		pm = NewPointsManager(path)
	} else {
		pm = nil
	}

	server := &Server{
		worldState:      worldState,
		blockchain:      blockchain,  // <--- Assign
		evmExecutor:     evmExecutor, // <--- Assign
		config:          cfg,         // <--- Assign
		pointsManager:   pm,
		port:            extractPortFromConfig(cfg.API.RESTAddr),
		enableTLS:       cfg.API.EnableTLS,
		certFile:        cfg.API.CertFile,
		keyFile:         cfg.API.KeyFile,
		rateLimitConfig: rateLimitConfig,
		enableFaucet:    cfg.API.EnableFaucet,
		peerIDFunc:      peerIDFunc, // this line must be present
	}

	server.rateLimiter = NewRateLimiter(server.rateLimitConfig)
	server.endpointLimiter = newEndpointLimiter()
	server.setupRoutes()
	return server
}

// Add "io" and "bytes" to imports if missing!

func (s *Server) handleJSONRPC(w http.ResponseWriter, r *http.Request) {
	// 1. Read the body
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeError(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	// Restore body so the underlying handlers can read it again
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	// 2. Decode just the Method
	var req struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		s.writeError(w, "Invalid JSON-RPC", http.StatusBadRequest)
		return
	}

	// 3. Dispatch to the correct handler based on the method string
	switch req.Method {
	case "eth_chainId":
		s.ethHandler.ChainId(w, r)
	case "net_version":
		s.ethHandler.NetworkId(w, r)
	case "web3_clientVersion":
		s.ethHandler.ClientVersion(w, r)
	case "eth_blockNumber":
		s.ethHandler.BlockNumber(w, r)
	case "eth_getBalance":
		s.ethHandler.GetBalance(w, r)
	case "eth_getTransactionCount":
		s.ethHandler.GetTransactionCount(w, r)
	case "eth_getCode":
		s.ethHandler.GetCode(w, r)
	case "eth_sendRawTransaction":
		s.ethHandler.SendRawTransaction(w, r)
	case "eth_call":
		s.ethHandler.Call(w, r)
	case "eth_estimateGas":
		s.ethHandler.EstimateGas(w, r)
	case "eth_gasPrice":
		s.ethHandler.GasPrice(w, r)
	case "eth_maxPriorityFeePerGas":
		s.ethHandler.MaxPriorityFeePerGas(w, r)
	case "eth_feeHistory":
		s.ethHandler.FeeHistory(w, r)
	case "eth_getBlockByNumber":
		s.ethHandler.GetBlockByNumber(w, r)
	case "eth_getBlockByHash":
		s.ethHandler.GetBlockByHash(w, r)
	case "eth_getTransactionByHash":
		s.ethHandler.GetTransactionByHash(w, r)
	case "eth_getTransactionReceipt":
		s.ethHandler.GetTransactionReceipt(w, r)
	case "eth_coinbase":
		s.ethHandler.Coinbase(w, r)
	case "eth_mining":
		s.ethHandler.Mining(w, r)
	case "eth_syncing":
		s.ethHandler.Syncing(w, r)
	default:
		s.writeError(w, fmt.Sprintf("Method %s not supported", req.Method), http.StatusNotFound)
	}
}

// Helper to extract port from config address
func extractPortFromConfig(addr string) int {
	switch addr {
	case ":8080":
		return 8080
	case ":8443":
		return 8443
	default:
		return 8080
	}
}

func parseChainID(chainIDStr string) int64 {
	log.Printf("🔍 DEBUG parseChainID: input='%s'", chainIDStr)

	// Try parsing the entire string first
	id, err := strconv.ParseInt(chainIDStr, 10, 64)
	if err == nil {
		log.Printf("✅ Parsed as integer: %d", id)
		return id
	}

	// Extract numbers from strings like "thrylos-local-1337"
	re := regexp.MustCompile(`\d+`)
	matches := re.FindAllString(chainIDStr, -1)
	if len(matches) > 0 {
		// Use the last number found (1337 in "thrylos-local-1337")
		lastNum := matches[len(matches)-1]
		if parsedID, err := strconv.ParseInt(lastNum, 10, 64); err == nil {
			log.Printf("✅ Extracted chain ID %d from string '%s'", parsedID, chainIDStr)
			return parsedID
		}
	}

	log.Printf("⚠️ Could not parse chain ID from '%s', using default: 1", chainIDStr)
	return 1 // Default to 1 (Mainnet) if parsing fails
}

// NewServerWithConfig creates a new API server with full configuration
// func NewServerWithConfig(worldState *state.WorldState, config *ServerConfig) *Server {
// 	server := &Server{
// 		worldState: worldState,
// 		port:       config.Port,
// 		enableTLS:  config.EnableTLS,
// 		certFile:   config.CertFile,
// 		keyFile:    config.KeyFile,
// 	}

// 	server.setupRoutes()
// 	return server
// }

func (s *Server) setupRoutes() {
	s.router = mux.NewRouter()

	// ✅ ADD REQUEST SIZE LIMIT GLOBALLY:
	maxRequestSize := int64(1024 * 1024) // 1MB default
	if s.config != nil && s.config.API.MaxRequestSize > 0 {
		maxRequestSize = s.config.API.MaxRequestSize
	}
	s.router.Use(s.RequestSizeLimitMiddleware(maxRequestSize))

	// ---------------------------------------------------------
	// 1. Initialize EVM RPC Handler
	// ---------------------------------------------------------
	// Try to parse ChainID from config, default to 1 (Mainnet) if fails
	// Try to parse ChainID from config, default to 1 (Mainnet) if fails
	chainID := parseChainID(s.config.Network.ChainID)

	// ✅ FIX: Assign to struct field so the Dispatcher can use it
	s.ethHandler = NewEthereumRPCHandler(s.blockchain, s.evmExecutor, chainID)

	// Local variable for existing routes below
	ethAPI := s.ethHandler

	// 2. Define Subrouters
	api := s.router.PathPrefix("/api/v1").Subrouter()

	// ========== STRICT RATE LIMITING (1 req/sec) ==========
	// Use for: State changes, Faucet, Broadcasting
	strict := api.PathPrefix("").Subrouter()
	strict.Use(s.RateLimitMiddleware("strict"))

	// We also need a strict router at the root level for EVM routes
	strictRoot := s.router.PathPrefix("").Subrouter()
	strictRoot.Use(s.RateLimitMiddleware("strict"))

	// ========== STANDARD RATE LIMITING (10 req/sec) ==========
	// Use for: Heavy computations (EVM calls, Gas estimation)
	standard := api.PathPrefix("").Subrouter()
	standard.Use(s.RateLimitMiddleware("standard"))

	standardRoot := s.router.PathPrefix("").Subrouter()
	standardRoot.Use(s.RateLimitMiddleware("standard"))

	// ========== PERMISSIVE RATE LIMITING (100 req/sec) ==========
	// Use for: Simple reads, Lookups, Health checks
	permissive := api.PathPrefix("").Subrouter()
	permissive.Use(s.RateLimitMiddleware("permissive"))

	// ---------------------------------------------------------
	// ✅ ADD POINTS ROUTES HERE (In Permissive section)
	// ---------------------------------------------------------
	permissive.HandleFunc("/points", s.getPoints).Methods("GET", "OPTIONS")
	permissive.HandleFunc("/leaderboard", s.getLeaderboard).Methods("GET", "OPTIONS")

	permissiveRoot := s.router.PathPrefix("").Subrouter()
	permissiveRoot.Use(s.RateLimitMiddleware("permissive"))

	// ---------------------------------------------------------
	// 3. Register Routes
	// ---------------------------------------------------------

	// === STRICT ROUTES (Writes & Critical) ===

	// Thrylos Native: Faucet (Dev only)
	// Thrylos Native: Faucet (Dev only)
	log.Printf("🔍 DEBUG: enableFaucet=%v, isDevEnvironment()=%v", s.enableFaucet, s.isDevEnvironment())
	if s.enableFaucet {
		// Always register when enabled; environment gating happens inside handler.
		// This avoids confusing 404s and returns explicit 403 when faucet is disabled by env.
		log.Println("✅ Registering faucet endpoint at /fund")
		permissive.HandleFunc("/fund", s.fundAddress).Methods("GET", "POST", "OPTIONS")
	} else {
		log.Println("❌ Faucet endpoint NOT registered (EnableFaucet=false)")
	}
	// Thrylos Native: Broadcast
	strict.HandleFunc("/transaction/broadcast", s.submitSignedTransaction).Methods("POST", "OPTIONS")

	// Staking endpoints (strict rate limiting)
	strict.HandleFunc("/stake", s.submitStakeTransaction).Methods("POST", "OPTIONS")
	strict.HandleFunc("/unstake", s.submitUnstakeTransaction).Methods("POST", "OPTIONS")

	// EVM: Send Raw Transaction (Write)
	strictRoot.HandleFunc("/eth_sendRawTransaction", ethAPI.SendRawTransaction).Methods("POST", "OPTIONS")

	// === STANDARD ROUTES (Computation Heavy) ===

	// Thrylos Native
	standard.HandleFunc("/estimate-gas", s.estimateGas).Methods("POST", "OPTIONS")

	// EVM: Execution & Simulation
	standardRoot.HandleFunc("/eth_call", ethAPI.Call).Methods("POST", "OPTIONS")
	standardRoot.HandleFunc("/eth_estimateGas", ethAPI.EstimateGas).Methods("POST", "OPTIONS")

	// === PERMISSIVE ROUTES (Reads & Info) ===

	// --- Thrylos Native Read Endpoints ---
	permissive.HandleFunc("/account/{address}/balance", s.getAccountBalance).Methods("GET", "OPTIONS")
	permissive.HandleFunc("/account/{address}", s.getAccount).Methods("GET", "OPTIONS")
	permissive.HandleFunc("/account/{address}/transactions", s.getAccountTransactions).Methods("GET", "OPTIONS")
	permissive.HandleFunc("/account/{address}/delegations", s.getAccountDelegations).Methods("GET", "OPTIONS")

	// Staking endpoints
	permissive.HandleFunc("/account/{address}/stake", s.getAccountStake).Methods("GET", "OPTIONS")
	permissive.HandleFunc("/account/{address}/rewards", s.getAccountRewards).Methods("GET", "OPTIONS")
	permissive.HandleFunc("/staking/stats", s.getStakingStats).Methods("GET", "OPTIONS")
	permissive.HandleFunc("/account/{address}/unbonding", s.getAccountUnbonding).Methods("GET", "OPTIONS")
	permissive.HandleFunc("/staking/validators", s.getStakingValidators).Methods("GET", "OPTIONS")
	permissive.HandleFunc("/staking/delegations/{address}", s.getDelegationHistory).Methods("GET", "OPTIONS")
	permissive.HandleFunc("/staking/rewards/{address}", s.getDetailedRewards).Methods("GET", "OPTIONS")

	// General Data endpoints
	permissive.HandleFunc("/blocks", s.getBlocks).Methods("GET", "OPTIONS")
	permissive.HandleFunc("/transactions", s.getRecentTransactions).Methods("GET", "OPTIONS")
	permissive.HandleFunc("/transaction/{hash}", s.getTransaction).Methods("GET", "OPTIONS")
	permissive.HandleFunc("/transactions/pending", s.getPendingTransactions).Methods("GET", "OPTIONS")
	permissive.HandleFunc("/block/{hash}", s.getBlockByHash).Methods("GET", "OPTIONS")
	permissive.HandleFunc("/block/height/{height}", s.getBlockByHeight).Methods("GET", "OPTIONS")
	permissive.HandleFunc("/block/latest", s.getLatestBlock).Methods("GET", "OPTIONS")
	permissive.HandleFunc("/validator/{address}", s.getValidator).Methods("GET", "OPTIONS")
	permissive.HandleFunc("/validators", s.getValidators).Methods("GET", "OPTIONS")
	permissive.HandleFunc("/validators/active", s.getActiveValidators).Methods("GET", "OPTIONS")
	permissive.HandleFunc("/status", s.getStatus).Methods("GET", "OPTIONS")
	permissive.HandleFunc("/health", s.getHealth).Methods("GET", "OPTIONS")
	permissive.HandleFunc("/peer-id", s.getPeerID).Methods("GET", "OPTIONS")
	permissive.HandleFunc("/validator/{address}/activity", s.getValidatorActivity).Methods("GET", "OPTIONS")

	permissive.HandleFunc("/stats", s.getNetworkStats).Methods("GET", "OPTIONS")

	// --- EVM Read Endpoints ---
	// Network info
	permissiveRoot.HandleFunc("/eth_chainId", ethAPI.ChainId).Methods("POST", "OPTIONS")
	permissiveRoot.HandleFunc("/eth_networkId", ethAPI.NetworkId).Methods("POST", "OPTIONS")

	// Account info
	permissiveRoot.HandleFunc("/eth_getBalance", ethAPI.GetBalance).Methods("POST", "OPTIONS")
	permissiveRoot.HandleFunc("/eth_getTransactionCount", ethAPI.GetTransactionCount).Methods("POST", "OPTIONS")
	permissiveRoot.HandleFunc("/eth_getCode", ethAPI.GetCode).Methods("POST", "OPTIONS")

	// Gas & blocks
	permissiveRoot.HandleFunc("/eth_gasPrice", ethAPI.GasPrice).Methods("POST", "OPTIONS")
	permissiveRoot.HandleFunc("/eth_feeHistory", ethAPI.FeeHistory).Methods("POST", "OPTIONS")
	permissiveRoot.HandleFunc("/eth_blockNumber", ethAPI.BlockNumber).Methods("POST", "OPTIONS")
	permissiveRoot.HandleFunc("/eth_getBlockByNumber", ethAPI.GetBlockByNumber).Methods("POST", "OPTIONS")
	permissiveRoot.HandleFunc("/eth_getBlockByHash", ethAPI.GetBlockByHash).Methods("POST", "OPTIONS")

	// Transaction info
	permissiveRoot.HandleFunc("/eth_getTransactionByHash", ethAPI.GetTransactionByHash).Methods("POST", "OPTIONS")
	permissiveRoot.HandleFunc("/eth_getTransactionReceipt", ethAPI.GetTransactionReceipt).Methods("POST", "OPTIONS")

	// Storage
	permissiveRoot.HandleFunc("/eth_getStorageAt", ethAPI.GetStorageAt).Methods("POST", "OPTIONS")

	// This catches standard JSON-RPC requests sent to "/"
	s.router.HandleFunc("/", s.handleJSONRPC).Methods("POST", "OPTIONS")

	// ---------------------------------------------------------
	// 4. CORS Configuration
	// ---------------------------------------------------------
	allowedOrigins := []string{
		"https://thrylos.org",
		"https://www.thrylos.org",
		"https://app.thrylos.org",
	}

	// Add localhost origins only in dev/testnet environments
	if s.isDevEnvironment() {
		allowedOrigins = append(allowedOrigins,
			"http://localhost:3000",
			"http://localhost:5173",
			"http://localhost:8080",
			"http://127.0.0.1:5173",
			"http://127.0.0.1:3000",
		)
	}

	c := cors.New(cors.Options{
		AllowedOrigins: allowedOrigins,
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders: []string{
			"Content-Type",
			"Authorization",
			"Accept",
			"Origin",
			"X-Requested-With",
		},
		ExposedHeaders: []string{
			"Content-Length",
			"X-RateLimit-Limit",
			"X-RateLimit-Remaining",
			"Retry-After",
		},
		AllowCredentials: true,
		MaxAge:           300,
		Debug:            false,
	})

	// Apply Middleware
	s.router.Use(c.Handler)
	s.router.Use(s.loggingMiddleware)
	s.router.Use(s.jsonMiddleware)
}

// Start starts the HTTP server
func (s *Server) Start() error {
	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	if s.enableTLS {
		log.Printf("🔒 HTTPS API Server starting on port %d", s.port)
		log.Printf("📊 Health check: https://localhost:%d/api/v1/health", s.port)
		log.Printf("💰 Account endpoint: https://localhost:%d/api/v1/account/{address}", s.port)
		return s.server.ListenAndServeTLS(s.certFile, s.keyFile)
	} else {
		log.Printf("🌐 HTTP API Server starting on port %d", s.port)
		log.Printf("📊 Health check: http://localhost:%d/api/v1/health", s.port)
		log.Printf("💰 Account endpoint: http://localhost:%d/api/v1/account/{address}", s.port)
		log.Printf("⚠️  Warning: Using HTTP in development mode. Use HTTPS for production!")
		return s.server.ListenAndServe()
	}
}

// Stop stops the HTTP server
func (s *Server) Stop() error {
	if s.server != nil {
		return s.server.Close()
	}
	return nil
}

func (s *Server) submitSignedTransaction(w http.ResponseWriter, r *http.Request) {
	var tx core.Transaction
	if err := json.NewDecoder(r.Body).Decode(&tx); err != nil {
		s.writeError(w, "Invalid transaction format", http.StatusBadRequest)
		return
	}

	// Expect transaction to be fully formed and signed
	if tx.Id == "" {
		s.writeError(w, "Transaction ID required", http.StatusBadRequest)
		return
	}

	if tx.Hash == "" {
		s.writeError(w, "Transaction hash required", http.StatusBadRequest)
		return
	}

	if len(tx.Signature) == 0 {
		s.writeError(w, "Transaction signature required", http.StatusBadRequest)
		return
	}

	// Validate signature using your crypto system
	// This is where your existing validation logic goes
	if err := s.worldState.AddTransaction(&tx); err != nil {
		s.writeError(w, fmt.Sprintf("Invalid transaction: %v", err), http.StatusBadRequest)
		return
	}

	// ✅ CORRECTED: Only one block, checks for nil, runs in background
	if s.pointsManager != nil {
		go s.pointsManager.RecordTransaction(tx.From, tx.To)
	}

	s.writeJSON(w, map[string]interface{}{"status": "accepted", "tx_hash": tx.Hash})
}

func (s *Server) estimateGas(w http.ResponseWriter, r *http.Request) {
	var req struct {
		From   string `json:"from"`
		To     string `json:"to"`
		Amount int64  `json:"amount"`
		Data   string `json:"data,omitempty"` // For smart contracts later
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	// Basic gas estimation based on transaction type
	var gasEstimate int64 = 21000 // Standard transaction gas from your config

	// If there's data (smart contract call), increase gas
	if req.Data != "" {
		gasEstimate += int64(len(req.Data)) * 68 // Gas per byte
	}

	// Get current gas price from your config (1000 from config.go)
	gasPrice := int64(1000)

	// Calculate total fee
	totalFee := gasEstimate * gasPrice

	response := map[string]interface{}{
		"gas_estimate": gasEstimate,
		"gas_price":    gasPrice,
		"total_fee":    totalFee,
		"fee_thrylos":  float64(totalFee) / 1000000000, // Convert to THRYLOS
	}

	s.writeJSON(w, response)
}

// submitStakeTransaction handles staking (delegation) requests
func (s *Server) submitStakeTransaction(w http.ResponseWriter, r *http.Request) {
	// ✅ ADD THIS: Import these at the top if not already imported
	// import "io"
	// import "bytes"

	// Read and log raw body
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeError(w, "Failed to read body", http.StatusBadRequest)
		return
	}

	// Restore body
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	var req struct {
		From      string `json:"from"`
		To        string `json:"to"`
		Amount    string `json:"amount"`
		Type      string `json:"type"`
		Signature string `json:"signature"`
		Timestamp int64  `json:"timestamp"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("❌ JSON decode error: %v", err)
		s.writeError(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	// ✅ ADD THIS: Log decoded values
	log.Printf("🔍 DECODED: From=%s, To=%s, Amount='%s', Type=%s",
		req.From, req.To, req.Amount, req.Type)

	// Rest of your existing validation...
	if req.From == "" || req.To == "" || req.Amount == "" {
		s.writeError(w, "Missing required fields: from, to, amount", http.StatusBadRequest)
		return
	}

	if req.Signature == "" {
		s.writeError(w, "Transaction signature required", http.StatusBadRequest)
		return
	}

	// ✅ C-01 FIX: Cryptographically verify the signature proves ownership of req.From
	if err := verifyStakingSignature(req.From, req.To, req.Amount, req.Timestamp, req.Signature); err != nil {
		log.Printf("❌ Signature verification failed for stake from %s: %v", req.From, err)
		s.writeError(w, "Invalid signature", http.StatusUnauthorized)
		return
	}

	amountBig, ok := new(big.Int).SetString(req.Amount, 10)
	if !ok || amountBig.Sign() <= 0 {
		log.Printf("❌ Parse failed: ok=%v, sign=%d", ok, amountBig.Sign())
		s.writeError(w, "Invalid amount: must be a positive number", http.StatusBadRequest)
		return
	}

	// ✅ ADD: Log after parsing
	log.Printf("✅ AFTER PARSING BigInt: %s", amountBig.String())
	log.Printf("   BigInt sign: %d", amountBig.Sign())
	log.Printf("   BigInt bits: %d", amountBig.BitLen())

	// Balance sufficiency is checked atomically inside stakingManager.Delegate()
	// under its mutex — do not pre-check here to avoid a TOCTOU race.

	// Verify validator exists
	validator, err := s.worldState.GetValidator(req.To)
	if err != nil {
		s.writeError(w, fmt.Sprintf("Validator not found: %s", req.To), http.StatusNotFound)
		return
	}

	// Check if validator is active
	if !validator.Active {
		s.writeError(w, "Validator is not active", http.StatusBadRequest)
		return
	}

	log.Printf("🔍 CALLING stakingManager.Delegate with amount: %s", amountBig.String())

	stakingManager := s.worldState.GetStakingManager()
	if err := stakingManager.Delegate(req.From, req.To, amountBig); err != nil {
		log.Printf("❌ DELEGATE FAILED: %v", err)
		s.writeError(w, fmt.Sprintf("Staking failed: %v", err), http.StatusBadRequest)
		return
	}

	log.Printf("✅ DELEGATE SUCCEEDED")

	// Generate transaction hash for tracking
	txHash := fmt.Sprintf("0x%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s:%d", req.From, req.To, req.Amount, req.Timestamp))))

	response := map[string]interface{}{
		"status":    "success",
		"tx_hash":   txHash,
		"message":   "Successfully delegated to validator",
		"from":      req.From,
		"validator": req.To,
		"amount":    req.Amount,
		"timestamp": req.Timestamp,
	}

	s.writeJSON(w, response)
}

// submitUnstakeTransaction handles unstaking (undelegation) requests
// submitUnstakeTransaction handles unstaking (undelegation) requests
func (s *Server) submitUnstakeTransaction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		From      string `json:"from"`
		To        string `json:"to"`
		Amount    string `json:"amount"`
		Type      string `json:"type"`
		Signature string `json:"signature"`
		Timestamp int64  `json:"timestamp"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.From == "" || req.To == "" || req.Amount == "" {
		s.writeError(w, "Missing required fields: from, to, amount", http.StatusBadRequest)
		return
	}

	// Validate signature
	if req.Signature == "" {
		s.writeError(w, "Transaction signature required", http.StatusBadRequest)
		return
	}

	// ✅ C-01 FIX: Cryptographically verify the signature proves ownership of req.From
	if err := verifyStakingSignature(req.From, req.To, req.Amount, req.Timestamp, req.Signature); err != nil {
		log.Printf("❌ Signature verification failed for unstake from %s: %v", req.From, err)
		s.writeError(w, "Invalid signature", http.StatusUnauthorized)
		return
	}

	// Parse and validate amount
	amountBig, ok := new(big.Int).SetString(req.Amount, 10)
	if !ok || amountBig.Sign() <= 0 {
		s.writeError(w, "Invalid amount: must be a positive number", http.StatusBadRequest)
		return
	}

	// Check if user has enough delegated amount
	account, err := s.worldState.GetAccount(req.From)
	if err != nil {
		s.writeError(w, fmt.Sprintf("Account not found: %s", req.From), http.StatusNotFound)
		return
	}

	// Get current delegation to this validator
	delegatedAmountStr := "0"
	if account.DelegatedTo != nil {
		if amount, exists := account.DelegatedTo[req.To]; exists {
			delegatedAmountStr = amount
		}
	}

	delegatedBig, ok := new(big.Int).SetString(delegatedAmountStr, 10)
	if !ok {
		delegatedBig = big.NewInt(0)
	}

	// Check if user has enough delegation
	if amountBig.Cmp(delegatedBig) > 0 {
		s.writeError(w, fmt.Sprintf("Insufficient delegation: have %s wei delegated, requested %s wei", delegatedAmountStr, req.Amount), http.StatusBadRequest)
		return
	}

	// Execute undelegation using StakingManager
	stakingManager := s.worldState.GetStakingManager()
	if err := stakingManager.Undelegate(req.From, req.To, amountBig); err != nil {
		s.writeError(w, fmt.Sprintf("Unstaking failed: %v", err), http.StatusBadRequest)
		return
	}

	// ✅ NEW: Award Points for Unstaking (Background Goroutine)
	// Runs only if points system is active (Dev/Testnet)
	if s.pointsManager != nil {
		go s.pointsManager.RecordUndelegation(req.From)
	}

	// Generate transaction hash for tracking
	txHash := fmt.Sprintf("0x%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s:%d", req.From, req.To, req.Amount, req.Timestamp))))

	response := map[string]interface{}{
		"status":    "success",
		"tx_hash":   txHash,
		"message":   "Successfully undelegated from validator",
		"from":      req.From,
		"validator": req.To,
		"amount":    req.Amount,
		"timestamp": req.Timestamp,
	}

	s.writeJSON(w, response)
}

// UPDATED Account endpoints for account-based system
func (s *Server) getAccount(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	address := vars["address"]

	account, err := s.worldState.GetAccount(address)
	if err != nil {
		s.writeError(w, "Account not found", http.StatusNotFound)
		return
	}

	// Get additional staking information
	stakedAmount := account.StakedAmount
	rewards := account.Rewards
	delegations := account.DelegatedTo

	// FIX: Update type to map[string]string
	if delegations == nil {
		delegations = make(map[string]string)
	}

	response := map[string]interface{}{
		"address":       account.Address,
		"balance":       account.Balance,
		"nonce":         account.Nonce,
		"staked_amount": stakedAmount,
		"rewards":       rewards,
		"delegated_to":  delegations,
	}

	s.writeJSON(w, response)
}
func (s *Server) getNetworkStats(w http.ResponseWriter, r *http.Request) {
	// Get current block height from blockchain
	latestBlock := s.worldState.GetCurrentBlock()
	height := int64(0)
	if latestBlock != nil {
		height = latestBlock.Header.Index // ✅ Use Header.Index, not Index
	}

	// Get active validators
	validators := s.worldState.GetActiveValidators()
	activeValidators := len(validators)

	// Get network status
	networkStatus := "healthy"
	if height == 0 {
		networkStatus = "initializing"
	}

	// Calculate a live APY estimate from active validator commission rates.
	// Formula: base annual rate (10%) minus the average commission across validators.
	// Replace with rewards.Distributor.GetCurrentAPY() once wired to the Server struct.
	const baseAnnualRate = 10.0
	apy := baseAnnualRate
	if len(validators) > 0 {
		var totalCommission float64
		for _, v := range validators {
			totalCommission += v.Commission
		}
		avgCommission := totalCommission / float64(len(validators))
		apy = baseAnnualRate * (1 - avgCommission)
	}
	apyStr := fmt.Sprintf("%.2f", apy)

	response := map[string]interface{}{
		"current_height":    height,
		"active_validators": activeValidators,
		"network_status":    networkStatus,
		"apy":               apyStr,
	}

	s.writeJSON(w, response)
}

func (s *Server) getAccountBalance(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	address := vars["address"]

	balance, err := s.worldState.GetBalance(address)
	if err != nil {
		s.writeError(w, "Account not found", http.StatusNotFound)
		return
	}

	nonce, _ := s.worldState.GetNonce(address)

	// FIX: Use big.Float for safe conversion
	// 1. Create a big.Float from the balance
	fBalance := new(big.Float).SetInt(balance)

	// 2. Define the Base Unit Divisor (10^18)
	// We use SetString to ensure precision for large numbers
	fDivisor := new(big.Float).SetInt(config.BaseUnit)

	// 3. Perform Division
	fResult := new(big.Float).Quo(fBalance, fDivisor)

	// 4. Extract float64 for the JSON response
	balanceThrylos, _ := fResult.Float64()

	response := map[string]interface{}{
		"address":        address,
		"balance":        balance.String(), // Send string to preserve full precision
		"balanceThrylos": balanceThrylos,   // Approximate human-readable amount
		"nonce":          nonce,
	}

	s.writeJSON(w, response)
}

func (s *Server) getAccountStake(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	address := vars["address"]

	account, err := s.worldState.GetAccount(address)
	if err != nil {
		s.writeError(w, "Account not found", http.StatusNotFound)
		return
	}

	response := map[string]interface{}{
		"address":       address,
		"staked_amount": account.StakedAmount,
	}

	s.writeJSON(w, response)
}

func (s *Server) getAccountRewards(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	address := vars["address"]

	account, err := s.worldState.GetAccount(address)
	if err != nil {
		s.writeError(w, "Account not found", http.StatusNotFound)
		return
	}

	response := map[string]interface{}{
		"address": address,
		"rewards": account.Rewards,
	}

	s.writeJSON(w, response)
}

// RequestSizeLimitMiddleware limits the size of request bodies
func (s *Server) RequestSizeLimitMiddleware(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Limit request body size
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) getAccountDelegations(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	address := vars["address"]

	// Use your existing StakingManager
	stakingManager := s.worldState.GetStakingManager()
	delegations, err := stakingManager.GetDelegations(address)
	if err != nil {
		s.writeError(w, "Failed to get delegations", http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"address":     address,
		"delegations": delegations,
		"count":       len(delegations),
	}

	s.writeJSON(w, response)
}

func (s *Server) getAccountTransactions(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	address := vars["address"]

	// Add debug logging
	log.Printf("🔍 Getting transactions for address: %s", address)

	// Parse query parameters
	limitStr := r.URL.Query().Get("limit")
	limit := 50 // default limit
	if limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 && parsedLimit <= 1000 {
			limit = parsedLimit
		}
	}

	var accountTxs []map[string]interface{}

	// Get confirmed transactions using efficient indexing
	// ACCESS DB THROUGH WORLDSTATE
	log.Printf("🔍 Fetching confirmed transactions from database index...")
	confirmedTxs, err := s.worldState.GetTransactionsByAddress(address, limit)
	if err != nil {
		log.Printf("❌ Error getting transactions for address %s: %v", address, err)
		http.Error(w, "Failed to fetch transactions", http.StatusInternalServerError)
		return
	}

	log.Printf("🔍 Found %d confirmed transactions", len(confirmedTxs))

	// Convert confirmed transactions to response format
	for _, tx := range confirmedTxs {
		if len(accountTxs) >= limit {
			break
		}

		txData := map[string]interface{}{
			"hash":      tx.Id,
			"from":      tx.From,
			"to":        tx.To,
			"amount":    tx.Amount,
			"nonce":     tx.Nonce,
			"gas":       tx.Gas,
			"gas_price": tx.GasPrice,
			"timestamp": tx.Timestamp,
			"status":    "confirmed",
		}
		accountTxs = append(accountTxs, txData)
	}

	// Add pending transactions (still check these for real-time updates)
	pendingTxs := s.worldState.GetPendingTransactions()
	log.Printf("🔍 Checking %d pending transactions", len(pendingTxs))

	for _, tx := range pendingTxs {
		if len(accountTxs) >= limit {
			break
		}

		if tx.From == address || tx.To == address {
			log.Printf("🔍 Found pending transaction: %s", tx.Id)

			txData := map[string]interface{}{
				"hash":      tx.Id,
				"from":      tx.From,
				"to":        tx.To,
				"amount":    tx.Amount,
				"nonce":     tx.Nonce,
				"gas":       tx.Gas,
				"gas_price": tx.GasPrice,
				"timestamp": tx.Timestamp,
				"status":    "pending",
			}
			accountTxs = append(accountTxs, txData)
		}
	}

	// Sort all transactions by timestamp (newest first)
	sort.Slice(accountTxs, func(i, j int) bool {
		timeI, okI := accountTxs[i]["timestamp"].(int64)
		timeJ, okJ := accountTxs[j]["timestamp"].(int64)
		if !okI || !okJ {
			return false
		}
		return timeI > timeJ
	})

	log.Printf("✅ Returning %d total transactions for address %s", len(accountTxs), address)

	response := map[string]interface{}{
		"address":      address,
		"transactions": accountTxs,
		"count":        len(accountTxs),
		"limit":        limit,
	}

	s.writeJSON(w, response)
}

// Development endpoint to fund addresses (for testing)
func (s *Server) fundAddress(w http.ResponseWriter, r *http.Request) {
	if !s.isDevEnvironment() || !s.enableFaucet {
		s.writeError(w, "Faucet not available", http.StatusForbidden)
		return
	}

	var req struct {
		Address string `json:"address"`
		Amount  string `json:"amount"`
	}
	// Support GET ?address= as well as POST JSON body
	if r.Method == http.MethodGet {
		req.Address = r.URL.Query().Get("address")
		req.Amount = r.URL.Query().Get("amount")
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.writeError(w, "Invalid request body", http.StatusBadRequest)
			return
		}
	}

	// Default faucet amount to 100 THR (100 * 10^18) when omitted.
	if strings.TrimSpace(req.Amount) == "" {
		req.Amount = "100000000000000000000"
	}

	if req.Address == "" {
		s.writeError(w, "Invalid address", http.StatusBadRequest)
		return
	}

	// 🛑 CRITICAL FIX: Check Rate Limit (Points Cooldown) BEFORE sending tokens
	// This prevents users from spamming the faucet for tokens even if they don't get points
	if s.pointsManager != nil {
		userPoints := s.pointsManager.GetUserPoints(req.Address)

		// Check if 24 hours have passed since last use
		if !userPoints.LastFaucet.IsZero() && time.Since(userPoints.LastFaucet) < 24*time.Hour {
			waitTime := 24*time.Hour - time.Since(userPoints.LastFaucet)

			// Return 429 Too Many Requests
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":          fmt.Sprintf("Faucet limit reached. Please wait %s.", waitTime.Round(time.Minute)),
				"status":         "cooldown",
				"next_available": userPoints.LastFaucet.Add(24 * time.Hour).Unix(),
			})
			return // <--- STOP EXECUTION HERE
		}
	}

	// --- IF WE PASS HERE, THE USER IS ALLOWED ---

	// Parse Amount string to BigInt
	amountBig := math.ParseBigInt(req.Amount)
	if amountBig.Sign() <= 0 {
		s.writeError(w, "Invalid amount", http.StatusBadRequest)
		return
	}

	// Try to get existing account first
	account, err := s.worldState.GetAccount(req.Address)
	if err != nil {
		// Account doesn't exist, create a new one
		account = &core.Account{
			Address:      req.Address,
			Balance:      math.BigIntToString(amountBig),
			Nonce:        0,
			StakedAmount: "0",
			DelegatedTo:  make(map[string]string),
			Rewards:      "0",
		}
	} else {
		// Account exists, add funding to existing balance
		currentBal := math.ParseBigInt(account.Balance)
		newBal := new(big.Int).Add(currentBal, amountBig)
		account.Balance = math.BigIntToString(newBal)
	}

	// Update storage (Send Tokens)
	if err := s.worldState.UpdateAccountWithStorage(account); err != nil {
		s.writeError(w, fmt.Sprintf("Failed to create/update account: %v", err), http.StatusInternalServerError)
		return
	}

	// Award Points (We know this will succeed now because we checked above)
	var newTotalPoints int
	var awarded bool
	if s.pointsManager != nil {
		newTotalPoints, awarded = s.pointsManager.AwardFaucet(req.Address)
	}

	// Convert to human-readable THRYLOS for display (Balance / 10^18)
	fBalance := new(big.Float).SetInt(math.ParseBigInt(account.Balance))
	fAmount := new(big.Float).SetInt(amountBig)
	fBase := new(big.Float).SetInt(config.BaseUnit) // 10^18

	balanceDisplay, _ := new(big.Float).Quo(fBalance, fBase).Float64()
	amountDisplay, _ := new(big.Float).Quo(fAmount, fBase).Float64()

	response := map[string]interface{}{
		"message":         "Account funded successfully",
		"address":         req.Address,
		"amount_added":    req.Amount,      // Raw string
		"amount_thrylos":  amountDisplay,   // Human readable
		"new_balance":     account.Balance, // Raw string
		"balance_thrylos": balanceDisplay,  // Human readable
		"points_awarded":  awarded,
		"total_points":    newTotalPoints,
		"nonce":           account.Nonce,
	}

	s.writeJSON(w, response)
}

// isDevEnvironment checks if we're running in a development environment
// Ethereum/Solana approach: environment-based, not authentication-based
func isDevEnvironmentForEnv(env string) bool {
	switch env {
	case "development", "dev", "devnet":
		return true
	case "testnet", "test":
		// Public testnet should mirror production safety defaults.
		return false
	case "production", "prod", "mainnet":
		return false
	case "":
		log.Println("⚠️  THRYLOS_ENVIRONMENT is not set, assuming PRODUCTION (dev-only features disabled)")
		return false
	default:
		log.Printf("⚠️  Unknown THRYLOS_ENVIRONMENT=%q, assuming PRODUCTION (dev-only features disabled)\n", env)
		return false
	}
}

func isDevEnvironment() bool {
	return isDevEnvironmentForEnv(strings.ToLower(os.Getenv("THRYLOS_ENVIRONMENT")))
}

func (s *Server) isDevEnvironment() bool {
	env := strings.ToLower(os.Getenv("THRYLOS_ENVIRONMENT"))
	if env == "" && s != nil && s.config != nil {
		env = strings.ToLower(strings.TrimSpace(s.config.Environment))
	}
	return isDevEnvironmentForEnv(env)
}

// ========== POINTS SYSTEM HANDLERS ==========

func (s *Server) getPoints(w http.ResponseWriter, r *http.Request) {
	// ✅ Safety Check
	if s.pointsManager == nil {
		s.writeError(w, "Points system not active on Mainnet", http.StatusNotImplemented)
		return
	}

	address := r.URL.Query().Get("address")
	if address == "" {
		s.writeError(w, "Address required", http.StatusBadRequest)
		return
	}

	// Get basic points data
	user := s.pointsManager.GetUserPoints(address)

	// Optional: Check if they have staked in the WorldState to award the "Delegation Bonus"
	account, err := s.worldState.GetAccount(address)
	hasStaked := false
	if err == nil && len(account.DelegatedTo) > 0 {
		hasStaked = true
	}

	// Sync chain activity (if you implemented SyncChainActivity in points.go)
	// s.pointsManager.SyncChainActivity(address, int(account.Nonce), hasStaked)

	// If using the simpler points.go from step 4, just checking delegation is enough:
	if hasStaked {
		s.pointsManager.RecordDelegation(address)
	}

	response := map[string]interface{}{
		"address":     user.Address,
		"points":      user.TotalPoints,
		"rank":        "Member",
		"streak":      user.CurrentStreak,
		"next_faucet": user.LastFaucet.Add(24 * time.Hour).Unix(),
	}

	s.writeJSON(w, response)
}

func (s *Server) getLeaderboard(w http.ResponseWriter, r *http.Request) {
	// Return top 50
	leaderboard := s.pointsManager.GetLeaderboard(50)
	s.writeJSON(w, leaderboard)
}

// Transaction endpoints (keep existing implementation)

func (s *Server) getTransaction(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	hash := vars["hash"]

	// Try to get from storage first
	tx, err := s.worldState.GetTransactionFromStorage(hash)
	if err != nil {
		// Check pending transactions
		pendingTxs := s.worldState.GetPendingTransactions()
		for _, pendingTx := range pendingTxs {
			if pendingTx.Id == hash {
				tx = pendingTx
				break
			}
		}
	}

	if tx == nil {
		s.writeError(w, "Transaction not found", http.StatusNotFound)
		return
	}

	response := TransactionResponse{
		Hash:      tx.Id,
		From:      tx.From,
		To:        tx.To,
		Amount:    tx.Amount,
		Nonce:     tx.Nonce,
		Gas:       tx.Gas,
		GasPrice:  tx.GasPrice,
		Timestamp: tx.Timestamp,
		Status:    "confirmed", // or determine actual status
	}

	s.writeJSON(w, response)
}

func (s *Server) getPendingTransactions(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	limitStr := r.URL.Query().Get("limit")
	limit := 100 // default limit
	if limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 && parsedLimit <= 1000 {
			limit = parsedLimit
		}
	}

	pendingTxs := s.worldState.GetPendingTransactions()

	var transactions []TransactionResponse
	for i, tx := range pendingTxs {
		if i >= limit {
			break
		}

		txResponse := TransactionResponse{
			Hash:      tx.Id,
			From:      tx.From,
			To:        tx.To,
			Amount:    tx.Amount,
			Nonce:     tx.Nonce,
			Gas:       tx.Gas,
			GasPrice:  tx.GasPrice,
			Timestamp: tx.Timestamp,
			Status:    "pending",
		}
		transactions = append(transactions, txResponse)
	}

	response := map[string]interface{}{
		"transactions": transactions,
		"count":        len(transactions),
		"limit":        limit,
	}

	s.writeJSON(w, response)
}

func (s *Server) getBlockByHash(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	hash := vars["hash"]

	block, err := s.worldState.GetBlockByHash(hash)
	if err != nil {
		// Try storage
		block, err = s.worldState.GetBlockFromStorage(hash)
		if err != nil {
			s.writeError(w, "Block not found", http.StatusNotFound)
			return
		}
	}

	response := s.formatBlock(block)
	s.writeJSON(w, response)
}

func (s *Server) getBlockByHeight(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	heightStr := vars["height"]

	height, err := strconv.ParseInt(heightStr, 10, 64)
	if err != nil {
		s.writeError(w, "Invalid height", http.StatusBadRequest)
		return
	}

	block, err := s.worldState.GetBlock(height)
	if err != nil {
		s.writeError(w, "Block not found", http.StatusNotFound)
		return
	}

	response := s.formatBlock(block)
	s.writeJSON(w, response)
}

func (s *Server) getLatestBlock(w http.ResponseWriter, r *http.Request) {
	block := s.worldState.GetCurrentBlock()
	if block == nil {
		s.writeError(w, "No blocks found", http.StatusNotFound)
		return
	}

	response := s.formatBlock(block)
	s.writeJSON(w, response)
}

func (s *Server) getBlocks(w http.ResponseWriter, r *http.Request) {
	limit := 10
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}

	height := s.worldState.GetHeight()
	if height < 0 {
		s.writeJSON(w, []map[string]interface{}{})
		return
	}

	blocks := make([]map[string]interface{}, 0, limit)
	for i := height; i >= 0 && len(blocks) < limit; i-- {
		block, err := s.worldState.GetBlock(i)
		if err != nil || block == nil {
			continue
		}
		blocks = append(blocks, s.formatBlock(block))
	}

	s.writeJSON(w, blocks)
}

func (s *Server) getRecentTransactions(w http.ResponseWriter, r *http.Request) {
	limit := 10
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}

	height := s.worldState.GetHeight()
	if height < 0 {
		s.writeJSON(w, []TransactionResponse{})
		return
	}

	txs := make([]TransactionResponse, 0, limit)
	for i := height; i >= 0 && len(txs) < limit; i-- {
		block, err := s.worldState.GetBlock(i)
		if err != nil || block == nil {
			continue
		}

		for j := len(block.Transactions) - 1; j >= 0 && len(txs) < limit; j-- {
			tx := block.Transactions[j]
			txs = append(txs, TransactionResponse{
				Hash:      tx.Id,
				From:      tx.From,
				To:        tx.To,
				Amount:    tx.Amount,
				Nonce:     tx.Nonce,
				Gas:       tx.Gas,
				GasPrice:  tx.GasPrice,
				Timestamp: tx.Timestamp,
				Status:    "confirmed",
			})
		}
	}

	sort.Slice(txs, func(i, j int) bool {
		return txs[i].Timestamp > txs[j].Timestamp
	})

	s.writeJSON(w, txs)
}

func (s *Server) getValidator(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	address := vars["address"]

	validator, err := s.worldState.GetValidator(address)
	if err != nil {
		s.writeError(w, "Validator not found", http.StatusNotFound)
		return
	}

	response := s.formatValidator(validator)
	s.writeJSON(w, response)
}

func (s *Server) getValidators(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	limitStr := r.URL.Query().Get("limit")
	limit := 100 // default limit
	if limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 && parsedLimit <= 1000 {
			limit = parsedLimit
		}
	}

	activeOnly := r.URL.Query().Get("active") == "true"

	var validators []map[string]interface{}

	if activeOnly {
		activeValidators := s.worldState.GetActiveValidators()
		for i, validator := range activeValidators {
			if i >= limit {
				break
			}
			validators = append(validators, s.formatValidator(validator))
		}
	} else {
		// Get all validators (you'd need to implement GetAllValidators)
		// For now, return active validators
		activeValidators := s.worldState.GetActiveValidators()
		for i, validator := range activeValidators {
			if i >= limit {
				break
			}
			validators = append(validators, s.formatValidator(validator))
		}
	}

	response := map[string]interface{}{
		"validators": validators,
		"count":      len(validators),
		"limit":      limit,
	}

	s.writeJSON(w, response)
}

func (s *Server) getActiveValidators(w http.ResponseWriter, r *http.Request) {
	activeValidators := s.worldState.GetActiveValidators()

	var validators []map[string]interface{}
	for _, validator := range activeValidators {
		validators = append(validators, s.formatValidator(validator))
	}

	response := map[string]interface{}{
		"validators": validators,
		"count":      len(validators),
	}

	s.writeJSON(w, response)
}

func (s *Server) getStatus(w http.ResponseWriter, r *http.Request) {
	status := s.worldState.GetStatus()
	s.writeJSON(w, status)
}

func (s *Server) getHealth(w http.ResponseWriter, r *http.Request) {
	health := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().Unix(),
		"height":    s.worldState.GetHeight(),
		"version":   "1.0.0",
	}
	s.writeJSON(w, health)
}

func (s *Server) formatBlock(block *core.Block) map[string]interface{} {
	var transactions []TransactionResponse
	for _, tx := range block.Transactions {
		txResponse := TransactionResponse{
			Hash:     tx.Id,
			From:     tx.From,
			To:       tx.To,
			Amount:   tx.Amount,
			Nonce:    tx.Nonce,
			Gas:      tx.Gas,
			GasPrice: tx.GasPrice,
		}
		transactions = append(transactions, txResponse)
	}

	return map[string]interface{}{
		"hash":         block.Hash,
		"height":       block.Header.Index,
		"index":        block.Header.Index,
		"prev_hash":    block.Header.PrevHash,
		"state_root":   block.Header.StateRoot,
		"timestamp":    block.Header.Timestamp,
		"gas_used":     block.Header.GasUsed,
		"gas_limit":    block.Header.GasLimit,
		"validator":    block.Header.Validator,
		"transactions": transactions,
		"tx_count":     len(block.Transactions),
	}
}
func (s *Server) formatValidator(validator *core.Validator) map[string]interface{} {
	// Convert boolean Active to string status
	status := "inactive"
	if validator.Active {
		status = "active"
	}

	// Handle jailed validators
	if validator.JailUntil > 0 && time.Now().Unix() < validator.JailUntil {
		status = "jailed"
	}

	return map[string]interface{}{
		"address":        validator.Address,
		"name":           validator.Name,
		"description":    validator.Description,
		"website":        validator.Website,
		"commission":     validator.Commission,
		"totalStaked":    validator.Stake + validator.DelegatedStake, // Combined stake
		"uptime":         s.calculateValidatorUptime(validator),
		"status":         status, // ✅ Fixed: string instead of boolean
		"selfStake":      validator.SelfStake,
		"delegatorCount": len(validator.Delegators),
		"blocksProposed": validator.BlocksProposed,
		"blocksMissed":   validator.BlocksMissed,
		"createdAt":      validator.CreatedAt,
		"updatedAt":      validator.UpdatedAt,
		"delegations":    validator.Delegators,

		// Keep the old fields for backward compatibility
		"active":          validator.Active,
		"stake":           validator.Stake,
		"self_stake":      validator.SelfStake,
		"delegated_stake": validator.DelegatedStake,
		"blocks_proposed": validator.BlocksProposed,
		"blocks_missed":   validator.BlocksMissed,
		"jail_until":      validator.JailUntil,
		"created_at":      validator.CreatedAt,
		"updated_at":      validator.UpdatedAt,
		"delegator_count": len(validator.Delegators),
	}
}

// Add this helper function
func (s *Server) calculateValidatorUptime(validator *core.Validator) float64 {
	totalBlocks := validator.BlocksProposed + validator.BlocksMissed
	if totalBlocks == 0 {
		return 100.0 // New validators start with 100% uptime
	}
	return (float64(validator.BlocksProposed) / float64(totalBlocks)) * 100.0
}

func (s *Server) writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("Error encoding JSON: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func (s *Server) writeError(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error":     message,
		"status":    statusCode,
		"timestamp": time.Now().Unix(),
	})
}

// Middleware (keep existing)

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Create a custom ResponseWriter to capture status code
		lrw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(lrw, r)

		duration := time.Since(start)
		log.Printf("%s %s %d %v", r.Method, r.URL.Path, lrw.statusCode, duration)
	})
}

func (s *Server) jsonMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

type StakingStatsResponse struct {
	TotalStaked           string  `json:"total_staked"`
	AnnualPercentageYield float64 `json:"annual_percentage_yield"`
	NextRewardTime        *int64  `json:"next_reward_time"`
	UnbondingPeriod       int     `json:"unbonding_period"`
	ActiveValidators      int     `json:"active_validators"`
	TotalDelegators       int     `json:"total_delegators"`
	AverageCommission     float64 `json:"average_commission"`
}

type StakingValidatorResponse struct {
	Address        string  `json:"address"`
	Name           string  `json:"name"`
	Description    string  `json:"description"` // Add this field
	Website        string  `json:"website"`     // Add this field
	Commission     float64 `json:"commission"`
	TotalStaked    string  `json:"totalStaked"`
	Uptime         float64 `json:"uptime"`
	Status         string  `json:"status"`
	SelfStake      string  `json:"selfStake"`
	DelegatorCount int     `json:"delegatorCount"`
	BlocksProposed int64   `json:"blocksProposed"`
	BlocksMissed   int64   `json:"blocksMissed"`
	CreatedAt      int64   `json:"createdAt"` // Add this field
	UpdatedAt      int64   `json:"updatedAt"` // Add this field
}

type DelegationHistoryItem struct {
	Validator string `json:"validator"`
	Amount    string `json:"amount"`
	Timestamp int64  `json:"timestamp"`
	Status    string `json:"status"`
	TxHash    string `json:"tx_hash"`
	Action    string `json:"action"` // "delegate", "undelegate", "claim"
}

type DelegationHistoryResponse struct {
	Address string                  `json:"address"`
	History []DelegationHistoryItem `json:"history"`
	Count   int                     `json:"count"`
}

// ========== ENDPOINT IMPLEMENTATIONS ==========

// 1. Get staking statistics
func (s *Server) getStakingStats(w http.ResponseWriter, r *http.Request) {
	activeValidators := s.worldState.GetActiveValidators()
	totalStaked := s.worldState.GetTotalStaked()
	activeValidatorCount := len(activeValidators)

	totalCommission := float64(0)
	totalDelegators := 0

	for _, validator := range activeValidators {
		totalCommission += validator.Commission
		totalDelegators += len(validator.Delegators)
	}

	averageCommission := float64(0)
	if activeValidatorCount > 0 {
		averageCommission = totalCommission / float64(activeValidatorCount)
	}

	// Base APY - you can get this from config if available
	baseAPY := float64(8.5)

	// Next reward time based on your block time
	nextRewardTime := time.Now().Unix() + 200 // 200ms from your config

	response := StakingStatsResponse{
		TotalStaked:           totalStaked.String(),
		AnnualPercentageYield: baseAPY,
		NextRewardTime:        &nextRewardTime,
		UnbondingPeriod:       21, // Days
		ActiveValidators:      activeValidatorCount,
		TotalDelegators:       totalDelegators,
		AverageCommission:     averageCommission,
	}

	s.writeJSON(w, response)
}

// getAccountUnbonding returns the unbonding queue for an account
// In server.go - Update the handler to use WorldState directly
func (s *Server) getAccountUnbonding(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	address := vars["address"]

	// ✅ Use the existing WorldState method
	unbondingEntries := s.worldState.GetUnbondingEntries(address)

	// Format for frontend
	var formattedQueue []map[string]interface{}
	for _, entry := range unbondingEntries {
		formattedQueue = append(formattedQueue, map[string]interface{}{
			"validator":   entry.ValidatorAddr,
			"amount":      entry.Amount,
			"created_at":  entry.CreationTime,
			"complete_at": entry.CompletionTime,
			"delegator":   entry.DelegatorAddr,
		})
	}

	response := map[string]interface{}{
		"address":         address,
		"unbonding_queue": formattedQueue,
		"count":           len(formattedQueue),
	}

	s.writeJSON(w, response)
}

// 2. Get validators formatted for staking interface
func (s *Server) getStakingValidators(w http.ResponseWriter, r *http.Request) {
	activeValidators := s.worldState.GetActiveValidators()

	var validators []StakingValidatorResponse
	for _, validator := range activeValidators {
		// Calculate uptime percentage
		uptime := float64(100) // Default to 100%
		if validator.BlocksProposed > 0 {
			total := validator.BlocksProposed + validator.BlocksMissed
			uptime = float64(validator.BlocksProposed) / float64(total) * 100
		}

		// Use actual validator name if available, otherwise generate fallback
		name := validator.Name
		if name == "" {
			name = fmt.Sprintf("Validator %s", validator.Address[:12])
		}

		// More robust status determination
		status := "active" // Default to active

		currentTime := time.Now().Unix()

		// Check if jailed
		if validator.JailUntil > currentTime {
			status = "jailed"
		} else {
			// If not jailed, check if explicitly marked as inactive
			// OR if it has very low activity (no blocks proposed and old)
			if !validator.Active {
				status = "inactive"
			} else if validator.BlocksProposed == 0 &&
				validator.CreatedAt > 0 &&
				(currentTime-validator.CreatedAt) > 3600 { // Created more than 1 hour ago but no blocks
				status = "inactive"
			} else {
				status = "active"
			}
		}

		validatorResponse := StakingValidatorResponse{
			Address:        validator.Address,
			Name:           name,
			Description:    validator.Description,
			Website:        validator.Website,
			Commission:     validator.Commission,
			TotalStaked:    validator.Stake,
			Uptime:         uptime,
			Status:         status,
			SelfStake:      validator.SelfStake,
			DelegatorCount: len(validator.Delegators),
			BlocksProposed: validator.BlocksProposed,
			BlocksMissed:   validator.BlocksMissed,
			CreatedAt:      validator.CreatedAt,
			UpdatedAt:      validator.UpdatedAt,
		}

		validators = append(validators, validatorResponse)
	}

	response := map[string]interface{}{
		"validators": validators,
		"count":      len(validators),
	}

	s.writeJSON(w, response)
}

// 5. Submit reward claiming transaction
// func (s *Server) submitClaimTransaction(w http.ResponseWriter, r *http.Request) {
// 	var tx core.Transaction
// 	if err := json.NewDecoder(r.Body).Decode(&tx); err != nil {
// 		s.writeError(w, "Invalid transaction format", http.StatusBadRequest)
// 		return
// 	}

// 	// Set the correct transaction type for your executor
// 	tx.Type = core.TransactionType_CLAIM_REWARDS

// 	// For claim transactions, amount should be 0 (claims all available rewards)
// 	tx.Amount = 0
// 	tx.To = tx.From // Self-transaction for claiming

// 	// Validate required fields
// 	if tx.From == "" {
// 		s.writeError(w, "Invalid claim transaction: missing sender", http.StatusBadRequest)
// 		return
// 	}

// 	// Validate signature
// 	if len(tx.Signature) == 0 {
// 		s.writeError(w, "Transaction signature required", http.StatusBadRequest)
// 		return
// 	}

// 	// Check if user has rewards to claim
// 	account, err := s.worldState.GetAccount(tx.From)
// 	if err != nil {
// 		s.writeError(w, "Account not found", http.StatusNotFound)
// 		return
// 	}

// 	if account.Rewards <= 0 {
// 		s.writeError(w, "No rewards available to claim", http.StatusBadRequest)
// 		return
// 	}

// 	// Use your existing validation and execution system
// 	if err := s.worldState.ValidateTransaction(&tx); err != nil {
// 		s.writeError(w, fmt.Sprintf("Transaction validation failed: %v", err), http.StatusBadRequest)
// 		return
// 	}

// 	if err := s.worldState.AddTransaction(&tx); err != nil {
// 		s.writeError(w, fmt.Sprintf("Failed to submit claim transaction: %v", err), http.StatusBadRequest)
// 		return
// 	}

// 	response := map[string]interface{}{
// 		"status":         "accepted",
// 		"tx_hash":        tx.Hash,
// 		"message":        "Claim transaction submitted successfully",
// 		"claimed_amount": account.Rewards,
// 	}

// 	s.writeJSON(w, response)
// }

// 6. Get delegation history
func (s *Server) getDelegationHistory(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	address := vars["address"]

	// Parse query parameters
	limitStr := r.URL.Query().Get("limit")
	limit := 50 // default limit
	if limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 && parsedLimit <= 1000 {
			limit = parsedLimit
		}
	}

	// Get current delegations from account
	var history []DelegationHistoryItem

	account, err := s.worldState.GetAccount(address)
	if err != nil {
		// Account might not exist, return empty history
		history = []DelegationHistoryItem{}
	} else {
		// Convert current delegations to history format
		if account.DelegatedTo != nil {
			for validator, amount := range account.DelegatedTo {
				if len(history) >= limit {
					break
				}

				history = append(history, DelegationHistoryItem{
					Validator: validator,
					Amount:    amount,
					Timestamp: time.Now().Unix(),
					Status:    "active",
					TxHash:    "", // Would need transaction indexing for real tx hash
					Action:    "delegate",
				})
			}
		}
	}

	response := DelegationHistoryResponse{
		Address: address,
		History: history,
		Count:   len(history),
	}

	s.writeJSON(w, response)
}

// 7. Get detailed rewards information
func (s *Server) getDetailedRewards(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	address := vars["address"]

	account, err := s.worldState.GetAccount(address)
	if err != nil {
		s.writeError(w, "Account not found", http.StatusNotFound)
		return
	}

	// Use big.Float for precise APY calculations with 18 decimals
	estimatedDaily := big.NewFloat(0)
	estimatedMonthly := big.NewFloat(0)
	estimatedAnnual := big.NewFloat(0)

	baseAPY := 0.085 // 8.5%

	if account.DelegatedTo != nil {
		for validatorAddr, stakedAmountStr := range account.DelegatedTo {
			validator, err := s.worldState.GetValidator(validatorAddr)
			if err != nil {
				continue
			}

			// 1. Parse staked amount string to BigFloat
			stakeInt, ok := new(big.Int).SetString(stakedAmountStr, 10)
			if !ok {
				continue
			}
			stakeFloat := new(big.Float).SetInt(stakeInt)

			// 2. Calculate Net APY
			// netAPY = baseAPY * (1 - commission/100)
			commissionFactor := 1.0 - (validator.Commission / 100.0)
			netAPY := baseAPY * commissionFactor

			// 3. Calculate Annual Reward: stake * netAPY
			annualReward := new(big.Float).Mul(stakeFloat, big.NewFloat(netAPY))

			// 4. Calculate intervals
			dailyReward := new(big.Float).Quo(annualReward, big.NewFloat(365))
			monthlyReward := new(big.Float).Quo(annualReward, big.NewFloat(12))

			// 5. Accumulate
			estimatedAnnual.Add(estimatedAnnual, annualReward)
			estimatedMonthly.Add(estimatedMonthly, monthlyReward)
			estimatedDaily.Add(estimatedDaily, dailyReward)
		}
	}

	// Convert BigFloats to BigInt strings for the response (to avoid overflow)
	// We truncate decimals for the response by converting Float -> Int
	estDailyInt, _ := estimatedDaily.Int(nil)
	estMonthlyInt, _ := estimatedMonthly.Int(nil)
	estAnnualInt, _ := estimatedAnnual.Int(nil)

	response := map[string]interface{}{
		"address":           address,
		"current_rewards":   account.Rewards, // Already a string
		"estimated_daily":   estDailyInt.String(),
		"estimated_monthly": estMonthlyInt.String(),
		"estimated_annual":  estAnnualInt.String(),
		"delegations":       account.DelegatedTo,
		"staked_amount":     account.StakedAmount,
	}

	s.writeJSON(w, response)
}

// func (s *Server) createValidator(w http.ResponseWriter, r *http.Request) {
// 	var tx core.Transaction
// 	if err := json.NewDecoder(r.Body).Decode(&tx); err != nil {
// 		s.writeError(w, "Invalid transaction format", http.StatusBadRequest)
// 		return
// 	}

// 	log.Printf("Received validator creation transaction: %+v", tx)

// 	// Validate required fields for validator creation
// 	if tx.From == "" {
// 		s.writeError(w, "Validator address (from) is required", http.StatusBadRequest)
// 		return
// 	}

// 	if tx.Hash == "" {
// 		s.writeError(w, "Transaction hash is required", http.StatusBadRequest)
// 		return
// 	}

// 	if len(tx.Signature) == 0 {
// 		s.writeError(w, "Transaction signature is required", http.StatusBadRequest)
// 		return
// 	}

// 	// For validator transactions, we might use a different transaction type
// 	// Check if it's type 6 from frontend or adjust based on your system
// 	if tx.Type != 6 {
// 		log.Printf("Warning: Expected type 6 for validator creation, got %d", tx.Type)
// 		// Force set to validator creation type
// 		tx.Type = 6
// 	}

// 	// Parse validator data from transaction data field
// 	var validatorData struct {
// 		Type        string  `json:"type"`
// 		Name        string  `json:"name"`
// 		Description string  `json:"description"`
// 		Website     string  `json:"website"`
// 		Commission  float64 `json:"commission"`
// 		SelfStake   int64   `json:"self_stake"`
// 	}

// 	if len(tx.Data) > 0 {
// 		if err := json.Unmarshal(tx.Data, &validatorData); err != nil {
// 			s.writeError(w, "Invalid validator data format", http.StatusBadRequest)
// 			return
// 		}
// 	}

// 	// Validate validator data
// 	if validatorData.Name == "" {
// 		s.writeError(w, "Validator name is required", http.StatusBadRequest)
// 		return
// 	}

// 	if validatorData.Commission < 0 || validatorData.Commission > 1 {
// 		s.writeError(w, "Validator commission must be between 0 and 1", http.StatusBadRequest)
// 		return
// 	}

// 	// Check minimum stake requirement (25 THRYLOS = 25 * 1e9 nano)
// 	const MIN_VALIDATOR_STAKE = 25 * 1000000000
// 	if tx.Amount < MIN_VALIDATOR_STAKE {
// 		s.writeError(w, fmt.Sprintf("Minimum validator stake is %d nano (25 THRYLOS)", MIN_VALIDATOR_STAKE), http.StatusBadRequest)
// 		return
// 	}

// 	// Check if validator already exists
// 	existingValidator, err := s.worldState.GetValidator(tx.From)
// 	if err == nil && existingValidator != nil {
// 		s.writeError(w, "Validator already exists for this address", http.StatusBadRequest)
// 		return
// 	}

// 	// Check account balance
// 	account, err := s.worldState.GetAccount(tx.From)
// 	if err != nil {
// 		s.writeError(w, "Account not found", http.StatusNotFound)
// 		return
// 	}

// 	totalCost := tx.Amount + (tx.Gas * tx.GasPrice)
// 	if account.Balance < totalCost {
// 		s.writeError(w, fmt.Sprintf("Insufficient balance: have %d, need %d", account.Balance, totalCost), http.StatusBadRequest)
// 		return
// 	}

// 	// Validate nonce
// 	if account.Nonce != tx.Nonce {
// 		s.writeError(w, fmt.Sprintf("Invalid nonce: expected %d, got %d", account.Nonce, tx.Nonce), http.StatusBadRequest)
// 		return
// 	}

// 	// Create the validator object with all metadata
// 	dummyPubkey := make([]byte, 32)
// 	copy(dummyPubkey, []byte(tx.From)) // Use address as temp pubkey

// 	validator := &core.Validator{
// 		Address:        tx.From,
// 		Pubkey:         dummyPubkey,
// 		Name:           validatorData.Name,        // Add name
// 		Description:    validatorData.Description, // Add description
// 		Website:        validatorData.Website,     // Add website
// 		Stake:          tx.Amount,
// 		SelfStake:      tx.Amount,
// 		DelegatedStake: 0,
// 		Commission:     validatorData.Commission,
// 		Active:         true,
// 		BlocksProposed: 0,
// 		BlocksMissed:   0,
// 		JailUntil:      0,
// 		CreatedAt:      time.Now().Unix(),
// 		UpdatedAt:      time.Now().Unix(),
// 		Delegators:     make(map[string]int64),
// 	}

// 	// Add validator to the system
// 	if err := s.worldState.AddValidator(validator); err != nil {
// 		s.writeError(w, fmt.Sprintf("Failed to create validator: %v", err), http.StatusInternalServerError)
// 		return
// 	}

// 	// Update account - deduct staked amount and gas fees
// 	account.Balance -= totalCost
// 	account.StakedAmount += tx.Amount
// 	account.Nonce++

// 	if err := s.worldState.UpdateAccountWithStorage(account); err != nil {
// 		s.writeError(w, fmt.Sprintf("Failed to update account: %v", err), http.StatusInternalServerError)
// 		return
// 	}

// 	// Optionally validate the transaction signature if needed
// 	if err := s.worldState.ValidateTransaction(&tx); err != nil {
// 		log.Printf("Warning: Transaction validation failed (proceeding anyway): %v", err)
// 	}

// 	// Add transaction to pending pool for inclusion in next block
// 	if err := s.worldState.AddTransaction(&tx); err != nil {
// 		log.Printf("Warning: Failed to add validator creation transaction to pool: %v", err)
// 	}

// 	log.Printf("Validator created successfully: %s (%s) with stake %d", validatorData.Name, tx.From, tx.Amount)

// 	// Return success response
// 	response := map[string]interface{}{
// 		"status":  "success",
// 		"message": "Validator created successfully",
// 		"tx_hash": tx.Hash,
// 		"validator": map[string]interface{}{
// 			"address":     validator.Address,
// 			"name":        validator.Name,        // Include name in response
// 			"description": validator.Description, // Include description in response
// 			"website":     validator.Website,     // Include website in response
// 			"commission":  validator.Commission,
// 			"self_stake":  validator.SelfStake,
// 			"total_stake": validator.Stake,
// 			"active":      validator.Active,
// 			"created_at":  validator.CreatedAt,
// 		},
// 	}

// 	s.writeJSON(w, response)
// }

type ValidatorActivityItem struct {
	Type      string `json:"type"`      // "block", "delegation", "commission", "withdrawal"
	Details   string `json:"details"`   // Human readable description
	Time      string `json:"time"`      // Human readable time
	Timestamp int64  `json:"timestamp"` // Unix timestamp

	// ✅ Change this from *int64 to *string
	Reward *string `json:"reward"`

	Amount *string `json:"amount"`  // Transaction amount in nano (nullable)
	TxHash string  `json:"tx_hash"` // Transaction hash if applicable
	From   string  `json:"from"`    // Address for delegation events
}

type ValidatorActivityResponse struct {
	Address  string                  `json:"address"`
	Activity []ValidatorActivityItem `json:"activity"`
	Count    int                     `json:"count"`
	Limit    int                     `json:"limit"`
}

// Add this function to your server.go file
func (s *Server) getValidatorActivity(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	address := vars["address"]

	// Parse query parameters
	limitStr := r.URL.Query().Get("limit")
	limit := 50 // default limit
	if limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 && parsedLimit <= 1000 {
			limit = parsedLimit
		}
	}

	// Check if validator exists
	validator, err := s.worldState.GetValidator(address)
	if err != nil {
		s.writeError(w, "Validator not found", http.StatusNotFound)
		return
	}

	var activity []ValidatorActivityItem

	// 1. Get block validation activity
	if validator.BlocksProposed > 0 {
		// Get recent blocks validated by this validator
		activity = append(activity, s.getBlockValidationActivity(address, validator)...)
	}

	// 2. Get delegation activity
	delegationActivity := s.getDelegationActivity(address, validator)
	activity = append(activity, delegationActivity...)

	// 3. Get commission earnings activity
	commissionActivity := s.getCommissionActivity(address, validator)
	activity = append(activity, commissionActivity...)

	// 4. Sort by timestamp (most recent first)
	sort.Slice(activity, func(i, j int) bool {
		return activity[i].Timestamp > activity[j].Timestamp
	})

	// 5. Apply limit
	if len(activity) > limit {
		activity = activity[:limit]
	}

	response := ValidatorActivityResponse{
		Address:  address,
		Activity: activity,
		Count:    len(activity),
		Limit:    limit,
	}

	s.writeJSON(w, response)
}

// Helper function to get block validation activity
func (s *Server) getBlockValidationActivity(address string, validator *core.Validator) []ValidatorActivityItem {
	var activity []ValidatorActivityItem

	// 1. Get block reward from config (It is now a *big.Int)
	// Convert to string to match the struct definition
	rewardStr := math.BigIntToString(config.BlockReward)

	// Generate recent block validation events based on blocks proposed
	blocksToShow := int64(5)
	if validator.BlocksProposed < blocksToShow {
		blocksToShow = validator.BlocksProposed
	}

	currentTime := time.Now().Unix()

	for i := int64(0); i < blocksToShow; i++ {
		blockNumber := validator.BlocksProposed - i
		timeAgo := currentTime - (i * 3)

		// 2. Create a local copy of the string for this iteration
		// (Needed to take a valid pointer address &currentReward)
		currentReward := rewardStr

		activity = append(activity, ValidatorActivityItem{
			Type:      "block",
			Details:   fmt.Sprintf("Block #%d validated", blockNumber),
			Time:      formatTimeAgo(timeAgo),
			Timestamp: timeAgo,

			// ✅ Fix: Pass the address of the string
			Reward: &currentReward,

			TxHash: fmt.Sprintf("block_%d_%s", blockNumber, address[:8]),
		})
	}

	return activity
}

// Helper function to get delegation activity
func (s *Server) getDelegationActivity(address string, validator *core.Validator) []ValidatorActivityItem {
	var activity []ValidatorActivityItem

	for delegatorAddr, amountStr := range validator.Delegators {
		// 1. Create a local copy to safely take the address
		currentAmount := amountStr

		readableAmount := formatToThrylos(currentAmount)

		activity = append(activity, ValidatorActivityItem{
			Type:      "delegation",
			Details:   fmt.Sprintf("New delegation: %s THR", readableAmount),
			Time:      formatTimeAgo(time.Now().Unix() - 3600),
			Timestamp: time.Now().Unix() - 3600,

			// ✅ Fix: Use the address of the local copy
			Amount: &currentAmount,

			From:   delegatorAddr,
			TxHash: fmt.Sprintf("del_%s_%s", delegatorAddr[:8], address[:8]),
		})
	}

	return activity
}

// Helper function to get commission activity
func (s *Server) getCommissionActivity(address string, validator *core.Validator) []ValidatorActivityItem {
	var activity []ValidatorActivityItem

	// 1. Parse DelegatedStake string to BigInt
	delegatedStake := math.ParseBigInt(validator.DelegatedStake)

	// 2. Check if > 0 using Sign()
	if delegatedStake.Sign() > 0 {
		// Convert to BigFloat for math operations
		stakeFloat := new(big.Float).SetInt(delegatedStake)

		// Use actual validator reward rate: 9% APR
		validatorAPR := 0.09

		// Calculate Daily Reward: Stake * APR / 365
		annualReward := new(big.Float).Mul(stakeFloat, big.NewFloat(validatorAPR))
		dailyReward := new(big.Float).Quo(annualReward, big.NewFloat(365))

		// Calculate Commission: DailyReward * CommissionRate
		// (validator.Commission is likely float64 like 0.10)
		commissionFloat := new(big.Float).Mul(dailyReward, big.NewFloat(validator.Commission))

		// Convert result back to BigInt -> String
		commissionInt, _ := commissionFloat.Int(nil)

		if commissionInt.Sign() > 0 {
			commissionStr := commissionInt.String()

			activity = append(activity, ValidatorActivityItem{
				Type:      "commission",
				Details:   "Commission earned from delegations",
				Time:      formatTimeAgo(time.Now().Unix() - 7200), // 2 hours ago
				Timestamp: time.Now().Unix() - 7200,

				// ✅ Fix: Pass address of the string string
				Reward: &commissionStr,

				TxHash: fmt.Sprintf("comm_%s_%d", address[:8], time.Now().Unix()),
			})
		}
	}

	return activity
}

// Helper function to format amounts to THRYLOS
func formatToThrylos(amountStr string) string {
	// 1. Parse the string value to BigInt
	val, ok := new(big.Int).SetString(amountStr, 10)
	if !ok {
		return "0.00" // Handle invalid strings gracefully
	}

	// 2. Convert to BigFloat for precise division
	fVal := new(big.Float).SetInt(val)

	// 3. Get BaseUnit from config (10^18) as Float
	fBase := new(big.Float).SetInt(config.BaseUnit)

	// 4. Divide: Amount / BaseUnit
	result := new(big.Float).Quo(fVal, fBase)

	// 5. Return formatted string (e.g., "10.500000")
	// 'f' = decimal notation, 6 = precision
	return result.Text('f', 6)
}

// Helper function to format time ago
func formatTimeAgo(timestamp int64) string {
	diff := time.Now().Unix() - timestamp

	if diff < 60 {
		return fmt.Sprintf("%d seconds ago", diff)
	} else if diff < 3600 {
		minutes := diff / 60
		return fmt.Sprintf("%d minutes ago", minutes)
	} else if diff < 86400 {
		hours := diff / 3600
		return fmt.Sprintf("%d hours ago", hours)
	} else {
		days := diff / 86400
		return fmt.Sprintf("%d days ago", days)
	}
}

// Enhanced version that integrates with your transaction system
func (s *Server) getValidatorActivityEnhanced(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	address := vars["address"]

	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 && parsedLimit <= 1000 {
			limit = parsedLimit
		}
	}

	validator, err := s.worldState.GetValidator(address)
	if err != nil {
		s.writeError(w, "Validator not found", http.StatusNotFound)
		return
	}

	var activity []ValidatorActivityItem

	// 1. Check recent transactions for validator-related activity
	pendingTxs := s.worldState.GetPendingTransactions()
	for _, tx := range pendingTxs {
		if tx.To == address && (tx.Type == core.TransactionType_DELEGATE) {
			amount := tx.Amount
			activity = append(activity, ValidatorActivityItem{
				Type:      "delegation",
				Details:   fmt.Sprintf("New delegation: %s THR", formatToThrylos(tx.Amount)),
				Time:      formatTimeAgo(tx.Timestamp),
				Timestamp: tx.Timestamp,
				Amount:    &amount,
				From:      tx.From,
				TxHash:    tx.Hash,
			})
		}

		if tx.From == address && (tx.Type == core.TransactionType_UNDELEGATE) {
			amount := tx.Amount
			activity = append(activity, ValidatorActivityItem{
				Type:      "withdrawal",
				Details:   fmt.Sprintf("Undelegation processed: %s THR", formatToThrylos(tx.Amount)),
				Time:      formatTimeAgo(tx.Timestamp),
				Timestamp: tx.Timestamp,
				Amount:    &amount,
				TxHash:    tx.Hash,
			})
		}
	}

	// 2. Add block validation activity
	blockActivity := s.getBlockValidationActivity(address, validator)
	activity = append(activity, blockActivity...)

	// 3. Add commission activity
	commissionActivity := s.getCommissionActivity(address, validator)
	activity = append(activity, commissionActivity...)

	// Sort and limit
	sort.Slice(activity, func(i, j int) bool {
		return activity[i].Timestamp > activity[j].Timestamp
	})

	if len(activity) > limit {
		activity = activity[:limit]
	}

	response := ValidatorActivityResponse{
		Address:  address,
		Activity: activity,
		Count:    len(activity),
		Limit:    limit,
	}

	s.writeJSON(w, response)
}

// verifyStakingSignature verifies that the request was signed by the owner of fromAddr.
// The frontend must sign the canonical payload string with the user's Ethereum private key.
// Canonical payload: "<from>:<to>:<amount>:<timestamp>"  (same fields the tx hash uses)
func verifyStakingSignature(fromAddr, toAddr, amount string, timestamp int64, sigHex string) error {
	// 1. Strip 0x prefix if present
	sigHex = strings.TrimPrefix(sigHex, "0x")
	sigBytes, err := hex.DecodeString(sigHex)
	if err != nil {
		return fmt.Errorf("invalid signature hex: %w", err)
	}
	if len(sigBytes) != 65 {
		return fmt.Errorf("invalid signature length: got %d, want 65", len(sigBytes))
	}

	// Reject requests older than 5 minutes or more than 30s in the future
	now := time.Now().Unix()
	if timestamp < now-300 || timestamp > now+30 {
		return fmt.Errorf("request timestamp out of acceptable window")
	}

	// 2. Build the exact same payload the frontend signed
	payload := fmt.Sprintf("%s:%s:%s:%d", fromAddr, toAddr, amount, timestamp)

	// 3. Ethereum personal_sign prefixes the message before hashing
	// This matches MetaMask's eth_sign / personal_sign behaviour
	prefixed := fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(payload), payload)
	hash := crypto.Keccak256([]byte(prefixed))

	// 4. Normalise recovery ID: Ethereum uses 27/28, go-ethereum expects 0/1
	if sigBytes[64] >= 27 {
		sigBytes[64] -= 27
	}

	// 5. Recover the public key from the signature
	pubKeyBytes, err := crypto.Ecrecover(hash, sigBytes)
	if err != nil {
		return fmt.Errorf("failed to recover public key: %w", err)
	}

	pubKey, err := crypto.UnmarshalPubkey(pubKeyBytes)
	if err != nil {
		return fmt.Errorf("failed to unmarshal public key: %w", err)
	}

	// 6. Derive address from recovered key and compare
	recoveredAddr := crypto.PubkeyToAddress(*pubKey).Hex()
	if !strings.EqualFold(recoveredAddr, fromAddr) {
		return fmt.Errorf("signature mismatch: signed by %s, claimed %s", recoveredAddr, fromAddr)
	}

	return nil
}

// POST /api/v1/admin/export-points-snapshot
// Protected by admin auth / dev environment gate
func (s *Server) handleExportPointsSnapshot(w http.ResponseWriter, r *http.Request) {
	if !s.isDevEnvironment() {
		s.writeError(w, "Not available", http.StatusForbidden)
		return
	}
	path := os.Getenv("POINTS_SNAPSHOT_PATH")
	if path == "" {
		path = "points_snapshot.json"
	}
	if err := s.pointsManager.ExportSnapshot(path); err != nil {
		s.writeError(w, "Export failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "exported", "path": path})
}
