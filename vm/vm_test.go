// vm_test.go - Test VM logic functions directly without WorldState dependencies

package vm

import (
	"strings"
	"testing"
)

// TEST 1: Test Asset Configuration (no WorldState needed)
func TestAssetConfigs(t *testing.T) {
	// Create a minimal VM just for accessing methods
	vm := &ThrylosVM{
		gasPrice: 1000,
		gasLimit: 1000000,
		gasUsed:  0,
	}

	configs := vm.getAssetConfigs()

	expectedTypes := []string{
		"supply_chain", "carbon_credit", "real_estate",
		"certificate", "license", "membership",
		"loyalty_points", "utility_token",
	}

	for _, expectedType := range expectedTypes {
		if config, exists := configs[expectedType]; exists {
			t.Logf("✅ Asset type '%s': max_supply=%d, max_decimals=%d, requires_approval=%v",
				expectedType, config.MaxSupply, config.MaxDecimals, config.RequiresApproval)
		} else {
			t.Errorf("❌ Missing asset type: %s", expectedType)
		}
	}

	// Test specific constraints
	if supplyChain, exists := configs["supply_chain"]; exists {
		if supplyChain.MaxSupply != 1000000 {
			t.Errorf("Supply chain max supply wrong: got %d, want 1000000", supplyChain.MaxSupply)
		}
		if supplyChain.MaxDecimals != 2 {
			t.Errorf("Supply chain max decimals wrong: got %d, want 2", supplyChain.MaxDecimals)
		}
	}

	if carbonCredit, exists := configs["carbon_credit"]; exists {
		if !carbonCredit.RequiresApproval {
			t.Error("Carbon credits should require approval")
		}
	}
}

// TEST 2: Test Currency Detection Logic
func TestDetectCurrencyAttempt(t *testing.T) {
	vm := &ThrylosVM{
		gasPrice: 1000,
		gasLimit: 1000000,
		gasUsed:  0,
	}

	testCases := []struct {
		name          string
		assetName     string
		assetType     string
		supply        int64
		decimals      string
		shouldBlock   bool
		expectedError string
	}{
		{
			name:          "Bitcoin Copy",
			assetName:     "Bitcoin Copy Token",
			assetType:     "supply_chain",
			supply:        1000,
			decimals:      "2",
			shouldBlock:   true,
			expectedError: "currency-like term",
		},
		{
			name:          "Moon Rocket",
			assetName:     "Moon Rocket To Mars",
			assetType:     "supply_chain",
			supply:        1000,
			decimals:      "2",
			shouldBlock:   true,
			expectedError: "currency-like term",
		},
		{
			name:          "Large Supply High Decimals",
			assetName:     "Test Asset",
			assetType:     "supply_chain",
			supply:        10000000,
			decimals:      "8",
			shouldBlock:   true,
			expectedError: "resembles currency",
		},
		{
			name:          "Valid Supply Chain",
			assetName:     "Coffee Batch Tracker",
			assetType:     "supply_chain",
			supply:        1000,
			decimals:      "2",
			shouldBlock:   false,
			expectedError: "",
		},
		{
			name:          "Valid Certificate",
			assetName:     "University Diploma",
			assetType:     "certificate",
			supply:        1,
			decimals:      "0",
			shouldBlock:   false,
			expectedError: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			op := &VMOperation{
				Type:   "create_asset",
				From:   "test_address",
				Amount: tc.supply,
				Parameters: map[string]string{
					"name":         tc.assetName,
					"asset_type":   tc.assetType,
					"max_decimals": tc.decimals,
				},
			}

			err := vm.DetectCurrencyAttempt(op)

			if tc.shouldBlock {
				if err == nil {
					t.Errorf("Expected '%s' to be blocked but it wasn't", tc.assetName)
				} else if !strings.Contains(err.Error(), tc.expectedError) {
					t.Errorf("Expected error containing '%s', got '%s'", tc.expectedError, err.Error())
				} else {
					t.Logf("✅ Correctly blocked: %s - %s", tc.assetName, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Expected '%s' to be allowed but got error: %s", tc.assetName, err.Error())
				} else {
					t.Logf("✅ Correctly allowed: %s", tc.assetName)
				}
			}
		})
	}
}

