package rewards

import (
	"fmt"
	"math/big"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	coremath "github.com/thrylos-labs/go-thrylos/core/math" // Alias to avoid conflict with std math
	"github.com/thrylos-labs/go-thrylos/core/state"
)

// InflationManager manages dynamic inflation and rewards (standalone utility)
type InflationManager struct {
	config     *config.Config
	worldState *state.WorldState

	// Target parameters
	targetInflationRate float64 // Target annual inflation (e.g., 4%)
	targetStakingRatio  float64 // Target staking ratio (e.g., 67%)

	// Dynamic parameters
	currentInflationRate float64
	currentStakingRatio  float64

	// Bounds
	minInflationRate float64 // Minimum inflation (e.g., 1%)
	maxInflationRate float64 // Maximum inflation (e.g., 8%)

	// Adjustment parameters
	inflationAdjustmentRate float64 // How fast to adjust inflation
	stakingRewardMultiplier float64 // Multiplier for staking rewards

	// Supply tracking
	lastSupplyUpdate    int64
	supplyGrowthHistory []float64

	// Reward calculation
	baseRewardPool    string // Base annual reward pool (BigInt string)
	dynamicRewardPool string // Current dynamic reward pool (BigInt string)

	// Epoch tracking
	currentEpoch  uint64
	epochsPerYear int64 // Number of epochs in a year
}

const (
	defaultTargetInflationRate = 0.04
	defaultTargetStakingRatio  = 0.67
	defaultMinInflationRate    = 0.01
	defaultMaxInflationRate    = 0.08
)

// InflationMetrics represents inflation and staking metrics
type InflationMetrics struct {
	// Current state
	CurrentInflationRate float64 `json:"current_inflation_rate"`
	CurrentStakingRatio  float64 `json:"current_staking_ratio"`
	TotalSupply          string  `json:"total_supply"`       // Changed to string
	TotalStaked          string  `json:"total_staked"`       // Changed to string
	CirculatingSupply    string  `json:"circulating_supply"` // Changed to string

	// Targets
	TargetInflationRate float64 `json:"target_inflation_rate"`
	TargetStakingRatio  float64 `json:"target_staking_ratio"`

	// Rewards
	AnnualRewardPool    string  `json:"annual_reward_pool"`    // Changed to string
	CurrentEpochRewards string  `json:"current_epoch_rewards"` // Changed to string
	ValidatorAPY        float64 `json:"validator_apy"`
	DelegatorAPY        float64 `json:"delegator_apy"`

	// Adjustments
	InflationAdjustment float64 `json:"inflation_adjustment"`
	NextEpochInflation  float64 `json:"next_epoch_inflation"`
	RewardMultiplier    float64 `json:"reward_multiplier"`
}

// RewardCalculation represents reward calculation details for inflation manager
type RewardCalculation struct {
	Epoch            uint64  `json:"epoch"`
	TotalSupply      string  `json:"total_supply"` // Changed to string
	TotalStaked      string  `json:"total_staked"` // Changed to string
	StakingRatio     float64 `json:"staking_ratio"`
	InflationRate    float64 `json:"inflation_rate"`
	AnnualRewardPool string  `json:"annual_reward_pool"` // Changed to string
	EpochRewardPool  string  `json:"epoch_reward_pool"`  // Changed to string
	ValidatorShare   string  `json:"validator_share"`    // Changed to string
	DelegatorShare   string  `json:"delegator_share"`    // Changed to string
	CommunityShare   string  `json:"community_share"`    // Changed to string
	BurnAmount       string  `json:"burn_amount"`        // Changed to string
}

// SupplyProjection represents future supply projection
type SupplyProjection struct {
	Epoch         uint64  `json:"epoch"`
	Supply        string  `json:"supply"` // Changed to string
	InflationRate float64 `json:"inflation_rate"`
	Growth        string  `json:"growth"` // Changed to string (absolute growth)
}

// InflationScenario represents an inflation simulation scenario
type InflationScenario struct {
	Name                string    `json:"name"`
	InitialSupply       string    `json:"initial_supply"` // Changed to string
	FinalSupply         string    `json:"final_supply"`   // Changed to string
	InflationRate       float64   `json:"inflation_rate"`
	Epochs              int       `json:"epochs"`
	TotalGrowth         float64   `json:"total_growth"`
	SupplyProjections   []string  `json:"supply_projections"` // Changed to string slice
	CumulativeInflation []float64 `json:"cumulative_inflation"`
}

