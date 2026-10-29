package webhook

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/netguard"
)

// Test vectors, also published in docs/guides/notifications.md.
const (
	vectorKey       = "bkwh_dGhpcyBpcyBhbiBleGFtcGxlIGtleSBvbmx5ISEhISE"
	vectorTimestamp = int64(1767225600)
	vectorBody      = `{"id":"evt_01","type":"webhook.test","created_at":"2026-01-01T00:00:00Z","data":{"message":"This is a test delivery from bekup."}}`
	vectorDelivery  = "dlv_01"
	vectorEvent     = "webhook.test"
	vectorSignature = "v1=ba73f3d1d38e7e39caef2f7db71ef92aa65c84e158df98b60642729694e3f8aa"
)

// referenceHMAC is an independent RFC 2104 HMAC-SHA256 built from the
// hash primitive only, to check Sign against something other than
// crypto/hmac.
func referenceHMAC(key, msg []byte) []byte {
	const block = 64
	if len(key) > block {
		s := sha256.Sum256(key)
		key = s[:]
	}
	k := make([]byte, block)
	copy(k, key)
	ipad := make([]byte, 0, block+len(msg))
	opad := make([]byte, 0, block+sha256.Size)
	for _, b := range k {
		ipad = append(ipad, b^0x36)
		opad = append(opad, b^0x5c)
	}
	inner := sha256.Sum256(append(ipad, msg...))
	outer := sha256.Sum256(append(opad, inner[:]...))
	return outer[:]
}

func TestSignVectors(t *testing.T) {
	tests := []struct {
		key             string
		ts              int64
		delivery, event string
		body            string
		want            string
	}{
		{vectorKey, vectorTimestamp, vectorDelivery, vectorEvent, vectorBody, vectorSignature},
		{"0123456789abcdef0123456789abcdef", 1700000000, "d1", "backup.failed", "{}", "v1=378998cd93fa9aa63e43535f70c44d01da1b7a49810db1f273d5caa4814a456d"},
	}
	for _, tt := range tests {
		if got := Sign([]byte(tt.key), tt.ts, tt.delivery, tt.event, []byte(tt.body)); got != tt.want {
			t.Errorf("Sign = %s, want %s", got, tt.want)
		}
	}
}

func TestSignMatchesReference(t *testing.T) {
	for i, size := range []int{0, 1, 63, 64, 65, 1000, MaxPayloadBytes} {
		key := bytes.Repeat([]byte{byte('a' + i)}, 32+i*20)
		body := bytes.Repeat([]byte("x"), size)
		ts := int64(1700000000 + i)
		msg := append([]byte(strconv.FormatInt(ts, 10)+"\ndlv.1:x\nbackup.failed\n"), body...)
		want := "v1=" + hex.EncodeToString(referenceHMAC(key, msg))
		if got := Sign(key, ts, "dlv.1:x", "backup.failed", body); got != want {
			t.Errorf("size %d: Sign = %s, want %s", size, got, want)
		}
	}
}

func TestTestPayloadMatchesVector(t *testing.T) {
	b, err := TestPayload("evt_01", time.Unix(vectorTimestamp, 0))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != vectorBody {
		t.Fatalf("TestPayload = %s", b)
	}
	if _, err := BuildPayload("bad id", "backup.failed", time.Now(), nil); !errors.Is(err, ErrEventInvalid) {
		t.Fatalf("bad id = %v", err)
	}
	if _, err := BuildPayload("e1", "Backup.Failed", time.Now(), nil); !errors.Is(err, ErrEventInvalid) {
		t.Fatalf("bad type = %v", err)
	}
	if _, err := BuildPayload("e1", "backup.failed", time.Now(), strings.Repeat("x", MaxPayloadBytes)); !errors.Is(err, ErrPayloadInvalid) {
		t.Fatalf("huge payload = %v", err)
	}
}

