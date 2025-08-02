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
	GenesisSupply = int64(20000000)   // 20 million THRYLOS initial supply (20%)

	// Economic thresholds
	MinimumBalance        = BaseUnit / 1000 // 0.001 THRYLOS (1,000,000 base units)
	MinimumTransfer       = BaseUnit / 100  // 0.01 THRYLOS (10,000,000 base units)
	MinimumStakeAmount    = BaseUnit * 1    // 1 THRYLOS (1,000,000,000 base units)
	MinimumDelegation     = BaseUnit / 10   // 0.1 THRYLOS (100,000,000 base units)
	MinimumValidatorStake = BaseUnit * 34   // 34 THRYLOS (34,000,000,000 base units)

	// Gas economics
	BaseGasPrice   = int64(1000)     // 0.000001 THRYLOS per gas unit
	MaxGasPerBlock = int64(10000000) // 10M gas per block
	StandardTxGas  = int64(21000)    // Standard transaction gas
	StakingTxGas   = int64(50000)    // Staking transaction gas

	// Dynamic reward economics (no longer fixed)
	// Note: Block rewards are now calculated dynamically based on inflation
	BaseBlockReward = BaseUnit / 100 // 0.01 THRYLOS base (will be adjusted dynamically)

	// Add missing constants
	BlockReward         = BaseUnit / 50          // 0.02 THRYLOS per block
	ValidatorReward     = BaseUnit / 100         // 0.01 THRYLOS validator reward
	DelegatorReward     = BaseUnit / 200         // 0.005 THRYLOS delegator reward
	ValidatorRewardPool = TotalSupply * 50 / 100 // 50M THRYLOS (50%)

	// Distribution constants (unchanged)
	GenesisDistribution = TotalSupply * 20 / 100 // 20M THRYLOS (20%)
	LiquidityPool       = TotalSupply * 20 / 100 // 20M THRYLOS (20%)
	DevelopmentPool     = TotalSupply * 10 / 100 // 10M THRYLOS (10%)

	// Dynamic reward pool (70% of total supply for long-term rewards)
	DynamicRewardPool = TotalSupply * 70 / 100 // 70M THRYLOS (70%) - distributed via inflation
)

type Config struct {
	// Node configuration
	NodeID   string `json:"node_id"`
	DataDir  string `json:"data_dir"`
	LogLevel string `json:"log_level"`

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
	MaxTimestampSkew  time.Duration `json:"max_timestamp_skew"` // 5 minutes
	MaxTimestampAge   time.Duration `json:"max_timestamp_age"`  // 1 hour
	MaxTxDataSize     int           `json:"max_tx_data_size"`   // Max transaction data bytes
}

