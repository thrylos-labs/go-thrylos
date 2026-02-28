package pos

import (
	"fmt"
	"math/big"
	"sort"

	"github.com/thrylos-labs/go-thrylos/consensus/validator"
	"github.com/thrylos-labs/go-thrylos/core/math"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

type SimulationConfig struct {
	Validators          []SimulationValidator
	Slots               uint64
	SlotsPerEpoch       int
	CooldownWindow      int
	QuorumThreshold     float64
	PartitionWindows    []SimulationPartitionWindow
	DelayedAttestations []SimulationDelayedAttestation
	WithheldSlots       map[uint64]bool
}

type SimulationValidator struct {
	Address  string
	Stake    string
	DomainID string
	Active   bool
}

type SimulationPartitionWindow struct {
	StartSlot                  uint64
	EndSlot                    uint64
	Groups                     [][]string
	ReplayBufferedAttestations bool
}

type SimulationDelayedAttestation struct {
	Validator  string
	StartSlot  uint64
	EndSlot    uint64
	DelaySlots uint64
}

type SimulationResult struct {
	ProducedBlocks        uint64
	MissedSlots           uint64
	PartitionedSlots      uint64
	WithheldSlots         uint64
	DeliveredAttestations uint64
	FinalizedBlocks       uint64
	UnfinalizedBlocks     uint64
	MaxFinalityDelay      uint64
	AverageFinalityDelay  float64
	HeadHeight            int64
	SlotResults           []SimulationSlotResult
}

type SimulationSlotResult struct {
	Slot                  uint64
	Proposer              string
	Produced              bool
	Withheld              bool
	Partitioned           bool
	VisibleStake          string
	AttestedStake         string
	DeliveredAttestations int
	BlockHash             string
	FinalizedAt           uint64
}

type simulatedBlock struct {
	slot          uint64
	hash          string
	proposer      string
	attestedStake *big.Int
	finalizedAt   uint64
	finalized     bool
	proposerGroup int
}

type pendingAttestation struct {
	blockHash     string
	validatorAddr string
	weight        *big.Int
	deliverySlot  uint64
}

type simulationHistory struct {
	blocks  map[int64]*core.Block
	height  int64
	domains map[string]string
}

func (h *simulationHistory) GetBlock(index int64) (*core.Block, error) {
	return h.blocks[index], nil
}

func (h *simulationHistory) GetHeight() int64 {
	return h.height
}

func (h *simulationHistory) GetValidatorStakeDomain(validatorAddr string) (string, error) {
	if domainID, exists := h.domains[validatorAddr]; exists && domainID != "" {
		return domainID, nil
	}
	return validatorAddr, nil
}

func RunAdversarialSimulation(cfg SimulationConfig) (*SimulationResult, error) {
	if len(cfg.Validators) == 0 {
		return nil, fmt.Errorf("simulation requires validators")
	}
	if cfg.Slots == 0 {
		return nil, fmt.Errorf("simulation requires at least one slot")
	}
	if cfg.SlotsPerEpoch <= 0 {
		cfg.SlotsPerEpoch = 32
	}
	if cfg.QuorumThreshold <= 0 {
		cfg.QuorumThreshold = 2.0 / 3.0
	}

	history := &simulationHistory{
		blocks:  make(map[int64]*core.Block),
		height:  -1,
		domains: make(map[string]string),
	}

	set := validator.NewSet(len(cfg.Validators))
	set.SetHistoryReader(history)

	validatorsByAddr := make(map[string]*core.Validator, len(cfg.Validators))
	totalStake := big.NewInt(0)
	for _, val := range cfg.Validators {
		if !val.Active {
			continue
		}
		converted := &core.Validator{
			Address: val.Address,
			Stake:   val.Stake,
			Active:  true,
		}
		validatorsByAddr[val.Address] = converted
		totalStake.Add(totalStake, math.ParseBigInt(val.Stake))
		if val.DomainID != "" {
			history.domains[val.Address] = val.DomainID
		}
		if err := set.AddValidator(converted); err != nil {
			return nil, err
		}
	}
	if len(validatorsByAddr) == 0 || totalStake.Sign() == 0 {
		return nil, fmt.Errorf("simulation requires active stake")
	}

	quorumStake := calculateQuorumStake(totalStake, cfg.QuorumThreshold)
	result := &SimulationResult{
		SlotResults: make([]SimulationSlotResult, 0, cfg.Slots),
	}

	pending := make([]pendingAttestation, 0)
	blocks := make(map[string]*simulatedBlock)
	var totalFinalityDelay uint64

	for slot := uint64(1); slot <= cfg.Slots; slot++ {
		deliveredCount := 0
		for i := 0; i < len(pending); {
			if pending[i].deliverySlot > slot {
				i++
				continue
			}

			entry := pending[i]
			block := blocks[entry.blockHash]
			if block != nil {
				block.attestedStake.Add(block.attestedStake, entry.weight)
				result.DeliveredAttestations++
				deliveredCount++
				if !block.finalized && block.attestedStake.Cmp(quorumStake) >= 0 {
					block.finalized = true
					block.finalizedAt = slot
					result.FinalizedBlocks++
					delay := slot - block.slot
					totalFinalityDelay += delay
					if delay > result.MaxFinalityDelay {
						result.MaxFinalityDelay = delay
					}
				}
			}

			pending = append(pending[:i], pending[i+1:]...)
		}

		epoch := (slot - 1) / uint64(cfg.SlotsPerEpoch)
		schedule, err := set.BuildEpochSchedule(activeValidators(cfg.Validators), epoch, cfg.SlotsPerEpoch, cfg.CooldownWindow)
		if err != nil {
			return nil, err
		}
		slotIndex := int((slot - 1) % uint64(cfg.SlotsPerEpoch))
		proposer := schedule[slotIndex]

		slotResult := SimulationSlotResult{
			Slot:                  slot,
			Proposer:              proposer,
			DeliveredAttestations: deliveredCount,
		}

		partitionWindow, partitioned, groupMap := partitionGroupsForSlot(cfg.PartitionWindows, slot)
		if partitioned {
			result.PartitionedSlots++
			slotResult.Partitioned = true
		}

		if cfg.WithheldSlots != nil && cfg.WithheldSlots[slot] {
			result.WithheldSlots++
			result.MissedSlots++
			slotResult.Withheld = true
			result.SlotResults = append(result.SlotResults, slotResult)
			continue
		}

		proposerValidator := validatorsByAddr[proposer]
		if proposerValidator == nil {
			result.MissedSlots++
			result.SlotResults = append(result.SlotResults, slotResult)
			continue
		}

		proposerGroup := groupMap[proposer]
		visibleStake := big.NewInt(0)
		attestedStake := big.NewInt(0)
		blockHash := fmt.Sprintf("sim-%d-%s", slot, proposer)

		for _, val := range validatorsByAddr {
			stake := math.ParseBigInt(val.Stake)
			delay := delayForValidator(cfg.DelayedAttestations, val.Address, slot)

			if partitioned && groupMap[val.Address] != proposerGroup {
				if partitionWindow != nil && partitionWindow.ReplayBufferedAttestations {
					deliverySlot := slot + delay
					healSlot := partitionWindow.EndSlot + 1
					if deliverySlot < healSlot {
						deliverySlot = healSlot
					}
					pending = append(pending, pendingAttestation{
						blockHash:     blockHash,
						validatorAddr: val.Address,
						weight:        new(big.Int).Set(stake),
						deliverySlot:  deliverySlot,
					})
				}
				continue
			}

			visibleStake.Add(visibleStake, stake)

			if delay == 0 {
				attestedStake.Add(attestedStake, stake)
				continue
			}

			pending = append(pending, pendingAttestation{
				blockHash:     blockHash,
				validatorAddr: val.Address,
				weight:        new(big.Int).Set(stake),
				deliverySlot:  slot + delay,
			})
		}

		slotResult.VisibleStake = visibleStake.String()
		slotResult.AttestedStake = attestedStake.String()

		block := &simulatedBlock{
			slot:          slot,
			hash:          blockHash,
			proposer:      proposer,
			attestedStake: attestedStake,
			proposerGroup: proposerGroup,
		}
		if attestedStake.Cmp(quorumStake) >= 0 {
			block.finalized = true
			block.finalizedAt = slot
			result.FinalizedBlocks++
		}

		blocks[blockHash] = block
		result.ProducedBlocks++
		result.HeadHeight++
		slotResult.Produced = true
		slotResult.BlockHash = blockHash
		if block.finalized {
			slotResult.FinalizedAt = block.finalizedAt
		}

		history.height++
		history.blocks[history.height] = &core.Block{
			Hash: blockHash,
			Header: &core.BlockHeader{
				Index:     history.height,
				Validator: proposer,
				Slot:      slot,
				Epoch:     epoch,
			},
		}

		result.SlotResults = append(result.SlotResults, slotResult)
	}

	for _, block := range blocks {
		if !block.finalized {
			result.UnfinalizedBlocks++
			continue
		}
		if block.finalizedAt > block.slot {
			updateSlotFinalization(result.SlotResults, block.hash, block.finalizedAt)
		}
	}

	if result.FinalizedBlocks > 0 {
		result.AverageFinalityDelay = float64(totalFinalityDelay) / float64(result.FinalizedBlocks)
	}

	return result, nil
}

func activeValidators(all []SimulationValidator) []*core.Validator {
	active := make([]*core.Validator, 0, len(all))
	for _, val := range all {
		if val.Active {
			active = append(active, &core.Validator{
				Address: val.Address,
				Stake:   val.Stake,
				Active:  true,
			})
		}
	}
	sort.Slice(active, func(i, j int) bool {
		return active[i].Address < active[j].Address
	})
	return active
}

func calculateQuorumStake(totalStake *big.Int, threshold float64) *big.Int {
	scaledThreshold := int64(threshold * 10000)
	if scaledThreshold <= 0 {
		scaledThreshold = 6667
	}
	numerator := new(big.Int).Mul(totalStake, big.NewInt(scaledThreshold))
	return numerator.Div(numerator, big.NewInt(10000))
}

func partitionGroupsForSlot(windows []SimulationPartitionWindow, slot uint64) (*SimulationPartitionWindow, bool, map[string]int) {
	groups := make(map[string]int)
	for _, window := range windows {
		if slot < window.StartSlot || slot > window.EndSlot {
			continue
		}
		for idx, members := range window.Groups {
			for _, addr := range members {
				groups[addr] = idx
			}
		}
		currentWindow := window
		return &currentWindow, true, groups
	}
	return nil, false, groups
}

func delayForValidator(windows []SimulationDelayedAttestation, validatorAddr string, slot uint64) uint64 {
	for _, window := range windows {
		if validatorAddr != window.Validator {
			continue
		}
		if slot < window.StartSlot || slot > window.EndSlot {
			continue
		}
		return window.DelaySlots
	}
	return 0
}

func updateSlotFinalization(results []SimulationSlotResult, blockHash string, finalizedAt uint64) {
	for i := range results {
		if results[i].BlockHash == blockHash {
			results[i].FinalizedAt = finalizedAt
			return
		}
	}
}
