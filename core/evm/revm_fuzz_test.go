package evm

import (
	"crypto/rand"
	"math"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrylos-labs/go-thrylos/config"
)

// MockStateReader for testing
type MockStateReader struct {
	nonces             map[string]uint64
	nonceMu            sync.Mutex
	balances           map[string]*big.Int
	nonceCheckCount    int64
	nonceSuccessCount  int64
	nonceRejectedCount int64
}

func NewMockStateReader() *MockStateReader {
	return &MockStateReader{
		nonces:   make(map[string]uint64),
		balances: make(map[string]*big.Int),
	}
}

func (m *MockStateReader) GetBalance(address string) (*big.Int, error) {
	m.nonceMu.Lock()
	defer m.nonceMu.Unlock()
	if bal, exists := m.balances[address]; exists {
		return new(big.Int).Set(bal), nil
	}
	return big.NewInt(1000000000000000000), nil
}

func (m *MockStateReader) GetNonce(address string) (uint64, error) {
	m.nonceMu.Lock()
	defer m.nonceMu.Unlock()
	if nonce, exists := m.nonces[address]; exists {
		return nonce, nil
	}
	return 0, nil
}

func (m *MockStateReader) AtomicIncrementNonce(address string, expectedNonce uint64) (success bool, currentNonce uint64, err error) {
	m.nonceMu.Lock()
	defer m.nonceMu.Unlock()

	atomic.AddInt64(&m.nonceCheckCount, 1)

	if nonce, exists := m.nonces[address]; exists {
		currentNonce = nonce
	} else {
		currentNonce = 0
	}

	if currentNonce != expectedNonce {
		atomic.AddInt64(&m.nonceRejectedCount, 1)
		return false, currentNonce, nil
	}

	m.nonces[address] = currentNonce + 1
	atomic.AddInt64(&m.nonceSuccessCount, 1)
	return true, currentNonce, nil
}

func (m *MockStateReader) SetNonce(address string, nonce uint64) {
	m.nonceMu.Lock()
	defer m.nonceMu.Unlock()
	m.nonces[address] = nonce
}

func (m *MockStateReader) GetContractCode(address string) ([]byte, error) {
	return []byte{}, nil
}

func (m *MockStateReader) GetContractStorage(address, key string) ([]byte, error) {
	return make([]byte, 32), nil
}

func (m *MockStateReader) GetStats() (checks, successes, rejected int64) {
	return atomic.LoadInt64(&m.nonceCheckCount),
		atomic.LoadInt64(&m.nonceSuccessCount),
		atomic.LoadInt64(&m.nonceRejectedCount)
}

func (m *MockStateReader) ResetStats() {
	atomic.StoreInt64(&m.nonceCheckCount, 0)
	atomic.StoreInt64(&m.nonceSuccessCount, 0)
	atomic.StoreInt64(&m.nonceRejectedCount, 0)
}

// ============================================================================
// FINAL TEST: Focus on Atomic Nonce Behavior Only
// ============================================================================

