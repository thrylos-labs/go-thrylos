package migration

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"sort"
	"time"

	"github.com/thrylos-labs/go-thrylos/api"
	"github.com/thrylos-labs/go-thrylos/core/state"
)

const (
	conversionDoneKey = "points_conversion_done"

	// RequiredApprovals is the minimum number of distinct approver signatures
	// required before the migration may execute.
	RequiredApprovals = 3

	// ApprovalWindowHours is how long an approval manifest remains valid.
	// Approvers must sign within this window to prevent stale authorisations
	// being replayed at a later date.
	ApprovalWindowHours = 48
)

// ---------------------------------------------------------------------------
// Approval manifest
// ---------------------------------------------------------------------------

// ApprovalManifest is the document that authorised signers review and sign
// before the migration runs. It commits to every parameter that will be used,
// so a signer cannot be tricked by a bait-and-switch on the command line.
type ApprovalManifest struct {
	// Immutable migration parameters — must match what is passed to ConvertPointsToThrylos.
	SnapshotHash    string  `json:"snapshot_hash"`    // SHA-256 of the raw snapshot file
	ConversionRatio float64 `json:"conversion_ratio"` // points * ratio = THRYLOS
	CapNanoThrylos  string  `json:"cap_nano_thrylos"` // maximum total nanoThrylos to distribute
	CreatedAt       int64   `json:"created_at"`       // unix timestamp when manifest was created
	ExpiresAt       int64   `json:"expires_at"`       // unix timestamp after which manifest is invalid
}

// ManifestDigest returns the canonical bytes that approvers sign.
func (m *ApprovalManifest) ManifestDigest() []byte {
	// Deterministic JSON: sort keys, no trailing whitespace.
	raw, _ := json.Marshal(m)
	digest := sha256.Sum256(raw)
	return digest[:]
}

// Approval is one approver's signature over the manifest.
type Approval struct {
	ApproverAddress string `json:"approver_address"` // hex-encoded Ethereum-style address
	Signature       string `json:"signature"`        // hex-encoded DER ECDSA signature over ManifestDigest()
}

