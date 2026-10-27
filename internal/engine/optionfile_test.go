package engine

import (
	"bytes"
	"errors"
	"testing"
)

// unescapeOptionValue mirrors the MySQL/MariaDB option-file parser for a
// double-quoted value (mysys my_default: strip the quotes, then process
// \b \t \n \r \s \" \' \\; other backslashes stay).
func unescapeOptionValue(t *testing.T, v []byte) []byte {
	t.Helper()
	if len(v) < 2 || v[0] != '"' || v[len(v)-1] != '"' {
		t.Fatalf("not double-quoted: %q", v)
	}
	v = v[1 : len(v)-1]
	var out []byte
	for i := 0; i < len(v); i++ {
		if v[i] != '\\' || i == len(v)-1 {
			if v[i] == '"' {
				t.Fatalf("unescaped quote in %q", v)
			}
			out = append(out, v[i])
			continue
		}
		i++
		switch v[i] {
		case 'n':
			out = append(out, '\n')
		case 't':
			out = append(out, '\t')
		case 'r':
			out = append(out, '\r')
		case 'b':
			out = append(out, '\b')
		case 's':
			out = append(out, ' ')
		case '"', '\'', '\\':
			out = append(out, v[i])
		default:
			out = append(out, '\\', v[i])
		}
	}
	return out
}

func TestEscapeOptionValue(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", `""`},
		{"simple", `"simple"`},
		{`a\b"c#d;e f` + "\tg'h", `"a\\b\"c#d;e f\tg\'h"`},
		{" lead and trail ", `" lead and trail "`},
		{"\\", `"\\"`},
		{"\b", `"\b"`},
		{`\n`, `"\\n"`},
	}
	for _, tt := range tests {
		in, want := tt.in, tt.want
		got, err := EscapeOptionValue([]byte(in))
		if err != nil || string(got) != want {
			t.Errorf("EscapeOptionValue(%q) = %s, %v; want %s", in, got, err, want)
		}
		if back := unescapeOptionValue(t, got); string(back) != in {
			t.Errorf("round trip %q -> %q", in, back)
		}
	}
	for _, bad := range []string{"a\x00b", "a\nb", "a\rb"} {
		if _, err := EscapeOptionValue([]byte(bad)); !errors.Is(err, ErrInvalidSecret) {
			t.Errorf("EscapeOptionValue(%q) err = %v", bad, err)
		}
	}
}

func FuzzEscapeOptionValue(f *testing.F) {
	for _, s := range []string{"", `a\b"c'd`, "x\ty", "#;", "\\\\\""} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		got, err := EscapeOptionValue(in)
		if err != nil {
			if !bytes.ContainsAny(in, "\x00\n\r") {
				t.Fatalf("rejected %q", in)
			}
			return
		}
		if bytes.ContainsAny(got, "\x00\n\r") {
			t.Fatalf("output contains line breaks: %q", got)
		}
		if back := unescapeOptionValue(t, got); !bytes.Equal(back, in) {
			t.Fatalf("round trip %q -> %q -> %q", in, got, back)
		}
	})
}

func TestMySQLOptionFile(t *testing.T) {
	got, err := MySQLOptionFile([]byte(`p"w`))
	if err != nil || string(got) != "[client]\npassword=\"p\\\"w\"\n" {
		t.Errorf("MySQLOptionFile = %q, %v", got, err)
	}
	got, err = MySQLOptionFile(nil)
	if err != nil || string(got) != "[client]\n" {
		t.Errorf("empty password = %q, %v", got, err)
	}
	if _, err := MySQLOptionFile([]byte("a\nb")); !errors.Is(err, ErrInvalidSecret) {
		t.Errorf("newline: %v", err)
	}
}

func TestPGPassLine(t *testing.T) {
	got, err := PGPassLine("db.example.com", 5432, "backup", []byte(`pa:ss\word`))
	if err != nil || string(got) != `db.example.com:5432:*:backup:pa\:ss\\word`+"\n" {
		t.Errorf("PGPassLine = %q, %v", got, err)
	}
	got, err = PGPassLine("2001:db8::1", 5432, "u", []byte("x"))
	if err != nil || string(got) != `2001\:db8\:\:1:5432:*:u:x`+"\n" {
		t.Errorf("IPv6 = %q, %v", got, err)
	}
	if _, err := PGPassLine("db", 5432, "u", []byte("a\nb")); !errors.Is(err, ErrInvalidSecret) {
		t.Errorf("newline: %v", err)
	}
	if _, err := PGPassLine("-db", 5432, "u", nil); !errors.Is(err, ErrInvalidName) {
		t.Errorf("host: %v", err)
	}
	if _, err := PGPassLine("db", 0, "u", nil); !errors.Is(err, ErrInvalidName) {
		t.Errorf("port: %v", err)
	}
	if _, err := PGPassLine("db", 5432, "u:x", nil); !errors.Is(err, ErrInvalidName) {
		t.Errorf("user: %v", err)
	}
}
