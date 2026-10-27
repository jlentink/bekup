package preamble

import (
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/engine"
)

// Limits of the framing.
const (
	// MaxRecords is the number of distinct record types.
	MaxRecords = 5
	// MaxRecordBytes bounds one decoded record.
	MaxRecordBytes = 64 << 10
	// ExitCode is the wrapper's exit status for any preamble error.
	ExitCode = 97
	// Header is the first line of every preamble.
	Header = "BEKUP-PREAMBLE 1"
	// Trailer ends the record list.
	Trailer = "END"
)

// RecordType names a record; each maps to one fixed tmpfs file.
type RecordType string

// The record types.
const (
	RecordPGPass     RecordType = "pgpass"
	RecordMyCnf      RecordType = "mycnf"
	RecordCA         RecordType = "ca"
	RecordClientCert RecordType = "clientcert"
	RecordClientKey  RecordType = "clientkey"
)

// files maps every record type to its file. The same table is spelled out
// in Script; TestScriptMatchesFiles keeps them in sync.
var files = map[RecordType]engine.File{
	RecordPGPass:     engine.FilePGPass,
	RecordMyCnf:      engine.FileMyCnf,
	RecordCA:         engine.FileCA,
	RecordClientCert: engine.FileClientCert,
	RecordClientKey:  engine.FileClientKey,
}

// File returns the tmpfs file of a record type.
func (t RecordType) File() (engine.File, bool) {
	f, ok := files[t]
	return f, ok
}

// TypeForFile returns the record type that writes f.
func TypeForFile(f engine.File) (RecordType, bool) {
	for t, ff := range files {
		if ff == f {
			return t, true
		}
	}
	return "", false
}

// Script is the fixed wrapper, run as `/bin/sh -c Script -- <argv...>`. It
// is a reviewed constant: request data is never interpolated into it, and
// it only uses POSIX sh features available in busybox ash (alpine
// PostgreSQL images), dash (MariaDB) and bash (MySQL). printf is a shell
// builtin in all three, so decoded data only travels through a pipe, never
// through a child's argv (verified by the integration test).
const Script = `set -eu
umask 077
d=/tmp/bekup
fail() { printf 'bekup-preamble: %s\n' "$1" >&2; exit 97; }
mkdir -m 700 "$d" 2>/dev/null || fail 'cannot create the preamble directory'
IFS= read -r line || fail 'missing header'
[ "$line" = 'BEKUP-PREAMBLE 1' ] || fail 'bad header'
n=0
while :; do
  IFS= read -r line || fail 'truncated preamble'
  [ "$line" = 'END' ] && break
  n=$((n + 1))
  [ "$n" -le 5 ] || fail 'too many records'
  t=${line%% *}
  len=${line#* }
  [ "$t $len" = "$line" ] || fail 'bad record header'
  case $len in ''|*[!0-9]*|0?*) fail 'bad record length' ;; esac
  [ "${#len}" -le 5 ] || fail 'record too large'
  [ "$len" -le 65536 ] || fail 'record too large'
  case $t in
    pgpass) f=$d/pgpass ;;
    mycnf) f=$d/my.cnf ;;
    ca) f=$d/ca.pem ;;
    clientcert) f=$d/client-cert.pem ;;
    clientkey) f=$d/client-key.pem ;;
    *) fail 'unknown record type' ;;
  esac
  [ ! -e "$f" ] || fail 'duplicate record'
  IFS= read -r line || fail 'truncated record'
  case $line in *[!A-Za-z0-9+/=]*) fail 'bad record encoding' ;; esac
  printf '%s' "$line" | base64 -d >"$f" 2>/dev/null || fail 'bad record encoding'
  [ "$(($(wc -c <"$f")))" -eq "$len" ] || fail 'record length mismatch'
  chmod 600 "$f"
done
unset line t len f n
exec "$@"
`

// Entrypoint returns the container entrypoint; the tool's argv becomes the
// container Cmd, so the wrapper sees it as "$@".
func Entrypoint() []string { return []string{"/bin/sh", "-c", Script, "--"} }

