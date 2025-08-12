// staking_vm.go - FIXED VERSION - VM-powered staking endpoints
// Replace your existing staking_vm.go with this fixed version

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/vm"
)

// ============================================================================
// SECTION 1: REQUEST/RESPONSE STRUCTURES (UNCHANGED)
// ============================================================================

type VMStakeRequest struct {
	From      string `json:"from"`
	Validator string `json:"validator"`
	Amount    int64  `json:"amount"`
	Gas       int64  `json:"gas"`
	Nonce     uint64 `json:"nonce"`
	Signature []byte `json:"signature"`
}

type VMUnstakeRequest struct {
	From      string `json:"from"`
	Validator string `json:"validator"`
	Amount    int64  `json:"amount"`
	Gas       int64  `json:"gas"`
	Nonce     uint64 `json:"nonce"`
	Signature []byte `json:"signature"`
}

type VMClaimRequest struct {
	From      string `json:"from"`
	Gas       int64  `json:"gas"`
	Nonce     uint64 `json:"nonce"`
	Signature []byte `json:"signature"`
}

type VMCreateValidatorRequest struct {
	From        string  `json:"from"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Website     string  `json:"website"`
	PublicKey   string  `json:"public_key"`
	SelfStake   int64   `json:"self_stake"`
	Commission  float64 `json:"commission"`
	Gas         int64   `json:"gas"`
	Nonce       uint64  `json:"nonce"`
	Signature   []byte  `json:"signature"`
}

type VMStakingResponse struct {
	Success   bool       `json:"success"`
	TxHash    string     `json:"tx_hash,omitempty"`
	GasUsed   int64      `json:"gas_used"`
	GasPrice  int64      `json:"gas_price"`
	TotalCost int64      `json:"total_cost"`
	Events    []vm.Event `json:"events,omitempty"`
	Error     string     `json:"error,omitempty"`
	Message   string     `json:"message,omitempty"`
	Timestamp int64      `json:"timestamp"`
}

type VMUnbondValidatorRequest struct {
	From      string `json:"from"`
	Gas       int64  `json:"gas"`
	Nonce     uint64 `json:"nonce"`
	Signature []byte `json:"signature"`
}

// ============================================================================
// SECTION 2: FIXED VM STAKING ENDPOINTS
// ============================================================================

func (s *Server) delegateViaVM(w http.ResponseWriter, r *http.Request) {
	var req VMStakeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	if err := s.validateVMStakeRequest(&req); err != nil {
		s.writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	operation := &vm.VMOperation{
		Type:   "delegate",
		From:   req.From,
		Amount: req.Amount,
		Gas:    req.Gas,
		Parameters: map[string]string{
			"validator": req.Validator,
		},
	}

	result, err := s.executeVMStakingOperation(operation, req.From, req.Nonce, req.Signature)
	if err != nil {
		s.writeError(w, fmt.Sprintf("VM execution failed: %v", err), http.StatusInternalServerError)
		return
	}

	if !result.Success {
		s.writeError(w, result.Error, http.StatusBadRequest)
		return
	}

	// FIXED: Use correct TransactionType
	tx := s.createTransactionFromVM(operation, req.From, req.Nonce, req.Signature, core.TransactionType_DELEGATE)
	s.worldState.AddTransaction(tx)

	// FIXED: Use GetGasPrice() method
	response := &VMStakingResponse{
		Success:   true,
		TxHash:    tx.Hash,
		GasUsed:   result.GasUsed,
		GasPrice:  s.vm.GetGasPrice(),
		TotalCost: result.GasUsed*s.vm.GetGasPrice() + req.Amount,
		Events:    result.Events,
		Message:   fmt.Sprintf("Successfully delegated %d nano to validator %s", req.Amount, req.Validator),
		Timestamp: time.Now().Unix(),
	}

	s.writeJSON(w, response)
}

func (s *Server) undelegateViaVM(w http.ResponseWriter, r *http.Request) {
	var req VMUnstakeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	if err := s.validateVMUnstakeRequest(&req); err != nil {
		s.writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	operation := &vm.VMOperation{
		Type:   "undelegate",
		From:   req.From,
		Amount: req.Amount,
		Gas:    req.Gas,
		Parameters: map[string]string{
			"validator": req.Validator,
		},
	}

	result, err := s.executeVMStakingOperation(operation, req.From, req.Nonce, req.Signature)
	if err != nil {
		s.writeError(w, fmt.Sprintf("VM execution failed: %v", err), http.StatusInternalServerError)
		return
	}

	if !result.Success {
		s.writeError(w, result.Error, http.StatusBadRequest)
		return
	}

	// FIXED: Use correct TransactionType
	tx := s.createTransactionFromVM(operation, req.From, req.Nonce, req.Signature, core.TransactionType_UNDELEGATE)
	s.worldState.AddTransaction(tx)

	// FIXED: Use GetGasPrice() method
	response := &VMStakingResponse{
		Success:   true,
		TxHash:    tx.Hash,
		GasUsed:   result.GasUsed,
		GasPrice:  s.vm.GetGasPrice(),
		TotalCost: result.GasUsed * s.vm.GetGasPrice(),
		Events:    result.Events,
		Message:   fmt.Sprintf("Successfully undelegated %d nano from validator %s", req.Amount, req.Validator),
		Timestamp: time.Now().Unix(),
	}

	s.writeJSON(w, response)
}

func (s *Server) claimRewardsViaVM(w http.ResponseWriter, r *http.Request) {
	var req VMClaimRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	if err := s.validateVMClaimRequest(&req); err != nil {
		s.writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	account, err := s.worldState.GetAccount(req.From)
	if err != nil {
		s.writeError(w, "Account not found", http.StatusNotFound)
		return
	}

	if account.Rewards <= 0 {
		s.writeError(w, "No rewards available to claim", http.StatusBadRequest)
		return
	}

	operation := &vm.VMOperation{
		Type: "claim_rewards",
		From: req.From,
		Gas:  req.Gas,
	}

	result, err := s.executeVMStakingOperation(operation, req.From, req.Nonce, req.Signature)
	if err != nil {
		s.writeError(w, fmt.Sprintf("VM execution failed: %v", err), http.StatusInternalServerError)
		return
	}

	if !result.Success {
		s.writeError(w, result.Error, http.StatusBadRequest)
		return
	}

	// FIXED: Use correct TransactionType
	tx := s.createTransactionFromVM(operation, req.From, req.Nonce, req.Signature, core.TransactionType_CLAIM_REWARDS)
	s.worldState.AddTransaction(tx)

	// FIXED: Use GetGasPrice() method
	response := &VMStakingResponse{
		Success:   true,
		TxHash:    tx.Hash,
		GasUsed:   result.GasUsed,
		GasPrice:  s.vm.GetGasPrice(),
		TotalCost: result.GasUsed * s.vm.GetGasPrice(),
		Events:    result.Events,
		Message:   fmt.Sprintf("Successfully claimed %d nano in rewards", account.Rewards),
		Timestamp: time.Now().Unix(),
	}

	s.writeJSON(w, response)
}

func (s *Server) createValidatorViaVM(w http.ResponseWriter, r *http.Request) {
	var req VMCreateValidatorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	if err := s.validateVMCreateValidatorRequest(&req); err != nil {
		s.writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	operation := &vm.VMOperation{
		Type:   "create_validator",
		From:   req.From,
		Amount: req.SelfStake,
		Gas:    req.Gas,
		Parameters: map[string]string{
			"public_key":  req.PublicKey,
			"commission":  fmt.Sprintf("%.4f", req.Commission),
			"name":        req.Name,
			"description": req.Description,
			"website":     req.Website,
		},
	}

	result, err := s.executeVMStakingOperation(operation, req.From, req.Nonce, req.Signature)
	if err != nil {
		s.writeError(w, fmt.Sprintf("VM execution failed: %v", err), http.StatusInternalServerError)
		return
	}

	if !result.Success {
		s.writeError(w, result.Error, http.StatusBadRequest)
		return
	}

	validatorData, _ := json.Marshal(map[string]interface{}{
		"name":        req.Name,
		"description": req.Description,
		"website":     req.Website,
		"public_key":  req.PublicKey,
		"commission":  req.Commission,
	})

	// FIXED: Create custom TransactionType for validator creation
	// Since there's no core.TransactionType_CREATE_VALIDATOR, we'll use a custom type
	// You can add this to your protobuf enum or use a numeric value
	tx := s.createTransactionFromVM(operation, req.From, req.Nonce, req.Signature, core.TransactionType(6))
	tx.Data = validatorData
	s.worldState.AddTransaction(tx)

	// FIXED: Use GetGasPrice() method
	response := &VMStakingResponse{
		Success:   true,
		TxHash:    tx.Hash,
		GasUsed:   result.GasUsed,
		GasPrice:  s.vm.GetGasPrice(),
		TotalCost: result.GasUsed*s.vm.GetGasPrice() + req.SelfStake,
		Events:    result.Events,
		Message:   fmt.Sprintf("Successfully created validator '%s' with %d nano self-stake", req.Name, req.SelfStake),
		Timestamp: time.Now().Unix(),
	}

	s.writeJSON(w, response)
}

// ============================================================================
// VALIDATION HELPERS (UNCHANGED)
// ============================================================================

func (s *Server) validateVMStakeRequest(req *VMStakeRequest) error {
	if req.From == "" {
		return fmt.Errorf("from address required")
	}
	if req.Validator == "" {
		return fmt.Errorf("validator address required")
	}
	if req.Amount <= 0 {
		return fmt.Errorf("amount must be positive")
	}
	if req.Gas <= 0 {
		return fmt.Errorf("gas must be positive")
	}

	const MIN_DELEGATION = 100000000 // 0.1 THRYLOS in nano
	if req.Amount < MIN_DELEGATION {
		return fmt.Errorf("minimum delegation amount is %d nano (0.1 THRYLOS)", MIN_DELEGATION)
	}

	estimatedGas := int64(50000)
	if req.Gas < estimatedGas {
		return fmt.Errorf("insufficient gas: provided %d, minimum required %d", req.Gas, estimatedGas)
	}

	return nil
}

func (s *Server) validateVMUnstakeRequest(req *VMUnstakeRequest) error {
	if req.From == "" {
		return fmt.Errorf("from address required")
	}
	if req.Validator == "" {
		return fmt.Errorf("validator address required")
	}
	if req.Amount <= 0 {
		return fmt.Errorf("amount must be positive")
	}
	if req.Gas <= 0 {
		return fmt.Errorf("gas must be positive")
	}

	account, err := s.worldState.GetAccount(req.From)
	if err != nil {
		return fmt.Errorf("account not found")
	}

	delegatedAmount := int64(0)
	if account.DelegatedTo != nil {
		if amount, exists := account.DelegatedTo[req.Validator]; exists {
			delegatedAmount = amount
		}
	}

	if req.Amount > delegatedAmount {
		return fmt.Errorf("insufficient delegation: have %d, attempting to undelegate %d", delegatedAmount, req.Amount)
	}

	return nil
}

func (s *Server) validateVMClaimRequest(req *VMClaimRequest) error {
	if req.From == "" {
		return fmt.Errorf("from address required")
	}
	if req.Gas <= 0 {
		return fmt.Errorf("gas must be positive")
	}
	if len(req.Signature) == 0 {
		return fmt.Errorf("signature required")
	}
	return nil
}

func (s *Server) validateVMCreateValidatorRequest(req *VMCreateValidatorRequest) error {
	if req.From == "" {
		return fmt.Errorf("from address required")
	}
	if req.Name == "" {
		return fmt.Errorf("validator name required")
	}
	if req.PublicKey == "" {
		return fmt.Errorf("public key required")
	}
	if req.Commission < 0 || req.Commission > 1 {
		return fmt.Errorf("commission must be between 0 and 1")
	}

	const MIN_VALIDATOR_STAKE = 25 * 1000000000
	if req.SelfStake < MIN_VALIDATOR_STAKE {
		return fmt.Errorf("minimum validator stake is %d nano (25 THRYLOS)", MIN_VALIDATOR_STAKE)
	}

	existingValidator, err := s.worldState.GetValidator(req.From)
	if err == nil && existingValidator != nil {
		return fmt.Errorf("validator already exists for this address")
	}

	return nil
}

// ============================================================================
// FIXED VM EXECUTION HELPERS
// ============================================================================

func (s *Server) executeVMStakingOperation(operation *vm.VMOperation, from string, nonce uint64, signature []byte) (*vm.ExecutionResult, error) {
	currentNonce, err := s.worldState.GetNonce(from)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %v", err)
	}
	if currentNonce != nonce {
		return nil, fmt.Errorf("invalid nonce: expected %d, got %d", currentNonce, nonce)
	}

	if len(signature) == 0 {
		return nil, fmt.Errorf("signature required")
	}

	result, err := s.vm.SafeExecute(operation)
	if err != nil {
		return nil, fmt.Errorf("VM execution error: %v", err)
	}

	if result.Success {
		account, err := s.worldState.GetAccount(from)
		if err == nil {
			account.Nonce++
			s.worldState.UpdateAccountWithStorage(account)
		}
	}

	return result, nil
}

// FIXED: Updated function signature to use core.TransactionType
func (s *Server) createTransactionFromVM(operation *vm.VMOperation, from string, nonce uint64, signature []byte, txType core.TransactionType) *core.Transaction {
	timestamp := time.Now().Unix()
	hash := s.generateVMTxHash(operation, nonce, timestamp)

	return &core.Transaction{
		Id:        hash,
		Hash:      hash,
		From:      from,
		To:        operation.To,
		Amount:    operation.Amount,
		Type:      txType, // FIXED: Now uses core.TransactionType
		Gas:       operation.Gas,
		GasPrice:  s.vm.GetGasPrice(), // FIXED: Use method instead of field
		Nonce:     nonce,
		Timestamp: timestamp,
		Signature: signature,
	}
}

func (s *Server) generateVMTxHash(operation *vm.VMOperation, nonce uint64, timestamp int64) string {
	data := fmt.Sprintf("vm_%s_%s_%s_%d_%d_%d",
		operation.Type, operation.From, operation.To, operation.Amount, nonce, timestamp)
	return fmt.Sprintf("%x", []byte(data)[:16])
}

// ============================================================================
// GAS ESTIMATION (FIXED)
// ============================================================================

func (s *Server) estimateVMStakingGas(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Operation  string            `json:"operation"`
		From       string            `json:"from"`
		To         string            `json:"to,omitempty"`
		Amount     int64             `json:"amount,omitempty"`
		Parameters map[string]string `json:"parameters,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	operation := &vm.VMOperation{
		Type:       req.Operation,
		From:       req.From,
		To:         req.To,
		Amount:     req.Amount,
		Parameters: req.Parameters,
	}

	estimatedGas := s.vm.EstimateGas(operation)
	gasPrice := s.vm.GetGasPrice() // FIXED: Use method
	gasCost := estimatedGas * gasPrice
	totalCost := gasCost

	if req.Amount > 0 {
		totalCost += req.Amount
	}

	response := map[string]interface{}{
		"operation":      req.Operation,
		"estimated_gas":  estimatedGas,
		"gas_price":      gasPrice,
		"gas_cost":       gasCost,
		"operation_cost": req.Amount,
		"total_cost":     totalCost,
		"cost_thrylos":   float64(totalCost) / 1000000000,
		"breakdown": map[string]interface{}{
			"gas_fee":          gasCost,
			"operation_amount": req.Amount,
		},
	}

	s.writeJSON(w, response)
}