// TEST 3: Test Real World Reference Validation
func TestValidateRealWorldReference(t *testing.T) {
	vm := &ThrylosVM{
		gasPrice: 1000,
		gasLimit: 1000000,
		gasUsed:  0,
	}

	testCases := []struct {
		name          string
		reference     string
		shouldPass    bool
		expectedError string
	}{
		{
			name:          "Too Short",
			reference:     "Short ref",
			shouldPass:    false,
			expectedError: "too brief",
		},
		{
			name:          "Vague Digital Asset",
			reference:     "This is a digital asset for blockchain use",
			shouldPass:    false,
			expectedError: "cannot be vague",
		},
		{
			name:          "Currency Description",
			reference:     "Investment vehicle for storing value and medium of exchange",
			shouldPass:    false,
			expectedError: "currency/investment use case",
		},
		{
			name:          "Valid Coffee Reference",
			reference:     "1000kg organic coffee beans from Farm ABC, batch number CF2024001, harvested January 2024",
			shouldPass:    true,
			expectedError: "",
		},
		{
			name:          "Valid Property Reference",
			reference:     "Apartment unit 12B in downtown building, property ID NYC-2024-789, 750 sq ft residential space",
			shouldPass:    true,
			expectedError: "",
		},
		{
			name:          "Valid Certificate Reference",
			reference:     "Computer Science degree certificate from MIT, student ID 123456, graduation year 2024",
			shouldPass:    true,
			expectedError: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := vm.validateRealWorldReference(tc.reference)

			if tc.shouldPass {
				if err != nil {
					t.Errorf("Expected reference to pass but got error: %s", err.Error())
				} else {
					t.Logf("✅ Valid reference: %s", tc.name)
				}
			} else {
				if err == nil {
					t.Errorf("Expected reference to fail but it passed: %s", tc.reference)
				} else if !strings.Contains(err.Error(), tc.expectedError) {
					t.Errorf("Expected error containing '%s', got '%s'", tc.expectedError, err.Error())
				} else {
					t.Logf("✅ Correctly rejected: %s - %s", tc.name, err.Error())
				}
			}
		})
	}
}

// TEST 4: Test Asset ID Validation
func TestValidateAssetID(t *testing.T) {
	vm := &ThrylosVM{
		gasPrice: 1000,
		gasLimit: 1000000,
		gasUsed:  0,
	}

	testCases := []struct {
		name          string
		assetID       string
		shouldPass    bool
		expectedError string
	}{
		{
			name:          "Too Short",
			assetID:       "ab",
			shouldPass:    false,
			expectedError: "between 3 and 64 characters",
		},
		{
			name:          "Contains Currency Term",
			assetID:       "bitcoin_copy_001",
			shouldPass:    false,
			expectedError: "currency-like term",
		},
		{
			name:          "Contains Coin",
			assetID:       "my_special_coin",
			shouldPass:    false,
			expectedError: "currency-like term",
		},
		{
			name:          "Invalid Start",
			assetID:       "123invalid",
			shouldPass:    false,
			expectedError: "must start with letter",
		},
		{
			name:          "Valid Supply Chain",
			assetID:       "coffee_batch_001",
			shouldPass:    true,
			expectedError: "",
		},
		{
			name:          "Valid Certificate",
			assetID:       "mit_diploma_2024",
			shouldPass:    true,
			expectedError: "",
		},
		{
			name:          "Valid Property",
			assetID:       "property_nyc_12b",
			shouldPass:    true,
			expectedError: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := vm.validateAssetID(tc.assetID)

			if tc.shouldPass {
				if err != nil {
					t.Errorf("Expected asset ID to pass but got error: %s", err.Error())
				} else {
					t.Logf("✅ Valid asset ID: %s", tc.assetID)
				}
			} else {
				if err == nil {
					t.Errorf("Expected asset ID to fail but it passed: %s", tc.assetID)
				} else if !strings.Contains(err.Error(), tc.expectedError) {
					t.Errorf("Expected error containing '%s', got '%s'", tc.expectedError, err.Error())
				} else {
					t.Logf("✅ Correctly rejected: %s - %s", tc.assetID, err.Error())
				}
			}
		})
	}
}

