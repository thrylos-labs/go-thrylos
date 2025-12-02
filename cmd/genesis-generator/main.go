package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/thrylos-labs/go-thrylos/config"
	"github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/crypto"
)

func main() {
	// CLI flags
	numValidators := flag.Int("n", 4, "Number of bootnode validators to generate")
	outputDir := flag.String("o", "./keys", "Directory to save private keys")
	genesisPath := flag.String("g", "./config/genesis.json", "Path to save genesis.json")
	flag.Parse()

	// Ensure output directory exists
	if err := os.MkdirAll(*outputDir, 0700); err != nil {
		log.Fatalf("Failed to create key directory: %v", err)
	}

	fmt.Printf("🔒 Generating %d secure validator keys...\n", *numValidators)

	var genesisAccounts []config.GenesisAccount
	var totalAllocated int64

	// Amount to allocate per bootnode (e.g., 5M tokens)
	// BaseUnit is 1e9, so 5,000,000 * 1e9
	stakeAmount := int64(5000000) * config.BaseUnit

	for i := 1; i <= *numValidators; i++ {
		// 1. Generate secure random private key
		privKey, err := crypto.NewPrivateKey()
		if err != nil {
			log.Fatalf("Failed to generate private key: %v", err)
		}

		// 2. Derive address
		pubKey := privKey.PublicKey()
		address, err := account.GenerateAddress(pubKey)
		if err != nil {
			log.Fatalf("Failed to generate address: %v", err)
		}

		// 3. Save Private Key to disk (Hex encoded)
		// Format: validator_N.key
		keyFileName := fmt.Sprintf("validator_%d.key", i)
		keyPath := filepath.Join(*outputDir, keyFileName)

		// Get hex string of private key
		privKeyBytes := privKey.Bytes()
		privKeyHex := hex.EncodeToString(privKeyBytes)

		// Write with strict permissions (0600)
		if err := os.WriteFile(keyPath, []byte(privKeyHex), 0600); err != nil {
			log.Fatalf("Failed to write key file %s: %v", keyPath, err)
		}

		fmt.Printf("✅ Generated Validator %d: %s (Saved to %s)\n", i, address, keyPath)

		// 4. Add to Genesis Account list
		genesisAccounts = append(genesisAccounts, config.GenesisAccount{
			Address:      address,
			Balance:      stakeAmount,
			Purpose:      fmt.Sprintf("Bootnode %d Stake", i),
			Locked:       false,
			UnlockBlocks: 0,
		})

		totalAllocated += stakeAmount
	}

	// Add a foundation/reserve account (remainder of genesis supply)
	// For this example, we just generate a random cold wallet key too,
	// typically you'd put a specific hardware wallet address here manually later.
	foundationKey, _ := crypto.NewPrivateKey()
	foundationAddr, _ := account.GenerateAddress(foundationKey.PublicKey())
	foundationPath := filepath.Join(*outputDir, "foundation_cold.key")
	os.WriteFile(foundationPath, []byte(hex.EncodeToString(foundationKey.Bytes())), 0600)

	foundationAmount := config.GenesisSupply*config.BaseUnit - totalAllocated
	if foundationAmount > 0 {
		genesisAccounts = append(genesisAccounts, config.GenesisAccount{
			Address:      foundationAddr,
			Balance:      foundationAmount,
			Purpose:      "Foundation Reserve (Cold Storage)",
			Locked:       true,
			UnlockBlocks: 1000000,
		})
		fmt.Printf("🏦 Generated Foundation Reserve: %s (Saved to %s)\n", foundationAddr, foundationPath)
	}

	// 5. Construct Genesis Allocation
	genesis := config.GenesisAllocation{
		TotalGenesis: config.GenesisSupply * config.BaseUnit, // Ensure units match
		Accounts:     genesisAccounts,
	}

	// 6. Serialize and Save genesis.json
	genesisData, err := json.MarshalIndent(genesis, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal genesis JSON: %v", err)
	}

	// Ensure directory for genesis file exists
	genesisDir := filepath.Dir(*genesisPath)
	if err := os.MkdirAll(genesisDir, 0755); err != nil {
		log.Fatalf("Failed to create config directory: %v", err)
	}

	if err := os.WriteFile(*genesisPath, genesisData, 0644); err != nil {
		log.Fatalf("Failed to write genesis.json: %v", err)
	}

	fmt.Println("\n🎉 Generation Complete!")
	fmt.Printf("📄 Genesis file written to: %s\n", *genesisPath)
	fmt.Printf("🔑 Private keys saved in:   %s/\n", *outputDir)
	fmt.Println("⚠️  IMPORTANT: Back up the 'keys' directory securely. These keys are irrecoverable if lost.")
}
