// core/evm/gas_tracking_test.go
package evm

import (
	"testing"
)

// Test gas tracking without CGO dependencies
func TestGasLimitEnforcement(t *testing.T) {
	executor := &RevmExecutor{
		blockGasLimit: 30000000,
		blockGasUsed:  0,
	}

	// Test 1: Should accept transaction within limit
	err := executor.CheckAndReserveGas(1000000)
	if err != nil {
		t.Errorf("Should accept gas within limit: %v", err)
	}

	// Verify gas was reserved
	if executor.blockGasUsed != 1000000 {
		t.Errorf("Expected 1000000 gas used, got %d", executor.blockGasUsed)
	}

	// Test 2: Should reject when exceeding block limit
	err = executor.CheckAndReserveGas(30000000) // Would exceed (already used 1M)
	if err == nil {
		t.Error("Should reject transaction exceeding block gas limit")
	}
}

func TestGasRefund(t *testing.T) {
	executor := &RevmExecutor{
		blockGasLimit: 30000000,
		blockGasUsed:  1000000,
	}

	// Refund 500k gas
	executor.RefundGas(500000)

	if executor.blockGasUsed != 500000 {
		t.Errorf("Expected 500000 gas used, got %d", executor.blockGasUsed)
	}
}

func TestResetBlockGas(t *testing.T) {
	executor := &RevmExecutor{
		blockGasLimit: 30000000,
		blockGasUsed:  5000000,
	}

	// Reset for new block
	executor.ResetBlockGas(40000000)

	if executor.blockGasUsed != 0 {
		t.Errorf("Expected 0 gas used after reset, got %d", executor.blockGasUsed)
	}

	if executor.blockGasLimit != 40000000 {
		t.Errorf("Expected 40000000 gas limit, got %d", executor.blockGasLimit)
	}
}

func TestGasRefundUnderflow(t *testing.T) {
	executor := &RevmExecutor{
		blockGasLimit: 30000000,
		blockGasUsed:  100,
	}

	// Refund more than used
	executor.RefundGas(500)

	// Should not underflow
	if executor.blockGasUsed != 0 {
		t.Errorf("Expected 0 after refund underflow, got %d", executor.blockGasUsed)
	}
}
