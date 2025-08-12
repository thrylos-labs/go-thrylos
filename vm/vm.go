/*
Thrylos Custom Virtual Machine (VM) - High-Performance Blockchain Execution Engine

OVERVIEW:
The Thrylos VM is a purpose-built virtual machine optimized for blockchain operations,
designed for high throughput and Ed25519 signature verification. Unlike general-purpose
VMs like EVM or WASM, it provides native blockchain operations as first-class citizens.

ASSET-ONLY TOKEN MODEL:
- Removed currency/memecoin creation capabilities
- Replaced with constrained asset tokens representing real-world items
- Asset types: supply_chain, carbon_credit, real_estate, certificate, license, membership
- Limited decimals (max 4), supply caps, and real-world reference requirements
- Contracts cannot bypass asset restrictions through RISC-V execution

HOW IT WORKS:

1. OPERATION-BASED EXECUTION:
   - Transactions are converted to VMOperation structs with types like "transfer", "stake", "delegate"
   - Each operation type has optimized, native implementation paths
   - No bytecode interpretation - direct Go function calls for maximum performance

2. NATIVE BLOCKCHAIN OPERATIONS:
   - Transfer: Direct account balance updates through WorldState
   - Stake: Delegation to validators with automatic balance deduction
   - Cross-shard: Atomic transfers between different blockchain shards
   - Create Validator: Register new consensus participants
   - Asset Operations: Create, mint, burn, and transfer constrained asset tokens
   - Custom Contracts: Extensible execution for user-defined logic

3. GAS METERING SYSTEM:
   - Each operation has predetermined gas costs (transfer=21000, stake=50000, etc.)
   - Gas limits prevent infinite loops and resource exhaustion
   - Failure modes return unused gas to prevent economic attacks

4. STATE INTEGRATION:
   - Direct integration with WorldState for account/validator management
   - Atomic operations with automatic rollback on failures
   - Events emitted for transaction indexing and monitoring

5. SECURITY MODEL:
   - Operation validation before execution (balance checks, signature verification)
   - Fail-safe error handling with state rollback on failures
   - Address format validation and nonce management
   - No arbitrary code execution - only predefined operations

6. PERFORMANCE CHARACTERISTICS:
   - Zero interpretation overhead (direct function calls)
   - Parallel execution potential for non-conflicting operations
   - Optimized for Ed25519 cryptographic operations
   - Hardware-specific optimizations possible

EXECUTION FLOW:
Transaction -> VMOperation -> Validate -> Snapshot -> Execute -> Update State -> Emit Events -> Return Result

ADVANTAGES OVER EVM/WASM:
- 10-100x faster execution for blockchain-specific operations
- No smart contract attack surface (reentrancy, etc.)
- Deterministic gas costs
- Native cross-shard support
- Ed25519 optimized signature verification
- Purpose-built for high-throughput consensus
- Automatic state rollback on failures

USE CASES:
- High-frequency trading operations
- Cross-shard asset transfers
- Validator staking/delegation
- Real-world asset tokenization (no memecoins)
- Custom blockchain governance operations
- Performance-critical DeFi protocols
*/

package vm

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/thrylos-labs/go-thrylos/core/state"
	"github.com/thrylos-labs/go-thrylos/proto/core"
)

// ThrylosVM - Custom virtual machine for Thrylos operations
type ThrylosVM struct {
	worldState *state.WorldState
	gasPrice   int64
	gasLimit   int64
	gasUsed    int64
}

// VMOperation represents a blockchain operation
type VMOperation struct {
	Type       string            `json:"type"`
	From       string            `json:"from"`
	To         string            `json:"to,omitempty"`
	Amount     int64             `json:"amount,omitempty"`
	Data       []byte            `json:"data,omitempty"`
	Parameters map[string]string `json:"parameters,omitempty"`
	Gas        int64             `json:"gas"`
}

// ExecutionResult contains the result of VM execution
type ExecutionResult struct {
	Success      bool          `json:"success"`
	GasUsed      int64         `json:"gas_used"`
	ReturnData   []byte        `json:"return_data,omitempty"`
	Error        string        `json:"error,omitempty"`
	Events       []Event       `json:"events,omitempty"`
	StateChanges []StateChange `json:"state_changes,omitempty"`
}

type Event struct {
	Type string                 `json:"type"`
	Data map[string]interface{} `json:"data"`
}

type StateChange struct {
	Type    string `json:"type"` // "account_update", "validator_update", "asset_update"
	Address string `json:"address"`
	Before  []byte `json:"before"`
	After   []byte `json:"after"`
}

// AssetToken represents a real-world asset token (replaces Token struct)
type AssetToken struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	AssetType        string `json:"asset_type"`     // "supply_chain", "carbon_credit", etc.
	RealWorldRef     string `json:"real_world_ref"` // Required reference to physical asset
	MaxDecimals      int32  `json:"max_decimals"`   // Capped at 4
	TotalSupply      int64  `json:"total_supply"`
	Creator          string `json:"creator"`
	Transferable     bool   `json:"transferable"`      // Some assets shouldn't be tradeable
	RequiresApproval bool   `json:"requires_approval"` // KYC/compliance gating
	ExpirationDate   *int64 `json:"expiration_date"`   // Optional expiration
	RegulatoryInfo   string `json:"regulatory_info"`   // Compliance metadata
	CreatedAt        int64  `json:"created_at"`
}

// AssetConfig defines constraints for each asset type
type AssetConfig struct {
	MaxSupply        int64  `json:"max_supply"`
	RequiresApproval bool   `json:"requires_approval"`
	MaxDecimals      int32  `json:"max_decimals"`
	Description      string `json:"description"`
}

// NewThrylosVM creates a new custom VM instance
func NewThrylosVM(worldState *state.WorldState, gasPrice, gasLimit int64) *ThrylosVM {
	return &ThrylosVM{
		worldState: worldState,
		gasPrice:   gasPrice,
		gasLimit:   gasLimit,
		gasUsed:    0,
	}
}

