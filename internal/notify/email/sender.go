package email

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/hostname"
	"github.com/jlentink/bekup/internal/logging"
	"github.com/jlentink/bekup/internal/netguard"
)

// ErrNotConfigured means that no SMTP settings are stored or sending is disabled.
var ErrNotConfigured = errors.New("email: SMTP is not configured")

// Code classifies a send failure. The codes and their messages are safe to
// show to admins; Detail of a SendError (the blocked address) is for admins
// only.
type Code string

// Send failure codes.
const (
	CodeNotConfigured   Code = "not_configured"
	CodeNotAllowed      Code = "not_allowed"
	CodeUnreachable     Code = "unreachable"
	CodeTLS             Code = "tls_failed"
	CodeNoSTARTTLS      Code = "starttls_unavailable"
	CodeAuthFailed      Code = "auth_failed"
	CodeAuthUnsupported Code = "auth_unsupported"
	CodeRejected        Code = "rejected"
	CodeInvalidMessage  Code = "invalid_message"
	CodeFailed          Code = "failed"
)

var codeMessages = map[Code]string{
	CodeNotConfigured:   "e-mail is not configured (the SMTP settings are missing or disabled)",
	CodeNotAllowed:      "the SMTP server's address is not allowed by the SSRF guard (private relays must be allowlisted)",
	CodeUnreachable:     "the SMTP server could not be reached",
	CodeTLS:             "the TLS handshake with the SMTP server failed (certificate, host name or protocol version)",
	CodeNoSTARTTLS:      "the SMTP server does not offer STARTTLS",
	CodeAuthFailed:      "the SMTP server rejected the username or password",
	CodeAuthUnsupported: "the SMTP server offers no supported AUTH mechanism (PLAIN or LOGIN)",
	CodeRejected:        "the SMTP server rejected the message or a recipient",
	CodeInvalidMessage:  "the message is invalid",
	CodeFailed:          "sending the e-mail failed",
}

// Message returns the presentable text of c.
func (c Code) Message() string {
	if m, ok := codeMessages[c]; ok {
		return m
	}
	return codeMessages[CodeFailed]
}

// SendError is a classified send failure.
type SendError struct {
	Code Code
	// Detail is extra information for admins only (the blocked address or
	// the SMTP reply code); never server text or message content.
	Detail string
	err    error
}

func (e *SendError) Error() string { return "email: send: " + string(e.Code) }

// Unwrap returns the underlying error.
func (e *SendError) Unwrap() error { return e.err }

// Mailer sends messages (the Sender; tests use fakes).
type Mailer interface {
	Send(ctx context.Context, m *Message) error
}

// Delivery describes the connection a message went over (D-180).
type Delivery struct {
	// Encrypted is true when the SMTP session used TLS.
	Encrypted bool
	// TLSVersion is the negotiated version (e.g. "TLS 1.3"); empty
	// without TLS.
	TLSVersion string
}

// DeliveryMailer is a Mailer that also reports the connection security
// (the Sender; used by the admin test e-mail).
type DeliveryMailer interface {
	Mailer
	SendDelivery(ctx context.Context, m *Message) (Delivery, error)
}

// SenderOptions configure NewSender.
type SenderOptions struct {
	Settings *Settings
	Guard    *netguard.Guard
	// Dev is ignored: since D-180 the SMTP modes do not depend on dev mode
	// (kept so existing callers compile).
	Dev bool
	// HeloName is the name sent in EHLO (normally the host of
	// server.base_url); "localhost" when empty or invalid.
	HeloName string
	Logger   *slog.Logger
	// Now replaces time.Now (tests).
	Now func() time.Time
}

// Sender sends e-mail with the stored SMTP settings.
type Sender struct {
	o SenderOptions

	// warned holds the endpoints already warned about an unencrypted
	// send (one WARN per process and endpoint).
	mu     sync.Mutex
	warned map[string]struct{}
}

// maxWarnedEndpoints bounds Sender.warned.
const maxWarnedEndpoints = 64

