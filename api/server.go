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
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/cors"
	"github.com/thrylos-labs/go-thrylos/core/state"
	"github.com/thrylos-labs/go-thrylos/proto/core"
)

// Server represents the HTTP API server
type Server struct {
	worldState *state.WorldState
	router     *mux.Router
	server     *http.Server
	port       int

	// HTTPS configuration
	enableTLS bool
	certFile  string
	keyFile   string
}

// ServerConfig represents server configuration
type ServerConfig struct {
	Port      int
	EnableTLS bool
	CertFile  string
	KeyFile   string
}

// Response structures for account-based system
type AccountResponse struct {
	Address      string           `json:"address"`
	Balance      int64            `json:"balance"`
	Nonce        uint64           `json:"nonce"`
	StakedAmount int64            `json:"staked_amount"`
	Rewards      int64            `json:"rewards"`
	DelegatedTo  map[string]int64 `json:"delegated_to"`
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
	Amount    int64  `json:"amount"`
	Nonce     uint64 `json:"nonce"`
	Gas       int64  `json:"gas"`
	GasPrice  int64  `json:"gas_price"`
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
		worldState: worldState,
		port:       port,
	}

	server.setupRoutes()
	return server
}

// NewServerWithConfig creates a new API server with full configuration
func NewServerWithConfig(worldState *state.WorldState, config *ServerConfig) *Server {
	server := &Server{
		worldState: worldState,
		port:       config.Port,
		enableTLS:  config.EnableTLS,
		certFile:   config.CertFile,
		keyFile:    config.KeyFile,
	}

	server.setupRoutes()
	return server
}

// setupRoutes configures all API routes
func (s *Server) setupRoutes() {
	s.router = mux.NewRouter()

	// API version prefix
	api := s.router.PathPrefix("/api/v1").Subrouter()

	// Account endpoints
	api.HandleFunc("/account/{address}/balance", s.getAccountBalance).Methods("GET", "OPTIONS")
	api.HandleFunc("/account/{address}", s.getAccount).Methods("GET", "OPTIONS")
	api.HandleFunc("/account/{address}/transactions", s.getAccountTransactions).Methods("GET", "OPTIONS")
	api.HandleFunc("/account/{address}/delegations", s.getAccountDelegations).Methods("GET", "OPTIONS")

	// Staking endpoints
	api.HandleFunc("/account/{address}/stake", s.getAccountStake).Methods("GET", "OPTIONS")
	api.HandleFunc("/account/{address}/rewards", s.getAccountRewards).Methods("GET", "OPTIONS")
	api.HandleFunc("/staking/stats", s.getStakingStats).Methods("GET", "OPTIONS")
	api.HandleFunc("/staking/validators", s.getStakingValidators).Methods("GET", "OPTIONS")
	api.HandleFunc("/staking/delegate", s.submitStakeTransaction).Methods("POST", "OPTIONS")
	api.HandleFunc("/staking/undelegate", s.submitUnstakeTransaction).Methods("POST", "OPTIONS")
	api.HandleFunc("/staking/claim", s.submitClaimTransaction).Methods("POST", "OPTIONS")
	api.HandleFunc("/staking/delegations/{address}", s.getDelegationHistory).Methods("GET", "OPTIONS")
	api.HandleFunc("/staking/rewards/{address}", s.getDetailedRewards).Methods("GET", "OPTIONS")

	// Development endpoints - EXPLICITLY ADD OPTIONS
	api.HandleFunc("/fund", s.fundAddress).Methods("POST", "OPTIONS")

	api.HandleFunc("/transaction/{hash}", s.getTransaction).Methods("GET", "OPTIONS")
	api.HandleFunc("/transactions/pending", s.getPendingTransactions).Methods("GET", "OPTIONS")
	api.HandleFunc("/block/{hash}", s.getBlockByHash).Methods("GET", "OPTIONS")
	api.HandleFunc("/block/height/{height}", s.getBlockByHeight).Methods("GET", "OPTIONS")
	api.HandleFunc("/block/latest", s.getLatestBlock).Methods("GET", "OPTIONS")
	api.HandleFunc("/validator/{address}", s.getValidator).Methods("GET", "OPTIONS")
	api.HandleFunc("/validators", s.getValidators).Methods("GET", "OPTIONS")
	api.HandleFunc("/validators/active", s.getActiveValidators).Methods("GET", "OPTIONS")
	api.HandleFunc("/status", s.getStatus).Methods("GET", "OPTIONS")
	api.HandleFunc("/health", s.getHealth).Methods("GET", "OPTIONS")
	api.HandleFunc("/estimate-gas", s.estimateGas).Methods("POST", "OPTIONS")
	api.HandleFunc("/transaction/broadcast", s.submitSignedTransaction).Methods("POST", "OPTIONS")

	// Enhanced CORS configuration
	c := cors.New(cors.Options{
		AllowedOrigins: []string{
			"http://localhost:3000",
			"http://localhost:5173",
			"http://localhost:8080",
			"http://127.0.0.1:5173",
			"http://127.0.0.1:3000",
			"*", // Allow all origins for development - REMOVE IN PRODUCTION
		},
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{
			"*",
			"Content-Type",
			"Authorization",
			"Accept",
			"Origin",
			"X-Requested-With",
			"Access-Control-Allow-Origin",
		},
		ExposedHeaders: []string{
			"Content-Length",
			"Access-Control-Allow-Origin",
		},
		AllowCredentials: true,
		MaxAge:           86400, // 24 hours
		Debug:            true,  // Enable for development debugging
	})

	// Apply CORS middleware to the entire router
	s.router.Use(c.Handler)
	s.router.Use(s.loggingMiddleware)
	s.router.Use(s.jsonMiddleware)

	// Add explicit OPTIONS handler for all routes
	s.router.Methods("OPTIONS").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("OPTIONS request to: %s", r.URL.Path)
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Accept, Origin, X-Requested-With")
		w.Header().Set("Access-Control-Max-Age", "86400")
		w.WriteHeader(http.StatusOK)
	})
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

	s.writeJSON(w, map[string]interface{}{
		"status":  "accepted",
		"tx_hash": tx.Hash,
	})
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

