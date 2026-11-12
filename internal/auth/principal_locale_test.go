package auth

import (
	"testing"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
	"github.com/jlentink/bekup/internal/i18n"
)

// TestPrincipalLocale: the account's stored language travels on the
// principal (D-238); an unknown stored value is ignored.
func TestPrincipalLocale(t *testing.T) {
	t.Parallel()
	sess := sqlc.Session{ID: uuid.New(), UserID: uuid.New(), Aal: 1}
	for stored, want := range map[string]i18n.Locale{"nl": i18n.NL, "en": i18n.EN, "xx": "", "": ""} {
		u := sqlc.User{Role: sqlc.UserRole(RoleUser)}
		if stored != "" {
			u.Locale = &stored
		}
		p := principalOf(sess, u, RoleUser)
		if p.Locale != want {
			t.Errorf("stored %q: Locale = %q, want %q", stored, p.Locale, want)
		}
	}
	p := Principal{Locale: i18n.NL}
	if p.TextLocale(i18n.EN) != i18n.NL {
		t.Error("the account's language must win over Accept-Language")
	}
	if (Principal{}).TextLocale(i18n.NL) != i18n.NL || (Principal{}).TextLocale("") != i18n.EN {
		t.Error("without an account language: Accept-Language, else English")
	}
}
