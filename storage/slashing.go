// storage/slashing.go
// SlashingStorage handles blockchain slashing data persistence
// M-2 FIX: Added evidence pruning and archival support

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
	// M-2 FIX: Add prefix for archived evidence
	keyPrefixArchive = "slashing:archive:"
	// M-2 FIX: Track pruning statistics
	keyPrefixPruning = "slashing:pruning:stats"
)

// SlashingStorage handles slashing data persistence
type SlashingStorage struct {
	db *badger.DB
	// M-2 FIX: Track pruning stats
	pruningStats *PruningStats
	mu           sync.RWMutex
}

// M-2 FIX: PruningStats tracks pruning activity
type PruningStats struct {
	LastPruneTime       time.Time         `json:"last_prune_time"`
	TotalPruned         uint64            `json:"total_pruned"`
	TotalArchived       uint64            `json:"total_archived"`
	LastPruneCount      uint64            `json:"last_prune_count"`
	LastArchiveCount    uint64            `json:"last_archive_count"`
	EvidenceCountByType map[string]uint64 `json:"evidence_count_by_type"`
}

// NewSlashingStorage creates a new slashing storage handler
func NewSlashingStorage(db *badger.DB) *SlashingStorage {
	ss := &SlashingStorage{
		db: db,
		pruningStats: &PruningStats{
			EvidenceCountByType: make(map[string]uint64),
		},
	}

	// M-2 FIX: Load pruning stats on startup
	_ = ss.loadPruningStats()

	return ss
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

// M-2 FIX: Enhanced evidence storage with metadata
type StoredEvidence struct {
	Hash        string    `json:"hash"`
	ProcessedAt time.Time `json:"processed_at"`
	Type        string    `json:"type"`
	Validator   string    `json:"validator"`
}

// SaveProcessedEvidence marks evidence as processed to prevent double slashing
func (ss *SlashingStorage) SaveProcessedEvidence(evidenceHash string) error {
	key := keyPrefixEvidence + evidenceHash

	// M-2 FIX: Store metadata with evidence
	evidence := &StoredEvidence{
		Hash:        evidenceHash,
		ProcessedAt: time.Now(),
	}

	data, err := json.Marshal(evidence)
	if err != nil {
		return fmt.Errorf("failed to marshal evidence: %w", err)
	}

	return ss.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(key), data)
	})
}

// M-2 FIX: Enhanced version with metadata
func (ss *SlashingStorage) SaveProcessedEvidenceWithMetadata(evidenceHash, evidenceType, validator string) error {
	key := keyPrefixEvidence + evidenceHash

	evidence := &StoredEvidence{
		Hash:        evidenceHash,
		ProcessedAt: time.Now(),
		Type:        evidenceType,
		Validator:   validator,
	}

	data, err := json.Marshal(evidence)
	if err != nil {
		return fmt.Errorf("failed to marshal evidence: %w", err)
	}

	return ss.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(key), data)
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

// ===== M-2 FIX: Evidence Pruning Operations =====

// PruneOldEvidence removes evidence older than the retention period
func (ss *SlashingStorage) PruneOldEvidence(olderThan time.Time) (uint64, error) {
	var pruneCount uint64

	err := ss.db.Update(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(keyPrefixEvidence)

		it := txn.NewIterator(opts)
		defer it.Close()

		var toDelete [][]byte

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()

			err := item.Value(func(val []byte) error {
				var stored StoredEvidence
				if err := json.Unmarshal(val, &stored); err != nil {
					// Old format - just use key
					return nil
				}

				// Check if old enough to prune
				if stored.ProcessedAt.Before(olderThan) {
					toDelete = append(toDelete, item.KeyCopy(nil))
					pruneCount++
				}
				return nil
			})
			if err != nil {
				return err
			}
		}

		// Delete marked entries
		for _, key := range toDelete {
			if err := txn.Delete(key); err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		return 0, fmt.Errorf("failed to prune evidence: %w", err)
	}

	// Update stats
	ss.mu.Lock()
	ss.pruningStats.LastPruneTime = time.Now()
	ss.pruningStats.LastPruneCount = pruneCount
	ss.pruningStats.TotalPruned += pruneCount
	ss.mu.Unlock()

	_ = ss.savePruningStats()

	return pruneCount, nil
}

