package migration

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"

	"github.com/thrylos-labs/go-thrylos/api"
	"github.com/thrylos-labs/go-thrylos/core/state"
)

const conversionDoneKey = "points_conversion_done"

func ConvertPointsToThrylos(snapshotPath string, ratioThrylos float64, capNano *big.Int, ws *state.WorldState) error {
	// 1. Check not already run
	done, err := ws.GetMetadata(conversionDoneKey)
	if err != nil {
		return fmt.Errorf("failed to check conversion flag: %w", err)
	}
	if done == "true" {
		return fmt.Errorf("points conversion already completed — cannot run twice")
	}

	// 2. Load snapshot
	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		return fmt.Errorf("failed to read snapshot: %w", err)
	}
	var users map[string]*api.UserActivity
	if err := json.Unmarshal(data, &users); err != nil {
		return fmt.Errorf("failed to parse snapshot: %w", err)
	}

	// 3. Calculate allocations
	baseUnit := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	allocations := make(map[string]*big.Int)
	totalDistributed := big.NewInt(0)

	for addr, user := range users {
		if user.TotalPoints <= 0 {
			continue
		}
		thrylosAmount := new(big.Float).Mul(
			new(big.Float).SetFloat64(float64(user.TotalPoints)*ratioThrylos),
			new(big.Float).SetInt(baseUnit),
		)
		weiAmount, _ := thrylosAmount.Int(nil)
		allocations[addr] = weiAmount
		totalDistributed.Add(totalDistributed, weiAmount)
	}
	// 4. Enforce cap
	if totalDistributed.Cmp(capNano) > 0 {
		return fmt.Errorf("conversion would distribute %s nanoThrylos, exceeds cap of %s",
			totalDistributed.String(), capNano.String())
	}

	// 5. Credit accounts
	for addr, amount := range allocations {
		account, err := ws.GetAccount(addr)
		if err != nil {
			return fmt.Errorf("failed to get account %s: %w", addr, err)
		}
		balance, _ := new(big.Int).SetString(account.Balance, 10)
		if balance == nil {
			balance = big.NewInt(0)
		}
		account.Balance = new(big.Int).Add(balance, amount).String()
		if err := ws.UpdateAccountWithStorage(account); err != nil {
			return fmt.Errorf("failed to credit %s: %w", addr, err)
		}
	}

	// 6. Set one-time flag
	if err := ws.SetMetadata(conversionDoneKey, "true"); err != nil {
		return fmt.Errorf("failed to set conversion flag: %w", err)
	}

	return nil
}
