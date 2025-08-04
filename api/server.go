// api/server.go

// Main HTTP REST API server for blockchain data access

// Provides clean REST endpoints for wallets and applications to query blockchain state
// Handles account balances, transactions, blocks, validators, and system status
// Uses Gorilla Mux for routing, includes CORS support and logging middleware
// Designed for HTTP polling approach - simple, reliable, cacheable endpoints
// Serves as the primary interface between external applications and your blockchain node

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

// setupRoutes configures all API routes
func (s *Server) setupRoutes() {
	s.router = mux.NewRouter()

	// API version prefix
	api := s.router.PathPrefix("/api/v1").Subrouter()

	// Account endpoints
	api.HandleFunc("/account/{address}/balance", s.getAccountBalance).Methods("GET")
	api.HandleFunc("/account/{address}", s.getAccount).Methods("GET")
	api.HandleFunc("/account/{address}/transactions", s.getAccountTransactions).Methods("GET")
	api.HandleFunc("/account/{address}/delegations", s.getAccountDelegations).Methods("GET")

	// Transaction endpoints
	api.HandleFunc("/transaction/{hash}", s.getTransaction).Methods("GET")
	api.HandleFunc("/transactions/pending", s.getPendingTransactions).Methods("GET")

	// Block endpoints
	api.HandleFunc("/block/{hash}", s.getBlockByHash).Methods("GET")
	api.HandleFunc("/block/height/{height}", s.getBlockByHeight).Methods("GET")
	api.HandleFunc("/block/latest", s.getLatestBlock).Methods("GET")

	// Validator endpoints
	api.HandleFunc("/validator/{address}", s.getValidator).Methods("GET")
	api.HandleFunc("/validators", s.getValidators).Methods("GET")
	api.HandleFunc("/validators/active", s.getActiveValidators).Methods("GET")

	// Status endpoints
	api.HandleFunc("/status", s.getStatus).Methods("GET")
	api.HandleFunc("/health", s.getHealth).Methods("GET")

	// Add CORS middleware
	c := cors.New(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"*"},
	})

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

	log.Printf("🌐 API Server starting on port %d", s.port)
	log.Printf("📊 Health check: http://localhost:%d/api/v1/health", s.port)
	log.Printf("💰 Balance endpoint: http://localhost:%d/api/v1/account/{address}/balance", s.port)

	return s.server.ListenAndServe()
}

// Stop stops the HTTP server
func (s *Server) Stop() error {
	if s.server != nil {
		return s.server.Close()
	}
	return nil
}

// Account endpoints

func (s *Server) getAccountBalance(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	address := vars["address"]

	balance, err := s.worldState.GetBalance(address)
	if err != nil {
		s.writeError(w, "Account not found", http.StatusNotFound)
		return
	}

	nonce, _ := s.worldState.GetNonce(address)

	response := map[string]interface{}{
		"address": address,
		"balance": balance,
		"nonce":   nonce,
	}

	s.writeJSON(w, response)
}

func (s *Server) getAccount(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	address := vars["address"]

	account, err := s.worldState.GetAccount(address)
	if err != nil {
		s.writeError(w, "Account not found", http.StatusNotFound)
		return
	}

	response := map[string]interface{}{
		"address":       account.Address,
		"balance":       account.Balance,
		"nonce":         account.Nonce,
		"staked_amount": account.StakedAmount,
		"rewards":       account.Rewards,
		"delegated_to":  account.DelegatedTo,
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

	// Get recent transactions for the account
	// Note: This is a simplified implementation
	// In production, you'd want to index transactions by account for efficient queries
	pendingTxs := s.worldState.GetPendingTransactions()

	var accountTxs []map[string]interface{}
	count := 0

	for _, tx := range pendingTxs {
		if count >= limit {
			break
		}

		if tx.From == address || tx.To == address {
			txData := map[string]interface{}{
				"hash":      tx.Id,
				"from":      tx.From,
				"to":        tx.To,
				"amount":    tx.Amount,
				"gas":       tx.Gas,
				"gas_price": tx.GasPrice,
				"nonce":     tx.Nonce,
				"timestamp": tx.Timestamp,
				"status":    "pending",
			}
			accountTxs = append(accountTxs, txData)
			count++
		}
	}

	response := map[string]interface{}{
		"address":      address,
		"transactions": accountTxs,
		"count":        len(accountTxs),
		"limit":        limit,
	}

	s.writeJSON(w, response)
}

func (s *Server) getAccountDelegations(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	address := vars["address"]

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

// Transaction endpoints

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

	response := map[string]interface{}{
		"hash":      tx.Id,
		"from":      tx.From,
		"to":        tx.To,
		"amount":    tx.Amount,
		"gas":       tx.Gas,
		"gas_price": tx.GasPrice,
		"nonce":     tx.Nonce,
		"timestamp": tx.Timestamp,
		"signature": tx.Signature,
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

	var transactions []map[string]interface{}
	for i, tx := range pendingTxs {
		if i >= limit {
			break
		}

		txData := map[string]interface{}{
			"hash":      tx.Id,
			"from":      tx.From,
			"to":        tx.To,
			"amount":    tx.Amount,
			"gas":       tx.Gas,
			"gas_price": tx.GasPrice,
			"nonce":     tx.Nonce,
			"timestamp": tx.Timestamp,
		}
		transactions = append(transactions, txData)
	}

	response := map[string]interface{}{
		"transactions": transactions,
		"count":        len(transactions),
		"limit":        limit,
	}

	s.writeJSON(w, response)
}

// Block endpoints

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

// Validator endpoints

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

// Status endpoints

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

// Helper methods

func (s *Server) formatBlock(block *core.Block) map[string]interface{} {
	var transactions []map[string]interface{}
	for _, tx := range block.Transactions {
		txData := map[string]interface{}{
			"hash":      tx.Id,
			"from":      tx.From,
			"to":        tx.To,
			"amount":    tx.Amount,
			"gas":       tx.Gas,
			"gas_price": tx.GasPrice,
			"nonce":     tx.Nonce,
		}
		transactions = append(transactions, txData)
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

// Middleware

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
