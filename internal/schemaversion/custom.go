package schemaversion

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Mode is a job's version-capture mode (jobs.version_query_mode).
type Mode string

// Modes.
const (
	ModeNone   Mode = "none"
	ModePreset Mode = "preset"
	ModeCustom Mode = "custom"
)

// Valid reports a known mode.
func (m Mode) Valid() bool { return m == ModeNone || m == ModePreset || m == ModeCustom }

// Status is the outcome of one capture (run_items.schema_version_status,
// the stats object's schema_version_status). Stable strings.
type Status string

// Statuses.
const (
	// StatusOK: a version was captured.
	StatusOK Status = "ok"
	// StatusNone: the framework table (or the custom query's table) does
	// not exist, has no row or the value is NULL/empty: no version.
	StatusNone Status = "none"
	// StatusUnavailable: the query failed (privilege, timeout, unexpected
	// table layout, server without the D-152 safeguards, error).
	StatusUnavailable Status = "unavailable"
	// StatusInvalid: the result is longer than MaxValueBytes or not
	// printable ASCII.
	StatusInvalid Status = "invalid"
)

// Valid reports a known status.
func (s Status) Valid() bool {
	return s == StatusOK || s == StatusNone || s == StatusUnavailable || s == StatusInvalid
}

// Limits.
const (
	// MaxCustomSQLBytes bounds a custom query.
	MaxCustomSQLBytes = 2048
	// MaxValueBytes bounds a captured version.
	MaxValueBytes = 64
	// StatementTimeout is the server-side limit of the version statement
	// (PostgreSQL statement_timeout, MySQL max_execution_time, MariaDB
	// max_statement_time).
	StatementTimeout = 5 * time.Second
	// LockTimeout bounds lock waits of the statement (PostgreSQL
	// lock_timeout, MySQL/MariaDB lock_wait_timeout in whole seconds).
	LockTimeout = 2 * time.Second
	// CaptureTimeout bounds the whole capture on the client side (layout
	// check plus statement plus cleanup).
	CaptureTimeout = 30 * time.Second
	// DefaultSchema is the PostgreSQL schema of a preset's table.
	DefaultSchema = "public"
)

// schemaRe is the PostgreSQL schema of a preset table: a plain identifier
// (quoted with engine.QuotePGObject anyway).
var schemaRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)

// ValidateSchema checks the schema name of a preset's table.
func ValidateSchema(s string) error {
	if !schemaRe.MatchString(s) {
		return errors.New("a schema name is a letter or underscore followed by up to 62 letters, digits or underscores")
	}
	return nil
}

// ValidatePreset checks a preset name.
func ValidatePreset(name string) error {
	if _, ok := Lookup(name); !ok {
		return errors.New("unknown preset")
	}
	return nil
}

// CleanValue checks a captured version: surrounding spaces are trimmed;
// empty is "no version", longer than MaxValueBytes or anything but
// printable ASCII is invalid. The value is returned only with StatusOK.
func CleanValue(v string) (string, Status) {
	v = strings.Trim(v, " \t\r\n")
	if v == "" {
		return "", StatusNone
	}
	if len(v) > MaxValueBytes {
		return "", StatusInvalid
	}
	for i := 0; i < len(v); i++ {
		if v[i] < 0x20 || v[i] > 0x7e {
			return "", StatusInvalid
		}
	}
	return v, StatusOK
}

// ValidValue reports whether v is a clean captured version (the worker
// re-checks the runner's value with it).
func ValidValue(v string) bool {
	c, st := CleanValue(v)
	return st == StatusOK && c == v
}

// SQLError is a refused custom query; Message is safe to show (it may
// quote a word of the caller's own query).
type SQLError struct{ Message string }

func (e *SQLError) Error() string { return "schemaversion: " + e.Message }

func sqlErr(format string, args ...any) error {
	return &SQLError{Message: fmt.Sprintf(format, args...)}
}

// The custom query check (D-210). It is deliberately simple and strict:
// every rule applies to the WHOLE text, string literals, quoted
// identifiers and all, so no quoting or comment trick can hide anything
// from it. It is a guardrail, not the security boundary: the boundary is
// how the runner executes the query (a READ ONLY transaction, statement
// and lock timeouts, one statement, the first column of the first row, a
// bounded printable result). A privileged backup role remains a residual
// risk (SECURITY.md RN-19).

