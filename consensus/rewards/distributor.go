// consensus/rewards/distributor.go

// Comprehensive rewards distribution system for Proof of Stake consensus
// Features:
// - Block rewards with configurable distribution ratios
// - Validator commission handling with automatic distribution
// - Delegator rewards proportional to stake weight
// - Performance-based reward adjustments
// - Anti-centralization incentives and penalties
// - Cross-shard reward coordination
// - Reward pool management and sustainability
// - Tax and fee collection for network treasury

package rewards

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/core/state"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// Distributor manages reward distribution across the network
type Distributor struct {
	config     *config.Config
	worldState *state.WorldState

	// Reward pools
	validatorRewardPool int64
	delegatorRewardPool int64
	communityPool       int64
	developmentPool     int64

	// Distribution settings
	baseBlockReward       int64
	inflationRate         float64
	communityTaxRate      float64
	proposerBonusRate     float64
	performanceMultiplier float64

	// Anti-centralization settings
	maxValidatorRewardShare float64 // Maximum reward share for any single validator
	concentrationPenalty    float64 // Penalty for high concentration

	// Reward tracking
	totalRewardsDistributed int64
	rewardHistory           map[string]*ValidatorRewardHistory
	epochRewards            map[uint64]*EpochRewardSummary

	// Performance tracking
	performanceScores map[string]float64
	performanceWindow int64

	// Synchronization
	mu sync.RWMutex

	// Current epoch for reward calculations
	currentEpoch uint64
}

// ValidatorRewardHistory tracks reward history for a validator
type ValidatorRewardHistory struct {
	ValidatorAddress   string                   `json:"validator_address"`
	TotalRewards       int64                    `json:"total_rewards"`
	TotalCommission    int64                    `json:"total_commission"`
	RewardsDistributed int64                    `json:"rewards_distributed"`
	EpochRewards       map[uint64]*EpochRewards `json:"epoch_rewards"`
	PerformanceHistory []PerformanceEntry       `json:"performance_history"`
	LastRewardTime     int64                    `json:"last_reward_time"`
	AverageAPY         float64                  `json:"average_apy"`
}

// EpochRewards represents rewards for a specific epoch
type EpochRewards struct {
	Epoch                uint64  `json:"epoch"`
	BlockReward          int64   `json:"block_reward"`
	Commission           int64   `json:"commission"`
	DelegatorRewards     int64   `json:"delegator_rewards"`
	PerformanceBonus     int64   `json:"performance_bonus"`
	ConcentrationPenalty int64   `json:"concentration_penalty"`
	BlocksProposed       int64   `json:"blocks_proposed"`
	AttestationsMade     int64   `json:"attestations_made"`
	PerformanceScore     float64 `json:"performance_score"`
	TotalStake           int64   `json:"total_stake"`
	Timestamp            int64   `json:"timestamp"`
}

// EpochRewardSummary summarizes rewards for an entire epoch
type EpochRewardSummary struct {
	Epoch                   uint64                 `json:"epoch"`
	TotalRewardsDistributed int64                  `json:"total_rewards_distributed"`
	ValidatorCount          int                    `json:"validator_count"`
	TotalStake              int64                  `json:"total_stake"`
	AveragePerformance      float64                `json:"average_performance"`
	TopValidators           []ValidatorRewardEntry `json:"top_validators"`
	CommunityTax            int64                  `json:"community_tax"`
	InflationRate           float64                `json:"inflation_rate"`
	Timestamp               int64                  `json:"timestamp"`
}

// ValidatorRewardEntry represents a validator's rewards in an epoch
type ValidatorRewardEntry struct {
	ValidatorAddress string  `json:"validator_address"`
	TotalReward      int64   `json:"total_reward"`
	Commission       int64   `json:"commission"`
	PerformanceScore float64 `json:"performance_score"`
	StakeShare       float64 `json:"stake_share"`
}

// PerformanceEntry tracks performance over time
type PerformanceEntry struct {
	Timestamp        int64   `json:"timestamp"`
	PerformanceScore float64 `json:"performance_score"`
	BlocksProposed   int64   `json:"blocks_proposed"`
	AttestationsMade int64   `json:"attestations_made"`
}

// RewardDistributionResult contains the result of reward distribution
type RewardDistributionResult struct {
	Epoch                     uint64                          `json:"epoch"`
	TotalRewardsDistributed   int64                           `json:"total_rewards_distributed"`
	ValidatorRewards          map[string]*ValidatorRewardInfo `json:"validator_rewards"`
	CommunityTaxCollected     int64                           `json:"community_tax_collected"`
	DistributionTime          time.Duration                   `json:"distribution_time"`
	ParticipatingValidators   int                             `json:"participating_validators"`
	AverageRewardPerValidator int64                           `json:"average_reward_per_validator"`
}

// ValidatorRewardInfo contains detailed reward information for a validator
type ValidatorRewardInfo struct {
	ValidatorAddress      string           `json:"validator_address"`
	BaseReward            int64            `json:"base_reward"`
	PerformanceBonus      int64            `json:"performance_bonus"`
	ProposerBonus         int64            `json:"proposer_bonus"`
	ConcentrationPenalty  int64            `json:"concentration_penalty"`
	TotalValidatorReward  int64            `json:"total_validator_reward"`
	Commission            int64            `json:"commission"`
	DelegatorRewards      int64            `json:"delegator_rewards"`
	DelegatorDistribution map[string]int64 `json:"delegator_distribution"`
	PerformanceScore      float64          `json:"performance_score"`
	StakeShare            float64          `json:"stake_share"`
}

