package web

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jlentink/bekup/internal/setup"
	"github.com/jlentink/bekup/internal/setup/wizard"
)

const (
	testImage = "ghcr.io/jlentink/bekup:1.2.3@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	genPass   = "correct horse battery staple again"
)

// harness is one running browser setup with a client that trusts its
// certificate (no InsecureSkipVerify).
type harness struct {
	t      *testing.T
	base   string
	origin string
	code   string
	client *http.Client
	done   chan error
	cancel context.CancelFunc

	mu      sync.Mutex
	initOps []setup.Options
	pass    []byte
	// sensitive are the bodies of the sensitive steps drive saw.
	sensitive []string
	// dbPass are the role passwords Init got (copies: the plan is
	// destroyed after Init).
	dbPass map[string][]byte
}

func testEnv(t *testing.T) wizard.Env {
	t.Helper()
	dir := t.TempDir()
	return wizard.Env{
		DefaultDir: dir, Version: "1.2.3", Image: testImage, DockerGID: "998",
		Ownership:          setup.OwnershipNone,
		GeneratePassphrase: func() ([]byte, error) { return []byte(genPass), nil },
		LoadCA:             func(string, string, []byte) error { return nil },
	}
}

func start(t *testing.T, mut func(*Options)) *harness {
	t.Helper()
	h := &harness{t: t, done: make(chan error, 1)}
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	var cert *Certificate
	opts := Options{
		Dir: t.TempDir(), Env: testEnv(t), Assets: fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>setup</title>")}},
		Out: io.Discard, listener: ln, gateSleep: func(time.Duration) {},
		Init: func(o setup.Options) error {
			// Like setup.Init: the passphrase is asked while writing.
			pass, err := o.CAPassphrase()
			h.mu.Lock()
			defer h.mu.Unlock()
			h.initOps, h.pass = append(h.initOps, o), pass
			if o.DBPassword != nil {
				h.dbPass = map[string][]byte{}
				for _, role := range []string{"bekup_app", "bekup_owner"} {
					if pw, perr := o.DBPassword(role); perr == nil {
						h.dbPass[role] = bytes.Clone(pw)
					}
				}
			}
			return err
		},
		Check: func(setup.CheckOptions) ([]setup.Finding, error) { return nil, nil },
		ready: func(code string, c *Certificate) {
			h.code, cert = code, c
			close(ready)
		},
	}
	if mut != nil {
		mut(&opts)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() { h.done <- Run(ctx, opts) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-h.done:
		case <-time.After(10 * time.Second):
			t.Error("server did not stop")
		}
	})
	select {
	case <-ready:
	case err := <-h.done:
		t.Fatalf("Run: %v", err)
	}
	leaf, err := x509.ParseCertificate(cert.TLS.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %v", ln.Addr())
	}
	port := addr.Port
	h.base = fmt.Sprintf("https://localhost:%d", port)
	h.origin = h.base
	h.client = &http.Client{Jar: jar, Timeout: 40 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13},
	}}
	return h
}

// reply is the status and headers of a response whose body was read.
type reply struct {
	StatusCode int
	Header     http.Header
}

// do sends a request as the setup SPA does (marker, Origin,
// Sec-Fetch-Site), with mut changing the headers; the body is read and
// closed.
func (h *harness) do(method, path, ctype string, body []byte, mut func(http.Header)) (reply, []byte) {
	h.t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, h.base+path, bytes.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	if method == http.MethodPost {
		req.Header.Set(HeaderMarker, "1")
		req.Header.Set("Origin", h.origin)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Content-Type", ctype)
	}
	if mut != nil {
		mut(req.Header)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatal(err)
	}
	return reply{StatusCode: resp.StatusCode, Header: resp.Header}, data
}

func (h *harness) postJSON(path string, v any, mut func(http.Header)) (reply, []byte) {
	h.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.do(http.MethodPost, path, "application/json", b, mut)
}

func (h *harness) login() {
	h.t.Helper()
	if resp, body := h.postJSON("/api/setup/session", map[string]string{"code": h.code}, nil); resp.StatusCode != http.StatusNoContent {
		h.t.Fatalf("login: %d %s", resp.StatusCode, body)
	}
}

func (h *harness) status(after uint64) Status {
	h.t.Helper()
	return h.statusPath("/api/setup/status", after > 0, after)
}

// statusPath fetches the status; wait sends ?after= (also 0: wait for the
// first step while setup works).
func (h *harness) statusPath(path string, wait bool, after uint64) Status {
	h.t.Helper()
	if wait {
		path += fmt.Sprintf("?after=%d", after)
	}
	resp, body := h.do(http.MethodGet, path, "", nil, nil)
	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("status: %d %s", resp.StatusCode, body)
	}
	var st Status
	if err := json.Unmarshal(body, &st); err != nil {
		h.t.Fatal(err)
	}
	return st
}

