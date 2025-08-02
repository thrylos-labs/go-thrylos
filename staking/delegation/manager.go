// // staking/delegation/manager.go

// // Delegation management for liquid staking with advanced features
// // Features:
// // - Automated delegation distribution across validators
// // - Performance-based validator selection and rebalancing
// // - Risk management and slashing protection
// // - Commission optimization and fee management
// // - Unbonding queue management with liquid withdrawals
// // - MEV (Maximal Extractable Value) protection and sharing

package delegation

// import (
// 	"fmt"
// 	"math"
// 	"sort"
// 	"sync"
// 	"time"

// 	"github.com/thrylos-labs/go-thrylos/config"
// 	"github.com/thrylos-labs/go-thrylos/consensus/validator"
// 	"github.com/thrylos-labs/go-thrylos/core/state"
// )

// // Manager handles delegation strategies and validator selection
// type Manager struct {
// 	config       *config.Config
// 	worldState   *state.WorldState
// 	validatorMgr *validator.Manager

// 	// Delegation strategy
// 	strategy DelegationStrategy

// 	// Validator scoring and selection
// 	validatorScorer *ValidatorScorer
// 	rebalancer      *PortfolioRebalancer

// 	// Risk management
// 	riskManager     *RiskManager
// 	slashingTracker *SlashingTracker

// 	// Delegation tracking
// 	activeDelegations    map[string]*DelegationInfo
// 	unbondingQueue       *UnbondingQueue
// 	pendingRedelegations map[string]*RedelegationInfo

// 	// Performance metrics
// 	totalDelegated     int64
// 	totalRewardsEarned int64
// 	averageAPY         float64
// 	portfolioRisk      float64
// 	lastRebalanceTime  int64

// 	// Synchronization
// 	mu sync.RWMutex

// 	// Configuration
// 	maxValidatorsPerPool int     // Maximum validators to delegate to
// 	minDelegationAmount  int64   // Minimum delegation per validator
// 	rebalanceThreshold   float64 // Trigger rebalancing when drift exceeds this
// 	riskTolerance        float64 // Risk tolerance level (0-1)
// 	performanceWindow    int64   // Performance evaluation window in blocks
// }

// // DelegationStrategy defines how delegations are distributed
// type DelegationStrategy string

// const (
// 	StrategyEqualWeight      DelegationStrategy = "equal_weight"   // Equal distribution
// 	StrategyPerformanceBased DelegationStrategy = "performance"    // Based on validator performance
// 	StrategyRiskOptimized    DelegationStrategy = "risk_optimized" // Optimize risk/reward ratio
// 	StrategyYieldMaximizing  DelegationStrategy = "yield_max"      // Maximum yield strategy
// 	StrategyConservative     DelegationStrategy = "conservative"   // Low-risk, stable returns
// )

// // DelegationInfo tracks individual delegations
// type DelegationInfo struct {
// 	ValidatorAddress   string  `json:"validator_address"`
// 	Amount             int64   `json:"amount"`
// 	Timestamp          int64   `json:"timestamp"`
// 	AccumulatedRewards int64   `json:"accumulated_rewards"`
// 	Commission         float64 `json:"commission"`
// 	Performance        float64 `json:"performance"`
// 	RiskScore          float64 `json:"risk_score"`
// 	LastRewardClaim    int64   `json:"last_reward_claim"`
// 	IsActive           bool    `json:"is_active"`
// }

// // RedelegationInfo tracks pending redelegations
// type RedelegationInfo struct {
// 	FromValidator  string `json:"from_validator"`
// 	ToValidator    string `json:"to_validator"`
// 	Amount         int64  `json:"amount"`
// 	CompletionTime int64  `json:"completion_time"`
// 	Reason         string `json:"reason"`
// }

// // ValidatorScore represents a validator's overall score for delegation
// type ValidatorScore struct {
// 	ValidatorAddress  string  `json:"validator_address"`
// 	PerformanceScore  float64 `json:"performance_score"`
// 	ReliabilityScore  float64 `json:"reliability_score"`
// 	CommissionScore   float64 `json:"commission_score"`
// 	RiskScore         float64 `json:"risk_score"`
// 	LiquidityScore    float64 `json:"liquidity_score"`
// 	OverallScore      float64 `json:"overall_score"`
// 	RecommendedWeight float64 `json:"recommended_weight"`
// }

