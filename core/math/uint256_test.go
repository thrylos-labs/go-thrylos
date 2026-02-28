package math

import (
	"bytes"
	"math/big"
	"testing"
)

func TestParseUint256Decimal(t *testing.T) {
	value, err := ParseUint256Decimal("123456789")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value.String() != "123456789" {
		t.Fatalf("expected 123456789, got %s", value.String())
	}
}

func TestParseUint256DecimalRejectsInvalid(t *testing.T) {
	if _, err := ParseUint256Decimal("-1"); err == nil {
		t.Fatal("expected negative uint256 to be rejected")
	}
	if _, err := ParseUint256Decimal("not-a-number"); err == nil {
		t.Fatal("expected invalid uint256 string to be rejected")
	}
}

func TestUint256BytesRoundTrip(t *testing.T) {
	raw, err := BigIntToUint256Bytes(big.NewInt(1024))
	if err != nil {
		t.Fatalf("unexpected encode error: %v", err)
	}

	value, err := ParseUint256Bytes(raw)
	if err != nil {
		t.Fatalf("unexpected decode error: %v", err)
	}
	if value.Cmp(big.NewInt(1024)) != 0 {
		t.Fatalf("expected 1024, got %s", value.String())
	}
}

func TestParseUint256CompatPrefersBytes(t *testing.T) {
	value, err := ParseUint256Compat([]byte{0x2a}, "999")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value.Cmp(big.NewInt(42)) != 0 {
		t.Fatalf("expected 42 from bytes path, got %s", value.String())
	}
}

func TestValidateCanonicalUint256BytesRejectsNonCanonical(t *testing.T) {
	if _, err := ParseUint256Bytes([]byte{0x00, 0x01}); err == nil {
		t.Fatal("expected leading-zero encoding to be rejected")
	}

	oversized := bytes.Repeat([]byte{0x01}, MaxUint256Bytes+1)
	if _, err := ParseUint256Bytes(oversized); err == nil {
		t.Fatal("expected oversized encoding to be rejected")
	}
}

func TestSyncUint256ForWritePreservesLegacyDecimal(t *testing.T) {
	raw := []byte(nil)
	decimal := "001"

	if err := SyncUint256ForWrite(&raw, &decimal); err != nil {
		t.Fatalf("unexpected normalize error: %v", err)
	}
	if decimal != "001" {
		t.Fatalf("expected legacy decimal to be preserved, got %q", decimal)
	}

	value, err := ParseUint256Bytes(raw)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if value.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("expected encoded value 1, got %s", value.String())
	}
}

func TestNormalizeUint256CompatPrefersBytesOnRead(t *testing.T) {
	raw := []byte{0x02}
	decimal := "1"

	if err := NormalizeUint256Compat(&raw, &decimal); err != nil {
		t.Fatalf("unexpected normalize error: %v", err)
	}
	if decimal != "2" {
		t.Fatalf("expected decimal to be healed from bytes, got %q", decimal)
	}
}

func TestValidateUint256CompatRejectsMismatchedRepresentations(t *testing.T) {
	raw := []byte{0x02}
	decimal := "1"

	if _, err := ValidateUint256Compat(raw, decimal); err == nil {
		t.Fatal("expected mismatched representations to be rejected")
	}
}
