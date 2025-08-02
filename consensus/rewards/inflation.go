// consensus/rewards/inflation.go

// Dynamic inflation system that maintains target staking ratio and economic stability
// Features:
// - Target inflation rate with automatic adjustments
// - Staking ratio-based reward scaling
// - Supply-responsive reward distribution
// - Economic sustainability mechanisms
// - Validator participation incentives

package rewards

import (
	"fmt"
	"math"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
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
	baseRewardPool    int64 // Base annual reward pool
	dynamicRewardPool int64 // Current dynamic reward pool

	// Epoch tracking
	currentEpoch  uint64
	epochsPerYear int64 // Number of epochs in a year
}

// InflationMetrics represents inflation and staking metrics
type InflationMetrics struct {
	// Current state
	CurrentInflationRate float64 `json:"current_inflation_rate"`
	CurrentStakingRatio  float64 `json:"current_staking_ratio"`
	TotalSupply          int64   `json:"total_supply"`
	TotalStaked          int64   `json:"total_staked"`
	CirculatingSupply    int64   `json:"circulating_supply"`

	// Targets
	TargetInflationRate float64 `json:"target_inflation_rate"`
	TargetStakingRatio  float64 `json:"target_staking_ratio"`

	// Rewards
	AnnualRewardPool    int64   `json:"annual_reward_pool"`
	CurrentEpochRewards int64   `json:"current_epoch_rewards"`
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
	TotalSupply      int64   `json:"total_supply"`
	TotalStaked      int64   `json:"total_staked"`
	StakingRatio     float64 `json:"staking_ratio"`
	InflationRate    float64 `json:"inflation_rate"`
	AnnualRewardPool int64   `json:"annual_reward_pool"`
	EpochRewardPool  int64   `json:"epoch_reward_pool"`
	ValidatorShare   int64   `json:"validator_share"`
	DelegatorShare   int64   `json:"delegator_share"`
	CommunityShare   int64   `json:"community_share"`
	BurnAmount       int64   `json:"burn_amount"`
}

// SupplyProjection represents future supply projection
type SupplyProjection struct {
	Epoch         uint64  `json:"epoch"`
	Supply        int64   `json:"supply"`
	InflationRate float64 `json:"inflation_rate"`
	Growth        float64 `json:"growth"`
}

// InflationScenario represents an inflation simulation scenario
type InflationScenario struct {
	Name                string    `json:"name"`
	InitialSupply       int64     `json:"initial_supply"`
	FinalSupply         int64     `json:"final_supply"`
	InflationRate       float64   `json:"inflation_rate"`
	Epochs              int       `json:"epochs"`
	TotalGrowth         float64   `json:"total_growth"`
	SupplyProjections   []int64   `json:"supply_projections"`
	CumulativeInflation []float64 `json:"cumulative_inflation"`
}

// NewInflationManager creates a new inflation manager
func NewInflationManager(config *config.Config, worldState *state.WorldState) *InflationManager {
	return &InflationManager{
		config:                  config,
		worldState:              worldState,
		targetInflationRate:     0.04, // 4% target inflation
		targetStakingRatio:      0.67, // 67% target staking ratio
		currentInflationRate:    0.04, // Start at target
		minInflationRate:        0.01, // 1% minimum
		maxInflationRate:        0.08, // 8% maximum
		inflationAdjustmentRate: 0.1,  // 10% adjustment per epoch
		stakingRewardMultiplier: 1.0,  // Base multiplier
		epochsPerYear:           365,  // Daily epochs
		baseRewardPool:          config.Economics.ValidatorRewardPool,
	}
}

// CalculateEpochRewards calculates rewards for the current epoch
func (im *InflationManager) CalculateEpochRewards(epoch uint64) (*RewardCalculation, error) {
	im.currentEpoch = epoch

	// Get current network state
	totalSupply := im.worldState.GetTotalSupply()
	totalStaked := im.worldState.GetTotalStaked()

	if totalSupply == 0 {
		return nil, fmt.Errorf("total supply is zero")
	}

	// Calculate current staking ratio
	im.currentStakingRatio = float64(totalStaked) / float64(totalSupply)

	// Adjust inflation rate based on staking ratio
	im.adjustInflationRate()

	// Calculate annual reward pool based on current inflation
	annualRewardPool := int64(float64(totalSupply) * im.currentInflationRate)

	// Calculate epoch reward pool (daily distribution)
	epochRewardPool := annualRewardPool / im.epochsPerYear

	// Apply staking ratio multiplier
	stakingMultiplier := im.calculateStakingMultiplier()
	epochRewardPool = int64(float64(epochRewardPool) * stakingMultiplier)

	// Distribute rewards
	validatorShare, delegatorShare, communityShare := im.distributeRewards(epochRewardPool)

	// Calculate burn amount (if any)
	burnAmount := im.calculateBurnAmount(epochRewardPool)

	calculation := &RewardCalculation{
		Epoch:            epoch,
		TotalSupply:      totalSupply,
		TotalStaked:      totalStaked,
		StakingRatio:     im.currentStakingRatio,
		InflationRate:    im.currentInflationRate,
		AnnualRewardPool: annualRewardPool,
		EpochRewardPool:  epochRewardPool,
		ValidatorShare:   validatorShare,
		DelegatorShare:   delegatorShare,
		CommunityShare:   communityShare,
		BurnAmount:       burnAmount,
	}

	// Update supply tracking
	im.updateSupplyTracking(totalSupply)

	return calculation, nil
}

