package engine

import (
	"errors"
	"testing"
)

func TestVersionCompare(t *testing.T) {
	tests := []struct {
		a, b Version
		want int
	}{
		{Version{1, 2, 3}, Version{1, 2, 3}, 0},
		{Version{1, 2, 3}, Version{1, 2, 4}, -1},
		{Version{1, 3, 0}, Version{1, 2, 9}, 1},
		{Version{2, 0, 0}, Version{10, 0, 0}, -1},
	}
	for _, tt := range tests {
		if got := tt.a.Compare(tt.b); got != tt.want {
			t.Errorf("%v.Compare(%v) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
		if tt.a.Less(tt.b) != (tt.want < 0) {
			t.Errorf("%v.Less(%v) wrong", tt.a, tt.b)
		}
	}
	if !(Version{}).IsZero() || (Version{0, 0, 1}).IsZero() {
		t.Error("IsZero")
	}
}

func TestParseVersion(t *testing.T) {
	good := map[string]Version{
		"18.6":    {18, 6, 0},
		"8.4.13":  {8, 4, 13},
		"10.11.0": {10, 11, 0},
	}
	for in, want := range good {
		got, err := ParseVersion(in)
		if err != nil || got != want {
			t.Errorf("ParseVersion(%q) = %v, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "18", "18.", "a.b", "18.6-alpine", "1.2.3.4", "123456.1", "-1.2", " 1.2"} {
		if _, err := ParseVersion(in); !errors.Is(err, ErrInvalidVersion) {
			t.Errorf("ParseVersion(%q) err = %v", in, err)
		}
	}
}

func TestParsePGServerVersionNum(t *testing.T) {
	good := map[string]Version{
		"180006": {18, 6, 0},
		"170002": {17, 2, 0},
		"100023": {10, 23, 0},
		"90624":  {9, 6, 24},
		"80400":  {8, 4, 0},
	}
	for in, want := range good {
		got, err := ParsePGServerVersionNum(in)
		if err != nil || got != want {
			t.Errorf("ParsePGServerVersionNum(%q) = %v, %v", in, got, err)
		}
	}
	if v, err := ParsePGServerVersionNum(" 160004\n"); err != nil || v != (Version{16, 4, 0}) {
		t.Errorf("whitespace: %v %v", v, err)
	}
	for _, in := range []string{"", "1234", "60000", "17.2", "-170002", "1700020000", "17000a", "+17000"} {
		if _, err := ParsePGServerVersionNum(in); !errors.Is(err, ErrInvalidVersion) {
			t.Errorf("ParsePGServerVersionNum(%q) err = %v", in, err)
		}
	}
}

func TestParseMySQLVersion(t *testing.T) {
	good := map[string]Version{
		"8.4.13":                  {8, 4, 13},
		"8.0.36-0ubuntu0.22.04.1": {8, 0, 36},
		"9.7.4-log":               {9, 7, 4},
		"8.0.28":                  {8, 0, 28},
	}
	for in, want := range good {
		got, err := ParseMySQLVersion(in)
		if err != nil || got != want {
			t.Errorf("ParseMySQLVersion(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseMySQLVersion("10.6.18-MariaDB"); !errors.Is(err, ErrWrongEngine) {
		t.Errorf("MariaDB string: %v", err)
	}
	if _, err := ParseMySQLVersion("5.5.5-10.11.2-mariadb-log"); !errors.Is(err, ErrWrongEngine) {
		t.Errorf("MariaDB handshake: %v", err)
	}
	for _, in := range []string{"", "eight", "8.0.36x", "8"} {
		if _, err := ParseMySQLVersion(in); !errors.Is(err, ErrInvalidVersion) {
			t.Errorf("ParseMySQLVersion(%q) err = %v", in, err)
		}
	}
}

func TestParseMariaDBVersion(t *testing.T) {
	good := map[string]Version{
		"11.4.2-MariaDB-log": {11, 4, 2},
		"10.6.18-MariaDB":    {10, 6, 18},
		"5.5.5-10.6.18-MariaDB-1:10.6.18+maria~ubu2004": {10, 6, 18},
		"12.3.3-MariaDB-ubu2404":                        {12, 3, 3},
	}
	for in, want := range good {
		got, err := ParseMariaDBVersion(in)
		if err != nil || got != want {
			t.Errorf("ParseMariaDBVersion(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseMariaDBVersion("8.4.13"); !errors.Is(err, ErrWrongEngine) {
		t.Errorf("MySQL string: %v", err)
	}
	for _, in := range []string{"MariaDB", "x-MariaDB", "10.6.18MariaDB"} {
		if _, err := ParseMariaDBVersion(in); !errors.Is(err, ErrInvalidVersion) {
			t.Errorf("ParseMariaDBVersion(%q) err = %v", in, err)
		}
	}
}

func FuzzParseMySQLVersion(f *testing.F) {
	for _, s := range []string{"8.4.13", "8.0.36-log", "10.6.18-MariaDB", "", "99999.99999.99999"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		v, err := ParseMySQLVersion(s)
		if err != nil {
			return
		}
		if v.Major > maxVersionPart || v.Minor > maxVersionPart || v.Patch > maxVersionPart || v.Major < 0 {
			t.Fatalf("out of range: %v", v)
		}
		if _, err := ParseMariaDBVersion(s); err == nil {
			t.Fatalf("%q parsed as both MySQL and MariaDB", s)
		}
	})
}

func FuzzParseMariaDBVersion(f *testing.F) {
	for _, s := range []string{"11.4.2-MariaDB-log", "5.5.5-10.6.18-MariaDB", "8.4.0"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		v, err := ParseMariaDBVersion(s)
		if err != nil {
			return
		}
		if v.Major > maxVersionPart || v.Minor > maxVersionPart || v.Patch > maxVersionPart || v.Major < 0 {
			t.Fatalf("out of range: %v", v)
		}
	})
}

func FuzzParsePGServerVersionNum(f *testing.F) {
	for _, s := range []string{"180006", "90624", "", "9999999"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		v, err := ParsePGServerVersionNum(s)
		if err != nil {
			return
		}
		if v.Major < 7 || v.Major > 999 || v.Minor < 0 || v.Patch < 0 {
			t.Fatalf("implausible version %v from %q", v, s)
		}
	})
}
