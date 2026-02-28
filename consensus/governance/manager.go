package governance

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/thrylos-labs/go-thrylos/config"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

const proposalMetadataPrefix = "governance/proposal/"

type ProposalStatus string

const (
	ProposalStatusActive   ProposalStatus = "active"
	ProposalStatusApproved ProposalStatus = "approved"
	ProposalStatusRejected ProposalStatus = "rejected"
)

type Vote struct {
	ValidatorAddress string `json:"validator_address"`
	DomainID         string `json:"domain_id"`
	Approve          bool   `json:"approve"`
	CastAt           int64  `json:"cast_at"`
}

type Proposal struct {
	ID            string           `json:"id"`
	Parameter     string           `json:"parameter"`
	ProposedValue string           `json:"proposed_value"`
	Proposer      string           `json:"proposer"`
	Status        ProposalStatus   `json:"status"`
	CreatedAt     int64            `json:"created_at"`
	VotingEndsAt  int64            `json:"voting_ends_at"`
	FinalizedAt   int64            `json:"finalized_at"`
	YesStake      string           `json:"yes_stake"`
	NoStake       string           `json:"no_stake"`
	Votes         map[string]*Vote `json:"votes"`
}

type ProposalPayload struct {
	Parameter     string `json:"parameter"`
	ProposedValue string `json:"proposed_value"`
}

type VotePayload struct {
	ProposalID string `json:"proposal_id"`
	Approve    bool   `json:"approve"`
}

type FinalizePayload struct {
	ProposalID string `json:"proposal_id"`
}

type StateStore interface {
	SetMetadata(key, value string) error
	GetMetadata(key string) (string, error)
	GetValidator(address string) (*core.Validator, error)
	GetActiveValidators() []*core.Validator
	GetValidatorStakeDomain(validatorAddr string) (string, error)
	GetStakeDomainTotalStake(domainID string) (*big.Int, error)
}

type Manager struct {
	config     *config.Config
	worldState StateStore
}

func NewManager(cfg *config.Config, worldState StateStore) *Manager {
	return &Manager{
		config:     cfg,
		worldState: worldState,
	}
}

func (gm *Manager) SubmitParameterChangeProposal(proposer, parameter, proposedValue string) (*Proposal, error) {
	proposalID := fmt.Sprintf("%s-%d", strings.ReplaceAll(parameter, ".", "_"), time.Now().UnixNano())
	return gm.SubmitProposal(proposalID, proposer, parameter, proposedValue)
}

func (gm *Manager) SubmitProposal(id, proposer, parameter, proposedValue string) (*Proposal, error) {
	if !gm.config.Governance.Enabled {
		return nil, fmt.Errorf("governance is disabled")
	}
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("proposal ID cannot be empty")
	}
	if err := gm.ensureActiveValidator(proposer); err != nil {
		return nil, err
	}
	if err := ValidateParameterChange(parameter, proposedValue); err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	proposal := &Proposal{
		ID:            id,
		Parameter:     parameter,
		ProposedValue: proposedValue,
		Proposer:      proposer,
		Status:        ProposalStatusActive,
		CreatedAt:     now,
		VotingEndsAt:  now + int64(gm.config.Governance.VotingPeriod/time.Second),
		YesStake:      "0",
		NoStake:       "0",
		Votes:         make(map[string]*Vote),
	}
	if gm.config.Governance.VotingPeriod <= 0 {
		proposal.VotingEndsAt = now
	}

	if err := gm.saveProposal(proposal); err != nil {
		return nil, err
	}

	return proposal, nil
}

func ParseProposalPayload(data []byte) (*ProposalPayload, error) {
	var payload ProposalPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("invalid governance proposal payload: %w", err)
	}
	if strings.TrimSpace(payload.Parameter) == "" {
		return nil, fmt.Errorf("proposal parameter cannot be empty")
	}
	if strings.TrimSpace(payload.ProposedValue) == "" {
		return nil, fmt.Errorf("proposal value cannot be empty")
	}
	return &payload, nil
}

