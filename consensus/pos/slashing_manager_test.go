package pos

import (
	"math/big"
	"testing"

	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

type slashingTestWorldState struct {
	height int64
}

func (m *slashingTestWorldState) GetBalance(address string) (*big.Int, error) {
	return big.NewInt(1_000_000_000), nil
}

func (m *slashingTestWorldState) UpdateBalance(address string, amount *big.Int) error {
	return nil
}

func (m *slashingTestWorldState) GetHeight() int64 {
	return m.height
}

func (m *slashingTestWorldState) GetValidator(address string) (*core.Validator, error) {
	return &core.Validator{Address: address, Stake: "1000000000"}, nil
}

func (m *slashingTestWorldState) UpdateValidator(validator *core.Validator) error {
	return nil
}

func TestSlashingManager_ProcessVoteDetectsSurroundVoting(t *testing.T) {
	worldState := &slashingTestWorldState{height: 100}
	manager := NewSlashingManager(nil, worldState, nil, nil)

	inner := &Vote{
		ValidatorAddress: "validator-1",
		SourceEpoch:      2,
		TargetEpoch:      4,
	}
	outer := &Vote{
		ValidatorAddress: "validator-1",
		SourceEpoch:      1,
		TargetEpoch:      5,
	}

	if err := manager.ProcessVote(inner); err != nil {
		t.Fatalf("unexpected error recording inner vote: %v", err)
	}

	err := manager.ProcessVote(outer)
	if err == nil {
		t.Fatal("expected surround-voting detection error")
	}

	svErr, ok := err.(*SurroundVotingError)
	if !ok {
		t.Fatalf("expected SurroundVotingError, got %T", err)
	}
	if svErr.InnerVote.TargetEpoch != inner.TargetEpoch {
		t.Fatalf("expected inner vote target epoch %d, got %d", inner.TargetEpoch, svErr.InnerVote.TargetEpoch)
	}
	if svErr.OuterVote.TargetEpoch != outer.TargetEpoch {
		t.Fatalf("expected outer vote target epoch %d, got %d", outer.TargetEpoch, svErr.OuterVote.TargetEpoch)
	}
}
