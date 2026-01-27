package rewards

import (
	"fmt"
	"math"
	"math/big"
	"sort"
	"sync"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	coremath "github.com/thrylos-labs/go-thrylos/core/math" // Alias to avoid conflict with std lib
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

	// Token amounts as strings (BigInt)
	totalSupply string
	totalStaked string

	// Reward pools
	validatorRewardPool string

	// Token amounts as strings (BigInt)
	communityPool   string
	developmentPool string

	// Distribution settings
	baseBlockReward string

	// Rates stay float64
	inflationRate     float64
	communityTaxRate  float64
	proposerBonusRate float64

	// Performance tracking
	performanceMultiplier   float64
	maxValidatorRewardShare float64
	concentrationPenalty    float64
	performanceWindow       int64

	// Reward tracking (BigInt strings)
	totalRewardsDistributed string
	totalTokensBurned       string

	rewardHistory     map[string]*ValidatorRewardHistory
	epochRewards      map[uint64]*EpochRewardSummary
	performanceScores map[string]float64

	mu           sync.RWMutex
	currentEpoch uint64
}

// DynamicInflationController manages dynamic inflation
type DynamicInflationController struct {
	targetInflationRate     float64
	targetStakingRatio      float64
	minInflationRate        float64
	maxInflationRate        float64
	inflationAdjustmentRate float64
	epochsPerYear           int64
}

// EconomicMetrics represents current economic state
type EconomicMetrics struct {
	TotalSupply       string  `json:"total_supply"`
	TotalStaked       string  `json:"total_staked"`
	CirculatingSupply string  `json:"circulating_supply"`
	StakingRatio      float64 `json:"staking_ratio"`

	TargetStakingRatio   float64 `json:"target_staking_ratio"`
	CurrentInflationRate float64 `json:"current_inflation_rate"`
	TargetInflationRate  float64 `json:"target_inflation_rate"`
	AnnualRewardPool     string  `json:"annual_reward_pool"`

	ValidatorAPY float64 `json:"validator_apy"`
	DelegatorAPY float64 `json:"delegator_apy"`

	TotalBurned string  `json:"total_burned"`
	BurnRate    float64 `json:"burn_rate"`

	EconomicHealth    string `json:"economic_health"`
	RecommendedAction string `json:"recommended_action"`
}

// DynamicRewardCalculation represents reward calculation details
type DynamicRewardCalculation struct {
	Epoch            uint64  `json:"epoch"`
	TotalSupply      string  `json:"total_supply"`
	TotalStaked      string  `json:"total_staked"`
	StakingRatio     float64 `json:"staking_ratio"`
	InflationRate    float64 `json:"inflation_rate"`
	AnnualRewardPool string  `json:"annual_reward_pool"`
	EpochRewardPool  string  `json:"epoch_reward_pool"`
	ValidatorShare   string  `json:"validator_share"`
	DelegatorShare   string  `json:"delegator_share"`
	CommunityShare   string  `json:"community_share"`
}

// ValidatorRewardHistory tracks reward history for a validator
type ValidatorRewardHistory struct {
	ValidatorAddress   string                   `json:"validator_address"`
	TotalRewards       string                   `json:"total_rewards"`
	TotalCommission    string                   `json:"total_commission"`
	RewardsDistributed string                   `json:"rewards_distributed"`
	EpochRewards       map[uint64]*EpochRewards `json:"epoch_rewards"`
	PerformanceHistory []PerformanceEntry       `json:"performance_history"`
	LastRewardTime     int64                    `json:"last_reward_time"`
	AverageAPY         float64                  `json:"average_apy"`
}

// EpochRewards represents rewards for a specific epoch
type EpochRewards struct {
	Epoch                uint64  `json:"epoch"`
	BlockReward          string  `json:"block_reward"`
	Commission           string  `json:"commission"`
	DelegatorRewards     string  `json:"delegator_rewards"`
	PerformanceBonus     string  `json:"performance_bonus"`
	ConcentrationPenalty string  `json:"concentration_penalty"`
	BlocksProposed       int64   `json:"blocks_proposed"`
	AttestationsMade     int64   `json:"attestations_made"`
	PerformanceScore     float64 `json:"performance_score"`
	TotalStake           string  `json:"total_stake"`
	Timestamp            int64   `json:"timestamp"`
}

// EpochRewardSummary summarizes rewards for an entire epoch
type EpochRewardSummary struct {
	Epoch                   uint64                 `json:"epoch"`
	TotalRewardsDistributed string                 `json:"total_rewards_distributed"`
	ValidatorCount          int                    `json:"validator_count"`
	TotalStake              string                 `json:"total_stake"`
	AveragePerformance      float64                `json:"average_performance"`
	TopValidators           []ValidatorRewardEntry `json:"top_validators"`
	CommunityTax            string                 `json:"community_tax"`
	InflationRate           float64                `json:"inflation_rate"`
	Timestamp               int64                  `json:"timestamp"`
}