// drive answers the screens like wizard.Script does, through the API, and
// returns the final status and the revealed passphrases.
func (h *harness) drive(answers []wizard.Answer) (Status, [][]byte) {
	h.t.Helper()
	var last uint64
	var revealed [][]byte
	next := 0
	for i := range 500 {
		st := h.statusPath("/api/setup/status", i > 0, last)
		if st.Phase != PhaseQuestions {
			if next != len(answers) {
				h.t.Fatalf("flow ended (%s %q) with %d unused answers\n%s", st.Phase, st.Error, len(answers)-next, st.Report)
			}
			return st, revealed
		}
		if st.Step == nil {
			continue // setup is working
		}
		step := *st.Step
		last = step.ID
		if step.Error != "" {
			h.t.Fatalf("screen %q refused an answer: %s", step.Title, step.Error)
		}
		if next >= len(answers) {
			h.t.Fatalf("no answer for %s %q", step.Kind, step.Title)
		}
		a := answers[next]
		next++
		if a.Title != "" && !strings.Contains(step.Title, a.Title) {
			h.t.Fatalf("answer %d expects %q, got %s %q", next, a.Title, step.Kind, step.Title)
		}
		var resp reply
		var body []byte
		switch {
		case a.Back:
			resp, body = h.postJSON("/api/setup/answer", Answer{Step: step.ID, Back: true}, nil)
		case step.Kind == KindSecret:
			resp, body = h.do(http.MethodPost, fmt.Sprintf("/api/setup/secret?step=%d", step.ID), "application/octet-stream", []byte(a.Value), nil)
		case step.Kind == KindSensitive:
			h.mu.Lock()
			h.sensitive = append(h.sensitive, step.Body)
			h.mu.Unlock()
			r, secret := h.postJSON("/api/setup/reveal", map[string]uint64{"step": step.ID}, nil)
			if r.StatusCode != http.StatusOK || r.Header.Get("Cache-Control") != "no-store" {
				h.t.Fatalf("reveal: %d %q %s", r.StatusCode, r.Header.Get("Cache-Control"), secret)
			}
			revealed = append(revealed, secret)
			if r, again := h.postJSON("/api/setup/reveal", map[string]uint64{"step": step.ID}, nil); r.StatusCode != http.StatusConflict || bytes.Contains(again, secret) {
				h.t.Fatalf("second reveal: %d %s", r.StatusCode, again)
			}
			resp, body = h.postJSON("/api/setup/answer", Answer{Step: step.ID}, nil)
		default:
			v := a.Value
			if v == "" {
				v = step.Default
			}
			resp, body = h.postJSON("/api/setup/answer", Answer{Step: step.ID, Value: v}, nil)
		}
		if resp.StatusCode != http.StatusNoContent {
			h.t.Fatalf("answer to %q: %d %s", step.Title, resp.StatusCode, body)
		}
	}
	h.t.Fatal("too many screens")
	return Status{}, nil
}

// traefikAnswers is a complete init with the bundled Traefik and database.
func traefikAnswers() []wizard.Answer {
	return []wizard.Answer{
		{Title: "bekup setup"},
		{Title: "Reverse proxy", Value: "own-traefik"},
		{Title: "Domain", Value: "backup.example.com"},
		{Title: "Let's Encrypt e-mail", Value: "ops@example.com"},
		{Title: "Let's Encrypt staging?", Value: "no"},
		{Title: "Database", Value: "bundled"},
		{Title: "Master encryption key", Value: "generate"},
		{Title: "Internal CA", Value: "generate"},
		{Title: "CA key passphrase", Value: "generate"},
		{Title: "Bundled database secrets", Value: "yes"},
		{Title: "Docker socket group", Value: "998"},
		{Title: "Target database networks", Value: "10.20.0.0/16"},
		{Title: "Target database host names"},
		// The external API (D-220): the browser asks the wizard's question.
		{Title: "External API for CI pipelines", Value: "yes"},
		{Title: "Summary"},
		{Title: "Write the deployment now?", Value: "yes"},
	}
}

