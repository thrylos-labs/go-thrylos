package config

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/thrylos-labs/go-thrylos/core/math"
)

var (
	// === TOKEN ECONOMICS (18 Decimals) ===

	// Chain IDs
	MainnetChainID = "thrylos-1"
	TestnetChainID = "thrylos-testnet-1"
	DevnetChainID  = "thrylos-devnet-1337"

	// Base Unit (1 THRYLOS = 10^18 Wei)
	BaseUnit = new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)

	// Total Supply: 100 Million THRYLOS
	TotalSupply = new(big.Int).Mul(big.NewInt(100_000_000), BaseUnit)

	// Genesis Supply: 15 Million THRYLOS (15%)
	GenesisSupply = new(big.Int).Mul(big.NewInt(15_000_000), BaseUnit)

	// Economic Thresholds
	MinimumBalance     = new(big.Int).Div(BaseUnit, big.NewInt(1000)) // 0.001 THRYLOS
	MinimumTransfer    = new(big.Int).Div(BaseUnit, big.NewInt(100))  // 0.01 THRYLOS
	MinimumStakeAmount = new(big.Int).Mul(big.NewInt(1), BaseUnit)    // 1 THRYLOS
	MinimumDelegation  = new(big.Int).Div(BaseUnit, big.NewInt(10))   // 0.1 THRYLOS

	// Validator Entry: 2500 THRYLOS
	MinimumValidatorStake = new(big.Int).Mul(big.NewInt(2500), BaseUnit)

	// Gas Economics
	BaseGasPrice   = big.NewInt(10)    // 10 Wei (Low fees)
	MaxGasPerBlock = int64(30_000_000) // Standard Ethereum block gas limit
	StandardTxGas  = int64(21_000)
	StakingTxGas   = int64(50_000)

	MaxTransactionsPerBlock = 1000
	MaxBlockSize            = int64(2 * 1024 * 1024) // 2MB

	// Rewards (calculated in init)
	BlockReward        *big.Int
	ValidatorReward    *big.Int
	DelegatorReward    *big.Int
	TransactionPoolTTL = 24 * time.Hour // Max time a tx stays in the pool before expiry
)

func GetChainIDForEnvironment(env string) string {
	switch env {
	case "mainnet", "production":
		return MainnetChainID
	case "testnet":
		return TestnetChainID
	case "devnet", "development", "local":
		return DevnetChainID
	default:
		return DevnetChainID // Default to devnet for safety
	}
}

// GenesisAccount represents an initial account with balance
type GenesisAccount struct {
	Address      string `json:"address"`
	Balance      string `json:"balance"` // ⚠️ CHANGE THIS from int64 to string
	Purpose      string `json:"purpose"`
	Locked       bool   `json:"locked"`
	UnlockBlocks int64  `json:"unlock_blocks"`
}

// GenesisAllocation represents the genesis token allocation
type GenesisAllocation struct {
	TotalGenesis     string           `json:"total_genesis"`
	GenesisTimestamp int64            `json:"genesis_timestamp"`
	Accounts         []GenesisAccount `json:"accounts"`
}

type PointsConfig struct {
	ConversionRatio float64 `json:"conversion_ratio"`
	ConversionCap   string  `json:"conversion_cap"`
	SnapshotPath    string  `json:"snapshot_path"`
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

	// Governance configuration for parameter changes and ownership-linked stake domains
	Governance GovernanceConfig `json:"governance"`

	// Points config
	Points PointsConfig `json:"points"`

	// Sharding configuration
	Sharding ShardingConfig `json:"sharding"`

	// API configuration
	API APIConfig `json:"api"`

	// P2P networking configuration
	P2P P2PConfig `json:"p2p" yaml:"p2p"`

	// Environment (mainnet, testnet, devnet, production, development)
	Environment string `json:"environment"`

	// Validator key configuration (used by production entrypoint)
	Validator ValidatorKeyConfig `json:"validator"`

	GenesisTimestamp int64 `json:"genesis_timestamp"`
}

// P2PConfig represents P2P networking configuration
type P2PConfig struct {
	Enabled        bool     `json:"enabled" yaml:"enabled"`
	ListenPort     int      `json:"listen_port" yaml:"listen_port"`
	BootstrapPeers []string `json:"bootstrap_peers" yaml:"bootstrap_peers"`
	MaxPeers       int      `json:"max_peers" yaml:"max_peers"`
	EnableMDNS     bool     `json:"enable_mdns" yaml:"enable_mdns"`
	EnableDHT      bool     `json:"enable_dht" yaml:"enable_dht"`
	// Message validation
	MaxMessageSize     int64         `json:"max_message_size" yaml:"max_message_size"`         // 10MB default
	MaxBlockRangeSize  int           `json:"max_block_range_size" yaml:"max_block_range_size"` // 100 blocks
	StreamReadTimeout  time.Duration `json:"stream_read_timeout" yaml:"stream_read_timeout"`   // 30s
	StreamWriteTimeout time.Duration `json:"stream_write_timeout" yaml:"stream_write_timeout"` // 30s
	RequestRateLimit   int           `json:"request_rate_limit" yaml:"request_rate_limit"`     // requests/min per peer
	MaxPendingRequests int           `json:"max_pending_requests" yaml:"max_pending_requests"` // 100 per peer
}