// UPDATED Account endpoints for account-based system

func (s *Server) getAccount(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	address := vars["address"]

	account, err := s.worldState.GetAccount(address)
	if err != nil {
		s.writeError(w, "Account not found", http.StatusNotFound)
		return
	}

	// Get additional staking information using existing methods
	stakedAmount := account.StakedAmount
	rewards := account.Rewards
	delegations := account.DelegatedTo
	if delegations == nil {
		delegations = make(map[string]int64)
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

func (s *Server) getAccountBalance(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	address := vars["address"]

	balance, err := s.worldState.GetBalance(address)
	if err != nil {
		s.writeError(w, "Account not found", http.StatusNotFound)
		return
	}

	nonce, _ := s.worldState.GetNonce(address)

	// Convert to THRYLOS (1 THRYLOS = 1e9 nano based on your BaseUnit)
	const NANO_PER_THRYLOS = 1000000000
	balanceThrylos := float64(balance) / NANO_PER_THRYLOS

	response := map[string]interface{}{
		"address":        address,
		"balance":        balance,
		"balanceThrylos": balanceThrylos,
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

	// Parse query parameters
	limitStr := r.URL.Query().Get("limit")
	limit := 50 // default limit
	if limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 && parsedLimit <= 1000 {
			limit = parsedLimit
		}
	}

	// Get transactions for this account from pending transactions
	var accountTxs []map[string]interface{}

	// Check pending transactions
	pendingTxs := s.worldState.GetPendingTransactions()
	for _, tx := range pendingTxs {
		if len(accountTxs) >= limit {
			break
		}

		if tx.From == address || tx.To == address {
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

	// TODO: Add confirmed transactions from blocks
	// You can implement this by scanning recent blocks or adding transaction indexing

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
	var req struct {
		Address string `json:"address"`
		Amount  int64  `json:"amount"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Address == "" || req.Amount <= 0 {
		s.writeError(w, "Invalid address or amount", http.StatusBadRequest)
		return
	}

	// Try to get existing account first
	account, err := s.worldState.GetAccount(req.Address)
	if err != nil {
		// Account doesn't exist, create a new one
		account = &core.Account{
			Address:      req.Address,
			Balance:      req.Amount, // Set initial balance
			Nonce:        0,
			StakedAmount: 0,
			DelegatedTo:  make(map[string]int64),
			Rewards:      0,
		}
	} else {
		// Account exists, add funding to existing balance
		account.Balance += req.Amount
	}

	// Use the proper WorldState method to update with storage persistence
	if err := s.worldState.UpdateAccountWithStorage(account); err != nil {
		s.writeError(w, fmt.Sprintf("Failed to create/update account: %v", err), http.StatusInternalServerError)
		return
	}

	// Convert to THRYLOS for display
	const NANO_PER_THRYLOS = 1000000000
	balanceThrylos := float64(account.Balance) / NANO_PER_THRYLOS
	amountThrylos := float64(req.Amount) / NANO_PER_THRYLOS

	response := map[string]interface{}{
		"message":         "Account funded successfully",
		"address":         req.Address,
		"amount_added":    req.Amount,
		"amount_thrylos":  amountThrylos,
		"new_balance":     account.Balance,
		"balance_thrylos": balanceThrylos,
		"nonce":           account.Nonce,
	}

	s.writeJSON(w, response)
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
	return map[string]interface{}{
		"address":         validator.Address,
		"stake":           validator.Stake,
		"self_stake":      validator.SelfStake,
		"delegated_stake": validator.DelegatedStake,
		"commission":      validator.Commission,
		"active":          validator.Active,
		"blocks_proposed": validator.BlocksProposed,
		"blocks_missed":   validator.BlocksMissed,
		"jail_until":      validator.JailUntil,
		"created_at":      validator.CreatedAt,
		"updated_at":      validator.UpdatedAt,
		"delegator_count": len(validator.Delegators),
	}
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
	TotalStaked           int64   `json:"total_staked"`
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
	Commission     float64 `json:"commission"`
	TotalStaked    int64   `json:"totalStaked"`
	Uptime         float64 `json:"uptime"`
	Status         string  `json:"status"`
	SelfStake      int64   `json:"self_stake"`
	DelegatorCount int     `json:"delegator_count"`
	BlocksProposed int64   `json:"blocks_proposed"`
	BlocksMissed   int64   `json:"blocks_missed"`
}

type DelegationHistoryItem struct {
	Validator string `json:"validator"`
	Amount    int64  `json:"amount"`
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
		TotalStaked:           totalStaked,
		AnnualPercentageYield: baseAPY,
		NextRewardTime:        &nextRewardTime,
		UnbondingPeriod:       21, // Days
		ActiveValidators:      activeValidatorCount,
		TotalDelegators:       totalDelegators,
		AverageCommission:     averageCommission,
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

		// Generate human-readable name
		name := fmt.Sprintf("Validator %s", validator.Address[:12])

		// Determine status
		status := "active"
		if validator.JailUntil > time.Now().Unix() {
			status = "jailed"
		} else if !validator.Active {
			status = "inactive"
		}

		validatorResponse := StakingValidatorResponse{
			Address:        validator.Address,
			Name:           name,
			Commission:     validator.Commission,
			TotalStaked:    validator.Stake,
			Uptime:         uptime,
			Status:         status,
			SelfStake:      validator.SelfStake,
			DelegatorCount: len(validator.Delegators),
			BlocksProposed: validator.BlocksProposed,
			BlocksMissed:   validator.BlocksMissed,
		}

		validators = append(validators, validatorResponse)
	}

	response := map[string]interface{}{
		"validators": validators,
		"count":      len(validators),
	}

	s.writeJSON(w, response)
}

// 3. Submit staking/delegation transaction using your existing executor
func (s *Server) submitStakeTransaction(w http.ResponseWriter, r *http.Request) {
	var tx core.Transaction
	if err := json.NewDecoder(r.Body).Decode(&tx); err != nil {
		s.writeError(w, "Invalid transaction format", http.StatusBadRequest)
		return
	}

	// Your executor handles DELEGATE transactions
	// Set the correct transaction type for your executor
	tx.Type = core.TransactionType_DELEGATE

	// Validate required fields
	if tx.From == "" || tx.To == "" || tx.Amount <= 0 {
		s.writeError(w, "Invalid staking transaction: missing required fields", http.StatusBadRequest)
		return
	}

	// Validate signature
	if len(tx.Signature) == 0 {
		s.writeError(w, "Transaction signature required", http.StatusBadRequest)
		return
	}

	// Use your existing validation and execution system
	if err := s.worldState.ValidateTransaction(&tx); err != nil {
		s.writeError(w, fmt.Sprintf("Transaction validation failed: %v", err), http.StatusBadRequest)
		return
	}

	// Add to transaction pool - your executor will handle it when block is created
	if err := s.worldState.AddTransaction(&tx); err != nil {
		s.writeError(w, fmt.Sprintf("Failed to submit staking transaction: %v", err), http.StatusBadRequest)
		return
	}

	response := map[string]interface{}{
		"status":    "accepted",
		"tx_hash":   tx.Hash,
		"message":   "Delegation transaction submitted successfully",
		"validator": tx.To,
		"amount":    tx.Amount,
	}

	s.writeJSON(w, response)
}

// 4. Submit unstaking transaction
func (s *Server) submitUnstakeTransaction(w http.ResponseWriter, r *http.Request) {
	var tx core.Transaction
	if err := json.NewDecoder(r.Body).Decode(&tx); err != nil {
		s.writeError(w, "Invalid transaction format", http.StatusBadRequest)
		return
	}

	// Set the correct transaction type for your executor
	tx.Type = core.TransactionType_UNDELEGATE

	// Validate required fields
	if tx.From == "" || tx.To == "" || tx.Amount <= 0 {
		s.writeError(w, "Invalid unstaking transaction: missing required fields", http.StatusBadRequest)
		return
	}

	// Validate signature
	if len(tx.Signature) == 0 {
		s.writeError(w, "Transaction signature required", http.StatusBadRequest)
		return
	}

	// Check if user has enough delegated amount
	account, err := s.worldState.GetAccount(tx.From)
	if err != nil {
		s.writeError(w, "Account not found", http.StatusNotFound)
		return
	}

	delegatedAmount := int64(0)
	if account.DelegatedTo != nil {
		if amount, exists := account.DelegatedTo[tx.To]; exists {
			delegatedAmount = amount
		}
	}

	if tx.Amount > delegatedAmount {
		s.writeError(w, fmt.Sprintf("Insufficient delegation: have %d, need %d",
			delegatedAmount, tx.Amount), http.StatusBadRequest)
		return
	}

	// Use your existing validation and execution system
	if err := s.worldState.ValidateTransaction(&tx); err != nil {
		s.writeError(w, fmt.Sprintf("Transaction validation failed: %v", err), http.StatusBadRequest)
		return
	}

	if err := s.worldState.AddTransaction(&tx); err != nil {
		s.writeError(w, fmt.Sprintf("Failed to submit unstaking transaction: %v", err), http.StatusBadRequest)
		return
	}

	response := map[string]interface{}{
		"status":           "accepted",
		"tx_hash":          tx.Hash,
		"message":          "Undelegation transaction submitted successfully",
		"validator":        tx.To,
		"amount":           tx.Amount,
		"unbonding_period": 0, // Your system uses liquid staking (immediate)
	}

	s.writeJSON(w, response)
}

// 5. Submit reward claiming transaction
func (s *Server) submitClaimTransaction(w http.ResponseWriter, r *http.Request) {
	var tx core.Transaction
	if err := json.NewDecoder(r.Body).Decode(&tx); err != nil {
		s.writeError(w, "Invalid transaction format", http.StatusBadRequest)
		return
	}

	// Set the correct transaction type for your executor
	tx.Type = core.TransactionType_CLAIM_REWARDS

	// For claim transactions, amount should be 0 (claims all available rewards)
	tx.Amount = 0
	tx.To = tx.From // Self-transaction for claiming

	// Validate required fields
	if tx.From == "" {
		s.writeError(w, "Invalid claim transaction: missing sender", http.StatusBadRequest)
		return
	}

	// Validate signature
	if len(tx.Signature) == 0 {
		s.writeError(w, "Transaction signature required", http.StatusBadRequest)
		return
	}

	// Check if user has rewards to claim
	account, err := s.worldState.GetAccount(tx.From)
	if err != nil {
		s.writeError(w, "Account not found", http.StatusNotFound)
		return
	}

	if account.Rewards <= 0 {
		s.writeError(w, "No rewards available to claim", http.StatusBadRequest)
		return
	}

	// Use your existing validation and execution system
	if err := s.worldState.ValidateTransaction(&tx); err != nil {
		s.writeError(w, fmt.Sprintf("Transaction validation failed: %v", err), http.StatusBadRequest)
		return
	}

	if err := s.worldState.AddTransaction(&tx); err != nil {
		s.writeError(w, fmt.Sprintf("Failed to submit claim transaction: %v", err), http.StatusBadRequest)
		return
	}

	response := map[string]interface{}{
		"status":         "accepted",
		"tx_hash":        tx.Hash,
		"message":        "Claim transaction submitted successfully",
		"claimed_amount": account.Rewards,
	}

	s.writeJSON(w, response)
}

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

	// Calculate estimated rewards based on current delegations
	estimatedDaily := float64(0)
	estimatedMonthly := float64(0)
	estimatedAnnual := float64(0)

	baseAPY := 8.5 / 100 // 8.5% annual yield

	if account.DelegatedTo != nil {
		for validatorAddr, stakedAmount := range account.DelegatedTo {
			validator, err := s.worldState.GetValidator(validatorAddr)
			if err != nil {
				continue
			}

			// Calculate rewards considering validator commission
			netAPY := baseAPY * (1 - validator.Commission/100)
			annualReward := float64(stakedAmount) * netAPY

			estimatedAnnual += annualReward
			estimatedMonthly += annualReward / 12
			estimatedDaily += annualReward / 365
		}
	}

	response := map[string]interface{}{
		"address":           address,
		"current_rewards":   account.Rewards,
		"estimated_daily":   int64(estimatedDaily),
		"estimated_monthly": int64(estimatedMonthly),
		"estimated_annual":  int64(estimatedAnnual),
		"delegations":       account.DelegatedTo,
		"staked_amount":     account.StakedAmount,
	}

	s.writeJSON(w, response)
}