// TestBrowserFlowMatchesWizard: the browser produces exactly the options
// of the scripted wizard with the same answers (and so of the flags), and
// shows the generated passphrase once.
func TestBrowserFlowMatchesWizard(t *testing.T) {
	answers := traefikAnswers()
	script := &wizard.Script{Answers: append(slices.Clone(answers), wizard.Answer{Title: "CA key passphrase (shown ONCE)"})}
	env := testEnv(t)
	env.Dir, env.UI = t.TempDir(), script
	want, err := wizard.RunInit(env)
	if err != nil {
		t.Fatalf("script: %v\n%v", err, script.Screens)
	}
	defer want.Destroy()

	h := start(t, nil)
	h.login()
	st, revealed := h.drive(append(answers, wizard.Answer{Title: "CA key passphrase (shown ONCE)"}))
	if st.Phase != PhaseDone || !st.Zip {
		t.Fatalf("status %+v", st)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.initOps) != 1 {
		t.Fatalf("Init ran %d times", len(h.initOps))
	}
	got := h.initOps[0]
	if !equalAnswers(got.Answers, want.Options.Answers) || got.WriteCompose != want.Options.WriteCompose {
		t.Fatalf("answers differ:\n got %+v\nwant %+v", got.Answers, want.Options.Answers)
	}
	if string(h.pass) != genPass || len(revealed) != 1 || string(revealed[0]) != genPass {
		t.Fatalf("passphrase %q, revealed %q", h.pass, revealed)
	}
	if !strings.Contains(regexp.MustCompile(`--dir \S+ `).ReplaceAllString(st.Report, ""), strings.Join(withoutDir(want.Flags), " ")) {
		t.Fatalf("report lacks the equivalent flags:\n%s", st.Report)
	}
	if strings.Contains(st.Report, genPass) {
		t.Fatal("the passphrase leaked into the report")
	}
	// The browser's passphrase screen does not talk about Docker logs
	// (the passphrase never reaches the terminal there).
	if len(h.sensitive) != 1 || strings.Contains(h.sensitive[0], "log-driver") || !strings.Contains(h.sensitive[0], "on this page only") {
		t.Fatalf("passphrase screen %q", h.sensitive)
	}
}

// ownProxyDBAnswers is a complete init with your own proxy (join mode) and
// a database container on this host whose roles exist (passwords as
// secret answers).
func ownProxyDBAnswers() []wizard.Answer {
	return []wizard.Answer{
		{Title: "bekup setup"},
		{Title: "Reverse proxy", Value: "own"},
		{Title: "Your proxy's Docker network", Value: "web"},
		{Title: "Your proxy's container name", Value: "caddy"},
		{Title: "Traefik Docker labels?", Value: "yes"},
		{Title: "Traefik entrypoint"},
		{Title: "Traefik certificate resolver"},
		{Title: "Domain", Value: "backup.example.com"},
		{Title: "Database", Value: "external"},
		{Title: "Where does your PostgreSQL run?", Value: "docker"},
		{Title: "The database's Docker network", Value: "dbnet"},
		{Title: "The database's container name", Value: "postgres"},
		{Title: "PostgreSQL port"},
		{Title: "Database name"},
		{Title: "TLS to your PostgreSQL", Value: "require"},
		{Title: "An explicit weakening", Value: "yes"},
		{Title: "Roles and database", Value: "existing-roles"},
		{Title: "Password of bekup_app", Value: "app-password-0123456789-abcdef"},
		{Title: "Repeat the password of bekup_app", Value: "app-password-0123456789-abcdef"},
		{Title: "Password of bekup_owner", Value: "owner-password-0123456789-abcdef"},
		{Title: "Repeat the password of bekup_owner", Value: "owner-password-0123456789-abcdef"},
		{Title: "Master encryption key", Value: "generate"},
		{Title: "Internal CA", Value: "generate"},
		{Title: "CA key passphrase", Value: "generate"},
		{Title: "Docker socket group", Value: "998"},
		{Title: "Target database networks", Value: "10.20.0.0/16"},
		{Title: "Target database host names"},
		{Title: "External API for CI pipelines", Value: "no"},
		{Title: "Summary"},
		{Title: "Write the deployment now?", Value: "yes"},
		{Title: "CA key passphrase (shown ONCE)"},
	}
}

