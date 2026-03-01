package api

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	coremath "github.com/thrylos-labs/go-thrylos/core/math"
	corepb "github.com/thrylos-labs/go-thrylos/proto/core"
)

// --- POINTS POLICY (testnet season defaults) ---
const (
	TotalAirdropTHR = 5_000_000

	// Hard cap per wallet per season.
	MaxPointsPerWallet = 10_000

	// Faucet is for funding, not points.
	PointsFaucet = 0

	// Transfer points with diminishing returns.
	MinTransferWeiString    = "1000000000000000000" // 1 THR
	TransferPointsFull      = 5
	TransferPointsReduced   = 2
	TransferFullTierCount   = 10
	TransferDailyCountCap   = 20
	StakePointsPerAction    = 20
	StakeDailyCountCap      = 5
	UnstakePointsPerAction  = 15
	UnstakeDailyCountCap    = 3
	PointsStreakBonus       = 50
	defaultLeaderboardLimit = 50
)

// --- DATA STRUCTURES ---

type UserActivity struct {
	Address     string `json:"address"`
	TotalPoints int    `json:"total_points"`

	// Faucet Tracking
	LastFaucet time.Time `json:"last_faucet"`

	// Daily action counters.
	DailyCounterDate   string `json:"daily_counter_date"` // YYYY-MM-DD
	DailyTransferCount int    `json:"daily_transfer_count"`
	DailyStakeCount    int    `json:"daily_stake_count"`
	DailyUnstakeCount  int    `json:"daily_unstake_count"`

	// Confirmed transfer tracking for idempotent rewards.
	RewardedTransfers map[string]bool `json:"rewarded_transfers"`

	// Retention
	CurrentStreak  int    `json:"current_streak"`
	MaxStreak      int    `json:"max_streak"`
	LastActiveDate string `json:"last_active_date"` // YYYY-MM-DD
}

type PointsPolicy struct {
	TotalAirdropTHR       int    `json:"total_airdrop_thr"`
	MaxPointsPerWallet    int    `json:"max_points_per_wallet"`
	MinTransferWei        string `json:"min_transfer_wei"`
	TransferDailyCap      int    `json:"transfer_daily_cap"`
	TransferFullTier      int    `json:"transfer_full_tier_count"`
	TransferPointsFull    int    `json:"transfer_points_full"`
	TransferPointsReduced int    `json:"transfer_points_reduced"`
	StakeDailyCap         int    `json:"stake_daily_cap"`
	StakePoints           int    `json:"stake_points"`
	UnstakeDailyCap       int    `json:"unstake_daily_cap"`
	UnstakePoints         int    `json:"unstake_points"`
	FaucetPoints          int    `json:"faucet_points"`
}

type PointsStats struct {
	TotalPointsIssued  int `json:"total_points_issued"`
	UniqueWallets      int `json:"unique_wallets"`
	TotalAirdropTHR    int `json:"total_airdrop_thr"`
	MaxPointsPerWallet int `json:"max_points_per_wallet"`
}

type PointsManager struct {
	mu       sync.RWMutex
	Users    map[string]*UserActivity `json:"users"`
	FilePath string
}

var minTransferWei = mustBigInt(MinTransferWeiString)

// --- INITIALIZATION ---

func NewPointsManager(path string) *PointsManager {
	pm := &PointsManager{
		Users:    make(map[string]*UserActivity),
		FilePath: path,
	}
	pm.load()
	return pm
}

// --- CORE LOGIC ---

// AwardFaucet updates faucet cooldown metadata but awards no points.
func (pm *PointsManager) AwardFaucet(address string) (int, bool, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	user := pm.getOrCreate(normalizeAddress(address))
	now := time.Now().UTC()
	today := now.Format("2006-01-02")

	if !user.LastFaucet.IsZero() && now.Sub(user.LastFaucet) < 24*time.Hour {
		return user.TotalPoints, false, nil
	}

	previousLastFaucet := user.LastFaucet
	previousCurrentStreak := user.CurrentStreak
	previousMaxStreak := user.MaxStreak
	previousLastActiveDate := user.LastActiveDate

	user.LastFaucet = now
	pm.updateStreak(user, today)
	if err := pm.save(); err != nil {
		user.LastFaucet = previousLastFaucet
		user.CurrentStreak = previousCurrentStreak
		user.MaxStreak = previousMaxStreak
		user.LastActiveDate = previousLastActiveDate
		return user.TotalPoints, false, err
	}
	return user.TotalPoints, PointsFaucet > 0, nil
}

