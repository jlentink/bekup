package destinations

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/storage"
)

// Space reporting (D-225): the total and free space behind a destination,
// where its backend can report it (disk: statfs; SFTP:
// statvfs@openssh.com; WebDAV: RFC 4331 quota; S3: never). Asked on
// demand (the destination page) or by the hourly space check (D-227,
// spacecheck.go), through Open (SSRF guard, pins), bounded by
// Options.SpaceTimeout, shared by concurrent callers and cached per
// destination; results carry fixed reasons and codes only, never server
// text. Every answer of a backend is stored (recordSpace) for the readers
// that never ask a backend (the periodic summary, the storage page).

// SpaceStatus is the outcome of a space report.
type SpaceStatus string

// Space report outcomes.
const (
	// SpaceAvailable: the backend reported at least one value.
	SpaceAvailable SpaceStatus = "available"
	// SpaceUnsupported: the backend type or this server reports no space.
	SpaceUnsupported SpaceStatus = "unsupported"
	// SpaceUnavailable: the space could not be asked now (not ready,
	// unreachable, rate limited, ...).
	SpaceUnavailable SpaceStatus = "unavailable"
)

// Default space report limits.
const (
	DefaultSpaceTimeout     = 15 * time.Second
	DefaultSpaceCacheTTL    = 5 * time.Minute
	DefaultSpaceFailureTTL  = time.Minute
	maxSpaceCacheEntries    = 1024
	lowSpaceFractionDivisor = 10 // free < total/10 is low
)

// Fixed reasons of space reports (shown to staff as they are).
const (
	spaceReasonS3        = "S3 does not report free space: a bucket has no fixed size."
	spaceReasonDisk      = "This platform cannot report the disk's free space."
	spaceReasonSFTP      = "The SFTP server does not report free space (it lacks the statvfs@openssh.com extension)."
	spaceReasonWebDAV    = "The WebDAV server reports no quota (RFC 4331 quota properties)."
	spaceReasonBusy      = "Too many space checks right now; try again in a minute."
	spaceReasonNotReady  = "The destination is not ready: "
	spaceReasonUnchecked = "The space could not be checked."
)

// SpaceReport is what Space returns.
type SpaceReport struct {
	DestinationID uuid.UUID
	Status        SpaceStatus
	// Reason is a fixed explanation when Status is not SpaceAvailable.
	Reason string
	// Space holds the reported values (SpaceAvailable only).
	Space storage.Space
	// NewestSetBytes is the size of the newest full set of backups on the
	// destination (per job, its newest run's present files there).
	NewestSetBytes int64
	// Low: the free space is below a tenth of the total, or below
	// NewestSetBytes (the next set of backups may not fit).
	Low bool
	// CheckedAt is when the backend was asked (zero: not asked).
	CheckedAt time.Time
}

// spaceResult is the cached part of a report (the backend's answer).
type spaceResult struct {
	status SpaceStatus
	reason string
	// code is the stable code of reason (stored, D-227): SpaceCodeUnsupported,
	// SpaceCodeNotReady, SpaceCodeFailed or a storage.ErrorCode; empty
	// when available.
	code      string
	space     storage.Space
	checkedAt time.Time
}

type spaceEntry struct {
	res        spaceResult
	settingsAt time.Time // the row's updated_at when asked
	expires    time.Time
}

type spaceCall struct {
	done chan struct{}
	res  spaceResult
	err  error
}

// spaceCache caches space answers per destination and shares one backend
// call between concurrent callers.
type spaceCache struct {
	mu       sync.Mutex
	entries  map[uuid.UUID]spaceEntry
	inflight map[uuid.UUID]*spaceCall
}

func (c *spaceCache) get(id uuid.UUID, settingsAt, now time.Time) (spaceResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[id]
	if !ok || !e.settingsAt.Equal(settingsAt) || !now.Before(e.expires) {
		return spaceResult{}, false
	}
	return e.res, true
}

func (c *spaceCache) put(id uuid.UUID, e spaceEntry, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[uuid.UUID]spaceEntry{}
	}
	if _, ok := c.entries[id]; !ok && len(c.entries) >= maxSpaceCacheEntries {
		for k := range c.entries {
			if !now.Before(c.entries[k].expires) {
				delete(c.entries, k)
			}
		}
		for k := range c.entries {
			if len(c.entries) < maxSpaceCacheEntries {
				break
			}
			delete(c.entries, k)
		}
	}
	c.entries[id] = e
}

