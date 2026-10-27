package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"
)

// Argon2Params are the argon2id cost parameters (D-047, D-073).
type Argon2Params struct {
	// MemoryKiB is the memory cost in KiB.
	MemoryKiB uint32
	// Iterations is the time cost.
	Iterations uint32
	// Parallelism is the number of lanes.
	Parallelism uint8
}

// MinArgon2Params are the plan's parameters; configuration can only raise
// them (security.argon2_*).
var MinArgon2Params = Argon2Params{MemoryKiB: 64 * 1024, Iterations: 3, Parallelism: 1}

// Fixed sizes of new hashes.
const (
	saltLen = 16
	keyLen  = 32
)

// Bounds accepted when parsing a stored hash. They cap the work a
// (corrupted or planted) stored hash can make Verify do.
const (
	maxParseMemoryKiB  = 4 * 1024 * 1024 // = security.argon2_memory_kib maximum
	maxParseIterations = 64
	minParseSaltLen    = 8
	maxParseSaltLen    = 64
	minParseKeyLen     = 16
	maxParseKeyLen     = 64
)

// MaxPasswordBytes bounds the input to argon2id. Longer inputs are refused
// before hashing (policy) or treated as a wrong password (login), so a
// client cannot make the server hash megabytes.
const MaxPasswordBytes = 4096

// DefaultHashWait is how long a hash operation waits for a free slot of
// the concurrency semaphore before failing with ErrBusy.
const DefaultHashWait = 2 * time.Second

// Errors.
var (
	// ErrBusy means every hashing slot was taken and the bounded wait
	// expired (or too many callers were already waiting). The caller
	// answers RATE_LIMITED; requests never queue without bound.
	ErrBusy = errors.New("auth: password hashing is busy")
	// ErrInvalidHash means a stored hash is not a supported argon2id PHC
	// string within the accepted bounds.
	ErrInvalidHash = errors.New("auth: invalid password hash")
)

// HasherOptions configure NewHasher.
type HasherOptions struct {
	// Params are the parameters of new hashes (at least MinArgon2Params).
	Params Argon2Params
	// MaxConcurrent bounds concurrent argon2id operations (memory:
	// MaxConcurrent × MemoryKiB). At least 1.
	MaxConcurrent int
	// Wait is the longest wait for a slot (0: DefaultHashWait).
	Wait time.Duration
	// MaxWaiters bounds the callers waiting for a slot (0: 4 ×
	// MaxConcurrent); callers beyond it fail with ErrBusy at once.
	MaxWaiters int
	// WeakForTests accepts Params below MinArgon2Params so that tests can
	// hash cheaply. It is honored only inside test binaries
	// (testing.Testing()); NewHasher fails if it is set anywhere else.
	WeakForTests bool
}

// Hasher hashes and verifies passwords with argon2id in the PHC string
// format ($argon2id$v=19$m=..,t=..,p=..$salt$hash, unpadded standard
// base64). A semaphore bounds concurrent operations. It is safe for
// concurrent use.
type Hasher struct {
	params     Argon2Params
	sem        chan struct{}
	wait       time.Duration
	maxWaiters int64
	waiters    atomic.Int64
	// dummy is a hash of a random password with the current parameters,
	// verified for unknown users so that their timing matches.
	dummy string
	// dummyCount counts DummyVerify calls (tests assert the unknown-user
	// path through it instead of wall-clock timing).
	dummyCount atomic.Uint64
}

// NewHasher returns a hasher. It computes the dummy hash once (one argon2id
// run).
func NewHasher(o HasherOptions) (*Hasher, error) {
	p := o.Params
	if o.WeakForTests && !testing.Testing() {
		return nil, errors.New("auth: WeakForTests outside a test binary")
	}
	minimum := MinArgon2Params
	if o.WeakForTests {
		minimum = Argon2Params{MemoryKiB: 8, Iterations: 1, Parallelism: 1}
	}
	if p.MemoryKiB < minimum.MemoryKiB || p.Iterations < minimum.Iterations || p.Parallelism < minimum.Parallelism {
		return nil, fmt.Errorf("auth: argon2id parameters below the minimum m=%d,t=%d,p=%d",
			MinArgon2Params.MemoryKiB, MinArgon2Params.Iterations, MinArgon2Params.Parallelism)
	}
	if p.MemoryKiB > maxParseMemoryKiB || p.Iterations > maxParseIterations {
		return nil, errors.New("auth: argon2id parameters above the supported maximum")
	}
	if o.MaxConcurrent < 1 {
		return nil, errors.New("auth: MaxConcurrent must be at least 1")
	}
	if o.Wait <= 0 {
		o.Wait = DefaultHashWait
	}
	if o.MaxWaiters <= 0 {
		o.MaxWaiters = 4 * o.MaxConcurrent
	}
	h := &Hasher{params: p, sem: make(chan struct{}, o.MaxConcurrent), wait: o.Wait, maxWaiters: int64(o.MaxWaiters)}
	var pw [32]byte
	_, _ = rand.Read(pw[:])
	h.dummy = h.encode(pw[:])
	return h, nil
}

// Params returns the parameters of new hashes.
func (h *Hasher) Params() Argon2Params { return h.params }

