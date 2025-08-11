// RISC-V Custom Contract execution for Thrylos VM - Asset-Only Model

package vm

import (
	"fmt"
	"strings"
	"time"
)

// RISC-V Engine interface - you'll implement this with chosen RISC-V library
type RISCVEngine interface {
	Load(bytecode []byte) error
	Execute(gasLimit int64) (*RISCVResult, error)
	SetAPI(api *ContractAPI)
	Reset()
}

// RISC-V execution result
type RISCVResult struct {
	Success    bool
	GasUsed    int64
	ReturnData []byte
	Error      string
	APICallLog []APICall
}

// API call tracking for events
type APICall struct {
	Function   string                 `json:"function"`
	Parameters map[string]interface{} `json:"parameters"`
	Result     interface{}            `json:"result"`
	GasUsed    int64                  `json:"gas_used"`
}

// Contract API provides blockchain functions to RISC-V contracts
type ContractAPI struct {
	vm       *ThrylosVM
	caller   string
	gasUsed  *int64
	maxGas   int64
	callLog  []APICall
	snapshot interface{} // WorldState snapshot for rollback
}

// Blockchain API functions that RISC-V contracts can call

func (api *ContractAPI) GetBalance(address string) (int64, error) {
	*api.gasUsed += 5000 // Gas cost for balance query

	if *api.gasUsed > api.maxGas {
		return 0, fmt.Errorf("out of gas")
	}

	balance, err := api.vm.worldState.GetBalance(address)

	// Log the API call
	api.callLog = append(api.callLog, APICall{
		Function:   "get_balance",
		Parameters: map[string]interface{}{"address": address},
		Result:     balance,
		GasUsed:    5000,
	})

	return balance, err
}

func (api *ContractAPI) Transfer(to string, amount int64) error {
	*api.gasUsed += 21000 // Standard transfer gas cost

	if *api.gasUsed > api.maxGas {
		return fmt.Errorf("out of gas")
	}

	// Create internal transfer operation
	transferOp := &VMOperation{
		Type:   "transfer",
		From:   api.caller,
		To:     to,
		Amount: amount,
		Gas:    21000,
	}

	// Execute transfer through VM
	result, err := api.vm.executeTransfer(transferOp)

	// Log the API call
	api.callLog = append(api.callLog, APICall{
		Function: "transfer",
		Parameters: map[string]interface{}{
			"to":     to,
			"amount": amount,
		},
		Result:  result.Success,
		GasUsed: 21000,
	})

	if err != nil || !result.Success {
		return fmt.Errorf("transfer failed: %v", err)
	}

	return nil
}

// REMOVED: GetTokenBalance - no more arbitrary token support
// REPLACED WITH: GetAssetBalance for real-world assets only

func (api *ContractAPI) GetAssetBalance(assetID, address string) (int64, error) {
	*api.gasUsed += 7500 // Gas cost for asset balance query

	if *api.gasUsed > api.maxGas {
		return 0, fmt.Errorf("out of gas")
	}

	// TODO: Implement asset balance lookup in WorldState
	// For now, return placeholder
	balance := int64(0)

	api.callLog = append(api.callLog, APICall{
		Function: "get_asset_balance",
		Parameters: map[string]interface{}{
			"asset_id": assetID,
			"address":  address,
		},
		Result:  balance,
		GasUsed: 7500,
	})

	return balance, nil
}

// BLOCKED: CreateToken - contracts cannot create arbitrary tokens
func (api *ContractAPI) CreateToken(tokenID, name, symbol string, supply int64, decimals int32) error {
	return fmt.Errorf("token creation not allowed from contracts - use CreateAsset for real-world assets")
}

