package restore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/artifactverify"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/runstate"
	"github.com/jlentink/bekup/internal/storage"
)

func TestErrorCode(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{nil, ""},
		{&VerificationError{Code: VerifyStoredDigest}, CodeVerification},
		{fmt.Errorf("x: %w", manifestFailure(pipeline.ErrReplay)), CodeVerification},
		{ErrUnavailable, CodeArtifactUnavailable},
		{ErrNotFound, CodeArtifactUnavailable},
		{ErrKeyUnavailable, CodeKeyUnavailable},
		{ErrIdentityRequired, CodeIdentityRequired},
		{ErrIdentityExpired, CodeIdentityExpired},
		{ErrIdentityMismatch, CodeIdentityMismatch},
		{fmt.Errorf("%w: %w", ErrInsufficientSpace, storage.ErrInsufficientSpace), CodeInsufficientSpace},
		{runstate.ErrHoldLost, CodeHoldLost},
		{fmt.Errorf("%w: boom", ErrDestination), CodeDestination},
		{fmt.Errorf("%w: boom", artifactverify.ErrDestination), CodeDestination},
		{context.DeadlineExceeded, CodeTimeout},
		{context.Canceled, CodeCanceled},
		{errors.New("postgres://user:pw@host failed"), CodeInternal},
	}
	for _, c := range cases {
		if got := ErrorCode(c.err); got != c.want {
			t.Errorf("ErrorCode(%v) = %q, want %q", c.err, got, c.want)
		}
		if c.want != "" && ErrorMessage(c.want) == "" {
			t.Errorf("no message for %q", c.want)
		}
	}
	if ErrorMessage("nope") != ErrorMessage(CodeInternal) {
		t.Error("unknown codes must map to the internal message")
	}
}

func TestManifestFailure(t *testing.T) {
	cases := map[error]string{
		pipeline.ErrReplay:                                                                 VerifyManifestReplayed,
		&pipeline.BindingError{Field: "entries"}:                                           VerifyManifestBinding,
		&pipeline.BindingError{Field: "run_id", Replay: true}:                              VerifyManifestReplayed,
		pipeline.ErrManifestBadSignature:                                                   VerifyManifestSignature,
		pipeline.ErrManifestUnknownKey:                                                     VerifyManifestUnknownKey,
		pipeline.ErrManifestMalformed:                                                      VerifyManifestMalformed,
		errors.New("manifest digest mismatch"):                                             VerifyManifestInvalid,
		fmt.Errorf("%w: x", artifactverify.ErrManifest):                                    VerifyManifestInvalid,
		fmt.Errorf("%w: %w", artifactverify.ErrManifest, pipeline.ErrManifestBadSignature): VerifyManifestSignature,
	}
	for err, want := range cases {
		ve := manifestFailure(err)
		if ve.Code != want {
			t.Errorf("%v: code %q, want %q", err, ve.Code, want)
		}
		if !errors.Is(ve, ErrVerification) || !errors.Is(ve, err) {
			t.Errorf("%v: does not match ErrVerification and its cause", err)
		}
	}
}

func TestStagingOverhead(t *testing.T) {
	for _, n := range []int64{0, 1, 64 << 10, 64<<10 + 1, 10 << 30} {
		chunks := n/(64<<10) + 1
		if got := stagingOverhead(n); got < uint64(chunks*16+200) { //nolint:gosec // G115: small positive test values
			t.Errorf("overhead(%d) = %d is below the age framing", n, got)
		}
	}
}

func testService(t *testing.T, dir string) *Service {
	t.Helper()
	return &Service{o: Options{ScratchDir: dir, StaleAfter: DefaultStaleAfter, Logger: slog.New(slog.DiscardHandler)}, live: map[uuid.UUID]struct{}{}}
}

func TestNewValidates(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("New without dependencies")
	}
}

func TestCheckScratch(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "scratch")
	s := testService(t, dir)
	if err := s.checkScratch(); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dir)
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("created %v %v", fi, err)
	}
	if err := os.Chmod(dir, 0o770); err != nil { //nolint:gosec // G302: the insecure mode under test
		t.Fatal(err)
	}
	if err := s.checkScratch(); err == nil {
		t.Fatal("a group-writable scratch directory was accepted")
	}
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := testService(t, file).checkScratch(); err == nil {
		t.Fatal("a file was accepted as scratch directory")
	}
}

func TestSweepMissingScratch(t *testing.T) {
	s := testService(t, filepath.Join(t.TempDir(), "missing"))
	if n, err := s.SweepScratch(t.Context()); n != 0 || err != nil {
		t.Fatalf("sweep of a missing directory: %d %v", n, err)
	}
	if err := s.removeDir(uuid.New()); err != nil {
		t.Fatal(err)
	}
}