type NetworkConfig struct {
	ListenAddr     string        `json:"listen_addr"`
	BootstrapPeers []string      `json:"bootstrap_peers"`
	MaxPeers       int           `json:"max_peers"`
	PingInterval   time.Duration `json:"ping_interval"`
	NetworkID      string        `json:"network_id"`
	ChainID        string        `json:"chain_id"`
}

type ConsensusConfig struct {
	BlockTime           time.Duration `json:"block_time"`
	MaxTxPerBlock       int           `json:"max_tx_per_block"`
	MaxBlockSize        int64         `json:"max_block_size"`
	MinGasPrice         string        `json:"min_gas_price"`
	MaxValidators       int           `json:"max_validators"`
	MinActiveValidators int           `json:"min_active_validators"`
	ValidatorRotation   time.Duration `json:"validator_rotation"`
	SlashingEnabled     bool          `json:"slashing_enabled"`
	MaxFutureBlockTime  time.Duration `json:"max_future_block_time"`
	MaxPastBlockTime    time.Duration `json:"max_past_block_time"`
	MaxBlockTimeDrift   time.Duration `json:"max_block_time_drift"`
	MaxTimestampSkew    time.Duration `json:"max_timestamp_skew"`
	MaxTimestampAge     time.Duration `json:"max_timestamp_age"`
	MaxTxDataSize       int           `json:"max_tx_data_size"`
	StakeCacheTTL       time.Duration `json:"stake_cache_ttl"`

	SlashingDoubleVote      int     `json:"slashing_double_vote"`
	SlashingSurroundVote    int     `json:"slashing_surround_vote"`
	SlashingInvalidProposal int     `json:"slashing_invalid_proposal"`
	SlashingDowntime        int     `json:"slashing_downtime"`
	SlashingInvalidSig      int     `json:"slashing_invalid_sig"`
	MaxMissedAttestations   uint64  `json:"max_missed_attestations"`
	JailDurationHours       int     `json:"jail_duration_hours"`
	MaxReorgDepth           int     `json:"max_reorg_depth"`
	FinalizationEpochs      int     `json:"finalization_epochs"`
	MinStakeForReorg        float64 `json:"min_stake_for_reorg"`
	CheckpointInterval      int     `json:"checkpoint_interval"`
}

type StakingConfig struct {
	MinValidatorStake          string        `json:"min_validator_stake"`
	MinDelegation              string        `json:"min_delegation"`
	MinSelfStake               string        `json:"min_self_stake"`
	MaxValidatorStake          string        `yaml:"max_validator_stake"`  // e.g., "10000000000000000000000000" (10M tokens)
	MaxStakePercentage         float64       `yaml:"max_stake_percentage"` // e.g., 0.15 (15%)
	UnbondingPeriod            time.Duration `yaml:"unbonding_period"`     // e.g., 604800s (7 days)
	MaxCommission              float64       `json:"max_commission"`
	CommissionChangeMax        float64       `json:"commission_change_max"`
	UnbondingTime              time.Duration `json:"unbonding_time"`
	MaxDelegationsPerValidator int           `json:"max_delegations_per_validator"`
	SlashFractionDoubleSign    float64       `json:"slash_fraction_double_sign"`
	SlashFractionDowntime      float64       `json:"slash_fraction_downtime"`
	DowntimeJailDuration       time.Duration `json:"downtime_jail_duration"`
	MinSignedPerWindow         float64       `json:"min_signed_per_window"`
	SignedBlocksWindow         int64         `json:"signed_blocks_window"`
	MaxSlashingEvents          int           `json:"max_slashing_events"`     // Default: 3
	MinStakeRetention          float64       `json:"min_stake_retention"`     // Default: 0.5 (50%)
	AutoRemoveOnDoubleSign     bool          `json:"auto_remove_double_sign"` // Default: true
}