func TestVerify(t *testing.T) {
	key := []byte(vectorKey)
	body := []byte(vectorBody)
	now := time.Unix(vectorTimestamp, 0)
	ts := "1767225600"
	tests := []struct {
		name     string
		sig      string
		ts       string
		delivery string
		event    string
		body     []byte
		now      time.Time
		want     error
	}{
		{"valid", vectorSignature, ts, vectorDelivery, vectorEvent, body, now, nil},
		{"valid at window edge", vectorSignature, ts, vectorDelivery, vectorEvent, body, now.Add(5 * time.Minute), nil},
		{"valid among several", "v0=zz, v1=00ff," + vectorSignature, ts, vectorDelivery, vectorEvent, body, now, nil},
		{"expired", vectorSignature, ts, vectorDelivery, vectorEvent, body, now.Add(5*time.Minute + time.Second), ErrSignatureExpired},
		{"future", vectorSignature, ts, vectorDelivery, vectorEvent, body, now.Add(-6 * time.Minute), ErrSignatureExpired},
		{"tampered body", vectorSignature, ts, vectorDelivery, vectorEvent, append([]byte(nil), append(body, ' ')...), now, ErrSignatureMismatch},
		{"tampered timestamp", vectorSignature, "1767225601", vectorDelivery, vectorEvent, body, now, ErrSignatureMismatch},
		{"missing signature", "", ts, vectorDelivery, vectorEvent, body, now, ErrSignatureMissing},
		{"missing timestamp", vectorSignature, "", vectorDelivery, vectorEvent, body, now, ErrSignatureMissing},
		{"bad timestamp", vectorSignature, "abc", vectorDelivery, vectorEvent, body, now, ErrSignatureMalformed},
		{"no v1", "v2=abcd", ts, vectorDelivery, vectorEvent, body, now, ErrSignatureMalformed},
		{"bad hex", "v1=zz", ts, vectorDelivery, vectorEvent, body, now, ErrSignatureMalformed},
		{"replayed under another delivery id", vectorSignature, ts, "dlv_02", vectorEvent, body, now, ErrSignatureMismatch},
		{"replayed as another event", vectorSignature, ts, vectorDelivery, "backup.failed", body, now, ErrSignatureMismatch},
		{"missing delivery", vectorSignature, ts, "", vectorEvent, body, now, ErrSignatureMissing},
		{"missing event", vectorSignature, ts, vectorDelivery, "", body, now, ErrSignatureMissing},
		{"newline in delivery", vectorSignature, ts, "dlv\n01", vectorEvent, body, now, ErrSignatureMalformed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Verify(key, tt.sig, tt.ts, tt.delivery, tt.event, tt.body, tt.now, 0)
			if !errors.Is(err, tt.want) || (tt.want == nil && err != nil) {
				t.Fatalf("Verify = %v, want %v", err, tt.want)
			}
		})
	}
}

func FuzzVerify(f *testing.F) {
	f.Add(vectorSignature, "1767225600", []byte(vectorBody))
	f.Add("v1=,", "-1", []byte{})
	f.Fuzz(func(t *testing.T, sig, ts string, body []byte) {
		if Verify([]byte(vectorKey), sig, ts, vectorDelivery, vectorEvent, body, time.Unix(vectorTimestamp, 0), 0) == nil &&
			!strings.Contains(sig, ",") && !strings.EqualFold(strings.TrimSpace(sig), Sign([]byte(vectorKey), vectorTimestamp, vectorDelivery, vectorEvent, body)) {
			t.Fatalf("accepted a foreign signature %q", sig)
		}
	})
}

func TestGenerateHMACKey(t *testing.T) {
	seen := map[string]bool{}
	for range 64 {
		k, err := GenerateHMACKey()
		if err != nil {
			t.Fatal(err)
		}
		s := DisplayHMACKey(k)
		if !strings.HasPrefix(s, KeyPrefix) || len(k) != 48 {
			t.Fatalf("key %q has the wrong form", s)
		}
		raw, err := base64.RawURLEncoding.DecodeString(s[len(KeyPrefix):])
		if err != nil || len(raw) != 32 {
			t.Fatalf("key body: %v (%d bytes)", err, len(raw))
		}
		if checkKey(k) != nil {
			t.Fatal("generated key rejected by Send")
		}
		if seen[s] {
			t.Fatal("duplicate key")
		}
		seen[s] = true
	}
}