// NewDistributor creates a new reward distributor
func NewDistributor(config *config.Config, worldState *state.WorldState) *Distributor {
	return &Distributor{
		config:                  config,
		worldState:              worldState,
		validatorRewardPool:     config.Economics.ValidatorRewardPool,
		baseBlockReward:         config.Economics.BlockReward,
		inflationRate:           config.Economics.InflationRate,
		communityTaxRate:        config.Economics.CommunityTax,
		proposerBonusRate:       config.Economics.BaseProposerReward + config.Economics.BonusProposerReward,
		performanceMultiplier:   1.0,
		maxValidatorRewardShare: 0.05, // 5% maximum share
		concentrationPenalty:    0.1,  // 10% penalty for concentration
		rewardHistory:           make(map[string]*ValidatorRewardHistory),
		epochRewards:            make(map[uint64]*EpochRewardSummary),
		performanceScores:       make(map[string]float64),
		performanceWindow:       100, // 100 blocks window
	}
}

// DistributeEpochRewards distributes rewards for an entire epoch
func (rd *Distributor) DistributeEpochRewards(epoch uint64) (*RewardDistributionResult, error) {
	rd.mu.Lock()
	defer rd.mu.Unlock()

	startTime := time.Now()
	rd.currentEpoch = epoch

	// Get active validators
	activeValidators := rd.worldState.GetActiveValidators()
	if len(activeValidators) == 0 {
		return nil, fmt.Errorf("no active validators for epoch %d", epoch)
	}

	// Calculate total stake
	totalStake := rd.calculateTotalStake(activeValidators)
	if totalStake == 0 {
		return nil, fmt.Errorf("total stake is zero for epoch %d", epoch)
	}

	// Calculate epoch rewards
	epochRewardPool := rd.calculateEpochRewardPool(epoch)

	// Collect community tax first
	communityTax := int64(float64(epochRewardPool) * rd.communityTaxRate)
	availableRewards := epochRewardPool - communityTax

	// Update community pool
	rd.communityPool += communityTax

	result := &RewardDistributionResult{
		Epoch:                   epoch,
		TotalRewardsDistributed: availableRewards,
		ValidatorRewards:        make(map[string]*ValidatorRewardInfo),
		CommunityTaxCollected:   communityTax,
		ParticipatingValidators: len(activeValidators),
	}

	// Calculate performance scores for all validators
	rd.updatePerformanceScores(activeValidators)

	// Distribute rewards to each validator
	for _, validator := range activeValidators {
		rewardInfo, err := rd.distributeValidatorRewards(validator, availableRewards, totalStake, epoch)
		if err != nil {
			return nil, fmt.Errorf("failed to distribute rewards for validator %s: %v", validator.Address, err)
		}

		result.ValidatorRewards[validator.Address] = rewardInfo
	}

	// Calculate average reward
	if len(result.ValidatorRewards) > 0 {
		totalDistributed := int64(0)
		for _, reward := range result.ValidatorRewards {
			totalDistributed += reward.TotalValidatorReward + reward.DelegatorRewards
		}
		result.AverageRewardPerValidator = totalDistributed / int64(len(result.ValidatorRewards))
	}

	result.DistributionTime = time.Since(startTime)

	// Record epoch summary
	rd.recordEpochSummary(epoch, result, activeValidators, totalStake)

	// Update total distributed
	rd.totalRewardsDistributed += result.TotalRewardsDistributed

	return result, nil
}

// DistributeBlockRewards distributes rewards for a single block
func (rd *Distributor) DistributeBlockRewards(blockHeight int64, proposerAddress string, attesters []string) error {
	rd.mu.Lock()
	defer rd.mu.Unlock()

	// Get proposer validator
	proposer, err := rd.worldState.GetValidator(proposerAddress)
	if err != nil {
		return fmt.Errorf("proposer validator not found: %v", err)
	}

	// Calculate block reward
	blockReward := rd.baseBlockReward

	// Add proposer bonus
	proposerBonus := int64(float64(blockReward) * rd.proposerBonusRate)
	totalProposerReward := blockReward + proposerBonus

	// Distribute proposer rewards
	commission := int64(float64(totalProposerReward) * proposer.Commission)
	delegatorReward := totalProposerReward - commission

	// Update proposer account
	if err := rd.worldState.GetAccountManager().AddRewards(proposerAddress, commission); err != nil {
		return fmt.Errorf("failed to add proposer commission: %v", err)
	}

	// Distribute to delegators
	if err := rd.distributeDelegatorRewards(proposer, delegatorReward); err != nil {
		return fmt.Errorf("failed to distribute delegator rewards: %v", err)
	}

	// Record in history
	rd.recordBlockReward(proposerAddress, totalProposerReward, commission, delegatorReward, blockHeight)

	// Distribute attester rewards (smaller amounts)
	attesterReward := blockReward / 10 // 10% of block reward for attesters
	if len(attesters) > 0 {
		rewardPerAttester := attesterReward / int64(len(attesters))
		for _, attesterAddr := range attesters {
			if err := rd.worldState.GetAccountManager().AddRewards(attesterAddr, rewardPerAttester); err != nil {
				// Log error but continue
				fmt.Printf("Failed to reward attester %s: %v\n", attesterAddr, err)
			}
		}
	}

	return nil
}