type EconomicsConfig struct {
	TotalSupply       string `json:"total_supply"`       // ⚠️ CHANGE THIS from int64 to string
	GenesisSupply     string `json:"genesis_supply"`     // ⚠️ CHANGE THIS from int64 to string
	CirculatingSupply string `json:"circulating_supply"` // ⚠️ CHANGE THIS from int64 to string

	InflationRate float64 `json:"inflation_rate"`
	InflationMax  float64 `json:"inflation_max"`
	InflationMin  float64 `json:"inflation_min"`
	GoalBonded    float64 `json:"goal_bonded"`

	BaseGasPrice string `json:"base_gas_price"` // ⚠️ CHANGE THIS
	MinimumFee   string `json:"minimum_fee"`    // ⚠️ CHANGE THIS

	MinGasLimit int64  `json:"min_gas_limit"`
	MaxGasPerTx int64  `json:"max_gas_per_tx"`
	MaxGasPrice string `json:"max_gas_price"` // ⚠️ CHANGE THIS
	MaxBlockGas int64  `json:"max_block_gas"`

	BlockReward         string  `json:"block_reward"` // ⚠️ CHANGE THIS
	CommunityTax        float64 `json:"community_tax"`
	BaseProposerReward  float64 `json:"base_proposer_reward"`
	BonusProposerReward float64 `json:"bonus_proposer_reward"`

	ValidatorRewardRate float64 `json:"validator_reward_rate"`
	DelegatorRewardRate float64 `json:"delegator_reward_rate"`

	MinBalance    string `json:"min_balance"`    // ⚠️ CHANGE THIS
	MinTransfer   string `json:"min_transfer"`   // ⚠️ CHANGE THIS
	MinStake      string `json:"min_stake"`      // ⚠️ CHANGE THIS
	MinDelegation string `json:"min_delegation"` // ⚠️ CHANGE THIS

	GenesisDistribution string `json:"genesis_distribution"`  // ⚠️ CHANGE THIS
	ValidatorRewardPool string `json:"validator_reward_pool"` // ⚠️ CHANGE THIS
	LiquidityPool       string `json:"liquidity_pool"`        // ⚠️ CHANGE THIS
	DevelopmentPool     string `json:"development_pool"`      // ⚠️ CHANGE THIS
}

type GovernanceConfig struct {
	Enabled                 bool          `json:"enabled"`
	VotingPeriod            time.Duration `json:"voting_period"`
	Quorum                  float64       `json:"quorum"`
	ApprovalThreshold       float64       `json:"approval_threshold"`
	OwnershipDomainsEnabled bool          `json:"ownership_domains_enabled"`
}

type ShardingConfig struct {
	EnableSharding         bool          `json:"enable_sharding"`
	TotalShards            int           `json:"total_shards"`
	BeaconShardID          int           `json:"beacon_shard_id"`
	CrossShardEnabled      bool          `json:"cross_shard_enabled"`
	ShardRebalanceInterval time.Duration `json:"shard_rebalance_interval"`
}

// config/config.go

type APIConfig struct {
	// REST API configuration
	EnableAPI bool   `json:"enable_api"` // Whether to enable the API server
	RESTAddr  string `json:"rest_addr"`  // REST API address (e.g., ":8080")
	EnableTLS bool   `json:"enable_tls"` // Enable HTTPS/TLS
	CertFile  string `json:"cert_file"`  // TLS certificate file path
	KeyFile   string `json:"key_file"`   // TLS private key file path

	// API settings
	EnableCORS     bool     `json:"enable_cors"`
	AllowedOrigins []string `json:"allowed_origins"` // [FIX] Added missing field
	RateLimit      int      `json:"rate_limit"`
	EnableMetrics  bool     `json:"enable_metrics"`

	// Faucet / funding endpoint
	EnableFaucet bool `json:"enable_faucet"`

	MaxRequestSize int64 `yaml:"max_request_size"`
}

// ValidatorKeyConfig controls how the node loads its validator key in production.
type ValidatorKeyConfig struct {
	Enabled     bool   `json:"enabled"`
	KeyFilePath string `json:"key_file_path"`
}

