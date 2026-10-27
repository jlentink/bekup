package auth

import (
	"crypto/hmac"
	"encoding/base32"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/jlentink/bekup/internal/crypto"
)

var testTOTPSecret = []byte("12345678901234567890") // RFC 6238 test key

// codeAt is the code an authenticator app shows at t.
func codeAt(t *testing.T, secret []byte, at time.Time) string {
	t.Helper()
	c, err := totp.GenerateCodeCustom(base32.StdEncoding.EncodeToString(secret), at, totp.ValidateOpts{
		Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTOTPRFC6238Vector(t *testing.T) {
	// RFC 6238 appendix B, SHA1, T = 59 s: 94287082 (8 digits) → 287082.
	at := time.Unix(59, 0)
	if got := codeAt(t, testTOTPSecret, at); got != "287082" {
		t.Fatalf("code %s", got)
	}
	step, ok := matchTOTP(testTOTPSecret, "287082", at, -1)
	if !ok || step != 1 {
		t.Fatalf("step %d ok %v", step, ok)
	}
}

func TestMatchTOTPSkew(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cur := now.Unix() / 30
	for _, tc := range []struct {
		name   string
		offset time.Duration
		ok     bool
	}{
		{"now", 0, true},
		{"previous step", -30 * time.Second, true},
		{"next step", 30 * time.Second, true},
		{"two steps back", -60 * time.Second, false},
		{"two steps ahead", 60 * time.Second, false},
		{"ten minutes old", -10 * time.Minute, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code := codeAt(t, testTOTPSecret, now.Add(tc.offset))
			step, ok := matchTOTP(testTOTPSecret, code, now, -1)
			if ok != tc.ok {
				t.Fatalf("ok %v, want %v", ok, tc.ok)
			}
			if ok && step != cur+int64(tc.offset/(30*time.Second)) {
				t.Fatalf("step %d", step)
			}
		})
	}
}

func TestMatchTOTPRejectsUsedAndOlderSteps(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cur := now.Unix() / 30
	code := codeAt(t, testTOTPSecret, now)
	if _, ok := matchTOTP(testTOTPSecret, code, now, cur); ok {
		t.Fatal("the last used step was accepted again")
	}
	prev := codeAt(t, testTOTPSecret, now.Add(-30*time.Second))
	if _, ok := matchTOTP(testTOTPSecret, prev, now, cur); ok {
		t.Fatal("a step older than the last used one was accepted")
	}
	next := codeAt(t, testTOTPSecret, now.Add(30*time.Second))
	if s, ok := matchTOTP(testTOTPSecret, next, now, cur); !ok || s != cur+1 {
		t.Fatal("a newer step was refused")
	}
}

func TestMatchTOTPInputs(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	code := codeAt(t, testTOTPSecret, now)
	for _, in := range []string{code[:3] + " " + code[3:], code[:3] + "-" + code[3:], " " + code + " "} {
		if _, ok := matchTOTP(testTOTPSecret, in, now, -1); !ok {
			t.Errorf("%q refused", in)
		}
	}
	for _, in := range []string{"", code[:5], code + "0", "abcdef", code[:5] + "x", "１２３４５６", strings.Repeat("1", 40)} {
		if _, ok := matchTOTP(testTOTPSecret, in, now, -1); ok {
			t.Errorf("%q accepted", in)
		}
	}
	other := []byte("abcdefghijabcdefghij")
	if _, ok := matchTOTP(other, code, now, -1); ok {
		t.Error("code of another secret accepted")
	}
}

func TestQRCodeSVG(t *testing.T) {
	uri := "otpauth://totp/bekup:alice@example.com?algorithm=SHA1&digits=6&issuer=bekup&period=30&secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	svg, err := QRCodeSVG(uri)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(svg), "data:") || strings.Contains(svg, "<script") ||
		strings.Contains(svg, "style") || strings.Contains(svg, "href") {
		t.Fatalf("unsafe SVG: %s", svg)
	}
	// Well-formed XML with exactly svg > rect + path.
	dec := xml.NewDecoder(strings.NewReader(svg))
	var names []string
	var viewBox, d string
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("invalid XML: %v", err)
		}
		if se, ok := tok.(xml.StartElement); ok {
			names = append(names, se.Name.Local)
			for _, a := range se.Attr {
				switch {
				case se.Name.Local == "svg" && a.Name.Local == "viewBox":
					viewBox = a.Value
				case se.Name.Local == "path" && a.Name.Local == "d":
					d = a.Value
				}
			}
		}
	}
	if strings.Join(names, ",") != "svg,rect,path" {
		t.Fatalf("elements %v", names)
	}
	// A QR code is 17 + 4·version modules plus a 4-module quiet zone.
	f := strings.Fields(viewBox)
	if len(f) != 4 {
		t.Fatalf("viewBox %q", viewBox)
	}
	n, err := strconv.Atoi(f[2])
	if err != nil {
		t.Fatal(err)
	}
	if v := (n - 8 - 17); v <= 0 || v%4 != 0 {
		t.Fatalf("size %d is not a QR code size", n)
	}
	if !regexp.MustCompile(`^(M\d+ \d+h\d+v1h-\d+z)+$`).MatchString(d) {
		t.Fatalf("unexpected path data %.80q", d)
	}
	// The three finder patterns: the top-left module row starts with a run
	// of 7 dark modules at the quiet-zone offset.
	if !strings.HasPrefix(d, "M4 4h7v1h-7z") {
		t.Fatalf("no finder pattern: %.40q", d)
	}
	if _, err := QRCodeSVG(""); err == nil {
		t.Fatal("empty content accepted")
	}
	if _, err := QRCodeSVG(strings.Repeat("a", maxQRContent+1)); err == nil {
		t.Fatal("oversized content accepted")
	}
}