// RESTRICTED: CreateAsset - only allowed asset types with real-world backing
func (api *ContractAPI) CreateAsset(assetID, name, assetType, realWorldRef string, supply int64, maxDecimals int32) error {
	*api.gasUsed += 150000 // Asset creation cost

	if *api.gasUsed > api.maxGas {
		return fmt.Errorf("out of gas")
	}

	// Validate asset type against allowed configs
	assetConfigs := api.vm.getAssetConfigs()
	config, exists := assetConfigs[assetType]
	if !exists {
		allowedTypes := make([]string, 0, len(assetConfigs))
		for k := range assetConfigs {
			allowedTypes = append(allowedTypes, k)
		}
		return fmt.Errorf("asset_type '%s' not supported from contracts. Allowed: %v", assetType, allowedTypes)
	}

	// Enforce real-world reference requirement
	if realWorldRef == "" || len(realWorldRef) < 10 {
		return fmt.Errorf("real_world_reference required (minimum 10 characters)")
	}

	// Enforce decimals limit
	if maxDecimals > config.MaxDecimals {
		return fmt.Errorf("%s assets limited to %d decimal places", assetType, config.MaxDecimals)
	}

	// Enforce supply limits
	if supply > config.MaxSupply {
		return fmt.Errorf("%s assets limited to %d maximum supply", assetType, config.MaxSupply)
	}

	// Create asset operation through VM
	assetOp := &VMOperation{
		Type:   "create_asset",
		From:   api.caller,
		Amount: supply,
		Gas:    150000,
		Parameters: map[string]string{
			"asset_id":             assetID,
			"name":                 name,
			"asset_type":           assetType,
			"real_world_reference": realWorldRef,
			"max_decimals":         fmt.Sprintf("%d", maxDecimals),
			"transferable":         "true", // Default for contract-created assets
		},
	}

	result, err := api.vm.executeCreateAsset(assetOp)

	api.callLog = append(api.callLog, APICall{
		Function: "create_asset",
		Parameters: map[string]interface{}{
			"asset_id":       assetID,
			"name":           name,
			"asset_type":     assetType,
			"real_world_ref": realWorldRef,
			"supply":         supply,
			"max_decimals":   maxDecimals,
		},
		Result:  result.Success,
		GasUsed: 150000,
	})

	if err != nil || !result.Success {
		return fmt.Errorf("asset creation failed: %v", err)
	}

	return nil
}

// RESTRICTED: TransferAsset - with validation for transferability
func (api *ContractAPI) TransferAsset(assetID, to string, amount int64) error {
	*api.gasUsed += 35000 // Asset transfer cost

	if *api.gasUsed > api.maxGas {
		return fmt.Errorf("out of gas")
	}

	// TODO: Check if asset is transferable (some assets like certificates shouldn't be)
	// This would require looking up asset metadata from WorldState

	transferOp := &VMOperation{
		Type:   "transfer_asset",
		From:   api.caller,
		To:     to,
		Amount: amount,
		Gas:    35000,
		Parameters: map[string]string{
			"asset_id": assetID,
		},
	}

	result, err := api.vm.executeTransferAsset(transferOp)

	api.callLog = append(api.callLog, APICall{
		Function: "transfer_asset",
		Parameters: map[string]interface{}{
			"asset_id": assetID,
			"to":       to,
			"amount":   amount,
		},
		Result:  result.Success,
		GasUsed: 35000,
	})

	if err != nil || !result.Success {
		return fmt.Errorf("asset transfer failed: %v", err)
	}

	return nil
}

// RESTRICTED: MintAsset - only creator or authorized minters can mint
func (api *ContractAPI) MintAsset(assetID, recipient string, amount int64) error {
	*api.gasUsed += 75000 // Asset minting cost

	if *api.gasUsed > api.maxGas {
		return fmt.Errorf("out of gas")
	}

	// TODO: Validate that caller is authorized to mint this asset
	// This would require checking asset creator or authorized minters

	mintOp := &VMOperation{
		Type:   "mint_asset",
		From:   api.caller,
		To:     recipient,
		Amount: amount,
		Gas:    75000,
		Parameters: map[string]string{
			"asset_id": assetID,
		},
	}

	result, err := api.vm.executeMintAsset(mintOp)

	api.callLog = append(api.callLog, APICall{
		Function: "mint_asset",
		Parameters: map[string]interface{}{
			"asset_id":  assetID,
			"recipient": recipient,
			"amount":    amount,
		},
		Result:  result.Success,
		GasUsed: 75000,
	})

	if err != nil || !result.Success {
		return fmt.Errorf("asset minting failed: %v", err)
	}

	return nil
}

// RESTRICTED: BurnAsset - validate ownership before burning
func (api *ContractAPI) BurnAsset(assetID string, amount int64) error {
	*api.gasUsed += 50000 // Asset burning cost

	if *api.gasUsed > api.maxGas {
		return fmt.Errorf("out of gas")
	}

	burnOp := &VMOperation{
		Type:   "burn_asset",
		From:   api.caller,
		Amount: amount,
		Gas:    50000,
		Parameters: map[string]string{
			"asset_id": assetID,
		},
	}

	result, err := api.vm.executeBurnAsset(burnOp)

	api.callLog = append(api.callLog, APICall{
		Function: "burn_asset",
		Parameters: map[string]interface{}{
			"asset_id": assetID,
			"amount":   amount,
		},
		Result:  result.Success,
		GasUsed: 50000,
	})

	if err != nil || !result.Success {
		return fmt.Errorf("asset burning failed: %v", err)
	}

	return nil
}

