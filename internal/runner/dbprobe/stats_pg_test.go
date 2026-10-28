package dbprobe

import (
	"regexp"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/engine"
)

var (
	pgLiteral    = regexp.MustCompile(`'[^']*'`)
	pgQuotedID   = regexp.MustCompile(`"[^"]*"`)
	pgQualOp     = regexp.MustCompile(`OPERATOR\(pg_catalog\.[^)\s]+\)`)
	pgQualCast   = regexp.MustCompile(`::pg_catalog\.[a-z0-9_]*(\[\])?`)
	pgCollate    = regexp.MustCompile(`COLLATE\s+(\S+)`)
	pgCall       = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_.]*)\s*\(`)
	pgInKeyword  = regexp.MustCompile(`(?i)\bIN\b`)
	pgCallSyntax = map[string]bool{
		"SELECT": true, "FROM": true, "WHERE": true, "AND": true, "OR": true, "NOT": true,
		"EXISTS": true, "ANY": true, "ALL": true, "OVER": true, "ON": true,
	}
)

// pgUnqualified returns why a statement could resolve a function,
// operator, type or collation through the search_path ("" if none).
func pgUnqualified(q string) string {
	for _, m := range pgCollate.FindAllStringSubmatch(q, -1) {
		if !strings.HasPrefix(m[1], "pg_catalog.") {
			return "unqualified collation " + m[1]
		}
	}
	s := pgLiteral.ReplaceAllString(q, "L")
	s = pgQuotedID.ReplaceAllString(s, "I")
	s = pgQualOp.ReplaceAllString(s, " ")
	s = pgQualCast.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "(*)", "()")
	if strings.ContainsAny(s, "=<>!~|+-*/%^&#@:") {
		return "operator or cast outside OPERATOR(pg_catalog.op) / ::pg_catalog.type: " + s
	}
	if pgInKeyword.MatchString(s) {
		return "IN resolves its operator through the search_path"
	}
	for _, m := range pgCall.FindAllStringSubmatch(s, -1) {
		if !pgCallSyntax[m[1]] && !strings.HasPrefix(m[1], "pg_catalog.") {
			return "unqualified function " + m[1]
		}
	}
	return ""
}

// Every statement of a PostgreSQL session is schema-qualified (D-152):
// even if the search_path pin failed, nothing of the database owner's
// could be resolved.
func TestPGQueriesQualified(t *testing.T) {
	s, err := engine.QuotePGObject("evil schema")
	if err != nil {
		t.Fatal(err)
	}
	n, err := engine.QuotePGObject(`t"x`)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := engine.ForKind(engine.KindPostgres)
	if err != nil {
		t.Fatal(err)
	}
	for name, q := range map[string]string{
		"check": pgCheckSettings, "pin": pgPinSettings, "restrict": pgRestrictKinds,
		"objects": pgObjectCountsQuery, "tables": pgTablesQuery, "privileges": pgNoPrivilegeQuery,
		"globals": pgGlobalCountsQuery, "timeouts": pgLiftTimeouts, "snapshot": pgExportSnapshot,
		"count":   "SELECT " + pgCount + " FROM ONLY " + s + "." + n,
		"version": eng.VersionQuery(), "databases": eng.ListDatabasesQuery(),
		"neutralize_owner": pgNeutralizeOwnerQuery, "secdef": pgSecdefQuery, "secdef_count": pgSecdefCountQuery,
		"public_access": pgPublicAccessQuery, "foreign_sessions": pgForeignSessionsQuery,
	} {
		if why := pgUnqualified(q); why != "" {
			t.Errorf("%s: %s", name, why)
		}
	}
}

// The checker itself catches the shapes of the original finding.
func TestPGUnqualifiedDetects(t *testing.T) {
	for _, q := range []string{
		`SELECT count(*) FROM pg_catalog.pg_class`,
		`SELECT 1 FROM pg_catalog.pg_class c WHERE c.relkind = 'r'`,
		`SELECT 1 FROM pg_catalog.pg_namespace n WHERE n.nspname NOT IN ('a')`,
		`SELECT n.nspname || '.' FROM pg_catalog.pg_namespace n`,
		`SELECT 1 WHERE 'a' !~ 'b'`,
		`SELECT c.reltuples::bigint FROM pg_catalog.pg_class c`,
		`SELECT 1 ORDER BY 1 COLLATE "C"`,
		`SELECT has_table_privilege(1, 'SELECT')`,
		`SELECT 1 WHERE 1 OPERATOR(public.=) 1`,
	} {
		if pgUnqualified(q) == "" {
			t.Errorf("not detected: %s", q)
		}
	}
}