// Record is one preamble record. Data is borrowed: Encode copies it into
// the output and never retains it.
type Record struct {
	Type RecordType
	Data []byte
}

// Errors of Encode and ForCommand.
var (
	ErrInvalidRecord = errors.New("preamble: invalid record")
	ErrMissingFile   = errors.New("preamble: no material for a required file")
)

// Encode frames records. The result contains the secrets in base64; the
// caller zeroes it (crypto.Zero) once it has been written to the
// container's stdin. On error nothing is allocated that holds secrets.
func Encode(records []Record) ([]byte, error) {
	if len(records) > MaxRecords {
		return nil, fmt.Errorf("%w: more than %d records", ErrInvalidRecord, MaxRecords)
	}
	size := len(Header) + 1 + len(Trailer) + 1
	seen := make(map[RecordType]bool, len(records))
	for _, r := range records {
		if _, ok := files[r.Type]; !ok {
			return nil, fmt.Errorf("%w: unknown type", ErrInvalidRecord)
		}
		if seen[r.Type] {
			return nil, fmt.Errorf("%w: duplicate type %s", ErrInvalidRecord, r.Type)
		}
		seen[r.Type] = true
		if len(r.Data) > MaxRecordBytes {
			return nil, fmt.Errorf("%w: %s larger than %d bytes", ErrInvalidRecord, r.Type, MaxRecordBytes)
		}
		size += len(r.Type) + 1 + len(strconv.Itoa(len(r.Data))) + 1 +
			base64.StdEncoding.EncodedLen(len(r.Data)) + 1
	}
	// Exact size: no reallocation leaves secret copies behind.
	out := make([]byte, 0, size)
	out = append(out, Header...)
	out = append(out, '\n')
	for _, r := range records {
		out = append(out, r.Type...)
		out = append(out, ' ')
		out = strconv.AppendInt(out, int64(len(r.Data)), 10)
		out = append(out, '\n')
		out = base64.StdEncoding.AppendEncode(out, r.Data)
		out = append(out, '\n')
	}
	out = append(out, Trailer...)
	out = append(out, '\n')
	if len(out) != size {
		crypto.Zero(out)
		return nil, errors.New("preamble: internal size mismatch")
	}
	return out, nil
}

// Material holds the file contents a command may need. The caller owns
// and zeroes every slice.
type Material struct {
	// PGPass is engine.PGPassLine output.
	PGPass []byte
	// MyCnf is engine.MySQLOptionFile output.
	MyCnf []byte
	// CA is the PEM of the pinned trust anchor(s).
	CA []byte
	// ClientCert and ClientKey are the optional client certificate (PEM).
	ClientCert []byte
	ClientKey  []byte
}

func (m *Material) get(t RecordType) ([]byte, bool) {
	var b []byte
	switch t {
	case RecordPGPass:
		b = m.PGPass
	case RecordMyCnf:
		b = m.MyCnf
	case RecordCA:
		b = m.CA
	case RecordClientCert:
		b = m.ClientCert
	case RecordClientKey:
		b = m.ClientKey
	default:
		return nil, false
	}
	// pgpass and my.cnf may legitimately be short but never empty (they
	// always have a line / a [client] group); PEM files are never empty.
	return b, len(b) > 0
}

// ForCommand returns exactly the records the command's Files need, in the
// command's order. Material for files the command does not use is never
// sent; a required file without material is an error.
func ForCommand(cmd engine.Command, m *Material) ([]Record, error) {
	if m == nil {
		m = &Material{}
	}
	recs := make([]Record, 0, len(cmd.Files))
	var types []RecordType
	for _, f := range cmd.Files {
		t, ok := TypeForFile(f)
		if !ok {
			return nil, fmt.Errorf("%w: unknown file", ErrInvalidRecord)
		}
		if slices.Contains(types, t) {
			return nil, fmt.Errorf("%w: duplicate file", ErrInvalidRecord)
		}
		types = append(types, t)
		data, ok := m.get(t)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrMissingFile, t)
		}
		recs = append(recs, Record{Type: t, Data: data})
	}
	return recs, nil
}