// // DelegationTarget represents a target allocation
// type DelegationTarget struct {
// 	ValidatorAddress string  `json:"validator_address"`
// 	TargetWeight     float64 `json:"target_weight"`
// 	CurrentWeight    float64 `json:"current_weight"`
// 	TargetAmount     int64   `json:"target_amount"`
// 	CurrentAmount    int64   `json:"current_amount"`
// 	RequiredAction   string  `json:"required_action"` // "increase", "decrease", "maintain"
// }

// // NewManager creates a new delegation manager
// func NewManager(
// 	config *config.Config,
// 	worldState *state.WorldState,
// 	validatorMgr *validator.Manager,
// 	strategy DelegationStrategy,
// ) *Manager {
// 	manager := &Manager{
// 		config:               config,
// 		worldState:           worldState,
// 		validatorMgr:         validatorMgr,
// 		strategy:             strategy,
// 		activeDelegations:    make(map[string]*DelegationInfo),
// 		pendingRedelegations: make(map[string]*RedelegationInfo),
// 		maxValidatorsPerPool: 20,                                  // Diversify across top 20 validators
// 		minDelegationAmount:  config.Economics.MinDelegation * 10, // 10x minimum
// 		rebalanceThreshold:   0.05,                                // 5% drift threshold
// 		riskTolerance:        0.6,                                 // Moderate risk tolerance
// 		performanceWindow:    config.Staking.SignedBlocksWindow,
// 	}

// 	// Initialize components
// 	manager.validatorScorer = NewValidatorScorer(config, validatorMgr)
// 	manager.rebalancer = NewPortfolioRebalancer(manager)
// 	manager.riskManager = NewRiskManager(config, validatorMgr)
// 	manager.slashingTracker = NewSlashingTracker(validatorMgr)
// 	manager.unbondingQueue = NewUnbondingQueue(config)

// 	return manager
// }

// // Delegate distributes stake across validators according to strategy
// func (dm *Manager) Delegate(amount int64, delegatorAddress string) (*DelegationResult, error) {
// 	dm.mu.Lock()
// 	defer dm.mu.Unlock()

// 	if amount < dm.config.Economics.MinDelegation {
// 		return nil, fmt.Errorf("delegation amount %d below minimum %d",
// 			amount, dm.config.Economics.MinDelegation)
// 	}

// 	// Get current validator scores and targets
// 	scores, err := dm.validatorScorer.ScoreValidators()
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to score validators: %v", err)
// 	}

// 	// Calculate optimal delegation distribution
// 	targets, err := dm.calculateDelegationTargets(amount, scores)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to calculate targets: %v", err)
// 	}

// 	// Execute delegations
// 	result := &DelegationResult{
// 		TotalAmount:      amount,
// 		DelegatorAddress: delegatorAddress,
// 		Timestamp:        time.Now().Unix(),
// 		Delegations:      make([]*DelegationInfo, 0),
// 		ExpectedAPY:      0,
// 		RiskScore:        0,
// 	}

// 	for _, target := range targets {
// 		if target.TargetAmount <= 0 {
// 			continue
// 		}

// 		// Execute delegation to validator
// 		delegation, err := dm.executeDelegation(
// 			delegatorAddress,
// 			target.ValidatorAddress,
// 			target.TargetAmount,
// 		)
// 		if err != nil {
// 			return nil, fmt.Errorf("failed to delegate to %s: %v",
// 				target.ValidatorAddress, err)
// 		}

// 		result.Delegations = append(result.Delegations, delegation)
// 		dm.activeDelegations[target.ValidatorAddress] = delegation
// 	}

// 	// Update metrics
// 	dm.totalDelegated += amount
// 	dm.updatePortfolioMetrics()

