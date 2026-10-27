package passkey

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth/passkey/passkeytest"
)

func TestRelyingPartyFor(t *testing.T) {
	cases := []struct {
		base    string
		dev     bool
		ok      bool
		id      string
		origins []string
		reason  string
	}{
		{"https://backup.example.com", false, true, "backup.example.com", []string{"https://backup.example.com"}, ""},
		{"https://Backup.Example.com:8443/ui/", false, true, "backup.example.com", []string{"https://backup.example.com:8443"}, ""},
		{"https://backup.example.com:443", false, true, "backup.example.com", []string{"https://backup.example.com"}, ""},
		{"https://backup.example.com", true, true, "backup.example.com", []string{"https://backup.example.com"}, ""},
		{"http://localhost:8080", false, true, "localhost", []string{"http://localhost:8080"}, ""},
		{"http://localhost:8080", true, true, "localhost", []string{"http://localhost:8080", DevOrigin}, ""},
		{"http://localhost:5173", true, true, "localhost", []string{DevOrigin}, ""},
		{"http://bekup.localhost", false, true, "bekup.localhost", []string{"http://bekup.localhost"}, ""},
		{"http://backup.example.com", false, false, "", nil, ReasonInsecureContext},
		{"http://backup.example.com", true, false, "", nil, ReasonInsecureContext},
		{"http://127.0.0.1:8080", true, false, "", nil, ReasonInsecureContext},
		{"https://192.0.2.1", false, false, "", nil, ReasonIPHost},
		{"https://[2001:db8::1]", false, false, "", nil, ReasonIPHost},
		{"ftp://backup.example.com", false, false, "", nil, ReasonInvalidURL},
		{"::not a url", false, false, "", nil, ReasonInvalidURL},
		{"", false, false, "", nil, ReasonInvalidURL},
	}
	for _, tc := range cases {
		rp := RelyingPartyFor(tc.base, tc.dev)
		if rp.Available != tc.ok || rp.ID != tc.id || !slices.Equal(rp.Origins, tc.origins) || rp.Reason != tc.reason {
			t.Errorf("RelyingPartyFor(%q, %v) = %+v", tc.base, tc.dev, rp)
		}
	}
}

func TestValidateName(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"  YubiKey 5  ", "YubiKey 5", true},
		{"Laptop 🔑", "Laptop 🔑", true},
		{strings.Repeat("é", 64), strings.Repeat("é", 64), true},
		{strings.Repeat("a", 65), "", false},
		{"   ", "", false},
		{"bad\x00name", "", false},
		{"tab\tname", "", false},
		{"\xff", "", false},
	} {
		got, err := ValidateName(tc.in)
		var ne *NameError
		if (err == nil) != tc.ok || got != tc.want || (err != nil && !errors.As(err, &ne)) {
			t.Errorf("ValidateName(%q) = %q, %v", tc.in, got, err)
		}
	}
}

func TestValidateResponseJSON(t *testing.T) {
	for _, tc := range []struct {
		in string
		ok bool
	}{
		{`{"id":"x"}`, true},
		{` {"id":"x"} `, true},
		{`[]`, false},
		{`"x"`, false},
		{`{"id":`, false},
		{``, false},
		{`{"a":"` + strings.Repeat("x", MaxResponseBytes) + `"}`, false},
	} {
		if err := ValidateResponseJSON([]byte(tc.in)); (err == nil) != tc.ok {
			t.Errorf("ValidateResponseJSON(%.20q) = %v", tc.in, err)
		}
	}
}

// ceremony runs the library part of the ceremonies (what Service does
// around the DB) against the virtual authenticator.
type ceremony struct {
	t    *testing.T
	wa   *webauthn.WebAuthn
	user *waUser
}

func newCeremony(t *testing.T, base string, dev bool) *ceremony {
	t.Helper()
	rp := RelyingPartyFor(base, dev)
	if !rp.Available {
		t.Fatalf("RP unavailable: %s", rp.Reason)
	}
	wa, err := newWebAuthn(rp)
	if err != nil {
		t.Fatal(err)
	}
	return &ceremony{t: t, wa: wa, user: &waUser{id: uuid.New(), name: "a@example.com", display: "A"}}
}

func (c *ceremony) register(a *passkeytest.Authenticator) (*webauthn.Credential, error) {
	c.t.Helper()
	creation, sd, err := c.wa.BeginRegistration(c.user,
		webauthn.WithExclusions(webauthn.Credentials(c.user.creds).CredentialDescriptors()),
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation))
	if err != nil {
		c.t.Fatal(err)
	}
	opts, err := json.Marshal(creation)
	if err != nil {
		c.t.Fatal(err)
	}
	resp, err := a.Register(opts)
	if err != nil {
		c.t.Fatal(err)
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(resp)
	if err != nil {
		return nil, err
	}
	return c.wa.CreateCredential(c.user, *sd, parsed)
}