// TEST 5: Test Gas Estimation
func TestGasEstimation(t *testing.T) {
	vm := &ThrylosVM{
		gasPrice: 1000,
		gasLimit: 1000000,
		gasUsed:  0,
	}

	testCases := []struct {
		name      string
		opType    string
		expected  int64
		assetName string
		reference string
	}{
		{
			name:      "Transfer",
			opType:    "transfer",
			expected:  21000,
			assetName: "",
			reference: "",
		},
		{
			name:      "Stake",
			opType:    "stake",
			expected:  50000,
			assetName: "",
			reference: "",
		},
		{
			name:      "Create Asset",
			opType:    "create_asset",
			expected:  150000,
			assetName: "Test Asset",
			reference: "Real world reference for testing gas estimation",
		},
		{
			name:      "Transfer Asset",
			opType:    "transfer_asset",
			expected:  35000,
			assetName: "",
			reference: "",
		},
		{
			name:      "Mint Asset",
			opType:    "mint_asset",
			expected:  75000,
			assetName: "",
			reference: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			op := &VMOperation{
				Type: tc.opType,
				Parameters: map[string]string{
					"name":                 tc.assetName,
					"real_world_reference": tc.reference,
				},
			}

			gas := vm.EstimateGas(op)
			if gas < tc.expected {
				t.Errorf("Gas estimate too low: got %d, want at least %d", gas, tc.expected)
			} else {
				t.Logf("✅ Gas estimation for %s: %d (expected: %d)", tc.opType, gas, tc.expected)
			}
		})
	}
}

// TEST 6: Test VM State Management
func TestVMStateManagement(t *testing.T) {
	vm := &ThrylosVM{
		gasPrice: 1000,
		gasLimit: 1000000,
		gasUsed:  0,
	}

	// Initial state
	if vm.GetGasUsed() != 0 {
		t.Error("Initial gas used should be 0")
	}

	if vm.GetGasRemaining() != vm.GetGasLimit() {
		t.Error("Initial gas remaining should equal gas limit")
	}

	// Simulate gas usage
	vm.gasUsed = 50000

	if vm.GetGasUsed() != 50000 {
		t.Error("Gas used not tracked correctly")
	}

	if vm.GetGasRemaining() != vm.GetGasLimit()-50000 {
		t.Error("Gas remaining calculated incorrectly")
	}

	// Test gas price and limit getters
	if vm.GetGasPrice() != 1000 {
		t.Errorf("Gas price wrong: got %d, want 1000", vm.GetGasPrice())
	}

	if vm.GetGasLimit() != 1000000 {
		t.Errorf("Gas limit wrong: got %d, want 1000000", vm.GetGasLimit())
	}

	// Reset
	vm.Reset()
	if vm.GetGasUsed() != 0 {
		t.Error("Reset should zero gas used")
	}

	t.Logf("✅ VM state management working correctly")
}

// TEST 7: Test Operation Types
func TestGetOperationTypes(t *testing.T) {
	vm := &ThrylosVM{
		gasPrice: 1000,
		gasLimit: 1000000,
		gasUsed:  0,
	}

	operationTypes := vm.GetOperationTypes()

	expectedOps := []string{
		"transfer", "stake", "delegate", "undelegate",
		"cross_shard_transfer", "create_validator",
		"create_asset", "mint_asset", "burn_asset", "transfer_asset",
		"claim_rewards", "custom_contract",
	}

	// Check that all expected operations are present
	for _, expectedOp := range expectedOps {
		found := false
		for _, op := range operationTypes {
			if op == expectedOp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Missing operation type: %s", expectedOp)
		}
	}

	// Check that blocked operations are not present
	blockedOps := []string{"create_token", "mint_token", "burn_token", "transfer_token"}
	for _, blockedOp := range blockedOps {
		for _, op := range operationTypes {
			if op == blockedOp {
				t.Errorf("Blocked operation type should not be present: %s", blockedOp)
			}
		}
	}

	t.Logf("✅ Found %d operation types, all expected operations present", len(operationTypes))
}

