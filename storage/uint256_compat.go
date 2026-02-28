package storage

import (
	coremath "github.com/thrylos-labs/go-thrylos/core/math"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

func normalizeAccountCompat(account *core.Account) error {
	if account == nil {
		return nil
	}

	if err := coremath.NormalizeUint256Compat(&account.BalanceBytes, &account.Balance); err != nil {
		return err
	}
	if err := coremath.NormalizeUint256Compat(&account.StakedAmountBytes, &account.StakedAmount); err != nil {
		return err
	}
	if err := coremath.NormalizeUint256Compat(&account.RewardsBytes, &account.Rewards); err != nil {
		return err
	}

	delegatedToBytes, delegatedTo, err := coremath.NormalizeUint256MapCompat(account.DelegatedToBytes, account.DelegatedTo)
	if err != nil {
		return err
	}
	account.DelegatedToBytes = delegatedToBytes
	account.DelegatedTo = delegatedTo

	return nil
}

func syncAccountCompatForWrite(account *core.Account) error {
	if account == nil {
		return nil
	}

	if err := coremath.SyncUint256ForWrite(&account.BalanceBytes, &account.Balance); err != nil {
		return err
	}
	if err := coremath.SyncUint256ForWrite(&account.StakedAmountBytes, &account.StakedAmount); err != nil {
		return err
	}
	if err := coremath.SyncUint256ForWrite(&account.RewardsBytes, &account.Rewards); err != nil {
		return err
	}

	delegatedToBytes, delegatedTo, err := coremath.SyncUint256MapForWrite(account.DelegatedToBytes, account.DelegatedTo)
	if err != nil {
		return err
	}
	account.DelegatedToBytes = delegatedToBytes
	account.DelegatedTo = delegatedTo

	return nil
}

func normalizeTransactionCompat(tx *core.Transaction) error {
	if tx == nil {
		return nil
	}

	if err := coremath.NormalizeUint256Compat(&tx.AmountBytes, &tx.Amount); err != nil {
		return err
	}
	if err := coremath.NormalizeUint256Compat(&tx.GasPriceBytes, &tx.GasPrice); err != nil {
		return err
	}

	return nil
}

func syncTransactionCompatForWrite(tx *core.Transaction) error {
	if tx == nil {
		return nil
	}

	if err := coremath.SyncUint256ForWrite(&tx.AmountBytes, &tx.Amount); err != nil {
		return err
	}
	if err := coremath.SyncUint256ForWrite(&tx.GasPriceBytes, &tx.GasPrice); err != nil {
		return err
	}

	return nil
}

func normalizeValidatorCompat(validator *core.Validator) error {
	if validator == nil {
		return nil
	}

	if err := coremath.NormalizeUint256Compat(&validator.StakeBytes, &validator.Stake); err != nil {
		return err
	}
	if err := coremath.NormalizeUint256Compat(&validator.SelfStakeBytes, &validator.SelfStake); err != nil {
		return err
	}
	if err := coremath.NormalizeUint256Compat(&validator.DelegatedStakeBytes, &validator.DelegatedStake); err != nil {
		return err
	}

	delegatorsBytes, delegators, err := coremath.NormalizeUint256MapCompat(validator.DelegatorsBytes, validator.Delegators)
	if err != nil {
		return err
	}
	validator.DelegatorsBytes = delegatorsBytes
	validator.Delegators = delegators

	return nil
}

func syncValidatorCompatForWrite(validator *core.Validator) error {
	if validator == nil {
		return nil
	}

	if err := coremath.SyncUint256ForWrite(&validator.StakeBytes, &validator.Stake); err != nil {
		return err
	}
	if err := coremath.SyncUint256ForWrite(&validator.SelfStakeBytes, &validator.SelfStake); err != nil {
		return err
	}
	if err := coremath.SyncUint256ForWrite(&validator.DelegatedStakeBytes, &validator.DelegatedStake); err != nil {
		return err
	}

	delegatorsBytes, delegators, err := coremath.SyncUint256MapForWrite(validator.DelegatorsBytes, validator.Delegators)
	if err != nil {
		return err
	}
	validator.DelegatorsBytes = delegatorsBytes
	validator.Delegators = delegators

	return nil
}

func normalizeBlockCompat(block *core.Block) error {
	if block == nil {
		return nil
	}

	if block.Header != nil {
		if err := coremath.NormalizeUint256Compat(&block.Header.TotalFeesBytes, &block.Header.TotalFees); err != nil {
			return err
		}
	}

	for _, tx := range block.Transactions {
		if err := normalizeTransactionCompat(tx); err != nil {
			return err
		}
	}

	return nil
}

func syncBlockCompatForWrite(block *core.Block) error {
	if block == nil {
		return nil
	}

	if block.Header != nil {
		if err := coremath.SyncUint256ForWrite(&block.Header.TotalFeesBytes, &block.Header.TotalFees); err != nil {
			return err
		}
	}

	for _, tx := range block.Transactions {
		if err := syncTransactionCompatForWrite(tx); err != nil {
			return err
		}
	}

	return nil
}