func (c *ceremony) assert(a *passkeytest.Authenticator) (*webauthn.Credential, error) {
	c.t.Helper()
	assertion, sd, err := c.wa.BeginLogin(c.user, webauthn.WithUserVerification(UserVerification))
	if err != nil {
		c.t.Fatal(err)
	}
	opts, err := json.Marshal(assertion)
	if err != nil {
		c.t.Fatal(err)
	}
	resp, err := a.Assert(opts)
	if err != nil {
		c.t.Fatal(err)
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(resp)
	if err != nil {
		return nil, err
	}
	return c.wa.ValidateLogin(c.user, *sd, parsed)
}

func TestCreationOptions(t *testing.T) {
	c := newCeremony(t, "https://backup.example.com", false)
	creation, sd, err := c.wa.BeginRegistration(c.user,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation))
	if err != nil {
		t.Fatal(err)
	}
	o := creation.Response
	if o.RelyingParty.ID != "backup.example.com" || o.Attestation != protocol.PreferNoAttestation ||
		o.AuthenticatorSelection.ResidentKey != protocol.ResidentKeyRequirementPreferred ||
		o.AuthenticatorSelection.UserVerification != protocol.VerificationPreferred ||
		o.Timeout != int(CeremonyTimeout.Milliseconds()) || len(o.Challenge) < 16 {
		t.Fatalf("unexpected creation options: %+v", o)
	}
	if sd.Expires.IsZero() {
		t.Fatal("the session data has no server-side expiry")
	}
}

func TestRoundTrip(t *testing.T) {
	c := newCeremony(t, "https://backup.example.com", false)
	a, err := passkeytest.New("https://backup.example.com")
	if err != nil {
		t.Fatal(err)
	}
	cred, err := c.register(a)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if cred.AttestationFormat != "none" || !cred.Flags.UserVerified || cred.Authenticator.SignCount != 0 {
		t.Fatalf("unexpected credential: %+v", cred)
	}
	c.user.creds = append(c.user.creds, *cred)
	got, err := c.assert(a)
	if err != nil {
		t.Fatalf("assert: %v", err)
	}
	if got.Authenticator.CloneWarning || got.Authenticator.SignCount != 1 {
		t.Fatalf("unexpected assertion state: %+v", got.Authenticator)
	}
	// Excluded on a second registration of the same authenticator is a
	// browser concern; the options must list it.
	creation, _, err := c.wa.BeginRegistration(c.user,
		webauthn.WithExclusions(webauthn.Credentials(c.user.creds).CredentialDescriptors()))
	if err != nil || len(creation.Response.CredentialExcludeList) != 1 {
		t.Fatalf("exclude list: %v %+v", err, creation.Response.CredentialExcludeList)
	}
}

func TestWrongOriginAndRPID(t *testing.T) {
	c := newCeremony(t, "https://backup.example.com", false)
	evil, err := passkeytest.New("https://evil.example.net")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.register(evil); err == nil {
		t.Fatal("registration from a wrong origin accepted")
	}
	wrongRP, err := passkeytest.New("https://backup.example.com")
	if err != nil {
		t.Fatal(err)
	}
	wrongRP.RPID = "example.com"
	if _, err := c.register(wrongRP); err == nil {
		t.Fatal("registration with a wrong RP ID accepted")
	}

	good, err := passkeytest.New("https://backup.example.com")
	if err != nil {
		t.Fatal(err)
	}
	cred, err := c.register(good)
	if err != nil {
		t.Fatal(err)
	}
	c.user.creds = append(c.user.creds, *cred)
	good.Origin = "https://evil.example.net"
	if _, err := c.assert(good); err == nil {
		t.Fatal("assertion from a wrong origin accepted")
	}
	good.Origin = "https://backup.example.com"
	good.RPID = "evil.example.net"
	if _, err := c.assert(good); err == nil {
		t.Fatal("assertion with a wrong RP ID accepted")
	}
	// The Vite origin is only accepted in dev mode on localhost.
	vite, err := passkeytest.New(DevOrigin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.register(vite); err == nil {
		t.Fatal("Vite origin accepted outside dev mode")
	}
}

func TestDevOrigin(t *testing.T) {
	c := newCeremony(t, "http://localhost:8080", true)
	vite, err := passkeytest.New(DevOrigin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.register(vite); err != nil {
		t.Fatalf("Vite origin refused in dev mode: %v", err)
	}
	c = newCeremony(t, "http://localhost:8080", false)
	if _, err := c.register(vite); err == nil {
		t.Fatal("Vite origin accepted outside dev mode")
	}
}

func TestCloneWarning(t *testing.T) {
	c := newCeremony(t, "https://backup.example.com", false)
	a, err := passkeytest.New("https://backup.example.com")
	if err != nil {
		t.Fatal(err)
	}
	cred, err := c.register(a)
	if err != nil {
		t.Fatal(err)
	}
	clone := a.Clone()
	cred.Authenticator.SignCount = 5
	c.user.creds = []webauthn.Credential{*cred}
	a.SignCount = 5
	got, err := c.assert(a) // 6 > 5
	if err != nil || got.Authenticator.CloneWarning {
		t.Fatalf("forward counter: %v %+v", err, got)
	}
	got, err = c.assert(clone) // 1 <= 5
	if err != nil || !got.Authenticator.CloneWarning {
		t.Fatalf("regressed counter not flagged: %v %+v", err, got)
	}
	// An authenticator without a counter (0 on both sides) is fine.
	zero, err := passkeytest.New("https://backup.example.com")
	if err != nil {
		t.Fatal(err)
	}
	zero.CounterStep = 0
	zc, err := c.register(zero)
	if err != nil {
		t.Fatal(err)
	}
	c.user.creds = []webauthn.Credential{*zc}
	for range 2 {
		got, err := c.assert(zero)
		if err != nil || got.Authenticator.CloneWarning {
			t.Fatalf("zero counter flagged: %v %+v", err, got)
		}
	}
}
