package s3store

import (
	"io"
	"testing"
)

// mustCopy copies r to w and fails the test on error.
func mustCopy(t testing.TB, w io.Writer, r io.Reader) {
	t.Helper()
	if _, err := io.Copy(w, r); err != nil {
		t.Fatalf("copy: %v", err)
	}
}

// feed writes r to pw from a goroutine; errors (the reader went away) end
// the feed.
func feed(pw *io.PipeWriter, r io.Reader) bool {
	_, err := io.Copy(pw, r)
	return err == nil
}

// closeLog closes c and logs a failure.
func closeLog(t testing.TB, c io.Closer) {
	t.Helper()
	if err := c.Close(); err != nil {
		t.Logf("close: %v", err)
	}
}

// stopPipe ends a pipe from the reading side.
func stopPipe(t testing.TB, pr *io.PipeReader) {
	t.Helper()
	if err := pr.CloseWithError(io.ErrClosedPipe); err != nil {
		t.Logf("close pipe: %v", err)
	}
}
