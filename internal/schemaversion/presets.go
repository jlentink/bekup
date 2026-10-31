package schemaversion

import (
	"slices"
	"strconv"
	"strings"

	"github.com/jlentink/bekup/internal/engine"
)

// TablePlaceholder stands for the quoted, qualified framework table in a
// preset statement (PostgreSQL: "schema"."table"; MySQL/MariaDB:
// `database`.`table`). The runner substitutes it with names quoted by
// engine.QuotePGObject / engine.QuoteMySQLObject.
const TablePlaceholder = "{{table}}"

// Query is a preset's statement for one engine family.
type Query struct {
	// Table is the table as the framework creates it on this engine
	// (exact case; MySQL/MariaDB match it case-insensitively when the
	// exact name does not exist).
	Table string
	// Columns are the columns the statement reads. The runner checks
	// before running it that the table is a plain table and that each of
	// them exists with a built-in scalar type (TypeAllowed*), so no code
	// of the database owner can run (casts, output functions, views).
	Columns []string
	// SQL is the statement with TablePlaceholder for the table. It is
	// constant, reviewed SQL: on PostgreSQL every function, operator and
	// collation is schema-qualified (D-152), on MySQL/MariaDB only
	// built-ins are used. It reads at most one row.
	SQL string
}

// Statement returns the statement for the quoted, qualified table.
func (q Query) Statement(quotedTable string) string {
	return strings.ReplaceAll(q.SQL, TablePlaceholder, quotedTable)
}

// Preset is a fixed version query for one migration framework. Jobs store
// only its Name.
type Preset struct {
	// Name is the stable identifier stored on the job.
	Name string
	// Label is the framework's display name.
	Label string
	// Description says what the captured version is.
	Description string
	// PG is the PostgreSQL statement, MySQL the MySQL/MariaDB one.
	PG    Query
	MySQL Query
	// format turns the row into the text (nil: the first column).
	format func(cols []*string) (string, bool)
}

// QueryFor returns the statement for an engine.
func (p *Preset) QueryFor(k engine.Kind) Query {
	if k == engine.KindPostgres {
		return p.PG
	}
	return p.MySQL
}

// Format turns the first row's columns (nil = NULL) into the version text;
// false means "no version" (NULL). The text is checked by CleanValue.
func (p *Preset) Format(cols []*string) (string, bool) {
	if p.format != nil {
		return p.format(cols)
	}
	return firstColumn(cols)
}

// Engines lists the engines a preset supports (every preset supports all).
func (p *Preset) Engines() []engine.Kind {
	return []engine.Kind{engine.KindPostgres, engine.KindMySQL, engine.KindMariaDB}
}

func firstColumn(cols []*string) (string, bool) {
	if len(cols) == 0 || cols[0] == nil {
		return "", false
	}
	return *cols[0], true
}

// formatMigrate is golang-migrate: "42" or "42 (dirty)".
func formatMigrate(cols []*string) (string, bool) {
	v, ok := firstColumn(cols)
	if !ok {
		return "", false
	}
	if len(cols) > 1 && cols[1] != nil && truthy(*cols[1]) {
		return v + " (dirty)", true
	}
	return v, true
}

// formatDjango is "app.migration".
func formatDjango(cols []*string) (string, bool) {
	if len(cols) < 2 || cols[0] == nil || cols[1] == nil {
		return "", false
	}
	return *cols[0] + "." + *cols[1], true
}

// truthy reads a boolean in the text format of PostgreSQL ("t") or
// MySQL/MariaDB ("1").
func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "t", "true", "1", "y", "yes", "on":
		return true
	}
	return false
}

// PostgreSQL helpers (D-152: qualified operators and collations).
const (
	pgEq    = ` OPERATOR(pg_catalog.=) `
	pgGt    = ` OPERATOR(pg_catalog.>) `
	pgNe    = ` OPERATOR(pg_catalog.<>) `
	pgCColl = ` COLLATE pg_catalog."C"`
)

