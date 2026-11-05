package destinations

import (
	"encoding/json"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/serviceutil"
	"github.com/jlentink/bekup/internal/storage"
)

// spaceService is a Service without a database: spaceResult never touches
// it (the DB parts of Space are covered by the integration tests).
func spaceService(t *testing.T, diskRoot string, allow func(string) (bool, time.Duration), now *atomic.Pointer[time.Time]) *Service {
	t.Helper()
	lim, err := serviceutil.NewLimiter(2, allow)
	if err != nil {
		t.Fatal(err)
	}
	return &Service{
		o: Options{
			DiskRoot: diskRoot, SpaceTimeout: 5 * time.Second, SpaceCacheTTL: DefaultSpaceCacheTTL,
			Logger: slog.New(slog.DiscardHandler),
			Now:    func() time.Time { return *now.Load() },
		},
		limiter: lim,
	}
}

func spaceRecord(t *testing.T, typ sqlc.DestinationType, cfg any, updated time.Time) *record {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, err := decode(sqlc.Destination{ID: uuid.New(), Name: "d", Type: typ, Config: raw, UpdatedAt: updated})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func clock(t time.Time) *atomic.Pointer[time.Time] {
	var p atomic.Pointer[time.Time]
	p.Store(&t)
	return &p
}

func allowAll(string) (bool, time.Duration) { return true, 0 }

func TestSpaceDiskAndCache(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	now := clock(start)
	var calls atomic.Int32
	s := spaceService(t, t.TempDir(), func(string) (bool, time.Duration) { calls.Add(1); return true, 0 }, now)
	r := spaceRecord(t, sqlc.DestinationTypeDisk, DiskConfig{Path: "backups"}, start)

	res, err := s.spaceResult(t.Context(), Actor{}, r)
	if err != nil {
		t.Fatal(err)
	}
	if res.status != SpaceAvailable || !res.space.FreeKnown || !res.space.TotalKnown || res.reason != "" || !res.checkedAt.Equal(start) {
		t.Fatalf("first result %+v", res)
	}
	// Cached: no second backend call (no limiter token taken).
	later := start.Add(time.Minute)
	now.Store(&later)
	if res2, err := s.spaceResult(t.Context(), Actor{}, r); err != nil || !res2.checkedAt.Equal(start) {
		t.Fatalf("cached result %+v, %v", res2, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("limiter consulted %d times, want 1", calls.Load())
	}
	// A settings change invalidates the cache.
	r2 := spaceRecord(t, sqlc.DestinationTypeDisk, DiskConfig{Path: "backups"}, later)
	r2.row.ID = r.row.ID
	if res3, err := s.spaceResult(t.Context(), Actor{}, r2); err != nil || !res3.checkedAt.Equal(later) {
		t.Fatalf("after a settings change %+v, %v", res3, err)
	}
	// Expiry asks again.
	expired := later.Add(DefaultSpaceCacheTTL)
	now.Store(&expired)
	if res4, err := s.spaceResult(t.Context(), Actor{}, r2); err != nil || !res4.checkedAt.Equal(expired) {
		t.Fatalf("after expiry %+v, %v", res4, err)
	}
	if calls.Load() != 3 {
		t.Fatalf("limiter consulted %d times, want 3", calls.Load())
	}
}

func TestSpaceWithoutNetworkCall(t *testing.T) {
	t.Parallel()
	start := time.Now()
	deny := func(string) (bool, time.Duration) {
		t.Error("the limiter must not be consulted")
		return false, time.Minute
	}
	s := spaceService(t, t.TempDir(), deny, clock(start))
	tests := []struct {
		name   string
		r      *record
		status SpaceStatus
		reason string
	}{
		{"s3 never reports", spaceRecord(t, sqlc.DestinationTypeS3, S3Config{Bucket: "b", Region: "eu-west-1"}, start),
			SpaceUnsupported, spaceReasonS3},
		{"sftp not ready", spaceRecord(t, sqlc.DestinationTypeSftp, SFTPConfig{Host: "h", Port: 22, User: "u", BasePath: "/b"}, start),
			SpaceUnavailable, spaceReasonNotReady + "confirm the server's host key and set a password or SSH key"},
		{"webdav not ready", spaceRecord(t, sqlc.DestinationTypeWebdav, WebDAVConfig{URL: "https://h/dav", Username: "u"}, start),
			SpaceUnavailable, spaceReasonNotReady + "set the password"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := s.spaceResult(t.Context(), Actor{}, tt.r)
			if err != nil || res.status != tt.status || res.reason != tt.reason || !res.checkedAt.IsZero() {
				t.Fatalf("got %+v, %v; want %s %q", res, err, tt.status, tt.reason)
			}
		})
	}
}

func TestSpaceRateLimitedIsNotCached(t *testing.T) {
	t.Parallel()
	start := time.Now()
	var allow atomic.Bool
	s := spaceService(t, t.TempDir(), func(string) (bool, time.Duration) { return allow.Load(), time.Minute }, clock(start))
	r := spaceRecord(t, sqlc.DestinationTypeDisk, DiskConfig{Path: "x"}, start)
	res, err := s.spaceResult(t.Context(), Actor{}, r)
	if err != nil || res.status != SpaceUnavailable || res.reason != spaceReasonBusy {
		t.Fatalf("rate limited: %+v, %v", res, err)
	}
	allow.Store(true)
	if res, err := s.spaceResult(t.Context(), Actor{}, r); err != nil || res.status != SpaceAvailable {
		t.Fatalf("after the limit: %+v, %v", res, err)
	}
}

func TestSpaceDiskRootMissing(t *testing.T) {
	t.Parallel()
	start := time.Now()
	s := spaceService(t, "", allowAll, clock(start))
	r := spaceRecord(t, sqlc.DestinationTypeDisk, DiskConfig{Path: "x"}, start)
	res, err := s.spaceResult(t.Context(), Actor{}, r)
	if err != nil || res.status != SpaceUnavailable || !strings.HasPrefix(res.reason, spaceReasonNotReady) {
		t.Fatalf("got %+v, %v", res, err)
	}
}

func TestLowSpace(t *testing.T) {
	t.Parallel()
	known := func(total, free int64) storage.Space {
		return storage.Space{Total: total, Free: free, TotalKnown: true, FreeKnown: true}
	}
	tests := []struct {
		name   string
		sp     storage.Space
		newest int64
		want   bool
	}{
		{"plenty", known(1000, 500), 100, false},
		{"below a tenth", known(1000, 99), 0, true},
		{"exactly a tenth", known(1000, 100), 0, false},
		{"newest set does not fit", known(1000, 400), 500, true},
		{"free only, set fits", storage.Space{Free: 600, FreeKnown: true}, 500, false},
		{"free only, set does not fit", storage.Space{Free: 400, FreeKnown: true}, 500, true},
		{"free unknown", storage.Space{Used: 5, UsedKnown: true}, 500, false},
		{"no backups yet", known(1000, 200), 0, false},
	}
	for _, tt := range tests {
		if got := lowSpace(tt.sp, tt.newest); got != tt.want {
			t.Errorf("%s: lowSpace = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestSpaceCacheBounded(t *testing.T) {
	t.Parallel()
	var c spaceCache
	now := time.Now()
	for range maxSpaceCacheEntries + 10 {
		c.put(uuid.New(), spaceEntry{expires: now.Add(time.Hour)}, now)
	}
	if n := len(c.entries); n > maxSpaceCacheEntries {
		t.Fatalf("%d entries, want at most %d", n, maxSpaceCacheEntries)
	}
}