// 	// Calculate expected returns
// 	result.ExpectedAPY = dm.calculateExpectedAPY(result.Delegations)
// 	result.RiskScore = dm.calculatePortfolioRisk(result.Delegations)

// 	return result, nil
// }

// // executeDelegation performs the actual delegation to a validator
// func (dm *Manager) executeDelegation(
// 	delegatorAddr, validatorAddr string,
// 	amount int64,
// ) (*DelegationInfo, error) {

// 	// Add delegation through validator manager
// 	if err := dm.validatorMgr.AddDelegation(validatorAddr, delegatorAddr, amount); err != nil {
// 		return nil, fmt.Errorf("validator delegation failed: %v", err)
// 	}

// 	// Get validator info for metrics
// 	validator, err := dm.validatorMgr.GetValidator(validatorAddr)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to get validator: %v", err)
// 	}

// 	// Create delegation info
// 	delegation := &DelegationInfo{
// 		ValidatorAddress:   validatorAddr,
// 		Amount:             amount,
// 		Timestamp:          time.Now().Unix(),
// 		AccumulatedRewards: 0,
// 		Commission:         validator.Commission,
// 		Performance:        dm.validatorMgr.GetPerformanceScore(validatorAddr),
// 		RiskScore:          dm.riskManager.GetValidatorRisk(validatorAddr),
// 		LastRewardClaim:    time.Now().Unix(),
// 		IsActive:           true,
// 	}

// 	return delegation, nil
// }

// // Undelegate removes stake from validators and manages unbonding
// func (dm *Manager) Undelegate(amount int64, delegatorAddress string) (*UndelegationResult, error) {
// 	dm.mu.Lock()
// 	defer dm.mu.Unlock()

// 	if amount <= 0 {
// 		return nil, fmt.Errorf("undelegation amount must be positive")
// 	}

// 	totalDelegated := dm.getTotalDelegatedAmount()
// 	if amount > totalDelegated {
// 		return nil, fmt.Errorf("undelegation amount %d exceeds total delegated %d",
// 			amount, totalDelegated)
// 	}

// 	// Calculate optimal undelegation distribution
// 	undelegationPlan, err := dm.calculateUndelegationPlan(amount)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to calculate undelegation plan: %v", err)
// 	}

// 	result := &UndelegationResult{
// 		TotalAmount:      amount,
// 		DelegatorAddress: delegatorAddress,
// 		Timestamp:        time.Now().Unix(),
// 		Undelegations:    make([]*UndelegationInfo, 0),
// 		UnbondingTime:    dm.config.Staking.UnbondingTime,
// 		CompletionTime:   time.Now().Add(dm.config.Staking.UnbondingTime).Unix(),
// 	}

// 	// Execute undelegations
// 	for validatorAddr, undelegateAmount := range undelegationPlan {
// 		undelegation, err := dm.executeUndelegation(
// 			delegatorAddress,
// 			validatorAddr,
// 			undelegateAmount,
// 		)
// 		if err != nil {
// 			return nil, fmt.Errorf("failed to undelegate from %s: %v",
// 				validatorAddr, err)
// 		}

// 		result.Undelegations = append(result.Undelegations, undelegation)

// 		// Add to unbonding queue
// 		dm.unbondingQueue.Add(&UnbondingEntry{
// 			DelegatorAddress: delegatorAddress,
// 			ValidatorAddress: validatorAddr,
// 			Amount:           undelegateAmount,
// 			CompletionTime:   result.CompletionTime,
// 		})
// 	}

// 	// Update metrics
// 	dm.totalDelegated -= amount
// 	dm.updatePortfolioMetrics()

// 	return result, nil
// }

// // executeUndelegation performs the actual undelegation from a validator
// func (dm *Manager) executeUndelegation(
// 	delegatorAddr, validatorAddr string,
// 	amount int64,
// ) (*UndelegationInfo, error) {

// 	// Remove delegation through validator manager
// 	if err := dm.validatorMgr.RemoveDelegation(validatorAddr, delegatorAddr, amount); err != nil {
// 		return nil, fmt.Errorf("validator undelegation failed: %v", err)
// 	}

