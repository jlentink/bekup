package db

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/logging"
)

// BootstrapOptions configure Bootstrap (`bekup db bootstrap`, D-207).
type BootstrapOptions struct {
	// Admin is the fields-mode login of the administrator account (its
	// Database is the maintenance database, usually postgres). In
	// single-account mode it is the single account itself.
	Admin config.DBCredentials
	// TLS is the policy of the admin connection (the configured one).
	TLS TLSOptions
	// Database is bekup's database name.
	Database string
	// SingleAccount creates NOLOGIN roles the admin account keeps.
	SingleAccount bool
	// OwnerPassword / AppPassword are the passwords of the two login roles
	// (two roles only; turned into SCRAM verifiers here, never sent; the
	// caller zeroes them).
	OwnerPassword, AppPassword []byte
	// ResetPasswords sets the passwords of existing login roles too.
	ResetPasswords bool
	// AdoptExistingRoles accepts bekup_owner/bekup_app roles that already
	// exist (created by an earlier bootstrap, a DBA or someone else): they
	// are checked, never changed silently. Without it bootstrap refuses
	// existing roles, so a role somebody else prepared is never taken over
	// unnoticed.
	AdoptExistingRoles bool
	// DryRun only reports what would be done.
	DryRun bool
	// ConnectTimeout bounds each connection attempt (0: 10s).
	ConnectTimeout time.Duration
	// Registry receives the passwords and verifiers for log redaction (nil:
	// logging.DefaultRegistry).
	Registry *logging.Registry
}

// BootstrapResult reports what Bootstrap did, without secrets.
type BootstrapResult struct {
	ServerVersion string
	// AdminRole is the role name the admin login acts as.
	AdminRole string
	// Actions lists the executed (or, with DryRun, planned) steps.
	Actions  []string
	Warnings []string
	// Citext is the extension state after the run (a Citext* constant).
	Citext string
	TLS    TLSFacts
	// Changed reports whether a role, a password, the database or citext
	// was created or set (re-applied grants do not count).
	Changed bool
	// ManualRevoke is set when the temporary bekup_owner membership of the
	// admin could not be revoked: the statement an administrator must run.
	ManualRevoke string
}

// revokeTimeout bounds the revoke of the temporary bekup_owner membership,
// which runs even after the command was interrupted.
const revokeTimeout = 15 * time.Second

// Bootstrap creates or verifies bekup's roles and database with an
// administrator account (D-207): the roles with SCRAM verifiers of the
// configured passwords (two roles) or as NOLOGIN roles the account keeps
// (single-account mode), the database owned by bekup_owner with PUBLIC
// revoked and CONNECT for bekup_app, and citext when bekup_owner could not
// create it itself. It is idempotent once AdoptExistingRoles accepts the
// existing roles: they are checked (never silently changed; ResetPasswords
// sets their passwords; other members or ADMIN OPTION holders than the
// admin are refused), an existing database must already be owned by
// bekup_owner. The admin password is
// only used for this connection and never stored; nothing is logged here.
// Run the D-099 role checks and the migrate preflight afterwards.
func Bootstrap(ctx context.Context, o BootstrapOptions) (_ *BootstrapResult, err error) {
	reg := o.Registry
	if reg == nil {
		reg = logging.DefaultRegistry
	}
	if err := o.validate(); err != nil {
		return nil, err
	}
	timeout := o.ConnectTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	conn, err := bootstrapConnect(ctx, o.Admin, o.TLS, reg, timeout)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, conn.Close(context.WithoutCancel(ctx))) }()

	q := sqlc.New(conn)
	res := &BootstrapResult{TLS: ConnTLSFacts(conn.PgConn(), o.TLS)}
	pl, err := o.plan(ctx, q, res)
	for _, v := range []string{pl.plan.OwnerVerifier, pl.plan.AppVerifier} {
		if v != "" {
			reg.Register([]byte(v))
		}
	}
	if err != nil {
		return res, err
	}
	stmts, err := pl.plan.Statements()
	if err != nil {
		return res, err
	}
	for _, st := range stmts {
		if st.Sensitive {
			reg.Register([]byte(st.SQL))
		}
	}
	if o.DryRun {
		for _, st := range stmts {
			res.Actions = append(res.Actions, st.Action)
		}
		res.Actions = append(res.Actions, "create citext in the database as the admin if bekup_owner cannot (checked after the database exists)")
		if pl.revoke {
			res.Actions = append(res.Actions, revokeOwnerStatement().Action)
		}
		return res, nil
	}
	granted := false
	revoke := func() error {
		if !pl.revoke || !granted {
			return nil
		}
		return o.revokeTempGrant(ctx, conn, reg, timeout, res)
	}
	pp := pl.plan
	res.Changed = pp.CreateOwner || pp.CreateApp || pp.SetOwnerPassword || pp.SetAppPassword || pp.CreateDatabase
	for _, st := range stmts {
		if err := execStatement(ctx, conn, st); err != nil {
			return res, errors.Join(err, revoke())
		}
		if st.TempOwnerGrant {
			granted = true
		}
		res.Actions = append(res.Actions, st.Action)
	}
	// citext: in bekup's database, while the admin still holds bekup_owner
	// (CONNECT is revoked from PUBLIC).
	if err := o.ensureCitext(ctx, reg, timeout, res); err != nil {
		return res, errors.Join(err, revoke())
	}
	return res, revoke()
}