// ValidatorRewardEntry represents a validator's rewards in an epoch
type ValidatorRewardEntry struct {
	ValidatorAddress string  `json:"validator_address"`
	TotalReward      string  `json:"total_reward"`
	Commission       string  `json:"commission"`
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
	TotalRewardsDistributed   string                          `json:"total_rewards_distributed"`
	ValidatorRewards          map[string]*ValidatorRewardInfo `json:"validator_rewards"`
	CommunityTaxCollected     string                          `json:"community_tax_collected"`
	DistributionTime          time.Duration                   `json:"distribution_time"`
	ParticipatingValidators   int                             `json:"participating_validators"`
	AverageRewardPerValidator string                          `json:"average_reward_per_validator"`
}

// ValidatorRewardInfo contains detailed reward information for a validator
type ValidatorRewardInfo struct {
	ValidatorAddress      string            `json:"validator_address"`
	BaseReward            string            `json:"base_reward"`
	PerformanceBonus      string            `json:"performance_bonus"`
	ProposerBonus         string            `json:"proposer_bonus"`
	ConcentrationPenalty  string            `json:"concentration_penalty"`
	TotalValidatorReward  string            `json:"total_validator_reward"`
	Commission            string            `json:"commission"`
	DelegatorRewards      string            `json:"delegator_rewards"`
	DelegatorDistribution map[string]string `json:"delegator_distribution"`
	PerformanceScore      float64           `json:"performance_score"`
	StakeShare            float64           `json:"stake_share"`
}

// InflationProjection represents future inflation projection
type InflationProjection struct {
	Epoch         uint64  `json:"epoch"`
	Supply        string  `json:"supply"`
	InflationRate float64 `json:"inflation_rate"`
	StakingRatio  float64 `json:"staking_ratio"`
	RewardPool    string  `json:"reward_pool"`
}

// NewDistributor creates a new dynamic reward distributor
func NewDistributor(config *config.Config, worldState *state.WorldState) *Distributor {
	totalProposerRate := config.Economics.BaseProposerReward + config.Economics.BonusProposerReward

	distributor := &Distributor{
		config:                  config,
		worldState:              worldState,
		validatorRewardPool:     config.Economics.ValidatorRewardPool,
		currentInflationRate:    0.04,
		baseBlockReward:         config.Economics.BlockReward,
		inflationRate:           config.Economics.InflationRate,
		communityTaxRate:        config.Economics.CommunityTax,
		proposerBonusRate:       totalProposerRate,
		performanceMultiplier:   1.0,
		maxValidatorRewardShare: 0.05,
		concentrationPenalty:    0.1,
		rewardHistory:           make(map[string]*ValidatorRewardHistory),
		epochRewards:            make(map[uint64]*EpochRewardSummary),
		performanceScores:       make(map[string]float64),
		performanceWindow:       100,

		// Initialize with "0"
		totalRewardsDistributed: "0",
		totalTokensBurned:       "0",
	}

	distributor.inflationController = &DynamicInflationController{
		targetInflationRate:     0.04,
		targetStakingRatio:      0.67,
		minInflationRate:        0.01,
		maxInflationRate:        0.08,
		inflationAdjustmentRate: 0.1,
		epochsPerYear:           365,
	}

	return distributor
}

// DistributeEpochRewards distributes rewards using dynamic inflation
func (rd *Distributor) DistributeEpochRewards(epoch uint64) (*RewardDistributionResult, error) {
	rd.mu.Lock()
	defer rd.mu.Unlock()

	startTime := time.Now()
	rd.currentEpoch = epoch

	if err := rd.updateEconomicState(); err != nil {
		return nil, fmt.Errorf("failed to update economic state: %v", err)
	}

	rewardCalculation, err := rd.calculateDynamicRewards(epoch)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate dynamic rewards: %v", err)
	}

	activeValidators := rd.worldState.GetActiveValidators()
	if len(activeValidators) == 0 {
		return nil, fmt.Errorf("no active validators for epoch %d", epoch)
	}

	// Calculate Community Tax using coremath
	epochPoolBig := coremath.ParseBigInt(rewardCalculation.EpochRewardPool)
	communityTaxBig := mulBigIntFloat(epochPoolBig, rd.communityTaxRate) // Local helper for float rate
	availableRewardsBig := coremath.Sub(epochPoolBig, communityTaxBig)

	result := &RewardDistributionResult{
		Epoch:                   epoch,
		TotalRewardsDistributed: availableRewardsBig.String(),
		ValidatorRewards:        make(map[string]*ValidatorRewardInfo),
		CommunityTaxCollected:   communityTaxBig.String(),
		ParticipatingValidators: len(activeValidators),
	}

	rd.updatePerformanceScores(activeValidators)

	for _, validator := range activeValidators {
		rewardInfo, err := rd.distributeValidatorRewards(validator, availableRewardsBig.String(), rewardCalculation.TotalStaked, epoch)
		if err != nil {
			return nil, fmt.Errorf("failed to distribute rewards for validator %s: %v", validator.Address, err)
		}
		result.ValidatorRewards[validator.Address] = rewardInfo
	}

	burnAmount := rd.calculateBurnAmount(rewardCalculation)
	if burnAmount != "0" {
		rd.burnTokens(burnAmount)
	}

	result.DistributionTime = time.Since(startTime)

	// Calculate Average using coremath
	if len(result.ValidatorRewards) > 0 {
		totalDistributed := big.NewInt(0)
		for _, reward := range result.ValidatorRewards {
			totalDistributed = coremath.Add(totalDistributed, coremath.ParseBigInt(reward.TotalValidatorReward))
			totalDistributed = coremath.Add(totalDistributed, coremath.ParseBigInt(reward.DelegatorRewards))
		}

		count := big.NewInt(int64(len(result.ValidatorRewards)))
		// We use standard div here as it's not in the simple helper list provided,
		// but standard big.Int Div is safe for non-zero denominator.
		avg := new(big.Int).Div(totalDistributed, count)
		result.AverageRewardPerValidator = avg.String()
	}

	rd.recordEpochSummary(epoch, result, activeValidators, rewardCalculation.TotalStaked)

	// Update totals
	rd.totalRewardsDistributed = addBigIntStrings(rd.totalRewardsDistributed, result.TotalRewardsDistributed)
	if burnAmount != "0" {
		rd.totalTokensBurned = addBigIntStrings(rd.totalTokensBurned, burnAmount)
	}

	return result, nil
}

