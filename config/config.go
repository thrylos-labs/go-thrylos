package config

import (
	"time"
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

	// API configuration
	API APIConfig `json:"api"`
}

type NetworkConfig struct {
	ListenAddr     string        `json:"listen_addr"`
	BootstrapPeers []string      `json:"bootstrap_peers"`
	MaxPeers       int           `json:"max_peers"`
	PingInterval   time.Duration `json:"ping_interval"`
}

type ConsensusConfig struct {
	BlockTime     time.Duration `json:"block_time"`
	MaxTxPerBlock int           `json:"max_tx_per_block"`
	MaxBlockSize  int64         `json:"max_block_size"`
	MinGasPrice   int64         `json:"min_gas_price"`
	MaxValidators int           `json:"max_validators"`
}

type StakingConfig struct {
	MinValidatorStake int64   `json:"min_validator_stake"`
	MinDelegation     int64   `json:"min_delegation"`
	MaxCommission     float64 `json:"max_commission"`
	InflationRate     float64 `json:"inflation_rate"`
	CommunityTax      float64 `json:"community_tax"`
}

type APIConfig struct {
	JSONRPCAddr   string `json:"jsonrpc_addr"`
	WebSocketAddr string `json:"websocket_addr"`
	EnableCORS    bool   `json:"enable_cors"`
}

// Load returns a default configuration
// TODO: Add file-based configuration loading
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
		},
		Consensus: ConsensusConfig{
			BlockTime:     3 * time.Second,
			MaxTxPerBlock: 1000,
			MaxBlockSize:  1024 * 1024, // 1MB
			MinGasPrice:   1000,
			MaxValidators: 100,
		},
		Staking: StakingConfig{
			MinValidatorStake: 1000000000000, // 1000 THRYLOS
			MinDelegation:     1000000000,    // 1 THRYLOS
			MaxCommission:     0.10,          // 10%
			InflationRate:     0.08,          // 8%
			CommunityTax:      0.02,          // 2%
		},
		API: APIConfig{
			JSONRPCAddr:   ":8545",
			WebSocketAddr: ":8546",
			EnableCORS:    true,
		},
	}, nil
}
