package resolvers

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/runstate"
)

func TestTimeCursorRoundTrip(t *testing.T) {
	at := time.UnixMicro(1_790_000_000_123_456)
	id := uuid.New()
	c := encodeTimeCursor(runCursorPrefix, at, id)
	got, gotID, ok, err := decodeTimeCursor(runCursorPrefix, &c)
	if err != nil || !ok || !got.Equal(at) || gotID != id {
		t.Fatalf("round trip: %v %v %v %v", got, gotID, ok, err)
	}
	// A run cursor is not an artifact cursor.
	if _, _, _, err := decodeTimeCursor(artifactCursorPrefix, &c); err == nil {
		t.Fatal("prefix not checked")
	}
	if _, _, ok, err := decodeTimeCursor(runCursorPrefix, nil); ok || err != nil {
		t.Fatal("nil cursor")
	}
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	long := string(make([]byte, maxRunCursorLen+1))
	for _, bad := range []string{"%%%", enc("run:"), enc("run:x:" + id.String()), enc("run:1:nope"), enc("run:1"), long} {
		if _, _, _, err := decodeTimeCursor(runCursorPrefix, &bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func FuzzDecodeTimeCursor(f *testing.F) {
	f.Add(encodeTimeCursor(runCursorPrefix, time.Now(), uuid.New()))
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		at, id, ok, err := decodeTimeCursor(runCursorPrefix, &s)
		if err == nil && ok {
			if again := encodeTimeCursor(runCursorPrefix, at, id); again == "" {
				t.Fatal("empty re-encoding")
			}
		}
	})
}

func TestParseLogID(t *testing.T) {
	s := func(v string) *string { return &v }
	for in, want := range map[string]int64{"0": 0, "42": 42, "9223372036854775807": 1<<63 - 1} {
		if got, err := parseLogID(s(in)); err != nil || got != want {
			t.Errorf("%s: %d %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "-1", "1e3", "x", "99999999999999999999", " 1"} {
		if _, err := parseLogID(s(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if got, err := parseLogID(nil); got != 0 || err != nil {
		t.Fatal("nil")
	}
}

func TestWarningsModel(t *testing.T) {
	dest := uuid.New()
	ws := warningsModel([]byte(`[{"code":"c1","message":"m\u0000x","db":"app","destination_id":"` + dest.String() + `"},{"code":"c2","message":"m2","destination_id":"nope"}]`))
	if len(ws) != 2 || ws[0].Message != "m x" || ws[0].Database == nil || *ws[0].Database != "app" ||
		ws[0].DestinationID == nil || *ws[0].DestinationID != dest || ws[1].DestinationID != nil || ws[1].Database != nil {
		t.Fatalf("warnings: %+v", ws)
	}
	for _, bad := range []string{"", "{", `{"code":"x"}`} {
		if got := warningsModel([]byte(bad)); got == nil || len(got) != 0 {
			t.Errorf("%q: %+v", bad, got)
		}
	}
}

func TestPendingIDs(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	cache := map[uuid.UUID]int{b: 1}
	got := pending(a, []uuid.UUID{a, b, c, c}, cache)
	if len(got) != 2 || got[0] != a || got[1] != c {
		t.Fatalf("pending: %v", got)
	}
	many := make([]uuid.UUID, runstate.MaxBatch+10)
	for i := range many {
		many[i] = uuid.New()
	}
	if n := len(pending(a, many, map[uuid.UUID]int{})); n != runstate.MaxBatch {
		t.Fatalf("batch not capped: %d", n)
	}
}

func TestBatchLoadUsesHintsOnce(t *testing.T) {
	ctx := WithRunLoader(context.Background())
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	hintRuns(ctx, ids)
	calls := 0
	fetch := func(_ context.Context, in []uuid.UUID) (map[uuid.UUID][]int, error) {
		calls++
		out := map[uuid.UUID][]int{}
		for i, id := range in {
			out[id] = []int{i}
		}
		return out, nil
	}
	cache := map[uuid.UUID][]int{}
	l := loaderFrom(ctx)
	for _, id := range ids {
		if _, err := batchLoad(ctx, l, func(*runLoader) map[uuid.UUID][]int { return cache }, runHintsOf, id, fetch); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("%d fetches, want 1", calls)
	}
	// Without a loader every id loads alone; errors pass through.
	boom := errors.New("boom")
	_, err := batchLoad(context.Background(), nil, nil, nil, ids[0], func(context.Context, []uuid.UUID) (map[uuid.UUID][]int, error) {
		return nil, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("error: %v", err)
	}
}

func TestDurationOf(t *testing.T) {
	start := time.Now().Add(-time.Minute)
	end := start.Add(30 * time.Second)
	if d := durationOf(&start, &end, false); d == nil || *d != 30 {
		t.Fatalf("finished: %v", d)
	}
	if d := durationOf(&start, nil, true); d == nil || *d < 59 {
		t.Fatalf("running: %v", d)
	}
	if durationOf(nil, &end, false) != nil || durationOf(&start, nil, false) != nil {
		t.Fatal("no duration expected")
	}
}

func TestRunSubscriptionsOutsideStream(t *testing.T) {
	r := &Resolver{}
	if _, err := r.runUpdated(context.Background(), nil, nil); !errors.Is(err, errNotStream) {
		t.Fatalf("runUpdated: %v", err)
	}
	if _, err := r.runLogAppended(context.Background(), uuid.New(), nil); !errors.Is(err, errNotStream) {
		t.Fatalf("runLogAppended: %v", err)
	}
}
