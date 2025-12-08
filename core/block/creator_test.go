package block

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/proto/core"
)

// Helper function to create a dummy transaction of a specific approximate size
func createDummyTx(id string, payloadSize int) *core.Transaction {
	// Create a payload string of 'A's repeated payloadSize times
	payload := strings.Repeat("A", payloadSize)

	return &core.Transaction{
		Id:   id,
		From: "sender_wallet_address",
		To:   "recipient_wallet_address",

		// FIX: Wrap these in quotes because they are now strings
		Amount:   "100",
		Gas:      21000, // Gas limit is still int64, so this is fine
		GasPrice: "10",  // FIX: Wrap in quotes

		Timestamp: time.Now().Unix(),
		Data:      []byte(payload),
		Signature: []byte("dummy_signature"),
	}
}
func TestValidateBlockSize(t *testing.T) {
	// Initialize the creator
	creator := NewCreator(0, 4)

	tests := []struct {
		name          string
		setupBlock    func() *core.Block
		expectError   bool
		errorContains string
	}{
		{
			name: "Valid Block - Small",
			setupBlock: func() *core.Block {
				return &core.Block{
					Header: &core.BlockHeader{Index: 1},
					Transactions: []*core.Transaction{
						createDummyTx("tx1", 100),
					},
				}
			},
			expectError: false,
		},
		{
			name: "Invalid Block - Too Many Transactions",
			setupBlock: func() *core.Block {
				// Create a slice with MaxTransactionsPerBlock + 1 transactions
				txs := make([]*core.Transaction, config.MaxTransactionsPerBlock+1)
				for i := 0; i < len(txs); i++ {
					txs[i] = createDummyTx(fmt.Sprintf("tx%d", i), 10)
				}
				return &core.Block{
					Header:       &core.BlockHeader{Index: 2},
					Transactions: txs,
				}
			},
			expectError:   true,
			errorContains: "too many transactions",
		},
		{
			name: "Invalid Block - Exceeds Max Byte Size",
			setupBlock: func() *core.Block {
				// 1MB payload becomes ~1.33MB after JSON Base64 encoding
				tx := createDummyTx("large_tx", int(config.MaxBlockSize))

				return &core.Block{
					Header:       &core.BlockHeader{Index: 3},
					Transactions: []*core.Transaction{tx},
				}
			},
			expectError:   true,
			errorContains: "block too large",
		},
		{
			name: "Valid Block - Near Limit but Safe",
			setupBlock: func() *core.Block {
				// CORRECTED: Reduced to 700KB.
				// 700,000 bytes * 1.33 (Base64) ≈ 931,000 bytes.
				// This fits safely under the 1,000,000 byte limit.
				tx := createDummyTx("large_safe_tx", 700_000)

				return &core.Block{
					Header:       &core.BlockHeader{Index: 4},
					Transactions: []*core.Transaction{tx},
				}
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			block := tt.setupBlock()
			err := creator.ValidateBlockSize(block)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