// updateEconomicState updates current economic metrics
func (rd *Distributor) updateEconomicState() error {
	// WorldState returns *big.Int
	supplyBI := rd.worldState.GetTotalSupply()
	stakedBI := rd.worldState.GetTotalStaked()

	// Store as string using standard String(), nil check handled by logic or state guarantee
	if supplyBI == nil {
		rd.totalSupply = "0"
	} else {
		rd.totalSupply = supplyBI.String()
	}

	if stakedBI == nil {
		rd.totalStaked = "0"
	} else {
		rd.totalStaked = stakedBI.String()
	}

	if supplyBI == nil || supplyBI.Sign() == 0 {
		return fmt.Errorf("total supply is zero")
	}

	// Calculate ratio using big.Float for precision
	supplyFloat := new(big.Float).SetInt(supplyBI)
	stakedFloat := new(big.Float).SetInt(stakedBI)

	ratioFloat := new(big.Float).Quo(stakedFloat, supplyFloat)
	rd.currentStakingRatio, _ = ratioFloat.Float64()

	rd.adjustInflationRate()

	return nil
}

func (rd *Distributor) adjustInflationRate() {
	ic := rd.inflationController
	stakingRatioDiff := rd.currentStakingRatio - ic.targetStakingRatio
	inflationAdjustment := -stakingRatioDiff * ic.inflationAdjustmentRate
	newInflationRate := rd.currentInflationRate + inflationAdjustment

	if newInflationRate < ic.minInflationRate {
		newInflationRate = ic.minInflationRate
	} else if newInflationRate > ic.maxInflationRate {
		newInflationRate = ic.maxInflationRate
	}

	rd.currentInflationRate = newInflationRate
}

func (rd *Distributor) calculateDynamicRewards(epoch uint64) (*DynamicRewardCalculation, error) {
	supplyBig := coremath.ParseBigInt(rd.totalSupply)

	// Annual Pool = Supply * InflationRate (float)
	annualRewardPoolBig := mulBigIntFloat(supplyBig, rd.currentInflationRate)

	// Epoch Pool = Annual / EpochsPerYear
	epochRewardPoolBig := new(big.Int).Div(annualRewardPoolBig, big.NewInt(rd.inflationController.epochsPerYear))

	// Apply staking multiplier
	stakingMultiplier := rd.calculateStakingMultiplier()
	epochRewardPoolBig = mulBigIntFloat(epochRewardPoolBig, stakingMultiplier)

	validatorShare, delegatorShare, communityShare := rd.distributeRewardShares(epochRewardPoolBig.String())

	return &DynamicRewardCalculation{
		Epoch:            epoch,
		TotalSupply:      rd.totalSupply,
		TotalStaked:      rd.totalStaked,
		StakingRatio:     rd.currentStakingRatio,
		InflationRate:    rd.currentInflationRate,
		AnnualRewardPool: annualRewardPoolBig.String(),
		EpochRewardPool:  epochRewardPoolBig.String(),
		ValidatorShare:   validatorShare,
		DelegatorShare:   delegatorShare,
		CommunityShare:   communityShare,
	}, nil
}

func (rd *Distributor) calculateStakingMultiplier() float64 {
	targetRatio := rd.inflationController.targetStakingRatio
	if rd.currentStakingRatio >= targetRatio {
		return 1.0
	} else if rd.currentStakingRatio >= targetRatio*0.8 {
		return 1.1
	} else if rd.currentStakingRatio >= targetRatio*0.6 {
		return 1.2
	} else {
		return 1.3
	}
}

