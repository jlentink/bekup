package web

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/setup"
	"github.com/jlentink/bekup/internal/setup/wizard"
)

// Server limits (D-217).
const (
	// MaxBody bounds every request body.
	MaxBody = 64 << 10
	// DefaultIdle ends the server after this long without a request of
	// the session.
	DefaultIdle = 30 * time.Minute
	// DefaultLifetime ends the server after this long in any case.
	DefaultLifetime = 2 * time.Hour
	// pollWait is the longest a status request waits for a change.
	pollWait = 25 * time.Second
	// maxRefusalLines bounds the refused sign-ins printed to the terminal
	// once the code was used (those are neither delayed nor counted).
	maxRefusalLines = 20
	// ListenPort is the port inside the container.
	ListenPort = 8443
	// HeaderMarker must be on every state-changing request.
	HeaderMarker = "X-Bekup-Setup"
)

// Phases of the setup flow.
const (
	PhaseQuestions = "questions" // a screen waits (Step set) or setup works
	PhaseDone      = "done"      // everything ran; the zip is available
	PhaseFailed    = "failed"    // the flow stopped with Error
)

// Modes of a run (D-217): the setup-web service writes the deployment,
// the setup-web-db service (UID 65532, migrate's configuration and
// secrets, only the state file of the deployment directory) prepares the
// database.
const (
	ModeInit     = "init"
	ModeDatabase = "database"
)

// Options configure the browser setup.
type Options struct {
	// Mode is ModeInit (default) or ModeDatabase.
	Mode string
	// Dir is the deployment directory.
	Dir string
	// Listen is the listen address (default ":8443").
	Listen string
	// PublicPort is the host's published port shown in the URL (default
	// ListenPort).
	PublicPort int
	// Hosts are the names or addresses the operator opens setup with
	// (certificate SANs and the printed URLs).
	Hosts []string
	// Env is the wizard environment (UI and Dir are set by the server).
	Env wizard.Env
	// Init writes the deployment (setup.Init; tests replace it).
	Init func(setup.Options) error
	// Check validates the deployment (setup.Check).
	Check func(setup.CheckOptions) ([]setup.Finding, error)
	// Database creates the roles and migrates the app database (database
	// mode). It asks through ui.
	Database func(ctx context.Context, ui wizard.UI, dir string) error
	// Assets is the setup SPA (index.html at the root).
	Assets fs.FS
	// Out is the terminal: URL, code and fingerprint.
	Out io.Writer
	// Logger logs requests (never secrets, codes or answers).
	Logger *slog.Logger
	// Idle and Lifetime override DefaultIdle and DefaultLifetime.
	Idle, Lifetime time.Duration
	// Now is the clock (tests).
	Now func() time.Time
	// listener replaces the TCP listener (tests).
	listener net.Listener
	// gateSleep replaces the failure delay (tests).
	gateSleep func(time.Duration)
	// ready is called with the code once the server listens (tests).
	ready func(code string, cert *Certificate)
}

// server is one browser setup run.
type server struct {
	opts   Options
	ui     *UI
	gate   *Gate
	ctx    context.Context
	cancel context.CancelFunc
	// hosts are the allowed Host names (nil: any; D-217 amendment 1).
	hosts map[string]bool

	mu       sync.Mutex
	phase    string
	errMsg   string
	written  bool // setup.Init succeeded (the zip is available)
	finished bool
	activity chan struct{}
	// refusals counts the refused sign-ins after the code was used.
	refusals int
	// zipNotes say what the zip leaves out (setup.ConfigZipNotes).
	zipNotes []string
}

// Status is the browser's view of the flow.
type Status struct {
	Phase  string `json:"phase"`
	Step   *Step  `json:"step,omitempty"`
	Report string `json:"report"`
	// ReportRev is the report's revision (?report= of a long poll).
	ReportRev uint64 `json:"reportRev"`
	Error     string `json:"error,omitempty"`
	Zip       bool   `json:"zip"`
	// ZipNotes say what the zip leaves out (the operator's own .env lines,
	// a changed compose.override.yaml).
	ZipNotes []string `json:"zipNotes,omitempty"`
}