// TestBrowserFlowMatchesWizardOwnProxyDB: the same parity for your own
// proxy with the Traefik labels and an external database container whose
// role passwords are secret answers.
func TestBrowserFlowMatchesWizardOwnProxyDB(t *testing.T) {
	answers := ownProxyDBAnswers()
	script := &wizard.Script{Answers: slices.Clone(answers)}
	env := testEnv(t)
	env.Dir, env.UI = t.TempDir(), script
	want, err := wizard.RunInit(env)
	if err != nil {
		t.Fatalf("script: %v\n%v", err, script.Screens)
	}
	defer want.Destroy()

	h := start(t, nil)
	h.login()
	st, _ := h.drive(answers)
	if st.Phase != PhaseDone {
		t.Fatalf("status %+v", st)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.initOps) != 1 {
		t.Fatalf("Init ran %d times", len(h.initOps))
	}
	got := h.initOps[0]
	if !equalAnswers(got.Answers, want.Options.Answers) || got.WriteCompose != want.Options.WriteCompose {
		t.Fatalf("answers differ:\n got %+v\nwant %+v", got.Answers, want.Options.Answers)
	}
	if got.Answers.DB.Location != setup.DBDocker || !got.Answers.ProxyLabels {
		t.Fatalf("not the own-proxy + database container shape: %+v", got.Answers)
	}
	for _, role := range []string{"bekup_app", "bekup_owner"} {
		w, werr := want.Options.DBPassword(role)
		if werr != nil || len(w) == 0 || !bytes.Equal(h.dbPass[role], w) {
			t.Fatalf("%s password differs: %v", role, werr)
		}
	}
	if !strings.Contains(regexp.MustCompile(`--dir \S+ `).ReplaceAllString(st.Report, ""), strings.Join(withoutDir(want.Flags), " ")) {
		t.Fatalf("report lacks the equivalent flags:\n%s", st.Report)
	}
	if strings.Contains(st.Report, "password-0123456789") {
		t.Fatal("a role password in the report")
	}
}

// withoutDir drops --dir and its value (each run has its own directory).
func withoutDir(flags []string) []string {
	if i := slices.Index(flags, "--dir"); i >= 0 {
		return slices.Delete(slices.Clone(flags), i, i+2)
	}
	return flags
}

// equalAnswers compares the answers (Image by its string).
func equalAnswers(a, b setup.Answers) bool {
	if a.Image.String() != b.Image.String() {
		return false
	}
	a.Image = b.Image
	return reflect.DeepEqual(a, b)
}

// TestBrowserBack: Back returns to the previous screen and a refused
// answer comes back with the wizard's message.
func TestBrowserBack(t *testing.T) {
	h := start(t, nil)
	h.login()
	st := h.status(0)
	for st.Step == nil {
		st = h.status(0)
	}
	first := *st.Step
	if r, b := h.postJSON("/api/setup/answer", Answer{Step: first.ID}, nil); r.StatusCode != http.StatusNoContent {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
	if r, _ := h.postJSON("/api/setup/answer", Answer{Step: first.ID}, nil); r.StatusCode != http.StatusConflict {
		t.Fatalf("stale answer: %d", r.StatusCode)
	}
	st = h.status(first.ID)
	proxy := *st.Step
	if r, b := h.postJSON("/api/setup/answer", Answer{Step: proxy.ID, Value: "no-such-proxy"}, nil); r.StatusCode != http.StatusNoContent {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
	st = h.status(proxy.ID)
	if st.Step == nil || st.Step.Title != proxy.Title || st.Step.Error == "" {
		t.Fatalf("refused answer: %+v", st.Step)
	}
	// A valid answer, then Back on the next screen returns to it.
	if r, b := h.postJSON("/api/setup/answer", Answer{Step: st.Step.ID, Value: "own-traefik"}, nil); r.StatusCode != http.StatusNoContent {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
	st = h.status(st.Step.ID)
	if st.Step == nil || st.Step.Title == proxy.Title {
		t.Fatalf("next screen: %+v", st.Step)
	}
	if r, b := h.postJSON("/api/setup/answer", Answer{Step: st.Step.ID, Back: true}, nil); r.StatusCode != http.StatusNoContent {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
	st = h.status(st.Step.ID)
	if st.Step == nil || st.Step.Title != proxy.Title {
		t.Fatalf("back: %+v", st.Step)
	}
}

func TestBrowserAccess(t *testing.T) {
	h := start(t, nil)
	// Nothing without a session.
	if r, _ := h.do(http.MethodGet, "/api/setup/status", "", nil, nil); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status without session: %d", r.StatusCode)
	}
	if r, _ := h.postJSON("/api/setup/session", map[string]string{"code": "AAAAAAAAAAAAAAAAAAAAAAAAAA"}, nil); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong code: %d", r.StatusCode)
	}
	for name, mut := range map[string]func(http.Header){
		"no marker":         func(hd http.Header) { hd.Del(HeaderMarker) },
		"foreign origin":    func(hd http.Header) { hd.Set("Origin", "https://evil.example") },
		"no fetch metadata": func(hd http.Header) { hd.Del("Sec-Fetch-Site") },
		"cross-site":        func(hd http.Header) { hd.Set("Sec-Fetch-Site", "cross-site") },
	} {
		if r, _ := h.postJSON("/api/setup/session", map[string]string{"code": h.code}, mut); r.StatusCode != http.StatusForbidden {
			t.Errorf("%s: %d", name, r.StatusCode)
		}
	}
	if r, _ := h.do(http.MethodPost, "/api/setup/session", "text/plain", []byte(`{"code":"`+h.code+`"}`), nil); r.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain: %d", r.StatusCode)
	}
	h.login()
	resp, _ := h.do(http.MethodGet, "/api/setup/status", "", nil, nil)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Security-Policy") == "" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("status: %d %v", resp.StatusCode, resp.Header)
	}
	// The code is single use: a second browser cannot sign in.
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	other := &harness{t: t, base: h.base, origin: h.origin, code: h.code,
		client: &http.Client{Jar: jar, Transport: h.client.Transport, Timeout: h.client.Timeout}}
	if r, b := other.postJSON("/api/setup/session", map[string]string{"code": h.code}, nil); r.StatusCode != http.StatusUnauthorized || !strings.Contains(string(b), "already open") {
		t.Fatalf("second session: %d %s", r.StatusCode, b)
	}
	// Body limit.
	big := bytes.Repeat([]byte("a"), MaxBody+1)
	if r, _ := h.do(http.MethodPost, "/api/setup/secret?step=1", "application/octet-stream", big, nil); r.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: %d", r.StatusCode)
	}
	// No zip before the files are written.
	if r, _ := h.do(http.MethodGet, "/api/setup/config.zip", "", nil, nil); r.StatusCode != http.StatusConflict {
		t.Fatalf("zip before init: %d", r.StatusCode)
	}
	// The SPA is served; /api is never the SPA.
	if r, b := h.do(http.MethodGet, "/", "", nil, nil); r.StatusCode != http.StatusOK || !strings.Contains(string(b), "setup") {
		t.Fatalf("index: %d", r.StatusCode)
	}
	if r, _ := h.do(http.MethodGet, "/api/nothing", "", nil, nil); r.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown api: %d", r.StatusCode)
	}
}