func (rd *Distributor) distributeRewardShares(epochRewardPool string) (string, string, string) {
	poolBig := coremath.ParseBigInt(epochRewardPool)

	// Community Tax
	communityShareBig := mulBigIntFloat(poolBig, rd.communityTaxRate)

	// Staking Rewards = Pool - Tax
	stakingRewardsBig := coremath.Sub(poolBig, communityShareBig)

	// Validator (20%)
	validatorShareBig := mulBigIntFloat(stakingRewardsBig, 0.20)

	// Delegator = Staking - Validator
	delegatorShareBig := coremath.Sub(stakingRewardsBig, validatorShareBig)

	return validatorShareBig.String(), delegatorShareBig.String(), communityShareBig.String()
}

func (rd *Distributor) calculateBurnAmount(calc *DynamicRewardCalculation) string {
	targetRatio := rd.inflationController.targetStakingRatio
	epochPoolBig := coremath.ParseBigInt(calc.EpochRewardPool)

	var burnPercentage float64
	if rd.currentStakingRatio > targetRatio*1.25 {
		burnPercentage = 0.15
	} else if rd.currentStakingRatio > targetRatio*1.15 {
		burnPercentage = 0.10
	} else if rd.currentStakingRatio > targetRatio*1.05 {
		burnPercentage = 0.05
	} else {
		return "0"
	}

	return mulBigIntFloat(epochPoolBig, burnPercentage).String()
}

func (rd *Distributor) burnTokens(amount string) {
	rd.totalTokensBurned = addBigIntStrings(rd.totalTokensBurned, amount)
	fmt.Printf("Burned %s tokens due to over-staking\n", amount)
}

func (rd *Distributor) distributeValidatorRewards(validator *core.Validator, totalRewardPool string, totalStake string, epoch uint64) (*ValidatorRewardInfo, error) {
	poolBig := coremath.ParseBigInt(totalRewardPool)
	totalStakeBig := coremath.ParseBigInt(totalStake)
	validatorStakeBig := coremath.ParseBigInt(validator.Stake)

	// Validate inputs
	if totalStakeBig.Sign() <= 0 {
		return nil, fmt.Errorf("invalid total stake: %s", totalStake)
	}
	if poolBig.Sign() < 0 {
		return nil, fmt.Errorf("invalid reward pool: %s", totalRewardPool)
	}

	// Stake Share = ValStake / TotalStake
	valStakeF := new(big.Float).SetInt(validatorStakeBig)
	totalStakeF := new(big.Float).SetInt(totalStakeBig)
	stakeShareF := new(big.Float).Quo(valStakeF, totalStakeF)
	stakeShare, _ := stakeShareF.Float64()

	// Base Reward = Pool * Share
	baseRewardBig := mulBigIntFloat(poolBig, stakeShare)

	performanceScore := rd.performanceScores[validator.Address]
	if performanceScore == 0 {
		performanceScore = 1.0
	}

	// Performance Bonus
	performanceBonusBig := mulBigIntFloat(baseRewardBig, performanceScore-1.0)

	// Concentration Penalty
	concentrationPenaltyBig := big.NewInt(0)
	if stakeShare > rd.maxValidatorRewardShare {
		penaltyRate := (stakeShare - rd.maxValidatorRewardShare) * rd.concentrationPenalty
		concentrationPenaltyBig = mulBigIntFloat(baseRewardBig, penaltyRate)
	}

	// Proposer Bonus
	proposerBonusBig := rd.calculateProposerBonus(validator, epoch)

	// Total = Base + Perf + Prop - Conc (using SafeMath)
	totalValidatorRewardBig := coremath.AddBig(baseRewardBig, performanceBonusBig)
	totalValidatorRewardBig = coremath.AddBig(totalValidatorRewardBig, proposerBonusBig)
	totalValidatorRewardBig = coremath.SubBig(totalValidatorRewardBig, concentrationPenaltyBig)

	// Ensure non-negative reward
	if totalValidatorRewardBig.Sign() < 0 {
		totalValidatorRewardBig = big.NewInt(0)
	}

	// Commission
	commissionBig := mulBigIntFloat(totalValidatorRewardBig, validator.Commission)

	// Delegator = Total - Commission (using SafeMath)
	delegatorRewardBig := coremath.SubBig(totalValidatorRewardBig, commissionBig)

	// Add validator commission rewards
	if err := rd.worldState.GetAccountManager().AddRewards(validator.Address, commissionBig.Int64()); err != nil {
		return nil, fmt.Errorf("failed to add validator commission: %v", err)
	}

	// Distribute delegator rewards
	delegatorDistribution := make(map[string]string)
	if delegatorRewardBig.Sign() > 0 {
		var err error
		delegatorDistribution, err = rd.distributeDelegatorRewardsDetailed(validator, delegatorRewardBig)
		if err != nil {
			return nil, fmt.Errorf("failed to distribute delegator rewards: %v", err)
		}
	}

	// Record epoch rewards
	rd.recordValidatorEpochReward(validator.Address, epoch, &EpochRewards{
		Epoch:                epoch,
		BlockReward:          baseRewardBig.String(),
		Commission:           commissionBig.String(),
		DelegatorRewards:     delegatorRewardBig.String(),
		PerformanceBonus:     performanceBonusBig.String(),
		ConcentrationPenalty: concentrationPenaltyBig.String(),
		PerformanceScore:     performanceScore,
		TotalStake:           validator.Stake,
		Timestamp:            time.Now().Unix(),
	})

	return &ValidatorRewardInfo{
		ValidatorAddress:      validator.Address,
		BaseReward:            baseRewardBig.String(),
		PerformanceBonus:      performanceBonusBig.String(),
		ProposerBonus:         proposerBonusBig.String(),
		ConcentrationPenalty:  concentrationPenaltyBig.String(),
		TotalValidatorReward:  totalValidatorRewardBig.String(),
		Commission:            commissionBig.String(),
		DelegatorRewards:      delegatorRewardBig.String(),
		DelegatorDistribution: delegatorDistribution,
		PerformanceScore:      performanceScore,
		StakeShare:            stakeShare,
	}, nil
}