// Run serves the browser setup until it is finished, idle, out of time,
// out of code attempts or ctx ends.
func Run(ctx context.Context, opts Options) error {
	if opts.Listen == "" {
		opts.Listen = ":" + strconv.Itoa(ListenPort)
	}
	if opts.PublicPort == 0 {
		opts.PublicPort = ListenPort
	}
	if opts.Idle == 0 {
		opts.Idle = DefaultIdle
	}
	if opts.Lifetime == 0 {
		opts.Lifetime = DefaultLifetime
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.Mode == "" {
		opts.Mode = ModeInit
	}
	if opts.Check == nil || opts.Assets == nil || opts.Out == nil ||
		opts.Mode == ModeInit && opts.Init == nil || opts.Mode == ModeDatabase && opts.Database == nil ||
		opts.Mode != ModeInit && opts.Mode != ModeDatabase {
		return errors.New("setup web: Check, Assets, Out and Init (init mode) or Database (database mode) are required")
	}
	// A build without the setup UI (web/dist-setup holds only .gitkeep)
	// would print a code for a page that does not exist.
	if _, err := fs.Stat(opts.Assets, "index.html"); err != nil {
		return errors.New("setup web: this bekup build does not include the browser setup's UI (web/dist-setup); use the terminal setup instead (docker compose run --rm setup)")
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Lifetime)
	defer cancel()
	s := &server{opts: opts, ctx: ctx, cancel: cancel, phase: PhaseQuestions, activity: make(chan struct{}, 1), hosts: allowedHosts(opts.Hosts)}
	s.ui = NewUI(ctx)
	gate, code, err := NewGate(cancel)
	if err != nil {
		return err
	}
	if opts.gateSleep != nil {
		gate.sleep = opts.gateSleep
	}
	s.gate = gate
	cert, err := NewCertificate(opts.Hosts, opts.Now())
	if err != nil {
		return fmt.Errorf("setup web: certificate: %w", err)
	}
	ln := opts.listener
	if ln == nil {
		var lc net.ListenConfig
		if ln, err = lc.Listen(ctx, "tcp", opts.Listen); err != nil {
			return fmt.Errorf("setup web: %w", err)
		}
	}
	tln := tls.NewListener(ln, cert.TLSConfig())
	srv := &http.Server{
		Handler:           s.handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      pollWait + 30*time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          slog.NewLogLogger(opts.Logger.Handler(), slog.LevelDebug),
		// Requests end with the server: a long poll never holds up the
		// shutdown.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	s.printAccess(code, cert.Fingerprint)
	if opts.ready != nil {
		opts.ready(code, cert)
	}
	flowDone := make(chan struct{})
	go func() {
		defer close(flowDone)
		s.flow(ctx)
	}()
	go s.watchIdle(ctx)
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(tln) }()
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		cancel()
		<-flowDone
		return err
	}
	shutCtx, shutCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer shutCancel()
	err = srv.Shutdown(shutCtx)
	<-flowDone
	<-serveErr
	s.mu.Lock()
	finished := s.finished
	s.mu.Unlock()
	switch {
	case finished:
		_, _ = fmt.Fprintln(opts.Out, "\nbekup setup finished. The browser setup has stopped.")
	case errors.Is(context.Cause(ctx), context.DeadlineExceeded):
		_, _ = fmt.Fprintln(opts.Out, "\nbekup setup stopped: time limit reached.")
	default:
		_, _ = fmt.Fprintln(opts.Out, "\nbekup setup stopped (idle, too many wrong codes, or interrupted).")
	}
	return err
}

