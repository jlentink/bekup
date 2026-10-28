package dbprobe

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/jlentink/bekup/internal/runnerpb"
)

// Privilege detection (task 4.8, D-068). SQL inside a restored dump runs
// with the rights of the connection's role; a role that can reach the
// server's host (files, programs, server variables) or raise its own or
// other accounts' privileges lets a malicious dump take over the target
// server. PrivilegeSource reports such roles with stable reason codes;
// serve gates restores into them (admins only, with an acknowledgement).
//
// The detection is fail closed: anything it cannot classify completely
// (MySQL/MariaDB role grants, unparsable grant lines, query failures)
// yields PRIVILEGE_STATUS_UNKNOWN, which serve treats as privileged.
// Reasons never carry server-provided names except privilege names and
// PostgreSQL's predefined role names (fixed vocabulary, bounded).

// Limits of a verdict.
const (
	// MaxPrivilegeReasons bounds the reasons of one verdict.
	MaxPrivilegeReasons = 32
	// maxReasonBytes bounds one reason.
	maxReasonBytes = 64
	// maxGrantLines bounds the SHOW GRANTS lines read.
	maxGrantLines = 512
	// maxMemberRoles bounds the PostgreSQL membership rows read.
	maxMemberRoles = 1000
	// maxGrantLineBytes bounds one SHOW GRANTS line that is parsed.
	maxGrantLineBytes = 64 << 10
	// detailTooManyRows is the Error detail of a row bound (rows, column).
	detailTooManyRows = "too many rows"
)

// Reason codes.
const (
	// PostgreSQL role attributes of the connection's role.
	ReasonPGSuperuser   = "superuser"
	ReasonPGCreateRole  = "createrole"
	ReasonPGReplication = "replication"
	ReasonPGBypassRLS   = "bypassrls"
	// ReasonPGMemberOfPrefix + attribute: member (directly or not) of a
	// role with that attribute (reachable with SET ROLE).
	ReasonPGMemberOfPrefix = "member_of_role_with:"
	// ReasonPGPredefinedPrefix + predefined role name.
	ReasonPGPredefinedPrefix = "member_of:"

	// ReasonMyGlobalPrefix + privilege name: a global (*.*) privilege
	// beyond data and schema work.
	ReasonMyGlobalPrefix = "global:"
	// ReasonMyGlobalDataPrefix + privilege name: a global (*.*) data or
	// schema privilege. It reaches every schema of the server, not only
	// the restored one, so a dump could read or change other databases
	// (gated since the M4 review).
	ReasonMyGlobalDataPrefix = "global_data:"
	// ReasonMyProxy: a PROXY grant (act as other accounts).
	ReasonMyProxy = "proxy"
	// ReasonMySystemSchema: write privileges on the mysql system schema
	// (account tables).
	ReasonMySystemSchema = "system_schema_write"

	// Unknown verdicts.
	ReasonRolesGranted     = "roles_granted"
	ReasonUnparsedGrant    = "unparsed_grant"
	ReasonTooMany          = "too_many_grants"
	ReasonDetectionFailed  = "detection_failed"
	ReasonDetectionMissing = "detection_unsupported"
)

// Privileges is the verdict on a connection's role.
type Privileges struct {
	Status  runnerpb.PrivilegeStatus
	Reasons []string
}

// PrivilegeSource is implemented by the PostgreSQL and MySQL/MariaDB
// connections.
type PrivilegeSource interface {
	// Privileges classifies the connection's role. On a query failure it
	// returns an UNKNOWN verdict (ReasonDetectionFailed) and the error.
	Privileges(ctx context.Context) (Privileges, error)
}

// UnknownPrivileges is the verdict when the detection could not run.
func UnknownPrivileges(reason string) Privileges {
	return Privileges{Status: runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_UNKNOWN, Reasons: []string{reason}}
}

// verdict collects reasons.
type verdict struct {
	privileged []string
	// data are privileged reasons of the global_data class; they are
	// listed after the others so the reason cap never hides a
	// server-level privilege behind a long data privilege list.
	data    []string
	unknown []string
}

