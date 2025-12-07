// core/evm/executor.go
// EVM execution engine for Thrylos blockchain
// This provides Ethereum compatibility and MetaMask support

package evm

import (
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/state"
)

// EVMExecutor wraps go-ethereum's EVM for Thrylos
type EVMExecutor struct {
	config   *config.Config
	chainCfg *params.ChainConfig
	vmConfig vm.Config
	stateDB  vm.StateDB
}

// NewEVMExecutor creates a new EVM executor
func NewEVMExecutor(cfg *config.Config, worldState *state.WorldState) *EVMExecutor {
	// Create Ethereum-compatible chain config
	chainCfg := &params.ChainConfig{
		ChainID:             big.NewInt(int64(cfg.Network.ChainID)), // Use your chain ID
		HomesteadBlock:      big.NewInt(0),
		EIP150Block:         big.NewInt(0),
		EIP155Block:         big.NewInt(0),
		EIP158Block:         big.NewInt(0),
		ByzantiumBlock:      big.NewInt(0),
		ConstantinopleBlock: big.NewInt(0),
		PetersburgBlock:     big.NewInt(0),
		IstanbulBlock:       big.NewInt(0),
		BerlinBlock:         big.NewInt(0),
		LondonBlock:         big.NewInt(0),
	}

	// VM configuration
	vmConfig := vm.Config{
		EnablePreimageRecording: false,
		// Add debugging options if needed
	}

	// Create state adapter (bridges Thrylos state to EVM state)
	stateAdapter := NewStateAdapter(worldState)

	return &EVMExecutor{
		config:   cfg,
		chainCfg: chainCfg,
		vmConfig: vmConfig,
		stateDB:  stateAdapter,
	}
}

// ExecuteCall executes a smart contract call
func (e *EVMExecutor) ExecuteCall(
	caller common.Address,
	contract common.Address,
	input []byte,
	gas uint64,
	value *big.Int,
	blockNumber *big.Int,
) ([]byte, uint64, error) {

	// Create EVM context
	blockContext := vm.BlockContext{
		CanTransfer: core.CanTransfer,
		Transfer:    core.Transfer,
		GetHash:     e.getHashFunc(blockNumber),
		Coinbase:    common.Address{}, // Set to block proposer
		BlockNumber: blockNumber,
		Time:        uint64(time.Now().Unix()),
		Difficulty:  big.NewInt(0), // PoS = 0 difficulty
		GasLimit:    e.config.Economics.MaxBlockGas,
		BaseFee:     big.NewInt(e.config.Economics.BaseGasPrice),
	}

	txContext := vm.TxContext{
		Origin:   caller,
		GasPrice: big.NewInt(e.config.Economics.BaseGasPrice),
	}

	// Create EVM instance
	evm := vm.NewEVM(blockContext, txContext, e.stateDB, e.chainCfg, e.vmConfig)

	// Execute the call
	ret, gasLeft, err := evm.Call(
		vm.AccountRef(caller),
		contract,
		input,
		gas,
		value,
	)

	gasUsed := gas - gasLeft

	return ret, gasUsed, err
}

// DeployContract deploys a new smart contract
func (e *EVMExecutor) DeployContract(
	deployer common.Address,
	bytecode []byte,
	constructorArgs []byte,
	gas uint64,
	value *big.Int,
	blockNumber *big.Int,
) (contractAddress common.Address, gasUsed uint64, err error) {

	// Get deployer nonce
	nonce := e.stateDB.GetNonce(deployer)

	// Calculate contract address (Ethereum CREATE method)
	contractAddress = crypto.CreateAddress(deployer, nonce)

	// Create EVM context
	blockContext := vm.BlockContext{
		CanTransfer: core.CanTransfer,
		Transfer:    core.Transfer,
		GetHash:     e.getHashFunc(blockNumber),
		Coinbase:    common.Address{},
		BlockNumber: blockNumber,
		Time:        uint64(time.Now().Unix()),
		Difficulty:  big.NewInt(0),
		GasLimit:    e.config.Economics.MaxBlockGas,
		BaseFee:     big.NewInt(e.config.Economics.BaseGasPrice),
	}

	txContext := vm.TxContext{
		Origin:   deployer,
		GasPrice: big.NewInt(e.config.Economics.BaseGasPrice),
	}

	// Create EVM
	evm := vm.NewEVM(blockContext, txContext, e.stateDB, e.chainCfg, e.vmConfig)

	// Combine bytecode and constructor args
	code := append(bytecode, constructorArgs...)

	// Deploy contract
	_, contractAddr, gasLeft, err := evm.Create(
		vm.AccountRef(deployer),
		code,
		gas,
		value,
	)

	if err != nil {
		return common.Address{}, 0, fmt.Errorf("contract deployment failed: %v", err)
	}

	gasUsed = gas - gasLeft

	// Increment nonce
	e.stateDB.SetNonce(deployer, nonce+1)

	return contractAddr, gasUsed, nil
}

