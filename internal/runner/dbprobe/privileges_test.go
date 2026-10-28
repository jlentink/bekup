package dbprobe

import (
	"slices"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/runnerpb"
)

const (
	stNot     = runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_NOT_PRIVILEGED
	stPriv    = runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_PRIVILEGED
	stUnknown = runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_UNKNOWN
)

// The privilege queries are schema-qualified like every other statement
// of a PostgreSQL session (D-152).
func TestPGPrivilegeQueriesQualified(t *testing.T) {
	for name, q := range map[string]string{"self": pgSelfPrivileges, "members": pgMemberPrivileges} {
		if why := pgUnqualified(q); why != "" {
			t.Errorf("%s: %s", name, why)
		}
	}
}

func TestClassifyPG(t *testing.T) {
	for _, c := range []struct {
		name    string
		self    [][]string
		members [][]string
		status  runnerpb.PrivilegeStatus
		reasons []string
	}{
		{"plain role", [][]string{{"f", "f", "f", "f"}}, nil, stNot, nil},
		{"superuser", [][]string{{"t", "f", "f", "f"}}, nil, stPriv, []string{"superuser"}},
		{
			"attributes", [][]string{{"f", "t", "t", "t"}}, nil, stPriv,
			[]string{"createrole", "replication", "bypassrls"},
		},
		{
			"predefined and indirect superuser", [][]string{{"f", "f", "f", "f"}},
			[][]string{
				{"pg_execute_server_program", "f", "f", "f", "f"},
				{"pg_write_server_files", "f", "f", "f", "f"},
				{"some admin group", "t", "f", "f", "f"},
			},
			stPriv,
			[]string{"member_of:pg_execute_server_program", "member_of:pg_write_server_files", "member_of_role_with:superuser"},
		},
		{
			// They reach every database the role can connect to.
			"read and write all data", [][]string{{"f", "f", "f", "f"}},
			[][]string{{"pg_read_all_data", "f", "f", "f", "f"}, {"pg_write_all_data", "f", "f", "f", "f"}},
			stPriv, []string{"member_of:pg_read_all_data", "member_of:pg_write_all_data"},
		},
		{"other predefined roles", [][]string{{"f", "f", "f", "f"}}, [][]string{{"pg_monitor", "f", "f", "f", "f"}}, stNot, nil},
		{"no row", nil, nil, stUnknown, []string{ReasonDetectionFailed}},
		{"odd value", [][]string{{"x", "f", "f", "f"}}, nil, stUnknown, []string{ReasonDetectionFailed}},
		{"short member row", [][]string{{"f", "f", "f", "f"}}, [][]string{{"r"}}, stUnknown, []string{ReasonDetectionFailed}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := classifyPG(c.self, c.members)
			if got.Status != c.status || !slices.Equal(got.Reasons, c.reasons) {
				t.Fatalf("got %v %q, want %v %q", got.Status, got.Reasons, c.status, c.reasons)
			}
		})
	}
}

