package evm

import (
	"crypto/rand"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/thrylos-labs/go-thrylos/config"
)

// MockStateReader for fuzzing context
type MockStateReader struct{}

func (m *MockStateReader) GetBalance(address string) (*big.Int, error) {
	return big.NewInt(1000000000000000000), nil // 1 ETH
}
func (m *MockStateReader) GetNonce(address string) (uint64, error) {
	return 0, nil
}
func (m *MockStateReader) GetContractCode(address string) ([]byte, error) {
	return []byte{}, nil
}
func (m *MockStateReader) GetContractStorage(address, key string) ([]byte, error) {
	return make([]byte, 32), nil
}

func TestRevmExecutor_Fuzz_FFI(t *testing.T) {
	// Initialize Executor with Mock State
	cfg := &config.Config{Network: config.NetworkConfig{ChainID: "1"}}
	executor, err := NewRevmExecutor(cfg, &MockStateReader{})
	assert.NoError(t, err)
	defer executor.Close()

	// 1. Fuzz DeployContract
	t.Run("Fuzz_DeployContract", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			// Generate random bytecode of random length (0 to 10KB)
			lenBytecode := randInt(10000)
			bytecode := make([]byte, lenBytecode)
			rand.Read(bytecode)

			// Generate random addresses
			deployer := common.BytesToAddress(randBytes(20))

			// Execute
			addr, gas, err := executor.DeployContract(
				deployer,
				bytecode,
				uint64(randInt(10000000)), // Random gas limit
				big.NewInt(0),
			)

			// We expect errors for garbage bytecode, but NOT crashes (panics)
			// If err is nil, it means revm actually executed the random bytes validly (rare but possible)
			if err != nil {
				assert.NotContains(t, err.Error(), "panic", "FFI panic detected")
				assert.NotContains(t, err.Error(), "segmentation violation", "Segfault detected")
			} else {
				assert.NotNil(t, addr)
				assert.GreaterOrEqual(t, gas, uint64(0))
			}
		}
	})

	// 2. Fuzz ExecuteCall
	t.Run("Fuzz_ExecuteCall", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			// Generate random calldata (0 to 1MB to test buffer limits)
			lenData := randInt(1024 * 1024)
			inputData := make([]byte, lenData)
			rand.Read(inputData)

			caller := common.BytesToAddress(randBytes(20))
			contract := common.BytesToAddress(randBytes(20))

			// Execute
			ret, gas, err := executor.ExecuteCall(
				caller,
				contract,
				inputData,
				uint64(randInt(10000000)),
				big.NewInt(randInt(1000)),
			)

			// Validation: Must not crash
			if err != nil {
				// Ensure error messages are safe strings
				assert.NotEmpty(t, err.Error())
			} else {
				// If success, return data should be valid
				assert.GreaterOrEqual(t, gas, uint64(0))
				assert.NotNil(t, ret)
			}
		}
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