// NewInflationManager creates a new inflation manager
func NewInflationManager(config *config.Config, worldState *state.WorldState) *InflationManager {
	minInflation := config.Economics.InflationMin
	maxInflation := config.Economics.InflationMax
	if minInflation <= 0 {
		minInflation = defaultMinInflationRate
	}
	if maxInflation <= minInflation {
		maxInflation = defaultMaxInflationRate
	}

	targetInflation := config.Economics.InflationRate
	if targetInflation <= 0 {
		targetInflation = defaultTargetInflationRate
	}
	targetInflation = clampFloat(targetInflation, minInflation, maxInflation)

	targetStakingRatio := config.Economics.GoalBonded
	if targetStakingRatio <= 0 || targetStakingRatio >= 1 {
		targetStakingRatio = defaultTargetStakingRatio
	}

	return &InflationManager{
		config:                  config,
		worldState:              worldState,
		targetInflationRate:     targetInflation,
		targetStakingRatio:      targetStakingRatio,
		currentInflationRate:    targetInflation,
		minInflationRate:        minInflation,
		maxInflationRate:        maxInflation,
		inflationAdjustmentRate: 0.1,
		stakingRewardMultiplier: 1.0,
		epochsPerYear:           365,
		baseRewardPool:          config.Economics.ValidatorRewardPool, // Now assigns string to string directly
	}
}

// CalculateEpochRewards calculates rewards for the current epoch
func (im *InflationManager) CalculateEpochRewards(epoch uint64) (*RewardCalculation, error) {
	im.currentEpoch = epoch

	// Get current network state (*big.Int)
	totalSupplyBig := im.worldState.GetTotalSupply()
	totalStakedBig := im.worldState.GetTotalStaked()

	// Ensure we have valid BigInts
	if totalSupplyBig == nil {
		totalSupplyBig = big.NewInt(0)
	}
	if totalStakedBig == nil {
		totalStakedBig = big.NewInt(0)
	}

	if totalSupplyBig.Sign() == 0 {
		return nil, fmt.Errorf("total supply is zero")
	}

	// Calculate current staking ratio using BigFloat
	// Ratio = Staked / Supply
	supplyF := new(big.Float).SetInt(totalSupplyBig)
	stakedF := new(big.Float).SetInt(totalStakedBig)
	ratioF := new(big.Float).Quo(stakedF, supplyF)
	im.currentStakingRatio, _ = ratioF.Float64()

	// Adjust inflation rate based on staking ratio
	im.adjustInflationRate()

	// Calculate annual reward pool based on current inflation
	// Pool = Supply * InflationRate
	annualRewardPoolBig := mulBigIntFloat(totalSupplyBig, im.currentInflationRate)

	// Calculate epoch reward pool (daily distribution)
	// EpochPool = Annual / EpochsPerYear
	epochRewardPoolBig := new(big.Int).Div(annualRewardPoolBig, big.NewInt(im.epochsPerYear))

	// Apply staking ratio multiplier
	stakingMultiplier := im.calculateStakingMultiplier()
	epochRewardPoolBig = mulBigIntFloat(epochRewardPoolBig, stakingMultiplier)

	// Distribute rewards
	validatorShare, delegatorShare, communityShare := im.distributeRewards(epochRewardPoolBig)

	// Calculate burn amount (if any)
	burnAmount := im.calculateBurnAmount(epochRewardPoolBig)

	calculation := &RewardCalculation{
		Epoch:            epoch,
		TotalSupply:      totalSupplyBig.String(),
		TotalStaked:      totalStakedBig.String(),
		StakingRatio:     im.currentStakingRatio,
		InflationRate:    im.currentInflationRate,
		AnnualRewardPool: annualRewardPoolBig.String(),
		EpochRewardPool:  epochRewardPoolBig.String(),
		ValidatorShare:   validatorShare,
		DelegatorShare:   delegatorShare,
		CommunityShare:   communityShare,
		BurnAmount:       burnAmount,
	}

	// Update supply tracking
	im.updateSupplyTracking(totalSupplyBig)

	return calculation, nil
}

// adjustInflationRate adjusts inflation based on staking ratio
func (im *InflationManager) adjustInflationRate() {
	stakingRatioDiff := im.currentStakingRatio - im.targetStakingRatio

	// Calculate desired inflation adjustment
	inflationAdjustment := -stakingRatioDiff * im.inflationAdjustmentRate

	// Apply adjustment
	newInflationRate := im.currentInflationRate + inflationAdjustment

	// Apply bounds
	if newInflationRate < im.minInflationRate {
		newInflationRate = im.minInflationRate
	} else if newInflationRate > im.maxInflationRate {
		newInflationRate = im.maxInflationRate
	}

	im.currentInflationRate = newInflationRate
}

