package transaction

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/thrylos-labs/go-thrylos/proto/core"
)

// StateInterface defines all methods required by Validator and Executor
// This interface allows us to break the dependency cycle with core/state
type StateInterface interface {
	// Account access (Required by Validator & Executor)
	GetAccount(address string) (*core.Account, error)
	GetBalance(address string) (int64, error)
	GetNonce(address string) (uint64, error)

	// State updates (Required by Executor)
	UpdateBalance(address string, amount int64) error
	SetNonce(address string, nonce uint64) error

	// EVM specific (Required by Executor)
	GetContractCode(address string) ([]byte, error)
	GetContractStorage(address, key string) ([]byte, error)
	SetContractStorage(address, key string, value []byte) error
}

// EVMExecutorInterface defines interaction with the EVM engine
type EVMExecutorInterface interface {
	ExecuteCall(caller common.Address, contract common.Address, input []byte, gas uint64, value *big.Int) ([]byte, uint64, error)
	DeployContract(deployer common.Address, bytecode []byte, gas uint64, value *big.Int) (common.Address, uint64, error)
	Close()
}