// adjustInflationRate adjusts inflation based on staking ratio
func (im *InflationManager) adjustInflationRate() {
	stakingRatioDiff := im.currentStakingRatio - im.targetStakingRatio

	// Calculate desired inflation adjustment
	// If staking ratio is below target, increase inflation to incentivize staking
	// If staking ratio is above target, decrease inflation
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
	// Reward higher staking participation with better rewards
	if im.currentStakingRatio >= im.targetStakingRatio {
		// Above target: standard rewards
		return 1.0
	} else if im.currentStakingRatio >= im.targetStakingRatio*0.8 {
		// Moderately below target: slight bonus
		return 1.1
	} else if im.currentStakingRatio >= im.targetStakingRatio*0.6 {
		// Well below target: good bonus
		return 1.2
	} else {
		// Far below target: maximum bonus
		return 1.3
	}
}

// distributeRewards distributes epoch rewards among validators, delegators, and community
func (im *InflationManager) distributeRewards(epochRewardPool int64) (int64, int64, int64) {
	// Community tax (2%)
	communityShare := int64(float64(epochRewardPool) * 0.02)

	// Remaining for stakers
	stakingRewards := epochRewardPool - communityShare

	// Validator vs Delegator split (20% validators, 80% delegators)
	validatorShare := int64(float64(stakingRewards) * 0.20)
	delegatorShare := stakingRewards - validatorShare

	return validatorShare, delegatorShare, communityShare
}

// calculateBurnAmount calculates tokens to burn for deflationary pressure
func (im *InflationManager) calculateBurnAmount(epochRewardPool int64) int64 {
	// Burn mechanism: if staking ratio is very high, burn some rewards
	if im.currentStakingRatio > im.targetStakingRatio*1.2 {
		// Burn 10% of rewards if staking is 20% above target
		return int64(float64(epochRewardPool) * 0.1)
	}

	return 0 // No burning by default
}

// updateSupplyTracking updates supply growth tracking
func (im *InflationManager) updateSupplyTracking(currentSupply int64) {
	now := time.Now().Unix()

	if im.lastSupplyUpdate > 0 {
		// Calculate growth rate since last update
		timeDiff := float64(now - im.lastSupplyUpdate)
		if timeDiff > 0 {
			// Annualize the growth rate
			growthRate := float64(currentSupply) / float64(im.lastSupplyUpdate)
			annualizedGrowth := math.Pow(growthRate, 365*24*3600/timeDiff) - 1

			// Add to history
			im.supplyGrowthHistory = append(im.supplyGrowthHistory, annualizedGrowth)

			// Keep only last 30 data points
			if len(im.supplyGrowthHistory) > 30 {
				im.supplyGrowthHistory = im.supplyGrowthHistory[1:]
			}
		}
	}

	im.lastSupplyUpdate = now
}