func (v *verdict) priv(r string)     { v.privileged = append(v.privileged, r) }
func (v *verdict) dataPriv(r string) { v.data = append(v.data, r) }
func (v *verdict) unsure(r string)   { v.unknown = append(v.unknown, r) }
func (v *verdict) result() Privileges {
	all := append(append(slices.Clone(v.privileged), v.data...), v.unknown...)
	reasons := make([]string, 0, len(all))
	for _, r := range all {
		if len(r) > maxReasonBytes {
			r = r[:maxReasonBytes]
		}
		if !slices.Contains(reasons, r) && len(reasons) < MaxPrivilegeReasons {
			reasons = append(reasons, r)
		}
	}
	st := runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_NOT_PRIVILEGED
	switch {
	case len(v.privileged) > 0 || len(v.data) > 0:
		st = runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_PRIVILEGED
	case len(v.unknown) > 0:
		st = runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_UNKNOWN
	}
	return Privileges{Status: st, Reasons: reasons}
}

// isTooManyRows reports the row bound of rows / column.
func isTooManyRows(err error) bool {
	var pe *Error
	return errors.As(err, &pe) && pe.Reason == runnerpb.ReasonFailed && pe.Detail == detailTooManyRows
}

// --- PostgreSQL ---------------------------------------------------------------

// pgPredefinedRoles are the predefined roles whose membership is
// privileged: host file and program access, signaling other sessions,
// reading every session's statements, subscriptions (outbound
// connections from the server), and pg_read_all_data / pg_write_all_data:
// they grant SELECT / INSERT, UPDATE, DELETE on every table of every
// database the role can connect to, so a dump could read or change other
// databases than the restored one (M4 review; corrects D-168).
var pgPredefinedRoles = []string{
	"pg_read_all_data",
	"pg_write_all_data",
	"pg_execute_server_program",
	"pg_read_server_files",
	"pg_write_server_files",
	"pg_signal_backend",
	"pg_read_all_stats",
	"pg_create_subscription",
}

// pgSelfPrivileges reads the role attributes of the session's role.
// CURRENT_USER is an SQL keyword (not resolved through the search_path).
const pgSelfPrivileges = `SELECT r.rolsuper, r.rolcreaterole, r.rolreplication, r.rolbypassrls ` +
	`FROM pg_catalog.pg_roles r WHERE r.rolname OPERATOR(pg_catalog.=) CURRENT_USER`

// pgMemberPrivileges lists the roles the session's role is a member of
// (directly or not, with or without INHERIT/SET) that carry a privileged
// attribute or are privileged predefined roles.
var pgMemberPrivileges = `SELECT m.rolname, m.rolsuper, m.rolcreaterole, m.rolreplication, m.rolbypassrls ` +
	`FROM pg_catalog.pg_roles m WHERE m.rolname OPERATOR(pg_catalog.<>) CURRENT_USER ` +
	`AND pg_catalog.pg_has_role(CURRENT_USER, m.oid, 'MEMBER') ` +
	`AND (m.rolsuper OR m.rolcreaterole OR m.rolreplication OR m.rolbypassrls ` +
	`OR m.rolname OPERATOR(pg_catalog.=) ANY (ARRAY['` + strings.Join(pgPredefinedRoles, `', '`) + `']::pg_catalog.name[]))`

// Privileges implements PrivilegeSource.
func (c *pgConn) Privileges(ctx context.Context) (Privileges, error) {
	self, err := c.rows(ctx, pgSelfPrivileges, 1)
	if err != nil {
		return UnknownPrivileges(ReasonDetectionFailed), err
	}
	members, err := c.rows(ctx, pgMemberPrivileges, maxMemberRoles)
	switch {
	case isTooManyRows(err) && len(self) == 1 && len(self[0]) == len(pgAttributeReasons) && self[0][0] == "t":
		// A superuser is a member of every role: the list may be long.
		return classifyPG(self, nil), nil
	case isTooManyRows(err):
		return UnknownPrivileges(ReasonTooMany), nil
	case err != nil:
		return UnknownPrivileges(ReasonDetectionFailed), err
	}
	return classifyPG(self, members), nil
}

