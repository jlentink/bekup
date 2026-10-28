package offline

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/crypto"
)

// PassphraseExportHeader is the first line of a job passphrase export
// (internal/jobkeys Export.File, docs/guides/encryption.md).
const PassphraseExportHeader = "bekup-job-passphrases v1" //nolint:gosec // G101: a file format header, not a credential

// ErrPassphraseExport reports an unusable passphrase export.
var ErrPassphraseExport = errors.New("offline: invalid passphrase export")

// PassphraseExport is a parsed job passphrase export. Destroy it after
// use.
type PassphraseExport struct {
	JobID    uuid.UUID
	versions map[int]*crypto.Secret
}

// IsPassphraseExport reports whether data starts with the export header.
func IsPassphraseExport(data []byte) bool {
	line, _, _ := bytes.Cut(data, []byte("\n"))
	return string(bytes.TrimSpace(line)) == PassphraseExportHeader
}

func exportErr(line int, msg string) error {
	// Never echo the line: it may hold a passphrase.
	if line > 0 {
		return fmt.Errorf("%w: line %d: %s", ErrPassphraseExport, line, msg)
	}
	return fmt.Errorf("%w: %s", ErrPassphraseExport, msg)
}

// ParsePassphraseExport parses a job passphrase export. The passphrases
// are copied into secrets; the caller still zeroes data.
func ParsePassphraseExport(data []byte) (*PassphraseExport, error) {
	ex := &PassphraseExport{versions: map[int]*crypto.Secret{}}
	ok := false
	defer func() {
		if !ok {
			ex.Destroy()
		}
	}()
	lines := bytes.Split(data, []byte("\n"))
	pending := 0 // version waiting for its passphrase line
	seenHeader, seenJob := false, false
	for i, raw := range lines {
		n := i + 1
		line := bytes.TrimSuffix(raw, []byte("\r"))
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 || trimmed[0] == '#' {
			continue
		}
		if !seenHeader {
			if string(trimmed) != PassphraseExportHeader {
				return nil, exportErr(n, "missing header")
			}
			seenHeader = true
			continue
		}
		word, rest, _ := bytes.Cut(trimmed, []byte(" "))
		switch string(word) {
		case "job":
			id, err := uuid.Parse(string(bytes.TrimSpace(rest)))
			if err != nil || seenJob {
				return nil, exportErr(n, "invalid job line")
			}
			ex.JobID, seenJob = id, true
		case "exported":
		case "version":
			if pending != 0 {
				return nil, exportErr(n, "version without a passphrase line")
			}
			f := strings.Fields(string(rest))
			if len(f) == 0 {
				return nil, exportErr(n, "invalid version line")
			}
			v, err := strconv.Atoi(f[0])
			if err != nil || v < 1 || v > 1<<20 {
				return nil, exportErr(n, "invalid version number")
			}
			if _, dup := ex.versions[v]; dup {
				return nil, exportErr(n, "duplicate version")
			}
			pending = v
		case "passphrase":
			if pending == 0 {
				return nil, exportErr(n, "passphrase without a version line")
			}
			// The passphrase is the rest of the (untrimmed) line verbatim.
			_, p, _ := bytes.Cut(bytes.TrimLeft(line, " \t"), []byte(" "))
			if len(p) == 0 {
				return nil, exportErr(n, "empty passphrase")
			}
			ex.versions[pending] = crypto.NewSecret(bytes.Clone(p))
			pending = 0
		default:
			return nil, exportErr(n, "unknown line")
		}
	}
	switch {
	case !seenHeader:
		return nil, exportErr(0, "missing header")
	case !seenJob:
		return nil, exportErr(0, "missing job line")
	case pending != 0:
		return nil, exportErr(0, "last version has no passphrase")
	case len(ex.versions) == 0:
		return nil, exportErr(0, "no passphrases")
	}
	ok = true
	return ex, nil
}

// Versions returns the passphrase versions in the export, ascending.
func (e *PassphraseExport) Versions() []int {
	out := make([]int, 0, len(e.versions))
	for v := range e.versions {
		out = append(out, v)
	}
	slices.Sort(out)
	return out
}

// Passphrase returns the passphrase of a version (borrowed: valid until
// Destroy).
func (e *PassphraseExport) Passphrase(version int) (*crypto.Secret, bool) {
	s, ok := e.versions[version]
	return s, ok
}

// Destroy zeroes every passphrase.
func (e *PassphraseExport) Destroy() {
	if e == nil {
		return
	}
	for _, s := range e.versions {
		s.Destroy()
	}
}
