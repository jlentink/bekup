package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

// Memberships of bekup's roles (D-207). A member of bekup_owner can act as
// the schema owner (and so rewrite the audit log), a member of bekup_app
// can read and change every row the app can, and a holder of ADMIN OPTION
// on either can grant itself that membership at any time. bekup expects
// none of them except the account that ran `bekup db bootstrap`
// (PostgreSQL 16+ gives the creator of a role ADMIN OPTION on it) and, in
// single-account mode, the single account.

// roleMemberships returns every direct membership in bekup_owner and
// bekup_app.
func roleMemberships(ctx context.Context, q *sqlc.Queries) ([]sqlc.RoleMembershipsRow, error) {
	ms, err := q.RoleMemberships(ctx, []string{OwnerRole, AppRole})
	if err != nil {
		return nil, fmt.Errorf("read the memberships of %s and %s: %w", OwnerRole, AppRole, err)
	}
	return ms, nil
}

// describeMembership renders one membership without anything but role
// names.
func describeMembership(m sqlc.RoleMembershipsRow) string {
	s := m.MemberName + " is a member of " + m.RoleName
	if m.AdminOption {
		s += " WITH ADMIN OPTION"
	}
	if m.MemberSuperuser {
		s += " (a superuser)"
	}
	return s
}

// revokeMembershipHint is the statement that removes a membership.
func revokeMembershipHint(m sqlc.RoleMembershipsRow) string {
	return "REVOKE " + m.RoleName + " FROM " + pgx.Identifier{m.MemberName}.Sanitize() + ";"
}

// splitMemberships separates the memberships of the expected roles from
// the foreign ones. bekup_app as a member of bekup_owner is reported by
// the role checks and left out here.
func splitMemberships(ms []sqlc.RoleMembershipsRow, expected ...string) (foreign []sqlc.RoleMembershipsRow) {
	for _, m := range ms {
		if m.MemberName == AppRole && m.RoleName == OwnerRole {
			continue
		}
		known := false
		for _, e := range expected {
			if e != "" && m.MemberName == e {
				known = true
			}
		}
		if !known {
			foreign = append(foreign, m)
		}
	}
	return foreign
}

// membershipWarning is the preflight warning for memberships of roles
// other than the single-account login ("" with two roles).
func membershipWarning(ms []sqlc.RoleMembershipsRow, login string) string {
	foreign := splitMemberships(ms, login)
	if len(foreign) == 0 {
		return ""
	}
	parts := make([]string, 0, len(foreign))
	for _, m := range foreign {
		parts = append(parts, describeMembership(m))
	}
	return fmt.Sprintf("other roles can act as or administer bekup's roles: %s. PostgreSQL 16+ gives the account that created a role ADMIN OPTION on it "+
		"(expected for the account that ran bekup db bootstrap); anyone else could take over bekup's roles (REVOKE <role> FROM <member> as an administrator)",
		strings.Join(parts, "; "))
}