// fakeResolver answers every host with addrs and checks them with guard.
type fakeResolver struct {
	guard *netguard.Guard
	addrs map[string][]string
	err   error
}

func (f fakeResolver) ResolveAndCheck(ctx context.Context, host string) ([]netip.Addr, error) {
	if f.err != nil {
		return nil, f.err
	}
	answers, ok := f.addrs[host]
	if !ok {
		return f.guard.ResolveAndCheck(ctx, host) // IP literals
	}
	out := make([]netip.Addr, 0, len(answers))
	for _, a := range answers {
		ip := netip.MustParseAddr(a)
		if err := f.guard.Check(ip, host); err != nil {
			return nil, err
		}
		out = append(out, ip)
	}
	return out, nil
}

func mustGuard(t *testing.T, opts netguard.Options) *netguard.Guard {
	t.Helper()
	g, err := netguard.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestValidateURL(t *testing.T) {
	g := mustGuard(t, netguard.Options{})
	res := fakeResolver{guard: g, addrs: map[string][]string{
		"hooks.example.com":   {"93.184.216.34"},
		"internal.example":    {"10.1.2.3"},
		"mixed.example":       {"93.184.216.34", "192.168.1.1"},
		"metadata.example":    {"169.254.169.254"},
		"mapped.example":      {"::ffff:127.0.0.1"},
		"v6public.example":    {"2606:2800:220:1:248:1893:25c8:1946"},
		"nat64-local.example": {"64:ff9b::a00:1"},
	}}
	long := "https://hooks.example.com/" + strings.Repeat("a", MaxURLLength)
	tests := []struct {
		name      string
		raw       string
		allowHTTP bool
		want      error
	}{
		{"https host", "https://hooks.example.com/services/T0/B0/xyz?a=1", false, nil},
		{"https port", "https://hooks.example.com:8443/x", false, nil},
		{"public ipv4 literal", "https://93.184.216.34/hook", false, nil},
		{"public ipv6", "https://v6public.example/", false, nil},
		{"http refused outside dev", "http://hooks.example.com/x", false, ErrInsecureScheme},
		{"http allowed in dev", "http://hooks.example.com/x", true, nil},
		{"ftp", "ftp://hooks.example.com/x", true, ErrInsecureScheme},
		{"no scheme", "hooks.example.com/x", false, ErrInsecureScheme},
		{"uppercase scheme normalized", "HTTPS://hooks.example.com/", false, nil},
		{"userinfo", "https://user:pw@hooks.example.com/", false, ErrUserinfo},
		{"user only", "https://user@hooks.example.com/", false, ErrUserinfo},
		{"fragment", "https://hooks.example.com/x#frag", false, ErrFragment},
		{"empty fragment", "https://hooks.example.com/x#", false, ErrFragment},
		{"too long", long, false, ErrURLTooLong},
		{"empty", "", false, ErrInvalidURL},
		{"space", "https://hooks.example.com/a b", false, ErrInvalidURL},
		{"control char", "https://hooks.example.com/\n", false, ErrInvalidURL},
		{"unicode", "https://hööks.example.com/", false, ErrInvalidURL},
		{"no host", "https:///path", false, ErrInvalidURL},
		{"opaque", "https:hooks.example.com", false, ErrInvalidURL},
		{"port zero", "https://hooks.example.com:0/", false, ErrInvalidURL},
		{"port too big", "https://hooks.example.com:70000/", false, ErrInvalidURL},
		{"empty port", "https://hooks.example.com:/", false, ErrInvalidURL},
		{"loopback", "https://127.0.0.1/", false, ErrHostNotAllowed},
		{"loopback v6", "https://[::1]/", false, ErrHostNotAllowed},
		{"ipv4-mapped literal", "https://[::ffff:127.0.0.1]/", false, ErrHostNotAllowed},
		{"ipv4-mapped private literal", "https://[::ffff:10.0.0.1]/", false, ErrHostNotAllowed},
		{"ipv4-mapped answer", "https://mapped.example/", false, ErrHostNotAllowed},
		{"private literal", "https://10.0.0.1/", false, ErrHostNotAllowed},
		{"private answer", "https://internal.example/", false, ErrHostNotAllowed},
		{"one private answer", "https://mixed.example/", false, ErrHostNotAllowed},
		{"aws metadata", "https://169.254.169.254/latest/meta-data/", false, ErrHostNotAllowed},
		{"metadata answer", "https://metadata.example/", false, ErrHostNotAllowed},
		{"alibaba metadata", "https://100.100.100.200/", false, ErrHostNotAllowed},
		{"aws v6 metadata", "https://[fd00:ec2::254]/", false, ErrHostNotAllowed},
		{"nat64 private", "https://nat64-local.example/", false, ErrHostNotAllowed},
		{"unspecified", "https://0.0.0.0/", false, ErrHostNotAllowed},
		{"decimal ip", "https://2130706433/", false, ErrInvalidURL},
		{"zoned v6", "https://[fe80::1%25en0]/", false, ErrInvalidURL},
		{"underscore host", "https://a_b.example/", false, ErrInvalidURL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := ValidateURL(context.Background(), tt.raw, res, URLOptions{AllowHTTP: tt.allowHTTP, Logger: slog.New(slog.DiscardHandler)})
			if tt.want == nil {
				if err != nil {
					t.Fatalf("ValidateURL = %v, want ok", err)
				}
				if u == nil {
					t.Fatal("nil URL")
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("ValidateURL = %v, want %v", err, tt.want)
			}
			if strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), "10.") {
				t.Fatalf("error leaks an address: %v", err)
			}
		})
	}
}

