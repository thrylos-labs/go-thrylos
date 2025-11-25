// SlashingStorage handles blockchain slashing data persistence
//
// This component manages the persistent storage of slashing-related data for the
// Proof-of-Stake consensus mechanism. It handles:
//
// • Jailed Validators: Tracks validators temporarily excluded from consensus
// • Slashing Records: Complete audit trail of all slashing events
// • Processed Evidence: Prevents double-slashing for the same offense
// • Validator Status: Active/jailed/slashed/exited state tracking
// • Attestation History: For downtime detection and slashing
//
// SlashingStorage operates independently of consensus logic, providing clean
// separation between storage operations and business logic. It ensures all
// slashing data survives node restarts and maintains accountability.
//
// Key responsibilities:
// - Atomic storage of slashing events
// - Deduplication of evidence to prevent double slashing
// - Efficient bulk loading on node startup
// - Audit trail for governance and appeals

package storage

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v3"
	"github.com/thrylos-labs/go-thrylos/types"
)

// Key prefixes for slashing data
const (
	keyPrefixJail     = "slashing:jail:"
	keyPrefixRecord   = "slashing:record:"
	keyPrefixEvidence = "slashing:evidence:"
	keyPrefixHistory  = "slashing:history:"
	keyPrefixStatus   = "slashing:status:"
)

// SlashingStorage handles slashing data persistence
type SlashingStorage struct {
	db *badger.DB
}

// NewSlashingStorage creates a new slashing storage handler
func NewSlashingStorage(db *badger.DB) *SlashingStorage {
	return &SlashingStorage{
		db: db,
	}
}

// Close implements the Storage interface
func (ss *SlashingStorage) Close() error {
	// SlashingStorage doesn't own the BadgerDB instance
	// It will be closed by BadgerStorage
	return nil
}

// ===== Jailed Validator Operations =====

// SaveJailedValidator persists a jailed validator
func (ss *SlashingStorage) SaveJailedValidator(address string, jail *JailedValidator) error {
	key := keyPrefixJail + address

	data, err := json.Marshal(jail)
	if err != nil {
		return fmt.Errorf("failed to marshal jailed validator: %w", err)
	}

	return ss.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(key), data)
	})
}

// GetJailedValidator retrieves a jailed validator by address
func (ss *SlashingStorage) GetJailedValidator(address string) (*JailedValidator, error) {
	key := keyPrefixJail + address

	var jail JailedValidator
	err := ss.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		if err != nil {
			return err
		}

		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &jail)
		})
	})

	if err == badger.ErrKeyNotFound {
		return nil, nil // Not jailed
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get jailed validator: %w", err)
	}

	return &jail, nil
}

// DeleteJailedValidator removes a validator from jail (when released)
func (ss *SlashingStorage) DeleteJailedValidator(address string) error {
	key := keyPrefixJail + address

	return ss.db.Update(func(txn *badger.Txn) error {
		return txn.Delete([]byte(key))
	})
}

// GetAllJailedValidators retrieves all currently jailed validators
func (ss *SlashingStorage) GetAllJailedValidators() (map[string]*JailedValidator, error) {
	jailed := make(map[string]*JailedValidator)

	err := ss.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(keyPrefixJail)

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()

			err := item.Value(func(val []byte) error {
				var jail JailedValidator
				if err := json.Unmarshal(val, &jail); err != nil {
					return err
				}
				jailed[jail.ValidatorAddress] = &jail
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})

	return jailed, err
}

// ===== Slashing Record Operations =====

// SaveSlashingRecord persists a slashing record
func (ss *SlashingStorage) SaveSlashingRecord(address string, record *types.SlashingRecord) error {
	// Use timestamp in key for chronological ordering
	key := fmt.Sprintf("%s%s:%d", keyPrefixRecord, address, record.Timestamp.UnixNano())

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("failed to marshal slashing record: %w", err)
	}

	return ss.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(key), data)
	})
}

// GetSlashingRecords retrieves all slashing records for a validator
func (ss *SlashingStorage) GetSlashingRecords(address string) ([]*types.SlashingRecord, error) {
	prefix := keyPrefixRecord + address
	var records []*types.SlashingRecord

	err := ss.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(prefix)

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()

			err := item.Value(func(val []byte) error {
				var record types.SlashingRecord
				if err := json.Unmarshal(val, &record); err != nil {
					return err
				}
				records = append(records, &record)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})

	return records, err
}

// ===== Processed Evidence Operations =====

// SaveProcessedEvidence marks evidence as processed to prevent double slashing
func (ss *SlashingStorage) SaveProcessedEvidence(evidenceHash string) error {
	key := keyPrefixEvidence + evidenceHash

	return ss.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(key), []byte("1"))
	})
}