type StakingConfig struct {
	MinValidatorStake          int64         `json:"min_validator_stake"` // 34 THRYLOS
	MinDelegation              int64         `json:"min_delegation"`      // 0.1 THRYLOS
	MinSelfStake               int64         `json:"min_self_stake"`      // 3.4 THRYLOS (10% of validator stake)
	MaxCommission              float64       `json:"max_commission"`
	CommissionChangeMax        float64       `json:"commission_change_max"` // Max commission change per day
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
	// Supply parameters (utility-justified)
	TotalSupply       int64 `json:"total_supply"`       // 100M THRYLOS
	GenesisSupply     int64 `json:"genesis_supply"`     // 20M THRYLOS
	CirculatingSupply int64 `json:"circulating_supply"` // Current circulating supply

	// Inflation parameters (adjusted for smaller supply)
	InflationRate float64 `json:"inflation_rate"` // 5% annual (lower due to smaller supply)
	InflationMax  float64 `json:"inflation_max"`  // 8% maximum
	InflationMin  float64 `json:"inflation_min"`  // 2% minimum
	GoalBonded    float64 `json:"goal_bonded"`    // 67% target bonded ratio

	// Fee parameters
	BaseGasPrice int64 `json:"base_gas_price"`
	MinimumFee   int64 `json:"minimum_fee"`

	// Rewards (adjusted for 100M supply)
	BlockReward         int64   `json:"block_reward"`          // 0.02 THRYLOS per block
	CommunityTax        float64 `json:"community_tax"`         // 2% community tax
	BaseProposerReward  float64 `json:"base_proposer_reward"`  // 1% base proposer reward
	BonusProposerReward float64 `json:"bonus_proposer_reward"` // 4% bonus proposer reward

	// Validator economics
	ValidatorRewardRate float64 `json:"validator_reward_rate"` // 8% annual for validators
	DelegatorRewardRate float64 `json:"delegator_reward_rate"` // 6% annual for delegators

	// Thresholds (proportional to 100M supply)
	MinBalance    int64 `json:"min_balance"`    // 0.001 THRYLOS
	MinTransfer   int64 `json:"min_transfer"`   // 0.01 THRYLOS
	MinStake      int64 `json:"min_stake"`      // 1 THRYLOS
	MinDelegation int64 `json:"min_delegation"` // 0.1 THRYLOS

	// Distribution tracking
	GenesisDistribution int64 `json:"genesis_distribution"`  // 20M THRYLOS
	ValidatorRewardPool int64 `json:"validator_reward_pool"` // 50M THRYLOS
	LiquidityPool       int64 `json:"liquidity_pool"`        // 20M THRYLOS
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
	JSONRPCAddr   string `json:"jsonrpc_addr"`
	WebSocketAddr string `json:"websocket_addr"`
	GRPCAddr      string `json:"grpc_addr"`
	EnableCORS    bool   `json:"enable_cors"`
	RateLimit     int    `json:"rate_limit"` // requests per minute
	EnableMetrics bool   `json:"enable_metrics"`
}

