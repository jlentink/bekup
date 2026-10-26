package auditchain

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

// goldenRow and goldenHash pin the canonical serialization v1. The
// integration test TestCanonicalGolden checks that Postgres'
// audit_log_canonical() produces the same hash for this row.
func goldenRow() Row {
	uid := uuid.MustParse("0b9f6f3e-6d2a-4c1e-9a53-3e0d2c1b4a59")
	return Row{
		ID:          42,
		TS:          time.Date(2026, 10, 26, 12, 34, 56, 789012000, time.UTC),
		ActorUserID: &uid,
		ActorKind:   "user",
		Action:      "job.update",
		TargetType:  new("job"),
		TargetID:    nil,
		IP:          new("2001:db8::1"),
		Details:     `{"a": 1, "name": "é"}`,
		PrevHash:    bytes.Repeat([]byte{0x11}, HashSize),
	}
}

const goldenHash = "1772526146dd540a91fc512df6c44de8c15f23ea4cef084dc98137a97039aaeb"

func TestGolden(t *testing.T) {
	r := goldenRow()
	if got := hex.EncodeToString(Hash(&r)); got != goldenHash {
		t.Fatalf("hash = %s, want %s", got, goldenHash)
	}
}

func TestCanonicalLayout(t *testing.T) {
	r := Row{ID: 1, TS: time.UnixMicro(1_000_001), ActorKind: "cli", Action: "x", Details: "{}", PrevHash: GenesisPrevHash}
	got := Canonical(&r)
	var want bytes.Buffer
	want.WriteString("bekup-audit-v1")
	want.Write([]byte{1, 0, 0, 0, 32})
	want.Write(GenesisPrevHash)
	want.Write([]byte{1, 0, 0, 0, 1, '1'})
	want.Write([]byte{1, 0, 0, 0, 7})
	want.WriteString("1000001")
	want.WriteByte(0) // actor_user_id NULL
	want.Write([]byte{1, 0, 0, 0, 3})
	want.WriteString("cli")
	want.Write([]byte{1, 0, 0, 0, 1, 'x'})
	want.Write([]byte{0, 0, 0}) // target_type, target_id, ip NULL
	want.Write([]byte{1, 0, 0, 0, 2})
	want.WriteString("{}")
	if !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("canonical =\n%q\nwant\n%q", got, want.Bytes())
	}
}

func TestNullVsEmptyDiffer(t *testing.T) {
	a, b := goldenRow(), goldenRow()
	b.TargetID = new("")
	if bytes.Equal(Hash(&a), Hash(&b)) {
		t.Fatal("NULL and empty string hash identically")
	}
	// Field boundaries are unambiguous.
	c, d := goldenRow(), goldenRow()
	c.ActorKind, c.Action = "user", "x"
	d.ActorKind, d.Action = "use", "rx"
	if bytes.Equal(Hash(&c), Hash(&d)) {
		t.Fatal("moving bytes between fields keeps the hash")
	}
}

func TestVerify(t *testing.T) {
	r := goldenRow()
	h := Hash(&r)
	if err := Verify(&r, r.PrevHash, h); err != nil {
		t.Fatal(err)
	}
	if err := Verify(&r, GenesisPrevHash, h); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("wrong prev: %v", err)
	}
	r.Action = "forged"
	if err := Verify(&r, r.PrevHash, h); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("tampered row: %v", err)
	}
}

func TestFromCanonical(t *testing.T) {
	src := sqlc.ListAuditLogCanonicalRow{ID: 3, ActorKind: "system", Action: "a", HasIp: false, IpText: "", DetailsText: "{}"}
	if r := FromCanonical(&src); r.IP != nil {
		t.Fatal("ip must be NULL when has_ip is false")
	}
	src.HasIp, src.IpText = true, "10.0.0.1"
	if r := FromCanonical(&src); r.IP == nil || *r.IP != "10.0.0.1" {
		t.Fatalf("ip = %v", r.IP)
	}
}