// UTILITY: GetAssetInfo - check asset metadata and constraints
func (api *ContractAPI) GetAssetInfo(assetID string) (map[string]interface{}, error) {
	*api.gasUsed += 2000 // Asset info query cost

	if *api.gasUsed > api.maxGas {
		return nil, fmt.Errorf("out of gas")
	}

	// TODO: Implement asset info lookup in WorldState
	// For now, return placeholder
	assetInfo := map[string]interface{}{
		"asset_id":          assetID,
		"exists":            true, // Placeholder
		"asset_type":        "supply_chain",
		"transferable":      true,
		"requires_approval": false,
	}

	api.callLog = append(api.callLog, APICall{
		Function: "get_asset_info",
		Parameters: map[string]interface{}{
			"asset_id": assetID,
		},
		Result:  assetInfo,
		GasUsed: 2000,
	})

	return assetInfo, nil
}

// UTILITY: ValidateAssetOperation - check if operation is allowed
func (api *ContractAPI) ValidateAssetOperation(assetID, operation string) (bool, error) {
	*api.gasUsed += 1500

	if *api.gasUsed > api.maxGas {
		return false, fmt.Errorf("out of gas")
	}

	// TODO: Implement asset operation validation
	// Check transferability, approval requirements, expiration, etc.
	valid := true // Placeholder

	api.callLog = append(api.callLog, APICall{
		Function: "validate_asset_operation",
		Parameters: map[string]interface{}{
			"asset_id":  assetID,
			"operation": operation,
		},
		Result:  valid,
		GasUsed: 1500,
	})

	return valid, nil
}

func (api *ContractAPI) EmitEvent(eventName string, data map[string]interface{}) error {
	*api.gasUsed += 1000 // Gas cost per event

	if *api.gasUsed > api.maxGas {
		return fmt.Errorf("out of gas")
	}

	api.callLog = append(api.callLog, APICall{
		Function: "emit_event",
		Parameters: map[string]interface{}{
			"event_name": eventName,
			"data":       data,
		},
		Result:  true,
		GasUsed: 1000,
	})

	return nil
}

func (api *ContractAPI) GetBlockHeight() int64 {
	*api.gasUsed += 1000 // Cheap operation

	height := api.vm.worldState.GetHeight()

	api.callLog = append(api.callLog, APICall{
		Function:   "get_block_height",
		Parameters: map[string]interface{}{},
		Result:     height,
		GasUsed:    1000,
	})

	return height
}

func (api *ContractAPI) GetCaller() string {
	// Free operation
	return api.caller
}

// SECURITY: DetectCurrencyPatterns - prevent contracts from creating currency-like assets
func (api *ContractAPI) detectCurrencyPatterns(name, assetType string, supply int64, decimals int32) error {
	name = strings.ToLower(name)
	assetType = strings.ToLower(assetType)

	// Block currency-like names
	currencyWords := []string{
		"coin", "token", "cash", "money", "currency", "dollar", "euro", "bitcoin",
		"doge", "pepe", "moon", "rocket", "diamond", "hands", "hodl", "ape",
		"shib", "safe", "baby", "mini", "mega", "ultra", "super", "hyper",
	}

	for _, word := range currencyWords {
		if strings.Contains(name, word) {
			return fmt.Errorf("asset name '%s' contains currency-like term '%s' - not allowed", name, word)
		}
	}

	// Block large supplies with high decimals (currency characteristics)
	if supply > 1000000 && decimals >= 4 {
		return fmt.Errorf("large supply (%d) with high decimals (%d) resembles currency - not allowed", supply, decimals)
	}

	// Block non-utility asset types with currency characteristics
	nonUtilityTypes := []string{"investment", "trading", "speculation", "finance"}
	for _, blockType := range nonUtilityTypes {
		if assetType == blockType {
			return fmt.Errorf("asset type '%s' not allowed - only real-world utility assets permitted", assetType)
		}
	}

	return nil
}

// Placeholder for RISC-V engine implementation
// You'll replace this with actual RISC-V library integration
type MockRISCVEngine struct {
	bytecode []byte
	api      *ContractAPI
}

func NewRISCVEngine() RISCVEngine {
	return &MockRISCVEngine{}
}

func (engine *MockRISCVEngine) Load(bytecode []byte) error {
	engine.bytecode = bytecode
	return nil
}

func (engine *MockRISCVEngine) SetAPI(api *ContractAPI) {
	engine.api = api
}