func Load() (*Config, error) {
	return &Config{
		NodeID:   "thrylos-v2-node",
		DataDir:  "./data",
		LogLevel: "info",

		Network: NetworkConfig{
			ListenAddr:     "/ip4/0.0.0.0/tcp/9000",
			BootstrapPeers: []string{}, // Add bootstrap peers later
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
			MaxTimestampSkew:  5 * time.Minute, // 5 minutes future tolerance
			MaxTimestampAge:   1 * time.Hour,   // 1 hour past tolerance
			MaxTxDataSize:     1024 * 1024,     // 1MB max transaction data
		},

		Staking: StakingConfig{
			MinValidatorStake:          MinimumValidatorStake,      // 32 THRYLOS
			MinDelegation:              MinimumDelegation,          // 0.1 THRYLOS
			MinSelfStake:               MinimumValidatorStake / 10, // 3.2 THRYLOS (10% of validator stake)
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
			GenesisSupply:     GenesisSupply, // 20M THRYLOS
			CirculatingSupply: GenesisSupply, // Start with genesis supply

			InflationRate: 0.05, // 5% annual inflation (lower due to smaller supply)
			InflationMax:  0.08, // 8% max inflation
			InflationMin:  0.02, // 2% min inflation
			GoalBonded:    0.67, // Target 67% of supply bonded

			BaseGasPrice: BaseGasPrice,
			MinimumFee:   BaseGasPrice * StandardTxGas, // ~0.021 THRYLOS

			BlockReward:         BlockReward, // 0.02 THRYLOS per block
			CommunityTax:        0.02,        // 2% community tax
			BaseProposerReward:  0.01,        // 1% base proposer reward
			BonusProposerReward: 0.04,        // 4% bonus proposer reward

			ValidatorRewardRate: 0.08, // 8% annual for validators
			DelegatorRewardRate: 0.06, // 6% annual for delegators

			MinBalance:    MinimumBalance,     // 0.001 THRYLOS
			MinTransfer:   MinimumTransfer,    // 0.01 THRYLOS
			MinStake:      MinimumStakeAmount, // 1 THRYLOS
			MinDelegation: MinimumDelegation,  // 0.1 THRYLOS

			// Distribution pools
			GenesisDistribution: GenesisDistribution, // 20M THRYLOS
			ValidatorRewardPool: ValidatorRewardPool, // 50M THRYLOS
			LiquidityPool:       LiquidityPool,       // 20M THRYLOS
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
			JSONRPCAddr:   ":8545",
			WebSocketAddr: ":8546",
			GRPCAddr:      ":9090",
			EnableCORS:    true,
			RateLimit:     1000, // 1000 requests per minute
			EnableMetrics: true,
		},

		// ADD THIS P2P CONFIGURATION:
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

// GetSupplyBreakdown returns the token distribution breakdown
func (c *Config) GetSupplyBreakdown() map[string]interface{} {
	return map[string]interface{}{
		"total_supply": c.Economics.TotalSupply,
		"distribution": map[string]interface{}{
			"genesis": map[string]interface{}{
				"amount":     c.Economics.GenesisDistribution,
				"percentage": 20.0,
				"purpose":    "Initial circulation and early adopters",
			},
			"validator_rewards": map[string]interface{}{
				"amount":     c.Economics.ValidatorRewardPool,
				"percentage": 50.0,
				"purpose":    "Staking rewards distributed over time",
			},
			"liquidity": map[string]interface{}{
				"amount":     c.Economics.LiquidityPool,
				"percentage": 20.0,
				"purpose":    "DEX liquidity and market making",
			},
			"development": map[string]interface{}{
				"amount":     c.Economics.DevelopmentPool,
				"percentage": 10.0,
				"purpose":    "Team and ecosystem development",
			},
		},
		"validator_economics": map[string]interface{}{
			"min_validator_stake":      c.Staking.MinValidatorStake,
			"max_validators":           c.Consensus.MaxValidators,
			"total_validator_capacity": c.Staking.MinValidatorStake * int64(c.Consensus.MaxValidators),
			"staking_accessibility": map[string]int64{
				"min_delegation": c.Economics.MinDelegation,
				"min_stake":      c.Economics.MinStake,
			},
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

	return nil
}

// GetEconomicSummary returns a human-readable summary of the economics
func (c *Config) GetEconomicSummary() string {
	return fmt.Sprintf(`
THRYLOS Token Economics Summary:
================================
Total Supply: %d THRYLOS (100 Million)
Genesis Supply: %d THRYLOS (20 Million, 20%%)

Distribution:
- Genesis/Initial: %d THRYLOS (20%%)
- Validator Rewards: %d THRYLOS (50%%)
- Liquidity Pool: %d THRYLOS (20%%)
- Development: %d THRYLOS (10%%)

Staking Requirements:
- Validator Stake: %d THRYLOS (32 THRYLOS)
- Minimum Delegation: %.1f THRYLOS
- Minimum Stake: %d THRYLOS

Transaction Costs:
- Standard Transaction: ~%.3f THRYLOS
- Gas Price: %.6f THRYLOS per gas unit

Rewards:
- Block Reward: %.2f THRYLOS
- Validator APR: %.1f%%
- Delegator APR: %.1f%%
- Inflation Rate: %.1f%%`,
		c.Economics.TotalSupply,
		c.Economics.GenesisSupply,
		c.Economics.GenesisDistribution,
		c.Economics.ValidatorRewardPool,
		c.Economics.LiquidityPool,
		c.Economics.DevelopmentPool,
		c.Staking.MinValidatorStake,
		float64(c.Economics.MinDelegation)/float64(BaseUnit),
		c.Economics.MinStake,
		float64(c.Economics.MinimumFee)/float64(BaseUnit),
		float64(c.Economics.BaseGasPrice)/float64(BaseUnit),
		float64(c.Economics.BlockReward)/float64(BaseUnit),
		c.Economics.ValidatorRewardRate*100,
		c.Economics.DelegatorRewardRate*100,
		c.Economics.InflationRate*100,
	)
}