// distributeValidatorRewards distributes rewards for a specific validator in an epoch
func (rd *Distributor) distributeValidatorRewards(validator *core.Validator, totalRewardPool int64, totalStake int64, epoch uint64) (*ValidatorRewardInfo, error) {
	// Calculate base reward based on stake proportion
	stakeShare := float64(validator.Stake) / float64(totalStake)
	baseReward := int64(float64(totalRewardPool) * stakeShare)

	// Get performance score
	performanceScore := rd.performanceScores[validator.Address]
	if performanceScore == 0 {
		performanceScore = 1.0 // Default performance score
	}

	// Calculate performance bonus/penalty
	performanceMultiplier := performanceScore
	performanceBonus := int64(float64(baseReward) * (performanceMultiplier - 1.0))

	// Calculate concentration penalty
	concentrationPenalty := int64(0)
	if stakeShare > rd.maxValidatorRewardShare {
		penaltyRate := (stakeShare - rd.maxValidatorRewardShare) * rd.concentrationPenalty
		concentrationPenalty = int64(float64(baseReward) * penaltyRate)
	}

	// Calculate proposer bonus (if this validator proposed blocks)
	proposerBonus := rd.calculateProposerBonus(validator, epoch)

	// Total validator reward
	totalValidatorReward := baseReward + performanceBonus + proposerBonus - concentrationPenalty

	// Ensure non-negative reward
	if totalValidatorReward < 0 {
		totalValidatorReward = 0
	}

	// Calculate commission
	commission := int64(float64(totalValidatorReward) * validator.Commission)
	delegatorReward := totalValidatorReward - commission

	// Distribute commission to validator
	if err := rd.worldState.GetAccountManager().AddRewards(validator.Address, commission); err != nil {
		return nil, fmt.Errorf("failed to add validator commission: %v", err)
	}

	// Distribute rewards to delegators
	delegatorDistribution := make(map[string]int64)
	if delegatorReward > 0 {
		var err error
		delegatorDistribution, err = rd.distributeDelegatorRewardsDetailed(validator, delegatorReward)
		if err != nil {
			return nil, fmt.Errorf("failed to distribute delegator rewards: %v", err)
		}
	}

	// Record in validator history
	rd.recordValidatorEpochReward(validator.Address, epoch, &EpochRewards{
		Epoch:                epoch,
		BlockReward:          baseReward,
		Commission:           commission,
		DelegatorRewards:     delegatorReward,
		PerformanceBonus:     performanceBonus,
		ConcentrationPenalty: concentrationPenalty,
		PerformanceScore:     performanceScore,
		TotalStake:           validator.Stake,
		Timestamp:            time.Now().Unix(),
	})

	return &ValidatorRewardInfo{
		ValidatorAddress:      validator.Address,
		BaseReward:            baseReward,
		PerformanceBonus:      performanceBonus,
		ProposerBonus:         proposerBonus,
		ConcentrationPenalty:  concentrationPenalty,
		TotalValidatorReward:  totalValidatorReward,
		Commission:            commission,
		DelegatorRewards:      delegatorReward,
		DelegatorDistribution: delegatorDistribution,
		PerformanceScore:      performanceScore,
		StakeShare:            stakeShare,
	}, nil
}

// distributeDelegatorRewards distributes rewards to delegators
func (rd *Distributor) distributeDelegatorRewards(validator *core.Validator, totalReward int64) error {
	if totalReward <= 0 || validator.DelegatedStake == 0 {
		return nil
	}

	// Distribute proportionally to delegated stake
	for delegatorAddr, delegatedAmount := range validator.Delegators {
		delegatorShare := float64(delegatedAmount) / float64(validator.DelegatedStake)
		delegatorReward := int64(float64(totalReward) * delegatorShare)

		if delegatorReward > 0 {
			if err := rd.worldState.GetAccountManager().AddRewards(delegatorAddr, delegatorReward); err != nil {
				return fmt.Errorf("failed to reward delegator %s: %v", delegatorAddr, err)
			}
		}
	}

	return nil
}

// distributeDelegatorRewardsDetailed distributes rewards and returns distribution map
func (rd *Distributor) distributeDelegatorRewardsDetailed(validator *core.Validator, totalReward int64) (map[string]int64, error) {
	distribution := make(map[string]int64)

	if totalReward <= 0 || validator.DelegatedStake == 0 {
		return distribution, nil
	}

	// Distribute proportionally to delegated stake
	for delegatorAddr, delegatedAmount := range validator.Delegators {
		delegatorShare := float64(delegatedAmount) / float64(validator.DelegatedStake)
		delegatorReward := int64(float64(totalReward) * delegatorShare)

		if delegatorReward > 0 {
			if err := rd.worldState.GetAccountManager().AddRewards(delegatorAddr, delegatorReward); err != nil {
				return nil, fmt.Errorf("failed to reward delegator %s: %v", delegatorAddr, err)
			}
			distribution[delegatorAddr] = delegatorReward
		}
	}

	return distribution, nil
}