func TestParseMySQLGrants(t *testing.T) {
	for _, c := range []struct {
		name    string
		lines   []string
		status  runnerpb.PrivilegeStatus
		reasons []string
	}{
		{
			"restore role limited to its schema", []string{
				"GRANT USAGE, SHOW DATABASES, REPLICATION CLIENT ON *.* TO `restore`@`%`",
				"GRANT ALL PRIVILEGES ON `app`.* TO `restore`@`%`",
				"GRANT SELECT, INSERT, UPDATE, DELETE, CREATE, DROP, INDEX, ALTER, CREATE TEMPORARY TABLES, LOCK TABLES, " +
					"EXECUTE, CREATE VIEW, SHOW VIEW, CREATE ROUTINE, ALTER ROUTINE, EVENT, TRIGGER ON `app`.* TO `restore`@`%`",
			}, stNot, nil,
		},
		{
			// Global data privileges reach every schema of the server.
			"global data privileges", []string{
				"GRANT USAGE ON *.* TO `restore`@`%`",
				"GRANT SELECT, INSERT, UPDATE, DELETE, CREATE, DROP, INDEX, ALTER, CREATE TEMPORARY TABLES, LOCK TABLES, " +
					"EXECUTE, CREATE VIEW, SHOW VIEW, CREATE ROUTINE, ALTER ROUTINE, EVENT, TRIGGER ON *.* TO `restore`@`%`",
			}, stPriv, []string{
				"global_data:SELECT", "global_data:INSERT", "global_data:UPDATE", "global_data:DELETE",
				"global_data:CREATE", "global_data:DROP", "global_data:INDEX", "global_data:ALTER",
				"global_data:CREATE TEMPORARY TABLES", "global_data:LOCK TABLES", "global_data:EXECUTE",
				"global_data:CREATE VIEW", "global_data:SHOW VIEW", "global_data:CREATE ROUTINE",
				"global_data:ALTER ROUTINE", "global_data:EVENT", "global_data:TRIGGER",
			},
		},
		{"global select alone", []string{"GRANT SELECT ON *.* TO `u`@`%`"}, stPriv, []string{"global_data:SELECT"}},
		{
			"mariadb and mysql 8 data privileges", []string{
				"GRANT DELETE HISTORY, SHOW CREATE ROUTINE ON *.* TO `u`@`%`",
				"GRANT SHOW_ROUTINE ON *.* TO `u`@`%`",
			}, stPriv, []string{"global_data:DELETE HISTORY", "global_data:SHOW CREATE ROUTINE", "global_data:SHOW_ROUTINE"},
		},
		{
			// Server-level reasons come first, whatever the line order.
			"server privileges listed before data", []string{
				"GRANT SELECT, INSERT ON *.* TO `u`@`%`",
				"GRANT FILE ON *.* TO `u`@`%`",
			}, stPriv, []string{"global:FILE", "global_data:SELECT", "global_data:INSERT"},
		},
		{
			"harmless global privileges", []string{
				"GRANT USAGE, SHOW DATABASES, REPLICATION CLIENT ON *.* TO `u`@`%`",
				"GRANT FLUSH_STATUS,APPLICATION_PASSWORD_ADMIN ON *.* TO `u`@`%`",
			}, stNot, nil,
		},
		{
			"mysql 5.7 root", []string{
				"GRANT ALL PRIVILEGES ON *.* TO 'root'@'localhost' WITH GRANT OPTION",
				"GRANT PROXY ON ''@'' TO 'root'@'localhost' WITH GRANT OPTION",
			}, stPriv, []string{"global:ALL PRIVILEGES", "global:GRANT OPTION", "proxy"},
		},
		{
			"mysql 8 static and dynamic", []string{
				"GRANT SELECT, RELOAD, PROCESS, FILE, SUPER ON *.* TO `ops`@`%`",
				"GRANT SYSTEM_VARIABLES_ADMIN,SET_USER_ID,SYSTEM_USER,FLUSH_TABLES ON *.* TO `ops`@`%`",
			}, stPriv,
			[]string{"global:RELOAD", "global:PROCESS", "global:FILE", "global:SUPER",
				"global:SYSTEM_VARIABLES_ADMIN", "global:SET_USER_ID", "global:SYSTEM_USER", "global:FLUSH_TABLES",
				"global_data:SELECT"},
		},
		{
			"unknown dynamic privilege counts", []string{"GRANT SOME_FUTURE_ADMIN ON *.* TO `u`@`%`"},
			stPriv, []string{"global:SOME_FUTURE_ADMIN"},
		},
		{
			"mariadb root", []string{
				"GRANT ALL PRIVILEGES ON *.* TO `root`@`localhost` IDENTIFIED VIA mysql_native_password USING 'invalid' OR unix_socket WITH GRANT OPTION",
				"GRANT PROXY ON ''@'%' TO 'root'@'localhost' WITH GRANT OPTION",
			}, stPriv, []string{"global:ALL PRIVILEGES", "global:GRANT OPTION", "proxy"},
		},
		{
			"mariadb split super", []string{
				"GRANT BINLOG ADMIN, SET USER, READ_ONLY ADMIN, SLAVE MONITOR ON *.* TO `u`@`%` IDENTIFIED BY PASSWORD '*AAAA'",
			}, stPriv, []string{"global:BINLOG ADMIN", "global:SET USER", "global:READ_ONLY ADMIN"},
		},
		{
			"create user and shutdown", []string{"GRANT SHUTDOWN, CREATE USER ON *.* TO 'u'@'%'"},
			stPriv, []string{"global:SHUTDOWN", "global:CREATE USER"},
		},
		{
			"global grant option alone", []string{"GRANT SELECT ON *.* TO `u`@`%` WITH GRANT OPTION"},
			stPriv, []string{"global:GRANT OPTION", "global_data:SELECT"},
		},
		{
			"schema grant option is not privileged", []string{"GRANT ALL ON `app`.* TO `u`@`%` WITH GRANT OPTION"},
			stNot, nil,
		},
		{
			"mysql schema write", []string{"GRANT INSERT, UPDATE ON `mysql`.* TO `u`@`%`"},
			stPriv, []string{ReasonMySystemSchema},
		},
		{
			"mysql table write", []string{"GRANT INSERT (`User`), SELECT ON `mysql`.`user` TO `u`@`%`"},
			stPriv, []string{ReasonMySystemSchema},
		},
		{"mysql schema read", []string{"GRANT SELECT ON mysql.* TO 'u'@'%'"}, stNot, nil},
		{"wildcard schema covers mysql", []string{"GRANT ALL ON `%`.* TO `u`@`%`"}, stPriv, []string{ReasonMySystemSchema}},
		{"pattern covers mysql", []string{"GRANT ALL ON `my_ql`.* TO `u`@`%`"}, stPriv, []string{ReasonMySystemSchema}},
		{"escaped pattern does not", []string{"GRANT ALL ON `my\\_ql`.* TO `u`@`%`"}, stNot, nil},
		{
			"mysql 8 role grant", []string{
				"GRANT USAGE ON *.* TO `u`@`%`",
				"GRANT `app_admin`@`%`,`reader`@`%` TO `u`@`%`",
			}, stUnknown, []string{ReasonRolesGranted},
		},
		{
			"mariadb role and default role", []string{
				"GRANT USAGE ON *.* TO `u`@`%`",
				"GRANT `dba` TO `u`@`%`",
				"SET DEFAULT ROLE `dba` FOR `u`@`%`",
			}, stUnknown, []string{ReasonRolesGranted},
		},
		{"unquoted role", []string{"GRANT dba TO 'u'@'%'"}, stUnknown, []string{ReasonRolesGranted}},
		{
			"privileged beats unknown", []string{"GRANT FILE ON *.* TO `u`@`%`", "GRANT `r`@`%` TO `u`@`%`"},
			stPriv, []string{"global:FILE", ReasonRolesGranted},
		},
		{
			// A partial revoke does not make a global grant schema-limited.
			"partial revoke ignored", []string{"GRANT SELECT ON *.* TO `u`@`%`", "REVOKE SELECT ON `mysql`.* FROM `u`@`%`"},
			stPriv, []string{"global_data:SELECT"},
		},
		{"unparsable", []string{"GRANT SELECT ON *.* TO `u`@`%"}, stUnknown, []string{ReasonUnparsedGrant}},
		{"other statement", []string{"CREATE USER x"}, stUnknown, []string{ReasonUnparsedGrant}},
		{"no level", []string{"GRANT SELECT ON TO `u`@`%`"}, stUnknown, []string{ReasonUnparsedGrant}},
		{
			// A column name cannot end the privilege list early.
			"column names with keywords", []string{"GRANT SELECT (`a ON *.* TO`, `b`), INSERT (c) ON `app`.`t` TO `u`@`%`"},
			stNot, nil,
		},
		{
			"routine level", []string{"GRANT EXECUTE, ALTER ROUTINE ON PROCEDURE `app`.`p` TO `u`@`%`"},
			stNot, nil,
		},
		{"empty", nil, stNot, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := ParseMySQLGrants(c.lines)
			if got.Status != c.status || !slices.Equal(got.Reasons, c.reasons) {
				t.Fatalf("got %v %q, want %v %q", got.Status, got.Reasons, c.status, c.reasons)
			}
		})
	}
}

