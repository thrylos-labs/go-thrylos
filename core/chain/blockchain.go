package chain

import (
	"fmt"
	"sync"

	"github.com/thrylos-labs/go-thrylos/config"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

type Blockchain struct {
	// Core state
	blocks   []*core.Block
	accounts map[string]*core.Account

	// Configuration
	config *config.Config

	// Synchronization
	mu sync.RWMutex

	// Genesis
	genesis *core.Block
}

func NewBlockchain(cfg *config.Config) (*Blockchain, error) {
	bc := &Blockchain{
		blocks:   make([]*core.Block, 0),
		accounts: make(map[string]*core.Account),
		config:   cfg,
	}

	// Create genesis block
	genesis, err := bc.createGenesisBlock()
	if err != nil {
		return nil, fmt.Errorf("failed to create genesis block: %v", err)
	}

	bc.genesis = genesis
	bc.blocks = append(bc.blocks, genesis)

	return bc, nil
}

func (bc *Blockchain) createGenesisBlock() (*core.Block, error) {
	// Create genesis account with initial supply
	genesisAccount := &core.Account{
		Address:      "tl1genesis000000000000000000000000000000000",
		Balance:      120000000000000000, // 120M THRYLOS
		Nonce:        0,
		StakedAmount: 0,
		DelegatedTo:  make(map[string]int64),
		Rewards:      0,
	}

	bc.accounts[genesisAccount.Address] = genesisAccount

	// Create genesis block
	genesis := &core.Block{
		Header: &core.BlockHeader{
			Index:     0,
			PrevHash:  "",
			Timestamp: 1640995200, // Jan 1, 2022
			Validator: genesisAccount.Address,
			TxRoot:    "",
			StateRoot: "",
			GasUsed:   0,
			GasLimit:  1000000,
		},
		Transactions: []*core.Transaction{}, // No transactions in genesis
		Hash:         "genesis_block_hash",
		Signature:    []byte("genesis_signature"),
	}

	return genesis, nil
}

func (bc *Blockchain) Status() string {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	return fmt.Sprintf("Height: %d, Accounts: %d",
		len(bc.blocks)-1, len(bc.accounts))
}

func (bc *Blockchain) GetAccount(address string) *core.Account {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	if account, exists := bc.accounts[address]; exists {
		return account
	}

	// Return new account if doesn't exist
	return &core.Account{
		Address:      address,
		Balance:      0,
		Nonce:        0,
		StakedAmount: 0,
		DelegatedTo:  make(map[string]int64),
		Rewards:      0,
	}
}

func (bc *Blockchain) GetBlockCount() int {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return len(bc.blocks)
}
