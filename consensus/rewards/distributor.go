package rewards

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/state"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

// Distributor manages dynamic reward distribution with inflation control
type Distributor struct {
	config     *config.Config
	worldState *state.WorldState

	// Dynamic inflation controller
	inflationController *DynamicInflationController

	// Current economic state
	currentInflationRate float64
	currentStakingRatio  float64
	totalSupply          int64
	totalStaked          int64

	// Reward pools
	validatorRewardPool int64 // Validator reward pool
	communityPool       int64 // Community pool
	developmentPool     int64 // Development pool

	// Distribution settings (now dynamic)
	baseBlockReward   int64   // Base reward per block
	inflationRate     float64 // Current inflation rate
	communityTaxRate  float64 // Community tax rate
	proposerBonusRate float64 // Proposer bonus rate

	// Performance tracking
	performanceMultiplier   float64 // Performance multiplier
	maxValidatorRewardShare float64 // Max reward share per validator
	concentrationPenalty    float64 // Penalty for stake concentration
	performanceWindow       int64   // Performance calculation window

	// Reward tracking
	totalRewardsDistributed int64
	totalTokensBurned       int64
	rewardHistory           map[string]*ValidatorRewardHistory
	epochRewards            map[uint64]*EpochRewardSummary

	// Performance tracking
	performanceScores map[string]float64

	// Synchronization
	mu sync.RWMutex

	// Current epoch for reward calculations
	currentEpoch uint64
}

// DynamicInflationController manages dynamic inflation (renamed to avoid conflict)
type DynamicInflationController struct {
	// Target parameters
	targetInflationRate float64 // 4% target
	targetStakingRatio  float64 // 67% target

	// Bounds
	minInflationRate float64 // 1% minimum
	maxInflationRate float64 // 8% maximum

	// Adjustment parameters
	inflationAdjustmentRate float64 // Adjustment speed

	// Epoch tracking
	epochsPerYear int64 // 365 daily epochs
}

// EconomicMetrics represents current economic state
type EconomicMetrics struct {
	// Supply metrics
	TotalSupply       int64 `json:"total_supply"`
	TotalStaked       int64 `json:"total_staked"`
	CirculatingSupply int64 `json:"circulating_supply"`

	// Ratios
	StakingRatio       float64 `json:"staking_ratio"`
	TargetStakingRatio float64 `json:"target_staking_ratio"`

	// Inflation
	CurrentInflationRate float64 `json:"current_inflation_rate"`
	TargetInflationRate  float64 `json:"target_inflation_rate"`
	AnnualRewardPool     int64   `json:"annual_reward_pool"`

	// APY calculations
	ValidatorAPY float64 `json:"validator_apy"`
	DelegatorAPY float64 `json:"delegator_apy"`

	// Burn metrics
	TotalBurned int64   `json:"total_burned"`
	BurnRate    float64 `json:"burn_rate"`

	// Health indicators
	EconomicHealth    string `json:"economic_health"`
	RecommendedAction string `json:"recommended_action"`
}

