package engine

import (
	"errors"
	"slices"
	"testing"
)

func warnCodes(ws []Warning) []WarningCode {
	out := make([]WarningCode, 0, len(ws))
	for _, w := range ws {
		out = append(out, w.Code)
	}
	return out
}

func sameCodes(a, b []WarningCode) bool { return slices.Equal(a, b) }

func TestSelectDumpClient(t *testing.T) {
	c := mustEmbedded(t)
	tests := []struct {
		kind    Kind
		server  Version
		wantKey string
		warns   []WarningCode
		err     error
	}{
		{KindPostgres, Version{18, 1, 0}, "18", nil, nil},
		{KindPostgres, Version{16, 4, 0}, "16", nil, nil},
		{KindPostgres, Version{13, 2, 0}, "13", []WarningCode{WarnLegacyClient}, nil},
		{KindPostgres, Version{12, 20, 0}, "13", []WarningCode{WarnLegacyClient}, nil},
		{KindPostgres, Version{10, 0, 0}, "13", []WarningCode{WarnLegacyClient}, nil},
		{KindPostgres, Version{19, 0, 0}, "", nil, ErrNoClient},
		{KindPostgres, Version{9, 6, 24}, "", nil, ErrServerTooOld},
		{KindMySQL, Version{8, 4, 2}, "8.4", nil, nil},
		{KindMySQL, Version{8, 0, 36}, "8.0", []WarningCode{WarnLegacyClient}, nil},
		{KindMySQL, Version{9, 1, 0}, "9.7", nil, nil},
		{KindMySQL, Version{9, 7, 4}, "9.7", nil, nil},
		{KindMySQL, Version{26, 10, 0}, "9.7", []WarningCode{WarnClientOlderThanServer}, nil},
		{KindMySQL, Version{5, 7, 44}, "", nil, ErrServerTooOld},
		{KindMariaDB, Version{10, 6, 21}, "10.6", []WarningCode{WarnLegacyClient}, nil},
		{KindMariaDB, Version{10, 5, 0}, "10.6", []WarningCode{WarnLegacyClient}, nil},
		{KindMariaDB, Version{10, 11, 2}, "10.11", nil, nil},
		{KindMariaDB, Version{11, 2, 0}, "11.4", nil, nil},
		{KindMariaDB, Version{12, 3, 1}, "12.3", nil, nil},
		{KindMariaDB, Version{13, 0, 0}, "12.3", []WarningCode{WarnClientOlderThanServer}, nil},
		{KindMariaDB, Version{10, 3, 39}, "", nil, ErrServerTooOld},
		{"oracle", Version{19, 0, 0}, "", nil, ErrUnknownEngine},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind)+"/"+tt.server.String(), func(t *testing.T) {
			e, ws, err := c.SelectDumpClient(tt.kind, tt.server)
			if tt.err != nil {
				if !errors.Is(err, tt.err) {
					t.Fatalf("err = %v, want %v", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if e.Key() != tt.wantKey || e.Kind() != tt.kind {
				t.Errorf("got %s %s, want %s", e.Kind(), e.Key(), tt.wantKey)
			}
			if got := warnCodes(ws); !sameCodes(got, tt.warns) {
				t.Errorf("warnings = %v, want %v", got, tt.warns)
			}
		})
	}
}

func TestSelectRestoreClient(t *testing.T) {
	c := mustEmbedded(t)
	tests := []struct {
		kind       Kind
		dump       Version
		target     Version
		wantKey    string
		err        error
		wantWarned bool
	}{
		// Restore image = max(dump client, target).
		{KindPostgres, Version{18, 6, 0}, Version{16, 2, 0}, "18", nil, false},
		{KindPostgres, Version{14, 24, 0}, Version{17, 1, 0}, "17", nil, false},
		{KindPostgres, Version{16, 15, 0}, Version{16, 0, 0}, "16", nil, false},
		{KindPostgres, Version{19, 0, 0}, Version{16, 0, 0}, "", ErrNoClient, false},
		{KindPostgres, Version{18, 6, 0}, Version{9, 6, 0}, "", ErrServerTooOld, false},
		{KindMySQL, Version{8, 4, 13}, Version{9, 7, 0}, "9.7", nil, false},
		{KindMySQL, Version{9, 7, 4}, Version{8, 4, 0}, "9.7", nil, false},
		{KindMariaDB, Version{10, 11, 19}, Version{11, 8, 2}, "11.8", nil, false},
		{KindMariaDB, Version{12, 3, 3}, Version{10, 11, 0}, "12.3", nil, false},
		{KindMariaDB, Version{10, 6, 28}, Version{10, 6, 0}, "10.6", nil, true},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind)+"/"+tt.dump.String()+"->"+tt.target.String(), func(t *testing.T) {
			e, ws, err := c.SelectRestoreClient(tt.kind, tt.dump, tt.target)
			if tt.err != nil {
				if !errors.Is(err, tt.err) {
					t.Fatalf("err = %v, want %v", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if e.Key() != tt.wantKey {
				t.Errorf("key = %s, want %s", e.Key(), tt.wantKey)
			}
			if (len(ws) > 0) != tt.wantWarned {
				t.Errorf("warnings = %v", ws)
			}
		})
	}
}

func TestCheckDumpCompat(t *testing.T) {
	c := mustEmbedded(t)
	pg16, _ := c.Lookup(KindPostgres, "16")
	pg13, _ := c.Lookup(KindPostgres, "13")
	my84, _ := c.Lookup(KindMySQL, "8.4")

	if ws, err := CheckDumpCompat(pg16, Version{15, 3, 0}); err != nil || len(ws) != 0 {
		t.Errorf("pg16 vs 15: %v %v", ws, err)
	}
	if _, err := CheckDumpCompat(pg16, Version{17, 0, 0}); !errors.Is(err, ErrNoClient) {
		t.Errorf("pg16 vs 17 must block: %v", err)
	}
	if ws, err := CheckDumpCompat(pg13, Version{13, 0, 0}); err != nil || !sameCodes(warnCodes(ws), []WarningCode{WarnLegacyClient}) {
		t.Errorf("pg13 legacy: %v %v", ws, err)
	}
	if ws, err := CheckDumpCompat(my84, Version{9, 7, 0}); err != nil || !sameCodes(warnCodes(ws), []WarningCode{WarnClientOlderThanServer}) {
		t.Errorf("mysql older client must warn: %v %v", ws, err)
	}
	if _, err := CheckDumpCompat(my84, Version{5, 7, 0}); !errors.Is(err, ErrServerTooOld) {
		t.Errorf("mysql 5.7: %v", err)
	}
	if _, err := CheckDumpCompat(Entry{}, Version{16, 0, 0}); !errors.Is(err, ErrInvalidEntry) {
		t.Errorf("zero entry: %v", err)
	}
}

func TestMinServerVersion(t *testing.T) {
	for _, k := range Kinds() {
		if v, ok := MinServerVersion(k); !ok || v.IsZero() {
			t.Errorf("%s: %v %v", k, v, ok)
		}
	}
	if _, ok := MinServerVersion("x"); ok {
		t.Error("unknown kind")
	}
}