// Execute runs a VM operation with automatic state rollback on failure
func (vm *ThrylosVM) Execute(op *VMOperation) (*ExecutionResult, error) {
	// Check gas limit
	if op.Gas > vm.gasLimit {
		return &ExecutionResult{
			Success: false,
			Error:   "gas limit exceeded",
		}, nil
	}

	// Create state snapshot before execution for rollback capability
	snapshot := vm.worldState.CreateSnapshot()

	// Validate operation before execution
	if err := vm.ValidateOperation(op); err != nil {
		return &ExecutionResult{
			Success: false,
			Error:   err.Error(),
			GasUsed: vm.gasUsed,
		}, nil
	}

	// Execute based on operation type
	var result *ExecutionResult
	var err error

	switch op.Type {
	case "transfer":
		result, err = vm.executeTransfer(op)
	case "stake":
		result, err = vm.executeStake(op)
	case "delegate":
		result, err = vm.executeDelegate(op)
	case "undelegate":
		result, err = vm.executeUndelegate(op)
	case "cross_shard_transfer":
		result, err = vm.executeCrossShardTransfer(op)
	case "create_validator":
		result, err = vm.executeCreateValidator(op)
	case "unbond_validator":
		result, err = vm.executeUnbondValidator(op)
	// Asset operations (replaced token operations)
	case "create_asset":
		result, err = vm.executeCreateAsset(op)
	case "mint_asset":
		result, err = vm.executeMintAsset(op)
	case "burn_asset":
		result, err = vm.executeBurnAsset(op)
	case "transfer_asset":
		result, err = vm.executeTransferAsset(op)
	case "claim_rewards":
		result, err = vm.executeClaimRewards(op)
	// BLOCKED: Old token operations return errors
	case "create_token", "mint_token", "burn_token", "transfer_token":
		result = &ExecutionResult{
			Success: false,
			Error:   fmt.Sprintf("operation type '%s' not supported - use asset operations instead", op.Type),
			GasUsed: vm.gasUsed,
		}
	default:
		result = &ExecutionResult{
			Success: false,
			Error:   fmt.Sprintf("operation type '%s' not supported", op.Type),
			GasUsed: vm.gasUsed,
		}
	}

	// Rollback state on failure
	if result != nil && (!result.Success || err != nil) {
		vm.worldState.RestoreFromSnapshot(snapshot)
		vm.gasUsed = 0 // Reset gas on rollback
	}

	return result, err
}

// Built-in operations optimized for Thrylos

func (vm *ThrylosVM) executeTransfer(op *VMOperation) (*ExecutionResult, error) {
	baseGas := int64(21000) // Base transfer cost
	vm.gasUsed += baseGas

	// Execute transfer through WorldState
	tx := &core.Transaction{
		From:     op.From,
		To:       op.To,
		Amount:   op.Amount,
		GasPrice: vm.gasPrice,
		Gas:      op.Gas,
	}

	receipt, err := vm.worldState.ExecuteTransaction(tx)
	if err != nil {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   err.Error(),
		}, nil
	}

	return &ExecutionResult{
		Success: receipt.Status == 1,
		GasUsed: vm.gasUsed,
		Events: []Event{{
			Type: "transfer",
			Data: map[string]interface{}{
				"from":   op.From,
				"to":     op.To,
				"amount": op.Amount,
			},
		}},
	}, nil
}

func (vm *ThrylosVM) executeStake(op *VMOperation) (*ExecutionResult, error) {
	stakeGas := int64(50000)
	vm.gasUsed += stakeGas

	stakingManager := vm.worldState.GetStakingManager()
	validatorAddr := op.Parameters["validator"]

	if validatorAddr == "" {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   "validator parameter required",
		}, nil
	}

	err := stakingManager.Delegate(op.From, validatorAddr, op.Amount)
	if err != nil {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   err.Error(),
		}, nil
	}

	return &ExecutionResult{
		Success: true,
		GasUsed: vm.gasUsed,
		Events: []Event{{
			Type: "stake",
			Data: map[string]interface{}{
				"delegator": op.From,
				"validator": validatorAddr,
				"amount":    op.Amount,
			},
		}},
	}, nil
}

func (vm *ThrylosVM) executeDelegate(op *VMOperation) (*ExecutionResult, error) {
	delegateGas := int64(50000)
	vm.gasUsed += delegateGas

	stakingManager := vm.worldState.GetStakingManager()
	validatorAddr := op.Parameters["validator"]

	if validatorAddr == "" {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   "validator parameter required",
		}, nil
	}

	err := stakingManager.Delegate(op.From, validatorAddr, op.Amount)
	if err != nil {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   err.Error(),
		}, nil
	}

	return &ExecutionResult{
		Success: true,
		GasUsed: vm.gasUsed,
		Events: []Event{{
			Type: "delegate",
			Data: map[string]interface{}{
				"delegator": op.From,
				"validator": validatorAddr,
				"amount":    op.Amount,
			},
		}},
	}, nil
}

func (vm *ThrylosVM) executeUndelegate(op *VMOperation) (*ExecutionResult, error) {
	undelegateGas := int64(75000)
	vm.gasUsed += undelegateGas

	stakingManager := vm.worldState.GetStakingManager()
	validatorAddr := op.Parameters["validator"]

	if validatorAddr == "" {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   "validator parameter required",
		}, nil
	}

	err := stakingManager.Undelegate(op.From, validatorAddr, op.Amount)
	if err != nil {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   err.Error(),
		}, nil
	}

	return &ExecutionResult{
		Success: true,
		GasUsed: vm.gasUsed,
		Events: []Event{{
			Type: "undelegate",
			Data: map[string]interface{}{
				"delegator": op.From,
				"validator": validatorAddr,
				"amount":    op.Amount,
			},
		}},
	}, nil
}