// TestBrowserExhausted: the MaxFailures-th wrong code stops setup.
func TestBrowserExhausted(t *testing.T) {
	h := start(t, nil)
	for i := range MaxFailures {
		r, _ := h.postJSON("/api/setup/session", map[string]string{"code": fmt.Sprintf("WRONG%d", i)}, nil)
		if r.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i, r.StatusCode)
		}
	}
	select {
	case <-h.done:
		h.done <- nil // for the cleanup
	case <-time.After(10 * time.Second):
		t.Fatal("setup kept running after the last failure")
	}
}

// TestBrowserIdle: setup stops without activity.
func TestBrowserIdle(t *testing.T) {
	h := start(t, func(o *Options) { o.Idle = 200 * time.Millisecond })
	select {
	case <-h.done:
		h.done <- nil
	case <-time.After(10 * time.Second):
		t.Fatal("idle setup kept running")
	}
}

// TestBrowserSecretZeroedOnRefusal: a secret answer to a non-secret step
// is refused.
func TestBrowserWrongKind(t *testing.T) {
	ui := NewUI(context.Background())
	noted := make(chan error, 1)
	go func() { noted <- ui.Note("note", "body") }()
	var step Step
	for {
		s, ok, ch := ui.Current()
		if ok {
			step = s
			break
		}
		<-ch
	}
	secret := []byte("s3cret")
	if err := ui.AnswerSecret(step.ID, secret); !errors.Is(err, ErrWrongKind) {
		t.Fatalf("secret to a note: %v", err)
	}
	if !bytes.Equal(secret, make([]byte, len(secret))) {
		t.Fatal("refused secret not zeroed")
	}
	if err := ui.Answer(Answer{Step: step.ID}); err != nil {
		t.Fatal(err)
	}
	if err := <-noted; err != nil {
		t.Fatal(err)
	}
}