// revokeTempGrant takes the temporary bekup_owner membership away from the
// admin again (D-207). It runs on a context that survives an interrupted
// command (bounded by revokeTimeout) and reconnects when the connection
// died; when that fails too, res.ManualRevoke names the statement an
// administrator must run.
func (o *BootstrapOptions) revokeTempGrant(ctx context.Context, conn *pgx.Conn, reg *logging.Registry, timeout time.Duration, res *BootstrapResult) error {
	st := revokeOwnerStatement()
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), revokeTimeout)
	defer cancel()
	err := execStatement(rctx, conn, st)
	if err != nil && conn.IsClosed() {
		c2, cerr := bootstrapConnect(rctx, o.Admin, o.TLS, reg, min(timeout, revokeTimeout))
		if cerr != nil {
			err = errors.Join(err, cerr)
		} else {
			if err2 := execStatement(rctx, c2, st); err2 != nil {
				err = errors.Join(err, err2)
			} else {
				err = nil
			}
			// A close error after a successful revoke changes nothing.
			if cerr := c2.Close(rctx); cerr != nil && err != nil {
				err = errors.Join(err, cerr)
			}
		}
	}
	if err != nil {
		res.ManualRevoke = "REVOKE " + OwnerRole + " FROM " + pgx.Identifier{res.AdminRole}.Sanitize() + ";"
		return fmt.Errorf("the temporary %s membership of the admin account could not be revoked (%w); manual REVOKE needed, run as an administrator: %s",
			OwnerRole, err, res.ManualRevoke)
	}
	res.Actions = append(res.Actions, st.Action)
	return nil
}

func (o *BootstrapOptions) validate() error {
	if !o.Admin.FieldsMode() || o.Admin.Host == "" || o.Admin.User == "" {
		return errors.New("bootstrap needs the administrator's host and user (fields mode)")
	}
	if !config.ValidDBName(o.Database) {
		return fmt.Errorf("invalid database name %q", o.Database)
	}
	if o.SingleAccount {
		if len(o.OwnerPassword) > 0 || len(o.AppPassword) > 0 || o.ResetPasswords {
			return errors.New("single-account mode creates NOLOGIN roles without passwords")
		}
		return nil
	}
	for _, p := range []struct {
		name string
		pw   []byte
	}{{"database.owner_password_file", o.OwnerPassword}, {"database.password_file", o.AppPassword}} {
		if err := CheckRolePassword(p.pw); err != nil {
			return fmt.Errorf("%s: %w", p.name, err)
		}
	}
	if subtle.ConstantTimeCompare(o.OwnerPassword, o.AppPassword) == 1 {
		return errors.New("bekup_owner and bekup_app need different passwords (database.owner_password_file and database.password_file hold the same value)")
	}
	return nil
}

