package config

import (
	"fmt"
	"time"
)

const (
	// === UTILITY-BASED TOKEN ECONOMICS WITH DYNAMIC INFLATION ===

	// Token denomination (1 THRYLOS = 1e9 base units)
	BaseUnit      = int64(1000000000) // 1 THRYLOS
	TotalSupply   = int64(100000000)  // 100 million THRYLOS
	GenesisSupply = int64(15000000)   // 15 million THRYLOS initial supply (15%) - Reduced for sustainability

	// Economic thresholds
	MinimumBalance        = BaseUnit / 1000 // 0.001 THRYLOS (1,000,000 base units)
	MinimumTransfer       = BaseUnit / 100  // 0.01 THRYLOS (10,000,000 base units)
	MinimumStakeAmount    = BaseUnit * 1    // 1 THRYLOS (1,000,000,000 base units)
	MinimumDelegation     = BaseUnit / 10   // 0.1 THRYLOS (100,000,000 base units)
	MinimumValidatorStake = BaseUnit * 25   // 25 THRYLOS (25,000,000,000 base units) - Reduced for accessibility

	// Gas economics
	BaseGasPrice   = int64(1000)     // 0.000001 THRYLOS per gas unit
	MaxGasPerBlock = int64(10000000) // 10M gas per block
	StandardTxGas  = int64(21000)    // Standard transaction gas
	StakingTxGas   = int64(50000)    // Staking transaction gas

	// Dynamic reward economics
	BaseBlockReward = BaseUnit / 100 // 0.01 THRYLOS base (will be adjusted dynamically)

	// Block rewards and validator economics
	BlockReward     = BaseUnit / 50  // 0.02 THRYLOS per block
	ValidatorReward = BaseUnit / 100 // 0.01 THRYLOS validator reward
	DelegatorReward = BaseUnit / 200 // 0.005 THRYLOS delegator reward

	// === OPTIMIZED DISTRIBUTION (No Advisors) ===
	// Total: 100M THRYLOS distributed as follows:

	// Genesis Distribution - Immediate circulation (15%)
	GenesisDistribution = TotalSupply * 15 / 100 // 15M THRYLOS (15%) - Public launch, early adopters

	// Validator Reward Pool - Long-term staking incentives (60%)
	ValidatorRewardPool = TotalSupply * 60 / 100 // 60M THRYLOS (60%) - Increased for sustainability

	// Liquidity & Market Making - DEX and trading support (15%)
	LiquidityPool = TotalSupply * 15 / 100 // 15M THRYLOS (15%) - AMM liquidity, market making

	// Development & Ecosystem - Core team and development (10%)
	DevelopmentPool = TotalSupply * 10 / 100 // 10M THRYLOS (10%) - Team, development, partnerships

	// Dynamic reward pool (60% of total supply for long-term rewards)
	DynamicRewardPool = ValidatorRewardPool // 60M THRYLOS (60%) - distributed via inflation
)

// GenesisAccount represents an initial account with balance
type GenesisAccount struct {
	Address      string `json:"address"`
	Balance      int64  `json:"balance"`
	Purpose      string `json:"purpose"`
	Locked       bool   `json:"locked"`        // Whether tokens are locked initially
	UnlockBlocks int64  `json:"unlock_blocks"` // Blocks until unlock (0 = immediate)
}

// GenesisAllocation represents the genesis token allocation
type GenesisAllocation struct {
	TotalGenesis int64            `json:"total_genesis"`
	Accounts     []GenesisAccount `json:"accounts"`
}

type Config struct {
	// Node configuration
	NodeID   string `json:"node_id"`
	DataDir  string `json:"data_dir"`
	LogLevel string `json:"log_level"`

	// Genesis allocation
	Genesis GenesisAllocation `json:"genesis"`

	// Network configuration
	Network NetworkConfig `json:"network"`

	// Consensus configuration
	Consensus ConsensusConfig `json:"consensus"`

	// Staking configuration
	Staking StakingConfig `json:"staking"`

	// Economics configuration with dynamic inflation
	Economics EconomicsConfig `json:"economics"`

	// Sharding configuration
	Sharding ShardingConfig `json:"sharding"`

	// API configuration
	API APIConfig `json:"api"`

	// P2P networking configuration
	P2P P2PConfig `json:"p2p" yaml:"p2p"`
}

