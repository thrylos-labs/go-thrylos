package transaction_test

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/thrylos-labs/go-thrylos/config"
	accountpkg "github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/core/state"
	"github.com/thrylos-labs/go-thrylos/core/transaction"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
)

func newTestGovernanceExecutor(t *testing.T, cfg *config.Config) (*transaction.Executor, *state.WorldState) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "thrylos-tx-governance-*")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = os.RemoveAll(tmpDir)
	})

	badgerStore, err := storage.NewBadgerStorage(tmpDir)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = badgerStore.Close()
	})

	ws, err := state.NewWorldState(tmpDir, accountpkg.ShardID(0), 1, cfg, badgerStore)
	require.NoError(t, err)

	validator := transaction.NewValidator(accountpkg.ShardID(0), 1, cfg)
	executor := transaction.NewExecutor(accountpkg.ShardID(0), 1, ws.GetStateStorage(), ws, validator, cfg, nil)
	return executor, ws
}

func TestExecuteTransaction_GovernanceLifecycle(t *testing.T) {
	const (
		validatorOne   = "0x1111111111111111111111111111111111111111"
		validatorTwo   = "0x2222222222222222222222222222222222222222"
		validatorThree = "0x3333333333333333333333333333333333333333"
		domainID       = "operator-a"
	)

	cfg := config.DefaultConfig()
	cfg.Governance.VotingPeriod = time.Second
	cfg.Governance.Quorum = 0.50
	cfg.Governance.ApprovalThreshold = 0.67

	executor, ws := newTestGovernanceExecutor(t, cfg)
	accountManager := ws.GetAccountManager()

	setValidatorState := func(address string, pubkey byte, stake string) {
		err := ws.SetValidator(address, &core.Validator{
			Address:        address,
			Pubkey:         []byte{pubkey},
			Stake:          stake,
			SelfStake:      stake,
			DelegatedStake: "0",
			Delegators:     map[string]string{},
			Active:         true,
			CreatedAt:      time.Now().Unix(),
			UpdatedAt:      time.Now().Unix(),
		})
		require.NoError(t, err)

		err = accountManager.UpdateAccount(&core.Account{
			Address:      address,
			Balance:      "1000000",
			Nonce:        0,
			StakedAmount: "0",
			DelegatedTo:  map[string]string{},
			Rewards:      "0",
		})
		require.NoError(t, err)
	}

	setValidatorState(validatorOne, 1, "60")
	setValidatorState(validatorTwo, 2, "40")
	setValidatorState(validatorThree, 3, "20")
	require.NoError(t, ws.SetValidatorStakeDomain(validatorOne, domainID))
	require.NoError(t, ws.SetValidatorStakeDomain(validatorTwo, domainID))
	ws.UpdateTotalStaked()

	proposalPayload, err := json.Marshal(map[string]string{
		"parameter":      "economics.community_tax",
		"proposed_value": "0.04",
	})
	require.NoError(t, err)

	_, err = executor.ExecuteTransaction(&core.Transaction{
		Id:        "gov-propose",
		From:      validatorOne,
		Amount:    "0",
		Gas:       21000,
		GasPrice:  "1",
		Nonce:     0,
		Data:      proposalPayload,
		Type:      core.TransactionType_GOVERNANCE_PROPOSE,
		Hash:      "gov-proposal-hash",
		Timestamp: time.Now().Unix(),
		ChainId:   cfg.Network.ChainID,
	}, accountManager)
	require.NoError(t, err)

	votePayload, err := json.Marshal(map[string]interface{}{
		"proposal_id": "gov-proposal-hash",
		"approve":     true,
	})
	require.NoError(t, err)

	_, err = executor.ExecuteTransaction(&core.Transaction{
		Id:        "gov-vote-1",
		From:      validatorOne,
		Amount:    "0",
		Gas:       21000,
		GasPrice:  "1",
		Nonce:     1,
		Data:      votePayload,
		Type:      core.TransactionType_GOVERNANCE_VOTE,
		Hash:      "gov-vote-1-hash",
		Timestamp: time.Now().Unix(),
		ChainId:   cfg.Network.ChainID,
	}, accountManager)
	require.NoError(t, err)

	_, err = executor.ExecuteTransaction(&core.Transaction{
		Id:        "gov-vote-2",
		From:      validatorThree,
		Amount:    "0",
		Gas:       21000,
		GasPrice:  "1",
		Nonce:     0,
		Data:      votePayload,
		Type:      core.TransactionType_GOVERNANCE_VOTE,
		Hash:      "gov-vote-2-hash",
		Timestamp: time.Now().Unix(),
		ChainId:   cfg.Network.ChainID,
	}, accountManager)
	require.NoError(t, err)

	time.Sleep(1100 * time.Millisecond)

	finalizePayload, err := json.Marshal(map[string]string{
		"proposal_id": "gov-proposal-hash",
	})
	require.NoError(t, err)

	_, err = executor.ExecuteTransaction(&core.Transaction{
		Id:        "gov-finalize",
		From:      validatorThree,
		Amount:    "0",
		Gas:       21000,
		GasPrice:  "1",
		Nonce:     1,
		Data:      finalizePayload,
		Type:      core.TransactionType_GOVERNANCE_FINALIZE,
		Hash:      "gov-finalize-hash",
		Timestamp: time.Now().Unix(),
		ChainId:   cfg.Network.ChainID,
	}, accountManager)
	require.NoError(t, err)

	storedValue, err := ws.GetMetadata("governance/param/economics.community_tax")
	require.NoError(t, err)
	require.Equal(t, "0.04", storedValue)
	require.Equal(t, 0.04, cfg.Economics.CommunityTax)

	accountOne, err := accountManager.GetAccount(validatorOne)
	require.NoError(t, err)
	require.Equal(t, "958000", accountOne.Balance)
	require.Equal(t, uint64(2), accountOne.Nonce)

	accountThree, err := accountManager.GetAccount(validatorThree)
	require.NoError(t, err)
	require.Equal(t, "958000", accountThree.Balance)
	require.Equal(t, uint64(2), accountThree.Nonce)
}