func bootstrapConnect(ctx context.Context, creds config.DBCredentials, o TLSOptions, reg *logging.Registry, timeout time.Duration) (*pgx.Conn, error) {
	cc, err := connConfig(creds, o, reg)
	if err != nil {
		return nil, err
	}
	cc.RuntimeParams["application_name"] = "bekup-bootstrap"
	cc.ConnectTimeout = timeout
	conn, err := pgx.ConnectConfig(ctx, cc)
	if err != nil {
		return nil, fmt.Errorf("connect to database %s as %s: %w", creds.Database, creds.User, err)
	}
	return conn, nil
}

type bootstrapPlan struct {
	plan   RolePlan
	revoke bool // REVOKE bekup_owner FROM CURRENT_USER at the end
}

// plan inspects the server and decides the statements.
func (o *BootstrapOptions) plan(ctx context.Context, q *sqlc.Queries, res *BootstrapResult) (bootstrapPlan, error) {
	admin, err := q.BootstrapAdminFacts(ctx)
	if err != nil {
		return bootstrapPlan{}, fmt.Errorf("read the admin role: %w", err)
	}
	res.ServerVersion, res.AdminRole = admin.ServerVersion, admin.RoleName
	if admin.ServerVersionNum < minServerVersionNum {
		return bootstrapPlan{}, fmt.Errorf("bekup needs PostgreSQL 15 or newer (server: %s)", admin.ServerVersion)
	}
	if admin.ServerVersionNum > testedServerVersionMax {
		res.Warnings = append(res.Warnings, fmt.Sprintf("PostgreSQL %s is newer than the tested versions 15 to 17", admin.ServerVersion))
	}
	if admin.RoleName == OwnerRole || admin.RoleName == AppRole {
		return bootstrapPlan{}, fmt.Errorf("the admin account cannot be %s itself", admin.RoleName)
	}
	if o.SingleAccount && admin.IsSuperuser {
		return bootstrapPlan{}, fmt.Errorf("single-account mode refuses a SUPERUSER account (%q): use an account with CREATEROLE and CREATEDB, or the two least-privilege roles", admin.RoleName)
	}
	pg16 := admin.ServerVersionNum >= 160000

	p := RolePlan{Database: o.Database, SingleAccount: o.SingleAccount}
	needCreateRole := false
	var existing []string
	for _, r := range []struct {
		name      string
		forbidden []string
		create    *bool
		reset     *bool
		verifier  *string
		pw        []byte
	}{
		{OwnerRole, nil, &p.CreateOwner, &p.SetOwnerPassword, &p.OwnerVerifier, o.OwnerPassword},
		{AppRole, []string{OwnerRole}, &p.CreateApp, &p.SetAppPassword, &p.AppVerifier, o.AppPassword},
	} {
		facts, err := q.BootstrapRoleFacts(ctx, r.name)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			*r.create = true
			needCreateRole = true
		case err != nil:
			return bootstrapPlan{}, fmt.Errorf("read role %s: %w", r.name, err)
		default:
			existing = append(existing, r.name)
			if err := o.checkExistingRole(ctx, q, facts, r.forbidden, res); err != nil {
				return bootstrapPlan{}, err
			}
			if o.ResetPasswords {
				*r.reset = true
				needCreateRole = true
			}
		}
		if !o.SingleAccount && (*r.create || *r.reset) {
			v, err := NewSCRAMVerifier(r.pw)
			if err != nil {
				return bootstrapPlan{}, fmt.Errorf("%s: %w", r.name, err)
			}
			*r.verifier = v
		}
	}
	if len(existing) > 0 {
		if err := o.checkExistingMemberships(ctx, q, admin.RoleName, existing, res); err != nil {
			return bootstrapPlan{}, err
		}
	}

	dbFacts, err := q.BootstrapDatabaseFacts(ctx, o.Database)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		p.CreateDatabase = true
	case err != nil:
		return bootstrapPlan{}, fmt.Errorf("read database %s: %w", o.Database, err)
	default:
		if dbFacts.Owner != OwnerRole {
			return bootstrapPlan{}, fmt.Errorf("database %q exists but is owned by %q, not %s; change the owner (ALTER DATABASE %s OWNER TO %s) or choose another database.name",
				o.Database, dbFacts.Owner, OwnerRole, pgx.Identifier{o.Database}.Sanitize(), OwnerRole)
		}
		if dbFacts.Encoding != "UTF8" {
			return bootstrapPlan{}, fmt.Errorf("database %q exists with encoding %s; bekup needs UTF8", o.Database, dbFacts.Encoding)
		}
	}

	if !admin.IsSuperuser {
		var missing []string
		if needCreateRole && !admin.CreateRole {
			missing = append(missing, "CREATEROLE")
		}
		if p.CreateDatabase && !admin.CreateDb {
			missing = append(missing, "CREATEDB")
		}
		if len(missing) > 0 {
			return bootstrapPlan{}, fmt.Errorf("the admin account %q lacks %s (use the provider's admin account, or let a DBA run the script of bekup db roles-sql)",
				admin.RoleName, strings.Join(missing, " and "))
		}
		if !pg16 && !admin.Inherit {
			return bootstrapPlan{}, fmt.Errorf("the admin account %q is NOINHERIT; on PostgreSQL 15 bootstrap needs an account that inherits granted roles", admin.RoleName)
		}
	}

	// The admin needs bekup_owner's privileges for CREATE DATABASE ...
	// OWNER, the database grants and citext. A superuser has them anyway;
	// otherwise grant the role unless the admin already holds it, and (two
	// roles) take a granted membership away again at the end. In
	// single-account mode the account keeps both roles.
	bp := bootstrapPlan{}
	if !admin.IsSuperuser {
		held, err := o.needGrant(ctx, q, admin, OwnerRole, p.CreateOwner, pg16)
		if err != nil {
			return bootstrapPlan{}, err
		}
		p.GrantOwner = !held
		bp.revoke = !held && !o.SingleAccount
		if o.SingleAccount {
			heldApp, err := o.needGrant(ctx, q, admin, AppRole, p.CreateApp, pg16)
			if err != nil {
				return bootstrapPlan{}, err
			}
			p.GrantApp = !heldApp
		}
		// PostgreSQL 16+: ALTER ROLE ... PASSWORD of an existing role needs
		// ADMIN OPTION on it as well.
		for _, r := range []struct {
			role string
			set  bool
		}{{OwnerRole, p.SetOwnerPassword}, {AppRole, p.SetAppPassword}} {
			if !r.set || !pg16 {
				continue
			}
			ok, err := q.BootstrapHasAdminOption(ctx, r.role)
			if err != nil {
				return bootstrapPlan{}, fmt.Errorf("check the admin's ADMIN OPTION on %s: %w", r.role, err)
			}
			if !ok {
				return bootstrapPlan{}, adminOptionError(admin.RoleName, r.role, "set its password")
			}
		}
	}
	p.GrantWithOptions = pg16
	bp.plan = p
	return bp, nil
}