// deniedWords are keywords refused as whole words (case-insensitive)
// anywhere in the text. A version query needs none of them.
var deniedWords = wordSet(
	// set operations (a second query glued onto the first)
	"union", "intersect", "except",
	// writes and DDL
	"insert", "update", "delete", "merge", "replace", "into", "copy", "load", "outfile", "dumpfile",
	"create", "alter", "drop", "truncate", "rename", "comment", "grant", "revoke",
	// session and transaction control
	"set", "reset", "do", "call", "execute", "exec", "prepare", "deallocate", "declare",
	"begin", "commit", "rollback", "savepoint", "release", "start", "transaction",
	"lock", "unlock", "share", "listen", "notify", "unlisten", "discard",
	// maintenance and server control
	"vacuum", "analyze", "reindex", "cluster", "refresh", "import", "attach", "detach",
	"install", "uninstall", "kill", "flush", "purge", "shutdown", "handler", "security",
	// delays and escapes
	"sleep", "benchmark", "uescape",
)

// deniedFunctions are functions refused as whole words anywhere in the
// text (also inside strings, where dynamic SQL would hide them): file and
// network access, backend control, locks, settings, dynamic SQL and
// delays that the READ ONLY transaction does not stop. Exact names, name
// prefixes (deniedPrefixes) and suffixes (deniedSuffixes). The list
// catches the well-known ones; a database may hold its own functions that
// do anything, which is why it is not the boundary.
var (
	deniedFunctions = wordSet(
		"pg_read_file", "pg_read_binary_file", "pg_stat_file", "pg_terminate_backend",
		"pg_cancel_backend", "pg_reload_conf", "pg_rotate_logfile", "pg_switch_wal",
		"pg_promote", "pg_notify", "set_config", "pg_backup_start", "pg_backup_stop",
		"pg_log_backend_memory_contexts", "pg_drop_replication_slot", "pg_import_system_collations",
		"ts_stat", "ts_rewrite",
		"load_file", "get_lock", "release_lock", "release_all_locks",
	)
	deniedPrefixes = []string{
		"pg_ls_", "pg_file_", "pg_advisory", "pg_try_advisory", "pg_logical_", "pg_replication_",
		"pg_create_", "pg_stat_reset", "pg_sleep", "lo_", "dblink",
		"query_to_xml", "table_to_xml", "schema_to_xml", "database_to_xml", "cursor_to_xml",
		"xp_", "sys_",
	}
	deniedSuffixes = []string{"_to_xmlschema", "_to_xml_and_xmlschema"}
)