// calculateTotalStake calculates total stake of active validators
func (rd *Distributor) calculateTotalStake(validators []*core.Validator) int64 {
	total := int64(0)
	for _, validator := range validators {
		total += validator.Stake
	}
	return total
}

// calculateEpochRewardPool calculates total rewards available for an epoch
func (rd *Distributor) calculateEpochRewardPool(epoch uint64) int64 {
	// Base reward pool
	basePool := rd.validatorRewardPool / 365 // Daily allocation from yearly pool

	// Apply inflation
	inflationMultiplier := 1.0 + rd.inflationRate/365 // Daily inflation
	adjustedPool := int64(float64(basePool) * inflationMultiplier)

	// Epoch-based adjustments (could vary by epoch)
	epochMultiplier := rd.calculateEpochMultiplier(epoch)
	finalPool := int64(float64(adjustedPool) * epochMultiplier)

	return finalPool
}

// calculateEpochMultiplier calculates epoch-specific reward multiplier
func (rd *Distributor) calculateEpochMultiplier(epoch uint64) float64 {
	// Example: slightly higher rewards for early epochs to bootstrap network
	if epoch < 100 {
		return 1.2 // 20% bonus for first 100 epochs
	} else if epoch < 1000 {
		return 1.1 // 10% bonus for first 1000 epochs
	}
	return 1.0 // Normal rewards after bootstrap period
}

// updatePerformanceScores updates performance scores for all validators
func (rd *Distributor) updatePerformanceScores(validators []*core.Validator) {
	for _, validator := range validators {
		score := rd.calculatePerformanceScore(validator)
		rd.performanceScores[validator.Address] = score
	}
}

// calculatePerformanceScore calculates performance score for a validator
func (rd *Distributor) calculatePerformanceScore(validator *core.Validator) float64 {
	// Get validator metrics from manager (assuming it exists)
	// In a real implementation, you'd integrate with the validator manager

	// Base score from blocks proposed vs missed
	totalBlocks := validator.BlocksProposed + validator.BlocksMissed
	if totalBlocks == 0 {
		return 1.0 // Default score for new validators
	}

	// Uptime score (0.0 to 1.0)
	uptimeScore := float64(validator.BlocksProposed) / float64(totalBlocks)

	// Participation score (simplified)
	participationScore := 1.0
	if validator.BlocksMissed > validator.BlocksProposed {
		participationScore = 0.5 // Penalty for poor participation
	}

	// Age bonus (slight bonus for consistent validators)
	currentTime := time.Now().Unix()
	age := float64(currentTime - validator.CreatedAt)
	ageBonus := math.Min(age/(365*24*3600), 0.1) // Max 10% bonus for 1+ year validators

	// Combined score
	score := (uptimeScore * 0.6) + (participationScore * 0.3) + ageBonus

	// Ensure score is between 0.1 and 2.0 (min 10%, max 200%)
	if score < 0.1 {
		score = 0.1
	} else if score > 2.0 {
		score = 2.0
	}

	return score
}

// calculateProposerBonus calculates proposer bonus for an epoch
func (rd *Distributor) calculateProposerBonus(validator *core.Validator, epoch uint64) int64 {
	// This would integrate with consensus to track blocks proposed per epoch
	// For now, return proportional bonus based on total blocks proposed

	if validator.BlocksProposed == 0 {
		return 0
	}

	// Simplified calculation - in reality would track per-epoch
	expectedBlocks := int64(32)              // Slots per epoch
	actualBlocks := validator.BlocksProposed // This should be epoch-specific

	if actualBlocks > expectedBlocks {
		actualBlocks = expectedBlocks // Cap at expected
	}

	bonusRate := float64(actualBlocks) / float64(expectedBlocks)
	baseBonus := rd.baseBlockReward / 10 // 10% of block reward as base bonus

	return int64(float64(baseBonus) * bonusRate)
}

// recordBlockReward records a block reward in history
func (rd *Distributor) recordBlockReward(validatorAddr string, totalReward, commission, delegatorReward, blockHeight int64) {
	if rd.rewardHistory[validatorAddr] == nil {
		rd.rewardHistory[validatorAddr] = &ValidatorRewardHistory{
			ValidatorAddress: validatorAddr,
			EpochRewards:     make(map[uint64]*EpochRewards),
		}
	}

	history := rd.rewardHistory[validatorAddr]
	history.TotalRewards += totalReward
	history.TotalCommission += commission
	history.RewardsDistributed += delegatorReward
	history.LastRewardTime = time.Now().Unix()
}

// recordValidatorEpochReward records epoch reward for a validator
func (rd *Distributor) recordValidatorEpochReward(validatorAddr string, epoch uint64, epochReward *EpochRewards) {
	if rd.rewardHistory[validatorAddr] == nil {
		rd.rewardHistory[validatorAddr] = &ValidatorRewardHistory{
			ValidatorAddress: validatorAddr,
			EpochRewards:     make(map[uint64]*EpochRewards),
		}
	}

	history := rd.rewardHistory[validatorAddr]
	history.EpochRewards[epoch] = epochReward

	// Update totals
	history.TotalRewards += epochReward.BlockReward + epochReward.PerformanceBonus
	history.TotalCommission += epochReward.Commission
	history.RewardsDistributed += epochReward.DelegatorRewards
	history.LastRewardTime = epochReward.Timestamp

	// Update performance history
	perfEntry := PerformanceEntry{
		Timestamp:        epochReward.Timestamp,
		PerformanceScore: epochReward.PerformanceScore,
		BlocksProposed:   epochReward.BlocksProposed,
		AttestationsMade: epochReward.AttestationsMade,
	}
	history.PerformanceHistory = append(history.PerformanceHistory, perfEntry)

	// Keep only last 100 entries
	if len(history.PerformanceHistory) > 100 {
		history.PerformanceHistory = history.PerformanceHistory[1:]
	}

	// Calculate average APY
	history.AverageAPY = rd.calculateValidatorAPY(history)
}