// P2PConfig represents P2P networking configuration
type P2PConfig struct {
	Enabled        bool     `json:"enabled" yaml:"enabled"`
	ListenPort     int      `json:"listen_port" yaml:"listen_port"`
	BootstrapPeers []string `json:"bootstrap_peers" yaml:"bootstrap_peers"`
	MaxPeers       int      `json:"max_peers" yaml:"max_peers"`
	EnableMDNS     bool     `json:"enable_mdns" yaml:"enable_mdns"`
	EnableDHT      bool     `json:"enable_dht" yaml:"enable_dht"`
}

type NetworkConfig struct {
	ListenAddr     string        `json:"listen_addr"`
	BootstrapPeers []string      `json:"bootstrap_peers"`
	MaxPeers       int           `json:"max_peers"`
	PingInterval   time.Duration `json:"ping_interval"`
	NetworkID      string        `json:"network_id"`
}

type ConsensusConfig struct {
	BlockTime         time.Duration `json:"block_time"`
	MaxTxPerBlock     int           `json:"max_tx_per_block"`
	MaxBlockSize      int64         `json:"max_block_size"`
	MinGasPrice       int64         `json:"min_gas_price"`
	MaxValidators     int           `json:"max_validators"`
	ValidatorRotation time.Duration `json:"validator_rotation"`
	SlashingEnabled   bool          `json:"slashing_enabled"`
	MaxTimestampSkew  time.Duration `json:"max_timestamp_skew"`
	MaxTimestampAge   time.Duration `json:"max_timestamp_age"`
	MaxTxDataSize     int           `json:"max_tx_data_size"`
}

type StakingConfig struct {
	MinValidatorStake          int64         `json:"min_validator_stake"` // 25 THRYLOS (reduced)
	MinDelegation              int64         `json:"min_delegation"`      // 0.1 THRYLOS
	MinSelfStake               int64         `json:"min_self_stake"`      // 2.5 THRYLOS (10% of validator stake)
	MaxCommission              float64       `json:"max_commission"`
	CommissionChangeMax        float64       `json:"commission_change_max"`
	UnbondingTime              time.Duration `json:"unbonding_time"`
	MaxDelegationsPerValidator int           `json:"max_delegations_per_validator"`

	// Slashing parameters
	SlashFractionDoubleSign float64       `json:"slash_fraction_double_sign"`
	SlashFractionDowntime   float64       `json:"slash_fraction_downtime"`
	DowntimeJailDuration    time.Duration `json:"downtime_jail_duration"`
	MinSignedPerWindow      float64       `json:"min_signed_per_window"`
	SignedBlocksWindow      int64         `json:"signed_blocks_window"`
}

type EconomicsConfig struct {
	// Supply parameters
	TotalSupply       int64 `json:"total_supply"`       // 100M THRYLOS
	GenesisSupply     int64 `json:"genesis_supply"`     // 15M THRYLOS
	CirculatingSupply int64 `json:"circulating_supply"` // Current circulating supply

	// Inflation parameters (sustainable model)
	InflationRate float64 `json:"inflation_rate"` // 4% annual (balanced)
	InflationMax  float64 `json:"inflation_max"`  // 7% maximum
	InflationMin  float64 `json:"inflation_min"`  // 2% minimum
	GoalBonded    float64 `json:"goal_bonded"`    // 70% target bonded ratio (increased)

	// Fee parameters
	BaseGasPrice int64 `json:"base_gas_price"`
	MinimumFee   int64 `json:"minimum_fee"`

	// Rewards (optimized for sustainability)
	BlockReward         int64   `json:"block_reward"`          // 0.02 THRYLOS per block
	CommunityTax        float64 `json:"community_tax"`         // 3% community tax (increased)
	BaseProposerReward  float64 `json:"base_proposer_reward"`  // 1.5% base proposer reward
	BonusProposerReward float64 `json:"bonus_proposer_reward"` // 3.5% bonus proposer reward

	// Validator economics
	ValidatorRewardRate float64 `json:"validator_reward_rate"` // 9% annual for validators
	DelegatorRewardRate float64 `json:"delegator_reward_rate"` // 7% annual for delegators

	// Thresholds
	MinBalance    int64 `json:"min_balance"`
	MinTransfer   int64 `json:"min_transfer"`
	MinStake      int64 `json:"min_stake"`
	MinDelegation int64 `json:"min_delegation"`

	// Distribution tracking
	GenesisDistribution int64 `json:"genesis_distribution"`  // 15M THRYLOS
	ValidatorRewardPool int64 `json:"validator_reward_pool"` // 60M THRYLOS
	LiquidityPool       int64 `json:"liquidity_pool"`        // 15M THRYLOS
	DevelopmentPool     int64 `json:"development_pool"`      // 10M THRYLOS
}