func (rd *Distributor) distributeDelegatorRewardsDetailed(validator *core.Validator, totalReward *big.Int) (map[string]string, error) {
	distribution := make(map[string]string)

	delegatedStakeBig := coremath.ParseBigInt(validator.DelegatedStake)
	if totalReward.Sign() <= 0 || delegatedStakeBig.Sign() == 0 {
		return distribution, nil
	}

	delegatedStakeF := new(big.Float).SetInt(delegatedStakeBig)

	for delegatorAddr, delegatedAmountStr := range validator.Delegators {
		delegatedAmountBig := coremath.ParseBigInt(delegatedAmountStr)
		delegatedAmountF := new(big.Float).SetInt(delegatedAmountBig)

		// Share = Amount / TotalDelegated
		shareF := new(big.Float).Quo(delegatedAmountF, delegatedStakeF)

		// Reward = TotalReward * Share
		totalRewardF := new(big.Float).SetInt(totalReward)
		delegatorRewardF := new(big.Float).Mul(totalRewardF, shareF)

		delegatorRewardBig, _ := delegatorRewardF.Int(nil)

		if delegatorRewardBig.Sign() > 0 {
			if err := rd.worldState.GetAccountManager().AddRewards(delegatorAddr, delegatorRewardBig.Int64()); err != nil {
				return nil, fmt.Errorf("failed to reward delegator %s: %v", delegatorAddr, err)
			}
			distribution[delegatorAddr] = delegatorRewardBig.String()
		}
	}

	return distribution, nil
}

func (rd *Distributor) updatePerformanceScores(validators []*core.Validator) {
	for _, validator := range validators {
		score := rd.calculatePerformanceScore(validator)
		rd.performanceScores[validator.Address] = score
	}
}

func (rd *Distributor) calculatePerformanceScore(validator *core.Validator) float64 {
	totalBlocks := validator.BlocksProposed + validator.BlocksMissed
	if totalBlocks == 0 {
		return 1.0
	}

	uptimeScore := float64(validator.BlocksProposed) / float64(totalBlocks)
	participationScore := 1.0
	if validator.BlocksMissed > validator.BlocksProposed {
		participationScore = 0.5
	}

	currentTime := time.Now().Unix()
	age := float64(currentTime - validator.CreatedAt)
	ageBonus := math.Min(age/(365*24*3600), 0.1)

	score := (uptimeScore * 0.6) + (participationScore * 0.3) + ageBonus
	if score < 0.1 {
		score = 0.1
	} else if score > 2.0 {
		score = 2.0
	}
	return score
}

func (rd *Distributor) calculateProposerBonus(validator *core.Validator, epoch uint64) *big.Int {
	if validator.BlocksProposed == 0 {
		return big.NewInt(0)
	}

	expectedBlocks := int64(32)
	actualBlocks := validator.BlocksProposed
	if actualBlocks > expectedBlocks {
		actualBlocks = expectedBlocks
	}

	bonusRate := float64(actualBlocks) / float64(expectedBlocks)
	baseRewardBig := coremath.ParseBigInt(rd.baseBlockReward)
	baseBonusBig := new(big.Int).Div(baseRewardBig, big.NewInt(10))

	return mulBigIntFloat(baseBonusBig, bonusRate)
}