// MaxRawValueBytes bounds a value column as the runner receives it: the
// presets truncate their text output in SQL to MaxRawValueBytes+1
// characters (pgText, myText), so the server never sends a long value and
// the runner can still tell that it was too long (more than
// MaxRawValueBytes bytes received: invalid).
const MaxRawValueBytes = 256

// rawLimit is the SQL truncation length (one more than accepted).
var rawLimit = strconv.Itoa(MaxRawValueBytes + 1)

// pgText is a PostgreSQL output column truncated in SQL (expr is a quoted
// column or a built-in aggregate of one).
func pgText(expr string) string {
	return `pg_catalog.left(` + expr + `::pg_catalog.text, ` + rawLimit + `)`
}

// myText is the MySQL/MariaDB counterpart of pgText.
func myText(expr string) string {
	return "LEFT(CAST(" + expr + " AS CHAR), " + rawLimit + ")"
}

// presets is the reviewed catalog. Keep docs/guides/jobs-and-schedules.md
// ("Schema version") in sync. Every text output column goes through
// pgText / myText (bounded result).
var presets = []*Preset{
	{
		Name: "golang-migrate", Label: "golang-migrate",
		Description: "The version in schema_migrations, marked (dirty) after a failed migration.",
		PG: Query{Table: "schema_migrations", Columns: []string{"version", "dirty"},
			SQL: `SELECT ` + pgText(`"version"`) + `, "dirty" FROM ` + TablePlaceholder + ` LIMIT 1`},
		MySQL: Query{Table: "schema_migrations", Columns: []string{"version", "dirty"},
			SQL: "SELECT " + myText("`version`") + ", `dirty` FROM " + TablePlaceholder + " LIMIT 1"},
		format: formatMigrate,
	},
	{
		Name: "goose", Label: "goose",
		Description: "The newest applied version_id in goose_db_version (rolled-back versions skipped).",
		PG: Query{Table: "goose_db_version", Columns: []string{"id", "version_id", "is_applied"},
			SQL: `SELECT ` + pgText(`g."version_id"`) + ` FROM ` + TablePlaceholder + ` g WHERE g."is_applied" AND NOT EXISTS (SELECT 1 FROM ` +
				TablePlaceholder + ` h WHERE h."version_id"` + pgEq + `g."version_id" AND h."id"` + pgGt + `g."id" AND NOT h."is_applied") ` +
				`ORDER BY g."id" DESC LIMIT 1`},
		MySQL: Query{Table: "goose_db_version", Columns: []string{"id", "version_id", "is_applied"},
			SQL: "SELECT " + myText("g.`version_id`") + " FROM " + TablePlaceholder + " g WHERE g.`is_applied` AND NOT EXISTS (SELECT 1 FROM " +
				TablePlaceholder + " h WHERE h.`version_id` = g.`version_id` AND h.`id` > g.`id` AND NOT h.`is_applied`) " +
				"ORDER BY g.`id` DESC LIMIT 1"},
	},
	{
		// Undo migrations (Flyway Teams: type UNDO_SQL, UNDO_JDBC, ...)
		// are no version of their own, and a versioned migration undone
		// by a later successful undo no longer counts.
		Name: "flyway", Label: "Flyway",
		Description: "The version of the newest successful versioned migration in flyway_schema_history that was not undone.",
		PG: Query{Table: "flyway_schema_history", Columns: []string{"installed_rank", "version", "type", "success"},
			SQL: `SELECT ` + pgText(`h."version"`) + ` FROM ` + TablePlaceholder + ` h WHERE h."success" AND h."version" IS NOT NULL AND h."type"` + pgNe +
				`'DELETE' AND pg_catalog.left(h."type"::pg_catalog.text, 5)` + pgNe + `'UNDO_' AND NOT EXISTS (SELECT 1 FROM ` + TablePlaceholder +
				` u WHERE u."success" AND pg_catalog.left(u."type"::pg_catalog.text, 5)` + pgEq + `'UNDO_' AND u."version"` + pgEq +
				`h."version" AND u."installed_rank"` + pgGt + `h."installed_rank") ORDER BY h."installed_rank" DESC LIMIT 1`},
		MySQL: Query{Table: "flyway_schema_history", Columns: []string{"installed_rank", "version", "type", "success"},
			SQL: "SELECT " + myText("h.`version`") + " FROM " + TablePlaceholder + " h WHERE h.`success` AND h.`version` IS NOT NULL AND h.`type` <> 'DELETE' " +
				"AND LEFT(h.`type`, 5) <> 'UNDO_' AND NOT EXISTS (SELECT 1 FROM " + TablePlaceholder + " u WHERE u.`success` " +
				"AND LEFT(u.`type`, 5) = 'UNDO_' AND u.`version` = h.`version` AND u.`installed_rank` > h.`installed_rank`) " +
				"ORDER BY h.`installed_rank` DESC LIMIT 1"},
	},
	{
		// RERAN rows (runAlways / runOnChange changesets run again) are
		// skipped: Liquibase moves their date and order to the re-run, so
		// a re-run housekeeping changeset would otherwise look like the
		// newest migration.
		Name: "liquibase", Label: "Liquibase",
		Description: "The id of the newest changeset executed for the first time in databasechangelog (EXECUTED or MARK_RAN; re-runs are skipped).",
		PG: Query{Table: "databasechangelog", Columns: []string{"id", "dateexecuted", "orderexecuted", "exectype"},
			SQL: `SELECT ` + pgText(`"id"`) + ` FROM ` + TablePlaceholder + ` WHERE "exectype"` + pgEq +
				`ANY ('{EXECUTED,MARK_RAN}'::pg_catalog.text[]) ORDER BY "dateexecuted" DESC, "orderexecuted" DESC LIMIT 1`},
		MySQL: Query{Table: "DATABASECHANGELOG", Columns: []string{"ID", "DATEEXECUTED", "ORDEREXECUTED", "EXECTYPE"},
			SQL: "SELECT " + myText("`ID`") + " FROM " + TablePlaceholder + " WHERE `EXECTYPE` IN ('EXECUTED', 'MARK_RAN') " +
				"ORDER BY `DATEEXECUTED` DESC, `ORDEREXECUTED` DESC LIMIT 1"},
	},
	{
		Name: "rails", Label: "Ruby on Rails (Active Record)",
		Description: "The highest version in schema_migrations.",
		PG: Query{Table: "schema_migrations", Columns: []string{"version"},
			SQL: `SELECT ` + pgText(`"version"`) + ` FROM ` + TablePlaceholder + ` ORDER BY pg_catalog.length("version") DESC, "version"` + pgCColl +
				` DESC LIMIT 1`},
		MySQL: Query{Table: "schema_migrations", Columns: []string{"version"},
			SQL: "SELECT " + myText("`version`") + " FROM " + TablePlaceholder + " ORDER BY CHAR_LENGTH(`version`) DESC, `version` DESC LIMIT 1"},
	},
	{
		Name: "django", Label: "Django",
		Description: "The newest applied migration in django_migrations, as app.name.",
		PG: Query{Table: "django_migrations", Columns: []string{"id", "app", "name", "applied"},
			SQL: `SELECT ` + pgText(`"app"`) + `, ` + pgText(`"name"`) + ` FROM ` + TablePlaceholder + ` ORDER BY "applied" DESC, "id" DESC LIMIT 1`},
		MySQL: Query{Table: "django_migrations", Columns: []string{"id", "app", "name", "applied"},
			SQL: "SELECT " + myText("`app`") + ", " + myText("`name`") + " FROM " + TablePlaceholder + " ORDER BY `applied` DESC, `id` DESC LIMIT 1"},
		format: formatDjango,
	},
	{
		Name: "laravel", Label: "Laravel",
		Description: "The newest migration of the newest batch in migrations.",
		PG: Query{Table: "migrations", Columns: []string{"id", "migration", "batch"},
			SQL: `SELECT ` + pgText(`"migration"`) + ` FROM ` + TablePlaceholder + ` ORDER BY "batch" DESC, "id" DESC LIMIT 1`},
		MySQL: Query{Table: "migrations", Columns: []string{"id", "migration", "batch"},
			SQL: "SELECT " + myText("`migration`") + " FROM " + TablePlaceholder + " ORDER BY `batch` DESC, `id` DESC LIMIT 1"},
	},
	{
		Name: "alembic", Label: "Alembic (SQLAlchemy)",
		Description: "The revision in alembic_version (several heads joined with commas).",
		PG: Query{Table: "alembic_version", Columns: []string{"version_num"},
			SQL: `SELECT ` + pgText(`pg_catalog.string_agg("version_num", ',' ORDER BY "version_num"`+pgCColl+`)`) + ` FROM ` + TablePlaceholder},
		MySQL: Query{Table: "alembic_version", Columns: []string{"version_num"},
			SQL: "SELECT " + myText("GROUP_CONCAT(`version_num` ORDER BY `version_num` SEPARATOR ',')") + " FROM " + TablePlaceholder},
	},
	{
		Name: "prisma", Label: "Prisma Migrate",
		Description: "The newest finished, not rolled-back migration in _prisma_migrations.",
		PG: Query{Table: "_prisma_migrations", Columns: []string{"migration_name", "finished_at", "rolled_back_at"},
			SQL: `SELECT ` + pgText(`"migration_name"`) + ` FROM ` + TablePlaceholder + ` WHERE "finished_at" IS NOT NULL AND "rolled_back_at" IS NULL ` +
				`ORDER BY "finished_at" DESC, "migration_name"` + pgCColl + ` DESC LIMIT 1`},
		MySQL: Query{Table: "_prisma_migrations", Columns: []string{"migration_name", "finished_at", "rolled_back_at"},
			SQL: "SELECT " + myText("`migration_name`") + " FROM " + TablePlaceholder + " WHERE `finished_at` IS NOT NULL AND `rolled_back_at` IS NULL " +
				"ORDER BY `finished_at` DESC, `migration_name` DESC LIMIT 1"},
	},
	{
		Name: "knex", Label: "Knex.js",
		Description: "The newest migration in knex_migrations.",
		PG: Query{Table: "knex_migrations", Columns: []string{"id", "name"},
			SQL: `SELECT ` + pgText(`"name"`) + ` FROM ` + TablePlaceholder + ` ORDER BY "id" DESC LIMIT 1`},
		MySQL: Query{Table: "knex_migrations", Columns: []string{"id", "name"},
			SQL: "SELECT " + myText("`name`") + " FROM " + TablePlaceholder + " ORDER BY `id` DESC LIMIT 1"},
	},
	{
		Name: "sequelize", Label: "Sequelize",
		Description: "The highest migration name in SequelizeMeta.",
		PG: Query{Table: "SequelizeMeta", Columns: []string{"name"},
			SQL: `SELECT ` + pgText(`"name"`) + ` FROM ` + TablePlaceholder + ` ORDER BY "name"` + pgCColl + ` DESC LIMIT 1`},
		MySQL: Query{Table: "SequelizeMeta", Columns: []string{"name"},
			SQL: "SELECT " + myText("`name`") + " FROM " + TablePlaceholder + " ORDER BY `name` DESC LIMIT 1"},
	},
}

// Presets returns the presets in display order.
func Presets() []*Preset { return slices.Clone(presets) }

// Lookup returns the preset with the given name.
func Lookup(name string) (*Preset, bool) {
	for _, p := range presets {
		if p.Name == name {
			return p, true
		}
	}
	return nil, false
}

// TypeAllowedPG reports whether a column type (pg_catalog type name) may
// be read by a preset: built-in scalar types whose output and comparison
// are core code.
var TypeAllowedPG = []string{"int2", "int4", "int8", "numeric", "text", "varchar", "bpchar", "bool", "timestamp", "timestamptz", "date"}

// TypeAllowedMySQL is the same for information_schema.COLUMNS.DATA_TYPE.
var TypeAllowedMySQL = []string{
	"tinyint", "smallint", "mediumint", "int", "bigint", "decimal", "char", "varchar",
	"tinytext", "text", "mediumtext", "longtext", "datetime", "timestamp", "date",
}
