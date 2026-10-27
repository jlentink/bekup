package auth

import (
	"bufio"
	"bytes"
	_ "embed"
	"errors"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/ccojocar/zxcvbn-go"
)

// Password policy bounds (D-047, D-073).
const (
	// MinPasswordLength is the plan's minimum; security.password_min_length
	// can only raise it.
	MinPasswordLength = 12
	// MaxPasswordLength bounds a password in characters (at most
	// MaxPasswordBytes bytes in UTF-8), which bounds the hashing work.
	MaxPasswordLength = 1024
	// MinPasswordScore is the minimum zxcvbn score (0-4);
	// security.password_min_score can only raise it.
	MinPasswordScore = 3
	// strengthInputRunes caps the prefix that zxcvbn scores: its matching
	// cost grows quickly with the length, and the first 100 characters of
	// an otherwise accepted password carry plenty of entropy.
	strengthInputRunes = 100
	// minIdentityRunes is the shortest e-mail local part or name that is
	// searched for inside a password.
	minIdentityRunes = 3
)

// PolicyReason says why a password was refused. The values are stable and
// returned to the client as the error detail.
type PolicyReason string

// Policy reasons.
const (
	ReasonTooShort        PolicyReason = "too_short"
	ReasonTooLong         PolicyReason = "too_long"
	ReasonInvalid         PolicyReason = "invalid_characters"
	ReasonCommon          PolicyReason = "common"
	ReasonContainsAccount PolicyReason = "contains_account"
	ReasonWeak            PolicyReason = "weak"
	ReasonUnchanged       PolicyReason = "unchanged"
)

// PolicyError is a refused password. Message is safe to show to the user.
type PolicyError struct {
	Reason  PolicyReason
	Message string
}

func (e *PolicyError) Error() string { return "auth: password refused: " + string(e.Reason) }

// PasswordPolicy decides whether a new password is acceptable.
type PasswordPolicy struct {
	// MinLength in characters (at least MinPasswordLength).
	MinLength int
	// MinScore is the minimum zxcvbn score (at least MinPasswordScore).
	MinScore int
}

// NewPasswordPolicy returns the policy with the bounds applied: values
// below the plan's minimums are raised to them.
func NewPasswordPolicy(minLength, minScore int) PasswordPolicy {
	return PasswordPolicy{
		MinLength: min(max(minLength, MinPasswordLength), MaxPasswordLength),
		MinScore:  min(max(minScore, MinPasswordScore), 4),
	}
}

// Check returns nil for an acceptable password or a *PolicyError. account
// holds the e-mail address and display name of the account: a password
// containing the address, its local part or the name is refused, and they
// feed the strength estimate as known inputs.
func (p PasswordPolicy) Check(password string, account ...string) error {
	if !utf8.ValidString(password) {
		return &PolicyError{ReasonInvalid, "the password contains invalid characters"}
	}
	for _, r := range password {
		if r == 0 || (unicode.IsControl(r) && r != '\t') {
			return &PolicyError{ReasonInvalid, "the password must not contain control characters"}
		}
	}
	n := utf8.RuneCountInString(password)
	minLen := max(p.MinLength, MinPasswordLength)
	if n < minLen {
		return &PolicyError{ReasonTooShort, "the password is too short (at least " + strconv.Itoa(minLen) + " characters)"}
	}
	if n > MaxPasswordLength || len(password) > MaxPasswordBytes {
		return &PolicyError{ReasonTooLong, "the password is too long (at most " + strconv.Itoa(MaxPasswordLength) + " characters)"}
	}
	if IsCommonPassword(password) {
		return &PolicyError{ReasonCommon, "this password is too common; choose another one"}
	}
	inputs := identityParts(account)
	lower := strings.ToLower(password)
	for _, part := range inputs {
		if strings.Contains(lower, part) {
			return &PolicyError{ReasonContainsAccount, "the password must not contain your e-mail address or name"}
		}
	}
	if Strength(password, inputs) < max(p.MinScore, MinPasswordScore) {
		return &PolicyError{ReasonWeak, "the password is too easy to guess; use a longer passphrase or more varied characters"}
	}
	return nil
}

// Strength returns the zxcvbn score (0-4) of password, scoring at most its
// first strengthInputRunes characters.
func Strength(password string, inputs []string) int {
	if utf8.RuneCountInString(password) > strengthInputRunes {
		password = string([]rune(password)[:strengthInputRunes])
	}
	return zxcvbn.PasswordStrength(password, inputs).Score
}

// identityParts returns the lowercased account strings searched for in a
// password: each value, an e-mail address's local part and the words of a
// name, keeping only parts of at least minIdentityRunes characters.
func identityParts(account []string) []string {
	var out []string
	add := func(s string) {
		s = strings.ToLower(strings.TrimSpace(s))
		if utf8.RuneCountInString(s) >= minIdentityRunes {
			out = append(out, s)
		}
	}
	for _, a := range account {
		add(a)
		if local, _, ok := strings.Cut(a, "@"); ok {
			add(local)
		}
		fields := strings.FieldsFunc(a, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		if len(fields) > 1 {
			for _, f := range fields {
				add(f)
			}
		}
	}
	return out
}

// commonPasswords is the top-100k list of the xato.net 10-million-password
// corpus from SecLists (MIT; see NOTICE), one password per line, unchanged.
//
//go:embed common_passwords.txt
var commonPasswords []byte

var (
	commonOnce sync.Once
	commonSet  map[string]struct{}
)

// loadCommon builds the lookup set. Entries shorter than MinPasswordLength
// can never pass the length check, so only the longer ones are kept (the
// file stays verbatim for attribution and verification).
func loadCommon() {
	commonSet = make(map[string]struct{}, 1024)
	sc := bufio.NewScanner(bytes.NewReader(commonPasswords))
	for sc.Scan() {
		w := strings.ToLower(strings.TrimRight(sc.Text(), "\r"))
		if utf8.RuneCountInString(w) >= MinPasswordLength {
			commonSet[w] = struct{}{}
		}
	}
	if err := sc.Err(); err != nil {
		panic(errors.Join(errors.New("auth: embedded common password list unreadable"), err))
	}
}

// IsCommonPassword reports whether password (case-insensitively) is on the
// embedded common-password list.
func IsCommonPassword(password string) bool {
	commonOnce.Do(loadCommon)
	_, ok := commonSet[strings.ToLower(password)]
	return ok
}