// printAccess writes the access URLs, the code and the fingerprint to the
// terminal (the setup-web service runs with logging none).
func (s *server) printAccess(code, fingerprint string) {
	w := s.opts.Out
	_, _ = fmt.Fprintln(w, "\nbekup browser setup (D-217). Open one of these addresses in your browser:")
	hosts := s.opts.Hosts
	if len(hosts) == 0 {
		hosts = []string{"<this server's address>"}
	}
	for _, h := range hosts {
		host := h
		if strings.Contains(h, ":") {
			host = "[" + h + "]"
		}
		_, _ = fmt.Fprintf(w, "    https://%s:%d/#code=%s\n", host, s.opts.PublicPort, code)
	}
	_, _ = fmt.Fprintf(w, "\nAccess code (one use): %s\n", code)
	_, _ = fmt.Fprintf(w, "The browser warns about the self-signed certificate. Continue only if its\nSHA-256 fingerprint is exactly:\n    %s\n", fingerprint)
	_, _ = fmt.Fprintf(w, "\nSetup stops when it is finished, after %s without activity, after %s,\nor after %d wrong codes. Press Ctrl+C to stop it now.\n", s.opts.Idle, s.opts.Lifetime, MaxFailures)
}

// watchIdle ends the server after Idle without session activity.
func (s *server) watchIdle(ctx context.Context) {
	t := time.NewTimer(s.opts.Idle)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.activity:
			if !t.Stop() {
				select {
				case <-t.C:
				default:
				}
			}
			t.Reset(s.opts.Idle)
		case <-t.C:
			s.cancel()
			return
		}
	}
}

func (s *server) touch() {
	select {
	case s.activity <- struct{}{}:
	default:
	}
}

// flow runs one mode: init asks the questions and writes the deployment
// (then checks it); database checks the deployment and prepares the
// database.
func (s *server) flow(ctx context.Context) {
	out := s.ui.Out()
	_, err := os.Lstat(filepath.Join(s.opts.Dir, setup.StateFileName))
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		s.fail(err)
		return
	}
	switch {
	case s.opts.Mode == ModeDatabase && !exists:
		s.fail(errors.New("no deployment here yet: run the setup-web service first (docker compose run --rm --service-ports setup-web)"))
		return
	case s.opts.Mode == ModeInit && exists:
		_, _ = fmt.Fprintln(out, "This directory holds a bekup deployment already: nothing is written. Checking it.")
	case s.opts.Mode == ModeInit:
		if err := s.initDeployment(); err != nil {
			s.fail(err)
			return
		}
	}
	s.mu.Lock()
	s.written = true
	s.mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	if s.opts.Mode == ModeInit {
		_, _ = fmt.Fprintln(out, "\n== setup check ==")
		if _, err := s.opts.Check(setup.CheckOptions{Dir: s.opts.Dir, Version: s.opts.Env.Version, Ownership: s.opts.Env.Ownership, Out: out}); err != nil && !errors.Is(err, setup.ErrCheckFailed) {
			s.fail(err)
			return
		}
	} else {
		// The database run (UID 65532) sees only the state file of the
		// deployment directory (D-217 amendment 1: never the keys
		// directory), so it cannot check the deployment. The first run
		// (root) checked it.
		_, _ = fmt.Fprintln(out, "\nsetup check ran in the first run (this run sees only the state file of the deployment directory); after changes run:\n"+
			"    docker compose -f compose.yaml run --rm setup check")
	}
	if s.opts.Mode == ModeInit {
		if notes, err := setup.ConfigZipNotes(s.opts.Dir); err == nil {
			s.mu.Lock()
			s.zipNotes = notes
			s.mu.Unlock()
		}
		_, _ = fmt.Fprintln(out, "\nNext: prepare the database in the browser. Stop this setup (Finish), then run\n"+
			"    docker compose up -d postgres            (only with the bundled database)\n"+
			"    docker compose run --rm --service-ports setup-web-db")
	} else {
		_, _ = fmt.Fprintln(out, "\n== database ==")
		if err := s.opts.Database(ctx, s.ui, s.opts.Dir); err != nil {
			s.fail(err)
			return
		}
		_, _ = fmt.Fprintln(out, "\nNext: docker compose up -d, then create the first admin on the host:\n    docker compose run --rm admin create --email you@example.com --display-name \"Your Name\"")
	}
	s.mu.Lock()
	s.phase = PhaseDone
	s.mu.Unlock()
	s.ui.wake()
}