func (rd *Distributor) recordValidatorEpochReward(validatorAddr string, epoch uint64, epochReward *EpochRewards) {
	if rd.rewardHistory[validatorAddr] == nil {
		rd.rewardHistory[validatorAddr] = &ValidatorRewardHistory{
			ValidatorAddress:   validatorAddr,
			EpochRewards:       make(map[uint64]*EpochRewards),
			TotalRewards:       "0",
			TotalCommission:    "0",
			RewardsDistributed: "0",
		}
	}

	history := rd.rewardHistory[validatorAddr]
	history.EpochRewards[epoch] = epochReward

	// Update totals using coremath
	tr := coremath.ParseBigInt(history.TotalRewards)
	tr = coremath.Add(tr, coremath.ParseBigInt(epochReward.BlockReward))
	tr = coremath.Add(tr, coremath.ParseBigInt(epochReward.PerformanceBonus))
	history.TotalRewards = tr.String()

	tc := coremath.ParseBigInt(history.TotalCommission)
	tc = coremath.Add(tc, coremath.ParseBigInt(epochReward.Commission))
	history.TotalCommission = tc.String()

	rdis := coremath.ParseBigInt(history.RewardsDistributed)
	rdis = coremath.Add(rdis, coremath.ParseBigInt(epochReward.DelegatorRewards))
	history.RewardsDistributed = rdis.String()

	history.LastRewardTime = epochReward.Timestamp

	perfEntry := PerformanceEntry{
		Timestamp:        epochReward.Timestamp,
		PerformanceScore: epochReward.PerformanceScore,
		BlocksProposed:   epochReward.BlocksProposed,
		AttestationsMade: epochReward.AttestationsMade,
	}
	history.PerformanceHistory = append(history.PerformanceHistory, perfEntry)

	if len(history.PerformanceHistory) > 100 {
		history.PerformanceHistory = history.PerformanceHistory[1:]
	}

	history.AverageAPY = rd.calculateValidatorAPY(history)
}

func (rd *Distributor) recordEpochSummary(epoch uint64, result *RewardDistributionResult, validators []*core.Validator, totalStake string) {
	totalPerformance := 0.0
	for _, validator := range validators {
		totalPerformance += rd.performanceScores[validator.Address]
	}
	avgPerformance := totalPerformance / float64(len(validators))

	topValidators := make([]ValidatorRewardEntry, 0, len(result.ValidatorRewards))
	for addr, reward := range result.ValidatorRewards {
		t := coremath.ParseBigInt(reward.TotalValidatorReward)
		t = coremath.Add(t, coremath.ParseBigInt(reward.DelegatorRewards))

		topValidators = append(topValidators, ValidatorRewardEntry{
			ValidatorAddress: addr,
			TotalReward:      t.String(),
			Commission:       reward.Commission,
			PerformanceScore: reward.PerformanceScore,
			StakeShare:       reward.StakeShare,
		})
	}

	sort.Slice(topValidators, func(i, j int) bool {
		bi := coremath.ParseBigInt(topValidators[i].TotalReward)
		bj := coremath.ParseBigInt(topValidators[j].TotalReward)
		return coremath.Cmp(bi, bj) > 0
	})

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

func (rd *Distributor) calculateValidatorAPY(history *ValidatorRewardHistory) float64 {
	if len(history.EpochRewards) == 0 {
		return 0.0
	}

	recentRewards := big.NewInt(0)
	recentStake := big.NewInt(0)
	count := 0

	for _, epochReward := range history.EpochRewards {
		if count >= 30 {
			break
		}
		// rewards = block + commission
		recentRewards = coremath.Add(recentRewards, coremath.ParseBigInt(epochReward.BlockReward))
		recentRewards = coremath.Add(recentRewards, coremath.ParseBigInt(epochReward.Commission))
		recentStake = coremath.Add(recentStake, coremath.ParseBigInt(epochReward.TotalStake))
		count++
	}

	if count == 0 || recentStake.Sign() == 0 {
		return 0.0
	}

	rF := new(big.Float).SetInt(recentRewards)
	sF := new(big.Float).SetInt(recentStake)
	cF := big.NewFloat(float64(count))

	avgRewardPerEpoch := new(big.Float).Quo(rF, cF)
	avgStakePerEpoch := new(big.Float).Quo(sF, cF)
	epochsPerYear := big.NewFloat(365.0)

	annualReward := new(big.Float).Mul(avgRewardPerEpoch, epochsPerYear)
	apyF := new(big.Float).Quo(annualReward, avgStakePerEpoch)
	apyF.Mul(apyF, big.NewFloat(100.0))

	apy, _ := apyF.Float64()
	return apy
}

func (rd *Distributor) GetEconomicMetrics() *EconomicMetrics {
	rd.mu.RLock()
	defer rd.mu.RUnlock()

	validatorAPY := rd.calculateValidatorAPY_Global()
	delegatorAPY := rd.calculateDelegatorAPY()
	health, recommendation := rd.assessEconomicHealth()

	supplyBig := coremath.ParseBigInt(rd.totalSupply)
	annualPool := mulBigIntFloat(supplyBig, rd.currentInflationRate)

	totalStakeBig := coremath.ParseBigInt(rd.totalStaked)
	circulating := coremath.Sub(supplyBig, totalStakeBig)

	totalBurnedBig := coremath.ParseBigInt(rd.totalTokensBurned)
	burnRate := 0.0
	if supplyBig.Sign() > 0 {
		bF := new(big.Float).SetInt(totalBurnedBig)
		sF := new(big.Float).SetInt(supplyBig)
		res := new(big.Float).Quo(bF, sF)
		burnRate, _ = res.Float64()
	}

	return &EconomicMetrics{
		TotalSupply:          rd.totalSupply,
		TotalStaked:          rd.totalStaked,
		CirculatingSupply:    circulating.String(),
		StakingRatio:         rd.currentStakingRatio,
		TargetStakingRatio:   rd.inflationController.targetStakingRatio,
		CurrentInflationRate: rd.currentInflationRate,
		TargetInflationRate:  rd.inflationController.targetInflationRate,
		AnnualRewardPool:     annualPool.String(),
		ValidatorAPY:         validatorAPY,
		DelegatorAPY:         delegatorAPY,
		TotalBurned:          rd.totalTokensBurned,
		BurnRate:             burnRate,
		EconomicHealth:       health,
		RecommendedAction:    recommendation,
	}
}

func (rd *Distributor) calculateValidatorAPY_Global() float64 {
	if rd.currentStakingRatio == 0 {
		return 0
	}
	baseAPY := rd.currentInflationRate / rd.currentStakingRatio
	validatorShare := 0.20
	avgCommission := 0.05
	return (baseAPY*validatorShare + baseAPY*0.80*avgCommission) * 100
}

func (rd *Distributor) calculateDelegatorAPY() float64 {
	if rd.currentStakingRatio == 0 {
		return 0
	}
	baseAPY := rd.currentInflationRate / rd.currentStakingRatio
	return (baseAPY * 0.80 * (1.0 - 0.05)) * 100
}

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
		}
		return "Poor", "Network is significantly imbalanced. Immediate action required."
	}
}