// TestBrowserFinish: finishing is refused while setup works and stops the
// server once the flow is done.
func TestBrowserFinish(t *testing.T) {
	h := start(t, nil)
	h.login()
	if r, _ := h.postJSON("/api/setup/finish", struct{}{}, nil); r.StatusCode != http.StatusConflict {
		t.Fatalf("finish while asking: %d", r.StatusCode)
	}
	st, _ := h.drive(append(traefikAnswers(), wizard.Answer{Title: "CA key passphrase (shown ONCE)"}))
	if st.Phase != PhaseDone {
		t.Fatalf("status %+v", st)
	}
	if r, b := h.postJSON("/api/setup/finish", struct{}{}, func(hd http.Header) { hd.Del(HeaderMarker) }); r.StatusCode != http.StatusForbidden {
		t.Fatalf("finish without the marker: %d %s", r.StatusCode, b)
	}
	if r, b := h.postJSON("/api/setup/finish", struct{}{}, nil); r.StatusCode != http.StatusNoContent {
		t.Fatalf("finish: %d %s", r.StatusCode, b)
	}
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		h.done <- nil // for the cleanup
	case <-time.After(10 * time.Second):
		t.Fatal("setup kept running after finish")
	}
}

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestBrowserCodeNeverServed: the access code and the session value show
// up only on the terminal: never in the logs, a response body or header
// (other than the session's own Set-Cookie).
func TestBrowserCodeNeverServed(t *testing.T) {
	var logs, term syncBuffer
	h := start(t, func(o *Options) {
		o.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		o.Out = &term
	})
	var bodies bytes.Buffer
	collect := func(r reply, b []byte) {
		for k, v := range r.Header {
			if k != "Set-Cookie" {
				fmt.Fprintf(&bodies, "%s: %v\n", k, v)
			}
		}
		bodies.Write(b)
	}
	collect(h.postJSON("/api/setup/session", map[string]string{"code": "WRONG"}, nil))
	collect(h.postJSON("/api/setup/session", map[string]string{"code": h.code}, nil))
	collect(h.postJSON("/api/setup/session", map[string]string{"code": h.code}, nil))
	collect(h.do(http.MethodGet, "/api/setup/status", "", nil, nil))
	collect(h.do(http.MethodGet, "/", "", nil, nil))
	u, err := url.Parse(h.base)
	if err != nil {
		t.Fatal(err)
	}
	var session string
	for _, c := range h.client.Jar.Cookies(u) {
		if c.Name == CookieName {
			session = c.Value
		}
	}
	if session == "" || !strings.Contains(term.String(), h.code) || !strings.Contains(term.String(), "#code="+h.code) {
		t.Fatalf("session %q, terminal:\n%s", session, term.String())
	}
	for what, s := range map[string]string{"logs": logs.String(), "responses": bodies.String()} {
		if strings.Contains(strings.ToUpper(s), h.code) || strings.Contains(s, session) {
			t.Fatalf("the code or session value in the %s:\n%s", what, s)
		}
	}
	if strings.Contains(term.String(), session) {
		t.Fatal("the session value on the terminal")
	}
}

// TestBrowserNoUI: a build without the setup UI refuses to start (no code
// for a page that does not exist).
func TestBrowserNoUI(t *testing.T) {
	err := Run(context.Background(), Options{
		Dir: t.TempDir(), Assets: fstest.MapFS{".gitkeep": {}}, Out: io.Discard,
		Init:  func(setup.Options) error { return nil },
		Check: func(setup.CheckOptions) ([]setup.Finding, error) { return nil, nil },
	})
	if err == nil || !strings.Contains(err.Error(), "does not include the browser setup's UI") {
		t.Fatalf("Run without a UI: %v", err)
	}
}

// TestBrowserDatabaseRunSkipsCheck: the database run (UID 65532, read-only
// directory) never runs setup check, which would report the root-only key
// directories as errors; it points at the check of the setup service.
func TestBrowserDatabaseRunSkipsCheck(t *testing.T) {
	var checks, steps int
	h := start(t, func(o *Options) {
		o.Mode, o.Init = ModeDatabase, nil
		o.Check = func(setup.CheckOptions) ([]setup.Finding, error) { checks++; return nil, nil }
		o.Database = func(context.Context, wizard.UI, string) error { steps++; return nil }
		if err := os.WriteFile(filepath.Join(o.Dir, setup.StateFileName), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	h.login()
	st, _ := h.drive(nil)
	if st.Phase != PhaseDone || checks != 0 || steps != 1 || !strings.Contains(st.Report, "run --rm setup check") {
		t.Fatalf("phase %s, %d checks, %d database steps\n%s", st.Phase, checks, steps, st.Report)
	}
}

// TestBrowserUsedCodeCannotStopSetup: once the code was used, wrong codes
// neither count nor wait (nobody reaching the port can exhaust the
// attempts and stop a running setup); every refused sign-in is shown on
// the terminal with the peer's address, never the code.
func TestBrowserUsedCodeCannotStopSetup(t *testing.T) {
	var term syncBuffer
	var slept int
	var sleptMu sync.Mutex
	h := start(t, func(o *Options) {
		o.Out = &term
		o.gateSleep = func(time.Duration) { sleptMu.Lock(); slept++; sleptMu.Unlock() }
	})
	if r, _ := h.postJSON("/api/setup/session", map[string]string{"code": "WRONG"}, nil); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong code: %d", r.StatusCode)
	}
	if !strings.Contains(term.String(), "wrong access code from 127.0.0.1:") || !strings.Contains(term.String(), "(1 of 10)") {
		t.Fatalf("terminal:\n%s", term.String())
	}
	h.login()
	for i := range 2 * MaxFailures {
		r, b := h.postJSON("/api/setup/session", map[string]string{"code": fmt.Sprintf("WRONG%d", i)}, nil)
		if r.StatusCode != http.StatusUnauthorized || !strings.Contains(string(b), "already open") {
			t.Fatalf("attempt %d after use: %d %s", i, r.StatusCode, b)
		}
	}
	select {
	case err := <-h.done:
		t.Fatalf("setup stopped: %v", err)
	default:
	}
	if st := h.status(0); st.Phase != PhaseQuestions {
		t.Fatalf("status %+v", st)
	}
	sleptMu.Lock()
	defer sleptMu.Unlock()
	if slept != 1 {
		t.Fatalf("%d delays (only the wrong code before the sign-in waits)", slept)
	}
	out := term.String()
	if n := strings.Count(out, "refused: the access code was used already"); n != maxRefusalLines {
		t.Fatalf("%d refusal lines, want %d:\n%s", n, maxRefusalLines, out)
	}
	if strings.Contains(strings.ToUpper(out), "WRONG0") || strings.Count(out, h.code) != 2 {
		t.Fatalf("a typed code on the terminal:\n%s", out)
	}
}

// TestBrowserStopWithOpenPoll: a pending long poll ends with the server,
// so Run returns nil at once instead of waiting for the poll.
func TestBrowserStopWithOpenPoll(t *testing.T) {
	h := start(t, nil)
	h.login()
	st := h.status(0)
	for st.Step == nil {
		st = h.status(0)
	}
	polled := make(chan int, 1)
	go func() {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, fmt.Sprintf("%s/api/setup/status?after=%d", h.base, st.Step.ID), http.NoBody)
		if err != nil {
			polled <- -1
			return
		}
		resp, err := h.client.Do(req)
		if err != nil {
			polled <- -1
			return
		}
		closeBody(t, resp)
		polled <- resp.StatusCode
	}()
	time.Sleep(200 * time.Millisecond) // the poll waits
	begin := time.Now()
	h.cancel()
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		h.done <- nil // for the cleanup
	case <-time.After(4 * time.Second):
		t.Fatal("Run waited for the open poll")
	}
	if d := time.Since(begin); d > 3*time.Second {
		t.Fatalf("stopping took %v", d)
	}
	select {
	case <-polled:
	case <-time.After(5 * time.Second):
		t.Fatal("the poll did not end")
	}
}