func TestNormalizeRecoveryCode(t *testing.T) {
	for in, want := range map[string]string{
		"abcd-efgh-jkmn":    "abcdefghjkmn",
		"ABCD EFGH JKMN":    "abcdefghjkmn",
		"0123-4567-89ab":    "0123456789ab",
		"o1il-4567-89ab":    "0111456789ab",
		" abcdefghjkmn ":    "abcdefghjkmn",
		"ab-cd-ef-gh-jk-mn": "abcdefghjkmn",
	} {
		got, ok := normalizeRecoveryCode(in)
		if !ok || got != want {
			t.Errorf("%q → %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "abcd-efgh-jkm", "abcd-efgh-jkmnp", "abcd-efgh-jkmu", "abcd_efgh_jkmn", "äbcd-efgh-jkmn", strings.Repeat("a", 100)} {
		if _, ok := normalizeRecoveryCode(in); ok {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestNewRecoveryCodes(t *testing.T) {
	kr, err := crypto.GenerateKeyring(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer kr.Destroy()
	svc := &TOTP{o: TOTPOptions{Keyring: kr}}
	user := uuid.New()
	set, err := svc.newRecoveryCodes(user)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.plain) != RecoveryCodeCount || len(set.macs) != RecoveryCodeCount || set.keyID != kr.PrimaryID() {
		t.Fatalf("set %d/%d", len(set.plain), len(set.macs))
	}
	format := regexp.MustCompile(`^[0-9a-hjkmnp-tv-z]{4}-[0-9a-hjkmnp-tv-z]{4}-[0-9a-hjkmnp-tv-z]{4}$`)
	seen := map[string]bool{}
	for i, c := range set.plain {
		if !format.MatchString(c) || seen[c] {
			t.Fatalf("bad or duplicate code %q", c)
		}
		seen[c] = true
		norm, _ := normalizeRecoveryCode(c)
		mac, err := kr.MACWith(set.keyID, recoveryMACPurpose, recoveryMACInput(user, norm))
		if err != nil || !hmac.Equal(mac, set.macs[i]) {
			t.Fatal("stored MAC does not match the code")
		}
		// Bound to the user: another user's MAC differs.
		other, err := kr.MACWith(set.keyID, recoveryMACPurpose, recoveryMACInput(uuid.New(), norm))
		if err != nil || hmac.Equal(other, set.macs[i]) {
			t.Fatal("MAC not bound to the user")
		}
		if strings.Contains(string(set.macs[i]), norm) {
			t.Fatal("plaintext in the MAC")
		}
	}
	if bits := RecoveryCodeChars * 5; bits < 50 {
		t.Fatalf("recovery codes have only %d bits", bits)
	}
}

func TestNewTOTPOptions(t *testing.T) {
	if _, err := NewTOTP(TOTPOptions{}); err == nil {
		t.Fatal("missing dependencies accepted")
	}
}
