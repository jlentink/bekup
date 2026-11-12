package sshpolicy

import "github.com/jlentink/bekup/internal/i18n"

// userError is a static, secret-free user-name problem.
type userError string

func (e userError) Error() string { return string(e) }

// I18nMsg returns the problem as a whole, translatable sentence (D-238).
func (e userError) I18nMsg() i18n.Msg {
	switch e {
	case "required":
		return i18n.M("the user name is required")
	case "too long":
		return i18n.M("the user name is too long")
	case "must not start with '-'":
		return i18n.M("the user name must not start with '-'")
	default:
		return i18n.M("the user name contains a character that is not allowed")
	}
}

// ValidateUser checks an SSH user name: 1..MaxUserLen bytes of printable
// ASCII without spaces, not starting with '-'. The error message is one of
// "required", "too long", "must not start with '-'" or "invalid
// character" (it never repeats the input).
func ValidateUser(u string) error {
	switch {
	case u == "":
		return userError("required")
	case len(u) > MaxUserLen:
		return userError("too long")
	case u[0] == '-':
		return userError("must not start with '-'")
	}
	for i := range len(u) {
		if u[i] <= ' ' || u[i] >= 0x7f {
			return userError("invalid character")
		}
	}
	return nil
}