// NewSender validates o.
func NewSender(o SenderOptions) (*Sender, error) {
	if o.Settings == nil || o.Guard == nil {
		return nil, errors.New("email: Settings and Guard are required")
	}
	o.HeloName = hostname.Normalize(o.HeloName)
	if o.HeloName == "" || hostname.Validate(o.HeloName) != nil {
		o.HeloName = "localhost"
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Sender{o: o}, nil
}

// Send delivers m with the current settings. Failures are a *SendError
// (or the context's error).
func (s *Sender) Send(ctx context.Context, m *Message) error {
	_, err := s.SendDelivery(ctx, m)
	return err
}

// SendDelivery is Send that also reports whether the session was encrypted
// (as far as it got when it fails).
func (s *Sender) SendDelivery(ctx context.Context, m *Message) (Delivery, error) {
	cfg, pw, err := s.o.Settings.loadForSend(ctx)
	if errors.Is(err, ErrNotConfigured) {
		return Delivery{}, &SendError{Code: CodeNotConfigured, err: err}
	}
	if err != nil {
		return Delivery{}, err
	}
	defer crypto.Zero(pw)
	if len(pw) >= 4 {
		defer logging.DefaultRegistry.Register(pw)()
	}
	d, err := s.send(ctx, &cfg, pw, m)
	var se *SendError
	if errors.As(err, &se) {
		s.o.Logger.WarnContext(ctx, "email_send_failed", slog.String("code", string(se.Code)), slog.Int("recipients", len(m.To)))
	} else if err == nil {
		s.o.Logger.InfoContext(ctx, "email_sent", slog.Int("recipients", len(m.To)), slog.Bool("encrypted", d.Encrypted))
	}
	return d, err
}

// warnUnencrypted logs once per process and endpoint that a session ran
// without TLS (host and port only; never credentials or content).
func (s *Sender) warnUnencrypted(ctx context.Context, cfg *Config) {
	key := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	s.mu.Lock()
	_, seen := s.warned[key]
	if !seen {
		if s.warned == nil {
			s.warned = map[string]struct{}{}
		}
		if len(s.warned) >= maxWarnedEndpoints {
			clear(s.warned)
		}
		s.warned[key] = struct{}{}
	}
	s.mu.Unlock()
	if seen {
		return
	}
	s.o.Logger.WarnContext(ctx, "smtp_unencrypted_session",
		slog.String("host", cfg.Host), slog.Int("port", cfg.Port), slog.String("mode", string(cfg.Mode)),
		slog.String("detail", "e-mail (and AUTH, when configured) went to the SMTP server without TLS; logged once per server"))
}

// phase is the step of an SMTP session (error classification).
type phase int

const (
	phaseDial phase = iota
	phaseTLS
	phaseGreeting
	phaseSTARTTLS
	phaseAuth
	phaseEnvelope
	phaseData
)

func (s *Sender) send(ctx context.Context, cfg *Config, pw []byte, m *Message) (d Delivery, retErr error) {
	if !cfg.Mode.Valid() {
		return d, &SendError{Code: CodeFailed, Detail: "unknown SMTP mode"}
	}
	raw, err := m.build(sender{Address: cfg.FromAddress, Name: cfg.FromName}, s.o.Now())
	if err != nil {
		return d, &SendError{Code: CodeInvalidMessage, Detail: err.Error(), err: err}
	}
	tlsCfg, err := tlsConfigFor(cfg)
	if err != nil {
		return d, &SendError{Code: CodeTLS, err: err}
	}

	ctx, cancel := context.WithTimeout(ctx, cfg.sendTimeout())
	defer cancel()
	dctx, dcancel := context.WithTimeout(ctx, cfg.connectTimeout())
	conn, err := s.o.Guard.DialContext(dctx, "tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	dcancel()
	if err != nil {
		return d, classify(ctx, phaseDial, err)
	}
	defer func() { discard(conn.Close()) }()
	if dl, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(dl); err != nil {
			return d, classify(ctx, phaseDial, err)
		}
	}
	stop := context.AfterFunc(ctx, func() { discard(conn.Close()) })
	defer stop()

	if cfg.Mode == ModeImplicitTLS {
		tc := tls.Client(conn, tlsCfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			return d, classify(ctx, phaseTLS, err)
		}
		conn = tc
	}
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return d, classify(ctx, phaseGreeting, err)
	}
	defer func() { discard(c.Close()) }()
	if err := c.Hello(s.o.HeloName); err != nil {
		return d, classify(ctx, phaseGreeting, err)
	}
	offered, _ := c.Extension("STARTTLS")
	switch {
	case cfg.Mode == ModeSTARTTLS && !offered:
		return d, &SendError{Code: CodeNoSTARTTLS}
	case (cfg.Mode == ModeSTARTTLS || cfg.Mode == ModeAuto) && offered:
		// In auto mode a failed upgrade is an error, never a fallback.
		if err := c.StartTLS(tlsCfg); err != nil {
			return d, classify(ctx, phaseSTARTTLS, err)
		}
	}
	if st, ok := c.TLSConnectionState(); ok {
		d = Delivery{Encrypted: true, TLSVersion: tls.VersionName(st.Version)}
	} else {
		if !cfg.Mode.MayBeUnencrypted() {
			// Unreachable: starttls and implicit_tls always have TLS here.
			return d, &SendError{Code: CodeTLS, Detail: "no TLS session"}
		}
		s.warnUnencrypted(ctx, cfg)
	}
	if cfg.Username != "" {
		if err := s.auth(ctx, c, cfg, pw); err != nil {
			return d, err
		}
	}
	if err := c.Mail(cfg.FromAddress); err != nil {
		return d, classify(ctx, phaseEnvelope, err)
	}
	for _, to := range m.To {
		if err := c.Rcpt(to); err != nil {
			return d, classify(ctx, phaseEnvelope, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return d, classify(ctx, phaseData, err)
	}
	if _, err := w.Write(raw); err != nil {
		discard(w.Close())
		return d, classify(ctx, phaseData, err)
	}
	if err := w.Close(); err != nil {
		return d, classify(ctx, phaseData, err)
	}
	// The message is accepted; a failing QUIT does not change that.
	discard(c.Quit())
	return d, nil
}

// auth authenticates with PLAIN or LOGIN. Over TLS always; without TLS
// only when the mode permits plain text (auto, none: D-180). The
// mechanisms re-check this themselves (allowPlain).
func (s *Sender) auth(ctx context.Context, c *smtp.Client, cfg *Config, pw []byte) error {
	allowPlain := cfg.Mode.MayBeUnencrypted()
	if _, ok := c.TLSConnectionState(); !ok && !allowPlain {
		return &SendError{Code: CodeAuthUnsupported, Detail: "AUTH requires TLS"}
	}
	ok, params := c.Extension("AUTH")
	if !ok {
		return &SendError{Code: CodeAuthUnsupported}
	}
	mechs := strings.Fields(strings.ToUpper(params))
	var a credAuth
	switch {
	case containsString(mechs, "PLAIN"):
		a = &plainAuth{host: cfg.Host, user: cfg.Username, pass: pw, allowPlain: allowPlain}
	case containsString(mechs, "LOGIN"):
		a = &loginAuth{host: cfg.Host, user: cfg.Username, pass: pw, allowPlain: allowPlain}
	default:
		return &SendError{Code: CodeAuthUnsupported}
	}
	err := c.Auth(a)
	a.zero()
	if err != nil {
		return classify(ctx, phaseAuth, err)
	}
	return nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// tlsConfigFor returns the TLS client config: TLS >= 1.2, the SMTP host as
// server name and either the system roots or only the admin's CA bundle.
func tlsConfigFor(cfg *Config) (*tls.Config, error) {
	tc := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: hostname.Normalize(cfg.Host)}
	if cfg.CABundlePEM != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(cfg.CABundlePEM)) {
			return nil, errors.New("email: the CA bundle contains no certificate")
		}
		tc.RootCAs = pool
		return tc, nil
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("email: system roots: %w", err)
	}
	tc.RootCAs = pool
	return tc, nil
}

