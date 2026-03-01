package governance_test

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/consensus/governance"
	accountpkg "github.com/thrylos-labs/go-thrylos/core/account"
	coremath "github.com/thrylos-labs/go-thrylos/core/math"
	"github.com/thrylos-labs/go-thrylos/core/state"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
)

func newTestGovernanceManager(t *testing.T, cfg *config.Config) (*governance.Manager, *state.WorldState) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "thrylos-governance-test-*")
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

	return governance.NewManager(cfg, ws), ws
}

func u(v string) []byte {
	return coremath.ParseBigInt(v).Bytes()
}

func TestGovernanceProposal_UsesOneVotePerOwnershipDomain(t *testing.T) {
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
	cfg.Governance.OwnershipDomainsEnabled = true

	gm, ws := newTestGovernanceManager(t, cfg)

	setValidator := func(address string, stake string, pubkey byte) {
		err := ws.SetValidator(address, &core.Validator{
			Address:        address,
			Pubkey:         []byte{pubkey},
			Stake:          u(stake),
			SelfStake:      u(stake),
			DelegatedStake: nil,
			Delegators:     map[string][]byte{},
			Active:         true,
			CreatedAt:      time.Now().Unix(),
			UpdatedAt:      time.Now().Unix(),
		})
		require.NoError(t, err)
	}

	setValidator(validatorOne, "60", 1)
	setValidator(validatorTwo, "40", 2)
	setValidator(validatorThree, "20", 3)

	require.NoError(t, ws.SetValidatorStakeDomain(validatorOne, domainID))
	require.NoError(t, ws.SetValidatorStakeDomain(validatorTwo, domainID))
	ws.UpdateTotalStaked()

	proposal, err := gm.SubmitParameterChangeProposal(validatorOne, "economics.community_tax", "0.04")
	require.NoError(t, err)

	err = gm.CastVote(proposal.ID, validatorOne, true)
	require.NoError(t, err)

	err = gm.CastVote(proposal.ID, validatorTwo, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "has already voted")

	err = gm.CastVote(proposal.ID, validatorThree, true)
	require.NoError(t, err)

	time.Sleep(1100 * time.Millisecond)

	finalized, err := gm.FinalizeProposal(proposal.ID)
	require.NoError(t, err)
	require.Equal(t, governance.ProposalStatusApproved, finalized.Status)
	require.Equal(t, "120", finalized.YesStake)
	require.Equal(t, "0", finalized.NoStake)
	require.Equal(t, 0.04, cfg.Economics.CommunityTax)

	reloaded, err := gm.GetProposal(proposal.ID)
	require.NoError(t, err)
	require.Equal(t, governance.ProposalStatusApproved, reloaded.Status)
}

func TestGovernanceRejectsInvalidParameterChange(t *testing.T) {
	const validatorAddr = "0x4545454545454545454545454545454545454545"

	cfg := config.DefaultConfig()
	gm, ws := newTestGovernanceManager(t, cfg)

	err := ws.SetValidator(validatorAddr, &core.Validator{
		Address:        validatorAddr,
		Pubkey:         []byte{5},
		Stake:          u("50"),
		SelfStake:      u("50"),
		DelegatedStake: nil,
		Delegators:     map[string][]byte{},
		Active:         true,
		CreatedAt:      time.Now().Unix(),
		UpdatedAt:      time.Now().Unix(),
	})
	require.NoError(t, err)
	ws.UpdateTotalStaked()

	err = governance.ValidateParameterChange("economics.unknown", "0.1")
	require.Error(t, err)

	_, err = gm.SubmitProposal("proposal-1", validatorAddr, "economics.unknown", "0.1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not governance-managed")
}

func TestGovernanceFinalizeRejectsBeforeVotingEnds(t *testing.T) {
	const validatorAddr = "0x4444444444444444444444444444444444444444"

	cfg := config.DefaultConfig()
	cfg.Governance.VotingPeriod = 10 * time.Second

	gm, ws := newTestGovernanceManager(t, cfg)
	err := ws.SetValidator(validatorAddr, &core.Validator{
		Address:        validatorAddr,
		Pubkey:         []byte{4},
		Stake:          u("50"),
		SelfStake:      u("50"),
		DelegatedStake: nil,
		Delegators:     map[string][]byte{},
		Active:         true,
		CreatedAt:      time.Now().Unix(),
		UpdatedAt:      time.Now().Unix(),
	})
	require.NoError(t, err)
	ws.UpdateTotalStaked()

	proposal, err := gm.SubmitProposal("proposal-2", validatorAddr, "economics.community_tax", "0.04")
	require.NoError(t, err)

	_, err = gm.FinalizeProposal(proposal.ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "still in the voting period")
}

func TestGovernanceCanApplyEvmCircuitBreakerParameter(t *testing.T) {
	err := governance.ValidateParameterChange("economics.evm_max_tx_per_window", "1500")
	require.NoError(t, err)
}