// deniedSequences are refused anywhere in the text.
var deniedSequences = []struct{ seq, what string }{
	{";", "a semicolon (;)"},
	{"--", "a comment (--)"},
	{"/*", "a comment (/*)"},
	{"*/", "a comment (*/)"},
	{"#", "a comment (#)"},
	{`\`, `a backslash (\)`},
	{"$", "a dollar sign ($)"},
}

func wordSet(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// deniedFunction returns the list entry a name matches ("" for none):
// the name, "prefix*" or "*suffix".
func deniedFunction(name string) string {
	if deniedFunctions[name] {
		return name
	}
	for _, p := range deniedPrefixes {
		if strings.HasPrefix(name, p) {
			return p + "*"
		}
	}
	for _, s := range deniedSuffixes {
		if strings.HasSuffix(name, s) {
			return "*" + s
		}
	}
	return ""
}

// ValidateCustomSQL checks a custom version query. It is run by the server
// when a job is saved, by the worker before a dump and by the runner
// before every execution. The rules, all over the whole text:
//
//   - not empty, at most MaxCustomSQLBytes;
//   - printable ASCII, space, tab and newline only (no carriage return,
//     NUL or other control character, nothing outside ASCII);
//   - no ";" (not even a trailing one), no comment ("--", "/*", "*/",
//     "#"), no backslash, no "$", no U& escape;
//   - the first word is SELECT or WITH;
//   - quotes ('...', "...", `...`, doubled to escape) are terminated and
//     parentheses outside them are balanced, so the runner's wrapper stays
//     intact;
//   - no denied keyword (deniedWords) and no denied function
//     (deniedFunction) as a case-insensitive whole word anywhere, quoted
//     or not; a word glued to a number ("1union") is checked without the
//     number too.
//
// The error names the offending word or character, never the query.
func ValidateCustomSQL(sql string) error {
	if strings.TrimSpace(sql) == "" {
		return sqlErr("the query is empty")
	}
	if len(sql) > MaxCustomSQLBytes {
		return sqlErr("the query is longer than %d bytes", MaxCustomSQLBytes)
	}
	for i := 0; i < len(sql); i++ {
		switch c := sql[i]; {
		case c == '\n' || c == '\t':
		case c == '\r':
			return sqlErr("the query may not contain a carriage return (use plain newlines)")
		case c == 0:
			return sqlErr("the query may not contain a NUL character")
		case c < 0x20 || c == 0x7f:
			return sqlErr("the query may not contain the control character 0x%02X", c)
		case c > 0x7f:
			return sqlErr("the query may contain ASCII characters only (found byte 0x%02X)", c)
		}
	}
	for _, d := range deniedSequences {
		if strings.Contains(sql, d.seq) {
			return sqlErr("the query may not contain %s", d.what)
		}
	}
	lower := strings.ToLower(sql)
	if strings.Contains(lower, "u&'") || strings.Contains(lower, `u&"`) {
		return sqlErr("the query may not contain a U& escape")
	}
	if err := checkFirstWord(lower); err != nil {
		return err
	}
	if err := checkQuotesAndParens(sql); err != nil {
		return err
	}
	return checkWords(lower)
}

// StatementBody returns the validated statement without surrounding
// whitespace (the runner wraps it).
func StatementBody(sql string) (string, error) {
	if err := ValidateCustomSQL(sql); err != nil {
		return "", err
	}
	return strings.Trim(sql, " \t\n"), nil
}

func isWordByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// checkFirstWord: the lower-cased text starts with the word select or with.
func checkFirstWord(lower string) error {
	rest := strings.TrimLeft(lower, " \t\n")
	j := 0
	for j < len(rest) && isWordByte(rest[j]) {
		j++
	}
	if w := rest[:j]; w != "select" && w != "with" {
		return sqlErr("the query must start with SELECT or WITH")
	}
	return nil
}

// checkQuotesAndParens verifies terminated quotes and balanced
// parentheses outside them. Without backslashes and comments both engines
// read quotes the same way.
func checkQuotesAndParens(sql string) error {
	depth := 0
	for i := 0; i < len(sql); i++ {
		switch c := sql[i]; c {
		case '\'', '"', '`':
			j := i + 1
			for {
				k := strings.IndexByte(sql[j:], c)
				if k < 0 {
					return sqlErr("the query has an unterminated quote (%c)", c)
				}
				j += k + 1
				if j < len(sql) && sql[j] == c {
					j++ // a doubled quote character
					continue
				}
				break
			}
			i = j - 1
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return sqlErr("the query has unbalanced parentheses")
			}
		}
	}
	if depth != 0 {
		return sqlErr("the query has unbalanced parentheses")
	}
	return nil
}

// checkWords applies the word and function denylists to every maximal run
// of letters, digits and underscores in the lower-cased text, and to the
// part of a run after each digit (both servers read "1union" as "1" and
// "union").
func checkWords(lower string) error {
	for i := 0; i < len(lower); {
		if !isWordByte(lower[i]) {
			i++
			continue
		}
		j := i
		for j < len(lower) && isWordByte(lower[j]) {
			j++
		}
		run := lower[i:j]
		for k := 0; k < len(run); k++ {
			if k > 0 && (!isDigit(run[k-1]) || isDigit(run[k])) {
				continue
			}
			if err := checkWord(run[k:]); err != nil {
				return err
			}
		}
		i = j
	}
	return nil
}

func checkWord(w string) error {
	if deniedWords[w] {
		return sqlErr("the query may not contain the word %s", strings.ToUpper(w))
	}
	if f := deniedFunction(w); f != "" {
		return sqlErr("the query may not use %s", f)
	}
	return nil
}
