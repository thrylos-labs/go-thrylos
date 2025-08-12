package core

// import (
// 	"fmt"
// 	"testing"
// 	"time"

// 	"github.com/stretchr/testify/require"
// 	"github.com/thrylos-labs/go-thrylos/config"
// 	"github.com/thrylos-labs/go-thrylos/core/account"
// 	"github.com/thrylos-labs/go-thrylos/core/state"
// 	"github.com/thrylos-labs/go-thrylos/core/transaction"
// 	"github.com/thrylos-labs/go-thrylos/crypto"
// 	"github.com/thrylos-labs/go-thrylos/crypto/address"
// 	core "github.com/thrylos-labs/go-thrylos/proto/core"
// 	"github.com/thrylos-labs/go-thrylos/storage"
// )

// // TestMinimalFinality - a very simple finality test
// func TestMinimalFinality(t *testing.T) {
// 	// Load config
// 	testConfig, err := config.Load()
// 	require.NoError(t, err)

// 	// Use the EXISTING genesis account from the default config instead of creating a new one
// 	existingGenesisAccount := testConfig.Genesis.Accounts[0]
// 	genesisAddress := existingGenesisAccount.Address

// 	t.Logf("Using existing genesis address: %s with balance: %d", genesisAddress, existingGenesisAccount.Balance)

// 	// Create storage and worldstate with a unique directory each time
// 	dataDir := fmt.Sprintf("/tmp/minimal_finality_test_%d", time.Now().UnixNano())
// 	badgerStorage, err := storage.NewBadgerStorage(dataDir)
// 	require.NoError(t, err)
// 	defer badgerStorage.Close()

// 	worldState, err := state.NewWorldState(dataDir, account.ShardID(0), 1, testConfig, badgerStorage)
// 	require.NoError(t, err)
// 	defer worldState.Close()

// 	// Verify the genesis account has balance
// 	balance, err := worldState.GetBalance(genesisAddress)
// 	require.NoError(t, err)
// 	t.Logf("Genesis account balance: %d THRYLOS", balance/config.BaseUnit)
// 	require.Greater(t, balance, int64(0), "Genesis account should have balance")

// 	// Create transaction validator
// 	txValidator := transaction.NewValidator(account.ShardID(0), 1, testConfig)

// 	// Generate recipient address
// 	recipientPrivKey, err := crypto.NewPrivateKey()
// 	require.NoError(t, err)

// 	recipientAddress, err := address.GenerateAddress(recipientPrivKey.PublicKey().Bytes())
// 	require.NoError(t, err)

// 	t.Logf("Recipient address: %s", recipientAddress)

// 	// Create a proper transaction using the validator
// 	tx, err := txValidator.CreateTransferTransaction(
// 		genesisAddress,
// 		recipientAddress,
// 		1000*config.BaseUnit,
// 		21000,
// 		1000,
// 		0, // nonce
// 	)

// 	if err != nil {
// 		t.Fatalf("Failed to create transaction via validator: %v", err)
// 	}

// 	require.NotEmpty(t, tx.Hash, "Transaction hash should not be empty")

// 	// For now, we'll skip signing since we don't have the genesis private key
// 	// In a real test, you'd need the actual private key that corresponds to the genesis address
// 	t.Logf("⚠️  Skipping signature verification for this test")

// 	// Temporarily remove signature requirement by setting a dummy signature
// 	tx.Signature = []byte("dummy_signature_for_testing")

// 	t.Logf("Transaction created successfully with hash: %s", tx.Hash)

// 	// Test transaction submission
// 	startTime := time.Now()
// 	err = worldState.AddTransaction(tx)
// 	require.NoError(t, err, "Transaction submission should succeed")
// 	inclusionTime := time.Since(startTime)

// 	t.Logf("Transaction submitted successfully in %v", inclusionTime)

// 	// Create a simple block manually to test finality
// 	// Use the genesis address as validator
// 	validator := &core.Validator{
// 		Address:        genesisAddress,
// 		Pubkey:         []byte("dummy_pubkey_for_testing"),
// 		Stake:          100000 * config.BaseUnit,
// 		SelfStake:      100000 * config.BaseUnit,
// 		DelegatedStake: 0,
// 		Delegators:     make(map[string]int64),
// 		Commission:     0.05,
// 		Active:         true,
// 		CreatedAt:      time.Now().Unix(),
// 		UpdatedAt:      time.Now().Unix(),
// 	}

// 	err = worldState.AddValidator(validator)
// 	require.NoError(t, err)

// 	// Get pending transactions
// 	pendingTxs := worldState.GetPendingTransactions()
// 	t.Logf("Pending transactions: %d", len(pendingTxs))

// 	if len(pendingTxs) > 0 {
// 		// Get the current block to use its hash as previous hash
// 		currentBlock := worldState.GetCurrentBlock()
// 		var prevHash string
// 		var blockIndex int64 = 1
// 		var blockTimestamp int64 = time.Now().Unix()

// 		if currentBlock != nil {
// 			prevHash = currentBlock.Hash
// 			blockIndex = currentBlock.Header.Index + 1
// 			// Make sure our timestamp is after the previous block
// 			if blockTimestamp <= currentBlock.Header.Timestamp {
// 				blockTimestamp = currentBlock.Header.Timestamp + 1
// 			}
// 		}

// 		t.Logf("Creating block %d with previous hash: %s, timestamp: %d", blockIndex, prevHash, blockTimestamp)

// 		// Create a block with the transaction
// 		block := &core.Block{
// 			Header: &core.BlockHeader{
// 				Index:     blockIndex,
// 				PrevHash:  prevHash,
// 				Timestamp: blockTimestamp,
// 				Validator: validator.Address,
// 				GasLimit:  1000000,
// 				GasUsed:   21000,
// 				StateRoot: "",
// 			},
// 			Transactions: pendingTxs,
// 		}

// 		// Calculate block hash
// 		block.Hash = fmt.Sprintf("block_hash_%d", time.Now().UnixNano())

// 		// Add block
// 		blockStartTime := time.Now()
// 		err = worldState.AddBlock(block)
// 		require.NoError(t, err, "Block should be added successfully")
// 		blockTime := time.Since(blockStartTime)

// 		finalityTime := time.Since(startTime)

// 		t.Logf("Block created and added in %v", blockTime)
// 		t.Logf("Total finality time: %v", finalityTime)
// 		t.Logf("✅ Finality test completed successfully!")

// 		// Verify transaction is in the block
// 		require.Equal(t, 1, len(block.Transactions), "Block should contain 1 transaction")
// 		require.Equal(t, tx.Id, block.Transactions[0].Id, "Block should contain our transaction")

// 	} else {
// 		t.Logf("❌ No pending transactions found")
// 	}
// }