// recordEpochSummary records summary for an entire epoch
func (rd *Distributor) recordEpochSummary(epoch uint64, result *RewardDistributionResult, validators []*core.Validator, totalStake int64) {
	// Calculate average performance
	totalPerformance := 0.0
	for _, validator := range validators {
		totalPerformance += rd.performanceScores[validator.Address]
	}
	avgPerformance := totalPerformance / float64(len(validators))

	// Get top validators by reward
	topValidators := make([]ValidatorRewardEntry, 0, len(result.ValidatorRewards))
	for addr, reward := range result.ValidatorRewards {
		topValidators = append(topValidators, ValidatorRewardEntry{
			ValidatorAddress: addr,
			TotalReward:      reward.TotalValidatorReward + reward.DelegatorRewards,
			Commission:       reward.Commission,
			PerformanceScore: reward.PerformanceScore,
			StakeShare:       reward.StakeShare,
		})
	}

	// Sort by total reward (descending)
	sort.Slice(topValidators, func(i, j int) bool {
		return topValidators[i].TotalReward > topValidators[j].TotalReward
	})

	// Keep only top 10
	if len(topValidators) > 10 {
		topValidators = topValidators[:10]
	}

	summary := &EpochRewardSummary{
		Epoch:                   epoch,
		TotalRewardsDistributed: result.TotalRewardsDistributed,
		ValidatorCount:          len(validators),
		TotalStake:              totalStake,
		AveragePerformance:      avgPerformance,
		TopValidators:           topValidators,
		CommunityTax:            result.CommunityTaxCollected,
		InflationRate:           rd.inflationRate,
		Timestamp:               time.Now().Unix(),
	}

	rd.epochRewards[epoch] = summary
}

// calculateValidatorAPY calculates APY for a validator
func (rd *Distributor) calculateValidatorAPY(history *ValidatorRewardHistory) float64 {
	if len(history.EpochRewards) == 0 {
		return 0.0
	}

	// Simple APY calculation based on recent rewards
	// In production, this would be more sophisticated
	recentRewards := int64(0)
	recentStake := int64(0)
	count := 0

	// Use last 30 epochs for APY calculation
	for _, epochReward := range history.EpochRewards {
		if count >= 30 {
			break
		}
		recentRewards += epochReward.BlockReward + epochReward.Commission
		recentStake += epochReward.TotalStake
		count++
	}

	if count == 0 || recentStake == 0 {
		return 0.0
	}

	avgRewardPerEpoch := float64(recentRewards) / float64(count)
	avgStakePerEpoch := float64(recentStake) / float64(count)

	// Assuming ~365 epochs per year (daily epochs)
	epochsPerYear := 365.0
	annualReward := avgRewardPerEpoch * epochsPerYear

	apy := (annualReward / avgStakePerEpoch) * 100.0

	return apy
}

// GetValidatorRewardHistory returns reward history for a validator
func (rd *Distributor) GetValidatorRewardHistory(validatorAddr string) (*ValidatorRewardHistory, error) {
	rd.mu.RLock()
	defer rd.mu.RUnlock()

	history, exists := rd.rewardHistory[validatorAddr]
	if !exists {
		return nil, fmt.Errorf("no reward history found for validator %s", validatorAddr)
	}

	// Return a copy
	historyCopy := *history
	return &historyCopy, nil
}

// GetEpochRewardSummary returns reward summary for an epoch
func (rd *Distributor) GetEpochRewardSummary(epoch uint64) (*EpochRewardSummary, error) {
	rd.mu.RLock()
	defer rd.mu.RUnlock()

	summary, exists := rd.epochRewards[epoch]
	if !exists {
		return nil, fmt.Errorf("no reward summary found for epoch %d", epoch)
	}

	// Return a copy
	summaryCopy := *summary
	return &summaryCopy, nil
}

// GetDistributorStats returns overall distributor statistics
func (rd *Distributor) GetDistributorStats() map[string]interface{} {
	rd.mu.RLock()
	defer rd.mu.RUnlock()

	return map[string]interface{}{
		"total_rewards_distributed": rd.totalRewardsDistributed,
		"validator_reward_pool":     rd.validatorRewardPool,
		"community_pool":            rd.communityPool,
		"development_pool":          rd.developmentPool,
		"base_block_reward":         rd.baseBlockReward,
		"inflation_rate":            rd.inflationRate,
		"community_tax_rate":        rd.communityTaxRate,
		"current_epoch":             rd.currentEpoch,
		"tracked_validators":        len(rd.rewardHistory),
		"epoch_summaries":           len(rd.epochRewards),
		"max_validator_share":       rd.maxValidatorRewardShare,
		"concentration_penalty":     rd.concentrationPenalty,
	}
}