func ParseVotePayload(data []byte) (*VotePayload, error) {
	var payload VotePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("invalid governance vote payload: %w", err)
	}
	if strings.TrimSpace(payload.ProposalID) == "" {
		return nil, fmt.Errorf("proposal ID cannot be empty")
	}
	return &payload, nil
}

func ParseFinalizePayload(data []byte) (*FinalizePayload, error) {
	var payload FinalizePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("invalid governance finalize payload: %w", err)
	}
	if strings.TrimSpace(payload.ProposalID) == "" {
		return nil, fmt.Errorf("proposal ID cannot be empty")
	}
	return &payload, nil
}

func ValidateParameterChange(parameter, value string) error {
	switch parameter {
	case "economics.inflation_rate", "economics.community_tax", "staking.max_commission", "staking.max_stake_percentage":
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("invalid float value for %s: %w", parameter, err)
		}
		if parsed < 0 || parsed > 1 {
			return fmt.Errorf("%s must be between 0 and 1", parameter)
		}
		return nil
	case "consensus.min_active_validators":
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid integer value for %s: %w", parameter, err)
		}
		if parsed <= 0 {
			return fmt.Errorf("%s must be positive", parameter)
		}
		return nil
	default:
		return fmt.Errorf("parameter %s is not governance-managed", parameter)
	}
}

func (gm *Manager) GetProposal(id string) (*Proposal, error) {
	raw, err := gm.worldState.GetMetadata(proposalMetadataPrefix + id)
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, fmt.Errorf("proposal %s not found", id)
	}

	var proposal Proposal
	if err := json.Unmarshal([]byte(raw), &proposal); err != nil {
		return nil, fmt.Errorf("failed to decode proposal %s: %w", id, err)
	}
	if proposal.Votes == nil {
		proposal.Votes = make(map[string]*Vote)
	}

	return &proposal, nil
}

func (gm *Manager) CastVote(proposalID, validatorAddr string, approve bool) error {
	if !gm.config.Governance.Enabled {
		return fmt.Errorf("governance is disabled")
	}

	proposal, err := gm.GetProposal(proposalID)
	if err != nil {
		return err
	}
	if proposal.Status != ProposalStatusActive {
		return fmt.Errorf("proposal %s is not active", proposalID)
	}
	if time.Now().Unix() > proposal.VotingEndsAt {
		return fmt.Errorf("proposal %s voting period has ended", proposalID)
	}
	if err := gm.ensureActiveValidator(validatorAddr); err != nil {
		return err
	}

	domainID, err := gm.getVotingDomain(validatorAddr)
	if err != nil {
		return err
	}

	if existing, exists := proposal.Votes[domainID]; exists && existing.ValidatorAddress != validatorAddr {
		return fmt.Errorf("stake domain %s has already voted through %s", domainID, existing.ValidatorAddress)
	}

	proposal.Votes[domainID] = &Vote{
		ValidatorAddress: validatorAddr,
		DomainID:         domainID,
		Approve:          approve,
		CastAt:           time.Now().Unix(),
	}

	return gm.saveProposal(proposal)
}