var pgAttributeReasons = []string{ReasonPGSuperuser, ReasonPGCreateRole, ReasonPGReplication, ReasonPGBypassRLS}

// classifyPG turns the rows of pgSelfPrivileges and pgMemberPrivileges
// into a verdict (booleans in text format: "t" / "f").
func classifyPG(self, members [][]string) Privileges {
	var v verdict
	if len(self) != 1 || len(self[0]) != len(pgAttributeReasons) {
		v.unsure(ReasonDetectionFailed)
		return v.result()
	}
	for i, r := range pgAttributeReasons {
		switch self[0][i] {
		case "t":
			v.priv(r)
		case "f":
		default:
			v.unsure(ReasonDetectionFailed)
		}
	}
	for _, m := range members {
		if len(m) != 1+len(pgAttributeReasons) {
			v.unsure(ReasonDetectionFailed)
			continue
		}
		if slices.Contains(pgPredefinedRoles, m[0]) {
			v.priv(ReasonPGPredefinedPrefix + m[0])
		}
		for i, r := range pgAttributeReasons {
			if m[1+i] == "t" {
				v.priv(ReasonPGMemberOfPrefix + r)
			}
		}
	}
	return v.result()
}

// --- MySQL / MariaDB ------------------------------------------------------------

// myShowGrants lists the current account's grants (no user-table reads,
// D-153; every account may run it for itself).
const myShowGrants = "SHOW GRANTS"

// Privileges implements PrivilegeSource.
func (c *myConn) Privileges(ctx context.Context) (Privileges, error) {
	lines, err := c.column(ctx, maxGrantLines, myShowGrants)
	switch {
	case isTooManyRows(err):
		return UnknownPrivileges(ReasonTooMany), nil
	case err != nil:
		return UnknownPrivileges(ReasonDetectionFailed), err
	}
	return ParseMySQLGrants(lines), nil
}

// myHarmlessGlobal are global privileges that neither touch data nor
// act on the server or other accounts (names and status only).
var myHarmlessGlobal = map[string]bool{
	"USAGE": true, "SHOW DATABASES": true, "REPLICATION CLIENT": true,
	// MariaDB.
	"BINLOG MONITOR": true, "SLAVE MONITOR": true, "REPLICA MONITOR": true,
	// MySQL 8 dynamic privileges without effect on data or other accounts.
	"FLUSH_STATUS": true, "FLUSH_USER_RESOURCES": true, "FLUSH_OPTIMIZER_COSTS": true,
	"APPLICATION_PASSWORD_ADMIN": true, "RESOURCE_GROUP_USER": true,
}

// myGlobalData are the data and schema privileges a restore needs on its
// own schema. Granted globally (*.*) they reach every schema of the
// server, so they are privileged (class global_data): a restore account
// must hold them on the target schema only. Every global privilege in
// neither list, including ones unknown to bekup (new MySQL dynamic
// privileges), is privileged in the global class.
var myGlobalData = map[string]bool{
	"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true,
	"CREATE": true, "DROP": true, "REFERENCES": true, "INDEX": true, "ALTER": true,
	"CREATE TEMPORARY TABLES": true, "LOCK TABLES": true, "EXECUTE": true,
	"CREATE VIEW": true, "SHOW VIEW": true, "CREATE ROUTINE": true, "ALTER ROUTINE": true,
	"EVENT": true, "TRIGGER": true,
	// MariaDB.
	"DELETE HISTORY": true, "SHOW CREATE ROUTINE": true,
	// MySQL 8.
	"SHOW_ROUTINE": true,
}

// myReadOnlySchema are schema/table privileges that cannot change the
// mysql system schema.
var myReadOnlySchema = map[string]bool{"USAGE": true, "SELECT": true, "SHOW VIEW": true}

// ParseMySQLGrants classifies the lines of SHOW GRANTS (MySQL 5.7/8.x,
// MariaDB 10.x/11.x). Lines it does not understand make the verdict
// UNKNOWN; role grants (MySQL 8 and MariaDB roles: their privileges are
// not listed, and SET ROLE can activate them) make it UNKNOWN as well.
func ParseMySQLGrants(lines []string) Privileges {
	var v verdict
	for _, line := range lines {
		parseGrantLine(line, &v)
	}
	return v.result()
}

