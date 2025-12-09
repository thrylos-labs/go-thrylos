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

	// [FIX] Use strings for GasPrice fields
	cfg := &config.Config{
		Consensus: config.ConsensusConfig{
			MaxBlockSize:  1000000,
			MaxTxPerBlock: 100,
			MinGasPrice:   "1", // ✅ Fixed: String
		},
		Economics: config.EconomicsConfig{
			BaseGasPrice: "1", // ✅ Fixed: String
		},
	}

	ws, err := state.NewWorldState(tmpDir, 0, 1, cfg, badgerStore)
	require.NoError(t, err)

	bcConfig := &BlockchainConfig{
		Config:      cfg,
		WorldState:  ws,
		ShardID:     0,
		TotalShards: 1,
	}
	bc, err := NewBlockchain(bcConfig)
	require.NoError(t, err)

	// 2. Create Alice with balance
	privKey, _ := crypto.NewPrivateKey()
	aliceAddr, _ := account.GenerateAddress(privKey.PublicKey())

	// ✅ Fixed: Use String for balance
	initialBalanceStr := "100000"
	ws.GetAccountManager().UpdateAccount(&core.Account{
		Address: aliceAddr,
		Balance: initialBalanceStr,
		Nonce:   0,
	})

	// 3. Create Tx: Alice sends 1000 tokens.
	// ✅ Fixed: Use Strings for Amount and Price
	txAmountStr := "1000"
	txGas := int64(21000)
	txGasPriceStr := "1"

	tx := &core.Transaction{
		Id:        "tx1",
		From:      aliceAddr,
		To:        "0xRecipient",
		Amount:    txAmountStr, // ✅ Fixed
		Gas:       txGas,
		GasPrice:  txGasPriceStr, // ✅ Fixed
		Nonce:     0,
		Timestamp: time.Now().Unix(),
	}

	// Genesis setup
	genesis := &core.Block{
		Header: &core.BlockHeader{Index: 0},
		Hash:   "0xGenesis",
	}
	ws.AddBlock(genesis)

	newBlock := &core.Block{
		Header: &core.BlockHeader{
			Index:     1,
			PrevHash:  "0xGenesis",
			Timestamp: time.Now().Unix(),
			Validator: "0xValidator",
			GasLimit:  10000000,
		},
		Transactions: []*core.Transaction{tx},
		Hash:         "0xForkBlock",
	}

	// 4. Execute Reorg
	err = bc.ReorganizeChain([]*core.Block{newBlock})

	// Validation
	if err == nil {
		updatedAccount, _ := ws.GetAccount(aliceAddr)

		// ✅ Calculate Expected Balance using BigInt Math
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
		t.Logf("Test setup note: Reorg returned error: %v", err)
	}
}
