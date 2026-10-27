package sshpolicy

// userError is a static, secret-free user-name problem.
type userError string

func (e userError) Error() string { return string(e) }

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