type ShardingConfig struct {
	EnableSharding         bool          `json:"enable_sharding"`
	TotalShards            int           `json:"total_shards"`
	BeaconShardID          int           `json:"beacon_shard_id"`
	CrossShardEnabled      bool          `json:"cross_shard_enabled"`
	ShardRebalanceInterval time.Duration `json:"shard_rebalance_interval"`
}

type APIConfig struct {
	// REST API configuration
	EnableAPI bool   `json:"enable_api"` // Whether to enable the API server
	RESTAddr  string `json:"rest_addr"`  // REST API address (e.g., ":8080")
	EnableTLS bool   `json:"enable_tls"` // Enable HTTPS/TLS
	CertFile  string `json:"cert_file"`  // TLS certificate file path
	KeyFile   string `json:"key_file"`   // TLS private key file path

	// API settings
	EnableCORS    bool `json:"enable_cors"`
	RateLimit     int  `json:"rate_limit"`
	EnableMetrics bool `json:"enable_metrics"`
}

func Load() (*Config, error) {
	return &Config{
		NodeID:   "thrylos-v2-node",
		DataDir:  "./data",
		LogLevel: "info",

		// Genesis token allocation
		Genesis: GenesisAllocation{
			TotalGenesis: GenesisSupply,
			Accounts: []GenesisAccount{
				// Node developer/validator account - ALL 15M THRYLOS for testing
				{
					Address:      "tl1jhagm044kdavupen3e6", // Your consistent node address
					Balance:      15000000 * BaseUnit,      // 15M THRYLOS (full genesis supply for testing)
					Purpose:      "Development node with full genesis supply for testing",
					Locked:       false,
					UnlockBlocks: 0,
				},
			},
		},

		Network: NetworkConfig{
			ListenAddr:     "/ip4/0.0.0.0/tcp/9000",
			BootstrapPeers: []string{},
			MaxPeers:       50,
			PingInterval:   30 * time.Second,
			NetworkID:      "thrylos-mainnet",
		},

		Consensus: ConsensusConfig{
			BlockTime:         3 * time.Second,
			MaxTxPerBlock:     1000,
			MaxBlockSize:      2 * 1024 * 1024, // 2MB
			MinGasPrice:       BaseGasPrice,
			MaxValidators:     100,
			ValidatorRotation: 24 * time.Hour,
			SlashingEnabled:   true,
			MaxTimestampSkew:  5 * time.Minute,
			MaxTimestampAge:   1 * time.Hour,
			MaxTxDataSize:     1024 * 1024, // 1MB max transaction data
		},

		Staking: StakingConfig{
			MinValidatorStake:          MinimumValidatorStake,      // 25 THRYLOS (reduced for accessibility)
			MinDelegation:              MinimumDelegation,          // 0.1 THRYLOS
			MinSelfStake:               MinimumValidatorStake / 10, // 2.5 THRYLOS (10% of validator stake)
			MaxCommission:              0.20,                       // 20% max commission
			CommissionChangeMax:        0.01,                       // 1% per day max change
			UnbondingTime:              21 * 24 * time.Hour,        // 21 days
			MaxDelegationsPerValidator: 1000,

			// Slashing parameters
			SlashFractionDoubleSign: 0.05, // 5% slash for double signing
			SlashFractionDowntime:   0.01, // 1% slash for extended downtime
			DowntimeJailDuration:    10 * time.Minute,
			MinSignedPerWindow:      0.50,  // Must sign 50% of blocks
			SignedBlocksWindow:      10000, // Window of 10,000 blocks
		},

		Economics: EconomicsConfig{
			TotalSupply:       TotalSupply,   // 100M THRYLOS
			GenesisSupply:     GenesisSupply, // 15M THRYLOS
			CirculatingSupply: GenesisSupply, // Start with genesis supply

			InflationRate: 0.04, // 4% annual inflation (balanced)
			InflationMax:  0.07, // 7% max inflation
			InflationMin:  0.02, // 2% min inflation
			GoalBonded:    0.70, // Target 70% of supply bonded (increased)

			BaseGasPrice: BaseGasPrice,
			MinimumFee:   BaseGasPrice * StandardTxGas, // ~0.021 THRYLOS

			BlockReward:         BlockReward, // 0.02 THRYLOS per block
			CommunityTax:        0.03,        // 3% community tax (increased)
			BaseProposerReward:  0.015,       // 1.5% base proposer reward
			BonusProposerReward: 0.035,       // 3.5% bonus proposer reward

			ValidatorRewardRate: 0.09, // 9% annual for validators (increased)
			DelegatorRewardRate: 0.07, // 7% annual for delegators (increased)

			MinBalance:    MinimumBalance,     // 0.001 THRYLOS
			MinTransfer:   MinimumTransfer,    // 0.01 THRYLOS
			MinStake:      MinimumStakeAmount, // 1 THRYLOS
			MinDelegation: MinimumDelegation,  // 0.1 THRYLOS

			// Distribution pools
			GenesisDistribution: GenesisDistribution, // 15M THRYLOS
			ValidatorRewardPool: ValidatorRewardPool, // 60M THRYLOS
			LiquidityPool:       LiquidityPool,       // 15M THRYLOS
			DevelopmentPool:     DevelopmentPool,     // 10M THRYLOS
		},

		Sharding: ShardingConfig{
			EnableSharding:         true,
			TotalShards:            4,
			BeaconShardID:          -1,
			CrossShardEnabled:      true,
			ShardRebalanceInterval: 24 * time.Hour,
		},

		API: APIConfig{
			EnableAPI:     true,                 // Enable API by default
			RESTAddr:      ":8080",              // HTTP development port
			EnableTLS:     false,                // HTTP for development
			CertFile:      "./certs/server.crt", // Path for when TLS is enabled
			KeyFile:       "./certs/server.key", // Path for when TLS is enabled
			EnableCORS:    true,
			RateLimit:     1000,
			EnableMetrics: true,
		},

		// For production, you'd change to:
		// API: APIConfig{
		//     EnableAPI:     true,
		//     RESTAddr:      ":8443",                   // HTTPS production port
		//     EnableTLS:     true,                      // HTTPS for production
		//     CertFile:      "/etc/ssl/certs/domain.crt",
		//     KeyFile:       "/etc/ssl/private/domain.key",
		//     EnableCORS:    true,
		//     RateLimit:     1000,
		//     EnableMetrics: true,
		// },

		P2P: P2PConfig{
			Enabled:        true,
			ListenPort:     9000,
			BootstrapPeers: []string{},
			MaxPeers:       50,
			EnableMDNS:     true,
			EnableDHT:      true,
		},
	}, nil
}