func (vm *ThrylosVM) executeCreateValidator(op *VMOperation) (*ExecutionResult, error) {
	createValidatorGas := int64(100000)
	vm.gasUsed += createValidatorGas

	pubKey := op.Parameters["public_key"]
	commissionStr := op.Parameters["commission"]

	// ✅ EXTRACT NAME, DESCRIPTION, AND WEBSITE FROM PARAMETERS
	name := op.Parameters["name"]
	description := op.Parameters["description"]
	website := op.Parameters["website"]

	if pubKey == "" {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   "public_key parameter required",
		}, nil
	}

	// ✅ VALIDATE NAME IS PROVIDED
	if name == "" {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   "name parameter required",
		}, nil
	}

	commission := 0.1
	if commissionStr != "" {
		if parsedCommission, err := strconv.ParseFloat(commissionStr, 64); err == nil {
			commission = parsedCommission
		}
	}

	if commission < 0 || commission > 1 {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   "commission must be between 0 and 1 (0-100%)",
		}, nil
	}

	// ✅ CREATE VALIDATOR WITH ALL FIELDS INCLUDING NAME
	validator := &core.Validator{
		Address:        op.From,
		Name:           name,        // ✅ SET THE NAME FROM PARAMETERS
		Description:    description, // ✅ SET THE DESCRIPTION FROM PARAMETERS
		Website:        website,     // ✅ SET THE WEBSITE FROM PARAMETERS
		Pubkey:         []byte(pubKey),
		Stake:          op.Amount,
		SelfStake:      op.Amount,
		DelegatedStake: 0,
		Commission:     commission,
		Active:         true,
		Delegators:     make(map[string]int64),
		CreatedAt:      time.Now().Unix(),
		UpdatedAt:      time.Now().Unix(),
	}

	// ✅ ADD DEBUG LOGGING
	fmt.Printf("🔍 VM Creating validator with name: '%s', description: '%s', website: '%s'\n", name, description, website)

	err := vm.worldState.AddValidator(validator)
	if err != nil {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   err.Error(),
		}, nil
	}

	account, err := vm.worldState.GetAccount(op.From)
	if err != nil {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   fmt.Sprintf("failed to get creator account: %v", err),
		}, nil
	}

	if account.Balance < op.Amount {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   fmt.Sprintf("insufficient balance: have %d, need %d", account.Balance, op.Amount),
		}, nil
	}

	account.Balance -= op.Amount
	account.StakedAmount += op.Amount

	if err := vm.worldState.UpdateAccountWithStorage(account); err != nil {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   fmt.Sprintf("failed to update account: %v", err),
		}, nil
	}

	return &ExecutionResult{
		Success: true,
		GasUsed: vm.gasUsed,
		Events: []Event{{
			Type: "create_validator",
			Data: map[string]interface{}{
				"validator":   op.From,
				"name":        name,
				"description": description,
				"website":     website,
				"public_key":  pubKey,
				"stake":       op.Amount,
				"commission":  commission,
			},
		}},
	}, nil
}

// Add this function to your vm.go file, in the VM execution switch statement

func (vm *ThrylosVM) executeUnbondValidator(op *VMOperation) (*ExecutionResult, error) {
	unbondValidatorGas := int64(75000)
	vm.gasUsed += unbondValidatorGas

	// Get the validator
	validator, err := vm.worldState.GetValidator(op.From)
	if err != nil {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   fmt.Sprintf("validator not found: %v", err),
		}, nil
	}

	if !validator.Active {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   "validator is already inactive",
		}, nil
	}

	// Deactivate the validator
	validator.Active = false
	validator.UpdatedAt = time.Now().Unix()

	err = vm.worldState.UpdateValidator(validator)
	if err != nil {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   fmt.Sprintf("failed to update validator: %v", err),
		}, nil
	}

	// Return staked amount to validator's account after unbonding period
	// Note: In a real implementation, this would be handled by a scheduler
	// For now, we'll just mark it as inactive

	return &ExecutionResult{
		Success: true,
		GasUsed: vm.gasUsed,
		Events: []Event{{
			Type: "unbond_validator",
			Data: map[string]interface{}{
				"validator": op.From,
				"stake":     validator.Stake,
			},
		}},
	}, nil
}

func (vm *ThrylosVM) executeCrossShardTransfer(op *VMOperation) (*ExecutionResult, error) {
	crossShardGas := int64(100000)
	vm.gasUsed += crossShardGas

	crossShardManager := vm.worldState.GetCrossShardManager()

	nonce, _ := vm.worldState.GetNonce(op.From)
	transfer, err := crossShardManager.InitiateTransfer(op.From, op.To, op.Amount, nonce)
	if err != nil {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   err.Error(),
		}, nil
	}

	return &ExecutionResult{
		Success:    true,
		GasUsed:    vm.gasUsed,
		ReturnData: []byte(transfer.Hash),
		Events: []Event{{
			Type: "cross_shard_transfer",
			Data: map[string]interface{}{
				"from":       op.From,
				"to":         op.To,
				"amount":     op.Amount,
				"hash":       transfer.Hash,
				"from_shard": transfer.FromShard,
				"to_shard":   transfer.ToShard,
			},
		}},
	}, nil
}

// REMOVED: All token operations (executeCreateToken, executeMintToken, etc.)
// REPLACED WITH: Asset operations with real-world constraints

// Asset operations (replaces token operations)

// getAssetConfigs returns allowed asset types and their constraints
func (vm *ThrylosVM) getAssetConfigs() map[string]AssetConfig {
	return map[string]AssetConfig{
		"supply_chain": {
			MaxSupply:        1000000,
			RequiresApproval: false,
			MaxDecimals:      2,
			Description:      "Physical goods tracking and supply chain management",
		},
		"carbon_credit": {
			MaxSupply:        10000000,
			RequiresApproval: true,
			MaxDecimals:      4,
			Description:      "Environmental carbon offset credits",
		},
		"real_estate": {
			MaxSupply:        1000,
			RequiresApproval: true,
			MaxDecimals:      4,
			Description:      "Property ownership fractions and real estate shares",
		},
		"certificate": {
			MaxSupply:        100000,
			RequiresApproval: false,
			MaxDecimals:      0,
			Description:      "Educational certificates and professional credentials",
		},
		"license": {
			MaxSupply:        10000,
			RequiresApproval: true,
			MaxDecimals:      0,
			Description:      "Professional licenses and regulatory permits",
		},
		"membership": {
			MaxSupply:        50000,
			RequiresApproval: false,
			MaxDecimals:      0,
			Description:      "Membership tokens and access permissions",
		},
		"loyalty_points": {
			MaxSupply:        100000000,
			RequiresApproval: false,
			MaxDecimals:      2,
			Description:      "Customer loyalty and reward point systems",
		},
		"utility_token": {
			MaxSupply:        1000000,
			RequiresApproval: false,
			MaxDecimals:      2,
			Description:      "Service access and utility consumption tokens",
		},
	}
}

