package auth

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
)

// FieldError is invalid input for one field (VALIDATION in the API).
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return "auth: invalid " + e.Field + ": " + e.Message }

// maxDisplayNameLength is the users.display_name limit.
const maxDisplayNameLength = 200

// NormalizeEmail trims and checks an e-mail address for a new account: a
// plain addr-spec (no display name, comments or angle brackets), 3 to 254
// characters, no control characters or spaces.
func NormalizeEmail(field, email string) (string, error) {
	email = strings.TrimSpace(email)
	n := utf8.RuneCountInString(email)
	if !utf8.ValidString(email) || n < 3 || n > maxEmailLength {
		return "", &FieldError{field, "enter a valid e-mail address (at most 254 characters)"}
	}
	for _, r := range email {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", &FieldError{field, "enter a valid e-mail address"}
		}
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || addr.Name != "" {
		return "", &FieldError{field, "enter a valid e-mail address"}
	}
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" || domain == "" || strings.Contains(domain, "@") {
		return "", &FieldError{field, "enter a valid e-mail address"}
	}
	return email, nil
}

// NormalizeDisplayName trims and checks a display name: 1 to 200
// characters, no control characters.
func NormalizeDisplayName(field, name string) (string, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || name == "" || utf8.RuneCountInString(name) > maxDisplayNameLength {
		return "", &FieldError{field, "enter a display name of 1 to 200 characters"}
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", &FieldError{field, "the display name must not contain control characters"}
		}
	}
	return name, nil
}

// codeEncoding is the alphabet of temporary passwords:
// RFC 4648 base32 (A-Z, 2-7), unambiguous to read and type.
var codeEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// randomCode returns n bytes from crypto/rand as base32 in groups of four
// characters separated by '-'.
func randomCode(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: random code: %w", err)
	}
	s := codeEncoding.EncodeToString(b)
	var out strings.Builder
	for i := 0; i < len(s); i += 4 {
		if i > 0 {
			out.WriteByte('-')
		}
		out.WriteString(s[i:min(i+4, len(s))])
	}
	return out.String(), nil
}

// isUniqueViolation reports a unique constraint violation (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}