// classify maps an error of a session phase to a *SendError.
func classify(ctx context.Context, p phase, err error) error {
	if ctx.Err() != nil && errors.Is(context.Cause(ctx), context.Canceled) {
		return context.Canceled
	}
	var blocked *netguard.BlockedAddressError
	if errors.As(err, &blocked) {
		return &SendError{Code: CodeNotAllowed, Detail: blocked.Error(), err: err}
	}
	var tpe *textproto.Error
	if errors.As(err, &tpe) {
		detail := "SMTP reply " + strconv.Itoa(tpe.Code)
		switch p {
		case phaseAuth:
			return &SendError{Code: CodeAuthFailed, Detail: detail, err: err}
		case phaseSTARTTLS:
			return &SendError{Code: CodeNoSTARTTLS, Detail: detail, err: err}
		case phaseEnvelope, phaseData:
			return &SendError{Code: CodeRejected, Detail: detail, err: err}
		default:
			return &SendError{Code: CodeFailed, Detail: detail, err: err}
		}
	}
	if errors.Is(err, errAuthNoTLS) {
		return &SendError{Code: CodeAuthUnsupported, Detail: "AUTH requires TLS", err: err}
	}
	var ne net.Error
	timeout := ctx.Err() != nil || (errors.As(err, &ne) && ne.Timeout())
	switch n := netguard.Normalize(err); {
	case errors.Is(n, netguard.ErrNotAllowed):
		return &SendError{Code: CodeNotAllowed, err: err}
	case errors.Is(n, netguard.ErrTLS):
		return &SendError{Code: CodeTLS, err: err}
	case (p == phaseTLS || p == phaseSTARTTLS) && !timeout:
		// A failed handshake (remote alerts, a closed connection).
		return &SendError{Code: CodeTLS, err: err}
	case errors.Is(n, netguard.ErrUnreachable), ctx.Err() != nil:
		return &SendError{Code: CodeUnreachable, err: err}
	default:
		return &SendError{Code: CodeFailed, err: err}
	}
}