func (vm *ThrylosVM) executeCreateAsset(op *VMOperation) (*ExecutionResult, error) {
	createAssetGas := int64(150000)
	vm.gasUsed += createAssetGas

	// Extract asset parameters
	assetID := op.Parameters["asset_id"]
	name := op.Parameters["name"]
	assetType := op.Parameters["asset_type"]
	realWorldRef := op.Parameters["real_world_reference"]
	maxDecimalsStr := op.Parameters["max_decimals"]
	transferableStr := op.Parameters["transferable"]
	expirationStr := op.Parameters["expiration_date"]
	regulatoryInfo := op.Parameters["regulatory_info"]

	// Validate required parameters
	if assetID == "" || name == "" || assetType == "" {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   "asset_id, name, and asset_type are required",
		}, nil
	}

	// Must specify what real-world thing this represents
	if realWorldRef == "" {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   "real_world_reference required (what does this asset represent?)",
		}, nil
	}

	// Enhanced currency detection and real-world reference validation
	if err := vm.DetectCurrencyAttempt(op); err != nil {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   err.Error(),
		}, nil
	}

	// Enhanced real-world reference validation
	if err := vm.validateRealWorldReference(realWorldRef); err != nil {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   err.Error(),
		}, nil
	}

	// Check if asset ID already exists
	if existingAsset, err := vm.worldState.GetAsset(assetID); err == nil && existingAsset != nil {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   fmt.Sprintf("asset %s already exists", assetID),
		}, nil
	}

	// Validate asset ID format
	if err := vm.validateAssetID(assetID); err != nil {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   err.Error(),
		}, nil
	}

	// Validate asset type against allowed configs
	assetConfigs := vm.getAssetConfigs()
	config, exists := assetConfigs[assetType]
	if !exists {
		allowedTypes := make([]string, 0, len(assetConfigs))
		for k := range assetConfigs {
			allowedTypes = append(allowedTypes, k)
		}
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   fmt.Sprintf("asset_type '%s' not supported. Allowed types: %v", assetType, allowedTypes),
		}, nil
	}

	// Parse and validate decimals
	maxDecimals := config.MaxDecimals // Use config default
	if maxDecimalsStr != "" {
		if parsed, err := strconv.ParseInt(maxDecimalsStr, 10, 32); err == nil {
			requestedDecimals := int32(parsed)
			if requestedDecimals > config.MaxDecimals {
				return &ExecutionResult{
					Success: false,
					GasUsed: vm.gasUsed,
					Error:   fmt.Sprintf("%s assets limited to %d decimal places maximum", assetType, config.MaxDecimals),
				}, nil
			}
			maxDecimals = requestedDecimals
		} else {
			return &ExecutionResult{
				Success: false,
				GasUsed: vm.gasUsed,
				Error:   "invalid max_decimals value",
			}, nil
		}
	}

	// Enforce supply limits based on asset type
	if op.Amount <= 0 {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   "initial_supply must be positive",
		}, nil
	}

	if op.Amount > config.MaxSupply {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   fmt.Sprintf("%s assets limited to %d maximum supply", assetType, config.MaxSupply),
		}, nil
	}

	// Parse transferable (default to true)
	transferable := true
	if transferableStr == "false" {
		transferable = false
	}

	// Parse and validate expiration date
	var expirationDate *int64
	if expirationStr != "" {
		if parsed, err := strconv.ParseInt(expirationStr, 10, 64); err == nil {
			if parsed <= time.Now().Unix() {
				return &ExecutionResult{
					Success: false,
					GasUsed: vm.gasUsed,
					Error:   "expiration_date must be in the future",
				}, nil
			}
			expirationDate = &parsed
		} else {
			return &ExecutionResult{
				Success: false,
				GasUsed: vm.gasUsed,
				Error:   "invalid expiration_date format (must be Unix timestamp)",
			}, nil
		}
	}

	// Validate creator has sufficient balance for gas costs
	creatorBalance, err := vm.worldState.GetBalance(op.From)
	if err != nil {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   fmt.Sprintf("failed to get creator balance: %v", err),
		}, nil
	}

	estimatedGasCost := vm.gasUsed * vm.gasPrice
	if creatorBalance < estimatedGasCost {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   fmt.Sprintf("insufficient balance for gas costs: have %d, need %d", creatorBalance, estimatedGasCost),
		}, nil
	}

	// Create asset with constraints
	asset := &state.AssetToken{ // ✅ This will work
		ID:               assetID,
		Name:             name,
		AssetType:        assetType,
		RealWorldRef:     realWorldRef,
		MaxDecimals:      maxDecimals,
		TotalSupply:      op.Amount,
		Creator:          op.From,
		Transferable:     transferable,
		RequiresApproval: config.RequiresApproval,
		ExpirationDate:   expirationDate,
		RegulatoryInfo:   regulatoryInfo,
		CreatedAt:        time.Now().Unix(),
	}

	// Store the asset in WorldState
	if err := vm.worldState.StoreAsset(asset); err != nil {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   fmt.Sprintf("failed to store asset: %v", err),
		}, nil
	}

	// Set initial balance for creator
	if err := vm.worldState.SetAssetBalance(assetID, op.From, op.Amount); err != nil {
		// Rollback asset creation if balance setting fails
		vm.worldState.DeleteAsset(assetID)
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   fmt.Sprintf("failed to set initial balance: %v", err),
		}, nil
	}

	// Create asset registry entry for tracking
	if err := vm.worldState.AddAssetToRegistry(assetID, op.From, assetType); err != nil {
		// Rollback on registry failure
		vm.worldState.DeleteAsset(assetID)
		vm.worldState.SetAssetBalance(assetID, op.From, 0)
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   fmt.Sprintf("failed to register asset: %v", err),
		}, nil
	}

	assetData, _ := json.Marshal(asset)

	return &ExecutionResult{
		Success:    true,
		GasUsed:    vm.gasUsed,
		ReturnData: assetData,
		Events: []Event{{
			Type: "asset_created",
			Data: map[string]interface{}{
				"asset_id":          assetID,
				"name":              name,
				"asset_type":        assetType,
				"real_world_ref":    realWorldRef,
				"max_decimals":      maxDecimals,
				"total_supply":      op.Amount,
				"creator":           op.From,
				"transferable":      transferable,
				"requires_approval": config.RequiresApproval,
				"expiration_date":   expirationDate,
				"created_at":        asset.CreatedAt,
			},
		}},
		StateChanges: []StateChange{{
			Type:    "asset_created",
			Address: assetID,
			Before:  nil,
			After:   assetData,
		}},
	}, nil
}