// grantTok is a token of a grant line.
type grantTok struct {
	// word is an unquoted word (upper-cased), or a punctuation character.
	word string
	// quoted is set for a quoted name ('x', "x", `x`); text is unquoted.
	quoted bool
	text   string
}

func (t grantTok) is(w string) bool { return !t.quoted && t.word == w }

// tokenizeGrant splits a grant line; ok is false for unterminated quotes.
func tokenizeGrant(s string) ([]grantTok, bool) {
	var out []grantTok
	for i := 0; i < len(s); {
		ch := s[i]
		switch {
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r':
			i++
		case ch == '\'' || ch == '"' || ch == '`':
			var b strings.Builder
			j := i + 1
			closed := false
			for j < len(s) {
				c := s[j]
				switch {
				case c == '\\' && ch != '`' && j+1 < len(s):
					b.WriteByte(s[j+1])
					j += 2
					continue
				case c == ch && j+1 < len(s) && s[j+1] == ch:
					b.WriteByte(ch)
					j += 2
					continue
				case c == ch:
					closed = true
				default:
					b.WriteByte(c)
					j++
					continue
				}
				break
			}
			if !closed {
				return nil, false
			}
			out = append(out, grantTok{quoted: true, text: b.String()})
			i = j + 1
		case strings.IndexByte(",().@*;", ch) >= 0:
			out = append(out, grantTok{word: string(ch)})
			i++
		default:
			j := i
			for j < len(s) && strings.IndexByte(" \t\r\n'\"`,().@*;", s[j]) < 0 {
				j++
			}
			w := strings.ToUpper(s[i:j])
			out = append(out, grantTok{word: w, text: s[i:j]})
			i = j
		}
	}
	return out, true
}

// parseGrantLine classifies one SHOW GRANTS line into v.
func parseGrantLine(line string, v *verdict) {
	if len(line) > maxGrantLineBytes {
		v.unsure(ReasonUnparsedGrant)
		return
	}
	toks, ok := tokenizeGrant(line)
	if !ok || len(toks) == 0 {
		v.unsure(ReasonUnparsedGrant)
		return
	}
	switch {
	case toks[0].is("REVOKE"):
		return // partial revokes (MySQL 8) only reduce rights
	case toks[0].is("SET") && len(toks) > 2 && toks[1].is("DEFAULT") && toks[2].is("ROLE"):
		v.unsure(ReasonRolesGranted) // MariaDB default role
		return
	case !toks[0].is("GRANT"):
		v.unsure(ReasonUnparsedGrant)
		return
	}
	// GRANT <privileges> ON <level> TO <account> [...] | GRANT <roles> TO <account> [...]
	privs, rest, ok := grantPrivileges(toks[1:])
	if !ok {
		v.unsure(ReasonUnparsedGrant)
		return
	}
	if len(rest) == 0 || rest[0].is("TO") {
		v.unsure(ReasonRolesGranted) // GRANT `role`@`%` TO ... (no ON clause)
		return
	}
	if !rest[0].is("ON") {
		v.unsure(ReasonUnparsedGrant)
		return
	}
	if len(privs) == 1 && privs[0] == "PROXY" {
		v.priv(ReasonMyProxy)
		return
	}
	rest = rest[1:]
	if len(rest) > 0 && (rest[0].is("TABLE") || rest[0].is("FUNCTION") || rest[0].is("PROCEDURE") || rest[0].is("PACKAGE")) {
		rest = rest[1:]
		if len(rest) > 0 && rest[0].is("BODY") {
			rest = rest[1:]
		}
	}
	schema, global, rest, ok := grantLevel(rest)
	if !ok || len(rest) == 0 || !rest[0].is("TO") {
		v.unsure(ReasonUnparsedGrant)
		return
	}
	grantOption := hasWithGrantOption(rest)
	switch {
	case global:
		for _, p := range privs {
			switch {
			case myHarmlessGlobal[p]:
			case myGlobalData[p]:
				v.dataPriv(ReasonMyGlobalDataPrefix + p)
			default:
				v.priv(ReasonMyGlobalPrefix + privilegeName(p))
			}
		}
		if grantOption {
			v.priv(ReasonMyGlobalPrefix + "GRANT OPTION")
		}
	case likeMatches(schema, "mysql"):
		for _, p := range privs {
			if !myReadOnlySchema[p] {
				v.priv(ReasonMySystemSchema)
			}
		}
		if grantOption {
			v.priv(ReasonMySystemSchema)
		}
	}
}

