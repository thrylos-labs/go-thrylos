package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitizeConfigForEnvironment(t *testing.T) {
	// 1. Test Production Overrides
	t.Run("Production Security Overrides", func(t *testing.T) {
		// Setup unsafe config
		unsafeCfg := &Config{
			Environment: "production",
			API: APIConfig{
				EnableAPI:      true,
				EnableTLS:      false, // Unsafe
				EnableFaucet:   true,  // Unsafe
				EnableCORS:     true,
				AllowedOrigins: []string{"*"}, // Unsafe
			},
			Consensus: ConsensusConfig{
				SlashingEnabled: false, // Unsafe for mainnet
			},
		}

		// Apply sanitization
		sanitizeConfigForEnvironment(unsafeCfg)

		// Assert overrides applied
		assert.True(t, unsafeCfg.API.EnableTLS, "TLS should be enforced in production")
		assert.False(t, unsafeCfg.API.EnableFaucet, "Faucet should be disabled in production")
		assert.True(t, unsafeCfg.Consensus.SlashingEnabled, "Slashing should be enabled in production")
		assert.NotContains(t, unsafeCfg.API.AllowedOrigins, "*", "Wildcard CORS should be removed")
	})

	// 2. Test Development Flexibility
	t.Run("Development Allows Relaxed Config", func(t *testing.T) {
		// Setup dev config
		devCfg := &Config{
			Environment: "development",
			API: APIConfig{
				EnableAPI:    true,
				EnableTLS:    false, // Allowed in dev
				EnableFaucet: true,  // Allowed in dev
			},
		}

		// Apply sanitization
		sanitizeConfigForEnvironment(devCfg)

		// Assert settings remain
		assert.False(t, devCfg.API.EnableTLS, "TLS should not be forced in dev")
		assert.True(t, devCfg.API.EnableFaucet, "Faucet should remain enabled in dev")
	})

	// 3. Test Env Var Precedence
	t.Run("Environment Variable Precedence", func(t *testing.T) {
		os.Setenv("THRYLOS_ENVIRONMENT", "production")
		defer os.Unsetenv("THRYLOS_ENVIRONMENT")

		// Config says dev, but Env says Prod
		mixedCfg := &Config{
			Environment: "development",
			API: APIConfig{
				EnableFaucet: true,
			},
		}

		sanitizeConfigForEnvironment(mixedCfg)

		assert.False(t, mixedCfg.API.EnableFaucet, "Env var should override config file setting")
	})
}