func DefaultConfig() *Config {
	return &Config{
		NodeID:      "thrylos-v2-node",
		DataDir:     "./data",
		LogLevel:    "info",
		Environment: "development",

		Validator: ValidatorKeyConfig{
			Enabled:     false,
			KeyFilePath: "",
		},

		Genesis: GenesisAllocation{
			TotalGenesis: math.BigIntToString(GenesisSupply),
			Accounts:     []GenesisAccount{},
		},

		Network: NetworkConfig{
			ListenAddr:     "/ip4/0.0.0.0/tcp/9000",
			BootstrapPeers: []string{},
			MaxPeers:       50,
			PingInterval:   30 * time.Second,
			NetworkID:      "testnet",
			ChainID:        TestnetChainID,
		},

		Consensus: ConsensusConfig{
			BlockTime:           3 * time.Second,
			MaxTxPerBlock:       1000,
			MaxBlockSize:        2 * 1024 * 1024,
			MinGasPrice:         math.BigIntToString(BaseGasPrice), // FIX
			MaxValidators:       100,
			MinActiveValidators: 1,
			ValidatorRotation:   24 * time.Hour,
			MaxFutureBlockTime:  5 * time.Second,
			MaxPastBlockTime:    2 * time.Hour,
			MaxBlockTimeDrift:   10 * time.Minute,
			MaxTimestampSkew:    5 * time.Minute,
			MaxTimestampAge:     1 * time.Hour,
			MaxTxDataSize:       1024 * 1024,
			StakeCacheTTL:       30 * time.Second,
			SlashingEnabled:     true,

			SlashingDoubleVote:      50,
			SlashingSurroundVote:    30,
			SlashingInvalidProposal: 20,
			SlashingInvalidSig:      10,
			SlashingDowntime:        1,
			MaxMissedAttestations:   28800,
			JailDurationHours:       1,
		},

		Staking: StakingConfig{
			MinValidatorStake:          math.BigIntToString(MinimumValidatorStake), // FIX
			MinDelegation:              math.BigIntToString(MinimumDelegation),     // FIX
			MinSelfStake:               math.BigIntToString(new(big.Int).Div(MinimumValidatorStake, big.NewInt(10))),
			MaxCommission:              0.20,
			CommissionChangeMax:        0.01,
			UnbondingTime:              21 * 24 * time.Hour,
			UnbondingPeriod:            7 * 24 * time.Hour,
			MaxDelegationsPerValidator: 1000,
			SlashFractionDoubleSign:    0.05,
			SlashFractionDowntime:      0.001,
			DowntimeJailDuration:       10 * time.Minute,
			MinSignedPerWindow:         0.05,
			SignedBlocksWindow:         30000,
		},

		Points: PointsConfig{
			ConversionRatio: 0.001,                         // 1000 points → 1 THRYLOS
			ConversionCap:   "100000000000000000000000000", // 100M THRYLOS in Wei
			SnapshotPath:    "",
		},

		Economics: EconomicsConfig{
			TotalSupply:         math.BigIntToString(TotalSupply),
			GenesisSupply:       math.BigIntToString(GenesisSupply),
			CirculatingSupply:   math.BigIntToString(GenesisSupply),
			InflationRate:       0.04,
			InflationMax:        0.07,
			InflationMin:        0.02,
			GoalBonded:          0.70,
			BaseGasPrice:        math.BigIntToString(BaseGasPrice),
			MinimumFee:          math.BigIntToString(new(big.Int).Mul(BaseGasPrice, big.NewInt(StandardTxGas))),
			MinGasLimit:         StandardTxGas,
			MaxGasPerTx:         2000000,
			MaxGasPrice:         "10000", // Needs adjustment if using BigInt
			MaxBlockGas:         MaxGasPerBlock,
			BlockReward:         math.BigIntToString(BlockReward),
			CommunityTax:        0.03,
			BaseProposerReward:  0.015,
			BonusProposerReward: 0.035,
			ValidatorRewardRate: 0.12,
			DelegatorRewardRate: 0.08,
			MinBalance:          math.BigIntToString(MinimumBalance),
			MinTransfer:         math.BigIntToString(MinimumTransfer),
			MinStake:            math.BigIntToString(MinimumStakeAmount),
			MinDelegation:       math.BigIntToString(MinimumDelegation),

			// Percentages of Total Supply
			GenesisDistribution: math.BigIntToString(new(big.Int).Div(new(big.Int).Mul(TotalSupply, big.NewInt(15)), big.NewInt(100))),
			ValidatorRewardPool: math.BigIntToString(new(big.Int).Div(new(big.Int).Mul(TotalSupply, big.NewInt(60)), big.NewInt(100))),
			LiquidityPool:       math.BigIntToString(new(big.Int).Div(new(big.Int).Mul(TotalSupply, big.NewInt(15)), big.NewInt(100))),
			DevelopmentPool:     math.BigIntToString(new(big.Int).Div(new(big.Int).Mul(TotalSupply, big.NewInt(10)), big.NewInt(100))),
		},

		Governance: GovernanceConfig{
			Enabled:                 true,
			VotingPeriod:            72 * time.Hour,
			Quorum:                  0.33,
			ApprovalThreshold:       0.67,
			OwnershipDomainsEnabled: true,
		},

		Sharding: ShardingConfig{
			EnableSharding:         true,
			TotalShards:            4,
			BeaconShardID:          -1,
			CrossShardEnabled:      true,
			ShardRebalanceInterval: 24 * time.Hour,
		},

		API: APIConfig{
			EnableAPI:      true,
			RESTAddr:       ":8080",
			EnableTLS:      false,
			CertFile:       "",
			KeyFile:        "",
			EnableCORS:     true,
			AllowedOrigins: []string{"*"},
			RateLimit:      10,
			EnableMetrics:  true,
			EnableFaucet:   true,
		},

		P2P: P2PConfig{
			Enabled:            true,
			ListenPort:         9000,
			BootstrapPeers:     []string{},
			MaxPeers:           50,
			EnableMDNS:         false,
			EnableDHT:          true,
			MaxMessageSize:     2 * 1024 * 1024,
			MaxBlockRangeSize:  20,
			StreamReadTimeout:  30 * time.Second,
			StreamWriteTimeout: 30 * time.Second,
			RequestRateLimit:   20,
			MaxPendingRequests: 20,
		},
	}
}