// ArchiveEvidence moves old evidence to archive storage
func (ss *SlashingStorage) ArchiveEvidence(evidenceHash string, evidence []byte) error {
	key := keyPrefixArchive + evidenceHash

	return ss.db.Update(func(txn *badger.Txn) error {
		// Save to archive
		if err := txn.Set([]byte(key), evidence); err != nil {
			return err
		}

		// Remove from active storage
		activeKey := keyPrefixEvidence + evidenceHash
		return txn.Delete([]byte(activeKey))
	})
}

// GetArchivedEvidence retrieves evidence from archive
func (ss *SlashingStorage) GetArchivedEvidence(evidenceHash string) ([]byte, error) {
	key := keyPrefixArchive + evidenceHash

	var data []byte
	err := ss.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		if err != nil {
			return err
		}

		return item.Value(func(val []byte) error {
			data = append([]byte{}, val...)
			return nil
		})
	})

	if err == badger.ErrKeyNotFound {
		return nil, nil
	}

	return data, err
}

// PruneAndArchive combines pruning with archival
func (ss *SlashingStorage) PruneAndArchive(archiveAge, pruneAge time.Time) (archived, pruned uint64, err error) {
	err = ss.db.Update(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(keyPrefixEvidence)

		it := txn.NewIterator(opts)
		defer it.Close()

		var toArchive []struct{ key, value []byte }
		var toDelete [][]byte

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()

			err := item.Value(func(val []byte) error {
				var stored StoredEvidence
				if err := json.Unmarshal(val, &stored); err != nil {
					return nil
				}

				// Very old - just delete
				if stored.ProcessedAt.Before(pruneAge) {
					toDelete = append(toDelete, item.KeyCopy(nil))
					pruned++
				} else if stored.ProcessedAt.Before(archiveAge) {
					// Old but worth archiving
					toArchive = append(toArchive, struct{ key, value []byte }{
						key:   item.KeyCopy(nil),
						value: append([]byte{}, val...),
					})
					archived++
				}
				return nil
			})
			if err != nil {
				return err
			}
		}

		// Archive entries
		for _, item := range toArchive {
			hash := string(item.key)[len(keyPrefixEvidence):]
			archiveKey := []byte(keyPrefixArchive + hash)
			if err := txn.Set(archiveKey, item.value); err != nil {
				return err
			}
			if err := txn.Delete(item.key); err != nil {
				return err
			}
		}

		// Delete very old entries
		for _, key := range toDelete {
			if err := txn.Delete(key); err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		return 0, 0, fmt.Errorf("failed to prune and archive: %w", err)
	}

	// Update stats
	ss.mu.Lock()
	ss.pruningStats.LastPruneTime = time.Now()
	ss.pruningStats.LastArchiveCount = archived
	ss.pruningStats.LastPruneCount = pruned
	ss.pruningStats.TotalArchived += archived
	ss.pruningStats.TotalPruned += pruned
	ss.mu.Unlock()

	_ = ss.savePruningStats()

	return archived, pruned, nil
}

// GetEvidenceCount returns count of active evidence entries
func (ss *SlashingStorage) GetEvidenceCount() (uint64, error) {
	var count uint64

	err := ss.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(keyPrefixEvidence)
		opts.PrefetchValues = false

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			count++
		}
		return nil
	})

	return count, err
}

// GetPruningStats returns current pruning statistics
func (ss *SlashingStorage) GetPruningStats() *PruningStats {
	ss.mu.RLock()
	defer ss.mu.RUnlock()

	// Return a copy
	stats := *ss.pruningStats
	stats.EvidenceCountByType = make(map[string]uint64)
	for k, v := range ss.pruningStats.EvidenceCountByType {
		stats.EvidenceCountByType[k] = v
	}

	return &stats
}

// savePruningStats persists pruning statistics
func (ss *SlashingStorage) savePruningStats() error {
	ss.mu.RLock()
	data, err := json.Marshal(ss.pruningStats)
	ss.mu.RUnlock()

	if err != nil {
		return fmt.Errorf("failed to marshal pruning stats: %w", err)
	}

	return ss.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(keyPrefixPruning), data)
	})
}

