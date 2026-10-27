package audit

import (
	"errors"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/db/auditchain"
	"github.com/jlentink/bekup/internal/logging"
)

func TestActionCatalog(t *testing.T) {
	seen := map[Action]bool{}
	for _, a := range Actions() {
		if !actionRe.MatchString(string(a)) || len(a) > 128 {
			t.Errorf("%q: bad action syntax", a)
		}
		if seen[a] {
			t.Errorf("%q: duplicate", a)
		}
		seen[a] = true
		if !a.Valid() {
			t.Errorf("%q: not valid", a)
		}
	}
	for _, a := range []Action{"", "auth", "auth.login.unknown", "Auth.Login"} {
		if a.Valid() {
			t.Errorf("%q: valid", a)
		}
	}
	for _, a := range []Action{ActionAuthLoginSucceeded, ActionAuthLoginFailed, ActionAuthLogout, ActionUserCreated,
		ActionUserRoleChanged, ActionSessionRevoked, ActionSecretChanged, ActionSettingChanged,
		ActionCLIAdminCreated, ActionKeysRotated, ActionCertsRotated} {
		if !seen[a] {
			t.Errorf("%q missing from the catalog", a)
		}
	}
}

func TestRedact(t *testing.T) {
	reg := logging.NewRegistry()
	reg.Register([]byte("hunter2-registered"))
	type nested struct {
		APIToken string `json:"api_token"`
		Name     string `json:"name"`
	}
	tests := []struct {
		name    string
		in      Details
		want    string
		wantErr error
	}{
		{"empty", nil, `{}`, nil},
		{"secret keys", Details{"password": "p", "newPassword": "x", "key_id": "k1", "master_key_file": "/f", "user": "bob"},
			`{"key_id":"k1","master_key_file":"/f","newPassword":"[REDACTED]","password":"[REDACTED]","user":"bob"}`, nil},
		{"nested and struct", Details{"a": []any{map[string]any{"secret": 1}, nested{APIToken: "t", Name: "n"}}},
			`{"a":[{"secret":"[REDACTED]"},{"api_token":"[REDACTED]","name":"n"}]}`, nil},
		{"registered value", Details{"note": "pw is hunter2-registered!"}, `{"note":"pw is [REDACTED]!"}`, nil},
		{"url credentials", Details{"target": "postgres://u:pa55@db/x"}, `{"target":"postgres://u:[REDACTED]@db/x"}`, nil},
		{"nul", Details{"s": "a\x00b"}, `{"s":"a�b"}`, nil},
		{"too large", Details{"s": strings.Repeat("x", MaxDetailsBytes)}, "", ErrDetailsTooLarge},
		{"unencodable", Details{"f": func() {}}, "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Redact(tt.in, reg)
			if tt.name == "unencodable" {
				if err == nil {
					t.Fatal("want error")
				}
				return
			}
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Fatalf("got %s\nwant %s", got, tt.want)
			}
		})
	}
	deep := Details{}
	cur := map[string]any(deep)
	for range maxDetailsDepth + 2 {
		next := map[string]any{}
		cur["n"] = next
		cur = next
	}
	if _, err := Redact(deep, reg); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("deep nesting: %v", err)
	}
}