func TestValidateURLResolveFailure(t *testing.T) {
	g := mustGuard(t, netguard.Options{})
	res := fakeResolver{guard: g, err: errors.New("lookup nx.example: no such host")}
	if _, err := ValidateURL(context.Background(), "https://nx.example/", res, URLOptions{}); !errors.Is(err, ErrHostUnreachable) {
		t.Fatalf("unresolvable = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res.err = context.Canceled
	if _, err := ValidateURL(ctx, "https://nx.example/", res, URLOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled = %v", err)
	}
}

func TestValidateURLDevWarning(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	g := mustGuard(t, netguard.Options{})
	if _, err := ValidateURL(context.Background(), "http://93.184.216.34/path?k=v", g, URLOptions{AllowHTTP: true, Logger: logger}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "webhook_insecure_http_url_accepted") {
		t.Fatalf("no warning: %s", buf.String())
	}
	if strings.Contains(buf.String(), "93.184.216.34") || strings.Contains(buf.String(), "k=v") {
		t.Fatalf("warning leaks the URL: %s", buf.String())
	}
}

func FuzzParseURL(f *testing.F) {
	for _, s := range []string{"https://a.example/x", "http://[::1]:80/", "https://u@h/", "https://h:/#"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		u, err := parseURL(raw, false)
		if err != nil {
			return
		}
		if u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Hostname() == "" || len(raw) > MaxURLLength {
			t.Fatalf("accepted %q", raw)
		}
	})
}

func TestDisplayURL(t *testing.T) {
	tests := map[string]string{
		"https://hooks.slack.com/services/T0/B0/abc": "https://hooks.slack.com/…",
		"https://h.example:8443/":                    "https://h.example:8443",
		"https://h.example?k=v":                      "https://h.example/…",
		"https://u:p@h.example/x":                    "https://h.example/…",
		"ftp://h.example/":                           "",
		"::":                                         "",
	}
	for in, want := range tests {
		if got := DisplayURL(in); got != want {
			t.Errorf("DisplayURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClassifyStatus(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		status     int
		retryAfter string
		outcome    Outcome
		code       Code
		after      time.Duration
	}{
		{200, "", OutcomeSuccess, CodeOK, 0},
		{201, "", OutcomeSuccess, CodeOK, 0},
		{204, "", OutcomeSuccess, CodeOK, 0},
		{299, "", OutcomeSuccess, CodeOK, 0},
		{301, "", OutcomePermanent, CodeRedirect, 0},
		{304, "", OutcomePermanent, CodeRedirect, 0},
		{308, "", OutcomePermanent, CodeRedirect, 0},
		{400, "", OutcomePermanent, CodeHTTPStatus, 0},
		{401, "", OutcomePermanent, CodeHTTPStatus, 0},
		{403, "", OutcomePermanent, CodeHTTPStatus, 0},
		{404, "", OutcomePermanent, CodeHTTPStatus, 0},
		{410, "", OutcomePermanent, CodeHTTPStatus, 0},
		{408, "", OutcomeRetryable, CodeHTTPStatus, 0},
		{429, "", OutcomeRetryable, CodeHTTPStatus, 0},
		{429, "120", OutcomeRetryable, CodeHTTPStatus, 120 * time.Second},
		{429, "0", OutcomeRetryable, CodeHTTPStatus, 0},
		{429, "-5", OutcomeRetryable, CodeHTTPStatus, 0},
		{429, "999999999999", OutcomeRetryable, CodeHTTPStatus, MaxRetryAfter},
		{429, "Thu, 01 Jan 2026 00:02:00 GMT", OutcomeRetryable, CodeHTTPStatus, 2 * time.Minute},
		{429, "Wed, 31 Dec 2025 00:00:00 GMT", OutcomeRetryable, CodeHTTPStatus, 0},
		{429, "Fri, 02 Jan 2026 12:00:00 GMT", OutcomeRetryable, CodeHTTPStatus, MaxRetryAfter},
		{429, "soon", OutcomeRetryable, CodeHTTPStatus, 0},
		{500, "", OutcomeRetryable, CodeHTTPStatus, 0},
		{502, "30", OutcomeRetryable, CodeHTTPStatus, 0},
		{503, "30", OutcomeRetryable, CodeHTTPStatus, 30 * time.Second},
		{599, "", OutcomeRetryable, CodeHTTPStatus, 0},
		{600, "", OutcomePermanent, CodeHTTPStatus, 0},
		{101, "", OutcomePermanent, CodeHTTPStatus, 0},
	}
	for _, tt := range tests {
		r := classifyStatus(Result{}, tt.status, tt.retryAfter, now)
		if r.Outcome != tt.outcome || r.Code != tt.code || r.RetryAfter != tt.after || r.StatusCode != tt.status {
			t.Errorf("%d %q: got %s/%s/%s, want %s/%s/%s", tt.status, tt.retryAfter, r.Outcome, r.Code, r.RetryAfter, tt.outcome, tt.code, tt.after)
		}
		if (r.Outcome == OutcomeSuccess) != (r.Err == nil) {
			t.Errorf("%d: Err = %v", tt.status, r.Err)
		}
	}
}

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		outcome Outcome
		code    Code
	}{
		{"blocked", &netguard.BlockedAddressError{IP: netip.MustParseAddr("10.0.0.1")}, OutcomePermanent, CodeNotAllowed},
		{"redirect", netguard.ErrRedirect, OutcomePermanent, CodeRedirect},
		{"timeout", context.DeadlineExceeded, OutcomeRetryable, CodeUnreachable},
		{"no addresses", netguard.ErrNoAddresses, OutcomeRetryable, CodeUnreachable},
		{"other", errors.New("malformed HTTP response"), OutcomeRetryable, CodeFailed},
		{"canceled", context.Canceled, OutcomeAborted, CodeCanceled},
	}
	for _, tt := range tests {
		r := classifyError(Result{}, tt.err)
		if r.Outcome != tt.outcome || r.Code != tt.code {
			t.Errorf("%s: got %s/%s, want %s/%s", tt.name, r.Outcome, r.Code, tt.outcome, tt.code)
		}
		if r.Err != nil && strings.Contains(r.Err.Error(), "10.0.0.1") {
			t.Errorf("%s: error leaks the address: %v", tt.name, r.Err)
		}
	}
}

func TestFailurePolicy(t *testing.T) {
	p := DefaultFailurePolicy()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	retry := Result{Outcome: OutcomeRetryable, Code: CodeUnreachable}
	perm := Result{Outcome: OutcomePermanent, Code: CodeHTTPStatus}
	ok := Result{Outcome: OutcomeSuccess, Code: CodeOK}
	abort := Result{Outcome: OutcomeAborted, Code: CodeCanceled}

	t.Run("burst within a day does not disable", func(t *testing.T) {
		var st FailureState
		var d Decision
		for i := range 50 {
			d = p.Record(st, retry, t0.Add(time.Duration(i)*time.Minute))
			st = d.State
		}
		if d.Disable || st.ConsecutiveFailures != 50 {
			t.Fatalf("disabled after a burst: %+v", d)
		}
	})
	t.Run("ten failures over a day disable", func(t *testing.T) {
		var st FailureState
		var d Decision
		for i := range 10 {
			d = p.Record(st, retry, t0.Add(time.Duration(i)*160*time.Minute))
			st = d.State
			if i < 9 && d.Disable {
				t.Fatalf("disabled early at %d", i)
			}
		}
		if !d.Disable || d.Reason != DisableRepeatedFailures || d.Reason.Message() == "" {
			t.Fatalf("not disabled: %+v (span %s)", d, st.LastFailureAt.Sub(st.FirstFailureAt))
		}
	})
	t.Run("long span but few failures does not disable", func(t *testing.T) {
		var st FailureState
		st = p.Record(st, retry, t0).State
		d := p.Record(st, retry, t0.Add(72*time.Hour))
		if d.Disable {
			t.Fatal("disabled after 2 failures")
		}
	})
	t.Run("five permanent failures disable", func(t *testing.T) {
		var st FailureState
		var d Decision
		for i := range 5 {
			if i == 2 {
				st = p.Record(st, retry, t0).State // retryable in between does not reset
			}
			d = p.Record(st, perm, t0.Add(time.Duration(i)*time.Second))
			st = d.State
		}
		if !d.Disable || d.Reason != DisablePermanentFailures || d.Reason.Message() == "" {
			t.Fatalf("not disabled: %+v", d)
		}
	})
	t.Run("success resets", func(t *testing.T) {
		st := FailureState{ConsecutiveFailures: 9, PermanentFailures: 4, FirstFailureAt: t0, LastFailureAt: t0}
		d := p.Record(st, ok, t0.Add(time.Hour))
		if d.Disable || d.State != (FailureState{}) {
			t.Fatalf("not reset: %+v", d)
		}
		d = p.Record(d.State, perm, t0.Add(2*time.Hour))
		if d.State.PermanentFailures != 1 || !d.State.FirstFailureAt.Equal(t0.Add(2*time.Hour)) {
			t.Fatalf("counting after reset: %+v", d.State)
		}
	})
	t.Run("aborted attempts do not count", func(t *testing.T) {
		st := FailureState{ConsecutiveFailures: 3, FirstFailureAt: t0, LastFailureAt: t0}
		d := p.Record(st, abort, t0.Add(48*time.Hour))
		if d.State != st || d.Disable {
			t.Fatalf("aborted changed state: %+v", d)
		}
	})
	t.Run("validate", func(t *testing.T) {
		for _, bad := range []FailurePolicy{{}, {MaxConsecutive: 1, MaxPermanent: 0}, {MaxConsecutive: 1, MaxPermanent: 1, MinSpan: -1}, {MaxConsecutive: 5000, MaxPermanent: 1}} {
			if bad.Validate() == nil {
				t.Errorf("Validate(%+v) = nil", bad)
			}
		}
	})
}

func TestOutcomeString(t *testing.T) {
	for o, want := range map[Outcome]string{OutcomeSuccess: "success", OutcomeRetryable: "retryable", OutcomePermanent: "permanent", OutcomeAborted: "aborted", 0: "unknown"} {
		if o.String() != want {
			t.Errorf("%d = %s", o, o.String())
		}
	}
}