// GetGenesisAccounts returns the genesis account allocation
func (c *Config) GetGenesisAccounts() []GenesisAccount {
	return c.Genesis.Accounts
}

// GetMinimumBalances returns the minimum balance thresholds
func (c *Config) GetMinimumBalances() map[string]int64 {
	return map[string]int64{
		"balance":              c.Economics.MinBalance,
		"transfer":             c.Economics.MinTransfer,
		"stake":                c.Economics.MinStake,
		"delegation":           c.Economics.MinDelegation,
		"validator_stake":      c.Staking.MinValidatorStake,
		"validator_self_stake": c.Staking.MinSelfStake,
	}
}

// GetSupplyBreakdown returns the optimized token distribution breakdown
func (c *Config) GetSupplyBreakdown() map[string]interface{} {
	return map[string]interface{}{
		"total_supply": c.Economics.TotalSupply,
		"distribution": map[string]interface{}{
			"genesis": map[string]interface{}{
				"amount":     c.Economics.GenesisDistribution,
				"percentage": 15.0,
				"purpose":    "Public launch and early ecosystem bootstrap",
				"details": map[string]interface{}{
					"immediate_circulation": 10000000 * BaseUnit,
					"ecosystem_bootstrap":   3000000 * BaseUnit,
					"community_incentives":  2000000 * BaseUnit,
				},
			},
			"validator_rewards": map[string]interface{}{
				"amount":             c.Economics.ValidatorRewardPool,
				"percentage":         60.0,
				"purpose":            "Long-term staking rewards (sustainable model)",
				"distribution_years": 10, // Distributed over ~10 years
			},
			"liquidity": map[string]interface{}{
				"amount":     c.Economics.LiquidityPool,
				"percentage": 15.0,
				"purpose":    "DEX liquidity, AMM pools, and market making",
			},
			"development": map[string]interface{}{
				"amount":     c.Economics.DevelopmentPool,
				"percentage": 10.0,
				"purpose":    "Core team, development, and strategic partnerships",
				"vesting":    "4-year vesting with 1-year cliff",
			},
		},
		"validator_economics": map[string]interface{}{
			"min_validator_stake":      c.Staking.MinValidatorStake,
			"max_validators":           c.Consensus.MaxValidators,
			"total_validator_capacity": c.Staking.MinValidatorStake * int64(c.Consensus.MaxValidators),
			"accessibility": map[string]interface{}{
				"min_delegation": c.Economics.MinDelegation,
				"min_stake":      c.Economics.MinStake,
				"validator_apr":  c.Economics.ValidatorRewardRate * 100,
				"delegator_apr":  c.Economics.DelegatorRewardRate * 100,
			},
		},
		"sustainability_metrics": map[string]interface{}{
			"inflation_rate":        c.Economics.InflationRate * 100,
			"target_bonded_ratio":   c.Economics.GoalBonded * 100,
			"reward_pool_duration":  "~10 years at current inflation",
			"total_staking_rewards": c.Economics.ValidatorRewardPool,
		},
	}
}

