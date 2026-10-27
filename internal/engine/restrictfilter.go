package engine

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

// maxRestrictLine bounds the line a StripRestrictLines filter inspects:
// pg_dumpall writes "\restrict <64 alphanumerics>".
const maxRestrictLine = 512

// StripRestrictLines returns a reader that removes the "\restrict <key>"
// and "\unrestrict <key>" lines pg_dumpall (13.22+, ...) writes into a
// globals script. The runner runs psql under its own random restrict key
// (PSQLRestrictPrefix, D-138): a second \restrict or an \unrestrict with
// the dump's key would fail the restore with ON_ERROR_STOP. Only complete
// lines that consist of exactly such a command and an alphanumeric key
// are removed; every other line, including a malformed \restrict, passes
// unchanged (psql refuses it while restricted). Removing a line can never
// lift the runner's restriction, whose key the script cannot know.
func StripRestrictLines(r io.Reader) io.Reader {
	return &restrictFilter{br: bufio.NewReaderSize(r, 64<<10), atStart: true}
}

type restrictFilter struct {
	br      *bufio.Reader
	atStart bool // the next byte starts a line
	buf     []byte
	out     []byte // bytes ready for the caller (a window of buf)
	err     error
}

func (f *restrictFilter) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(f.out) == 0 {
		if f.err != nil {
			return 0, f.err
		}
		if f.atStart && f.skipRestrictLine() {
			continue
		}
		f.atStart = false
		chunk, err := f.br.ReadSlice('\n')
		f.buf = append(f.buf[:0], chunk...)
		f.out = f.buf
		switch {
		case err == nil:
			f.atStart = true
		case errors.Is(err, bufio.ErrBufferFull):
			// the line continues
		default:
			f.err = err
		}
	}
	n := copy(p, f.out)
	f.out = f.out[n:]
	return n, nil
}

// skipRestrictLine discards the line at the reader's position when it is
// a complete \restrict / \unrestrict line.
func (f *restrictFilter) skipRestrictLine() bool {
	head, err := f.br.Peek(maxRestrictLine)
	if err != nil && len(head) == 0 {
		return false // ReadSlice reports the error
	}
	if !bytes.HasPrefix(head, []byte(`\restrict `)) && !bytes.HasPrefix(head, []byte(`\unrestrict `)) {
		return false
	}
	i := bytes.IndexByte(head, '\n')
	if i < 0 || !isRestrictLine(head[:i]) {
		return false
	}
	if _, err := f.br.Discard(i + 1); err != nil { // cannot fail: Peek returned these bytes
		f.err = err
	}
	return true
}

// isRestrictLine matches `\restrict KEY` or `\unrestrict KEY` (optional
// trailing CR) with KEY of 1-256 ASCII letters and digits.
func isRestrictLine(l []byte) bool {
	l = bytes.TrimSuffix(l, []byte{'\r'})
	var key []byte
	switch {
	case bytes.HasPrefix(l, []byte(`\restrict `)):
		key = l[len(`\restrict `):]
	case bytes.HasPrefix(l, []byte(`\unrestrict `)):
		key = l[len(`\unrestrict `):]
	default:
		return false
	}
	if len(key) == 0 || len(key) > 256 {
		return false
	}
	for _, c := range key {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}
