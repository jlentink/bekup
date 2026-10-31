package db

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/config"
)

// RolePlan is the DDL that creates bekup's roles and database on an
// external PostgreSQL 15+ (D-207). `bekup db roles-sql` prints it as a psql
// script for a DBA and `bekup db bootstrap` runs the statements over its
// admin connection, so both produce the same state. Utility statements
// cannot take bind parameters: this is the only place in bekup that builds
// SQL text, from constants, a database name accepted by
// config.ValidDBName (quoted with pgx.Identifier) and SCRAM verifiers in
// the exact format SCRAMVerifier produces.
type RolePlan struct {
	// Database is bekup's database name.
	Database string
	// SingleAccount creates bekup_owner and bekup_app as NOLOGIN roles the
	// executing account keeps (SET ROLE), instead of two login roles.
	SingleAccount bool
	// CreateOwner / CreateApp create the roles.
	CreateOwner, CreateApp bool
	// SetOwnerPassword / SetAppPassword set the password of an existing
	// login role (bootstrap --reset-passwords).
	SetOwnerPassword, SetAppPassword bool
	// OwnerVerifier / AppVerifier are the SCRAM verifiers of the login
	// roles (two roles; empty with PromptPasswords).
	OwnerVerifier, AppVerifier string
	// PromptPasswords (roles-sql only) leaves the passwords out and adds
	// psql \password prompts instead.
	PromptPasswords bool
	// GrantOwner grants bekup_owner to the executing account before
	// CREATE DATABASE ... OWNER (PostgreSQL needs the membership); with
	// two roles RevokeOwner takes it away again at the end.
	GrantOwner, RevokeOwner bool
	// GrantApp (single-account mode) grants bekup_app to the executing
	// account, which keeps it. Bootstrap leaves it (and GrantOwner) out
	// when the account already holds the role.
	GrantApp bool
	// CreateDatabase creates the database (UTF8, template0).
	CreateDatabase bool
	// CreateExtension creates citext in the database as the executing
	// account (for servers where citext is not a trusted extension).
	CreateExtension bool
	// GrantWithOptions (PostgreSQL 16+) grants the roles WITH INHERIT
	// TRUE, SET TRUE, independent of the executing account's INHERIT
	// attribute and of createrole_self_grant.
	GrantWithOptions bool
}

// Statement is one step of a RolePlan.
type Statement struct {
	// SQL is the statement (or a psql meta-command with PSQLOnly).
	SQL string
	// Action describes the step without secrets (bootstrap's report).
	Action string
	// InDatabase: run while connected to bekup's database (otherwise on
	// the admin connection's database).
	InDatabase bool
	// Sensitive: SQL contains a password verifier; never log it.
	Sensitive bool
	// PSQLOnly: a psql meta-command, only part of the printed script.
	PSQLOnly bool
	// TempOwnerGrant: the GRANT of bekup_owner to the executing account
	// with two roles, which RevokeOwner (bootstrap: its revoke step) takes
	// away again.
	TempOwnerGrant bool
}

// NewRolesSQLPlan is the plan of `bekup db roles-sql`: create both login
// roles and the database on a fresh server.
func NewRolesSQLPlan(database, ownerVerifier, appVerifier string, prompt, createExtension bool) RolePlan {
	return RolePlan{
		Database: database, CreateOwner: true, CreateApp: true,
		OwnerVerifier: ownerVerifier, AppVerifier: appVerifier, PromptPasswords: prompt,
		GrantOwner: true, RevokeOwner: true, CreateDatabase: true, CreateExtension: createExtension,
	}
}

// NewSingleAccountPlan is the plan of single-account mode: NOLOGIN roles
// the executing account is granted and keeps.
func NewSingleAccountPlan(database string) RolePlan {
	return RolePlan{Database: database, SingleAccount: true, CreateOwner: true, CreateApp: true, GrantOwner: true, GrantApp: true, CreateDatabase: true}
}