// GetInflationMetrics returns current inflation and staking metrics
func (im *InflationManager) GetInflationMetrics() *InflationMetrics {
	totalSupply := im.worldState.GetTotalSupply()
	totalStaked := im.worldState.GetTotalStaked()

	// Calculate APYs
	validatorAPY := im.calculateValidatorAPY()
	delegatorAPY := im.calculateDelegatorAPY()

	// Calculate next epoch inflation prediction
	nextInflation := im.predictNextEpochInflation()

	// Calculate inflation adjustment
	stakingRatioDiff := im.currentStakingRatio - im.targetStakingRatio
	inflationAdjustment := -stakingRatioDiff * im.inflationAdjustmentRate

	return &InflationMetrics{
		CurrentInflationRate: im.currentInflationRate,
		CurrentStakingRatio:  im.currentStakingRatio,
		TotalSupply:          totalSupply,
		TotalStaked:          totalStaked,
		CirculatingSupply:    totalSupply - totalStaked, // Simplified
		TargetInflationRate:  im.targetInflationRate,
		TargetStakingRatio:   im.targetStakingRatio,
		AnnualRewardPool:     int64(float64(totalSupply) * im.currentInflationRate),
		CurrentEpochRewards:  int64(float64(totalSupply) * im.currentInflationRate / float64(im.epochsPerYear)),
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

	// Base APY from inflation, adjusted for staking ratio
	baseAPY := im.currentInflationRate / im.currentStakingRatio

	// Apply validator share (they get 20% of staking rewards + commissions)
	validatorAPY := baseAPY * 0.20

	// Add expected commission income (assume 5% average commission)
	commissionAPY := baseAPY * 0.80 * 0.05 // 5% of delegator rewards

	return (validatorAPY + commissionAPY) * 100 // Convert to percentage
}

// calculateDelegatorAPY calculates expected APY for delegators
func (im *InflationManager) calculateDelegatorAPY() float64 {
	if im.currentStakingRatio == 0 {
		return 0
	}

	// Base APY from inflation, adjusted for staking ratio
	baseAPY := im.currentInflationRate / im.currentStakingRatio

	// Delegators get 80% of staking rewards, minus average 5% commission
	delegatorAPY := baseAPY * 0.80 * 0.95 // After 5% commission

	return delegatorAPY * 100 // Convert to percentage
}

// predictNextEpochInflation predicts inflation for next epoch
func (im *InflationManager) predictNextEpochInflation() float64 {
	// Simulate next epoch adjustment
	stakingRatioDiff := im.currentStakingRatio - im.targetStakingRatio
	inflationAdjustment := -stakingRatioDiff * im.inflationAdjustmentRate

	nextInflation := im.currentInflationRate + inflationAdjustment

	// Apply bounds
	if nextInflation < im.minInflationRate {
		nextInflation = im.minInflationRate
	} else if nextInflation > im.maxInflationRate {
		nextInflation = im.maxInflationRate
	}

	return nextInflation
}

// SetTargetInflationRate updates the target inflation rate
func (im *InflationManager) SetTargetInflationRate(rate float64) error {
	if rate < 0 || rate > 0.15 { // Max 15% inflation
		return fmt.Errorf("target inflation rate must be between 0 and 0.15, got %f", rate)
	}

	im.targetInflationRate = rate
	return nil
}

// SetTargetStakingRatio updates the target staking ratio
func (im *InflationManager) SetTargetStakingRatio(ratio float64) error {
	if ratio < 0.1 || ratio > 0.9 { // Between 10% and 90%
		return fmt.Errorf("target staking ratio must be between 0.1 and 0.9, got %f", ratio)
	}

	im.targetStakingRatio = ratio
	return nil
}

// GetSupplyProjection calculates supply projection for future epochs
func (im *InflationManager) GetSupplyProjection(epochs int) []SupplyProjection {
	projections := make([]SupplyProjection, epochs)

	currentSupply := float64(im.worldState.GetTotalSupply())
	currentInflation := im.currentInflationRate

	for i := 0; i < epochs; i++ {
		// Project supply growth
		epochInflation := currentInflation / float64(im.epochsPerYear)
		newSupply := currentSupply * (1 + epochInflation)

		projections[i] = SupplyProjection{
			Epoch:         im.currentEpoch + uint64(i+1),
			Supply:        int64(newSupply),
			InflationRate: currentInflation,
			Growth:        newSupply - currentSupply,
		}

		currentSupply = newSupply

		// Adjust inflation for next epoch (simplified)
		if i < epochs-1 {
			currentInflation += 0.001 // Small adjustment
			if currentInflation > im.maxInflationRate {
				currentInflation = im.maxInflationRate
			}
		}
	}

	return projections
}

// GetInflationHistory returns historical inflation data
func (im *InflationManager) GetInflationHistory() []float64 {
	// Return copy of supply growth history
	history := make([]float64, len(im.supplyGrowthHistory))
	copy(history, im.supplyGrowthHistory)
	return history
}

// SimulateInflationScenarios simulates different inflation scenarios
func (im *InflationManager) SimulateInflationScenarios() map[string]*InflationScenario {
	scenarios := make(map[string]*InflationScenario)

	baseSupply := float64(im.worldState.GetTotalSupply())

	// Conservative scenario (2% inflation)
	scenarios["conservative"] = im.simulateScenario(baseSupply, 0.02, 365)

	// Target scenario (4% inflation)
	scenarios["target"] = im.simulateScenario(baseSupply, 0.04, 365)

	// Aggressive scenario (6% inflation)
	scenarios["aggressive"] = im.simulateScenario(baseSupply, 0.06, 365)

	return scenarios
}

// simulateScenario simulates inflation scenario
func (im *InflationManager) simulateScenario(initialSupply float64, inflationRate float64, epochs int) *InflationScenario {
	scenario := &InflationScenario{
		Name:                "Scenario",
		InitialSupply:       int64(initialSupply),
		InflationRate:       inflationRate,
		Epochs:              epochs,
		SupplyProjections:   make([]int64, epochs),
		CumulativeInflation: make([]float64, epochs),
	}

	currentSupply := initialSupply

	for i := 0; i < epochs; i++ {
		epochInflation := inflationRate / float64(im.epochsPerYear)
		currentSupply *= (1 + epochInflation)

		scenario.SupplyProjections[i] = int64(currentSupply)
		scenario.CumulativeInflation[i] = (currentSupply - initialSupply) / initialSupply
	}

	scenario.FinalSupply = int64(currentSupply)
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

	// Adjust current rate if it's outside new bounds
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
		return fmt.Errorf("inflation adjustment rate must be between 1%% and 100%%, got %.2f%%", rate*100)
	}

	im.inflationAdjustmentRate = rate
	return nil
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
	if epochs <= 0 || epochs > 365*24 { // Max hourly epochs
		return fmt.Errorf("epochs per year must be between 1 and %d, got %d", 365*24, epochs)
	}

	im.epochsPerYear = epochs
	return nil
}