func (engine *MockRISCVEngine) Execute(gasLimit int64) (*RISCVResult, error) {
	// Mock implementation demonstrating asset-restricted contract
	gasUsed := int64(50000)

	if engine.api != nil {
		// Example: contract checks caller's balance
		balance, err := engine.api.GetBalance(engine.api.GetCaller())
		if err != nil {
			return &RISCVResult{
				Success: false,
				Error:   err.Error(),
				GasUsed: gasUsed,
			}, nil
		}

		// Example: contract creates a legitimate asset (not currency)
		if balance > 100000 {
			err := engine.api.CreateAsset(
				"supply_chain_001",
				"Organic Coffee Batch #123",
				"supply_chain",
				"1000kg organic coffee beans from Farm XYZ, harvested 2024-08-11",
				1000, // 1000 units
				2,    // 2 decimal places
			)

			if err != nil {
				// This would fail if trying to create currency-like asset
				engine.api.EmitEvent("asset_creation_failed", map[string]interface{}{
					"reason": err.Error(),
				})
			} else {
				engine.api.EmitEvent("legitimate_asset_created", map[string]interface{}{
					"asset_id":   "supply_chain_001",
					"asset_type": "supply_chain",
					"caller":     engine.api.GetCaller(),
				})
			}
		}

		// Example: emit event for high balance (but can't create currency)
		if balance > 1000 {
			engine.api.EmitEvent("high_balance_detected", map[string]interface{}{
				"caller":  engine.api.GetCaller(),
				"balance": balance,
			})
		}
	}

	return &RISCVResult{
		Success:    true,
		GasUsed:    gasUsed,
		ReturnData: []byte("asset-restricted contract executed successfully"),
		APICallLog: engine.api.callLog,
	}, nil
}

func (engine *MockRISCVEngine) Reset() {
	engine.bytecode = nil
	engine.api = nil
}

// Enhanced gas estimation for custom contracts
func (vm *ThrylosVM) estimateCustomContractGas(bytecode []byte) int64 {
	baseGas := int64(200000) // Base execution cost

	// Add gas based on bytecode size
	bytecodeGas := int64(len(bytecode)) * 10 // 10 gas per byte

	// Add estimated gas for common operations
	// This would be more sophisticated in a real implementation
	estimatedOperations := int64(100) // Estimate based on bytecode analysis
	operationGas := estimatedOperations * 1000

	return baseGas + bytecodeGas + operationGas
}

// Contract deployment helper (stores bytecode for reuse)
func (vm *ThrylosVM) deployContract(op *VMOperation) (*ExecutionResult, error) {
	// This would store the contract bytecode in WorldState
	// and return a contract address for future calls

	contractAddress := fmt.Sprintf("contract_%s_%d", op.From, time.Now().Unix())

	// Store contract bytecode (you'd implement this in WorldState)
	// vm.worldState.StoreContract(contractAddress, op.Data)

	return &ExecutionResult{
		Success:    true,
		GasUsed:    vm.gasUsed,
		ReturnData: []byte(contractAddress),
		Events: []Event{{
			Type: "contract_deployed",
			Data: map[string]interface{}{
				"deployer": op.From,
				"address":  contractAddress,
				"size":     len(op.Data),
			},
		}},
	}, nil
}

// Additional security helpers for contract validation

// ValidateContractBytecode checks if contract bytecode attempts to bypass asset restrictions
func (vm *ThrylosVM) ValidateContractBytecode(bytecode []byte) error {
	// This would be more sophisticated in a real implementation
	// Could include static analysis to detect patterns that might bypass restrictions

	if len(bytecode) == 0 {
		return fmt.Errorf("empty contract bytecode")
	}

	// Basic size validation
	maxSize := int64(1024 * 1024) // 1MB max
	if int64(len(bytecode)) > maxSize {
		return fmt.Errorf("contract bytecode too large: %d bytes (max %d)", len(bytecode), maxSize)
	}

	return nil
}

// GetContractAssetPermissions returns what asset operations a contract can perform
func (vm *ThrylosVM) GetContractAssetPermissions() map[string]bool {
	return map[string]bool{
		"create_currency_tokens": false, // Explicitly blocked
		"create_asset_tokens":    true,  // Allowed with restrictions
		"transfer_assets":        true,  // Allowed with validation
		"mint_assets":            true,  // Allowed with authorization
		"burn_assets":            true,  // Allowed with ownership check
		"query_asset_info":       true,  // Always allowed
		"emit_events":            true,  // Always allowed
	}
}
