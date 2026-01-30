// core/evm/gas_tracking_test.go
package evm

import (
	"math/big"
	"runtime"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/thrylos-labs/go-thrylos/config"
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

func TestMemoryTracking(t *testing.T) {
	executor := createTestExecutor(t)
	defer executor.Close()

	// Get initial leak count
	initialLeaks := executor.GetLeakCount()

	// Execute a call
	_, _, err := executor.ExecuteCall(
		common.HexToAddress("0x1"),
		common.HexToAddress("0x2"),
		[]byte{},
		1000000,
		big.NewInt(0),
		0,
	)

	// We expect an error since we don't have a real contract
	// but that's okay, we're testing memory
	_ = err

	// Check for leaks
	finalLeaks := executor.GetLeakCount()
	if finalLeaks != initialLeaks {
		t.Errorf("Memory leak detected: initial=%d, final=%d", initialLeaks, finalLeaks)
		executor.ReportMemoryStats()
	}
}

func TestNoMemoryLeaksOnError(t *testing.T) {
	executor := createTestExecutor(t)
	defer executor.Close()

	initialLeaks := executor.GetLeakCount()

	// Execute with invalid parameters to trigger errors
	for i := 0; i < 100; i++ {
		_, _, _ = executor.ExecuteCall(
			common.HexToAddress("0x0"),
			common.HexToAddress("0x0"),
			[]byte{},
			0, // Invalid gas limit
			big.NewInt(0),
			uint64(i),
		)
	}

	finalLeaks := executor.GetLeakCount()
	if finalLeaks != initialLeaks {
		t.Errorf("Memory leaked on errors: initial=%d, final=%d", initialLeaks, finalLeaks)
		executor.ReportMemoryStats()
	}
}

func TestMemoryStressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	executor := createTestExecutor(t)
	defer executor.Close()

	// Start memory health monitoring
	stopHealthCheck := executor.StartMemoryHealthCheck(5 * time.Second)
	defer close(stopHealthCheck)

	initialLeaks := executor.GetLeakCount()

	// Run for 30 seconds
	done := make(chan bool)
	go func() {
		time.Sleep(30 * time.Second)
		done <- true
	}()

	count := 0
	for {
		select {
		case <-done:
			t.Logf("Processed %d requests", count)

			finalLeaks := executor.GetLeakCount()
			if finalLeaks != initialLeaks {
				t.Errorf("Memory leaked during stress test: initial=%d, final=%d",
					initialLeaks, finalLeaks)
				executor.ReportMemoryStats()
			}
			return
		default:
			_, _, _ = executor.ExecuteCall(
				common.HexToAddress("0x1"),
				common.HexToAddress("0x2"),
				[]byte{0x60, 0x00, 0x60, 0x00}, // Simple bytecode
				1000000,
				big.NewInt(0),
				uint64(count),
			)
			count++
		}
	}
}

func TestMemoryHealthCheck(t *testing.T) {
	executor := createTestExecutor(t)
	defer executor.Close()

	// Should start healthy
	if err := executor.CheckMemoryHealth(); err != nil {
		t.Errorf("Memory unhealthy at start: %v", err)
	}

	// Execute some calls
	for i := 0; i < 100; i++ {
		_, _, _ = executor.ExecuteCall(
			common.HexToAddress("0x1"),
			common.HexToAddress("0x2"),
			[]byte{},
			1000000,
			big.NewInt(0),
			uint64(i),
		)
	}

	// Should still be healthy
	if err := executor.CheckMemoryHealth(); err != nil {
		t.Errorf("Memory unhealthy after executions: %v", err)
		executor.ReportMemoryStats()
	}
}

func TestMemoryReporting(t *testing.T) {
	executor := createTestExecutor(t)
	defer executor.Close()

	// This should not crash or panic
	executor.ReportMemoryStats()

	// Get individual stats
	errorMsgs := executor.GetTrackedErrorMessages()
	returnData := executor.GetTrackedReturnData()
	leaks := executor.GetLeakCount()

	t.Logf("Tracked error messages: %d", errorMsgs)
	t.Logf("Tracked return data: %d", returnData)
	t.Logf("Potential leaks: %d", leaks)

	// All should be 0 or very low
	if leaks > 10 {
		t.Errorf("Too many potential leaks: %d", leaks)
	}
}

func TestMemoryUnderConcurrentLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping concurrent test in short mode")
	}

	executor := createTestExecutor(t)
	defer executor.Close()

	initialLeaks := executor.GetLeakCount()

	// Run concurrent executions
	const workers = 10
	const opsPerWorker = 100

	done := make(chan bool, workers)

	for w := 0; w < workers; w++ {
		go func(workerID int) {
			for i := 0; i < opsPerWorker; i++ {
				_, _, _ = executor.ExecuteCall(
					common.HexToAddress("0x1"),
					common.HexToAddress("0x2"),
					[]byte{},
					1000000,
					big.NewInt(0),
					uint64(workerID*opsPerWorker+i),
				)
			}
			done <- true
		}(w)
	}

	// Wait for all workers
	for w := 0; w < workers; w++ {
		<-done
	}

	// Allow GC to run
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	finalLeaks := executor.GetLeakCount()
	if finalLeaks != initialLeaks {
		t.Errorf("Memory leaked under concurrent load: initial=%d, final=%d",
			initialLeaks, finalLeaks)
		executor.ReportMemoryStats()
	}
}

// Benchmark memory overhead
func BenchmarkMemoryTracking(b *testing.B) {
	executor := createTestExecutor(b)
	defer executor.Close()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _, _ = executor.ExecuteCall(
			common.HexToAddress("0x1"),
			common.HexToAddress("0x2"),
			[]byte{},
			1000000,
			big.NewInt(0),
			uint64(i),
		)
	}

	b.StopTimer()

	// Check for leaks after benchmark
	if leaks := executor.GetLeakCount(); leaks > 0 {
		b.Errorf("Memory leaked during benchmark: %d leaks", leaks)
	}
}

// Helper function to create a test executor
func createTestExecutor(t testing.TB) *RevmExecutor {
	// Create a mock StateReader
	worldState := &mockStateReader{}

	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: "1",
		},
	}

	executor, err := NewRevmExecutor(cfg, worldState)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	return executor
}

type mockStateReader struct{}

func (m *mockStateReader) GetBalance(address string) (*big.Int, error) {
	return big.NewInt(0), nil
}

func (m *mockStateReader) GetNonce(address string) (uint64, error) {
	return 0, nil
}

func (m *mockStateReader) GetContractCode(address string) ([]byte, error) {
	return []byte{}, nil
}

func (m *mockStateReader) GetContractStorage(address, key string) ([]byte, error) {
	return []byte{}, nil
}

func (m *mockStateReader) AtomicIncrementNonce(address string, expectedNonce uint64) (bool, uint64, error) {
	return true, expectedNonce + 1, nil
}