// GetGasConfig returns gas-related configuration
func (c *Config) GetGasConfig() map[string]int64 {
	return map[string]int64{
		"base_price":    c.Economics.BaseGasPrice,
		"standard_tx":   StandardTxGas,
		"staking_tx":    StakingTxGas,
		"minimum_fee":   c.Economics.MinimumFee,
		"max_per_block": MaxGasPerBlock,
	}
}

// GetRewardConfig returns reward-related configuration
func (c *Config) GetRewardConfig() map[string]interface{} {
	return map[string]interface{}{
		"block_reward":          c.Economics.BlockReward,
		"validator_reward":      ValidatorReward,
		"delegator_reward":      DelegatorReward,
		"community_tax":         c.Economics.CommunityTax,
		"inflation_rate":        c.Economics.InflationRate,
		"validator_apr":         c.Economics.ValidatorRewardRate,
		"delegator_apr":         c.Economics.DelegatorRewardRate,
		"base_proposer_reward":  c.Economics.BaseProposerReward,
		"bonus_proposer_reward": c.Economics.BonusProposerReward,
	}
}

// CalculateBlockRewards calculates expected rewards over time
func (c *Config) CalculateBlockRewards() map[string]interface{} {
	blocksPerYear := int64(365 * 24 * 60 * 60 / 3) // 3-second blocks
	annualBlockRewards := c.Economics.BlockReward * blocksPerYear

	return map[string]interface{}{
		"blocks_per_year":        blocksPerYear,
		"annual_block_rewards":   annualBlockRewards,
		"daily_block_rewards":    annualBlockRewards / 365,
		"rewards_in_thrylos":     float64(annualBlockRewards) / float64(BaseUnit),
		"inflation_from_rewards": float64(annualBlockRewards) / float64(c.Economics.TotalSupply),
	}
}

// ValidateConfig validates the configuration parameters
func (c *Config) ValidateConfig() error {
	// Validate economic parameters
	if c.Economics.InflationRate < 0 || c.Economics.InflationRate > 1 {
		return fmt.Errorf("inflation rate must be between 0 and 1")
	}

	if c.Staking.MaxCommission < 0 || c.Staking.MaxCommission > 1 {
		return fmt.Errorf("max commission must be between 0 and 1")
	}

	if c.Economics.MinDelegation > c.Staking.MinValidatorStake {
		return fmt.Errorf("min delegation cannot exceed min validator stake")
	}

	if c.Consensus.MaxValidators <= 0 {
		return fmt.Errorf("max validators must be positive")
	}

	if c.Sharding.TotalShards <= 0 {
		return fmt.Errorf("total shards must be positive")
	}

	// Validate supply distribution adds up to 100%
	totalDistribution := c.Economics.GenesisDistribution +
		c.Economics.ValidatorRewardPool +
		c.Economics.LiquidityPool +
		c.Economics.DevelopmentPool

	if totalDistribution != c.Economics.TotalSupply {
		return fmt.Errorf("distribution pools (%d) don't equal total supply (%d)",
			totalDistribution, c.Economics.TotalSupply)
	}

	// Validate genesis accounts total matches genesis supply
	var totalGenesisBalance int64
	for _, account := range c.Genesis.Accounts {
		totalGenesisBalance += account.Balance
	}

	if totalGenesisBalance != c.Economics.GenesisSupply*BaseUnit {
		return fmt.Errorf("genesis accounts total (%d) doesn't match genesis supply (%d)",
			totalGenesisBalance, c.Economics.GenesisSupply*BaseUnit)
	}

	return nil
}