// grantPrivileges reads the comma separated privilege list up to ON or TO
// (outside parentheses). Column lists are skipped. A quoted name or '@'
// (a role) yields no privilege and stops at TO.
func grantPrivileges(toks []grantTok) ([]string, []grantTok, bool) {
	var (
		privs []string
		words []string
		depth int
		role  bool
	)
	flush := func() bool {
		if len(words) == 0 {
			return role
		}
		privs = append(privs, strings.Join(words, " "))
		words = words[:0]
		return true
	}
	for i, t := range toks {
		switch {
		case t.is("("):
			depth++
		case t.is(")"):
			depth--
			if depth < 0 {
				return nil, nil, false
			}
		case depth > 0:
		case t.is("ON") || t.is("TO"):
			if !flush() {
				return nil, nil, false
			}
			if role {
				return nil, toks[i:i:i], true // role grant: no ON clause expected
			}
			return privs, toks[i:], true
		case t.is(","):
			if !flush() {
				return nil, nil, false
			}
		case t.quoted || t.is("@"):
			role = true
		case t.word == "" || t.is(".") || t.is("*") || t.is(";"):
			return nil, nil, false
		default:
			words = append(words, t.word)
		}
	}
	return nil, nil, false
}

// grantLevel reads *.*, schema.* or schema.object.
func grantLevel(toks []grantTok) (schema string, global bool, rest []grantTok, ok bool) {
	if len(toks) < 3 || !toks[1].is(".") {
		return "", false, nil, false
	}
	switch first := toks[0]; {
	case first.is("*"):
		if !toks[2].is("*") {
			return "", false, nil, false
		}
		return "", true, toks[3:], true
	case first.quoted:
		schema = first.text
	case first.word != "" && first.text != "":
		schema = first.text
	default:
		return "", false, nil, false
	}
	if !toks[2].is("*") && !toks[2].quoted && toks[2].text == "" {
		return "", false, nil, false
	}
	return schema, false, toks[3:], true
}

// hasWithGrantOption looks for WITH GRANT OPTION after TO.
func hasWithGrantOption(toks []grantTok) bool {
	for i := 0; i+2 < len(toks); i++ {
		if toks[i].is("WITH") && toks[i+1].is("GRANT") && toks[i+2].is("OPTION") {
			return true
		}
	}
	return false
}

// privilegeName bounds a privilege name for a reason code.
func privilegeName(p string) string {
	if len(p) > maxReasonBytes-len(ReasonMyGlobalPrefix) {
		return "OTHER"
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		if (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != ' ' {
			return "OTHER"
		}
	}
	return p
}

// likeMatches reports whether a schema grant pattern (LIKE syntax: % and
// _ wildcards, backslash escapes) matches name (case-insensitively, as
// lower_case_table_names may fold names).
func likeMatches(pattern, name string) bool {
	return likeMatch(strings.ToLower(pattern), strings.ToLower(name))
}

func likeMatch(p, s string) bool {
	for p != "" {
		switch p[0] {
		case '%':
			for p != "" && p[0] == '%' {
				p = p[1:]
			}
			if p == "" {
				return true
			}
			for i := 0; i <= len(s); i++ {
				if likeMatch(p, s[i:]) {
					return true
				}
			}
			return false
		case '_':
			if s == "" {
				return false
			}
			p, s = p[1:], s[1:]
		case '\\':
			if len(p) > 1 {
				p = p[1:]
			}
			fallthrough
		default:
			if s == "" || s[0] != p[0] {
				return false
			}
			p, s = p[1:], s[1:]
		}
	}
	return s == ""
}
