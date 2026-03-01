package chain

import (
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/core/math"
	"github.com/thrylos-labs/go-thrylos/core/state"
	"github.com/thrylos-labs/go-thrylos/crypto"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
)

func TestReorgDoubleExecution(t *testing.T) {
	// 1. Setup
	tmpDir, err := os.MkdirTemp("", "thrylos-reorg-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	badgerStore, err := storage.NewBadgerStorage(tmpDir)
	require.NoError(t, err)
	defer badgerStore.Close()

	cfg := &config.Config{
		Consensus: config.ConsensusConfig{
			MaxBlockSize:       1000000,
			MaxTxPerBlock:      100,
			MinGasPrice:        "1",
			MaxFutureBlockTime: 15 * time.Second,
		},
		Economics: config.EconomicsConfig{
			BaseGasPrice: "1",
		},
	}

	ws, err := state.NewWorldState(tmpDir, 0, 1, cfg, badgerStore)
	require.NoError(t, err)

	bcConfig := &BlockchainConfig{
		Config:        cfg,
		WorldState:    ws,
		ShardID:       0,
		TotalShards:   1,
		MaxReorgDepth: 100,
	}
	bc, err := NewBlockchain(bcConfig)
	require.NoError(t, err)

	// 2. Create Alice with balance
	alicePrivKey, _ := crypto.NewPrivateKey()
	aliceAddr, _ := account.GenerateAddress(alicePrivKey.PublicKey())

	// ✅ Create recipient with proper address
	recipientPrivKey, _ := crypto.NewPrivateKey()
	recipientAddr, _ := account.GenerateAddress(recipientPrivKey.PublicKey())

	initialBalanceStr := "100000"
	ws.GetAccountManager().UpdateAccount(&core.Account{
		Address: aliceAddr,
		Balance: math.ParseBigInt(initialBalanceStr).Bytes(),
		Nonce:   0,
	})

	// ✅ Create recipient account (needed for transaction execution)
	ws.GetAccountManager().UpdateAccount(&core.Account{
		Address: recipientAddr,
		Balance: nil,
		Nonce:   0,
	})

	// 3. Create Tx: Alice sends 1000 tokens
	txAmountStr := "1000"
	txGas := int64(21000)
	txGasPriceStr := "1"

	tx := &core.Transaction{
		Id:        "tx1",
		Hash:      "0xTx1Hash",
		From:      aliceAddr,
		To:        recipientAddr, // ✅ Use proper address
		Amount:    math.ParseBigInt(txAmountStr).Bytes(),
		Gas:       txGas,
		GasPrice:  math.ParseBigInt(txGasPriceStr).Bytes(),
		Nonce:     0,
		Timestamp: time.Now().Unix(),
		Signature: []byte("mock-signature"),
	}

	// Genesis setup
	genesisTime := time.Now().Unix()
	genesis := &core.Block{
		Header: &core.BlockHeader{
			Index:     0,
			Timestamp: genesisTime,
			Validator: "0xGenesisValidator",
		},
		Hash: "0xGenesis",
	}
	ws.AddBlock(genesis)

	// ✅ Ensure new block timestamp is AFTER genesis
	newBlock := &core.Block{
		Header: &core.BlockHeader{
			Index:     1,
			PrevHash:  "0xGenesis",
			Timestamp: genesisTime + 1, // At least 1 second later
			Validator: "0xValidator",
			GasLimit:  10000000,
			GasUsed:   txGas,
		},
		Transactions: []*core.Transaction{tx},
		Hash:         "0xForkBlock",
	}

	// 4. Execute Reorg
	err = bc.ReorganizeChain([]*core.Block{newBlock})

	// Validation
	if err == nil {
		updatedAccount, _ := ws.GetAccount(aliceAddr)

		// Calculate Expected Balance using BigInt Math
		initialBalBig := math.ParseBigInt(initialBalanceStr)
		amountBig := math.ParseBigInt(txAmountStr)
		gasPriceBig := math.ParseBigInt(txGasPriceStr)
		gasLimitBig := big.NewInt(txGas)

		// Total Cost = Amount + (Gas * GasPrice)
		gasCostBig := new(big.Int).Mul(gasLimitBig, gasPriceBig)
		totalCostBig := new(big.Int).Add(amountBig, gasCostBig)

		// Expected = Initial - TotalCost
		expectedBalBig := new(big.Int).Sub(initialBalBig, totalCostBig)

		// Actual Balance (from string)
		actualBalBig := math.ParseBigInt(updatedAccount.Balance)

		// Compare using BigInt
		assert.Equal(t, expectedBalBig.String(), actualBalBig.String(),
			"Balance should decrease by exactly one tx cost.")

		// Check for double spend (Initial - 2*Cost)
		doubleCostBig := new(big.Int).Mul(totalCostBig, big.NewInt(2))
		doubleSpendBalBig := new(big.Int).Sub(initialBalBig, doubleCostBig)

		if actualBalBig.Cmp(doubleSpendBalBig) == 0 {
			t.Fatal("❌ CRITICAL FAILURE: Transaction was executed TWICE!")
		} else {
			t.Log("✅ Success: Transaction executed exactly once.")
		}
	} else {
		t.Fatalf("Reorg failed unexpectedly: %v", err)
	}
}

// ✅ NEW TEST: Verify reorg depth limits work
func TestReorgDepthLimit(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "thrylos-reorg-depth-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	badgerStore, err := storage.NewBadgerStorage(tmpDir)
	require.NoError(t, err)
	defer badgerStore.Close()

	cfg := &config.Config{
		Consensus: config.ConsensusConfig{
			MaxBlockSize:       1000000,
			MaxTxPerBlock:      100,
			MinGasPrice:        "1",
			MaxFutureBlockTime: 15 * time.Second,
		},
		Economics: config.EconomicsConfig{
			BaseGasPrice: "1",
		},
	}

	ws, err := state.NewWorldState(tmpDir, 0, 1, cfg, badgerStore)
	require.NoError(t, err)

	bcConfig := &BlockchainConfig{
		Config:        cfg,
		WorldState:    ws,
		ShardID:       0,
		TotalShards:   1,
		MaxReorgDepth: 100,
	}
	bc, err := NewBlockchain(bcConfig)
	require.NoError(t, err)

	// Create genesis
	genesis := &core.Block{
		Header: &core.BlockHeader{
			Index:     0,
			Timestamp: time.Now().Unix(),
		},
		Hash: "0xGenesis",
	}
	ws.AddBlock(genesis)

	// Try to reorg with 101 blocks (exceeds MaxReorgDepth of 100)
	tooManyBlocks := make([]*core.Block, 101)
	for i := 0; i < 101; i++ {
		tooManyBlocks[i] = &core.Block{
			Header: &core.BlockHeader{
				Index:     int64(i + 1),
				Timestamp: time.Now().Unix(),
			},
			Hash: "0xBlock" + string(rune(i)),
		}
	}

	err = bc.ReorganizeChain(tooManyBlocks)

	// ✅ Should reject reorg that's too deep
	assert.Error(t, err, "Should reject reorg exceeding MaxReorgDepth")
	assert.Contains(t, err.Error(), "reorg too deep", "Error should mention depth limit")
}

// ✅ NEW TEST: Verify snapshot/restore works on reorg failure
func TestReorgSnapshotRestore(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "thrylos-reorg-snapshot-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	badgerStore, err := storage.NewBadgerStorage(tmpDir)
	require.NoError(t, err)
	defer badgerStore.Close()

	cfg := &config.Config{
		Consensus: config.ConsensusConfig{
			MaxBlockSize:       1000000,
			MaxTxPerBlock:      100,
			MinGasPrice:        "1",
			MaxFutureBlockTime: 15 * time.Second,
		},
		Economics: config.EconomicsConfig{
			BaseGasPrice: "1",
		},
	}

	ws, err := state.NewWorldState(tmpDir, 0, 1, cfg, badgerStore)
	require.NoError(t, err)

	bcConfig := &BlockchainConfig{
		Config:        cfg,
		WorldState:    ws,
		ShardID:       0,
		TotalShards:   1,
		MaxReorgDepth: 100,
	}
	bc, err := NewBlockchain(bcConfig)
	require.NoError(t, err)

	// Create genesis
	genesis := &core.Block{
		Header: &core.BlockHeader{
			Index:     0,
			Timestamp: time.Now().Unix(),
			Validator: "0xGenesisValidator", // ✅ Added validator
		},
		Hash: "0xGenesis",
	}
	ws.AddBlock(genesis)

	// Get state before reorg
	stateBefore := bc.GetHeight()

	// Try to reorg with invalid block (future timestamp will fail validation)
	invalidBlock := &core.Block{
		Header: &core.BlockHeader{
			Index:     1,
			PrevHash:  "0xGenesis",
			Timestamp: time.Now().Add(1 * time.Hour).Unix(), // ✅ Too far in future - will fail
			Validator: "0xValidator",
			GasUsed:   0,
		},
		Hash: "0xInvalidBlock",
	}

	err = bc.ReorganizeChain([]*core.Block{invalidBlock})

	// ✅ Should fail due to validation
	assert.Error(t, err, "Should reject invalid block")
	assert.Contains(t, err.Error(), "too far in the future", "Should fail timestamp validation")

	// ✅ State should be unchanged (snapshot restored)
	stateAfter := bc.GetHeight()
	assert.Equal(t, stateBefore, stateAfter, "State should be restored after failed reorg")
}
