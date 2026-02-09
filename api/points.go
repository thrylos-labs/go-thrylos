package api

import (
	"encoding/json"
	"os"
	"sort"
	"sync"
	"time"
)

// --- CONFIGURATION ---
const (
	// Action Points
	PointsFaucet         = 100 // Daily
	PointsBaseTx         = 10  // Per Tx
	PointsUniqueReceiver = 50  // Bonus for sending to a NEW person
	PointsDelegate       = 500 // One-time bonus for first delegation
	PointsStreakBonus    = 50  // Extra per day of streak

	// Caps
	MaxDailyTxPoints = 200 // Cap spamming txs to ~20 per day
)

// --- DATA STRUCTURES ---

type UserActivity struct {
	Address     string `json:"address"`
	TotalPoints int    `json:"total_points"`

	// Faucet Tracking
	LastFaucet time.Time `json:"last_faucet"`

	// Transaction Tracking
	DailyTxPoints      int             `json:"daily_tx_points"`     // Resets daily
	LastActiveDate     string          `json:"last_active_date"`    // YYYY-MM-DD
	UniqueInteractions map[string]bool `json:"unique_interactions"` // Set of addresses sent to

	// Staking/Delegation
	HasDelegated bool `json:"has_delegated"`

	// Retention
	CurrentStreak int `json:"current_streak"`
	MaxStreak     int `json:"max_streak"`
}

type PointsManager struct {
	mu       sync.RWMutex
	Users    map[string]*UserActivity `json:"users"`
	FilePath string
}

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

// AwardFaucet: +100 Points (24h cooldown)
func (pm *PointsManager) AwardFaucet(address string) (int, bool) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	user := pm.getOrCreate(address)

	if time.Since(user.LastFaucet) < 24*time.Hour {
		return user.TotalPoints, false
	}

	user.TotalPoints += PointsFaucet
	user.LastFaucet = time.Now()

	// Update streak since they are active
	pm.updateStreak(user)

	pm.save()
	return user.TotalPoints, true
}

// RecordTransaction: Handles Base Tx, Unique Receiver, and Daily Caps
func (pm *PointsManager) RecordTransaction(from, to string) int {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	user := pm.getOrCreate(from)
	today := time.Now().Format("2006-01-02")

	// 1. Reset Daily Counters if new day
	if user.LastActiveDate != today {
		user.DailyTxPoints = 0
		user.LastActiveDate = today
		pm.updateStreak(user)
	}

	// 2. Check Daily Cap
	if user.DailyTxPoints >= MaxDailyTxPoints {
		return user.TotalPoints // Cap reached, no points
	}

	// 3. Calculate Points for this Tx
	pointsEarned := PointsBaseTx

	// 4. Bonus: Unique Receiver (Velocity)
	// Don't reward sending to self or empty
	if to != "" && to != from {
		if user.UniqueInteractions == nil {
			user.UniqueInteractions = make(map[string]bool)
		}
		if !user.UniqueInteractions[to] {
			pointsEarned += PointsUniqueReceiver
			user.UniqueInteractions[to] = true
		}
	}

	// 5. Apply Points
	user.TotalPoints += pointsEarned
	user.DailyTxPoints += pointsEarned

	pm.save()
	return user.TotalPoints
}

// RecordDelegation: +500 Points (One-time)
func (pm *PointsManager) RecordDelegation(address string) int {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	user := pm.getOrCreate(address)

	if !user.HasDelegated {
		user.TotalPoints += PointsDelegate
		user.HasDelegated = true
		pm.updateStreak(user) // Counts as activity
		pm.save()
	}

	return user.TotalPoints
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

	// Convert map to slice
	var entries []LeaderboardEntry
	for addr, u := range pm.Users {
		entries = append(entries, LeaderboardEntry{Address: addr, Points: u.TotalPoints})
	}

	// Sort Descending
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Points > entries[j].Points
	})

	// Slice top N
	if limit > len(entries) {
		limit = len(entries)
	}

	// Assign Ranks
	result := entries[:limit]
	for i := range result {
		result[i].Rank = i + 1
	}

	return result
}

func (pm *PointsManager) GetUserPoints(address string) *UserActivity {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if user, exists := pm.Users[address]; exists {
		return user
	}
	return &UserActivity{Address: address, TotalPoints: 0}
}

// Internal Helper: Update Streak Logic
func (pm *PointsManager) updateStreak(user *UserActivity) {
	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	// If already active today, do nothing
	if user.LastActiveDate == today {
		return
	}

	if user.LastActiveDate == yesterday {
		// Continued streak
		user.CurrentStreak++
		// Bonus Points for Streak
		user.TotalPoints += PointsStreakBonus
	} else {
		// Broke streak (or first time)
		user.CurrentStreak = 1
	}

	if user.CurrentStreak > user.MaxStreak {
		user.MaxStreak = user.CurrentStreak
	}

	user.LastActiveDate = today
}

func (pm *PointsManager) getOrCreate(address string) *UserActivity {
	if _, exists := pm.Users[address]; !exists {
		pm.Users[address] = &UserActivity{Address: address}
	}
	return pm.Users[address]
}

// Persistence
func (pm *PointsManager) save() {
	data, _ := json.MarshalIndent(pm.Users, "", "  ")
	_ = os.WriteFile(pm.FilePath, data, 0644)
}

func (pm *PointsManager) load() {
	data, err := os.ReadFile(pm.FilePath)
	if err == nil {
		_ = json.Unmarshal(data, &pm.Users)
	}
}