// UpdateInflationRate updates the inflation rate
func (rd *Distributor) UpdateInflationRate(newRate float64) error {
	rd.mu.Lock()
	defer rd.mu.Unlock()

	if newRate < 0 || newRate > 1 {
		return fmt.Errorf("inflation rate must be between 0 and 1, got %f", newRate)
	}

	rd.inflationRate = newRate
	return nil
}

// UpdateCommunityTaxRate updates the community tax rate
func (rd *Distributor) UpdateCommunityTaxRate(newRate float64) error {
	rd.mu.Lock()
	defer rd.mu.Unlock()

	if newRate < 0 || newRate > 0.2 { // Max 20% community tax
		return fmt.Errorf("community tax rate must be between 0 and 0.2, got %f", newRate)
	}

	rd.communityTaxRate = newRate
	return nil
}

// GetCommunityPool returns current community pool balance
func (rd *Distributor) GetCommunityPool() int64 {
	rd.mu.RLock()
	defer rd.mu.RUnlock()
	return rd.communityPool
}

// WithdrawFromCommunityPool withdraws funds from community pool (governance action)
func (rd *Distributor) WithdrawFromCommunityPool(amount int64, recipient string) error {
	rd.mu.Lock()
	defer rd.mu.Unlock()

	if amount <= 0 {
		return fmt.Errorf("withdrawal amount must be positive")
	}

	if amount > rd.communityPool {
		return fmt.Errorf("insufficient community pool balance: have %d, need %d", rd.communityPool, amount)
	}

	if err := account.ValidateAddress(recipient); err != nil {
		return fmt.Errorf("invalid recipient address: %v", err)
	}

	// Transfer from community pool to recipient
	if err := rd.worldState.GetAccountManager().AddRewards(recipient, amount); err != nil {
		return fmt.Errorf("failed to transfer community funds: %v", err)
	}

	rd.communityPool -= amount
	return nil
}

// CalculateEstimatedAPY calculates estimated APY for a given stake amount
func (rd *Distributor) CalculateEstimatedAPY(stakeAmount int64) float64 {
	rd.mu.RLock()
	defer rd.mu.RUnlock()

	// Get current total stake
	activeValidators := rd.worldState.GetActiveValidators()
	totalStake := rd.calculateTotalStake(activeValidators)

	if totalStake == 0 {
		return 0.0
	}

	// Calculate annual reward pool
	annualRewardPool := rd.validatorRewardPool

	// Apply inflation
	inflatedPool := int64(float64(annualRewardPool) * (1.0 + rd.inflationRate))

	// Account for community tax
	netRewardPool := int64(float64(inflatedPool) * (1.0 - rd.communityTaxRate))

	// Calculate stake share
	projectedTotalStake := totalStake + stakeAmount
	stakeShare := float64(stakeAmount) / float64(projectedTotalStake)

	// Estimated annual reward
	estimatedAnnualReward := int64(float64(netRewardPool) * stakeShare)

	// Calculate APY
	apy := (float64(estimatedAnnualReward) / float64(stakeAmount)) * 100.0

	// Apply average performance multiplier
	avgPerformance := rd.calculateAveragePerformance()
	apy *= avgPerformance

	return apy
}

// calculateAveragePerformance calculates average performance across all validators
func (rd *Distributor) calculateAveragePerformance() float64 {
	if len(rd.performanceScores) == 0 {
		return 1.0
	}

	total := 0.0
	for _, score := range rd.performanceScores {
		total += score
	}

	return total / float64(len(rd.performanceScores))
}

// GetTopPerformers returns top performing validators by APY
func (rd *Distributor) GetTopPerformers(limit int) []ValidatorPerformance {
	rd.mu.RLock()
	defer rd.mu.RUnlock()

	var performers []ValidatorPerformance

	for addr, history := range rd.rewardHistory {
		validator, err := rd.worldState.GetValidator(addr)
		if err != nil {
			continue
		}

		performance := ValidatorPerformance{
			ValidatorAddress: addr,
			APY:              history.AverageAPY,
			TotalRewards:     history.TotalRewards,
			PerformanceScore: rd.performanceScores[addr],
			Stake:            validator.Stake,
			Commission:       validator.Commission,
			Active:           validator.Active,
		}

		performers = append(performers, performance)
	}

	// Sort by APY (descending)
	sort.Slice(performers, func(i, j int) bool {
		return performers[i].APY > performers[j].APY
	})

	if limit > 0 && len(performers) > limit {
		performers = performers[:limit]
	}

	return performers
}

// ValidatorPerformance represents validator performance metrics
type ValidatorPerformance struct {
	ValidatorAddress string  `json:"validator_address"`
	APY              float64 `json:"apy"`
	TotalRewards     int64   `json:"total_rewards"`
	PerformanceScore float64 `json:"performance_score"`
	Stake            int64   `json:"stake"`
	Commission       float64 `json:"commission"`
	Active           bool    `json:"active"`
}

