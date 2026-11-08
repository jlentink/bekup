//go:build integration

package graph_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

const (
	meLocaleQuery  = `{ me { id locale } }`
	setLocaleMut   = `mutation($l: Locale) { setMyLocale(locale: $l) { id locale } }`
	loginLocaleMut = `mutation($e: String!, $p: String!) { login(email: $e, password: $p) { user { id locale } } }`
)

type localeGQL struct {
	ID     string  `json:"id"`
	Locale *string `json:"locale"`
}

// TestSetMyLocale: every signed-in account stores its own UI language
// (D-236); null follows the browser again; other accounts are untouched;
// the login result carries it; the database refuses unknown values.
func TestSetMyLocale(t *testing.T) {
	e := newAuthEnv(t)
	u := e.user("ann@example.com", sqlc.UserRoleUser)
	e.user("bo@example.com", sqlc.UserRoleAdmin)
	ac, bc := e.mustLogin("ann@example.com"), e.mustLogin("bo@example.com")

	if r, _ := e.do("", "", setLocaleMut, map[string]any{"l": "NL"}); r.code() != "UNAUTHENTICATED" {
		t.Fatalf("anonymous: %+v", r.Errors)
	}
	read := func(cookie, query, field string) localeGQL {
		t.Helper()
		r, _ := e.do(cookie, "", query, map[string]any{"l": "NL", "e": "ann@example.com", "p": testPassword})
		if len(r.Errors) > 0 {
			t.Fatalf("%s: %+v", field, r.Errors)
		}
		var v localeGQL
		raw := r.Data[field]
		if field == "login" {
			var lr struct {
				User localeGQL `json:"user"`
			}
			if err := json.Unmarshal(raw, &lr); err != nil {
				t.Fatal(err)
			}
			return lr.User
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	if v := read(ac, meLocaleQuery, "me"); v.Locale != nil {
		t.Fatalf("default locale: %+v", v)
	}
	if v := read(ac, setLocaleMut, "setMyLocale"); v.ID != u.ID.String() || v.Locale == nil || *v.Locale != "NL" {
		t.Fatalf("set NL: %+v", v)
	}
	if v := read(ac, meLocaleQuery, "me"); v.Locale == nil || *v.Locale != "NL" {
		t.Fatalf("me after set: %+v", v)
	}
	if v := read("", loginLocaleMut, "login"); v.Locale == nil || *v.Locale != "NL" {
		t.Fatalf("login result: %+v", v)
	}
	if v := read(bc, meLocaleQuery, "me"); v.Locale != nil {
		t.Fatalf("other account changed: %+v", v)
	}
	// null: follow the browser again.
	if r, _ := e.do(ac, "", setLocaleMut, map[string]any{"l": nil}); len(r.Errors) > 0 ||
		string(r.Data["setMyLocale"]) != `{"id":"`+u.ID.String()+`","locale":null}` {
		t.Fatalf("set null: %+v %s", r.Errors, r.Data["setMyLocale"])
	}
	// Unknown languages never reach the service.
	if r, _ := e.do(ac, "", setLocaleMut, map[string]any{"l": "DE"}); len(r.Errors) == 0 {
		t.Fatal("DE accepted")
	}
	// The CHECK holds for anything that bypasses the API.
	if _, err := e.pool.Exec(context.Background(), `UPDATE users SET locale = 'de' WHERE id = $1`, u.ID); err == nil {
		t.Fatal("database accepted locale 'de'")
	}
}
