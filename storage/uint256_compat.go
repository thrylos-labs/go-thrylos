package storage

import (
	coremath "github.com/thrylos-labs/go-thrylos/core/math"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

func canonicalizeUint256(raw []byte) ([]byte, error) {
	value, err := coremath.ParseUint256Bytes(raw)
	if err != nil {
		return nil, err
	}

	return coremath.BigIntToUint256Bytes(value)
}

func canonicalizeUint256Map(raw map[string][]byte) (map[string][]byte, error) {
	if raw == nil {
		return make(map[string][]byte), nil
	}

	out := make(map[string][]byte, len(raw))
	for key, value := range raw {
		canonical, err := canonicalizeUint256(value)
		if err != nil {
			return nil, err
		}
		out[key] = canonical
	}

	return out, nil
}

func normalizeAccountCompat(account *core.Account) error {
	if account == nil {
		return nil
	}

	var err error
	account.Balance, err = canonicalizeUint256(account.Balance)
	if err != nil {
		return err
	}
	account.StakedAmount, err = canonicalizeUint256(account.StakedAmount)
	if err != nil {
		return err
	}
	account.Rewards, err = canonicalizeUint256(account.Rewards)
	if err != nil {
		return err
	}
	account.DelegatedTo, err = canonicalizeUint256Map(account.DelegatedTo)
	return err
}

func syncAccountCompatForWrite(account *core.Account) error {
	return normalizeAccountCompat(account)
}

func normalizeTransactionCompat(tx *core.Transaction) error {
	if tx == nil {
		return nil
	}

	var err error
	tx.Amount, err = canonicalizeUint256(tx.Amount)
	if err != nil {
		return err
	}
	tx.GasPrice, err = canonicalizeUint256(tx.GasPrice)
	return err
}

func syncTransactionCompatForWrite(tx *core.Transaction) error {
	return normalizeTransactionCompat(tx)
}

func normalizeValidatorCompat(validator *core.Validator) error {
	if validator == nil {
		return nil
	}

	var err error
	validator.Stake, err = canonicalizeUint256(validator.Stake)
	if err != nil {
		return err
	}
	validator.SelfStake, err = canonicalizeUint256(validator.SelfStake)
	if err != nil {
		return err
	}
	validator.DelegatedStake, err = canonicalizeUint256(validator.DelegatedStake)
	if err != nil {
		return err
	}
	validator.Delegators, err = canonicalizeUint256Map(validator.Delegators)
	return err
}

func syncValidatorCompatForWrite(validator *core.Validator) error {
	return normalizeValidatorCompat(validator)
}

func normalizeBlockCompat(block *core.Block) error {
	if block == nil {
		return nil
	}

	if block.Header != nil {
		var err error
		block.Header.TotalFees, err = canonicalizeUint256(block.Header.TotalFees)
		if err != nil {
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
	return normalizeBlockCompat(block)
}