// TEST 8: Test Operation Info
func TestGetOperationInfo(t *testing.T) {
	vm := &ThrylosVM{
		gasPrice: 1000,
		gasLimit: 1000000,
		gasUsed:  0,
	}

	// Test asset creation info
	info := vm.GetOperationInfo("create_asset")

	if info["type"] != "create_asset" {
		t.Errorf("Wrong operation type in info: got %v, want create_asset", info["type"])
	}

	if gasCode, ok := info["gas_cost"].(int64); !ok || gasCode != 150000 {
		t.Errorf("Wrong gas cost for create_asset: got %v, want 150000", info["gas_cost"])
	}

	// Check required parameters for create_asset
	if reqParams, ok := info["required_parameters"].([]string); ok {
		expectedParams := []string{"asset_id", "name", "asset_type", "real_world_reference"}
		for _, expected := range expectedParams {
			found := false
			for _, param := range reqParams {
				if param == expected {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Missing required parameter for create_asset: %s", expected)
			}
		}
	}

	// Check supported asset types
	if assetTypes, ok := info["supported_asset_types"]; ok {
		t.Logf("✅ Supported asset types: %v", assetTypes)
	}

	t.Logf("✅ Operation info working correctly for create_asset")
}

// TEST 9: Test Parallel Execution Check
func TestParallelExecution(t *testing.T) {
	vm := &ThrylosVM{
		gasPrice: 1000,
		gasLimit: 1000000,
		gasUsed:  0,
	}

	op1 := &VMOperation{
		Type: "transfer",
		From: "addr1",
		To:   "addr2",
	}

	op2 := &VMOperation{
		Type: "transfer",
		From: "addr3",
		To:   "addr4",
	}

	op3 := &VMOperation{
		Type: "transfer",
		From: "addr1", // Conflicts with op1
		To:   "addr5",
	}

	// These should be parallelizable (no conflicting addresses)
	if !vm.CanExecuteInParallel(op1, op2) {
		t.Error("op1 and op2 should be parallelizable")
	} else {
		t.Logf("✅ Non-conflicting operations correctly identified as parallelizable")
	}

	// These should NOT be parallelizable (addr1 conflicts)
	if vm.CanExecuteInParallel(op1, op3) {
		t.Error("op1 and op3 should NOT be parallelizable")
	} else {
		t.Logf("✅ Conflicting operations correctly identified as non-parallelizable")
	}
}

// TEST 10: Test Asset-Only Model Enforcement
func TestAssetOnlyModelEnforcement(t *testing.T) {
	vm := &ThrylosVM{
		gasPrice: 1000,
		gasLimit: 1000000,
		gasUsed:  0,
	}

	t.Run("Asset Types Are Constrained", func(t *testing.T) {
		configs := vm.getAssetConfigs()

		for assetType, config := range configs {
			// All asset types should have supply limits
			if config.MaxSupply <= 0 {
				t.Errorf("Asset type %s has no supply limit", assetType)
			}

			// All asset types should have decimal limits
			if config.MaxDecimals > 8 {
				t.Errorf("Asset type %s allows too many decimals: %d", assetType, config.MaxDecimals)
			}

			t.Logf("✅ Asset type %s: supply limit %d, decimal limit %d",
				assetType, config.MaxSupply, config.MaxDecimals)
		}
	})

	t.Run("Currency Terms Are Blocked", func(t *testing.T) {
		currencyTerms := []string{
			"coin", "token", "bitcoin", "crypto", "currency",
			"money", "cash", "dollar", "investment", "trading",
		}

		for _, term := range currencyTerms {
			op := &VMOperation{
				Type: "create_asset",
				Parameters: map[string]string{
					"name": "Test " + term + " Asset",
				},
			}

			err := vm.DetectCurrencyAttempt(op)
			if err == nil {
				t.Errorf("Currency term '%s' was not blocked", term)
			} else {
				t.Logf("✅ Currency term '%s' correctly blocked", term)
			}
		}
	})

	t.Run("Real World Reference Required", func(t *testing.T) {
		invalidReferences := []string{
			"",
			"token",
			"digital asset",
			"blockchain token",
			"investment vehicle",
		}

		for _, ref := range invalidReferences {
			err := vm.validateRealWorldReference(ref)
			if err == nil {
				t.Errorf("Invalid reference '%s' was not blocked", ref)
			} else {
				t.Logf("✅ Invalid reference correctly blocked: %s", ref)
			}
		}
	})
}