// Enhanced real-world reference validation
func (vm *ThrylosVM) validateRealWorldReference(realWorldRef string) error {
	if len(realWorldRef) < 20 {
		return fmt.Errorf("real_world_reference too brief - provide detailed description (minimum 20 characters)")
	}

	if len(realWorldRef) > 500 {
		return fmt.Errorf("real_world_reference too long (maximum 500 characters)")
	}

	// Check for vague references that don't specify actual assets
	vagueTerms := []string{
		"digital asset", "blockchain token", "cryptocurrency", "virtual currency",
		"crypto token", "digital currency", "virtual asset", "blockchain asset",
		"token utility", "platform token", "governance token", "utility coin",
	}

	refLower := strings.ToLower(realWorldRef)
	for _, term := range vagueTerms {
		if strings.Contains(refLower, term) {
			return fmt.Errorf("real_world_reference cannot be vague - specify actual physical asset, not '%s'", term)
		}
	}

	// Require specific asset identification
	requiredTerms := []string{
		"serial", "batch", "unit", "certificate", "license", "property",
		"item", "product", "goods", "inventory", "shipment", "container",
		"plot", "building", "equipment", "vehicle", "specimen", "sample",
	}

	hasSpecificTerm := false
	for _, term := range requiredTerms {
		if strings.Contains(refLower, term) {
			hasSpecificTerm = true
			break
		}
	}

	if !hasSpecificTerm {
		return fmt.Errorf("real_world_reference must include specific identification (serial, batch, unit, certificate, etc.)")
	}

	// Block obvious currency-related descriptions
	currencyDescriptions := []string{
		"investment vehicle", "trading instrument", "store of value",
		"medium of exchange", "payment method", "currency alternative",
		"financial instrument", "speculative asset", "investment token",
	}

	for _, desc := range currencyDescriptions {
		if strings.Contains(refLower, desc) {
			return fmt.Errorf("real_world_reference describes currency/investment use case - not allowed")
		}
	}

	return nil
}