func Load() (*Config, error) {
	cfg := DefaultConfig()

	// Load Genesis
	genesisFile := "genesis.json"
	if _, err := os.Stat(genesisFile); os.IsNotExist(err) {
		genesisFile = "config/genesis.json"
	}

	// Attempt to load
	if err := loadGenesisFromFile(genesisFile, cfg); err != nil {
		// Log warning but fall back to safe defaults if file is missing/corrupt
		fmt.Printf("⚠️ Warning: Could not load genesis file: %v. Using internal defaults.\n", err)
		cfg.Genesis = DefaultGenesisAllocation()
	} else {
		// [FIX] Validate Critical Consensus Parameters
		// This ensures the loaded genesis.json matches the compiled binary's economic rules
		validateGenesisConsistency(cfg)
	}

	sanitizeConfigForEnvironment(cfg)
	return cfg, nil
}

// [FIX] New helper to return the canonical genesis allocation based on hardcoded constants
func DefaultGenesisAllocation() GenesisAllocation {
	return GenesisAllocation{
		TotalGenesis: math.BigIntToString(GenesisSupply),
		Accounts:     []GenesisAccount{}, // Empty by default, filled by gen-genesis tool usually
	}
}

// [FIX] Strict validation to prevent Mainnet forks due to config mismatch
func validateGenesisConsistency(cfg *Config) {
	// Parse loaded genesis total
	loadedTotal := math.ParseBigInt(cfg.Genesis.TotalGenesis)

	// Compare with hardcoded GenesisSupply
	if loadedTotal.Cmp(GenesisSupply) != 0 {
		panic(fmt.Sprintf("❌ CRITICAL CONFIG ERROR: Genesis file supply (%s) does not match hardcoded protocol rule (%s). "+
			"You must update config/genesis.json to match config.go constants.",
			loadedTotal.String(), GenesisSupply.String()))
	}
}

// [FIX L-04] sanitizeConfigForEnvironment enforces security overrides for production
func sanitizeConfigForEnvironment(c *Config) {
	// Check OS environment variable first, fallback to config string
	env := os.Getenv("THRYLOS_ENVIRONMENT")
	if env == "" {
		env = c.Environment
	}
	// If still empty, assume production for safety
	if env == "" {
		env = "production"
	}

	// Normalize
	isProd := false
	switch env {
	case "production", "prod", "mainnet":
		isProd = true
	}

	if isProd {
		// 1. Force TLS for API if API is enabled
		// Production nodes should never expose cleartext HTTP APIs
		if c.API.EnableAPI && !c.API.EnableTLS {
			fmt.Println("🔒 SECURITY OVERRIDE: Enforcing TLS for API in production environment")
			c.API.EnableTLS = true

			// If certificates are missing, the node will fail to start (which is safer than running insecurely)
			if c.API.CertFile == "" {
				c.API.CertFile = "./server.crt"
			}
			if c.API.KeyFile == "" {
				c.API.KeyFile = "./server.key"
			}
		}

		// 2. Disable Faucet
		// Free money endpoints must never exist on mainnet
		if c.API.EnableFaucet {
			fmt.Println("🔒 SECURITY OVERRIDE: Disabling faucet in production environment")
			c.API.EnableFaucet = false
		}

		// 3. Enforce Slashing
		// Consensus security depends on penalties being active
		if !c.Consensus.SlashingEnabled {
			fmt.Println("🔒 SECURITY OVERRIDE: Enabling slashing logic in production environment")
			c.Consensus.SlashingEnabled = true
		}

		// 4. Enforce CORS restrictions
		// Wildcard origins are dangerous for wallets
		if c.API.EnableCORS && len(c.API.AllowedOrigins) > 0 && c.API.AllowedOrigins[0] == "*" {
			fmt.Println("🔒 SECURITY OVERRIDE: Removing wildcard CORS origin in production")
			c.API.AllowedOrigins = []string{} // Requires manual configuration of specific domains
		}
	}
	// docker-compose can enable TLS without requiring a production environment.
	if val := os.Getenv("ENABLE_TLS"); val == "true" {
		c.API.EnableTLS = true
	}
	if val := os.Getenv("CERT_FILE"); val != "" {
		c.API.CertFile = val
	}
	if val := os.Getenv("KEY_FILE"); val != "" {
		c.API.KeyFile = val
	}
} // ← existing closing brace