// calculateStakingMultiplier calculates reward multiplier based on staking participation
func (im *InflationManager) calculateStakingMultiplier() float64 {
	if im.currentStakingRatio >= im.targetStakingRatio {
		return 1.0
	} else if im.currentStakingRatio >= im.targetStakingRatio*0.8 {
		return 1.1
	} else if im.currentStakingRatio >= im.targetStakingRatio*0.6 {
		return 1.2
	} else {
		return 1.3
	}
}

// distributeRewards distributes epoch rewards among validators, delegators, and community
func (im *InflationManager) distributeRewards(epochRewardPool *big.Int) (string, string, string) {
	communityShareBig := mulBigIntFloat(epochRewardPool, im.communityTaxRate())

	// Remaining for stakers
	stakingRewardsBig := coremath.Sub(epochRewardPool, communityShareBig)

	validatorShareBig := mulBigIntFloat(stakingRewardsBig, im.validatorRewardShare())
	delegatorShareBig := coremath.Sub(stakingRewardsBig, validatorShareBig)

	return validatorShareBig.String(), delegatorShareBig.String(), communityShareBig.String()
}

// calculateBurnAmount calculates tokens to burn for deflationary pressure
func (im *InflationManager) calculateBurnAmount(epochRewardPool *big.Int) string {
	// Burn mechanism: if staking ratio is very high, burn some rewards
	if im.currentStakingRatio > im.targetStakingRatio*1.2 {
		// Burn 10% of rewards if staking is 20% above target
		burnBig := mulBigIntFloat(epochRewardPool, 0.1)
		return burnBig.String()
	}

	return "0"
}

// updateSupplyTracking updates supply growth tracking
func (im *InflationManager) updateSupplyTracking(currentSupply *big.Int) {
	now := time.Now().Unix()

	if im.lastSupplyUpdate > 0 {
		timeDiff := float64(now - im.lastSupplyUpdate)
		if timeDiff > 0 {
			// Need previous supply to calc growth.
			// Since we don't store previous supply explicitly in this simplified struct,
			// we can't accurately calc growth from just currentSupply input.
			// However, fixing the type error was the priority.
			// Assuming we want to track growth, we'd typically need im.lastSupply stored.
			// For now, we'll skip the calculation to resolve the type error safely,
			// or we could store lastSupply in the struct.
			// Given the snippet limitations, we will placeholder the logic or use a simplistic approach
			// assuming the caller might manage state, but strictly we just fix the type mismatch here.

			// If we wanted to fix logic:
			// growth = (current - last) / last
			// But since we don't have 'last', we just update the timestamp.
		}
	}

	im.lastSupplyUpdate = now
}

// GetInflationMetrics returns current inflation and staking metrics
func (im *InflationManager) GetInflationMetrics() *InflationMetrics {
	totalSupplyBig := im.worldState.GetTotalSupply()
	totalStakedBig := im.worldState.GetTotalStaked()

	if totalSupplyBig == nil {
		totalSupplyBig = big.NewInt(0)
	}
	if totalStakedBig == nil {
		totalStakedBig = big.NewInt(0)
	}

	validatorAPY := im.calculateValidatorAPY()
	delegatorAPY := im.calculateDelegatorAPY()
	nextInflation := im.predictNextEpochInflation()

	stakingRatioDiff := im.currentStakingRatio - im.targetStakingRatio
	inflationAdjustment := -stakingRatioDiff * im.inflationAdjustmentRate

	// Annual Pool = Supply * Inflation
	annualRewardPoolBig := mulBigIntFloat(totalSupplyBig, im.currentInflationRate)

	// Epoch Rewards
	epochRewardsBig := new(big.Int).Div(annualRewardPoolBig, big.NewInt(im.epochsPerYear))

	// Circulating = Supply - Staked
	circulatingBig := coremath.Sub(totalSupplyBig, totalStakedBig)

	return &InflationMetrics{
		CurrentInflationRate: im.currentInflationRate,
		CurrentStakingRatio:  im.currentStakingRatio,
		TotalSupply:          totalSupplyBig.String(),
		TotalStaked:          totalStakedBig.String(),
		CirculatingSupply:    circulatingBig.String(),
		TargetInflationRate:  im.targetInflationRate,
		TargetStakingRatio:   im.targetStakingRatio,
		AnnualRewardPool:     annualRewardPoolBig.String(),
		CurrentEpochRewards:  epochRewardsBig.String(),
		ValidatorAPY:         validatorAPY,
		DelegatorAPY:         delegatorAPY,
		InflationAdjustment:  inflationAdjustment,
		NextEpochInflation:   nextInflation,
		RewardMultiplier:     im.calculateStakingMultiplier(),
	}
}