func (gm *Manager) FinalizeProposal(proposalID string) (*Proposal, error) {
	proposal, err := gm.GetProposal(proposalID)
	if err != nil {
		return nil, err
	}
	if proposal.Status != ProposalStatusActive {
		return proposal, nil
	}
	if time.Now().Unix() < proposal.VotingEndsAt {
		return nil, fmt.Errorf("proposal %s is still in the voting period", proposalID)
	}

	totalEligibleStake, err := gm.totalEligibleDomainStake()
	if err != nil {
		return nil, err
	}

	yesStake := big.NewInt(0)
	noStake := big.NewInt(0)
	for domainID, vote := range proposal.Votes {
		domainStake, err := gm.worldState.GetStakeDomainTotalStake(domainID)
		if err != nil {
			return nil, err
		}
		if vote.Approve {
			yesStake.Add(yesStake, domainStake)
		} else {
			noStake.Add(noStake, domainStake)
		}
	}

	participatingStake := new(big.Int).Add(new(big.Int).Set(yesStake), noStake)
	proposal.YesStake = yesStake.String()
	proposal.NoStake = noStake.String()
	proposal.FinalizedAt = time.Now().Unix()

	if totalEligibleStake.Sign() == 0 || participatingStake.Sign() == 0 {
		proposal.Status = ProposalStatusRejected
		return proposal, gm.saveProposal(proposal)
	}

	participationRatio, _ := new(big.Float).Quo(
		new(big.Float).SetInt(participatingStake),
		new(big.Float).SetInt(totalEligibleStake),
	).Float64()

	approvalRatio, _ := new(big.Float).Quo(
		new(big.Float).SetInt(yesStake),
		new(big.Float).SetInt(participatingStake),
	).Float64()

	if participationRatio < gm.config.Governance.Quorum || approvalRatio < gm.config.Governance.ApprovalThreshold {
		proposal.Status = ProposalStatusRejected
		return proposal, gm.saveProposal(proposal)
	}

	if err := gm.applyApprovedParameterChange(proposal.Parameter, proposal.ProposedValue); err != nil {
		return nil, err
	}

	proposal.Status = ProposalStatusApproved
	return proposal, gm.saveProposal(proposal)
}

func (gm *Manager) saveProposal(proposal *Proposal) error {
	data, err := json.Marshal(proposal)
	if err != nil {
		return fmt.Errorf("failed to encode proposal %s: %w", proposal.ID, err)
	}
	return gm.worldState.SetMetadata(proposalMetadataPrefix+proposal.ID, string(data))
}

func (gm *Manager) ensureActiveValidator(address string) error {
	validator, err := gm.worldState.GetValidator(address)
	if err != nil {
		return fmt.Errorf("validator %s not found", address)
	}
	if validator == nil || !validator.Active {
		return fmt.Errorf("validator %s is not active", address)
	}
	return nil
}

func (gm *Manager) getVotingDomain(validatorAddr string) (string, error) {
	if !gm.config.Governance.OwnershipDomainsEnabled {
		return validatorAddr, nil
	}
	return gm.worldState.GetValidatorStakeDomain(validatorAddr)
}

func (gm *Manager) totalEligibleDomainStake() (*big.Int, error) {
	activeValidators := gm.worldState.GetActiveValidators()
	seenDomains := make(map[string]struct{}, len(activeValidators))
	total := big.NewInt(0)

	for _, validator := range activeValidators {
		domainID, err := gm.getVotingDomain(validator.Address)
		if err != nil {
			return nil, err
		}
		if _, exists := seenDomains[domainID]; exists {
			continue
		}
		seenDomains[domainID] = struct{}{}

		domainStake, err := gm.worldState.GetStakeDomainTotalStake(domainID)
		if err != nil {
			return nil, err
		}
		total.Add(total, domainStake)
	}

	return total, nil
}

func (gm *Manager) applyApprovedParameterChange(parameter, value string) error {
	switch parameter {
	case "economics.inflation_rate":
		parsed, _ := strconv.ParseFloat(value, 64)
		gm.config.Economics.InflationRate = parsed
	case "economics.community_tax":
		parsed, _ := strconv.ParseFloat(value, 64)
		gm.config.Economics.CommunityTax = parsed
	case "staking.max_commission":
		parsed, _ := strconv.ParseFloat(value, 64)
		gm.config.Staking.MaxCommission = parsed
	case "staking.max_stake_percentage":
		parsed, _ := strconv.ParseFloat(value, 64)
		gm.config.Staking.MaxStakePercentage = parsed
	case "consensus.min_active_validators":
		parsed, _ := strconv.Atoi(value)
		gm.config.Consensus.MinActiveValidators = parsed
	default:
		return fmt.Errorf("parameter %s is not governance-managed", parameter)
	}

	return gm.worldState.SetMetadata("governance/param/"+parameter, value)
}