// 	// Update delegation info
// 	if delegation, exists := dm.activeDelegations[validatorAddr]; exists {
// 		delegation.Amount -= amount
// 		if delegation.Amount <= 0 {
// 			delegation.IsActive = false
// 		}
// 	}

// 	undelegation := &UndelegationInfo{
// 		ValidatorAddress: validatorAddr,
// 		Amount:           amount,
// 		Timestamp:        time.Now().Unix(),
// 	}

// 	return undelegation, nil
// }

// // Rebalance optimizes the delegation portfolio based on current performance
// func (dm *Manager) Rebalance() (*RebalanceResult, error) {
// 	dm.mu.Lock()
// 	defer dm.mu.Unlock()

// 	// Check if rebalancing is needed
// 	if !dm.needsRebalancing() {
// 		return &RebalanceResult{
// 			RebalanceNeeded: false,
// 			Reason:          "Portfolio within acceptable drift limits",
// 		}, nil
// 	}

// 	// Get current scores and calculate new targets
// 	scores, err := dm.validatorScorer.ScoreValidators()
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to score validators: %v", err)
// 	}

// 	currentTotal := dm.getTotalDelegatedAmount()
// 	newTargets, err := dm.calculateDelegationTargets(currentTotal, scores)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to calculate new targets: %v", err)
// 	}

// 	// Execute rebalancing through redelegations
// 	result, err := dm.rebalancer.ExecuteRebalance(newTargets)
// 	if err != nil {
// 		return nil, fmt.Errorf("rebalancing failed: %v", err)
// 	}

// 	dm.lastRebalanceTime = time.Now().Unix()
// 	return result, nil
// }

// // calculateDelegationTargets determines optimal allocation across validators
// func (dm *Manager) calculateDelegationTargets(
// 	amount int64,
// 	scores []*ValidatorScore,
// ) ([]*DelegationTarget, error) {

// 	if len(scores) == 0 {
// 		return nil, fmt.Errorf("no validators available for delegation")
// 	}

// 	// Sort validators by score (descending)
// 	sort.Slice(scores, func(i, j int) bool {
// 		return scores[i].OverallScore > scores[j].OverallScore
// 	})

// 	// Select top validators based on strategy
// 	selectedValidators := dm.selectValidatorsForDelegation(scores)

// 	// Calculate weights based on strategy
// 	weights := dm.calculateWeights(selectedValidators)

// 	// Create delegation targets
// 	targets := make([]*DelegationTarget, 0, len(selectedValidators))

// 	for i, validator := range selectedValidators {
// 		weight := weights[i]
// 		targetAmount := int64(float64(amount) * weight)

// 		// Ensure minimum delegation amount
// 		if targetAmount < dm.minDelegationAmount {
// 			continue
// 		}

// 		target := &DelegationTarget{
// 			ValidatorAddress: validator.ValidatorAddress,
// 			TargetWeight:     weight,
// 			TargetAmount:     targetAmount,
// 			RequiredAction:   "increase",
// 		}

// 		// Check current delegation
// 		if currentDelegation, exists := dm.activeDelegations[validator.ValidatorAddress]; exists {
// 			target.CurrentAmount = currentDelegation.Amount
// 			target.CurrentWeight = float64(currentDelegation.Amount) / float64(amount)

// 			if target.TargetAmount > target.CurrentAmount {
// 				target.RequiredAction = "increase"
// 			} else if target.TargetAmount < target.CurrentAmount {
// 				target.RequiredAction = "decrease"
// 			} else {
// 				target.RequiredAction = "maintain"
// 			}
// 		}

// 		targets = append(targets, target)
// 	}

// 	return targets, nil
// }

// // selectValidatorsForDelegation chooses validators based on strategy
// func (dm *Manager) selectValidatorsForDelegation(scores []*ValidatorScore) []*ValidatorScore {
// 	maxValidators := int(math.Min(float64(len(scores)), float64(dm.maxValidatorsPerPool)))

// 	switch dm.strategy {
// 	case StrategyEqualWeight:
// 		return scores[:maxValidators]