// TestBrowserPollsAreNotActivity: an open tab's status polls do not keep
// setup alive; answers do.
func TestBrowserPollsAreNotActivity(t *testing.T) {
	h := start(t, func(o *Options) { o.Idle = 600 * time.Millisecond })
	h.login()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-h.done:
			h.done <- nil
			return
		case <-deadline:
			t.Fatal("status polls kept the idle setup running")
		case <-time.After(100 * time.Millisecond):
		}
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, h.base+"/api/setup/status", http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		if resp, err := h.client.Do(req); err == nil {
			closeBody(t, resp)
		}
	}
}

// TestBrowserAnswersAreActivity: answering keeps setup alive past Idle.
func TestBrowserAnswersAreActivity(t *testing.T) {
	h := start(t, func(o *Options) { o.Idle = 700 * time.Millisecond })
	h.login()
	end := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(end) {
		st := h.status(0)
		if st.Step != nil {
			// A stale answer still counts as a request of the session.
			h.postJSON("/api/setup/answer", Answer{Step: st.Step.ID + 1000}, nil)
		}
		time.Sleep(150 * time.Millisecond)
	}
	select {
	case <-h.done:
		t.Fatal("setup stopped while answers came in")
	default:
	}
}

// TestBrowserSecondAnswerDuringValidation: once a screen has an answer,
// later answers are stale (a secret one is zeroed), also while the first
// is still being validated.
func TestBrowserSecondAnswerDuringValidation(t *testing.T) {
	ui := NewUI(context.Background())
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := ui.Secret("pw", "", func([]byte) error {
			close(entered)
			<-release
			return nil
		})
		done <- err
	}()
	var step Step
	for {
		s, ok, ch := ui.Current()
		if ok {
			step = s
			break
		}
		<-ch
	}
	if err := ui.AnswerSecret(step.ID, []byte("first-secret")); err != nil {
		t.Fatal(err)
	}
	<-entered
	second := []byte("second-secret")
	if err := ui.AnswerSecret(step.ID, second); !errors.Is(err, ErrStale) {
		t.Fatalf("second answer: %v", err)
	}
	if !bytes.Equal(second, make([]byte, len(second))) {
		t.Fatal("the refused second secret was not zeroed")
	}
	if err := ui.Answer(Answer{Step: step.ID, Back: true}); !errors.Is(err, ErrStale) {
		t.Fatalf("back after the answer: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// TestBrowserBufferedAnswerZeroed: an answer still buffered when its
// screen goes away (the server stops) is zeroed.
func TestBrowserBufferedAnswerZeroed(t *testing.T) {
	p := &pending{answers: make(chan Answer, 1)}
	secret := []byte("buffered-secret")
	p.answers <- Answer{secret: secret}
	drain(p)
	if !bytes.Equal(secret, make([]byte, len(secret))) {
		t.Fatal("the buffered secret was not zeroed")
	}
	drain(p) // empty: no block
}

// TestBrowserTLS12Refused: the setup server speaks TLS 1.3 only.
func TestBrowserTLS12Refused(t *testing.T) {
	h := start(t, nil)
	tr, ok := h.client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("transport")
	}
	cfg := tr.TLSClientConfig.Clone()
	cfg.MinVersion, cfg.MaxVersion = tls.VersionTLS12, tls.VersionTLS12
	c := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: cfg}}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, h.base+"/", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err == nil {
		closeBody(t, resp)
		t.Fatal("a TLS 1.2 connection was accepted")
	}
}

