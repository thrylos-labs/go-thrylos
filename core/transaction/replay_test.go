package transaction

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/proto/core"
)

func TestReplayProtection_ChainID(t *testing.T) {
	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: "thrylos-mainnet-1",
		},
	}

	validator := NewValidator(0, 1, cfg)

	t.Run("RejectsTransaction_WithoutChainID", func(t *testing.T) {
		tx := &core.Transaction{
			From:   "sender",
			To:     "receiver",
			Amount: "1000",
			Nonce:  1,
			// ChainId missing
		}

		err := validator.ValidateReplayProtection(tx, 100)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing chain_id")
	})

	t.Run("RejectsTransaction_WrongChainID", func(t *testing.T) {
		tx := &core.Transaction{
			From:    "sender",
			To:      "receiver",
			Amount:  "1000",
			Nonce:   1,
			ChainId: "thrylos-testnet-1", // Wrong chain
		}

		err := validator.ValidateReplayProtection(tx, 100)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "chain_id mismatch")
		assert.Contains(t, err.Error(), "prevents cross-chain replay")
	})

	t.Run("AcceptsTransaction_CorrectChainID", func(t *testing.T) {
		tx := &core.Transaction{
			From:      "sender",
			To:        "receiver",
			Amount:    "1000",
			Nonce:     1,
			ChainId:   "thrylos-mainnet-1", // Correct
			Timestamp: time.Now().Unix(),
		}

		err := validator.ValidateReplayProtection(tx, 100)
		assert.NoError(t, err)
	})
}

func TestReplayProtection_Expiration(t *testing.T) {
	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: "thrylos-mainnet-1",
		},
	}

	validator := NewValidator(0, 1, cfg)
	validator.replayConfig = DefaultReplayProtectionConfig()

	t.Run("RejectsExpiredTransaction", func(t *testing.T) {
		// Transaction from 2000 blocks ago (4000 seconds at 2s/block)
		oldTimestamp := time.Now().Unix() - 4000

		tx := &core.Transaction{
			From:      "sender",
			To:        "receiver",
			Amount:    "1000",
			Nonce:     1,
			ChainId:   "thrylos-mainnet-1",
			Timestamp: oldTimestamp,
		}

		err := validator.ValidateReplayProtection(tx, 100)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "expired")
	})

	t.Run("AcceptsRecentTransaction", func(t *testing.T) {
		tx := &core.Transaction{
			From:      "sender",
			To:        "receiver",
			Amount:    "1000",
			Nonce:     1,
			ChainId:   "thrylos-mainnet-1",
			Timestamp: time.Now().Unix(),
		}

		err := validator.ValidateReplayProtection(tx, 100)
		assert.NoError(t, err)
	})
}

func TestReplayProtection_Nonce(t *testing.T) {
	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: "thrylos-mainnet-1",
		},
	}

	validator := NewValidator(0, 1, cfg)

	t.Run("RejectsTransaction_WithoutNonce", func(t *testing.T) {
		tx := &core.Transaction{
			From:      "sender",
			To:        "receiver",
			Amount:    "1000",
			Nonce:     0, // Invalid
			ChainId:   "thrylos-mainnet-1",
			Timestamp: time.Now().Unix(),
		}

		err := validator.ValidateReplayProtection(tx, 100)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing nonce")
	})
}

func TestEnsureReplayProtection(t *testing.T) {
	cfg := &config.Config{
		Network: config.NetworkConfig{
			ChainID: "thrylos-mainnet-1",
		},
	}

	t.Run("SetsChainID_WhenMissing", func(t *testing.T) {
		tx := &core.Transaction{
			From:   "sender",
			To:     "receiver",
			Amount: "1000",
			Nonce:  1,
			// ChainId missing
		}

		err := EnsureReplayProtection(tx, cfg)
		require.NoError(t, err)

		assert.Equal(t, "thrylos-mainnet-1", tx.ChainId)
		assert.NotZero(t, tx.Timestamp)
	})

	t.Run("PreservesExisting_ChainID", func(t *testing.T) {
		tx := &core.Transaction{
			From:    "sender",
			To:      "receiver",
			Amount:  "1000",
			Nonce:   1,
			ChainId: "thrylos-mainnet-1",
		}

		err := EnsureReplayProtection(tx, cfg)
		require.NoError(t, err)

		assert.Equal(t, "thrylos-mainnet-1", tx.ChainId)
	})
}