// discard drops an error that cannot change the outcome (closing, QUIT).
func discard(error) {}

// errAuthNoTLS: an AUTH mechanism was asked to run without TLS.
var errAuthNoTLS = errors.New("email: AUTH refused without TLS")

// credAuth is an smtp.Auth holding a password copy it can zero.
type credAuth interface {
	smtp.Auth
	zero()
}

// plainAuth is SASL PLAIN (RFC 4616), only to the configured host. Unlike
// net/smtp.PlainAuth (which refuses unencrypted non-localhost servers) it
// runs without TLS when allowPlain is set: only for the modes that permit
// plain text (auto, none; D-180), so the credentials then travel
// unencrypted. Without allowPlain it requires TLS.
type plainAuth struct {
	host, user string
	pass       []byte
	resp       []byte
	allowPlain bool
}

func (a *plainAuth) Start(si *smtp.ServerInfo) (string, []byte, error) {
	if !si.TLS && !a.allowPlain {
		return "", nil, errAuthNoTLS
	}
	if si.Name != a.host {
		return "", nil, errors.New("email: wrong host name")
	}
	a.resp = make([]byte, 0, len(a.user)+len(a.pass)+2)
	a.resp = append(a.resp, 0)
	a.resp = append(a.resp, a.user...)
	a.resp = append(a.resp, 0)
	a.resp = append(a.resp, a.pass...)
	return "PLAIN", a.resp, nil
}

func (a *plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("email: unexpected server challenge")
	}
	return nil, nil
}

func (a *plainAuth) zero() { crypto.Zero(a.resp) }

// loginAuth is the LOGIN mechanism (username, then password); TLS rules
// as for plainAuth.
type loginAuth struct {
	host, user string
	pass       []byte
	step       int
	resp       []byte
	allowPlain bool
}

func (a *loginAuth) Start(si *smtp.ServerInfo) (string, []byte, error) {
	if !si.TLS && !a.allowPlain {
		return "", nil, errAuthNoTLS
	}
	if si.Name != a.host {
		return "", nil, errors.New("email: wrong host name")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(_ []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	a.step++
	switch a.step {
	case 1:
		return []byte(a.user), nil
	case 2:
		a.resp = append([]byte(nil), a.pass...)
		return a.resp, nil
	default:
		return nil, errors.New("email: unexpected server challenge")
	}
}

func (a *loginAuth) zero() { crypto.Zero(a.resp) }