// Role attributes. bekup_owner INHERITs so that, as the database owner, it
// gets the privileges of pg_database_owner, which owns the public schema of
// a new database: PostgreSQL 15 applies NOINHERIT to that implicit
// membership too, and only a superuser could hand the schema over (the
// bundled init script does). INHERIT adds nothing a membership would not
// allow through SET ROLE anyway, and the D-099 checks refuse every
// dangerous membership of bekup_owner. bekup_app stays NOINHERIT.
const (
	ownerLoginAttrs   = "LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS INHERIT"
	appLoginAttrs     = "LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS NOINHERIT"
	ownerNologinAttrs = "NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS INHERIT"
	appNologinAttrs   = "NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS NOINHERIT"
	appConnLimit      = " CONNECTION LIMIT 100"
)

// Statements returns the plan's statements in execution order.
func (p RolePlan) Statements() ([]Statement, error) {
	if !config.ValidDBName(p.Database) {
		return nil, fmt.Errorf("invalid database name %q", p.Database)
	}
	if p.SingleAccount && (p.SetOwnerPassword || p.SetAppPassword || p.PromptPasswords || p.RevokeOwner) {
		return nil, errors.New("single-account roles have no passwords and keep their membership")
	}
	if p.GrantApp && !p.SingleAccount {
		return nil, errors.New("only single-account mode grants bekup_app to the executing account")
	}
	pw := func(v string) (string, error) {
		if p.PromptPasswords || p.SingleAccount {
			return "", nil
		}
		if !validSCRAMVerifier(v) {
			return "", errors.New("role password verifier missing or malformed")
		}
		return " PASSWORD '" + v + "'", nil
	}
	dbName := pgx.Identifier{p.Database}.Sanitize()
	var out []Statement
	add := func(st Statement) { out = append(out, st) }

	for _, r := range []struct {
		role, verifier string
		create, set    bool
		attrs, nologin string
		limit          string
	}{
		{OwnerRole, p.OwnerVerifier, p.CreateOwner, p.SetOwnerPassword, ownerLoginAttrs, ownerNologinAttrs, ""},
		{AppRole, p.AppVerifier, p.CreateApp, p.SetAppPassword, appLoginAttrs, appNologinAttrs, appConnLimit},
	} {
		if r.create {
			clause, err := pw(r.verifier)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", r.role, err)
			}
			attrs := r.attrs
			if p.SingleAccount {
				attrs, r.limit = r.nologin, ""
			}
			add(Statement{
				SQL:    "CREATE ROLE " + r.role + " WITH " + attrs + r.limit + clause + ";",
				Action: "create role " + r.role, Sensitive: clause != "",
			})
			if p.PromptPasswords {
				add(Statement{SQL: `\echo 'Enter the password of ` + r.role + ` twice (the content of its password file):'` + "\n" + `\password ` + r.role, PSQLOnly: true})
			}
			continue
		}
		if r.set {
			clause, err := pw(r.verifier)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", r.role, err)
			}
			add(Statement{SQL: "ALTER ROLE " + r.role + " WITH" + clause + ";", Action: "set the password of role " + r.role, Sensitive: true})
		}
	}
	grantOpts := ""
	if p.GrantWithOptions {
		grantOpts = " WITH INHERIT TRUE, SET TRUE"
	}
	if p.GrantOwner {
		add(Statement{
			SQL: "GRANT " + OwnerRole + " TO CURRENT_USER" + grantOpts + ";", Action: "grant " + OwnerRole + " to the executing account",
			TempOwnerGrant: !p.SingleAccount,
		})
	}
	if p.GrantApp {
		add(Statement{SQL: "GRANT " + AppRole + " TO CURRENT_USER" + grantOpts + ";", Action: "grant " + AppRole + " to the executing account"})
	}
	if p.CreateDatabase {
		add(Statement{
			SQL:    "CREATE DATABASE " + dbName + " WITH OWNER " + OwnerRole + " ENCODING 'UTF8' TEMPLATE template0;",
			Action: "create database " + p.Database + " owned by " + OwnerRole,
		})
	}
	add(Statement{SQL: "REVOKE ALL ON DATABASE " + dbName + " FROM PUBLIC;", Action: "revoke the PUBLIC privileges on database " + p.Database})
	add(Statement{SQL: "GRANT CONNECT ON DATABASE " + dbName + " TO " + AppRole + ";", Action: "grant CONNECT on database " + p.Database + " to " + AppRole})
	if p.SingleAccount {
		add(Statement{SQL: "GRANT CONNECT ON DATABASE " + dbName + " TO CURRENT_USER;", Action: "grant CONNECT on database " + p.Database + " to the executing account"})
	}
	if p.CreateExtension {
		add(citextStatement(p.Database))
	}
	if p.RevokeOwner && !p.SingleAccount {
		add(revokeOwnerStatement())
	}
	return out, nil
}