// needGrant reports whether the admin already holds role (then no GRANT is
// needed) and, when it does not and the role exists, checks that the admin
// may grant it to itself: PostgreSQL 16+ needs ADMIN OPTION on the role
// (CREATEROLE alone no longer suffices; the creator of a role gets it
// automatically), 15 accepts CREATEROLE too. A held role is not granted
// again, so a single account that dropped CREATEROLE after its first
// bootstrap can still re-run it.
func (o *BootstrapOptions) needGrant(ctx context.Context, q *sqlc.Queries, admin sqlc.BootstrapAdminFactsRow, role string, creating, pg16 bool) (held bool, err error) {
	if creating {
		return false, nil
	}
	if held, err = adminHoldsRole(ctx, q, role, pg16); err != nil || held {
		return held, err
	}
	hasAdmin, err := q.BootstrapHasAdminOption(ctx, role)
	if err != nil {
		return false, fmt.Errorf("check the admin's ADMIN OPTION on %s: %w", role, err)
	}
	switch {
	case hasAdmin:
	case pg16:
		return false, adminOptionError(admin.RoleName, role, "grant it to itself (CREATE DATABASE ... OWNER and the database grants need the membership)")
	case !admin.CreateRole:
		return false, fmt.Errorf("the admin account %q can neither grant itself %s (no CREATEROLE, no ADMIN OPTION on the role) nor act as it: grant it CREATEROLE, or run bootstrap as an account that holds the role", admin.RoleName, role)
	}
	return false, nil
}