func TestParams(t *testing.T) {
	a := New(nil, Options{Registry: logging.NewRegistry()})
	uid := uuid.New()
	ip := netip.MustParseAddr("::ffff:10.0.0.1")
	p, err := a.params(Event{Actor: UserActor(uid), Action: ActionUserCreated, TargetType: "user", TargetID: "x", IP: ip,
		Details: Details{"token": "abc"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Ip.String() != "10.0.0.1" || *p.TargetType != "user" || string(p.Details) != `{"token":"[REDACTED]"}` || *p.ActorUserID != uid {
		t.Fatalf("params: %+v", p)
	}
	bad := []Event{
		{Actor: SystemActor(), Action: "nope.nope"},
		{Actor: Actor{Kind: ActorUser}, Action: ActionUserCreated},
		{Actor: Actor{Kind: ActorSystem, UserID: &uid}, Action: ActionUserCreated},
		{Actor: Actor{Kind: "robot"}, Action: ActionUserCreated},
		{Actor: SystemActor(), Action: ActionUserCreated, TargetType: strings.Repeat("t", 129)},
		{Actor: SystemActor(), Action: ActionUserCreated, TargetID: strings.Repeat("t", 257)},
	}
	for i, ev := range bad {
		if _, err := a.params(ev); !errors.Is(err, ErrInvalidEvent) {
			t.Errorf("bad[%d]: %v", i, err)
		}
	}
}

func TestCLIEvent(t *testing.T) {
	ev := CLIEvent(ActionCLIAdminCreated, "user", "u1", Details{"email": "a@b.c"})
	if ev.Actor.Kind != ActorCLI || ev.Details["email"] != "a@b.c" {
		t.Fatalf("%+v", ev)
	}
	if _, ok := ev.Details[DetailHostname]; !ok {
		t.Error("hostname missing")
	}
	if _, ok := ev.Details[DetailOSUser]; !ok {
		t.Error("os user missing")
	}
}

func TestHeadSignatureAndFile(t *testing.T) {
	k, err := crypto.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	hash := make([]byte, 32)
	hash[0] = 7
	at := time.Date(2026, 10, 26, 1, 2, 3, 456789000, time.UTC)
	msg := HeadMessage(42, hash, at)
	if want := "bekup-audit-head-v1\nchain_id=42\nchain_hash=07" + strings.Repeat("0", 62) + "\nsigned_at=1792976523456789\n"; string(msg) != want {
		t.Fatalf("message %q", msg)
	}
	sig, err := k.Sign(HeadPurpose, msg)
	if err != nil {
		t.Fatal(err)
	}
	h := SignedHead{Version: HeadVersion, ChainID: 42, ChainHash: hash, SignedAt: at, KeyID: k.ID, PublicKey: HexBytes(k.Public), Signature: sig}
	if err := h.VerifySignature(k.Public); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "head.json")
	if err := (FileExporter{Path: path}).ExportHead(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	got, err := ReadHeadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := got.VerifySignature(k.Public); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	// Any change breaks the signature; a manifest-purpose signature is rejected.
	for name, mut := range map[string]func(*SignedHead){
		"id":      func(h *SignedHead) { h.ChainID = 43 },
		"hash":    func(h *SignedHead) { h.ChainHash = make([]byte, 32) },
		"time":    func(h *SignedHead) { h.SignedAt = h.SignedAt.Add(time.Microsecond) },
		"version": func(h *SignedHead) { h.Version = "x" },
		"purpose": func(h *SignedHead) {
			s, err := k.Sign("manifest", HeadMessage(h.ChainID, h.ChainHash, h.SignedAt))
			if err != nil {
				t.Fatal(err)
			}
			h.Signature = s
		},
	} {
		c := got
		mut(&c)
		if c.VerifySignature(k.Public) == nil {
			t.Errorf("%s: tampered head verified", name)
		}
	}
	other, err := crypto.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	if got.VerifySignature(other.Public) == nil {
		t.Error("verified with another key")
	}
}

func TestCheckRow(t *testing.T) {
	r1 := auditchain.Row{ID: 1, TS: time.Unix(1, 0), ActorKind: "system", Action: "a.b", Details: "{}", PrevHash: auditchain.GenesisPrevHash}
	h1 := auditchain.Hash(&r1)
	if b := checkRow(&r1, h1, auditchain.GenesisPrevHash, 0); b != nil {
		t.Fatal(b)
	}
	r3 := auditchain.Row{ID: 3, TS: time.Unix(3, 0), ActorKind: "system", Action: "a.b", Details: "{}", PrevHash: make([]byte, 32)}
	r3.PrevHash[0] = 9
	h3 := auditchain.Hash(&r3)
	if b := checkRow(&r3, h3, h1, 1); b == nil || b.Kind != BreakLink || !strings.Contains(b.Reason, "missing") {
		t.Fatalf("gap: %+v", b)
	}
	if b := checkRow(&r3, h3, auditchain.GenesisPrevHash, 0); b == nil || b.Kind != BreakGenesis {
		t.Fatalf("genesis: %+v", b)
	}
	if b := checkRow(&r1, h3, auditchain.GenesisPrevHash, 0); b == nil || b.Kind != BreakHash {
		t.Fatalf("hash: %+v", b)
	}
	rep := Report{Broken: &Break{ID: 3, Kind: BreakHash}}
	if !errors.Is(rep.Err(), ErrTampered) {
		t.Fatal("Err")
	}
}