// 	case StrategyPerformanceBased:
// 		// Filter by performance threshold
// 		var filtered []*ValidatorScore
// 		for _, score := range scores {
// 			if score.PerformanceScore >= 0.8 && len(filtered) < maxValidators {
// 				filtered = append(filtered, score)
// 			}
// 		}
// 		return filtered

// 	case StrategyRiskOptimized:
// 		// Balance between performance and risk
// 		var filtered []*ValidatorScore
// 		for _, score := range scores {
// 			if score.RiskScore <= dm.riskTolerance && len(filtered) < maxValidators {
// 				filtered = append(filtered, score)
// 			}
// 		}
// 		return filtered

// 	case StrategyYieldMaximizing:
// 		// Focus on highest yield (lowest commission, highest performance)
// 		var filtered []*ValidatorScore
// 		for _, score := range scores {
// 			if score.CommissionScore >= 0.7 && score.PerformanceScore >= 0.8 && len(filtered) < maxValidators {
// 				filtered = append(filtered, score)
// 			}
// 		}
// 		return filtered

// 	case StrategyConservative:
// 		// Low risk, established validators
// 		var filtered []*ValidatorScore
// 		for _, score := range scores {
// 			if score.ReliabilityScore >= 0.9 && score.RiskScore <= 0.3 && len(filtered) < maxValidators {
// 				filtered = append(filtered, score)
// 			}
// 		}
// 		return filtered

// 	default:
// 		return scores[:maxValidators]
// 	}
// }

// // calculateWeights determines the weight allocation for selected validators
// func (dm *Manager) calculateWeights(validators []*ValidatorScore) []float64 {
// 	if len(validators) == 0 {
// 		return []float64{}
// 	}

// 	weights := make([]float64, len(validators))

// 	switch dm.strategy {
// 	case StrategyEqualWeight:
// 		// Equal weights
// 		weight := 1.0 / float64(len(validators))
// 		for i := range weights {
// 			weights[i] = weight
// 		}

// 	case StrategyPerformanceBased:
// 		// Weights based on performance scores
// 		totalScore := 0.0
// 		for _, v := range validators {
// 			totalScore += v.PerformanceScore
// 		}
// 		for i, v := range validators {
// 			weights[i] = v.PerformanceScore / totalScore
// 		}

// 	case StrategyRiskOptimized:
// 		// Weights based on risk-adjusted returns
// 		totalScore := 0.0
// 		for _, v := range validators {
// 			riskAdjusted := v.PerformanceScore / (1.0 + v.RiskScore)
// 			totalScore += riskAdjusted
// 		}
// 		for i, v := range validators {
// 			riskAdjusted := v.PerformanceScore / (1.0 + v.RiskScore)
// 			weights[i] = riskAdjusted / totalScore
// 		}

// 	default:
// 		// Default to overall score weighting
// 		totalScore := 0.0
// 		for _, v := range validators {
// 			totalScore += v.OverallScore
// 		}
// 		for i, v := range validators {
// 			weights[i] = v.OverallScore / totalScore
// 		}
// 	}

// 	return weights
// }

// // Helper methods
// func (dm *Manager) getTotalDelegatedAmount() int64 {
// 	total := int64(0)
// 	for _, delegation := range dm.activeDelegations {
// 		if delegation.IsActive {
// 			total += delegation.Amount
// 		}
// 	}
// 	return total
// }

// func (dm *Manager) needsRebalancing() bool {
// 	// Check time since last rebalance
// 	if time.Now().Unix()-dm.lastRebalanceTime < 86400 { // Daily rebalancing max
// 		return false
// 	}

// 	// Check portfolio drift
// 	return dm.calculatePortfolioDrift() > dm.rebalanceThreshold
// }

// func (dm *Manager) calculatePortfolioDrift() float64 {
// 	// Simplified drift calculation
// 	// In practice, would compare current weights vs target weights
// 	return 0.02 // Placeholder
// }

// func (dm *Manager) updatePortfolioMetrics() {
// 	// Update average APY, risk, and other portfolio metrics
// 	// Implementation would calculate these based on current delegations
// }

