package restore

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"

	"github.com/jlentink/bekup/internal/artifactverify"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/pipeline"
)

// Cleanup runs on the context's AfterFunc goroutine while the reader's
// owner may be inside Read (M4 review): it must only mark the reader
// aborted, never close the zstd decoder under a running Read. Run with
// -race.
func TestStagedCleanupDuringRead(t *testing.T) {
	var raw bytes.Buffer
	r := rand.New(rand.NewPCG(1, 2))
	const letters = "abcdefghijklmnopqrstuvwxyz"
	for raw.Len() < 1<<20 {
		raw.WriteString("row ")
		raw.WriteByte(letters[r.IntN(len(letters))])
	}
	var comp bytes.Buffer
	zw, err := zstd.NewWriter(&comp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(raw.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	for range 20 {
		svc := &Service{o: Options{Logger: slog.New(slog.DiscardHandler), ScratchDir: t.TempDir()}, live: map[uuid.UUID]struct{}{}}
		st := &Staged{s: svc, restore: sqlc.Restore{ID: uuid.New()}, stopHold: func() {}, stopAfter: func() bool { return false }}
		stored := artifactverify.NewDigestReader(&repeatReader{b: comp.Bytes()}, math.MaxInt64)
		plain, err := pipeline.OpenArtifact(stored, pipeline.Decryption{None: true})
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(filepath.Join(t.TempDir(), "staged"))
		if err != nil {
			t.Fatal(err)
		}
		pr := &plainReader{st: st, ctx: context.Background(), f: f, plain: plain, stored: stored,
			raw: artifactverify.NewDigestReader(plain, math.MaxInt64)}
		st.reader = pr
		ctx, cancel := context.WithCancel(t.Context())
		st.stopAfter = context.AfterFunc(ctx, st.Cleanup)
		done := make(chan error, 1)
		first := make(chan struct{})
		go func() {
			buf := make([]byte, 4<<10)
			once := false
			for {
				_, err := pr.Read(buf)
				if !once {
					once = true
					close(first)
				}
				if err != nil {
					done <- err
					return
				}
			}
		}()
		<-first
		cancel()
		err = <-done
		if !errors.Is(err, ErrCleanedUp) {
			t.Fatalf("read error = %v", err)
		}
		if _, err := pr.Read(make([]byte, 1)); !errors.Is(err, ErrCleanedUp) {
			t.Fatalf("read after cleanup = %v", err)
		}
		// The owner closes the reader.
		if err := pr.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// repeatReader repeats b forever (concatenated zstd frames: the stream
// never ends, so the reader is always mid-stream at the cleanup).
type repeatReader struct {
	b   []byte
	off int
}

func (r *repeatReader) Read(p []byte) (int, error) {
	n := copy(p, r.b[r.off:])
	r.off = (r.off + n) % len(r.b)
	return n, nil
}