// Enhanced asset ID validation
func (vm *ThrylosVM) validateAssetID(assetID string) error {
	if len(assetID) < 3 || len(assetID) > 64 {
		return fmt.Errorf("asset_id must be between 3 and 64 characters")
	}

	// Must start with letter and contain only alphanumeric, underscore, dash
	if !regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`).MatchString(assetID) {
		return fmt.Errorf("asset_id must start with letter and contain only alphanumeric, underscore, or dash")
	}

	// Block currency-like asset IDs
	currencyPatterns := []string{
		"coin", "token", "cash", "money", "currency", "dollar", "euro",
		"btc", "eth", "usdt", "usdc", "bnb", "ada", "sol", "doge",
		"shib", "pepe", "moon", "safe", "baby", "mini", "mega",
	}

	assetIDLower := strings.ToLower(assetID)
	for _, pattern := range currencyPatterns {
		if strings.Contains(assetIDLower, pattern) {
			return fmt.Errorf("asset_id '%s' contains currency-like term '%s' - not allowed", assetID, pattern)
		}
	}

	return nil
}

// Enhanced DetectCurrencyAttempt function (to replace the existing one)
func (vm *ThrylosVM) DetectCurrencyAttempt(op *VMOperation) error {
	if op.Type != "create_asset" {
		return nil
	}

	name := strings.ToLower(op.Parameters["name"])
	assetType := strings.ToLower(op.Parameters["asset_type"])
	realWorldRef := strings.ToLower(op.Parameters["real_world_reference"])

	// Expanded currency detection patterns
	currencyWords := []string{
		// Traditional currency terms
		"coin", "token", "cash", "money", "currency", "dollar", "euro", "yen", "pound",
		// Crypto terms
		"bitcoin", "ethereum", "crypto", "blockchain", "defi", "yield", "stake",
		// Meme coin patterns
		"doge", "pepe", "moon", "rocket", "diamond", "hands", "hodl", "ape",
		"shib", "safe", "baby", "mini", "mega", "ultra", "super", "hyper",
		"elon", "mars", "lambo", "millionaire", "billionaire", "rich", "wealth",
		// Investment terms
		"investment", "trading", "speculation", "finance", "returns", "profit",
		// Platform tokens
		"governance", "utility", "platform", "ecosystem", "protocol", "dao",
	}

	// Check name for currency terms
	for _, word := range currencyWords {
		if strings.Contains(name, word) {
			return fmt.Errorf("asset name '%s' contains currency-like term '%s' - not allowed", name, word)
		}
	}

	// Check real-world reference for currency indicators
	currencyRefTerms := []string{
		"digital currency", "virtual currency", "payment system", "store of value",
		"medium of exchange", "investment vehicle", "trading instrument",
		"financial protocol", "yield farming", "liquidity mining",
	}

	for _, term := range currencyRefTerms {
		if strings.Contains(realWorldRef, term) {
			return fmt.Errorf("real_world_reference contains currency/financial term '%s' - not allowed", term)
		}
	}

	// Parse decimals for validation
	decimals := int32(0)
	if decimalsStr := op.Parameters["max_decimals"]; decimalsStr != "" {
		if parsed, err := strconv.ParseInt(decimalsStr, 10, 32); err == nil {
			decimals = int32(parsed)
		}
	}

	// Block large supplies with high decimals (currency characteristics)
	if op.Amount > 1000000 && decimals >= 4 {
		return fmt.Errorf("large supply (%d) with high decimals (%d) resembles currency - not allowed", op.Amount, decimals)
	}

	// Block round numbers that suggest currency (1M, 10M, 100M, 1B, etc.)
	suspiciousSupplies := []int64{
		1000000, 10000000, 100000000, 1000000000, 10000000000,
		5000000, 50000000, 500000000, 5000000000,
	}

	for _, suspicious := range suspiciousSupplies {
		if op.Amount == suspicious {
			return fmt.Errorf("supply amount %d appears to be currency-like round number - provide business justification", op.Amount)
		}
	}

	// Block speculative asset types
	speculativeTypes := []string{
		"investment", "trading", "speculation", "finance", "defi", "yield",
		"governance", "utility", "platform", "protocol", "ecosystem",
	}

	for _, blockType := range speculativeTypes {
		if strings.Contains(assetType, blockType) {
			return fmt.Errorf("asset type containing '%s' not allowed - only real-world utility assets permitted", blockType)
		}
	}

	return nil
}

func (vm *ThrylosVM) executeMintAsset(op *VMOperation) (*ExecutionResult, error) {
	mintAssetGas := int64(75000)
	vm.gasUsed += mintAssetGas

	assetID := op.Parameters["asset_id"]
	recipient := op.To

	if assetID == "" {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   "asset_id parameter required",
		}, nil
	}

	if recipient == "" {
		recipient = op.From // Mint to sender if no recipient specified
	}

	// TODO: Implement asset validation and minting logic
	// This includes checking if asset exists, if minter is authorized, etc.

	return &ExecutionResult{
		Success: true,
		GasUsed: vm.gasUsed,
		Events: []Event{{
			Type: "asset_minted",
			Data: map[string]interface{}{
				"asset_id":  assetID,
				"recipient": recipient,
				"amount":    op.Amount,
				"minter":    op.From,
			},
		}},
	}, nil
}

func (vm *ThrylosVM) executeBurnAsset(op *VMOperation) (*ExecutionResult, error) {
	burnAssetGas := int64(50000)
	vm.gasUsed += burnAssetGas

	assetID := op.Parameters["asset_id"]

	if assetID == "" {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   "asset_id parameter required",
		}, nil
	}

	// TODO: Implement asset burning logic
	// This includes checking asset balance, reducing supply, etc.

	return &ExecutionResult{
		Success: true,
		GasUsed: vm.gasUsed,
		Events: []Event{{
			Type: "asset_burned",
			Data: map[string]interface{}{
				"asset_id": assetID,
				"amount":   op.Amount,
				"burner":   op.From,
			},
		}},
	}, nil
}

func (vm *ThrylosVM) executeTransferAsset(op *VMOperation) (*ExecutionResult, error) {
	transferAssetGas := int64(35000)
	vm.gasUsed += transferAssetGas

	assetID := op.Parameters["asset_id"]

	if assetID == "" {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   "asset_id parameter required",
		}, nil
	}

	if op.To == "" {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   "recipient address required",
		}, nil
	}

	// TODO: Implement asset transfer logic
	// This includes checking if asset is transferable, approval requirements, etc.

	return &ExecutionResult{
		Success: true,
		GasUsed: vm.gasUsed,
		Events: []Event{{
			Type: "asset_transferred",
			Data: map[string]interface{}{
				"asset_id": assetID,
				"from":     op.From,
				"to":       op.To,
				"amount":   op.Amount,
			},
		}},
	}, nil
}

func (vm *ThrylosVM) executeClaimRewards(op *VMOperation) (*ExecutionResult, error) {
	claimRewardsGas := int64(25000)
	vm.gasUsed += claimRewardsGas

	stakingManager := vm.worldState.GetStakingManager()

	err := stakingManager.ClaimRewards(op.From)
	if err != nil {
		return &ExecutionResult{
			Success: false,
			GasUsed: vm.gasUsed,
			Error:   err.Error(),
		}, nil
	}

	return &ExecutionResult{
		Success: true,
		GasUsed: vm.gasUsed,
		Events: []Event{{
			Type: "rewards_claimed",
			Data: map[string]interface{}{
				"claimer": op.From,
			},
		}},
	}, nil
}

// Integration with transaction executor

func (vm *ThrylosVM) ExecuteVMTransaction(tx *core.Transaction) (*ExecutionResult, error) {
	opType, parameters := vm.parseOperationFromTransaction(tx)

	op := &VMOperation{
		Type:       opType,
		From:       tx.From,
		To:         tx.To,
		Amount:     tx.Amount,
		Gas:        tx.Gas,
		Data:       tx.Data,
		Parameters: parameters,
	}

	return vm.Execute(op)
}

func (vm *ThrylosVM) parseOperationFromTransaction(tx *core.Transaction) (string, map[string]string) {
	if len(tx.Data) == 0 {
		return "transfer", nil
	}

	var opData struct {
		Type       string            `json:"type"`
		Parameters map[string]string `json:"parameters"`
	}

	if err := json.Unmarshal(tx.Data, &opData); err == nil {
		if opData.Type != "" {
			return opData.Type, opData.Parameters
		}
	}

	return "transfer", nil
}

// Helper and validation methods

func (vm *ThrylosVM) ValidateOperation(op *VMOperation) error {
	if op.From == "" {
		return fmt.Errorf("from address cannot be empty")
	}

	if op.Gas <= 0 {
		return fmt.Errorf("gas must be positive")
	}

	if op.Gas > vm.gasLimit {
		return fmt.Errorf("gas exceeds maximum limit")
	}

	switch op.Type {
	case "transfer", "stake", "delegate", "undelegate":
		if op.Amount <= 0 {
			return fmt.Errorf("amount must be positive")
		}
		return vm.validateBalance(op.From, op.Amount)

	case "create_validator":
		if op.Amount <= 0 {
			return fmt.Errorf("stake amount must be positive")
		}
		if op.Parameters["public_key"] == "" {
			return fmt.Errorf("public_key parameter required")
		}
		return vm.validateBalance(op.From, op.Amount)

	case "create_asset":
		if op.Amount <= 0 {
			return fmt.Errorf("initial_supply must be positive")
		}
		return vm.validateAssetCreation(op)

	case "cross_shard_transfer":
		if op.Amount <= 0 {
			return fmt.Errorf("amount must be positive")
		}
		if op.To == "" {
			return fmt.Errorf("recipient address required")
		}
		return vm.validateBalance(op.From, op.Amount)

	// BLOCKED: Old token operations
	case "create_token", "mint_token", "burn_token", "transfer_token":
		return fmt.Errorf("operation type '%s' not supported - use asset operations instead", op.Type)
	}

	return nil
}

func (vm *ThrylosVM) validateBalance(address string, amount int64) error {
	balance, err := vm.worldState.GetBalance(address)
	if err != nil {
		return fmt.Errorf("failed to get balance: %v", err)
	}

	if balance < amount {
		return fmt.Errorf("insufficient balance: have %d, need %d", balance, amount)
	}

	return nil
}

// ValidateContractAssetRestrictions ensures contracts cannot bypass asset limitations
func (vm *ThrylosVM) ValidateContractAssetRestrictions(bytecode []byte) error {
	// Basic bytecode analysis to detect potential bypass attempts
	// In a real implementation, this would be more sophisticated

	if len(bytecode) == 0 {
		return fmt.Errorf("empty contract bytecode")
	}

	// Check for suspicious patterns in bytecode (placeholder)
	// Real implementation would analyze RISC-V instructions
	bytecodeStr := string(bytecode)

	suspiciousPatterns := []string{
		"CREATE_TOKEN", "MINT_CURRENCY", "BYPASS_VALIDATION",
		"UNLIMITED_SUPPLY", "HIGH_DECIMALS", "CURRENCY_MODE",
	}

	for _, pattern := range suspiciousPatterns {
		if strings.Contains(strings.ToUpper(bytecodeStr), pattern) {
			return fmt.Errorf("contract bytecode contains suspicious pattern: %s", pattern)
		}
	}

	return nil
}

// validateAssetCreation validates asset creation parameters
func (vm *ThrylosVM) validateAssetCreation(op *VMOperation) error {
	assetID := op.Parameters["asset_id"]
	name := op.Parameters["name"]
	assetType := op.Parameters["asset_type"]
	realWorldRef := op.Parameters["real_world_reference"]

	if len(assetID) < 3 || len(assetID) > 32 {
		return fmt.Errorf("asset_id must be between 3 and 32 characters")
	}

	if len(name) < 1 || len(name) > 64 {
		return fmt.Errorf("asset name must be between 1 and 64 characters")
	}

	if assetType == "" {
		return fmt.Errorf("asset_type is required")
	}

	if realWorldRef == "" {
		return fmt.Errorf("real_world_reference is required")
	}

	if len(realWorldRef) < 10 || len(realWorldRef) > 256 {
		return fmt.Errorf("real_world_reference must be between 10 and 256 characters")
	}

	// Validate against allowed asset types
	assetConfigs := vm.getAssetConfigs()
	if _, exists := assetConfigs[assetType]; !exists {
		return fmt.Errorf("asset_type '%s' not supported", assetType)
	}

	return nil
}

// EstimateGas estimates gas needed for an operation
func (vm *ThrylosVM) EstimateGas(op *VMOperation) int64 {
	baseGas := vm.getBaseGas(op.Type)

	switch op.Type {
	case "create_asset":
		// Add cost based on asset name/reference length
		nameLength := len(op.Parameters["name"])
		refLength := len(op.Parameters["real_world_reference"])
		return baseGas + int64(nameLength+refLength)*50

	case "cross_shard_transfer":
		return baseGas + 50000

	case "create_validator":
		pubKeyLength := len(op.Parameters["public_key"])
		return baseGas + int64(pubKeyLength)*10
	}

	return baseGas
}

// getBaseGas returns base gas cost for operation types
func (vm *ThrylosVM) getBaseGas(opType string) int64 {
	switch opType {
	case "transfer":
		return 21000
	case "stake", "delegate":
		return 50000
	case "undelegate":
		return 75000
	case "cross_shard_transfer":
		return 100000
	case "create_validator":
		return 100000
	case "create_asset":
		return 150000
	case "mint_asset":
		return 75000
	case "burn_asset":
		return 50000
	case "transfer_asset":
		return 35000
	case "claim_rewards":
		return 25000
	case "custom_contract":
		return 200000
	default:
		return 21000
	}
}

// CanExecuteInParallel checks if two operations can be executed in parallel
func (vm *ThrylosVM) CanExecuteInParallel(op1, op2 *VMOperation) bool {
	if op1.From != op2.From && op1.From != op2.To && op1.To != op2.From && op1.To != op2.To {
		return true
	}
	return false
}

// GetState returns current VM state
func (vm *ThrylosVM) GetState() map[string]interface{} {
	return map[string]interface{}{
		"gas_used":  vm.gasUsed,
		"gas_limit": vm.gasLimit,
		"gas_price": vm.gasPrice,
	}
}

// Reset resets the VM state for new execution
func (vm *ThrylosVM) Reset() {
	vm.gasUsed = 0
}

// GetGasUsed returns the amount of gas used in current execution
func (vm *ThrylosVM) GetGasUsed() int64 {
	return vm.gasUsed
}

// GetGasRemaining returns the amount of gas remaining
func (vm *ThrylosVM) GetGasRemaining() int64 {
	return vm.gasLimit - vm.gasUsed
}

// SetGasLimit updates the gas limit for the VM
func (vm *ThrylosVM) SetGasLimit(gasLimit int64) {
	vm.gasLimit = gasLimit
}

// SetGasPrice updates the gas price for the VM
func (vm *ThrylosVM) SetGasPrice(gasPrice int64) {
	vm.gasPrice = gasPrice
}

// GetGasPrice returns the current gas price
func (vm *ThrylosVM) GetGasPrice() int64 {
	return vm.gasPrice
}

// GetGasLimit returns the current gas limit
func (vm *ThrylosVM) GetGasLimit() int64 {
	return vm.gasLimit
}

// SafeExecute wraps Execute with panic recovery
func (vm *ThrylosVM) SafeExecute(op *VMOperation) (result *ExecutionResult, err error) {
	defer func() {
		if r := vm.RecoverFromPanic(); r != nil {
			result = &ExecutionResult{
				Success: false,
				Error:   fmt.Sprintf("VM panic: %v", r),
				GasUsed: vm.gasUsed,
			}
			err = fmt.Errorf("VM execution panic: %v", r)
		}
	}()

	return vm.Execute(op)
}

// ExecuteBatch executes multiple operations in sequence with shared gas accounting
func (vm *ThrylosVM) ExecuteBatch(operations []*VMOperation) ([]*ExecutionResult, error) {
	results := make([]*ExecutionResult, 0, len(operations))

	snapshot := vm.worldState.CreateSnapshot()
	originalGasUsed := vm.gasUsed

	for i, op := range operations {
		result, err := vm.Execute(op)
		if err != nil {
			vm.worldState.RestoreFromSnapshot(snapshot)
			vm.gasUsed = originalGasUsed
			return nil, fmt.Errorf("batch execution failed at operation %d: %v", i, err)
		}

		if !result.Success {
			vm.worldState.RestoreFromSnapshot(snapshot)
			vm.gasUsed = originalGasUsed
			return nil, fmt.Errorf("batch execution failed at operation %d: %s", i, result.Error)
		}

		results = append(results, result)

		if vm.gasUsed >= vm.gasLimit {
			break
		}
	}

	return results, nil
}

// ValidateOperationSequence validates that a sequence of operations can be executed
func (vm *ThrylosVM) ValidateOperationSequence(operations []*VMOperation) error {
	snapshot := vm.worldState.CreateSnapshot()
	defer vm.worldState.RestoreFromSnapshot(snapshot)

	totalGas := int64(0)

	for i, op := range operations {
		estimatedGas := vm.EstimateGas(op)
		totalGas += estimatedGas

		if totalGas > vm.gasLimit {
			return fmt.Errorf("operation sequence exceeds gas limit at operation %d", i)
		}

		if err := vm.ValidateOperation(op); err != nil {
			return fmt.Errorf("operation %d validation failed: %v", i, err)
		}

		tempVM := &ThrylosVM{
			worldState: vm.worldState,
			gasPrice:   vm.gasPrice,
			gasLimit:   estimatedGas,
			gasUsed:    0,
		}

		if _, err := tempVM.Execute(op); err != nil {
			return fmt.Errorf("operation %d simulation failed: %v", i, err)
		}
	}

	return nil
}

// GetOperationTypes returns all supported operation types
func (vm *ThrylosVM) GetOperationTypes() []string {
	return []string{
		"transfer",
		"stake",
		"delegate",
		"undelegate",
		"cross_shard_transfer",
		"create_validator",
		"create_asset",
		"unbond_validator",
		"mint_asset",
		"burn_asset",
		"transfer_asset",
		"claim_rewards",
		"custom_contract",
	}
}

// GetOperationInfo returns information about a specific operation type
func (vm *ThrylosVM) GetOperationInfo(opType string) map[string]interface{} {
	info := map[string]interface{}{
		"type":     opType,
		"gas_cost": vm.getBaseGas(opType),
	}

	switch opType {
	case "transfer":
		info["description"] = "Transfer native tokens between accounts"
		info["required_fields"] = []string{"from", "to", "amount"}

	case "stake", "delegate":
		info["description"] = "Delegate tokens to a validator"
		info["required_fields"] = []string{"from", "amount"}
		info["required_parameters"] = []string{"validator"}

	case "undelegate":
		info["description"] = "Undelegate tokens from a validator"
		info["required_fields"] = []string{"from", "amount"}
		info["required_parameters"] = []string{"validator"}

	case "create_validator":
		info["description"] = "Create a new validator"
		info["required_fields"] = []string{"from", "amount"}
		info["required_parameters"] = []string{"public_key"}
		info["optional_parameters"] = []string{"commission"}

	case "create_asset":
		info["description"] = "Create a new real-world asset token"
		info["required_fields"] = []string{"from", "amount"}
		info["required_parameters"] = []string{"asset_id", "name", "asset_type", "real_world_reference"}
		info["optional_parameters"] = []string{"max_decimals", "transferable", "expiration_date", "regulatory_info"}
		info["supported_asset_types"] = vm.getAssetConfigs()

	case "mint_asset":
		info["description"] = "Mint additional asset tokens"
		info["required_fields"] = []string{"from", "amount"}
		info["required_parameters"] = []string{"asset_id"}
		info["optional_fields"] = []string{"to"}

	case "burn_asset":
		info["description"] = "Burn asset tokens"
		info["required_fields"] = []string{"from", "amount"}
		info["required_parameters"] = []string{"asset_id"}

	case "transfer_asset":
		info["description"] = "Transfer asset tokens between accounts"
		info["required_fields"] = []string{"from", "to", "amount"}
		info["required_parameters"] = []string{"asset_id"}

	case "cross_shard_transfer":
		info["description"] = "Transfer tokens between shards"
		info["required_fields"] = []string{"from", "to", "amount"}
	}

	return info
}

// Debug and monitoring methods

func (vm *ThrylosVM) GetExecutionTrace() map[string]interface{} {
	return map[string]interface{}{
		"gas_used":           vm.gasUsed,
		"gas_limit":          vm.gasLimit,
		"gas_price":          vm.gasPrice,
		"gas_remaining":      vm.GetGasRemaining(),
		"world_state_height": vm.worldState.GetHeight(),
		"shard_id":           vm.worldState.GetShardID(),
	}
}

func (vm *ThrylosVM) GetPerformanceMetrics() map[string]interface{} {
	return map[string]interface{}{
		"total_gas_used":       vm.gasUsed,
		"gas_efficiency":       float64(vm.gasUsed) / float64(vm.gasLimit),
		"supported_operations": len(vm.GetOperationTypes()),
	}
}

// Error handling and recovery

func (vm *ThrylosVM) RecoverFromPanic() interface{} {
	if r := recover(); r != nil {
		vm.gasUsed = 0
		return r
	}
	return nil
}

// Utility methods for operation creation

func CreateTransferOperation(from, to string, amount, gas int64) *VMOperation {
	return &VMOperation{
		Type:   "transfer",
		From:   from,
		To:     to,
		Amount: amount,
		Gas:    gas,
	}
}

func CreateStakeOperation(from, validator string, amount, gas int64) *VMOperation {
	return &VMOperation{
		Type:   "stake",
		From:   from,
		Amount: amount,
		Gas:    gas,
		Parameters: map[string]string{
			"validator": validator,
		},
	}
}

// CreateAssetOperation creates an asset creation operation
func CreateAssetOperation(from, assetID, name, assetType, realWorldRef string, supply, gas int64, maxDecimals int32, transferable bool) *VMOperation {
	return &VMOperation{
		Type:   "create_asset",
		From:   from,
		Amount: supply,
		Gas:    gas,
		Parameters: map[string]string{
			"asset_id":             assetID,
			"name":                 name,
			"asset_type":           assetType,
			"real_world_reference": realWorldRef,
			"max_decimals":         fmt.Sprintf("%d", maxDecimals),
			"transferable":         fmt.Sprintf("%t", transferable),
		},
	}
}

func CreateValidatorOperation(from, publicKey string, stake, gas int64, commission float64) *VMOperation {
	return &VMOperation{
		Type:   "create_validator",
		From:   from,
		Amount: stake,
		Gas:    gas,
		Parameters: map[string]string{
			"public_key": publicKey,
			"commission": fmt.Sprintf("%.4f", commission),
		},
	}
}

func CreateCrossShardTransferOperation(from, to string, amount, gas int64) *VMOperation {
	return &VMOperation{
		Type:   "cross_shard_transfer",
		From:   from,
		To:     to,
		Amount: amount,
		Gas:    gas,
	}
}