// func (dm *Manager) calculateExpectedAPY(delegations []*DelegationInfo) float64 {
// 	if len(delegations) == 0 {
// 		return 0
// 	}

// 	weightedAPY := 0.0
// 	totalAmount := int64(0)

// 	for _, delegation := range delegations {
// 		// Get validator expected APY
// 		validatorAPY := dm.getValidatorAPY(delegation.ValidatorAddress)
// 		weight := float64(delegation.Amount)
// 		weightedAPY += validatorAPY * weight
// 		totalAmount += delegation.Amount
// 	}

// 	return weightedAPY / float64(totalAmount)
// }

// func (dm *Manager) calculatePortfolioRisk(delegations []*DelegationInfo) float64 {
// 	// Simplified risk calculation
// 	// Would consider validator concentration, slashing history, etc.
// 	return 0.3 // Placeholder
// }

// func (dm *Manager) getValidatorAPY(validatorAddr string) float64 {
// 	// Get expected APY for validator
// 	// Implementation would query validator manager
// 	return 8.0 // Placeholder
// }

// func (dm *Manager) calculateUndelegationPlan(amount int64) (map[string]int64, error) {
// 	// Calculate optimal undelegation distribution
// 	// Prefer undelegating from worst performing validators first
// 	plan := make(map[string]int64)

// 	// Sort delegations by performance (ascending - worst first)
// 	var sortedDelegations []*DelegationInfo
// 	for _, delegation := range dm.activeDelegations {
// 		if delegation.IsActive {
// 			sortedDelegations = append(sortedDelegations, delegation)
// 		}
// 	}

// 	sort.Slice(sortedDelegations, func(i, j int) bool {
// 		return sortedDelegations[i].Performance < sortedDelegations[j].Performance
// 	})

// 	remaining := amount
// 	for _, delegation := range sortedDelegations {
// 		if remaining <= 0 {
// 			break
// 		}

// 		undelegateAmount := int64(math.Min(float64(remaining), float64(delegation.Amount)))
// 		plan[delegation.ValidatorAddress] = undelegateAmount
// 		remaining -= undelegateAmount
// 	}

// 	if remaining > 0 {
// 		return nil, fmt.Errorf("insufficient delegated amount to undelegate %d", amount)
// 	}

// 	return plan, nil
// }

// // Result types
// type DelegationResult struct {
// 	TotalAmount      int64             `json:"total_amount"`
// 	DelegatorAddress string            `json:"delegator_address"`
// 	Timestamp        int64             `json:"timestamp"`
// 	Delegations      []*DelegationInfo `json:"delegations"`
// 	ExpectedAPY      float64           `json:"expected_apy"`
// 	RiskScore        float64           `json:"risk_score"`
// }

// type UndelegationResult struct {
// 	TotalAmount      int64               `json:"total_amount"`
// 	DelegatorAddress string              `json:"delegator_address"`
// 	Timestamp        int64               `json:"timestamp"`
// 	Undelegations    []*UndelegationInfo `json:"undelegations"`
// 	UnbondingTime    time.Duration       `json:"unbonding_time"`
// 	CompletionTime   int64               `json:"completion_time"`
// }

// type UndelegationInfo struct {
// 	ValidatorAddress string `json:"validator_address"`
// 	Amount           int64  `json:"amount"`
// 	Timestamp        int64  `json:"timestamp"`
// }

// type RebalanceResult struct {
// 	RebalanceNeeded bool               `json:"rebalance_needed"`
// 	Reason          string             `json:"reason"`
// 	Actions         []*RebalanceAction `json:"actions"`
// 	TotalMoved      int64              `json:"total_moved"`
// 	ExpectedAPY     float64            `json:"expected_apy"`
// 	RiskReduction   float64            `json:"risk_reduction"`
// }

// type RebalanceAction struct {
// 	Type          string `json:"type"` // "redelegate", "delegate", "undelegate"
// 	FromValidator string `json:"from_validator"`
// 	ToValidator   string `json:"to_validator"`
// 	Amount        int64  `json:"amount"`
// 	Reason        string `json:"reason"`
// }