// adminOptionError explains the PostgreSQL 16 rule that an admin needs
// ADMIN OPTION on an existing role.
func adminOptionError(admin, role, what string) error {
	return fmt.Errorf("the admin account %q has no ADMIN OPTION on the existing role %s and so cannot %s (PostgreSQL 16+: CREATEROLE only covers roles the account created or holds ADMIN OPTION on): "+
		"run bootstrap as the account that created %s, or let a superuser run GRANT %s TO %s WITH ADMIN OPTION",
		admin, role, what, role, role, pgx.Identifier{admin}.Sanitize())
}

// checkExistingMemberships refuses foreign members and ADMIN OPTION holders
// of bekup's roles (D-207): only the admin itself (the creator holds ADMIN
// OPTION on PostgreSQL 16+; in single-account mode the account) may hold
// them. Superuser members gain nothing from a membership and are only
// warned about. Existing roles need AdoptExistingRoles.
func (o *BootstrapOptions) checkExistingMemberships(ctx context.Context, q *sqlc.Queries, adminRole string, existing []string, res *BootstrapResult) error {
	ms, err := roleMemberships(ctx, q)
	if err != nil {
		return err
	}
	var refused, fixes []string
	for _, m := range splitMemberships(ms, adminRole) {
		if m.MemberSuperuser {
			res.Warnings = append(res.Warnings, describeMembership(m)+"; superusers need no membership, consider "+revokeMembershipHint(m))
			continue
		}
		refused = append(refused, describeMembership(m))
		fixes = append(fixes, revokeMembershipHint(m))
	}
	if len(refused) > 0 {
		return fmt.Errorf("other roles can act as or administer bekup's roles: %s. They could take over bekup's roles (and alter its audit log); "+
			"revoke them as an administrator (%s) or run bootstrap as that account", strings.Join(refused, "; "), strings.Join(fixes, " "))
	}
	if !o.AdoptExistingRoles {
		return fmt.Errorf("role(s) %s already exist (from an earlier bootstrap, a DBA or someone else); review them (attributes, memberships and who created them), "+
			"then run bootstrap again with --adopt-existing-roles to accept them", strings.Join(existing, " and "))
	}
	res.Actions = append(res.Actions, "adopted the existing role(s) "+strings.Join(existing, " and ")+" (--adopt-existing-roles)")
	return nil
}