// initDeployment asks the init questions and writes the deployment.
func (s *server) initDeployment() error {
	env := s.opts.Env
	env.Dir, env.UI, env.Browser = s.opts.Dir, s.ui, true
	plan, err := wizard.RunInit(env)
	if err != nil {
		return err
	}
	defer plan.Destroy()
	if err := s.opts.Init(plan.Options); err != nil {
		return err
	}
	s.mu.Lock()
	s.written = true
	s.mu.Unlock()
	_, _ = fmt.Fprintf(s.ui.Out(), "\nThe same without the browser (no secrets in it): %s\n", strings.Join(plan.Flags, " "))
	if plan.FlagsNote != "" {
		_, _ = fmt.Fprintf(s.ui.Out(), "(%s)\n", plan.FlagsNote)
	}
	return nil
}

// fail ends the flow with err (aborts are not failures: the server is
// stopping anyway).
func (s *server) fail(err error) {
	if errors.Is(err, wizard.ErrAborted) {
		return
	}
	s.mu.Lock()
	s.phase = PhaseFailed
	s.errMsg = logging.DefaultRegistry.Scrub(err.Error())
	s.mu.Unlock()
	s.ui.wake()
}

func (s *server) status() Status {
	var st Status
	st.Report, st.ReportRev = s.ui.Report()
	if step, ok, _ := s.ui.Current(); ok {
		st.Step = &step
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st.Phase, st.Error, st.Zip = s.phase, s.errMsg, s.zipAvailable()
	if st.Zip {
		st.ZipNotes = s.zipNotes
	}
	return st
}

// zipAvailable: the first run offers the zip once the files exist. The
// database run never does (it sees only the state file; D-217 amendment
// 1). Call with s.mu held.
func (s *server) zipAvailable() bool {
	return s.written && s.opts.Mode == ModeInit
}

// handler builds the routes behind bekup's security headers and the body
// limit.
func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/setup/session", s.post(false, s.handleSession))
	// Status polls are not activity: an open tab must not keep the
	// server alive forever.
	mux.HandleFunc("GET /api/setup/status", s.get(false, s.handleStatus))
	mux.HandleFunc("POST /api/setup/answer", s.post(true, s.handleAnswer))
	mux.HandleFunc("POST /api/setup/secret", s.post(true, s.handleSecret))
	mux.HandleFunc("POST /api/setup/reveal", s.post(true, s.handleReveal))
	mux.HandleFunc("GET /api/setup/config.zip", s.get(true, s.handleZip))
	mux.HandleFunc("POST /api/setup/finish", s.post(true, s.handleFinish))
	mux.Handle("GET /", s.static())
	return httpserver.SecurityHeaders(true)(s.checkHost(http.MaxBytesHandler(mux, MaxBody)))
}

// allowedHosts are the Host names a request may carry when the operator
// named the setup's addresses: those and the certificate's loopback names
// (an SSH tunnel). Without names every Host is accepted.
func allowedHosts(hosts []string) map[string]bool {
	if len(hosts) == 0 {
		return nil
	}
	m := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
	for _, h := range hosts {
		m[strings.ToLower(strings.Trim(h, "[]"))] = true
	}
	return m
}

