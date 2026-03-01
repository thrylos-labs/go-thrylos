package pos

import (
	"math/big"
	"testing"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	coremath "github.com/thrylos-labs/go-thrylos/core/math"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/types"
)

type forkChoiceTestWorldState struct {
	height           int64
	balance          *big.Int
	validator        *core.Validator
	activeValidators []*core.Validator
}

func (m *forkChoiceTestWorldState) GetValidator(address string) (*core.Validator, error) {
	return m.validator, nil
}

func (m *forkChoiceTestWorldState) GetActiveValidators() []*core.Validator {
	if m.activeValidators != nil {
		return m.activeValidators
	}
	if m.validator != nil && m.validator.Active {
		return []*core.Validator{m.validator}
	}
	return nil
}

func (m *forkChoiceTestWorldState) GetBlockByHash(hash string) (*core.Block, error) {
	return nil, nil
}

func (m *forkChoiceTestWorldState) GetBalance(address string) (*big.Int, error) {
	return new(big.Int).Set(m.balance), nil
}

func (m *forkChoiceTestWorldState) UpdateBalance(address string, amount *big.Int) error {
	m.balance = new(big.Int).Set(amount)
	return nil
}

func (m *forkChoiceTestWorldState) GetHeight() int64 {
	return m.height
}

func (m *forkChoiceTestWorldState) UpdateValidator(validator *core.Validator) error {
	m.validator = validator
	return nil
}

func TestForkChoiceProcessAttestation_AppliesSlashingOnEquivocation(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Environment = "test"

	ws := &forkChoiceTestWorldState{
		height: 200,
		balance: big.NewInt(1_000_000),
		validator: &core.Validator{
			Address: "validator-1",
			Stake:   coremath.ParseBigInt("1000000").Bytes(),
			Active:  true,
		},
	}
	ws.activeValidators = []*core.Validator{
		ws.validator,
		{
			Address: "validator-2",
			Stake:   coremath.ParseBigInt("1000000").Bytes(),
			Active:  true,
		},
	}

	sm := NewSlashingManager(nil, ws, nil, ws)
	fc := NewForkChoice(cfg, ws, sm)

	att1 := &types.Attestation{
		ValidatorAddress: ws.validator.Address,
		BlockHash:        "0xaaaabbbb",
		BlockHeight:      10,
		Epoch:            2,
		Slot:             64,
		Timestamp:        time.Now().Unix(),
		Signature:        []byte("sig-1"),
	}
	att2 := &types.Attestation{
		ValidatorAddress: ws.validator.Address,
		BlockHash:        "0xccccdddd",
		BlockHeight:      10,
		Epoch:            2,
		Slot:             64,
		Timestamp:        time.Now().Unix(),
		Signature:        []byte("sig-2"),
	}

	fc.ProcessAttestation(att1)
	fc.ProcessAttestation(att2)

	records := sm.GetSlashingRecords(ws.validator.Address)
	if len(records) != 1 {
		t.Fatalf("expected 1 slashing record, got %d", len(records))
	}
	if records[0].Condition != types.DoubleVoting {
		t.Fatalf("expected double-voting slashing, got %v", records[0].Condition)
	}
	if records[0].SlashedAmount == nil || records[0].SlashedAmount.Sign() <= 0 {
		t.Fatalf("expected positive slashed amount, got %v", records[0].SlashedAmount)
	}
	if sm.IsValidatorActive(ws.validator.Address) {
		t.Fatal("expected validator to be inactive after equivocation slashing")
	}
}