// SyncConfirmedTransfers awards transfer points idempotently based on confirmed txs.
func (pm *PointsManager) SyncConfirmedTransfers(address string, txs []*corepb.Transaction) int {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	addr := normalizeAddress(address)
	user := pm.getOrCreate(addr)
	changed := false

	for _, tx := range txs {
		if tx == nil {
			continue
		}
		if normalizeAddress(tx.From) != addr {
			continue
		}
		if pm.applyConfirmedTransferReward(user, tx) {
			changed = true
		}
	}

	if changed {
		_ = pm.save()
	}
	return user.TotalPoints
}

// RecordTransaction remains for compatibility and forwards to confirmed-transfer logic.
func (pm *PointsManager) RecordTransaction(from, to string) int {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	from = normalizeAddress(from)
	to = normalizeAddress(to)
	user := pm.getOrCreate(from)
	day := time.Now().UTC().Format("2006-01-02")
	pm.ensureDailyCounters(user, day)

	if from == "" || to == "" || from == to || user.DailyTransferCount >= TransferDailyCountCap {
		return user.TotalPoints
	}

	points := TransferPointsFull
	if user.DailyTransferCount >= TransferFullTierCount {
		points = TransferPointsReduced
	}
	awarded := pm.addPointsWithWalletCap(user, points)
	if awarded > 0 {
		user.DailyTransferCount++
		pm.updateStreak(user, day)
		_ = pm.save()
	}
	return user.TotalPoints
}

// RecordDelegation rewards staking actions with a per-day cap.
func (pm *PointsManager) RecordDelegation(address string) int {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	user := pm.getOrCreate(normalizeAddress(address))
	day := time.Now().UTC().Format("2006-01-02")
	pm.ensureDailyCounters(user, day)

	if user.DailyStakeCount >= StakeDailyCountCap {
		return user.TotalPoints
	}

	awarded := pm.addPointsWithWalletCap(user, StakePointsPerAction)
	user.DailyStakeCount++
	if awarded > 0 {
		pm.updateStreak(user, day)
	}
	_ = pm.save()
	return user.TotalPoints
}

// RecordUndelegation rewards unstaking actions with a per-day cap.
func (pm *PointsManager) RecordUndelegation(address string) int {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	user := pm.getOrCreate(normalizeAddress(address))
	day := time.Now().UTC().Format("2006-01-02")
	pm.ensureDailyCounters(user, day)

	if user.DailyUnstakeCount >= UnstakeDailyCountCap {
		return user.TotalPoints
	}

	awarded := pm.addPointsWithWalletCap(user, UnstakePointsPerAction)
	user.DailyUnstakeCount++
	if awarded > 0 {
		pm.updateStreak(user, day)
	}
	_ = pm.save()
	return user.TotalPoints
}

func (pm *PointsManager) GetPolicy() PointsPolicy {
	return PointsPolicy{
		TotalAirdropTHR:       TotalAirdropTHR,
		MaxPointsPerWallet:    MaxPointsPerWallet,
		MinTransferWei:        MinTransferWeiString,
		TransferDailyCap:      TransferDailyCountCap,
		TransferFullTier:      TransferFullTierCount,
		TransferPointsFull:    TransferPointsFull,
		TransferPointsReduced: TransferPointsReduced,
		StakeDailyCap:         StakeDailyCountCap,
		StakePoints:           StakePointsPerAction,
		UnstakeDailyCap:       UnstakeDailyCountCap,
		UnstakePoints:         UnstakePointsPerAction,
		FaucetPoints:          PointsFaucet,
	}
}

func (pm *PointsManager) GetStats() PointsStats {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	total := 0
	for _, u := range pm.Users {
		total += u.TotalPoints
	}
	return PointsStats{
		TotalPointsIssued:  total,
		UniqueWallets:      len(pm.Users),
		TotalAirdropTHR:    TotalAirdropTHR,
		MaxPointsPerWallet: MaxPointsPerWallet,
	}
}

// --- LEADERBOARD & UTILS ---

type LeaderboardEntry struct {
	Address string `json:"address"`
	Points  int    `json:"points"`
	Rank    int    `json:"rank"`
}

func (pm *PointsManager) GetLeaderboard(limit int) []LeaderboardEntry {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	var entries []LeaderboardEntry
	for addr, u := range pm.Users {
		entries = append(entries, LeaderboardEntry{Address: addr, Points: u.TotalPoints})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Points > entries[j].Points
	})

	if limit <= 0 {
		limit = defaultLeaderboardLimit
	}
	if limit > len(entries) {
		limit = len(entries)
	}

	result := entries[:limit]
	for i := range result {
		result[i].Rank = i + 1
	}

	return result
}

func (pm *PointsManager) GetUserPoints(address string) *UserActivity {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	address = normalizeAddress(address)
	if user, exists := pm.Users[address]; exists {
		return user
	}
	return &UserActivity{Address: address, TotalPoints: 0}
}