// loadPruningStats loads pruning statistics from storage
func (ss *SlashingStorage) loadPruningStats() error {
	err := ss.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(keyPrefixPruning))
		if err != nil {
			return err
		}

		return item.Value(func(val []byte) error {
			ss.mu.Lock()
			defer ss.mu.Unlock()
			return json.Unmarshal(val, ss.pruningStats)
		})
	})

	if err == badger.ErrKeyNotFound {
		return nil // No stats yet
	}

	return err
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

// SlashingConfig defines the penalties for each slashing condition
type SlashingConfig struct {
	// Penalty percentages (0-100)
	DoubleVotingPenalty    uint8 // Default: 50%
	SurroundVotingPenalty  uint8 // Default: 30%
	InvalidProposalPenalty uint8 // Default: 20%

	// RENAMED: DowntimePenalty -> SlashingDowntime to match manager expectations
	SlashingDowntime uint8 // Default: 5%

	InvalidSignaturePenalty uint8 // Default: 10%

	// Downtime configuration
	MaxMissedAttestations uint64        // Default: 100
	AttestationWindow     time.Duration // Default: 24 hours

	// Slashing jail time (time before validator can rejoin)
	// RENAMED: JailDuration -> JailDurationHours (int) to match manager
	JailDurationHours int // Default: 168 (7 days)

	// Minimum stake required to be a validator
	MinimumStake string // Default: 1000 tokens

	// M-2 FIX: Pruning configuration
	EvidenceRetentionDays int  // Days to keep evidence before archiving
	ArchiveRetentionDays  int  // Days to keep archived evidence before deletion
	EnableAutoPruning     bool // Automatically prune old evidence
	PruneIntervalHours    int  // How often to run pruning (hours)
}

// DefaultSlashingConfig returns sensible default configuration
func DefaultSlashingConfig() *SlashingConfig {
	return &SlashingConfig{
		DoubleVotingPenalty:     50,
		SurroundVotingPenalty:   30,
		InvalidProposalPenalty:  20,
		SlashingDowntime:        5,
		InvalidSignaturePenalty: 10,
		MaxMissedAttestations:   100,
		AttestationWindow:       24 * time.Hour,
		JailDurationHours:       168, // 7 days (168 hours)
		MinimumStake:            "1000000000000000000000",
		// M-2 FIX: Pruning defaults
		EvidenceRetentionDays: 30, // Keep evidence for 30 days
		ArchiveRetentionDays:  90, // Keep archives for 90 days
		EnableAutoPruning:     true,
		PruneIntervalHours:    24, // Prune daily
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
	Slot             uint64
	BlockHash        string
	Signature        []byte
	Timestamp        time.Time

	// Added for FFG Surround Vote Detection (Mainnet Readiness)
	SourceEpoch uint64
	TargetEpoch uint64
}

// Conflicts checks if this attestation conflicts with another (double vote)
func (ar *AttestationRecord) Conflicts(other *AttestationRecord) bool {
	// Two attestations conflict if they vote in the same slot
	// (and epoch) but for different blocks
	return ar.Epoch == other.Epoch &&
		ar.Slot == other.Slot &&
		ar.BlockHash != other.BlockHash
}

// IsSurroundedBy checks if this attestation (ar) is surrounded by another attestation (other).
//
// A Surround Vote violation occurs if:
// other.SourceEpoch < ar.SourceEpoch AND other.TargetEpoch > ar.TargetEpoch
//
// This means 'other' spans a wider range of history that completely encapsulates 'ar',
// which contradicts the finality guarantees.
func (ar *AttestationRecord) IsSurroundedBy(other *AttestationRecord) bool {
	// Safety check: If FFG data is missing (Testnet Mode), we cannot detect surround votes.
	// In a real Mainnet scenario, 0 could be valid for genesis, so we'd need a better "empty" check.
	// For now, if both are 0, we assume we are in "Point Voting" mode (Testnet) and skip.
	if ar.SourceEpoch == 0 && ar.TargetEpoch == 0 && other.SourceEpoch == 0 && other.TargetEpoch == 0 {
		return false
	}

	// 1. Verify we are comparing the same validator
	if ar.ValidatorAddress != other.ValidatorAddress {
		return false
	}

	// 2. Casper FFG Surround Check
	// Condition: The 'other' vote surrounds 'ar'
	isSurrounded := other.SourceEpoch < ar.SourceEpoch && other.TargetEpoch > ar.TargetEpoch

	return isSurrounded
}