// StaticCall executes a read-only call (doesn't modify state)
func (e *EVMExecutor) StaticCall(
	caller common.Address,
	contract common.Address,
	input []byte,
	gas uint64,
	blockNumber *big.Int,
) ([]byte, uint64, error) {

	// Create EVM context
	blockContext := vm.BlockContext{
		CanTransfer: core.CanTransfer,
		Transfer:    core.Transfer,
		GetHash:     e.getHashFunc(blockNumber),
		Coinbase:    common.Address{},
		BlockNumber: blockNumber,
		Time:        uint64(time.Now().Unix()),
		Difficulty:  big.NewInt(0),
		GasLimit:    e.config.Economics.MaxBlockGas,
		BaseFee:     big.NewInt(e.config.Economics.BaseGasPrice),
	}

	txContext := vm.TxContext{
		Origin:   caller,
		GasPrice: big.NewInt(e.config.Economics.BaseGasPrice),
	}

	// Create EVM
	evm := vm.NewEVM(blockContext, txContext, e.stateDB, e.chainCfg, e.vmConfig)

	// Execute static call (read-only, reverts state changes)
	ret, gasLeft, err := evm.StaticCall(
		vm.AccountRef(caller),
		contract,
		input,
		gas,
	)

	gasUsed := gas - gasLeft

	return ret, gasUsed, err
}

// EstimateGas estimates gas needed for a transaction
func (e *EVMExecutor) EstimateGas(
	from common.Address,
	to *common.Address,
	data []byte,
	value *big.Int,
) (uint64, error) {

	// Start with a reasonable gas amount
	gas := uint64(e.config.Economics.MaxGasPerTx)

	blockNumber := big.NewInt(0) // Latest block

	var err error
	if to == nil {
		// Contract deployment
		_, gas, err = e.DeployContract(from, data, nil, gas, value, blockNumber)
	} else {
		// Contract call
		_, gas, err = e.ExecuteCall(from, *to, data, gas, value, blockNumber)
	}

	if err != nil {
		return 0, fmt.Errorf("gas estimation failed: %v", err)
	}

	// Add 10% buffer for safety
	estimatedGas := gas + (gas / 10)

	// Cap at max gas
	if estimatedGas > uint64(e.config.Economics.MaxGasPerTx) {
		estimatedGas = uint64(e.config.Economics.MaxGasPerTx)
	}

	return estimatedGas, nil
}

// getHashFunc returns a function to get block hash
func (e *EVMExecutor) getHashFunc(blockNumber *big.Int) func(n uint64) common.Hash {
	return func(n uint64) common.Hash {
		// TODO: Implement actual block hash lookup from your blockchain
		// For now, return empty hash
		return common.Hash{}
	}
}

// ValidateEVMTransaction validates an EVM transaction
func (e *EVMExecutor) ValidateEVMTransaction(
	from common.Address,
	to *common.Address,
	data []byte,
	gas uint64,
	gasPrice *big.Int,
	value *big.Int,
	nonce uint64,
) error {

	// Check gas limits
	if gas < 21000 {
		return fmt.Errorf("gas too low: minimum 21000, got %d", gas)
	}

	if gas > uint64(e.config.Economics.MaxGasPerTx) {
		return fmt.Errorf("gas too high: maximum %d, got %d",
			e.config.Economics.MaxGasPerTx, gas)
	}

	// Check gas price
	if gasPrice.Cmp(big.NewInt(e.config.Economics.BaseGasPrice)) < 0 {
		return fmt.Errorf("gas price too low: minimum %d, got %s",
			e.config.Economics.BaseGasPrice, gasPrice.String())
	}

	// Check sender balance
	balance := e.stateDB.GetBalance(from)
	totalCost := new(big.Int).Mul(gasPrice, big.NewInt(int64(gas)))
	totalCost = totalCost.Add(totalCost, value)

	if balance.Cmp(totalCost) < 0 {
		return fmt.Errorf("insufficient balance: have %s, need %s",
			balance.String(), totalCost.String())
	}

	// Check nonce
	expectedNonce := e.stateDB.GetNonce(from)
	if nonce != expectedNonce {
		return fmt.Errorf("invalid nonce: expected %d, got %d",
			expectedNonce, nonce)
	}

	return nil
}

// GetCode returns the code at a given address
func (e *EVMExecutor) GetCode(address common.Address) []byte {
	return e.stateDB.GetCode(address)
}

// GetCodeHash returns the code hash at a given address
func (e *EVMExecutor) GetCodeHash(address common.Address) common.Hash {
	return e.stateDB.GetCodeHash(address)
}

// GetStorageAt returns storage value at a specific key
func (e *EVMExecutor) GetStorageAt(address common.Address, key common.Hash) common.Hash {
	return e.stateDB.GetState(address, key)
}