func (pm *PointsManager) updateStreak(user *UserActivity, day string) {
	if day == "" {
		day = time.Now().UTC().Format("2006-01-02")
	}
	dayTime, err := time.Parse("2006-01-02", day)
	if err != nil {
		return
	}
	yesterday := dayTime.AddDate(0, 0, -1).Format("2006-01-02")

	if user.LastActiveDate == day {
		return
	}

	if user.LastActiveDate == yesterday {
		user.CurrentStreak++
		pm.addPointsWithWalletCap(user, PointsStreakBonus)
	} else {
		user.CurrentStreak = 1
	}

	if user.CurrentStreak > user.MaxStreak {
		user.MaxStreak = user.CurrentStreak
	}

	user.LastActiveDate = day
}

func (pm *PointsManager) getOrCreate(address string) *UserActivity {
	address = normalizeAddress(address)
	if _, exists := pm.Users[address]; !exists {
		pm.Users[address] = &UserActivity{
			Address:           address,
			RewardedTransfers: make(map[string]bool),
		}
	} else if pm.Users[address].RewardedTransfers == nil {
		pm.Users[address].RewardedTransfers = make(map[string]bool)
	}
	return pm.Users[address]
}

func (pm *PointsManager) save() error {
	data, _ := json.MarshalIndent(pm.Users, "", "  ")
	dir := filepath.Dir(pm.FilePath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	tmpFile, err := os.CreateTemp(dir, filepath.Base(pm.FilePath)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, pm.FilePath); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func (pm *PointsManager) load() {
	data, err := os.ReadFile(pm.FilePath)
	if err == nil {
		_ = json.Unmarshal(data, &pm.Users)
		for _, u := range pm.Users {
			if u.RewardedTransfers == nil {
				u.RewardedTransfers = make(map[string]bool)
			}
		}
	}
}

func (pm *PointsManager) ExportSnapshot(path string) error {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	data, err := json.MarshalIndent(pm.Users, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal snapshot: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

func (pm *PointsManager) ensureDailyCounters(user *UserActivity, day string) {
	if day == "" {
		day = time.Now().UTC().Format("2006-01-02")
	}
	if user.DailyCounterDate == day {
		return
	}
	user.DailyCounterDate = day
	user.DailyTransferCount = 0
	user.DailyStakeCount = 0
	user.DailyUnstakeCount = 0
}

func (pm *PointsManager) addPointsWithWalletCap(user *UserActivity, points int) int {
	if points <= 0 {
		return 0
	}
	remaining := MaxPointsPerWallet - user.TotalPoints
	if remaining <= 0 {
		return 0
	}
	if points > remaining {
		points = remaining
	}
	user.TotalPoints += points
	return points
}

func (pm *PointsManager) applyConfirmedTransferReward(user *UserActivity, tx *corepb.Transaction) bool {
	txHash := normalizeRewardTxHash(tx.Hash)
	if txHash == "" {
		txHash = normalizeRewardTxHash(tx.Id)
	}
	if txHash == "" {
		return false
	}
	if user.RewardedTransfers[txHash] {
		return false
	}

	day := dayKeyFromUnix(tx.Timestamp)
	pm.ensureDailyCounters(user, day)

	// Mark processed first for idempotency even if this transfer gets 0 points.
	user.RewardedTransfers[txHash] = true

	if user.DailyTransferCount >= TransferDailyCountCap {
		return true
	}
	if tx.To == "" || strings.EqualFold(tx.From, tx.To) {
		return true
	}

	amount := coremath.ParseBigInt(tx.Amount)
	if amount.Sign() <= 0 || amount.Cmp(minTransferWei) < 0 {
		return true
	}

	points := TransferPointsFull
	if user.DailyTransferCount >= TransferFullTierCount {
		points = TransferPointsReduced
	}
	awarded := pm.addPointsWithWalletCap(user, points)
	user.DailyTransferCount++
	if awarded > 0 {
		pm.updateStreak(user, day)
	}
	return true
}

func dayKeyFromUnix(ts int64) string {
	if ts <= 0 {
		return time.Now().UTC().Format("2006-01-02")
	}
	return time.Unix(ts, 0).UTC().Format("2006-01-02")
}

func normalizeAddress(address string) string {
	return strings.ToLower(strings.TrimSpace(address))
}

func normalizeRewardTxHash(hash string) string {
	hash = strings.TrimSpace(strings.ToLower(hash))
	return strings.TrimPrefix(hash, "0x")
}

func mustBigInt(value string) *big.Int {
	value = strings.TrimSpace(value)
	if value == "" {
		return big.NewInt(0)
	}
	n, ok := new(big.Int).SetString(value, 10)
	if !ok {
		return big.NewInt(0)
	}
	return n
}