// IsEvidenceProcessed checks if evidence has already been processed
func (ss *SlashingStorage) IsEvidenceProcessed(evidenceHash string) (bool, error) {
	key := keyPrefixEvidence + evidenceHash

	err := ss.db.View(func(txn *badger.Txn) error {
		_, err := txn.Get([]byte(key))
		return err
	})

	if err == badger.ErrKeyNotFound {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to check evidence: %w", err)
	}

	return true, nil
}

// GetAllProcessedEvidence retrieves all processed evidence hashes
func (ss *SlashingStorage) GetAllProcessedEvidence() (map[string]bool, error) {
	evidence := make(map[string]bool)

	err := ss.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(keyPrefixEvidence)
		opts.PrefetchValues = false // We only need keys

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			key := string(item.Key())
			hash := key[len(keyPrefixEvidence):] // Remove prefix
			evidence[hash] = true
		}
		return nil
	})

	return evidence, err
}

// ===== Validator Status Operations =====

// SaveValidatorStatus persists validator status
func (ss *SlashingStorage) SaveValidatorStatus(address string, status ValidatorStatus) error {
	key := keyPrefixStatus + address

	data, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("failed to marshal validator status: %w", err)
	}

	return ss.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(key), data)
	})
}

// GetValidatorStatus retrieves a validator's status
func (ss *SlashingStorage) GetValidatorStatus(address string) (ValidatorStatus, error) {
	key := keyPrefixStatus + address

	var status ValidatorStatus
	err := ss.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		if err != nil {
			return err
		}

		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &status)
		})
	})

	if err == badger.ErrKeyNotFound {
		return ValidatorActive, nil // Default: active
	}
	if err != nil {
		return ValidatorActive, fmt.Errorf("failed to get validator status: %w", err)
	}

	return status, nil
}

// GetAllValidatorStatuses retrieves all validator statuses
func (ss *SlashingStorage) GetAllValidatorStatuses() (map[string]ValidatorStatus, error) {
	statuses := make(map[string]ValidatorStatus)

	err := ss.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(keyPrefixStatus)

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			key := string(item.Key())
			address := key[len(keyPrefixStatus):] // Remove prefix

			err := item.Value(func(val []byte) error {
				var status ValidatorStatus
				if err := json.Unmarshal(val, &status); err != nil {
					return err
				}
				statuses[address] = status
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})

	return statuses, err
}

// ===== Attestation History Operations =====

// SaveAttestationHistory persists attestation history for a validator
func (ss *SlashingStorage) SaveAttestationHistory(address string, history *AttestationHistory) error {
	key := keyPrefixHistory + address

	data, err := json.Marshal(history)
	if err != nil {
		return fmt.Errorf("failed to marshal attestation history: %w", err)
	}

	return ss.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(key), data)
	})
}

// GetAttestationHistory retrieves attestation history for a validator
func (ss *SlashingStorage) GetAttestationHistory(address string) (*AttestationHistory, error) {
	key := keyPrefixHistory + address

	var history AttestationHistory
	err := ss.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		if err != nil {
			return err
		}

		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &history)
		})
	})

	if err == badger.ErrKeyNotFound {
		return &AttestationHistory{
			ValidatorAddress: address,
			Attestations:     make([]*AttestationRecord, 0),
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get attestation history: %w", err)
	}

	return &history, nil
}

// ===== Bulk Operations =====

// SlashingData holds all slashing data for bulk operations
type SlashingData struct {
	JailedValidators  map[string]*JailedValidator
	ProcessedEvidence map[string]bool
	ValidatorStatuses map[string]ValidatorStatus
}

// LoadAllSlashingData loads all slashing data in one operation (for startup)
func (ss *SlashingStorage) LoadAllSlashingData() (*SlashingData, error) {
	data := &SlashingData{
		JailedValidators:  make(map[string]*JailedValidator),
		ProcessedEvidence: make(map[string]bool),
		ValidatorStatuses: make(map[string]ValidatorStatus),
	}

	// Load jailed validators
	jailed, err := ss.GetAllJailedValidators()
	if err != nil {
		return nil, fmt.Errorf("failed to load jailed validators: %w", err)
	}
	data.JailedValidators = jailed

	// Load processed evidence
	evidence, err := ss.GetAllProcessedEvidence()
	if err != nil {
		return nil, fmt.Errorf("failed to load processed evidence: %w", err)
	}
	data.ProcessedEvidence = evidence

	// Load validator statuses
	statuses, err := ss.GetAllValidatorStatuses()
	if err != nil {
		return nil, fmt.Errorf("failed to load validator statuses: %w", err)
	}
	data.ValidatorStatuses = statuses

	return data, nil
}