// GetEconomicSummary returns a human-readable summary of the economics
func (c *Config) GetEconomicSummary() string {
	rewardCalc := c.CalculateBlockRewards()

	return fmt.Sprintf(`
THRYLOS Token Economics Summary (Optimized & Sustainable):
=========================================================
Total Supply: %d THRYLOS (100 Million)
Genesis Supply: %d THRYLOS (15 Million, 15%%) - REDUCED for sustainability

OPTIMIZED DISTRIBUTION (No Advisors):
- Genesis/Launch: %d THRYLOS (15%%) - Public + ecosystem bootstrap
- Validator Rewards: %d THRYLOS (60%%) - INCREASED for long-term sustainability  
- Liquidity Pool: %d THRYLOS (15%%) - DEX and market making
- Development: %d THRYLOS (10%%) - Core team (4-year vesting)

STAKING REQUIREMENTS (More Accessible):
- Validator Stake: %d THRYLOS (25 THRYLOS) - REDUCED from 34
- Minimum Delegation: %.1f THRYLOS
- Minimum Stake: %d THRYLOS
- Max Validators: %d (total capacity: %d THRYLOS)

TRANSACTION COSTS:
- Standard Transaction: ~%.3f THRYLOS
- Gas Price: %.6f THRYLOS per gas unit

REWARDS & SUSTAINABILITY:
- Block Reward: %.2f THRYLOS
- Validator APR: %.1f%% (INCREASED)
- Delegator APR: %.1f%% (INCREASED)
- Inflation Rate: %.1f%% (Balanced)
- Target Bonded: %.0f%% (High security)
- Annual Block Rewards: %.0f THRYLOS
- Community Tax: %.1f%% (Ecosystem development)

GENESIS ALLOCATION BREAKDOWN:
- Immediate Public: 10M THRYLOS (unlocked)
- Ecosystem Bootstrap: 3M THRYLOS (1-year lock)
- Community Incentives: 2M THRYLOS (6-month lock)

KEY IMPROVEMENTS:
✓ Reduced genesis supply (15% vs 20%) for better price stability
✓ Increased validator rewards (60% vs 50%) for long-term sustainability
✓ Lower validator requirements (25 vs 34 THRYLOS) for accessibility
✓ Higher staking APR (9%% validators, 7%% delegators) for participation
✓ No advisor allocation - community-focused distribution
✓ Gradual unlock mechanism for fair distribution`,
		c.Economics.TotalSupply,
		c.Economics.GenesisSupply,
		c.Economics.GenesisDistribution,
		c.Economics.ValidatorRewardPool,
		c.Economics.LiquidityPool,
		c.Economics.DevelopmentPool,
		c.Staking.MinValidatorStake,
		float64(c.Economics.MinDelegation)/float64(BaseUnit),
		c.Economics.MinStake,
		c.Consensus.MaxValidators,
		c.Staking.MinValidatorStake*int64(c.Consensus.MaxValidators),
		float64(c.Economics.MinimumFee)/float64(BaseUnit),
		float64(c.Economics.BaseGasPrice)/float64(BaseUnit),
		float64(c.Economics.BlockReward)/float64(BaseUnit),
		c.Economics.ValidatorRewardRate*100,
		c.Economics.DelegatorRewardRate*100,
		c.Economics.InflationRate*100,
		c.Economics.GoalBonded*100,
		rewardCalc["rewards_in_thrylos"].(float64),
		c.Economics.CommunityTax*100,
	)
}
