package state

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/thrylos-labs/go-thrylos/core/math"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

const (
	stakeDomainValidatorPrefix = "ownership/validator/"
	stakeDomainMembersPrefix   = "ownership/domain/"
)

type stakeDomainMembers struct {
	Validators []string `json:"validators"`
}

func normalizeStakeDomainID(domainID, validatorAddr string) string {
	normalized := strings.TrimSpace(domainID)
	if normalized == "" {
		return validatorAddr
	}
	return normalized
}

func validatorStakeDomainKey(validatorAddr string) string {
	return stakeDomainValidatorPrefix + validatorAddr
}

func stakeDomainMembersKey(domainID string) string {
	return stakeDomainMembersPrefix + domainID
}

// AddValidatorWithStakeDomain adds a validator and records its ownership domain atomically.
func (ws *WorldState) AddValidatorWithStakeDomain(validator *core.Validator, domainID string) error {
	ws.validatorMu.Lock()
	defer ws.validatorMu.Unlock()

	if err := ws.addValidator(validator); err != nil {
		return err
	}

	if err := ws.setValidatorStakeDomainLocked(validator.Address, domainID); err != nil {
		delete(ws.validators, validator.Address)
		return err
	}

	return nil
}

// SetValidatorStakeDomain assigns a validator to an ownership-linked stake domain.
func (ws *WorldState) SetValidatorStakeDomain(validatorAddr, domainID string) error {
	ws.validatorMu.Lock()
	defer ws.validatorMu.Unlock()

	return ws.setValidatorStakeDomainLocked(validatorAddr, domainID)
}

// GetValidatorStakeDomain returns the validator's effective ownership domain.
func (ws *WorldState) GetValidatorStakeDomain(validatorAddr string) (string, error) {
	ws.validatorMu.RLock()
	defer ws.validatorMu.RUnlock()

	return ws.getValidatorStakeDomainLocked(validatorAddr)
}

// GetStakeDomainValidators returns all validators currently linked to a stake domain.
func (ws *WorldState) GetStakeDomainValidators(domainID string) ([]string, error) {
	ws.validatorMu.RLock()
	defer ws.validatorMu.RUnlock()

	return ws.getStakeDomainValidatorsLocked(strings.TrimSpace(domainID))
}

// GetStakeDomainTotalStake returns the aggregate validator stake for a domain.
func (ws *WorldState) GetStakeDomainTotalStake(domainID string) (*big.Int, error) {
	ws.validatorMu.RLock()
	defer ws.validatorMu.RUnlock()

	validators, err := ws.getStakeDomainValidatorsLocked(strings.TrimSpace(domainID))
	if err != nil {
		return nil, err
	}

	total := big.NewInt(0)
	for _, validatorAddr := range validators {
		validator, exists := ws.validators[validatorAddr]
		if !exists || validator == nil {
			continue
		}
		total.Add(total, math.ParseBigInt(validator.Stake))
	}

	return total, nil
}

func (ws *WorldState) setValidatorStakeDomainLocked(validatorAddr, domainID string) error {
	if strings.TrimSpace(validatorAddr) == "" {
		return fmt.Errorf("validator address cannot be empty")
	}

	newDomainID := normalizeStakeDomainID(domainID, validatorAddr)
	currentDomainID, err := ws.getValidatorStakeDomainLocked(validatorAddr)
	if err != nil {
		return err
	}

	if currentDomainID == newDomainID {
		// Clear persisted metadata when the validator falls back to its implicit self-domain.
		if newDomainID == validatorAddr {
			return ws.state.SetMetadata(validatorStakeDomainKey(validatorAddr), "")
		}
		return nil
	}

	if currentDomainID != "" && currentDomainID != validatorAddr {
		members, err := ws.readStakeDomainMembersLocked(currentDomainID)
		if err != nil {
			return err
		}
		members.Validators = removeString(members.Validators, validatorAddr)
		if err := ws.writeStakeDomainMembersLocked(currentDomainID, members); err != nil {
			return err
		}
	}

	if newDomainID == validatorAddr {
		return ws.state.SetMetadata(validatorStakeDomainKey(validatorAddr), "")
	}

	if err := ws.state.SetMetadata(validatorStakeDomainKey(validatorAddr), newDomainID); err != nil {
		return err
	}

	members, err := ws.readStakeDomainMembersLocked(newDomainID)
	if err != nil {
		return err
	}
	if !containsString(members.Validators, validatorAddr) {
		members.Validators = append(members.Validators, validatorAddr)
		sort.Strings(members.Validators)
	}

	return ws.writeStakeDomainMembersLocked(newDomainID, members)
}

func (ws *WorldState) getValidatorStakeDomainLocked(validatorAddr string) (string, error) {
	customDomainID, err := ws.state.GetMetadata(validatorStakeDomainKey(validatorAddr))
	if err != nil {
		return "", err
	}
	return normalizeStakeDomainID(customDomainID, validatorAddr), nil
}

func (ws *WorldState) getStakeDomainValidatorsLocked(domainID string) ([]string, error) {
	if domainID == "" {
		return nil, fmt.Errorf("stake domain cannot be empty")
	}

	members, err := ws.readStakeDomainMembersLocked(domainID)
	if err != nil {
		return nil, err
	}

	if len(members.Validators) == 0 {
		if _, exists := ws.validators[domainID]; exists {
			return []string{domainID}, nil
		}
		return []string{}, nil
	}

	filtered := make([]string, 0, len(members.Validators))
	for _, validatorAddr := range members.Validators {
		if _, exists := ws.validators[validatorAddr]; exists {
			filtered = append(filtered, validatorAddr)
		}
	}

	if len(filtered) != len(members.Validators) {
		members.Validators = filtered
		if err := ws.writeStakeDomainMembersLocked(domainID, members); err != nil {
			return nil, err
		}
	}

	return filtered, nil
}

func (ws *WorldState) readStakeDomainMembersLocked(domainID string) (*stakeDomainMembers, error) {
	raw, err := ws.state.GetMetadata(stakeDomainMembersKey(domainID))
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return &stakeDomainMembers{Validators: []string{}}, nil
	}

	var members stakeDomainMembers
	if err := json.Unmarshal([]byte(raw), &members); err != nil {
		return nil, fmt.Errorf("failed to decode stake domain %s: %w", domainID, err)
	}
	if members.Validators == nil {
		members.Validators = []string{}
	}

	return &members, nil
}

func (ws *WorldState) writeStakeDomainMembersLocked(domainID string, members *stakeDomainMembers) error {
	data, err := json.Marshal(members)
	if err != nil {
		return fmt.Errorf("failed to encode stake domain %s: %w", domainID, err)
	}
	return ws.state.SetMetadata(stakeDomainMembersKey(domainID), string(data))
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func removeString(values []string, target string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if value != target {
			filtered = append(filtered, value)
		}
	}
	return filtered
}
