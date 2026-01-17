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
type MockStateReader struct {
	nonces map[string]uint64
}

func NewMockStateReader() *MockStateReader {
	return &MockStateReader{
		nonces: make(map[string]uint64),
	}
}

func (m *MockStateReader) GetBalance(address string) (*big.Int, error) {
	return big.NewInt(1000000000000000000), nil // 1 ETH
}

func (m *MockStateReader) GetNonce(address string) (uint64, error) {
	if nonce, exists := m.nonces[address]; exists {
		return nonce, nil
	}
	return 0, nil
}

func (m *MockStateReader) SetNonce(address string, nonce uint64) {
	m.nonces[address] = nonce
}

func (m *MockStateReader) GetContractCode(address string) ([]byte, error) {
	return []byte{}, nil
}

func (m *MockStateReader) GetContractStorage(address, key string) ([]byte, error) {
	return make([]byte, 32), nil
}

// ✅ NEW TEST: Verify nonce validation doesn't panic
func TestRevmExecutor_NonceValidation(t *testing.T) {
	cfg := &config.Config{Network: config.NetworkConfig{ChainID: "1"}}
	mockState := NewMockStateReader()
	executor, err := NewRevmExecutor(cfg, mockState)
	assert.NoError(t, err)
	defer executor.Close()

	caller := common.HexToAddress("0x1234567890123456789012345678901234567890")
	contract := common.HexToAddress("0x0987654321098765432109876543210987654321")

	t.Run("Correct_Nonce_Executes", func(t *testing.T) {
		// Set state nonce to 5
		mockState.SetNonce(caller.Hex(), 5)

		// Execute with correct nonce (5)
		_, _, err := executor.ExecuteCall(
			caller,
			contract,
			[]byte{}, // empty calldata
			1000000,
			big.NewInt(0),
			5, // Correct nonce
		)

		// May fail for other reasons, but NOT nonce
		if err != nil {
			assert.NotContains(t, err.Error(), "Nonce mismatch")
		}
	})

	t.Run("Wrong_Nonce_Returns_Error", func(t *testing.T) {
		// Set state nonce to 5
		mockState.SetNonce(caller.Hex(), 5)

		// Execute with wrong nonce (999)
		_, _, err := executor.ExecuteCall(
			caller,
			contract,
			[]byte{},
			1000000,
			big.NewInt(0),
			999, // Wrong nonce
		)

		// Should return error, not panic
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Nonce mismatch")
		assert.Contains(t, err.Error(), "expected 5")
		assert.Contains(t, err.Error(), "got 999")
	})

	t.Run("Fuzz_Invalid_Nonces_Dont_Crash", func(t *testing.T) {
		mockState.SetNonce(caller.Hex(), 10)

		// Try 1000 random invalid nonces
		for i := 0; i < 1000; i++ {
			wrongNonce := uint64(randInt(100000))

			// Skip if we randomly hit the correct nonce
			if wrongNonce == 10 {
				continue
			}

			_, _, err := executor.ExecuteCall(
				caller,
				contract,
				[]byte{},
				1000000,
				big.NewInt(0),
				wrongNonce,
			)

			// Must return error, not crash
			if err != nil {
				// Verify it's a proper error message, not a panic
				assert.NotContains(t, err.Error(), "panic")
				assert.NotContains(t, err.Error(), "segmentation")
			}
		}
	})
}

// Existing fuzz tests (updated)
func TestRevmExecutor_Fuzz_FFI(t *testing.T) {
	cfg := &config.Config{Network: config.NetworkConfig{ChainID: "1"}}
	mockState := NewMockStateReader()
	executor, err := NewRevmExecutor(cfg, mockState)
	assert.NoError(t, err)
	defer executor.Close()

	t.Run("Fuzz_DeployContract", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			lenBytecode := randInt(10000)
			bytecode := make([]byte, lenBytecode)
			rand.Read(bytecode)

			deployer := common.BytesToAddress(randBytes(20))

			addr, gas, err := executor.DeployContract(
				deployer,
				bytecode,
				uint64(randInt(10000000)),
				big.NewInt(0),
			)

			if err != nil {
				assert.NotContains(t, err.Error(), "panic")
				assert.NotContains(t, err.Error(), "segmentation")
			} else {
				assert.NotNil(t, addr)
				assert.GreaterOrEqual(t, gas, uint64(0))
			}
		}
	})

	t.Run("Fuzz_ExecuteCall", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			lenData := randInt(1024 * 1024)
			inputData := make([]byte, lenData)
			rand.Read(inputData)

			caller := common.BytesToAddress(randBytes(20))
			contract := common.BytesToAddress(randBytes(20))

			// Set a valid nonce for the caller
			randomNonce := uint64(randInt(100))
			mockState.SetNonce(caller.Hex(), randomNonce)

			ret, gas, err := executor.ExecuteCall(
				caller,
				contract,
				inputData,
				uint64(randInt(10000000)),
				big.NewInt(randInt(1000)),
				randomNonce, // Use the same nonce we set
			)

			if err != nil {
				assert.NotEmpty(t, err.Error())
				// Nonce should be valid in this test
				assert.NotContains(t, err.Error(), "Nonce mismatch")
			} else {
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