// ApprovalBundle is what is written to disk and loaded at migration time.
type ApprovalBundle struct {
	Manifest  ApprovalManifest `json:"manifest"`
	Approvals []Approval       `json:"approvals"`
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// ConvertPointsToThrylos migrates testnet points to real THRYLOS balances.
//
// Security controls:
//  1. One-time flag — cannot be run twice.
//  2. Approval bundle — requires at least RequiredApprovals valid ECDSA signatures
//     from distinct, whitelisted approver addresses over a manifest that commits
//     to every migration parameter (snapshot hash, ratio, cap).
//  3. Manifest freshness — the bundle must not be older than ApprovalWindowHours.
//  4. Parameter binding — the snapshot hash, ratio, and cap are verified against
//     the signed manifest; any discrepancy aborts immediately.
//  5. Cap enforcement — total distribution may never exceed capNano.
//  6. Dry-run mode — pass dryRun=true to calculate allocations without writing
//     any state, allowing off-chain auditing before execution.
func ConvertPointsToThrylos(
	snapshotPath string,
	ratioThrylos float64,
	capNano *big.Int,
	ws *state.WorldState,
	approvalBundlePath string,
	authorisedApprovers []*ecdsa.PublicKey,
	dryRun bool,
) error {

	// 1. Guard: refuse to run twice.
	done, err := ws.GetMetadata(conversionDoneKey)
	if err != nil {
		return fmt.Errorf("failed to check conversion flag: %w", err)
	}
	if done == "true" {
		return fmt.Errorf("points conversion already completed — cannot run twice")
	}

	// 2. Load and hash snapshot (must happen before bundle check so we can
	//    verify the manifest committed to the exact same file).
	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		return fmt.Errorf("failed to read snapshot: %w", err)
	}
	rawHash := sha256.Sum256(data)
	snapshotHash := hex.EncodeToString(rawHash[:])

	// 3. Load approval bundle.
	bundleData, err := os.ReadFile(approvalBundlePath)
	if err != nil {
		return fmt.Errorf("failed to read approval bundle: %w", err)
	}
	var bundle ApprovalBundle
	if err := json.Unmarshal(bundleData, &bundle); err != nil {
		return fmt.Errorf("failed to parse approval bundle: %w", err)
	}

	// 4. Verify manifest freshness.
	now := time.Now().Unix()
	if now > bundle.Manifest.ExpiresAt {
		return fmt.Errorf(
			"approval bundle has expired (expired %s, now %s) — obtain fresh signatures",
			time.Unix(bundle.Manifest.ExpiresAt, 0).UTC().Format(time.RFC3339),
			time.Unix(now, 0).UTC().Format(time.RFC3339),
		)
	}

	// 5. Verify manifest binds to the exact parameters we received.
	if bundle.Manifest.SnapshotHash != snapshotHash {
		return fmt.Errorf(
			"snapshot file hash mismatch: manifest signed over %s, actual file is %s — "+
				"snapshot has been modified since approvers signed",
			bundle.Manifest.SnapshotHash, snapshotHash,
		)
	}
	if bundle.Manifest.ConversionRatio != ratioThrylos {
		return fmt.Errorf(
			"conversion ratio mismatch: manifest signed %v, invocation uses %v",
			bundle.Manifest.ConversionRatio, ratioThrylos,
		)
	}
	if bundle.Manifest.CapNanoThrylos != capNano.String() {
		return fmt.Errorf(
			"cap mismatch: manifest signed %s, invocation uses %s",
			bundle.Manifest.CapNanoThrylos, capNano.String(),
		)
	}

	// 6. Verify signatures and count distinct approvals.
	digest := bundle.Manifest.ManifestDigest()
	validApprovals, err := countValidApprovals(bundle.Approvals, digest, authorisedApprovers)
	if err != nil {
		return fmt.Errorf("approval verification failed: %w", err)
	}
	if validApprovals < RequiredApprovals {
		return fmt.Errorf(
			"insufficient approvals: need %d, have %d valid signatures from whitelisted approvers",
			RequiredApprovals, validApprovals,
		)
	}
	fmt.Printf("✅ %d/%d approvals verified\n", validApprovals, RequiredApprovals)

	// 7. Parse snapshot.
	var users map[string]*api.UserActivity
	if err := json.Unmarshal(data, &users); err != nil {
		return fmt.Errorf("failed to parse snapshot: %w", err)
	}

	// 8. Calculate allocations.
	baseUnit := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	allocations := make(map[string]*big.Int)
	totalDistributed := big.NewInt(0)

	// Process in deterministic order for auditability.
	addrs := make([]string, 0, len(users))
	for addr := range users {
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)

	for _, addr := range addrs {
		user := users[addr]
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

	// 9. Enforce cap before touching any state.
	if totalDistributed.Cmp(capNano) > 0 {
		return fmt.Errorf(
			"conversion would distribute %s nanoThrylos, exceeds cap of %s — aborting",
			totalDistributed.String(), capNano.String(),
		)
	}

	fmt.Printf("📊 Migration summary: %d accounts, %s nanoThrylos total\n",
		len(allocations), totalDistributed.String())

	// 10. Dry-run exits here — no state mutations.
	if dryRun {
		fmt.Println("🔍 DRY RUN complete — no state has been modified. " +
			"Review the summary above before running without --dry-run.")
		return nil
	}

	// 11. Credit accounts.
	for _, addr := range addrs {
		amount, ok := allocations[addr]
		if !ok {
			continue
		}
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

	// 12. Set one-time flag only after all accounts are credited.
	if err := ws.SetMetadata(conversionDoneKey, "true"); err != nil {
		return fmt.Errorf("failed to set conversion flag: %w", err)
	}

	return nil
}

// GenerateApprovalManifest creates an ApprovalManifest from the migration
// parameters. Approvers call this (or an equivalent trusted tool) to produce
// the document they sign, then submit their Approval structs to be bundled.
func GenerateApprovalManifest(snapshotPath string, ratioThrylos float64, capNano *big.Int) (*ApprovalManifest, error) {
	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read snapshot for manifest generation: %w", err)
	}
	rawHash := sha256.Sum256(data)
	now := time.Now()
	return &ApprovalManifest{
		SnapshotHash:    hex.EncodeToString(rawHash[:]),
		ConversionRatio: ratioThrylos,
		CapNanoThrylos:  capNano.String(),
		CreatedAt:       now.Unix(),
		ExpiresAt:       now.Add(ApprovalWindowHours * time.Hour).Unix(),
	}, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// countValidApprovals verifies each Approval signature against digest and the
// whitelist of authorised approver public keys. It returns the number of
// distinct approvers whose signatures are valid. Duplicate approver addresses
// are counted only once.
func countValidApprovals(
	approvals []Approval,
	digest []byte,
	authorised []*ecdsa.PublicKey,
) (int, error) {
	seen := make(map[string]struct{})
	count := 0

	for _, approval := range approvals {
		if _, dup := seen[approval.ApproverAddress]; dup {
			return 0, fmt.Errorf("duplicate approval from address %s", approval.ApproverAddress)
		}
		seen[approval.ApproverAddress] = struct{}{}

		sigBytes, err := hex.DecodeString(approval.Signature)
		if err != nil {
			return 0, fmt.Errorf("invalid signature encoding for %s: %w",
				approval.ApproverAddress, err)
		}

		matched := false
		for _, pubKey := range authorised {
			if verifySignature(pubKey, digest, sigBytes, approval.ApproverAddress) {
				matched = true
				break
			}
		}
		if matched {
			count++
		}
		// Unknown approvers are silently skipped (not counted).
	}
	return count, nil
}

// verifySignature checks that sigBytes is a valid ECDSA signature over digest
// from pubKey and that pubKey corresponds to expectedAddress.
// Returns false (rather than erroring) on any mismatch, so callers can try
// multiple keys without aborting.
func verifySignature(pubKey *ecdsa.PublicKey, digest, sigBytes []byte, expectedAddress string) bool {
	// Derive the Ethereum-style address from the public key (keccak256 of pubkey bytes, last 20).
	// NOTE: replace this stub with your project's actual address derivation.
	addr := deriveAddress(pubKey)
	if addr != expectedAddress {
		return false
	}
	return ecdsa.VerifyASN1(pubKey, digest, sigBytes)
}

// deriveAddress is a stub — replace with the project's canonical address
// derivation function (typically keccak256(pubkey bytes)[12:] formatted as hex).
func deriveAddress(pub *ecdsa.PublicKey) string {
	// Placeholder: real implementation lives in crypto/address/address.go
	_ = pub
	return ""
}
