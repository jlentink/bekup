package engine

import (
	"bytes"
	"strconv"

	"github.com/jlentink/bekup/internal/crypto"
)

// checkSecretBytes rejects bytes that cannot be represented safely in the
// line-based password files.
func checkSecretBytes(v []byte) error {
	if bytes.ContainsAny(v, "\x00\n\r") {
		return ErrInvalidSecret
	}
	return nil
}

// EscapeOptionValue encodes v as a double-quoted MySQL/MariaDB option-file
// value. Inside the quotes "\", '"', "'", tab and backspace are
// backslash-escaped, so "#", ";" and surrounding spaces stay literal. NUL,
// CR and LF are rejected. Verified against mysqldump 8.0.46 and
// mariadb-dump 12.3.3 (--print-defaults).
func EscapeOptionValue(v []byte) ([]byte, error) {
	if err := checkSecretBytes(v); err != nil {
		return nil, err
	}
	// Exact upper bound: no reallocation leaves secret copies behind.
	out := make([]byte, 0, 2*len(v)+2)
	out = append(out, '"')
	for _, b := range v {
		switch b {
		case '\\':
			out = append(out, '\\', '\\')
		case '"':
			out = append(out, '\\', '"')
		case '\'':
			out = append(out, '\\', '\'')
		case '\t':
			out = append(out, '\\', 't')
		case '\b':
			out = append(out, '\\', 'b')
		default:
			out = append(out, b)
		}
	}
	return append(out, '"'), nil
}

// MySQLOptionFile returns the content of FileMyCnf: a [client] group with
// the password (omitted when empty). The caller zeroes the result after the
// preamble has been sent.
func MySQLOptionFile(password []byte) ([]byte, error) {
	const header = "[client]\n"
	if len(password) == 0 {
		return []byte(header), nil
	}
	v, err := EscapeOptionValue(password)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(header)+len("password=")+len(v)+1)
	out = append(out, header...)
	out = append(out, "password="...)
	out = append(out, v...)
	out = append(out, '\n')
	crypto.Zero(v)
	return out, nil
}

// pgpassEscape escapes "\" and ":" for a .pgpass field.
func pgpassEscape(dst, v []byte) []byte {
	for _, b := range v {
		if b == '\\' || b == ':' {
			dst = append(dst, '\\')
		}
		dst = append(dst, b)
	}
	return dst
}

// PGPassLine returns the content of FilePGPass: one line
// "host:port:*:user:password" (database "*", so the same file serves the
// target and the maintenance database). The caller zeroes the result.
func PGPassLine(host string, port int, user string, password []byte) ([]byte, error) {
	if err := ValidateHost(host); err != nil {
		return nil, err
	}
	if err := ValidatePort(port); err != nil {
		return nil, err
	}
	if err := ValidateUser(user); err != nil {
		return nil, err
	}
	if err := checkSecretBytes(password); err != nil {
		return nil, err
	}
	// Upper bound incl. escapes: no reallocation leaves secret copies behind.
	out := make([]byte, 0, 2*len(host)+2*len(user)+2*len(password)+16)
	out = pgpassEscape(out, []byte(host))
	out = append(out, ':')
	out = strconv.AppendInt(out, int64(port), 10)
	out = append(out, ":*:"...)
	out = pgpassEscape(out, []byte(user))
	out = append(out, ':')
	out = pgpassEscape(out, password)
	return append(out, '\n'), nil
}

// formatPort formats a validated port for argv.
func formatPort(p int) string { return strconv.Itoa(p) }