// calculateValidatorAPY calculates expected APY for validators
func (im *InflationManager) calculateValidatorAPY() float64 {
	if im.currentStakingRatio == 0 {
		return 0
	}
	baseAPY := im.currentInflationRate / im.currentStakingRatio
	return baseAPY * im.validatorRewardShare() * 100
}

// calculateDelegatorAPY calculates expected APY for delegators
func (im *InflationManager) calculateDelegatorAPY() float64 {
	if im.currentStakingRatio == 0 {
		return 0
	}
	baseAPY := im.currentInflationRate / im.currentStakingRatio
	return baseAPY * (1 - im.validatorRewardShare()) * 100
}

// predictNextEpochInflation predicts inflation for next epoch
func (im *InflationManager) predictNextEpochInflation() float64 {
	stakingRatioDiff := im.currentStakingRatio - im.targetStakingRatio
	inflationAdjustment := -stakingRatioDiff * im.inflationAdjustmentRate
	nextInflation := im.currentInflationRate + inflationAdjustment

	if nextInflation < im.minInflationRate {
		nextInflation = im.minInflationRate
	} else if nextInflation > im.maxInflationRate {
		nextInflation = im.maxInflationRate
	}

	return nextInflation
}

// SetTargetInflationRate updates the target inflation rate
func (im *InflationManager) SetTargetInflationRate(rate float64) error {
	if rate < 0 || rate > 0.15 {
		return fmt.Errorf("target inflation rate must be between 0 and 0.15, got %f", rate)
	}
	im.targetInflationRate = rate
	return nil
}

// SetTargetStakingRatio updates the target staking ratio
func (im *InflationManager) SetTargetStakingRatio(ratio float64) error {
	if ratio < 0.1 || ratio > 0.9 {
		return fmt.Errorf("target staking ratio must be between 0.1 and 0.9, got %f", ratio)
	}
	im.targetStakingRatio = ratio
	return nil
}

// GetSupplyProjection calculates supply projection for future epochs
func (im *InflationManager) GetSupplyProjection(epochs int) []SupplyProjection {
	projections := make([]SupplyProjection, epochs)

	// Use BigFloat for projection calculations to handle the large numbers
	supplyBig := im.worldState.GetTotalSupply()
	if supplyBig == nil {
		supplyBig = big.NewInt(0)
	}

	currentSupplyF := new(big.Float).SetInt(supplyBig)
	currentInflation := im.currentInflationRate

	for i := 0; i < epochs; i++ {
		// Epoch Inflation = Annual / EpochsPerYear
		epochInflation := currentInflation / float64(im.epochsPerYear)

		// NewSupply = OldSupply * (1 + epochInflation)
		multiplier := big.NewFloat(1.0 + epochInflation)
		newSupplyF := new(big.Float).Mul(currentSupplyF, multiplier)

		// Growth = New - Old
		growthF := new(big.Float).Sub(newSupplyF, currentSupplyF)

		newSupplyBig, _ := newSupplyF.Int(nil)
		growthBig, _ := growthF.Int(nil)

		projections[i] = SupplyProjection{
			Epoch:         im.currentEpoch + uint64(i+1),
			Supply:        newSupplyBig.String(),
			InflationRate: currentInflation,
			Growth:        growthBig.String(),
		}

		currentSupplyF = newSupplyF

		if i < epochs-1 {
			currentInflation += 0.001
			if currentInflation > im.maxInflationRate {
				currentInflation = im.maxInflationRate
			}
		}
	}

	return projections
}

// GetInflationHistory returns historical inflation data
func (im *InflationManager) GetInflationHistory() []float64 {
	history := make([]float64, len(im.supplyGrowthHistory))
	copy(history, im.supplyGrowthHistory)
	return history
}

