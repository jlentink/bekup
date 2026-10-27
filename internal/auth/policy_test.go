package auth

import (
	"errors"
	"strings"
	"testing"
)

func policyReason(err error) PolicyReason {
	var pe *PolicyError
	if errors.As(err, &pe) {
		return pe.Reason
	}
	return ""
}

func TestPasswordPolicy(t *testing.T) {
	p := NewPasswordPolicy(12, 3)
	email, name := "alice.smith@example.com", "Alice Smith"
	cases := []struct {
		pw   string
		want PolicyReason
	}{
		{"blue-mountain-river-42", ""},
		{"correct horse battery staple", ""},
		{"N8v#q2LmZ!r4Tx", ""},
		{"Kv9pQ2xW7mR", ReasonTooShort},              // 11 characters
		{"ÄÖÜäöüßÄÖÜ!", ReasonTooShort},              // counted in characters, not bytes
		{strings.Repeat("aB3$", 257), ReasonTooLong}, // 1028 characters
		{"password1234", ReasonCommon},               // on the list
		{"PASSWORD1234", ReasonCommon},               // case-insensitive
		{"alice.smith@example.com!", ReasonContainsAccount},
		{"xX-Alice.Smith-2024-Xx", ReasonContainsAccount}, // the e-mail local part
		{"my-SMITH-river-9431", ReasonContainsAccount},    // a word of the name
		{"aaaaaaaaaaaaaaaa", ReasonWeak},
		{"qwertyuiopasdf", ReasonWeak},
		{"summer2024summer", ReasonWeak},
		{"bad\x00password-xyz-123", ReasonInvalid},
		{"bad\npassword-xyz-123", ReasonInvalid},
		{"\xff\xfe-invalid-utf8-xyz", ReasonInvalid},
	}
	for _, c := range cases {
		got := policyReason(p.Check(c.pw, email, name))
		if got != c.want {
			t.Errorf("Check(%q) = %q, want %q", c.pw, got, c.want)
		}
	}
	if err := p.Check("blue-mountain-river-42", email, name); err != nil {
		t.Fatalf("good password refused: %v", err)
	}
	var pe *PolicyError
	if err := p.Check("zq9x"); !errors.As(err, &pe) || pe.Message == "" || strings.Contains(pe.Error(), "zq9x") || strings.Contains(pe.Message, "zq9x") {
		t.Fatalf("PolicyError must carry a message and never echo the password: %v", err)
	}
}

func TestPasswordPolicyBounds(t *testing.T) {
	p := NewPasswordPolicy(4, 0)
	if p.MinLength != MinPasswordLength || p.MinScore != MinPasswordScore {
		t.Fatalf("bounds not applied: %+v", p)
	}
	strict := NewPasswordPolicy(20, 4)
	if r := policyReason(strict.Check("blue-mountain-42")); r != ReasonTooShort {
		t.Fatalf("raised min length not applied: %q", r)
	}
	// A zero-value policy still enforces the plan's minimums.
	if r := policyReason(PasswordPolicy{}.Check("aB3$aB3$")); r != ReasonTooShort {
		t.Fatalf("zero policy: %q", r)
	}
}

func TestCommonPasswordList(t *testing.T) {
	if n := strings.Count(string(commonPasswords), "\n"); n < 99000 {
		t.Fatalf("embedded list has %d lines, want the top-100k list", n)
	}
	for _, pw := range []string{"password1234", "Password1234", "qwertyuiop123", "123456789012"} {
		if !IsCommonPassword(pw) {
			t.Errorf("%q not detected as common", pw)
		}
	}
	if IsCommonPassword("blue-mountain-river-42") {
		t.Error("false positive")
	}
}

func FuzzPasswordPolicy(f *testing.F) {
	f.Add("blue-mountain-river-42", "a@example.com")
	f.Add("", "")
	f.Fuzz(func(t *testing.T, pw, email string) {
		if len(pw) > 2*MaxPasswordBytes {
			return
		}
		err := NewPasswordPolicy(12, 3).Check(pw, email)
		var pe *PolicyError
		if err != nil && (!errors.As(err, &pe) || pe.Message == "") {
			t.Fatalf("Check returned %v", err)
		}
	})
}