// checkExistingRole verifies an existing bekup role: no dangerous
// attributes or memberships (the D-099 rules), LOGIN with two roles.
func (o *BootstrapOptions) checkExistingRole(ctx context.Context, q *sqlc.Queries, f sqlc.BootstrapRoleFactsRow, forbidden []string, res *BootstrapResult) error {
	rc, err := roleCheck(ctx, q, f.RoleName, forbidden)
	if err != nil {
		return err
	}
	if err := validateRole(rc); err != nil {
		return fmt.Errorf("existing %w; fix it as an administrator (ALTER ROLE / REVOKE) and run bootstrap again", err)
	}
	if f.CreateDb {
		res.Warnings = append(res.Warnings, fmt.Sprintf("role %s has CREATEDB; bekup does not need it (ALTER ROLE %s NOCREATEDB)", f.RoleName, f.RoleName))
	}
	switch {
	case !o.SingleAccount && !f.CanLogin:
		return fmt.Errorf("role %s exists but cannot log in (NOLOGIN, e.g. from single-account mode); ALTER ROLE %s LOGIN and run bootstrap with --reset-passwords", f.RoleName, f.RoleName)
	case o.SingleAccount && f.CanLogin:
		res.Warnings = append(res.Warnings, fmt.Sprintf("role %s can log in; in single-account mode it is only used through SET ROLE", f.RoleName))
	}
	res.Actions = append(res.Actions, "role "+f.RoleName+" exists and passed the role checks")
	return nil
}

// adminHoldsRole reports whether the admin can already SET ROLE to role
// and inherits its privileges (PostgreSQL 16+: the SET and INHERIT
// options; 15: membership, the admin is INHERIT).
func adminHoldsRole(ctx context.Context, q *sqlc.Queries, role string, pg16 bool) (bool, error) {
	var (
		ok  bool
		err error
	)
	if pg16 {
		ok, err = q.BootstrapCanSetRole(ctx, role)
	} else {
		ok, err = q.BootstrapIsMember(ctx, role)
	}
	if err != nil {
		return false, fmt.Errorf("check the admin's membership in %s: %w", role, err)
	}
	return ok, nil
}

// ensureCitext makes sure migration 00004 can use citext: nothing to do
// when it is installed or trusted (bekup_owner creates it); otherwise the
// admin creates it. A failure is a warning: `bekup migrate check` reports
// it with the fix.
func (o *BootstrapOptions) ensureCitext(ctx context.Context, reg *logging.Registry, timeout time.Duration, res *BootstrapResult) (err error) {
	creds := o.Admin
	creds.Database = o.Database
	conn, err := bootstrapConnect(ctx, creds, o.TLS, reg, timeout)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, conn.Close(context.WithoutCancel(ctx))) }()
	q := sqlc.New(conn)
	c, err := q.PreflightCitext(ctx)
	if err != nil {
		return fmt.Errorf("read citext status: %w", err)
	}
	res.Citext = citextState(c)
	switch res.Citext {
	case CitextInstalled, CitextCreatable:
		return nil
	case CitextUnavailable:
		res.Warnings = append(res.Warnings, "the citext extension is not available on the server; bekup migrate will fail until it is (ask your provider)")
		return nil
	}
	st := citextStatement(o.Database)
	if err := execStatement(ctx, conn, st); err != nil {
		res.Warnings = append(res.Warnings, fmt.Sprintf("could not create citext as %s (%s); create it as an administrator before bekup migrate (on Azure add CITEXT to azure.extensions first)",
			res.AdminRole, pgErrorText(err)))
		return nil
	}
	res.Actions = append(res.Actions, st.Action)
	res.Changed = true
	c, err = q.PreflightCitext(ctx)
	if err != nil {
		return fmt.Errorf("read citext status: %w", err)
	}
	res.Citext = citextState(c)
	return nil
}

func execStatement(ctx context.Context, conn *pgx.Conn, st Statement) error {
	if st.PSQLOnly {
		return nil
	}
	if _, err := conn.Exec(ctx, st.SQL); err != nil {
		return fmt.Errorf("%s: %s", st.Action, pgErrorText(err))
	}
	return nil
}

// pgErrorText is a server error without anything that could echo a
// statement: PostgreSQL's message, its hint and SQLSTATE (the messages of
// these DDL statements name roles and databases, never passwords).
func pgErrorText(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		s := pe.Message + " (SQLSTATE " + pe.Code + ")"
		if pe.Hint != "" {
			s += "; hint: " + pe.Hint
		}
		return s
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return err.Error()
	}
	return "statement failed (connection error)"
}