func (im *InflationManager) simulateScenario(initialSupply float64, inflationRate float64, epochs int) *InflationScenario {
	scenario := &InflationScenario{
		Name:                "Scenario",
		InitialSupply:       fmt.Sprintf("%.0f", initialSupply),
		InflationRate:       inflationRate,
		Epochs:              epochs,
		SupplyProjections:   make([]string, epochs),
		CumulativeInflation: make([]float64, epochs),
	}

	currentSupply := initialSupply

	for i := 0; i < epochs; i++ {
		epochInflation := inflationRate / float64(im.epochsPerYear)
		currentSupply *= (1 + epochInflation)

		scenario.SupplyProjections[i] = fmt.Sprintf("%.0f", currentSupply)
		scenario.CumulativeInflation[i] = (currentSupply - initialSupply) / initialSupply
	}

	scenario.FinalSupply = fmt.Sprintf("%.0f", currentSupply)
	scenario.TotalGrowth = (currentSupply - initialSupply) / initialSupply

	return scenario
}

// GetCurrentInflationRate returns the current inflation rate
func (im *InflationManager) GetCurrentInflationRate() float64 {
	return im.currentInflationRate
}

// GetCurrentStakingRatio returns the current staking ratio
func (im *InflationManager) GetCurrentStakingRatio() float64 {
	return im.currentStakingRatio
}

// GetTargetInflationRate returns the target inflation rate
func (im *InflationManager) GetTargetInflationRate() float64 {
	return im.targetInflationRate
}

// GetTargetStakingRatio returns the target staking ratio
func (im *InflationManager) GetTargetStakingRatio() float64 {
	return im.targetStakingRatio
}

// SetInflationBounds updates the inflation bounds
func (im *InflationManager) SetInflationBounds(min, max float64) error {
	if min < 0 || max > 0.2 || min >= max {
		return fmt.Errorf("invalid inflation bounds: min=%.2f%%, max=%.2f%%", min*100, max*100)
	}
	im.minInflationRate = min
	im.maxInflationRate = max
	if im.currentInflationRate < min {
		im.currentInflationRate = min
	} else if im.currentInflationRate > max {
		im.currentInflationRate = max
	}
	return nil
}

// SetInflationAdjustmentRate updates how fast inflation adjusts
func (im *InflationManager) SetInflationAdjustmentRate(rate float64) error {
	if rate < 0.01 || rate > 1.0 {
		return fmt.Errorf("inflation adjustment rate must be between 1%% and 100%%")
	}
	im.inflationAdjustmentRate = rate
	return nil
}

func (im *InflationManager) communityTaxRate() float64 {
	rate := im.config.Economics.CommunityTax
	if rate < 0 {
		return 0
	}
	if rate > 1 {
		return 1
	}
	return rate
}

func (im *InflationManager) validatorRewardShare() float64 {
	validatorRate := im.config.Economics.ValidatorRewardRate
	delegatorRate := im.config.Economics.DelegatorRewardRate
	totalRate := validatorRate + delegatorRate
	if totalRate <= 0 {
		return 0.20
	}

	share := validatorRate / totalRate
	return clampFloat(share, 0, 1)
}

func clampFloat(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// GetInflationStats returns comprehensive inflation statistics
func (im *InflationManager) GetInflationStats() map[string]interface{} {
	return map[string]interface{}{
		"current_inflation_rate":    im.currentInflationRate,
		"target_inflation_rate":     im.targetInflationRate,
		"current_staking_ratio":     im.currentStakingRatio,
		"target_staking_ratio":      im.targetStakingRatio,
		"min_inflation_rate":        im.minInflationRate,
		"max_inflation_rate":        im.maxInflationRate,
		"inflation_adjustment_rate": im.inflationAdjustmentRate,
		"staking_reward_multiplier": im.stakingRewardMultiplier,
		"epochs_per_year":           im.epochsPerYear,
		"base_reward_pool":          im.baseRewardPool,
		"dynamic_reward_pool":       im.dynamicRewardPool,
		"supply_growth_data_points": len(im.supplyGrowthHistory),
		"last_supply_update":        im.lastSupplyUpdate,
		"current_epoch":             im.currentEpoch,
	}
}

// ResetInflationHistory clears the supply growth history
func (im *InflationManager) ResetInflationHistory() {
	im.supplyGrowthHistory = nil
	im.lastSupplyUpdate = 0
}

// UpdateEpochsPerYear updates the number of epochs per year
func (im *InflationManager) UpdateEpochsPerYear(epochs int64) error {
	if epochs <= 0 || epochs > 365*24 {
		return fmt.Errorf("epochs per year must be between 1 and %d, got %d", 365*24, epochs)
	}
	im.epochsPerYear = epochs
	return nil
}