func loadGenesisFromFile(path string, cfg *Config) error {
	file, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var allocation GenesisAllocation
	if err := json.Unmarshal(file, &allocation); err != nil {
		return fmt.Errorf("invalid genesis.json format: %v", err)
	}

	// Validate that the file matches the expected supply
	// ✅ FIX: Use big.Int for the counter
	totalAllocated := big.NewInt(0)

	for _, acc := range allocation.Accounts {
		// ✅ FIX: Parse string balance to BigInt using helper
		bal := math.ParseBigInt(acc.Balance)
		totalAllocated.Add(totalAllocated, bal)
	}

	// ✅ FIX: Parse expected supply from config string
	expectedSupply := math.ParseBigInt(cfg.Economics.GenesisSupply)

	// ✅ FIX: Compare using Cmp() (Returns 0 if equal)
	// Note: We don't multiply by BaseUnit here because cfg.GenesisSupply
	// is ALREADY initialized as the full 18-decimal value in Load().
	if totalAllocated.Cmp(expectedSupply) != 0 {
		fmt.Printf("⚠️  Genesis allocation (%s) does not match Config Supply (%s)\n",
			totalAllocated.String(), expectedSupply.String())
	}

	cfg.Genesis = allocation
	// Propagate genesis_timestamp so all nodes use the same value for genesis block hashing
	if allocation.GenesisTimestamp != 0 {
		cfg.GenesisTimestamp = allocation.GenesisTimestamp
	}
	return nil
}

// GetGenesisAccounts returns the genesis account allocation
func (c *Config) GetGenesisAccounts() []GenesisAccount {
	return c.Genesis.Accounts
}