// Space reports the space behind a destination that is not deleted
// (staff; authorized by the caller). Network calls are rate limited per
// caller like tests; a cached answer (Options.SpaceCacheTTL, failures
// DefaultSpaceFailureTTL; a settings change invalidates it) costs none.
// Only an audit failure after a pin mismatch is returned as an error;
// everything else becomes a report with a fixed reason.
func (s *Service) Space(ctx context.Context, actor Actor, id uuid.UUID) (SpaceReport, error) {
	q := sqlc.New(s.o.DB)
	r, err := s.load(ctx, q, id, false)
	if err != nil {
		return SpaceReport{}, err
	}
	newest, err := q.DestinationNewestSetBytes(ctx, id)
	if err != nil {
		return SpaceReport{}, fmt.Errorf("destinations: newest backup set: %w", err)
	}
	res, err := s.spaceResult(ctx, actor, r)
	if err != nil {
		return SpaceReport{}, err
	}
	rep := SpaceReport{
		DestinationID: id, Status: res.status, Reason: res.reason, Space: res.space,
		NewestSetBytes: newest, CheckedAt: res.checkedAt,
	}
	rep.Low = LowSpace(res.space, newest)
	return rep, nil
}

// LowCause is why LowSpace reports low space.
type LowCause int

// Low-space causes.
const (
	// LowNone: not low (or the free space is unknown).
	LowNone LowCause = iota
	// LowFraction: less than a tenth of the total is free.
	LowFraction
	// LowBelowNewestSet: less is free than the newest full set of backups.
	LowBelowNewestSet
)

// LowSpace is the low-space rule of D-225 (also the summary's, D-226):
// the free space is known and below a tenth of the total (when known) or
// below the newest full set of backups.
func LowSpace(sp storage.Space, newestSet int64) bool { return LowSpaceCause(sp, newestSet) != LowNone }

// LowSpaceCause applies LowSpace and tells which part of the rule holds.
func LowSpaceCause(sp storage.Space, newestSet int64) LowCause {
	switch {
	case !sp.FreeKnown:
		return LowNone
	case sp.TotalKnown && sp.Total > 0 && sp.Free < sp.Total/lowSpaceFractionDivisor:
		return LowFraction
	case newestSet > 0 && sp.Free < newestSet:
		return LowBelowNewestSet
	default:
		return LowNone
	}
}

// spaceResult returns the backend's answer for r: without a network call
// for S3 and destinations that are not ready, else from the cache or one
// shared call.
func (s *Service) spaceResult(ctx context.Context, actor Actor, r *record) (spaceResult, error) {
	v := r.view()
	if v.Type == TypeS3 {
		return spaceResult{status: SpaceUnsupported, reason: spaceReasonS3, code: SpaceCodeUnsupported}, nil
	}
	if !v.Ready {
		return spaceResult{status: SpaceUnavailable, reason: spaceReasonNotReady + v.NotReadyReason, code: SpaceCodeNotReady}, nil
	}
	id, settingsAt := r.row.ID, r.row.UpdatedAt
	if res, ok := s.space.get(id, settingsAt, s.now()); ok {
		return res, nil
	}
	s.space.mu.Lock()
	if s.space.inflight == nil {
		s.space.inflight = map[uuid.UUID]*spaceCall{}
	}
	if call, ok := s.space.inflight[id]; ok {
		s.space.mu.Unlock()
		select {
		case <-call.done:
			return call.res, call.err
		case <-ctx.Done():
			return spaceResult{}, ctx.Err()
		}
	}
	call := &spaceCall{done: make(chan struct{})}
	s.space.inflight[id] = call
	s.space.mu.Unlock()

	call.res, call.err = s.askSpace(ctx, actor, r)
	s.space.mu.Lock()
	delete(s.space.inflight, id)
	s.space.mu.Unlock()
	close(call.done)
	return call.res, call.err
}

// askSpace opens the backend and asks it (rate limited, bounded by
// SpaceTimeout; the call is detached from the caller's cancellation so
// that callers sharing it are not cut off), caches and stores the answer
// (a storing failure is logged: the caller still gets the answer).
func (s *Service) askSpace(ctx context.Context, actor Actor, r *record) (spaceResult, error) {
	release, err := s.acquire(actor)
	if err != nil {
		var rl *RateLimitedError
		if errors.As(err, &rl) || errors.Is(err, ErrBusy) {
			return spaceResult{status: SpaceUnavailable, reason: spaceReasonBusy}, nil
		}
		return spaceResult{}, err
	}
	defer release()
	res, err := s.askBackend(ctx, actor, r, "")
	if err != nil {
		return spaceResult{}, err
	}
	if s.storeSpace != nil {
		if _, err := s.storeSpace(ctx, r, res); err != nil {
			s.o.Logger.WarnContext(ctx, "storing the destination space check failed",
				slog.String("destination_id", r.row.ID.String()), slog.Any("error", err))
		}
	}
	return res, nil
}