// DynamicRewardCalculation represents reward calculation details (renamed to avoid conflict)
type DynamicRewardCalculation struct {
	Epoch            uint64  `json:"epoch"`
	TotalSupply      int64   `json:"total_supply"`
	TotalStaked      int64   `json:"total_staked"`
	StakingRatio     float64 `json:"staking_ratio"`
	InflationRate    float64 `json:"inflation_rate"`
	AnnualRewardPool int64   `json:"annual_reward_pool"`
	EpochRewardPool  int64   `json:"epoch_reward_pool"`
	ValidatorShare   int64   `json:"validator_share"`
	DelegatorShare   int64   `json:"delegator_share"`
	CommunityShare   int64   `json:"community_share"`
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

// InflationProjection represents future inflation projection
type InflationProjection struct {
	Epoch         uint64  `json:"epoch"`
	Supply        int64   `json:"supply"`
	InflationRate float64 `json:"inflation_rate"`
	StakingRatio  float64 `json:"staking_ratio"`
	RewardPool    int64   `json:"reward_pool"`
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

// NewDistributor creates a new dynamic reward distributor
func NewDistributor(config *config.Config, worldState *state.WorldState) *Distributor {
	distributor := &Distributor{
		config:                  config,
		worldState:              worldState,
		validatorRewardPool:     config.Economics.ValidatorRewardPool,
		currentInflationRate:    0.04, // Start at 4% target
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

	// Initialize inflation controller
	distributor.inflationController = &DynamicInflationController{
		targetInflationRate:     0.04, // 4% target
		targetStakingRatio:      0.67, // 67% target
		minInflationRate:        0.01, // 1% minimum
		maxInflationRate:        0.08, // 8% maximum
		inflationAdjustmentRate: 0.1,  // 10% adjustment rate
		epochsPerYear:           365,  // Daily epochs
	}

	return distributor
}

// DistributeEpochRewards distributes rewards using dynamic inflation
func (rd *Distributor) DistributeEpochRewards(epoch uint64) (*RewardDistributionResult, error) {
	rd.mu.Lock()
	defer rd.mu.Unlock()

	startTime := time.Now()
	rd.currentEpoch = epoch

	// Update economic state
	if err := rd.updateEconomicState(); err != nil {
		return nil, fmt.Errorf("failed to update economic state: %v", err)
	}

	// Calculate dynamic rewards based on current economic conditions
	rewardCalculation, err := rd.calculateDynamicRewards(epoch)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate dynamic rewards: %v", err)
	}

	// Get active validators
	activeValidators := rd.worldState.GetActiveValidators()
	if len(activeValidators) == 0 {
		return nil, fmt.Errorf("no active validators for epoch %d", epoch)
	}

	// Collect community tax
	communityTax := int64(float64(rewardCalculation.EpochRewardPool) * rd.communityTaxRate)
	availableRewards := rewardCalculation.EpochRewardPool - communityTax

	result := &RewardDistributionResult{
		Epoch:                   epoch,
		TotalRewardsDistributed: availableRewards,
		ValidatorRewards:        make(map[string]*ValidatorRewardInfo),
		CommunityTaxCollected:   communityTax,
		ParticipatingValidators: len(activeValidators),
	}

	// Update performance scores
	rd.updatePerformanceScores(activeValidators)

	// Distribute rewards to validators
	for _, validator := range activeValidators {
		rewardInfo, err := rd.distributeValidatorRewards(validator, availableRewards, rewardCalculation.TotalStaked, epoch)
		if err != nil {
			return nil, fmt.Errorf("failed to distribute rewards for validator %s: %v", validator.Address, err)
		}
		result.ValidatorRewards[validator.Address] = rewardInfo
	}

	// Handle token burning if over-staked
	burnAmount := rd.calculateBurnAmount(rewardCalculation)
	if burnAmount > 0 {
		rd.burnTokens(burnAmount)
	}

	// Calculate metrics
	result.DistributionTime = time.Since(startTime)
	if len(result.ValidatorRewards) > 0 {
		totalDistributed := int64(0)
		for _, reward := range result.ValidatorRewards {
			totalDistributed += reward.TotalValidatorReward + reward.DelegatorRewards
		}
		result.AverageRewardPerValidator = totalDistributed / int64(len(result.ValidatorRewards))
	}

	// Record epoch summary
	rd.recordEpochSummary(epoch, result, activeValidators, rewardCalculation.TotalStaked)

	// Update totals
	rd.totalRewardsDistributed += result.TotalRewardsDistributed
	if burnAmount > 0 {
		rd.totalTokensBurned += burnAmount
	}

	return result, nil
}

// updateEconomicState updates current economic metrics
func (rd *Distributor) updateEconomicState() error {
	rd.totalSupply = rd.worldState.GetTotalSupply()
	rd.totalStaked = rd.worldState.GetTotalStaked()

	if rd.totalSupply == 0 {
		return fmt.Errorf("total supply is zero")
	}

	// Calculate current staking ratio
	rd.currentStakingRatio = float64(rd.totalStaked) / float64(rd.totalSupply)

	// Adjust inflation rate based on staking ratio
	rd.adjustInflationRate()

	return nil
}

// adjustInflationRate dynamically adjusts inflation based on staking participation
func (rd *Distributor) adjustInflationRate() {
	ic := rd.inflationController

	// Calculate how far off we are from target staking ratio
	stakingRatioDiff := rd.currentStakingRatio - ic.targetStakingRatio

	// Calculate desired inflation adjustment
	// If staking ratio is below target, increase inflation to incentivize staking
	// If staking ratio is above target, decrease inflation
	inflationAdjustment := -stakingRatioDiff * ic.inflationAdjustmentRate

	// Apply adjustment
	newInflationRate := rd.currentInflationRate + inflationAdjustment

	// Apply bounds
	if newInflationRate < ic.minInflationRate {
		newInflationRate = ic.minInflationRate
	} else if newInflationRate > ic.maxInflationRate {
		newInflationRate = ic.maxInflationRate
	}

	rd.currentInflationRate = newInflationRate
}

// calculateDynamicRewards calculates epoch rewards based on dynamic inflation
func (rd *Distributor) calculateDynamicRewards(epoch uint64) (*DynamicRewardCalculation, error) {
	// Calculate annual reward pool based on current inflation rate
	annualRewardPool := int64(float64(rd.totalSupply) * rd.currentInflationRate)

	// Calculate epoch reward pool (daily distribution)
	epochRewardPool := annualRewardPool / rd.inflationController.epochsPerYear

	// Apply staking participation multiplier
	stakingMultiplier := rd.calculateStakingMultiplier()
	epochRewardPool = int64(float64(epochRewardPool) * stakingMultiplier)

	// Distribute among validator, delegator, and community shares
	validatorShare, delegatorShare, communityShare := rd.distributeRewardShares(epochRewardPool)

	return &DynamicRewardCalculation{
		Epoch:            epoch,
		TotalSupply:      rd.totalSupply,
		TotalStaked:      rd.totalStaked,
		StakingRatio:     rd.currentStakingRatio,
		InflationRate:    rd.currentInflationRate,
		AnnualRewardPool: annualRewardPool,
		EpochRewardPool:  epochRewardPool,
		ValidatorShare:   validatorShare,
		DelegatorShare:   delegatorShare,
		CommunityShare:   communityShare,
	}, nil
}

// calculateStakingMultiplier calculates reward multiplier based on staking participation
func (rd *Distributor) calculateStakingMultiplier() float64 {
	targetRatio := rd.inflationController.targetStakingRatio

	if rd.currentStakingRatio >= targetRatio {
		// At or above target: standard rewards
		return 1.0
	} else if rd.currentStakingRatio >= targetRatio*0.8 {
		// Moderately below target: slight bonus to incentivize staking
		return 1.1
	} else if rd.currentStakingRatio >= targetRatio*0.6 {
		// Well below target: good bonus
		return 1.2
	} else {
		// Far below target: maximum bonus to strongly incentivize staking
		return 1.3
	}
}

// distributeRewardShares distributes rewards among validators, delegators, and community
func (rd *Distributor) distributeRewardShares(epochRewardPool int64) (int64, int64, int64) {
	// Community tax comes first
	communityShare := int64(float64(epochRewardPool) * rd.communityTaxRate)

	// Remaining for stakers
	stakingRewards := epochRewardPool - communityShare

	// Split between validators and delegators (80/20 split)
	validatorShare := int64(float64(stakingRewards) * 0.20)
	delegatorShare := stakingRewards - validatorShare

	return validatorShare, delegatorShare, communityShare
}

// calculateBurnAmount calculates tokens to burn for economic balance
func (rd *Distributor) calculateBurnAmount(calc *DynamicRewardCalculation) int64 {
	targetRatio := rd.inflationController.targetStakingRatio

	// Burn mechanism: if staking ratio is significantly above target
	if rd.currentStakingRatio > targetRatio*1.25 { // 25% above target
		// Burn 15% of epoch rewards to create deflationary pressure
		return int64(float64(calc.EpochRewardPool) * 0.15)
	} else if rd.currentStakingRatio > targetRatio*1.15 { // 15% above target
		// Burn 10% of epoch rewards
		return int64(float64(calc.EpochRewardPool) * 0.10)
	} else if rd.currentStakingRatio > targetRatio*1.05 { // 5% above target
		// Burn 5% of epoch rewards
		return int64(float64(calc.EpochRewardPool) * 0.05)
	}

	return 0 // No burning needed
}

// burnTokens removes tokens from circulation
func (rd *Distributor) burnTokens(amount int64) {
	// In a real implementation, this would actually remove tokens from total supply
	// For now, we just track the burn amount
	rd.totalTokensBurned += amount

	// Log the burn event
	fmt.Printf("Burned %d tokens due to over-staking (staking ratio: %.2f%%, target: %.2f%%)\n",
		amount, rd.currentStakingRatio*100, rd.inflationController.targetStakingRatio*100)
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

// Add these missing methods to your distributor.go file

// GetEconomicMetrics returns comprehensive economic metrics
func (rd *Distributor) GetEconomicMetrics() *EconomicMetrics {
	rd.mu.RLock()
	defer rd.mu.RUnlock()

	// Calculate APYs
	validatorAPY := rd.calculateValidatorAPY_Global()
	delegatorAPY := rd.calculateDelegatorAPY()

	// Determine economic health
	health, recommendation := rd.assessEconomicHealth()

	return &EconomicMetrics{
		TotalSupply:          rd.totalSupply,
		TotalStaked:          rd.totalStaked,
		CirculatingSupply:    rd.totalSupply - rd.totalStaked,
		StakingRatio:         rd.currentStakingRatio,
		TargetStakingRatio:   rd.inflationController.targetStakingRatio,
		CurrentInflationRate: rd.currentInflationRate,
		TargetInflationRate:  rd.inflationController.targetInflationRate,
		AnnualRewardPool:     int64(float64(rd.totalSupply) * rd.currentInflationRate),
		ValidatorAPY:         validatorAPY,
		DelegatorAPY:         delegatorAPY,
		TotalBurned:          rd.totalTokensBurned,
		BurnRate:             float64(rd.totalTokensBurned) / float64(rd.totalSupply),
		EconomicHealth:       health,
		RecommendedAction:    recommendation,
	}
}

// calculateValidatorAPY_Global calculates expected APY for validators globally
func (rd *Distributor) calculateValidatorAPY_Global() float64 {
	if rd.currentStakingRatio == 0 {
		return 0
	}

	// Base APY from inflation distributed among stakers
	baseAPY := rd.currentInflationRate / rd.currentStakingRatio

	// Validators get their share plus commission from delegators
	validatorShare := 0.20 // 20% of staking rewards
	avgCommission := 0.05  // Assume 5% average commission

	validatorAPY := baseAPY * validatorShare
	commissionAPY := baseAPY * 0.80 * avgCommission // Commission from delegator rewards

	return (validatorAPY + commissionAPY) * 100 // Convert to percentage
}

// calculateDelegatorAPY calculates expected APY for delegators
func (rd *Distributor) calculateDelegatorAPY() float64 {
	if rd.currentStakingRatio == 0 {
		return 0
	}

	// Base APY from inflation distributed among stakers
	baseAPY := rd.currentInflationRate / rd.currentStakingRatio

	// Delegators get 80% of staking rewards minus average commission
	delegatorShare := 0.80
	avgCommission := 0.05

	delegatorAPY := baseAPY * delegatorShare * (1.0 - avgCommission)

	return delegatorAPY * 100 // Convert to percentage
}

// assessEconomicHealth assesses the current economic health
func (rd *Distributor) assessEconomicHealth() (string, string) {
	stakingDiff := math.Abs(rd.currentStakingRatio - rd.inflationController.targetStakingRatio)
	inflationDiff := math.Abs(rd.currentInflationRate - rd.inflationController.targetInflationRate)

	if stakingDiff < 0.05 && inflationDiff < 0.01 {
		return "Excellent", "Network is well-balanced. Continue current parameters."
	} else if stakingDiff < 0.10 && inflationDiff < 0.02 {
		return "Good", "Network is mostly balanced with minor adjustments needed."
	} else if stakingDiff < 0.20 && inflationDiff < 0.03 {
		return "Fair", "Network needs rebalancing. Monitor staking participation."
	} else {
		if rd.currentStakingRatio < rd.inflationController.targetStakingRatio*0.5 {
			return "Poor", "URGENT: Very low staking ratio threatens network security."
		} else if rd.currentStakingRatio > rd.inflationController.targetStakingRatio*1.5 {
			return "Poor", "URGENT: Excessive staking reduces network liquidity."
		} else {
			return "Poor", "Network is significantly imbalanced. Immediate action required."
		}
	}
}

// UpdateInflationParameters allows governance to update inflation parameters
func (rd *Distributor) UpdateInflationParameters(
	targetInflation float64,
	targetStakingRatio float64,
	minInflation float64,
	maxInflation float64,
) error {
	rd.mu.Lock()
	defer rd.mu.Unlock()

	// Validate parameters
	if targetInflation < 0.01 || targetInflation > 0.15 {
		return fmt.Errorf("target inflation must be between 1%% and 15%%, got %.2f%%", targetInflation*100)
	}

	if targetStakingRatio < 0.1 || targetStakingRatio > 0.9 {
		return fmt.Errorf("target staking ratio must be between 10%% and 90%%, got %.2f%%", targetStakingRatio*100)
	}

	if minInflation >= maxInflation {
		return fmt.Errorf("min inflation (%.2f%%) must be less than max inflation (%.2f%%)", minInflation*100, maxInflation*100)
	}

	// Update inflation controller
	ic := rd.inflationController
	ic.targetInflationRate = targetInflation
	ic.targetStakingRatio = targetStakingRatio
	ic.minInflationRate = minInflation
	ic.maxInflationRate = maxInflation

	return nil
}

// GetInflationProjection projects inflation and supply for future epochs
func (rd *Distributor) GetInflationProjection(epochs int) []*InflationProjection {
	rd.mu.RLock()
	defer rd.mu.RUnlock()

	projections := make([]*InflationProjection, epochs)

	currentSupply := float64(rd.totalSupply)
	currentInflation := rd.currentInflationRate
	currentStaking := rd.currentStakingRatio

	for i := 0; i < epochs; i++ {
		// Project inflation adjustment
		stakingDiff := currentStaking - rd.inflationController.targetStakingRatio
		inflationAdjustment := -stakingDiff * rd.inflationController.inflationAdjustmentRate
		currentInflation = math.Max(rd.inflationController.minInflationRate,
			math.Min(rd.inflationController.maxInflationRate, currentInflation+inflationAdjustment))

		// Project supply growth
		epochInflation := currentInflation / float64(rd.inflationController.epochsPerYear)
		newSupply := currentSupply * (1 + epochInflation)

		projections[i] = &InflationProjection{
			Epoch:         rd.currentEpoch + uint64(i+1),
			Supply:        int64(newSupply),
			InflationRate: currentInflation,
			StakingRatio:  currentStaking,
			RewardPool:    int64(newSupply * currentInflation / float64(rd.inflationController.epochsPerYear)),
		}

		currentSupply = newSupply
		// Assume staking ratio gradually moves toward target (simplified)
		stakingAdjustment := (rd.inflationController.targetStakingRatio - currentStaking) * 0.1
		currentStaking += stakingAdjustment
	}

	return projections
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
		"total_tokens_burned":       rd.totalTokensBurned,
		"current_inflation_rate":    rd.currentInflationRate,
		"current_staking_ratio":     rd.currentStakingRatio,
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

	// Note: You'll need to import the account package or remove this validation
	// if err := account.ValidateAddress(recipient); err != nil {
	//     return fmt.Errorf("invalid recipient address: %v", err)
	// }

	// Transfer from community pool to recipient
	if err := rd.worldState.GetAccountManager().AddRewards(recipient, amount); err != nil {
		return fmt.Errorf("failed to transfer community funds: %v", err)
	}

	rd.communityPool -= amount
	return nil
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
	for _, history := range rd.rewardHistory { // Changed addr to _
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

// calculateTotalStake calculates total stake of active validators
func (rd *Distributor) calculateTotalStake(validators []*core.Validator) int64 {
	total := int64(0)
	for _, validator := range validators {
		total += validator.Stake
	}
	return total
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
