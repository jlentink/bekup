package runstate

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// The reader rejects bad pages and scopes before touching the database.
func TestReaderValidation(t *testing.T) {
	r := NewReader(nil)
	ctx := context.Background()
	owner := Scope{Owner: uuid.New()}
	if _, err := r.ListRuns(ctx, Scope{}, RunFilter{}, nil, 10); !errors.Is(err, ErrBadPage) {
		t.Fatalf("empty scope: %v", err)
	}
	for _, n := range []int{0, MaxPage + 1} {
		if _, err := r.ListRuns(ctx, owner, RunFilter{}, nil, n); !errors.Is(err, ErrBadPage) {
			t.Errorf("runs limit %d: %v", n, err)
		}
		if _, err := r.ListArtifacts(ctx, owner, ArtifactFilter{}, nil, n); !errors.Is(err, ErrBadPage) {
			t.Errorf("artifacts limit %d: %v", n, err)
		}
	}
	if _, _, err := r.Logs(ctx, uuid.New(), -1, 10); !errors.Is(err, ErrBadPage) {
		t.Fatalf("negative after: %v", err)
	}
	if _, _, err := r.Logs(ctx, uuid.New(), 0, MaxLogPage+1); !errors.Is(err, ErrBadPage) {
		t.Fatalf("log limit: %v", err)
	}
	if _, err := r.RecentRunsForJobs(ctx, []uuid.UUID{uuid.New()}, MaxRecentPerJob+1); !errors.Is(err, ErrBadPage) {
		t.Fatalf("per job: %v", err)
	}
	big := make([]uuid.UUID, MaxBatch+1)
	if _, err := r.Items(ctx, big); !errors.Is(err, ErrBadPage) {
		t.Fatalf("items batch: %v", err)
	}
	if _, err := r.Artifacts(ctx, big); !errors.Is(err, ErrBadPage) {
		t.Fatalf("artifacts batch: %v", err)
	}
	// Empty batches need no query either.
	if m, err := r.Items(ctx, nil); err != nil || len(m) != 0 {
		t.Fatalf("empty items: %v %v", m, err)
	}
}
