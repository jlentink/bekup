package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"time"
)

// Access limits (D-217).
const (
	// codeBytes is the entropy of the one-time access code (128 bits).
	codeBytes = 16
	// sessionBytes is the entropy of the session value (256 bits).
	sessionBytes = 32
	// MaxFailures ends the server after this many wrong codes.
	MaxFailures = 10
	// failureDelay is the delay per failed attempt so far (capped).
	failureDelay = 500 * time.Millisecond
	maxDelay     = 5 * time.Second
)

// CookieName is the session cookie (host-only, Secure, path /).
const CookieName = "__Host-bekup-setup"

var codeEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// Errors of the gate.
var (
	ErrBadCode      = errors.New("wrong access code")
	ErrSessionTaken = errors.New("setup is already open in another browser session")
	ErrExhausted    = errors.New("too many wrong access codes: setup stopped")
)

// Gate is the one-time code exchanged once for the only session. Only
// hashes are kept; the code itself lives in the terminal output.
type Gate struct {
	mu          sync.Mutex
	codeHash    [32]byte
	sessionHash [32]byte
	used        bool
	failures    int
	// onExhausted is called once when MaxFailures is reached.
	onExhausted func()
	sleep       func(time.Duration)
}

// NewGate returns a gate and its code (base32, 26 characters).
func NewGate(onExhausted func()) (*Gate, string, error) {
	raw := make([]byte, codeBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, "", err
	}
	code := codeEncoding.EncodeToString(raw)
	return &Gate{codeHash: hashCode(code), onExhausted: onExhausted, sleep: time.Sleep}, code, nil
}

// hashCode normalizes what an operator might type (case, spaces, dashes).
func hashCode(code string) [32]byte {
	c := strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(code)))
	return sha256.Sum256([]byte(c))
}

// Login exchanges the code for the session value (returned once) and
// reports the wrong codes so far. A wrong code is delayed; the
// MaxFailures-th failure ends setup. Once the code was used, every
// attempt is answered ErrSessionTaken at once: it neither counts nor
// waits, so nobody reaching the port can stop a running setup by
// exhausting the attempts.
func (g *Gate) Login(code string) (string, int, error) {
	h := hashCode(code)
	g.mu.Lock()
	if g.used {
		n := g.failures
		g.mu.Unlock()
		return "", n, ErrSessionTaken
	}
	if g.failures >= MaxFailures {
		n := g.failures
		g.mu.Unlock()
		return "", n, ErrExhausted
	}
	if subtle.ConstantTimeCompare(h[:], g.codeHash[:]) != 1 {
		g.failures++
		n := g.failures
		exhausted := n >= MaxFailures
		g.mu.Unlock()
		g.sleep(min(time.Duration(n)*failureDelay, maxDelay))
		if exhausted {
			if g.onExhausted != nil {
				g.onExhausted()
			}
			return "", n, ErrExhausted
		}
		return "", n, ErrBadCode
	}
	raw := make([]byte, sessionBytes)
	if _, err := rand.Read(raw); err != nil {
		n := g.failures
		g.mu.Unlock()
		return "", n, err
	}
	session := base64.RawURLEncoding.EncodeToString(raw)
	g.sessionHash = sha256.Sum256([]byte(session))
	g.used = true
	n := g.failures
	g.mu.Unlock()
	return session, n, nil
}

// Valid reports whether session is the gate's session.
func (g *Gate) Valid(session string) bool {
	if session == "" {
		return false
	}
	h := sha256.Sum256([]byte(session))
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.used && subtle.ConstantTimeCompare(h[:], g.sessionHash[:]) == 1
}