// GetMinimumBalances returns the minimum balance thresholds
func (c *Config) GetMinimumBalances() map[string]string {
	return map[string]string{
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
	// Helper to parse config strings to BigInt
	parse := func(s string) *big.Int {
		return math.ParseBigInt(s)
	}

	// 1. Calculate Total Validator Capacity (MinStake * MaxValidators)
	minValStake := parse(c.Staking.MinValidatorStake)
	maxVals := big.NewInt(int64(c.Consensus.MaxValidators))
	totalCapacity := new(big.Int).Mul(minValStake, maxVals)

	// 2. Calculate Genesis Details (Using BaseUnit which is *big.Int)
	// Example: 10M * BaseUnit
	immediateCirculation := new(big.Int).Mul(big.NewInt(10_000_000), BaseUnit)
	ecosystemBootstrap := new(big.Int).Mul(big.NewInt(3_000_000), BaseUnit)
	communityIncentives := new(big.Int).Mul(big.NewInt(2_000_000), BaseUnit)

	return map[string]interface{}{
		"total_supply": c.Economics.TotalSupply,
		"distribution": map[string]interface{}{
			"genesis": map[string]interface{}{
				"amount":     c.Economics.GenesisDistribution,
				"percentage": 15.0,
				"purpose":    "Public launch and early ecosystem bootstrap",
				"details": map[string]interface{}{
					"immediate_circulation": immediateCirculation.String(),
					"ecosystem_bootstrap":   ecosystemBootstrap.String(),
					"community_incentives":  communityIncentives.String(),
				},
			},
			"validator_rewards": map[string]interface{}{
				"amount":             c.Economics.ValidatorRewardPool,
				"percentage":         60.0,
				"purpose":            "Long-term staking rewards (sustainable model)",
				"distribution_years": 10,
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
			"total_validator_capacity": totalCapacity.String(), // ✅ Fixed: BigInt -> String
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
func (c *Config) GetGasConfig() map[string]string {
	return map[string]string{
		"base_price": c.Economics.BaseGasPrice,
		// Convert int64 constants to strings
		"standard_tx":   fmt.Sprintf("%d", StandardTxGas),
		"staking_tx":    fmt.Sprintf("%d", StakingTxGas),
		"minimum_fee":   c.Economics.MinimumFee,
		"max_per_block": fmt.Sprintf("%d", MaxGasPerBlock),
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
	// 1. Calculate blocks per year (3-second block time)
	blocksPerYear := int64(365 * 24 * 60 * 60 / 3)
	blocksPerYearBig := big.NewInt(blocksPerYear)

	// 2. Parse Block Reward (string -> BigInt)
	blockRewardBig := math.ParseBigInt(c.Economics.BlockReward)

	// 3. Calculate Annual Rewards (Reward * Blocks)
	annualBlockRewards := new(big.Int).Mul(blockRewardBig, blocksPerYearBig)

	// 4. Calculate Daily Rewards (Annual / 365)
	dailyBlockRewards := new(big.Int).Div(annualBlockRewards, big.NewInt(365))

	// 5. Calculate "Rewards in Thrylos" (Annual / BaseUnit) using Float for display
	fAnnual := new(big.Float).SetInt(annualBlockRewards)
	fBase := new(big.Float).SetInt(BaseUnit) // BaseUnit is *big.Int global
	rewardsInThrylos, _ := new(big.Float).Quo(fAnnual, fBase).Float64()

	// 6. Calculate Inflation (Annual Rewards / Total Supply)
	totalSupplyBig := math.ParseBigInt(c.Economics.TotalSupply)
	fTotalSupply := new(big.Float).SetInt(totalSupplyBig)

	inflationRate := 0.0
	if totalSupplyBig.Sign() > 0 {
		fInflation, _ := new(big.Float).Quo(fAnnual, fTotalSupply).Float64()
		inflationRate = fInflation
	}

	return map[string]interface{}{
		"blocks_per_year":        blocksPerYear,
		"annual_block_rewards":   annualBlockRewards.String(), // Return string to avoid overflow
		"daily_block_rewards":    dailyBlockRewards.String(),  // Return string
		"rewards_in_thrylos":     rewardsInThrylos,            // float64 (safe for display)
		"inflation_from_rewards": inflationRate,               // float64 (percentage)
	}
}

// ValidateConfig validates the configuration parameters
func (c *Config) ValidateConfig() error {
	// 1. Validate simple float parameters
	if c.Economics.InflationRate < 0 || c.Economics.InflationRate > 1 {
		return fmt.Errorf("inflation rate must be between 0 and 1")
	}

	if c.Staking.MaxCommission < 0 || c.Staking.MaxCommission > 1 {
		return fmt.Errorf("max commission must be between 0 and 1")
	}
	if c.Governance.Quorum < 0 || c.Governance.Quorum > 1 {
		return fmt.Errorf("governance quorum must be between 0 and 1")
	}
	if c.Governance.ApprovalThreshold < 0 || c.Governance.ApprovalThreshold > 1 {
		return fmt.Errorf("governance approval threshold must be between 0 and 1")
	}
	if c.Governance.VotingPeriod < 0 {
		return fmt.Errorf("governance voting period cannot be negative")
	}

	// 2. Validate BigInt comparisons (MinDelegation vs MinValidatorStake)
	// Parse strings to BigInt
	minDelegation := math.ParseBigInt(c.Economics.MinDelegation)
	minValidatorStake := math.ParseBigInt(c.Staking.MinValidatorStake)

	// Check: MinDelegation > MinValidatorStake ?
	if minDelegation.Cmp(minValidatorStake) > 0 {
		return fmt.Errorf("min delegation (%s) cannot exceed min validator stake (%s)",
			minDelegation.String(), minValidatorStake.String())
	}

	if c.Consensus.MaxValidators <= 0 {
		return fmt.Errorf("max validators must be positive")
	}
	if c.Consensus.MinActiveValidators <= 0 {
		return fmt.Errorf("min active validators must be positive")
	}
	if c.Consensus.MinActiveValidators > c.Consensus.MaxValidators {
		return fmt.Errorf("min active validators (%d) cannot exceed max validators (%d)",
			c.Consensus.MinActiveValidators, c.Consensus.MaxValidators)
	}

	if c.Sharding.TotalShards <= 0 {
		return fmt.Errorf("total shards must be positive")
	}

	// 3. Validate supply distribution adds up to TotalSupply
	// We must sum the BigInts manually
	totalDistribution := big.NewInt(0)
	totalDistribution.Add(totalDistribution, math.ParseBigInt(c.Economics.GenesisDistribution))
	totalDistribution.Add(totalDistribution, math.ParseBigInt(c.Economics.ValidatorRewardPool))
	totalDistribution.Add(totalDistribution, math.ParseBigInt(c.Economics.LiquidityPool))
	totalDistribution.Add(totalDistribution, math.ParseBigInt(c.Economics.DevelopmentPool))

	totalSupply := math.ParseBigInt(c.Economics.TotalSupply)

	// Check: totalDistribution != totalSupply ?
	if totalDistribution.Cmp(totalSupply) != 0 {
		return fmt.Errorf("distribution pools (%s) don't equal total supply (%s)",
			totalDistribution.String(), totalSupply.String())
	}

	// 4. Validate genesis accounts total matches GenesisSupply
	// Use 'c.Genesis.Accounts' (not 'allocation')
	totalAllocated := big.NewInt(0)
	for _, account := range c.Genesis.Accounts {
		bal := math.ParseBigInt(account.Balance)
		totalAllocated.Add(totalAllocated, bal)
	}

	genesisSupply := math.ParseBigInt(c.Economics.GenesisSupply)

	// Check: totalAllocated != genesisSupply ?
	if totalAllocated.Cmp(genesisSupply) != 0 {
		return fmt.Errorf("genesis accounts total (%s) doesn't match genesis supply (%s)",
			totalAllocated.String(), genesisSupply.String())
	}

	return nil
}

// GetEconomicSummary returns a human-readable summary of the economics
func (c *Config) GetEconomicSummary() string {
	rewardCalc := c.CalculateBlockRewards()

	// Helper to convert Wei string to Thrylos float64
	toThrylos := func(weiStr string) float64 {
		val := math.ParseBigInt(weiStr)
		if val == nil {
			return 0.0
		}

		fVal := new(big.Float).SetInt(val)
		fBase := new(big.Float).SetInt(BaseUnit) // 10^18

		res, _ := new(big.Float).Quo(fVal, fBase).Float64()
		return res
	}

	// Helper for total validator capacity calculation
	calcValidatorCapacity := func() float64 {
		minStake := math.ParseBigInt(c.Staking.MinValidatorStake)
		maxVals := big.NewInt(int64(c.Consensus.MaxValidators))
		totalCapWei := new(big.Int).Mul(minStake, maxVals)

		fVal := new(big.Float).SetInt(totalCapWei)
		fBase := new(big.Float).SetInt(BaseUnit)
		res, _ := new(big.Float).Quo(fVal, fBase).Float64()
		return res
	}

	return fmt.Sprintf(`
THRYLOS Token Economics Summary (Optimized & Sustainable):
=========================================================
Total Supply: %.0f THRYLOS (100 Million)
Genesis Supply: %.0f THRYLOS (15 Million, 15%%) - REDUCED for sustainability

OPTIMIZED DISTRIBUTION (No Advisors):
- Genesis/Launch: %.0f THRYLOS (15%%) - Public + ecosystem bootstrap
- Validator Rewards: %.0f THRYLOS (60%%) - INCREASED for long-term sustainability  
- Liquidity Pool: %.0f THRYLOS (15%%) - DEX and market making
- Development: %.0f THRYLOS (10%%) - Core team (4-year vesting)

STAKING REQUIREMENTS (More Accessible):
- Validator Stake: %.0f THRYLOS (25 THRYLOS) - REDUCED from 34
- Minimum Delegation: %.3f THRYLOS
- Minimum Stake: %.0f THRYLOS
- Max Validators: %d (total capacity: %.0f THRYLOS)

TRANSACTION COSTS:
- Standard Transaction: ~%.6f THRYLOS
- Gas Price: %.9f THRYLOS per gas unit

REWARDS & SUSTAINABILITY:
- Block Reward: %.4f THRYLOS
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
✓ Reduced genesis supply (15%% vs 20%%) for better price stability
✓ Increased validator rewards (60%% vs 50%%) for long-term sustainability
✓ Lower validator requirements (25 vs 34 THRYLOS) for accessibility
✓ Higher staking APR (9%% validators, 7%% delegators) for participation
✓ No advisor allocation - community-focused distribution
✓ Gradual unlock mechanism for fair distribution`,
		toThrylos(c.Economics.TotalSupply),
		toThrylos(c.Economics.GenesisSupply),
		toThrylos(c.Economics.GenesisDistribution),
		toThrylos(c.Economics.ValidatorRewardPool),
		toThrylos(c.Economics.LiquidityPool),
		toThrylos(c.Economics.DevelopmentPool),
		toThrylos(c.Staking.MinValidatorStake),
		toThrylos(c.Economics.MinDelegation),
		toThrylos(c.Economics.MinStake),
		c.Consensus.MaxValidators,
		calcValidatorCapacity(),
		toThrylos(c.Economics.MinimumFee),
		toThrylos(c.Economics.BaseGasPrice),
		toThrylos(c.Economics.BlockReward),
		c.Economics.ValidatorRewardRate*100,
		c.Economics.DelegatorRewardRate*100,
		c.Economics.InflationRate*100,
		c.Economics.GoalBonded*100,
		rewardCalc["rewards_in_thrylos"].(float64),
		c.Economics.CommunityTax*100,
	)
}