// askBackend asks the backend for its space (bounded by SpaceTimeout,
// detached from the caller's cancellation), audits a pin violation (fail
// closed: the answer is then neither cached nor stored) and caches the
// answer. audited is the mismatch code already stored (and so audited)
// for these settings: the same violation found again is not audited again
// (the scheduled check; amendment 1 of D-227), the answer is still stored.
// The caller has taken care of the limits.
func (s *Service) askBackend(ctx context.Context, actor Actor, r *record, audited string) (spaceResult, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.o.SpaceTimeout)
	defer cancel()
	res, mismatch := s.querySpace(ctx, r)
	if mismatch && (audited == "" || res.code != audited) {
		if err := s.pinMismatch(ctx, actor, r); err != nil {
			return spaceResult{}, err
		}
	}
	ttl := s.o.SpaceCacheTTL
	if res.status == SpaceUnavailable {
		ttl = min(ttl, DefaultSpaceFailureTTL)
	}
	now := s.now()
	res.checkedAt = now
	s.space.put(r.row.ID, spaceEntry{res: res, settingsAt: r.row.UpdatedAt, expires: now.Add(ttl)}, now)
	return res, nil
}

// querySpace opens the backend and asks for its space; mismatch reports
// an SFTP host-key or WebDAV certificate-pin violation.
func (s *Service) querySpace(ctx context.Context, r *record) (res spaceResult, mismatch bool) {
	id := r.row.ID
	v := r.view()
	d, err := s.open(ctx, r)
	if err == nil {
		defer func() {
			if cerr := d.Close(); cerr != nil {
				s.o.Logger.WarnContext(ctx, "closing destination after space check failed",
					slog.String("destination_id", id.String()), slog.String("code", string(storage.Classify(cerr))))
			}
		}()
		rep, ok := d.(storage.SpaceReporter)
		if !ok {
			return unsupportedSpace(v.Type), false
		}
		var sp storage.Space
		if sp, err = rep.Space(ctx); err == nil {
			if !sp.Known() {
				return unsupportedSpace(v.Type), false
			}
			return spaceResult{status: SpaceAvailable, space: sp}, false
		}
	}
	var nr *NotReadyError
	if errors.As(err, &nr) {
		return spaceResult{status: SpaceUnavailable, reason: spaceReasonNotReady + nr.Reason, code: SpaceCodeNotReady}, false
	}
	if errors.Is(err, storage.ErrUnsupported) {
		return unsupportedSpace(v.Type), false
	}
	code := storage.Classify(err)
	if errors.Is(netguard.Normalize(err), netguard.ErrNotAllowed) || errors.Is(netguard.Normalize(err), netguard.ErrUnreachable) {
		code = storage.CodeNetwork
	}
	// Only the normalized code: a backend error may carry server text.
	s.o.Logger.WarnContext(ctx, "destination space check failed",
		slog.String("destination_id", id.String()), slog.String("code", string(code)))
	reason, stored := code.Message(), string(code)
	if reason == "" || !storedSpaceCode.MatchString(stored) {
		reason, stored = spaceReasonUnchecked, SpaceCodeFailed
	}
	return spaceResult{status: SpaceUnavailable, reason: reason, code: stored}, isMismatchCode(string(code))
}

// isMismatchCode reports the stored codes of an SFTP host-key or WebDAV
// certificate-pin violation.
func isMismatchCode(code string) bool {
	return code == string(storage.CodeHostKeyMismatch) || code == string(storage.CodeTLSPinMismatch)
}

func unsupportedSpace(t Type) spaceResult {
	return spaceResult{status: SpaceUnsupported, reason: unsupportedSpaceReason(t), code: SpaceCodeUnsupported}
}

func unsupportedSpaceReason(t Type) string {
	switch t {
	case TypeSFTP:
		return spaceReasonSFTP
	case TypeWebDAV:
		return spaceReasonWebDAV
	case TypeDisk:
		return spaceReasonDisk
	default:
		return spaceReasonS3
	}
}