// checkHost refuses a request whose Host is not one of the named
// addresses (a cheap defense against DNS rebinding).
func (s *server) checkHost(next http.Handler) http.Handler {
	if s.hosts == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if !s.hosts[strings.ToLower(strings.Trim(host, "[]"))] {
			writeJSONError(w, http.StatusMisdirectedRequest, "open setup with one of the addresses the terminal shows")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// static serves the SPA: files that exist, index.html for "/"; never a
// directory listing.
func (s *server) static() http.Handler {
	files := http.FileServerFS(s.opts.Assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		if name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/"); name != "" {
			if fi, err := fs.Stat(s.opts.Assets, name); err != nil || fi.IsDir() {
				http.NotFound(w, r)
				return
			}
		}
		files.ServeHTTP(w, r)
	})
}

// sessionOK checks the session cookie.
func (s *server) sessionOK(r *http.Request) bool {
	c, err := r.Cookie(CookieName)
	return err == nil && s.gate.Valid(c.Value)
}

// get guards a read: the session only. activity: the read counts against
// the idle timeout (the zip download, never a status poll).
func (s *server) get(activity bool, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.sessionOK(r) {
			writeJSONError(w, http.StatusUnauthorized, "sign in with the access code")
			return
		}
		if activity {
			s.touch()
		}
		h(w, r)
	}
}

// post guards a state-changing request: the marker header, Origin and
// Sec-Fetch-Site of this very origin, and (needSession) the session. The
// content type is checked by each handler.
func (s *server) post(needSession bool, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// The allowed origin is the request's own: setup has no base URL.
		// A foreign page cannot send the marker header without a CORS
		// preflight, which this server never answers.
		self := []string{"https://" + strings.ToLower(r.Host)}
		if r.Header.Get(HeaderMarker) != "1" || len(r.Header.Values("Sec-Fetch-Site")) != 1 || !httpserver.OriginAllowed(r, self) {
			writeJSONError(w, http.StatusForbidden, "request refused")
			return
		}
		if needSession {
			if !s.sessionOK(r) {
				writeJSONError(w, http.StatusUnauthorized, "sign in with the access code")
				return
			}
			s.touch()
		}
		h(w, r)
	}
}

func isContentType(r *http.Request, want string) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mt == want
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if !isContentType(r, "application/json") {
		writeJSONError(w, http.StatusUnsupportedMediaType, "JSON expected")
		return false
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request")
		return false
	}
	return true
}

func (s *server) handleSession(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	session, failures, err := s.gate.Login(in.Code)
	switch {
	case errors.Is(err, ErrBadCode), errors.Is(err, ErrSessionTaken), errors.Is(err, ErrExhausted):
		s.printRefusal(r, err, failures)
		writeJSONError(w, http.StatusUnauthorized, err.Error())
		return
	case err != nil:
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: session, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	s.touch()
	w.WriteHeader(http.StatusNoContent)
}

// printRefusal shows a refused sign-in on the terminal with the peer's
// address (never the code), so the operator sees probing. After the code
// was used the refusals are neither delayed nor counted: only the first
// maxRefusalLines are printed.
func (s *server) printRefusal(r *http.Request, err error, failures int) {
	peer := r.RemoteAddr
	if errors.Is(err, ErrSessionTaken) {
		s.mu.Lock()
		s.refusals++
		n := s.refusals
		s.mu.Unlock()
		switch {
		case n < maxRefusalLines:
			_, _ = fmt.Fprintf(s.opts.Out, "setup web: sign-in from %s refused: the access code was used already\n", peer)
		case n == maxRefusalLines:
			_, _ = fmt.Fprintf(s.opts.Out, "setup web: sign-in from %s refused: the access code was used already (further refusals are not shown)\n", peer)
		}
		return
	}
	_, _ = fmt.Fprintf(s.opts.Out, "setup web: wrong access code from %s (%d of %d)\n", peer, failures, MaxFailures)
}

// handleStatus answers at once, or with ?after=<step id> waits (at most
// pollWait) until another step waits (after=0: until a step waits), the
// flow ended, the server stops or, with ?report=<revision>, the report
// changed.
func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if after := q.Get("after"); after != "" {
		id, err := strconv.ParseUint(after, 10, 64)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request")
			return
		}
		followReport := q.Has("report")
		var rev uint64
		if followReport {
			if rev, err = strconv.ParseUint(q.Get("report"), 10, 64); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid request")
				return
			}
		}
		timer := time.NewTimer(pollWait)
		defer timer.Stop()
	wait:
		for {
			step, ok, changed := s.ui.Current()
			if ok && step.ID != id || s.ended() || followReport && s.ui.ReportRev() != rev {
				break
			}
			select {
			case <-changed:
			case <-timer.C:
				break wait
			case <-s.ctx.Done():
				break wait
			case <-r.Context().Done():
				break wait
			}
		}
	}
	writeJSON(w, s.status())
}