func TestPrivilegeReasonsBounded(t *testing.T) {
	lines := make([]string, 0, 100)
	for i := range 100 {
		lines = append(lines, "GRANT PRIV_"+strings.Repeat("X", i)+" ON *.* TO `u`@`%`")
	}
	got := ParseMySQLGrants(lines)
	if got.Status != stPriv || len(got.Reasons) != MaxPrivilegeReasons {
		t.Fatalf("got %v with %d reasons", got.Status, len(got.Reasons))
	}
	for _, r := range got.Reasons {
		if len(r) > maxReasonBytes {
			t.Fatalf("reason too long: %q", r)
		}
	}
	if got := ParseMySQLGrants([]string{"GRANT \"x\" ON *.* TO u"}); got.Status != stUnknown {
		t.Fatalf("quoted privilege: %v", got)
	}
	if got := ParseMySQLGrants([]string{"GRANT BAD-NAME ON *.* TO `u`@`%`"}); !slices.Equal(got.Reasons, []string{"global:OTHER"}) {
		t.Fatalf("odd name: %q", got.Reasons)
	}
}

func TestLikeMatches(t *testing.T) {
	for _, c := range []struct {
		p    string
		want bool
	}{
		{"mysql", true}, {"MySQL", true}, {"%", true}, {"m%", true}, {"%l", true}, {"my_ql", true},
		{"my\\_ql", false}, {"mysq", false}, {"mysqlx", false}, {"%x%", false}, {"_____", true}, {"____", false},
	} {
		if got := likeMatches(c.p, "mysql"); got != c.want {
			t.Errorf("%q: got %v", c.p, got)
		}
	}
}

func FuzzParseMySQLGrants(f *testing.F) {
	for _, s := range []string{
		"GRANT ALL PRIVILEGES ON *.* TO 'root'@'localhost' WITH GRANT OPTION",
		"GRANT `r`@`%` TO `u`@`%`",
		"GRANT SELECT (`a`), INSERT ON `mysql`.`user` TO `u`@`%`",
		"SET DEFAULT ROLE `dba` FOR `u`@`%`",
		"GRANT SELECT ON `a\\`b`.* TO 'u\\'x'@'%'",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		got := ParseMySQLGrants([]string{line})
		if got.Status == runnerpb.PrivilegeStatus_PRIVILEGE_STATUS_UNSPECIFIED || len(got.Reasons) > MaxPrivilegeReasons {
			t.Fatalf("bad verdict %v", got)
		}
		if got.Status == stNot && len(got.Reasons) != 0 {
			t.Fatalf("reasons without privilege: %q", got.Reasons)
		}
		for _, r := range got.Reasons {
			if len(r) > maxReasonBytes {
				t.Fatalf("reason too long: %q", r)
			}
		}
	})
}