// TestBrowserHostCheck: with named addresses, a request for another Host
// (DNS rebinding) is refused; the names and loopback work.
func TestBrowserHostCheck(t *testing.T) {
	h := start(t, func(o *Options) { o.Hosts = []string{"setup.example.com"} })
	if r, _ := h.do(http.MethodGet, "/", "", nil, nil); r.StatusCode != http.StatusOK {
		t.Fatalf("localhost: %d", r.StatusCode)
	}
	for host, want := range map[string]int{"evil.example": http.StatusMisdirectedRequest, "setup.example.com:8443": http.StatusOK, "SETUP.example.com": http.StatusOK, "127.0.0.1:8443": http.StatusOK} {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, h.base+"/", http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		resp, err := h.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		closeBody(t, resp)
		if resp.StatusCode != want {
			t.Errorf("Host %s: %d, want %d", host, resp.StatusCode, want)
		}
	}
	// Without names every Host is accepted (the operator did not name one).
	h2 := start(t, nil)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, h2.base+"/", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "anything.example"
	resp, err := h2.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	closeBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("no names: %d", resp.StatusCode)
	}
}

// TestBrowserNoDirectoryListing: the SPA's files are served, directories
// never listed.
func TestBrowserNoDirectoryListing(t *testing.T) {
	h := start(t, func(o *Options) {
		o.Assets = fstest.MapFS{
			"index.html":    {Data: []byte("<!doctype html><title>setup</title>")},
			"assets/app.js": {Data: []byte("console.log(1)")},
		}
	})
	for p, want := range map[string]int{"/": http.StatusOK, "/assets/app.js": http.StatusOK, "/assets/": http.StatusNotFound, "/assets": http.StatusNotFound, "/missing.js": http.StatusNotFound} {
		if r, _ := h.do(http.MethodGet, p, "", nil, nil); r.StatusCode != want {
			t.Errorf("%s: %d, want %d", p, r.StatusCode, want)
		}
	}
}

// TestBrowserReportPoll: a poll that follows the report returns when the
// report changes (a live report while setup works); without ?report= it
// waits for a step only.
func TestBrowserReportPoll(t *testing.T) {
	proceed := make(chan struct{})
	h := start(t, func(o *Options) {
		o.Mode, o.Init = ModeDatabase, nil
		o.Database = func(ctx context.Context, ui wizard.UI, _ string) error {
			<-proceed
			_, _ = fmt.Fprintln(ui.Out(), "migrations running")
			select {
			case <-ctx.Done():
			case <-time.After(30 * time.Second):
			}
			return nil
		}
		if err := os.WriteFile(filepath.Join(o.Dir, setup.StateFileName), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	h.login()
	st := h.status(0)
	for !strings.Contains(st.Report, "== database ==") {
		time.Sleep(20 * time.Millisecond)
		st = h.status(0)
	}
	if st.Zip {
		t.Fatal("the database run offers the zip")
	}
	if r, _ := h.do(http.MethodGet, "/api/setup/config.zip", "", nil, nil); r.StatusCode != http.StatusConflict {
		t.Fatalf("zip in the database run: %d", r.StatusCode)
	}
	got := make(chan Status, 1)
	go func() {
		got <- h.statusPath(fmt.Sprintf("/api/setup/status?after=0&report=%d", st.ReportRev), false, 0)
	}()
	time.Sleep(100 * time.Millisecond)
	close(proceed)
	select {
	case next := <-got:
		if next.ReportRev == st.ReportRev || !strings.Contains(next.Report, "migrations running") {
			t.Fatalf("report poll: %+v", next)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the report poll did not return on the change")
	}
}

// closeBody closes a response body read by a test.
func closeBody(t *testing.T, resp *http.Response) {
	t.Helper()
	if err := resp.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}
}