// citextStatement creates citext (migration 00004 needs it) in database.
func citextStatement(database string) Statement {
	return Statement{SQL: "CREATE EXTENSION IF NOT EXISTS citext;", Action: "create the citext extension in database " + database, InDatabase: true}
}

// revokeOwnerStatement takes the temporary bekup_owner membership away from
// the executing account again (two roles).
func revokeOwnerStatement() Statement {
	return Statement{SQL: "REVOKE " + OwnerRole + " FROM CURRENT_USER;", Action: "revoke " + OwnerRole + " from the executing account again"}
}

// RolesSQLScript renders the plan as a psql script (`bekup db roles-sql`).
func RolesSQLScript(p RolePlan) (string, error) {
	stmts, err := p.Statements()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(`-- bekup: least-privilege roles and database for an external PostgreSQL 15+
-- (generated by "bekup db roles-sql"; D-207, docs/installation/external-database.md).
--
-- Run it once as an account with CREATEROLE and CREATEDB (or the provider's
-- admin role: rds_superuser, cloudsqlsuperuser, azure_pg_admin), connected
-- to the maintenance database over TLS, for example:
--   psql -v ON_ERROR_STOP=1 --no-psqlrc -f bekup-roles.sql \
--     "host=<host> dbname=postgres user=<admin> sslmode=verify-full sslrootcert=<ca.pem>"
--
`)
	if !p.PromptPasswords {
		b.WriteString(`-- SENSITIVE: the PASSWORD values are SCRAM-SHA-256 verifiers of bekup's
-- role passwords, never the passwords themselves. A verifier still allows an
-- offline guessing attack: keep this file private and delete it after use.
-- A server that logs DDL (log_statement = ddl/all, pgaudit) records them.
--
`)
	}
	if p.RevokeOwner && p.GrantOwner && !p.SingleAccount {
		b.WriteString(`-- The script grants bekup_owner to the executing account only for its
-- duration (the last statement revokes it). If it stops with an error after
-- that GRANT, revoke the membership by hand as the same account:
--   REVOKE bekup_owner FROM CURRENT_USER;
--
`)
	}
	b.WriteString("\\set ON_ERROR_STOP on\n")
	if p.PromptPasswords {
		// \password hashes with the session's password_encryption.
		b.WriteString("SET password_encryption = 'scram-sha-256';\n")
	}
	b.WriteString("\n")
	inDB := false
	for _, st := range stmts {
		if st.InDatabase && !inDB {
			b.WriteString("\\connect " + pgx.Identifier{p.Database}.Sanitize() + "\n")
			inDB = true
		}
		b.WriteString(st.SQL)
		b.WriteString("\n")
	}
	b.WriteString("\n-- Next: put the role passwords into database.password_file and\n" +
		"-- database.owner_password_file, then run: bekup migrate check && bekup migrate up\n")
	return b.String(), nil
}