// CalculateDelegatorRewards calculates expected rewards for a delegator
func (rd *Distributor) CalculateDelegatorRewards(validatorAddr string, delegationAmount int64, timeHorizonDays int) (*DelegatorRewardEstimate, error) {
	rd.mu.RLock()
	defer rd.mu.RUnlock()

	validator, err := rd.worldState.GetValidator(validatorAddr)
	if err != nil {
		return nil, fmt.Errorf("validator not found: %v", err)
	}

	history, exists := rd.rewardHistory[validatorAddr]
	if !exists {
		return nil, fmt.Errorf("no reward history for validator %s", validatorAddr)
	}

	// Calculate expected annual rewards
	validatorAPY := history.AverageAPY
	delegatorAPY := validatorAPY * (1.0 - validator.Commission) // After commission

	// Calculate rewards over time horizon
	annualReward := int64((float64(delegationAmount) * delegatorAPY) / 100.0)
	periodReward := (annualReward * int64(timeHorizonDays)) / 365

	// Apply performance adjustment
	performanceScore := rd.performanceScores[validatorAddr]
	if performanceScore == 0 {
		performanceScore = 1.0
	}
	adjustedReward := int64(float64(periodReward) * performanceScore)

	return &DelegatorRewardEstimate{
		ValidatorAddress:    validatorAddr,
		DelegationAmount:    delegationAmount,
		TimeHorizonDays:     timeHorizonDays,
		EstimatedReward:     adjustedReward,
		ValidatorAPY:        validatorAPY,
		DelegatorAPY:        delegatorAPY,
		ValidatorCommission: validator.Commission,
		PerformanceScore:    performanceScore,
		RiskFactor:          rd.calculateRiskFactor(validator),
	}, nil
}

// DelegatorRewardEstimate represents estimated rewards for a delegator
type DelegatorRewardEstimate struct {
	ValidatorAddress    string  `json:"validator_address"`
	DelegationAmount    int64   `json:"delegation_amount"`
	TimeHorizonDays     int     `json:"time_horizon_days"`
	EstimatedReward     int64   `json:"estimated_reward"`
	ValidatorAPY        float64 `json:"validator_apy"`
	DelegatorAPY        float64 `json:"delegator_apy"`
	ValidatorCommission float64 `json:"validator_commission"`
	PerformanceScore    float64 `json:"performance_score"`
	RiskFactor          float64 `json:"risk_factor"`
}

// calculateRiskFactor calculates risk factor for a validator
func (rd *Distributor) calculateRiskFactor(validator *core.Validator) float64 {
	// Base risk factors
	riskFactor := 0.0

	// Commission risk (higher commission = higher risk for delegators)
	commissionRisk := validator.Commission * 0.5

	// Performance risk (lower performance = higher risk)
	performanceScore := rd.performanceScores[validator.Address]
	if performanceScore == 0 {
		performanceScore = 1.0
	}
	performanceRisk := (2.0 - performanceScore) * 0.3

	// Concentration risk (higher stake concentration = higher risk)
	totalStake := rd.calculateTotalStake(rd.worldState.GetActiveValidators())
	stakeShare := float64(validator.Stake) / float64(totalStake)
	concentrationRisk := 0.0
	if stakeShare > 0.1 { // 10% threshold
		concentrationRisk = (stakeShare - 0.1) * 0.5
	}

	// Jail risk (recent jailing increases risk)
	jailRisk := 0.0
	if validator.JailUntil > time.Now().Unix()-30*24*3600 { // Jailed in last 30 days
		jailRisk = 0.2
	}

	riskFactor = commissionRisk + performanceRisk + concentrationRisk + jailRisk

	// Cap risk factor between 0 and 1
	if riskFactor > 1.0 {
		riskFactor = 1.0
	}

	return riskFactor
}

// GetRewardProjections calculates reward projections for different scenarios
func (rd *Distributor) GetRewardProjections(stakeAmount int64) *RewardProjections {
	rd.mu.RLock()
	defer rd.mu.RUnlock()

	projections := &RewardProjections{
		StakeAmount: stakeAmount,
		Scenarios:   make(map[string]*RewardScenario),
	}

	// Conservative scenario (low performance)
	projections.Scenarios["conservative"] = &RewardScenario{
		Name:                  "Conservative",
		APY:                   rd.CalculateEstimatedAPY(stakeAmount) * 0.8, // 80% of estimated
		PerformanceAssumption: 0.8,
		MonthlyReward:         int64((float64(stakeAmount) * projections.Scenarios["conservative"].APY * 0.8) / 1200), // Monthly
		AnnualReward:          int64((float64(stakeAmount) * projections.Scenarios["conservative"].APY * 0.8) / 100),
	}

	// Optimistic scenario (high performance)
	estimatedAPY := rd.CalculateEstimatedAPY(stakeAmount)
	projections.Scenarios["optimistic"] = &RewardScenario{
		Name:                  "Optimistic",
		APY:                   estimatedAPY * 1.2, // 120% of estimated
		PerformanceAssumption: 1.2,
		MonthlyReward:         int64((float64(stakeAmount) * estimatedAPY * 1.2) / 1200),
		AnnualReward:          int64((float64(stakeAmount) * estimatedAPY * 1.2) / 100),
	}

	// Realistic scenario (average performance)
	projections.Scenarios["realistic"] = &RewardScenario{
		Name:                  "Realistic",
		APY:                   estimatedAPY,
		PerformanceAssumption: 1.0,
		MonthlyReward:         int64((float64(stakeAmount) * estimatedAPY) / 1200),
		AnnualReward:          int64((float64(stakeAmount) * estimatedAPY) / 100),
	}

	return projections
}