// acquire takes a semaphore slot within the bounded wait.
func (h *Hasher) acquire(ctx context.Context) (func(), error) {
	select {
	case h.sem <- struct{}{}:
		return func() { <-h.sem }, nil
	default:
	}
	if h.waiters.Add(1) > h.maxWaiters {
		h.waiters.Add(-1)
		return nil, ErrBusy
	}
	defer h.waiters.Add(-1)
	t := time.NewTimer(h.wait)
	defer t.Stop()
	select {
	case h.sem <- struct{}{}:
		return func() { <-h.sem }, nil
	case <-t.C:
		return nil, ErrBusy
	case <-ctx.Done():
		return nil, fmt.Errorf("auth: waiting for a hashing slot: %w", ctx.Err())
	}
}

// Hash returns the PHC string of password with the current parameters.
func (h *Hasher) Hash(ctx context.Context, password string) (string, error) {
	if len(password) > MaxPasswordBytes {
		return "", errors.New("auth: password too long to hash")
	}
	release, err := h.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	return h.encode([]byte(password)), nil
}

func (h *Hasher) encode(password []byte) string {
	salt := make([]byte, saltLen)
	_, _ = rand.Read(salt)
	key := argon2.IDKey(password, salt, h.params.Iterations, h.params.MemoryKiB, h.params.Parallelism, keyLen)
	return formatPHC(h.params, salt, key)
}

func formatPHC(p Argon2Params, salt, key []byte) string {
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.MemoryKiB, p.Iterations, p.Parallelism, enc.EncodeToString(salt), enc.EncodeToString(key))
}

// phc is a parsed argon2id hash.
type phc struct {
	params Argon2Params
	salt   []byte
	key    []byte
}

// parsePHC parses an argon2id PHC string strictly: version 19, the
// parameters in the order m,t,p, unpadded standard base64 and every value
// within the parse bounds.
func parsePHC(s string) (phc, error) {
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return phc{}, ErrInvalidHash
	}
	var out phc
	kv := strings.Split(parts[3], ",")
	if len(kv) != 3 {
		return phc{}, ErrInvalidHash
	}
	vals := make([]uint64, 3)
	for i, name := range []string{"m=", "t=", "p="} {
		v, ok := strings.CutPrefix(kv[i], name)
		if !ok || v == "" || (len(v) > 1 && v[0] == '0') {
			return phc{}, ErrInvalidHash
		}
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return phc{}, ErrInvalidHash
		}
		vals[i] = n
	}
	if vals[0] < 8 || vals[0] > maxParseMemoryKiB || vals[1] < 1 || vals[1] > maxParseIterations || vals[2] < 1 || vals[2] > 255 {
		return phc{}, ErrInvalidHash
	}
	out.params = Argon2Params{
		MemoryKiB:   uint32(vals[0]), //nolint:gosec // G115: bounded by maxParseMemoryKiB above
		Iterations:  uint32(vals[1]), //nolint:gosec // G115: bounded by maxParseIterations above
		Parallelism: uint8(vals[2]),  //nolint:gosec // G115: bounded to 1..255 above
	}
	var err error
	if out.salt, err = base64.RawStdEncoding.Strict().DecodeString(parts[4]); err != nil ||
		len(out.salt) < minParseSaltLen || len(out.salt) > maxParseSaltLen {
		return phc{}, ErrInvalidHash
	}
	if out.key, err = base64.RawStdEncoding.Strict().DecodeString(parts[5]); err != nil ||
		len(out.key) < minParseKeyLen || len(out.key) > maxParseKeyLen {
		return phc{}, ErrInvalidHash
	}
	return out, nil
}

// Verify checks password against the stored PHC hash in constant time. ok
// reports a match; rehash reports that the hash uses other parameters or
// sizes than new hashes would, so the caller should store a fresh Hash
// after a successful login. An unparsable hash is ErrInvalidHash (never a
// match); a full semaphore is ErrBusy.
func (h *Hasher) Verify(ctx context.Context, password, encoded string) (ok, rehash bool, err error) {
	parsed, err := parsePHC(encoded)
	if err != nil {
		return false, false, err
	}
	// An over-long password never matches, but is still hashed
	// (truncated) so that its timing does not depend on the account.
	tooLong := len(password) > MaxPasswordBytes
	if tooLong {
		password = password[:MaxPasswordBytes]
	}
	release, err := h.acquire(ctx)
	if err != nil {
		return false, false, err
	}
	defer release()
	p := parsed.params
	storedLen := uint32(len(parsed.key)) //nolint:gosec // G115: at most maxParseKeyLen (parsePHC)
	key := argon2.IDKey([]byte(password), parsed.salt, p.Iterations, p.MemoryKiB, p.Parallelism, storedLen)
	if subtle.ConstantTimeCompare(key, parsed.key) != 1 || tooLong {
		return false, false, nil
	}
	rehash = p != h.params || len(parsed.salt) != saltLen || len(parsed.key) != keyLen
	return true, rehash, nil
}

// DummyVerify runs a full verification against a hash of a random
// password with the current parameters and discards the result. Login
// calls it whenever no real hash is checked (unknown, disabled, locked or
// password-less accounts), so those paths take as long as a wrong
// password. ErrBusy is returned like Verify's.
func (h *Hasher) DummyVerify(ctx context.Context, password string) error {
	h.dummyCount.Add(1)
	_, _, err := h.Verify(ctx, password, h.dummy)
	return err
}

// DummyVerifications returns how often DummyVerify ran (tests).
func (h *Hasher) DummyVerifications() uint64 { return h.dummyCount.Load() }