// ended reports a flow that no longer asks (or a server that stops).
func (s *server) ended() bool {
	if s.ctx.Err() != nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.phase != PhaseQuestions
}

func (s *server) handleAnswer(w http.ResponseWriter, r *http.Request) {
	var a Answer
	if !decodeJSON(w, r, &a) {
		return
	}
	if err := s.ui.Answer(a); err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSecret takes a secret answer as raw bytes (never a JSON string):
// ?step=<id>, Content-Type application/octet-stream; an empty body goes
// back.
func (s *server) handleSecret(w http.ResponseWriter, r *http.Request) {
	if !isContentType(r, "application/octet-stream") {
		writeJSONError(w, http.StatusUnsupportedMediaType, "octet-stream expected")
		return
	}
	id, err := strconv.ParseUint(r.URL.Query().Get("step"), 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request")
		return
	}
	// One fixed buffer, one byte over the limit to tell an oversized body
	// (refused, never truncated): growing would leave copies of the secret
	// behind.
	buf := make([]byte, MaxBody+1)
	n, err := io.ReadFull(r.Body, buf)
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) || n > MaxBody {
		clear(buf)
		writeJSONError(w, http.StatusRequestEntityTooLarge, "the answer is too long")
		return
	}
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		clear(buf)
		writeJSONError(w, http.StatusBadRequest, "invalid request")
		return
	}
	secret := bytes.Clone(buf[:n])
	clear(buf)
	if len(secret) == 0 {
		err = s.ui.Answer(Answer{Step: id, Back: true})
	} else {
		err = s.ui.AnswerSecret(id, secret)
	}
	if err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleReveal(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Step uint64 `json:"step"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if err := s.ui.Reveal(in.Step, w); err != nil {
		w.Header().Del("Content-Type")
		writeJSONError(w, http.StatusConflict, err.Error())
	}
}

func (s *server) handleZip(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	available := s.zipAvailable()
	s.mu.Unlock()
	if !available {
		writeJSONError(w, http.StatusConflict, "the zip is offered by the first run (setup-web) once the files are written")
		return
	}
	var buf bytes.Buffer
	if err := setup.WriteConfigZip(s.opts.Dir, &buf, s.opts.Now()); err != nil {
		s.opts.Logger.WarnContext(r.Context(), "setup_web_zip_failed", slog.String("error", logging.DefaultRegistry.Scrub(err.Error())))
		writeJSONError(w, http.StatusInternalServerError, "could not build the zip")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="bekup-config.zip"`)
	if _, err := w.Write(buf.Bytes()); err != nil {
		return // the browser went away
	}
}

func (s *server) handleFinish(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	done := s.phase != PhaseQuestions
	if done {
		s.finished = s.phase == PhaseDone
	}
	s.mu.Unlock()
	if !done {
		writeJSONError(w, http.StatusConflict, "setup is still running")
		return
	}
	w.WriteHeader(http.StatusNoContent)
	go s.cancel()
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		return // the browser went away
	}
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{msg}); err != nil {
		return // the browser went away
	}
}