const (
	// DoubleVoting: Validator votes for two different blocks at the same height
	DoubleVoting types.SlashingCondition = iota
	// SurroundVoting: Validator's attestation surrounds another attestation
	SurroundVoting
	// InvalidProposal: Validator proposes an invalid block
	InvalidProposal
	// Downtime: Validator is offline for extended period
	Downtime
	// InvalidSignature: Validator signs with incorrect key or malformed signature
	InvalidSignature
)

// SlashingConfig defines the penalties for each slashing condition
type SlashingConfig struct {
	// Penalty percentages (0-100)
	DoubleVotingPenalty     uint8 // Default: 50%
	SurroundVotingPenalty   uint8 // Default: 30%
	InvalidProposalPenalty  uint8 // Default: 20%
	DowntimePenalty         uint8 // Default: 5%
	InvalidSignaturePenalty uint8 // Default: 10%

	// Downtime configuration
	MaxMissedAttestations uint64        // Default: 100
	AttestationWindow     time.Duration // Default: 24 hours

	// Slashing jail time (time before validator can rejoin)
	JailDuration time.Duration // Default: 7 days

	// Minimum stake required to be a validator
	MinimumStake int64 // Default: 1000 tokens
}

// DefaultSlashingConfig returns sensible default configuration
func DefaultSlashingConfig() *SlashingConfig {
	return &SlashingConfig{
		DoubleVotingPenalty:     50,
		SurroundVotingPenalty:   30,
		InvalidProposalPenalty:  20,
		DowntimePenalty:         5,
		InvalidSignaturePenalty: 10,
		MaxMissedAttestations:   100,
		AttestationWindow:       24 * time.Hour,
		JailDuration:            7 * 24 * time.Hour,
		MinimumStake:            1000,
	}
}

// ValidatorStatus represents the current status of a validator
type ValidatorStatus int

const (
	ValidatorActive ValidatorStatus = iota
	ValidatorJailed
	ValidatorSlashed
	ValidatorExited
)

// JailedValidator tracks validators that are temporarily jailed
type JailedValidator struct {
	ValidatorAddress string
	JailTime         time.Time
	ReleaseTime      time.Time
	Reason           types.SlashingCondition
}

// AttestationHistory tracks validator attestations for downtime detection
type AttestationHistory struct {
	ValidatorAddress string
	TotalSlots       uint64
	MissedSlots      uint64
	LastAttestation  time.Time
	MissedSlotList   []uint64
	mu               sync.RWMutex
	Attestations     []*AttestationRecord
}

// RecordAttestation records that a validator attested at a slot
func (ah *AttestationHistory) RecordAttestation(slot uint64) {
	ah.mu.Lock()
	defer ah.mu.Unlock()

	ah.TotalSlots++
	ah.LastAttestation = time.Now()
}

// RecordMiss records that a validator missed a slot
func (ah *AttestationHistory) RecordMiss(slot uint64) {
	ah.mu.Lock()
	defer ah.mu.Unlock()

	ah.TotalSlots++
	ah.MissedSlots++
	ah.MissedSlotList = append(ah.MissedSlotList, slot)

	// Keep only last 1000 missed slots in memory
	if len(ah.MissedSlotList) > 1000 {
		ah.MissedSlotList = ah.MissedSlotList[len(ah.MissedSlotList)-1000:]
	}
}

// GetMissRate returns the percentage of missed attestations
func (ah *AttestationHistory) GetMissRate() float64 {
	ah.mu.RLock()
	defer ah.mu.RUnlock()

	if ah.TotalSlots == 0 {
		return 0
	}
	return float64(ah.MissedSlots) / float64(ah.TotalSlots) * 100
}

// AttestationRecord represents a single attestation for comparison
type AttestationRecord struct {
	ValidatorAddress string
	Epoch            uint64
	BlockHash        string
	Signature        []byte
	Timestamp        time.Time
}

// Conflicts checks if this attestation conflicts with another (double vote)
func (ar *AttestationRecord) Conflicts(other *AttestationRecord) bool {
	// Two attestations conflict if they have the same epoch
	// but different block hashes
	return ar.Epoch == other.Epoch && ar.BlockHash != other.BlockHash
}

// IsSurroundedBy checks if this attestation is surrounded by another
// Note: Since your Attestation only has Epoch (not source/target),
// surround voting detection is simplified or may need Vote struct instead
func (ar *AttestationRecord) IsSurroundedBy(other *AttestationRecord) bool {
	// For simplified surround detection with single epoch
	// This would need to be enhanced with Vote struct for full Casper FFG
	return false // Placeholder - use Vote struct for proper surround detection
}