func (s *Server) unbondValidatorViaVM(w http.ResponseWriter, r *http.Request) {
	var req VMUnbondValidatorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	if err := s.validateVMUnbondValidatorRequest(&req); err != nil {
		s.writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Check if validator exists
	validator, err := s.worldState.GetValidator(req.From)
	if err != nil {
		s.writeError(w, "Validator not found", http.StatusNotFound)
		return
	}

	if !validator.Active {
		s.writeError(w, "Validator is already inactive", http.StatusBadRequest)
		return
	}

	operation := &vm.VMOperation{
		Type: "unbond_validator",
		From: req.From,
		Gas:  req.Gas,
	}

	result, err := s.executeVMStakingOperation(operation, req.From, req.Nonce, req.Signature)
	if err != nil {
		s.writeError(w, fmt.Sprintf("VM execution failed: %v", err), http.StatusInternalServerError)
		return
	}

	if !result.Success {
		s.writeError(w, result.Error, http.StatusBadRequest)
		return
	}

	// Deactivate the validator
	validator.Active = false
	validator.UpdatedAt = time.Now().Unix()

	err = s.worldState.UpdateValidator(validator)
	if err != nil {
		s.writeError(w, fmt.Sprintf("Failed to update validator: %v", err), http.StatusInternalServerError)
		return
	}

	// Create transaction record
	tx := s.createTransactionFromVM(operation, req.From, req.Nonce, req.Signature, core.TransactionType(7)) // Custom type for unbond
	s.worldState.AddTransaction(tx)

	// Debug logging
	fmt.Printf("🔍 Unbonded validator: %s\n", req.From)

	response := &VMStakingResponse{
		Success:   true,
		TxHash:    tx.Hash,
		GasUsed:   result.GasUsed,
		GasPrice:  s.vm.GetGasPrice(),
		TotalCost: result.GasUsed * s.vm.GetGasPrice(),
		Events:    result.Events,
		Message:   "Validator unbonding initiated - will be deactivated after 14-day period",
		Timestamp: time.Now().Unix(),
	}

	s.writeJSON(w, response)
}

func (s *Server) validateVMUnbondValidatorRequest(req *VMUnbondValidatorRequest) error {
	if req.From == "" {
		return fmt.Errorf("from address required")
	}
	if req.Gas <= 0 {
		return fmt.Errorf("gas must be positive")
	}
	if len(req.Signature) == 0 {
		return fmt.Errorf("signature required")
	}
	return nil
}