// RewardProjections represents reward projections for different scenarios
type RewardProjections struct {
	StakeAmount int64                      `json:"stake_amount"`
	Scenarios   map[string]*RewardScenario `json:"scenarios"`
}

// RewardScenario represents a specific reward scenario
type RewardScenario struct {
	Name                  string  `json:"name"`
	APY                   float64 `json:"apy"`
	PerformanceAssumption float64 `json:"performance_assumption"`
	MonthlyReward         int64   `json:"monthly_reward"`
	AnnualReward          int64   `json:"annual_reward"`
}

// GetRewardStatistics returns comprehensive reward statistics
func (rd *Distributor) GetRewardStatistics() *RewardStatistics {
	rd.mu.RLock()
	defer rd.mu.RUnlock()

	stats := &RewardStatistics{
		TotalRewardsDistributed: rd.totalRewardsDistributed,
		CommunityPoolBalance:    rd.communityPool,
		ValidatorRewardPool:     rd.validatorRewardPool,
		DevelopmentPool:         rd.developmentPool,
		CurrentInflationRate:    rd.inflationRate,
		CommunityTaxRate:        rd.communityTaxRate,
		TrackedValidators:       len(rd.rewardHistory),
		EpochsTracked:           len(rd.epochRewards),
	}

	// Calculate average APY across all validators
	totalAPY := 0.0
	validatorCount := 0
	for _, history := range rd.rewardHistory {
		if history.AverageAPY > 0 {
			totalAPY += history.AverageAPY
			validatorCount++
		}
	}
	if validatorCount > 0 {
		stats.AverageValidatorAPY = totalAPY / float64(validatorCount)
	}

	// Calculate network staking ratio
	activeValidators := rd.worldState.GetActiveValidators()
	totalStaked := rd.calculateTotalStake(activeValidators)
	totalSupply := rd.worldState.GetTotalSupply()
	if totalSupply > 0 {
		stats.NetworkStakingRatio = float64(totalStaked) / float64(totalSupply)
	}

	// Get recent epoch performance
	if len(rd.epochRewards) > 0 {
		var recentEpochs []*EpochRewardSummary
		for _, summary := range rd.epochRewards {
			recentEpochs = append(recentEpochs, summary)
		}

		// Sort by epoch (descending)
		sort.Slice(recentEpochs, func(i, j int) bool {
			return recentEpochs[i].Epoch > recentEpochs[j].Epoch
		})

		// Take last 10 epochs for recent performance
		limit := 10
		if len(recentEpochs) < limit {
			limit = len(recentEpochs)
		}

		totalPerformance := 0.0
		for i := 0; i < limit; i++ {
			totalPerformance += recentEpochs[i].AveragePerformance
		}
		stats.RecentAveragePerformance = totalPerformance / float64(limit)
	}

	return stats
}

// RewardStatistics represents comprehensive reward statistics
type RewardStatistics struct {
	TotalRewardsDistributed  int64   `json:"total_rewards_distributed"`
	CommunityPoolBalance     int64   `json:"community_pool_balance"`
	ValidatorRewardPool      int64   `json:"validator_reward_pool"`
	DevelopmentPool          int64   `json:"development_pool"`
	CurrentInflationRate     float64 `json:"current_inflation_rate"`
	CommunityTaxRate         float64 `json:"community_tax_rate"`
	AverageValidatorAPY      float64 `json:"average_validator_apy"`
	NetworkStakingRatio      float64 `json:"network_staking_ratio"`
	RecentAveragePerformance float64 `json:"recent_average_performance"`
	TrackedValidators        int     `json:"tracked_validators"`
	EpochsTracked            int     `json:"epochs_tracked"`
}

// CleanupOldRewardData removes old reward data to manage memory
func (rd *Distributor) CleanupOldRewardData(maxEpochsToKeep int) {
	rd.mu.Lock()
	defer rd.mu.Unlock()

	if len(rd.epochRewards) <= maxEpochsToKeep {
		return
	}

	// Get epochs sorted by number
	var epochs []uint64
	for epoch := range rd.epochRewards {
		epochs = append(epochs, epoch)
	}
	sort.Slice(epochs, func(i, j int) bool {
		return epochs[i] < epochs[j]
	})

	// Remove oldest epochs
	epochsToRemove := len(epochs) - maxEpochsToKeep
	for i := 0; i < epochsToRemove; i++ {
		delete(rd.epochRewards, epochs[i])
	}

	// Cleanup validator histories (keep last 50 epochs per validator)
	for addr, history := range rd.rewardHistory {
		if len(history.EpochRewards) > 50 {
			// Keep only recent epochs
			var recentEpochs []uint64
			for epoch := range history.EpochRewards {
				recentEpochs = append(recentEpochs, epoch)
			}
			sort.Slice(recentEpochs, func(i, j int) bool {
				return recentEpochs[i] > recentEpochs[j]
			})

			// Create new map with recent epochs only
			newEpochRewards := make(map[uint64]*EpochRewards)
			for i := 0; i < 50 && i < len(recentEpochs); i++ {
				epoch := recentEpochs[i]
				newEpochRewards[epoch] = history.EpochRewards[epoch]
			}
			history.EpochRewards = newEpochRewards
		}
	}
}