func TestAtomicNonce_Final(t *testing.T) {
	cfg := &config.Config{Network: config.NetworkConfig{ChainID: "1"}}
	mockState := NewMockStateReader()
	executor, err := NewRevmExecutor(cfg, mockState)
	require.NoError(t, err)
	defer executor.Close()

	caller := common.HexToAddress("0x1234567890123456789012345678901234567890")
	contract := common.HexToAddress("0x0987654321098765432109876543210987654321")

	t.Run("✅_AtomicIncrementNonce_Works", func(t *testing.T) {
		mockState.ResetStats()
		mockState.SetNonce(caller.Hex(), 0)

		// Call ExecuteCall - we only care about nonce checking, not EVM execution
		executor.ExecuteCall(caller, contract, []byte{}, 1000000, big.NewInt(0), 0)

		checks, successes, _ := mockState.GetStats()
		finalNonce, _ := mockState.GetNonce(caller.Hex())

		t.Logf("Checks: %d, Successes: %d, Final nonce: %d", checks, successes, finalNonce)

		assert.Greater(t, checks, int64(0), "AtomicIncrementNonce must be called")
		assert.Equal(t, int64(1), successes, "Nonce check should succeed once")
		assert.Equal(t, uint64(1), finalNonce, "Nonce should increment to 1")
	})

	t.Run("✅_CRITICAL_RaceCondition_Prevented", func(t *testing.T) {
		mockState.ResetStats()
		mockState.SetNonce(caller.Hex(), 0)

		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				executor.ExecuteCall(caller, contract, []byte{}, 1000000, big.NewInt(0), 0)
			}()
		}
		wg.Wait()

		checks, successes, rejected := mockState.GetStats()
		finalNonce, _ := mockState.GetNonce(caller.Hex())

		t.Logf("Race test: %d checks, %d successes, %d rejected, final nonce: %d",
			checks, successes, rejected, finalNonce)

		// CRITICAL ASSERTIONS
		assert.Equal(t, int64(100), checks, "All 100 should attempt nonce check")
		assert.Equal(t, int64(1), successes, "CRITICAL: Only 1 should succeed (prevents double-spend)")
		assert.Equal(t, int64(99), rejected, "99 should be rejected")
		assert.Equal(t, uint64(1), finalNonce, "Nonce should be 1")

		if successes > 1 {
			t.Fatalf("🚨 CRITICAL BUG: %d nonce checks succeeded! DOUBLE-SPEND POSSIBLE!", successes)
		}

		t.Log("✅ PASS: Race condition prevented - only 1 of 100 succeeded")
	})

	t.Run("✅_Wrong_Nonce_Rejected", func(t *testing.T) {
		mockState.ResetStats()
		mockState.SetNonce(caller.Hex(), 5)

		executor.ExecuteCall(caller, contract, []byte{}, 1000000, big.NewInt(0), 10)

		checks, successes, rejected := mockState.GetStats()
		finalNonce, _ := mockState.GetNonce(caller.Hex())

		t.Logf("Wrong nonce test: checks=%d, successes=%d, rejected=%d, final nonce=%d",
			checks, successes, rejected, finalNonce)

		assert.Equal(t, int64(1), checks, "Should check nonce")
		assert.Equal(t, int64(0), successes, "Should not succeed")
		assert.Equal(t, int64(1), rejected, "Should be rejected")
		assert.Equal(t, uint64(5), finalNonce, "Nonce should not change")
	})

	t.Run("✅_Sequential_Nonces_Increment_Correctly", func(t *testing.T) {
		mockState.ResetStats()
		mockState.SetNonce(caller.Hex(), 0)

		// Make 10 calls with sequential nonces
		for nonce := uint64(0); nonce < 10; nonce++ {
			executor.ExecuteCall(caller, contract, []byte{}, 1000000, big.NewInt(0), nonce)
		}

		checks, successes, rejected := mockState.GetStats()
		finalNonce, _ := mockState.GetNonce(caller.Hex())

		t.Logf("Sequential test: %d checks, %d successes, %d rejected, final nonce: %d",
			checks, successes, rejected, finalNonce)

		assert.Equal(t, int64(10), checks, "Should check all 10 nonces")
		assert.Equal(t, int64(10), successes, "All 10 should succeed")
		assert.Equal(t, int64(0), rejected, "None should be rejected")
		assert.Equal(t, uint64(10), finalNonce, "Final nonce should be 10")

		t.Log("✅ PASS: Sequential nonces work correctly")
	})

	t.Run("✅_Deployment_Uses_Atomic_Nonce", func(t *testing.T) {
		deployer := common.HexToAddress("0xDEPLOYER")
		mockState.ResetStats()
		mockState.SetNonce(deployer.Hex(), 0)

		bytecode := []byte{0x60, 0x00, 0x60, 0x00, 0xf3}
		executor.DeployContract(deployer, bytecode, 1000000, big.NewInt(0), 0)

		checks, _, _ := mockState.GetStats()
		assert.Greater(t, checks, int64(0), "Deployment must use atomic nonce")
	})
}

// ============================================================================
// BENCHMARK
// ============================================================================

func BenchmarkAtomicNonce(b *testing.B) {
	cfg := &config.Config{Network: config.NetworkConfig{ChainID: "1"}}
	mockState := NewMockStateReader()
	executor, _ := NewRevmExecutor(cfg, mockState)
	defer executor.Close()

	caller := common.HexToAddress("0x1234")
	contract := common.HexToAddress("0x5678")
	mockState.SetNonce(caller.Hex(), 0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		executor.ExecuteCall(caller, contract, []byte{}, 1000000, big.NewInt(0), uint64(i))
	}
}

// ============================================================================
// INPUT VALIDATION
// ============================================================================

func TestRevmExecutor_InputValidation(t *testing.T) {
	cfg := &config.Config{Network: config.NetworkConfig{ChainID: "1"}}
	mockState := NewMockStateReader()
	executor, err := NewRevmExecutor(cfg, mockState)
	assert.NoError(t, err)
	defer executor.Close()

	caller := common.HexToAddress("0x1234")
	contract := common.HexToAddress("0x5678")
	mockState.SetNonce(caller.Hex(), 0)

	t.Run("Rejects_Excessive_Gas", func(t *testing.T) {
		_, _, err := executor.ExecuteCall(
			caller, contract, []byte{}, uint64(math.MaxUint64), big.NewInt(0), 0,
		)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "gas limit")
	})
}

// Helpers
func randInt(max int64) int64 {
	n, _ := rand.Int(rand.Reader, big.NewInt(max))
	return n.Int64()
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}