func (rd *Distributor) UpdateInflationParameters(targetInflation, targetStakingRatio, minInflation, maxInflation float64) error {
	rd.mu.Lock()
	defer rd.mu.Unlock()

	if targetInflation < 0.01 || targetInflation > 0.15 {
		return fmt.Errorf("target inflation must be between 1%% and 15%%")
	}
	if targetStakingRatio < 0.1 || targetStakingRatio > 0.9 {
		return fmt.Errorf("target staking ratio must be between 10%% and 90%%")
	}
	if minInflation >= maxInflation {
		return fmt.Errorf("min inflation must be less than max inflation")
	}

	ic := rd.inflationController
	ic.targetInflationRate = targetInflation
	ic.targetStakingRatio = targetStakingRatio
	ic.minInflationRate = minInflation
	ic.maxInflationRate = maxInflation
	return nil
}

func (rd *Distributor) GetInflationProjection(epochs int) []*InflationProjection {
	rd.mu.RLock()
	defer rd.mu.RUnlock()

	projections := make([]*InflationProjection, epochs)
	currentSupplyF, _ := new(big.Float).SetString(rd.totalSupply)
	currentSupply, _ := currentSupplyF.Float64()

	currentInflation := rd.currentInflationRate
	currentStaking := rd.currentStakingRatio

	for i := 0; i < epochs; i++ {
		stakingDiff := currentStaking - rd.inflationController.targetStakingRatio
		inflationAdjustment := -stakingDiff * rd.inflationController.inflationAdjustmentRate
		currentInflation = math.Max(rd.inflationController.minInflationRate,
			math.Min(rd.inflationController.maxInflationRate, currentInflation+inflationAdjustment))

		epochInflation := currentInflation / float64(rd.inflationController.epochsPerYear)
		newSupply := currentSupply * (1 + epochInflation)
		rewardPool := newSupply * currentInflation / float64(rd.inflationController.epochsPerYear)

		projections[i] = &InflationProjection{
			Epoch:         rd.currentEpoch + uint64(i+1),
			Supply:        fmt.Sprintf("%.0f", newSupply),
			InflationRate: currentInflation,
			StakingRatio:  currentStaking,
			RewardPool:    fmt.Sprintf("%.0f", rewardPool),
		}

		currentSupply = newSupply
		currentStaking += (rd.inflationController.targetStakingRatio - currentStaking) * 0.1
	}
	return projections
}

func (rd *Distributor) GetValidatorRewardHistory(validatorAddr string) (*ValidatorRewardHistory, error) {
	rd.mu.RLock()
	defer rd.mu.RUnlock()

	history, exists := rd.rewardHistory[validatorAddr]
	if !exists {
		return nil, fmt.Errorf("no reward history found for validator %s", validatorAddr)
	}
	historyCopy := *history
	return &historyCopy, nil
}

func (rd *Distributor) GetEpochRewardSummary(epoch uint64) (*EpochRewardSummary, error) {
	rd.mu.RLock()
	defer rd.mu.RUnlock()

	summary, exists := rd.epochRewards[epoch]
	if !exists {
		return nil, fmt.Errorf("no reward summary found for epoch %d", epoch)
	}
	summaryCopy := *summary
	return &summaryCopy, nil
}

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

func (rd *Distributor) UpdateInflationRate(newRate float64) error {
	rd.mu.Lock()
	defer rd.mu.Unlock()
	if newRate < 0 || newRate > 1 {
		return fmt.Errorf("inflation rate must be between 0 and 1")
	}
	rd.inflationRate = newRate
	return nil
}

func (rd *Distributor) UpdateCommunityTaxRate(newRate float64) error {
	rd.mu.Lock()
	defer rd.mu.Unlock()
	if newRate < 0 || newRate > 0.2 {
		return fmt.Errorf("community tax rate must be between 0 and 0.2")
	}
	rd.communityTaxRate = newRate
	return nil
}

func (rd *Distributor) GetCommunityPool() string {
	rd.mu.RLock()
	defer rd.mu.RUnlock()
	return rd.communityPool
}

func (rd *Distributor) WithdrawFromCommunityPool(amount string, recipient string) error {
	rd.mu.Lock()
	defer rd.mu.Unlock()

	amountBig := coremath.ParseBigInt(amount)
	poolBig := coremath.ParseBigInt(rd.communityPool)

	if amountBig.Sign() <= 0 {
		return fmt.Errorf("withdrawal amount must be positive")
	}
	if coremath.Cmp(poolBig, amountBig) < 0 {
		return fmt.Errorf("insufficient community pool balance")
	}

	// Assuming account manager accepts int64. Update if necessary.
	if err := rd.worldState.GetAccountManager().AddRewards(recipient, amountBig.Int64()); err != nil {
		return fmt.Errorf("failed to transfer community funds: %v", err)
	}

	poolBig = coremath.Sub(poolBig, amountBig)
	rd.communityPool = poolBig.String()
	return nil
}

func (rd *Distributor) CleanupOldRewardData(maxEpochsToKeep int) {
	rd.mu.Lock()
	defer rd.mu.Unlock()

	if len(rd.epochRewards) <= maxEpochsToKeep {
		return
	}

	var epochs []uint64
	for epoch := range rd.epochRewards {
		epochs = append(epochs, epoch)
	}
	sort.Slice(epochs, func(i, j int) bool { return epochs[i] < epochs[j] })

	for i := 0; i < len(epochs)-maxEpochsToKeep; i++ {
		delete(rd.epochRewards, epochs[i])
	}
	// (Simplified validator history cleanup omitted for brevity, logic identical to previous)
}

func (rd *Distributor) CalculateEstimatedAPY(stakeAmount string) float64 {
	rd.mu.RLock()
	defer rd.mu.RUnlock()

	activeValidators := rd.worldState.GetActiveValidators()
	totalStakeBig := rd.calculateTotalStake(activeValidators)
	if totalStakeBig.Sign() == 0 {
		return 0.0
	}

	stakeAmountBig := coremath.ParseBigInt(stakeAmount)
	annualRewardPoolBig := coremath.ParseBigInt(rd.validatorRewardPool)

	inflatedPoolBig := mulBigIntFloat(annualRewardPoolBig, 1.0+rd.inflationRate)
	netRewardBig := mulBigIntFloat(inflatedPoolBig, 1.0-rd.communityTaxRate)

	projectedTotalStake := coremath.Add(totalStakeBig, stakeAmountBig)

	sF := new(big.Float).SetInt(stakeAmountBig)
	pF := new(big.Float).SetInt(projectedTotalStake)
	stakeShareF := new(big.Float).Quo(sF, pF)

	nR_F := new(big.Float).SetInt(netRewardBig)
	estRewardF := new(big.Float).Mul(nR_F, stakeShareF)

	apyF := new(big.Float).Quo(estRewardF, sF)
	apyF.Mul(apyF, big.NewFloat(100.0))

	apy, _ := apyF.Float64()
	return apy * rd.calculateAveragePerformance()
}

func (rd *Distributor) calculateTotalStake(validators []*core.Validator) *big.Int {
	total := big.NewInt(0)
	for _, validator := range validators {
		total = coremath.Add(total, coremath.ParseBigInt(validator.Stake))
	}
	return total
}

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

// ---------------------------------------------------------
// Local Helpers (Extensions to core/math)
// ---------------------------------------------------------

// mulBigIntFloat multiplies a BigInt by a float64 (truncating)
// Kept local because core/math only supports integer percentages
func mulBigIntFloat(amount *big.Int, rate float64) *big.Int {
	if amount == nil {
		return big.NewInt(0)
	}
	amountFloat := new(big.Float).SetInt(amount)
	rateFloat := big.NewFloat(rate)
	resultFloat := new(big.Float).Mul(amountFloat, rateFloat)
	resultInt, _ := resultFloat.Int(nil)
	return resultInt
}

// addBigIntStrings adds two numeric strings
func addBigIntStrings(a, b string) string {
	biA := coremath.ParseBigInt(a)
	biB := coremath.ParseBigInt(b)
	return coremath.Add(biA, biB).String()
}
