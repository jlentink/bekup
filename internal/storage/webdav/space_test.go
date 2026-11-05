package webdav

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/storage"
)

// quotaServer answers a quota PROPFIND on /dav/backups/ with the given
// property values ("" leaves a property out); everything else goes to the
// WebDAV handler (which knows no quota properties).
func quotaServer(free, used string) middleware {
	return func(next http.Handler, _ *testServer) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "PROPFIND" || r.URL.Path != "/dav/backups/" {
				next.ServeHTTP(w, r)
				return
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
			if err != nil {
				http.Error(w, "read", http.StatusBadRequest)
				return
			}
			if r.Header.Get("Depth") != "0" || !strings.Contains(string(body), "quota-available-bytes") {
				r.Body = io.NopCloser(strings.NewReader(string(body)))
				next.ServeHTTP(w, r)
				return
			}
			props := `<d:resourcetype><d:collection/></d:resourcetype>`
			if free != "" {
				props += `<d:quota-available-bytes>` + free + `</d:quota-available-bytes>`
			}
			if used != "" {
				props += `<d:quota-used-bytes>` + used + `</d:quota-used-bytes>`
			}
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(http.StatusMultiStatus)
			fmt.Fprint(w, msHead+resp("/dav/backups/", props)+`</d:multistatus>`)
		})
	}
}

func TestSpaceQuota(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, free, used, dir string
		want                  storage.Space
		unsupported           bool
	}{
		{name: "both", free: "7000", used: "3000",
			want: storage.Space{Total: 10000, Free: 7000, Used: 3000, TotalKnown: true, FreeKnown: true, UsedKnown: true}},
		{name: "folder not created yet: its base is asked", free: "7000", used: "3000", dir: "not/yet",
			want: storage.Space{Total: 10000, Free: 7000, Used: 3000, TotalKnown: true, FreeKnown: true, UsedKnown: true}},
		{name: "unlimited (Nextcloud -3)", free: "-3", used: "123",
			want: storage.Space{Used: 123, UsedKnown: true}},
		{name: "only available", free: "55",
			want: storage.Space{Free: 55, FreeKnown: true}},
		{name: "unknown values", free: "-2", used: "-1", unsupported: true},
		{name: "garbage", free: "lots", used: "1.5e3", unsupported: true},
		{name: "no properties", unsupported: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := startServer(t, quotaServer(tt.free, tt.used))
			d := open(t, srv.config(t, tt.dir))
			sp, err := d.Space(t.Context())
			if tt.unsupported {
				if !errors.Is(err, storage.ErrUnsupported) {
					t.Fatalf("err = %v, want ErrUnsupported", err)
				}
				return
			}
			if err != nil || sp != tt.want {
				t.Fatalf("got %+v, %v; want %+v", sp, err, tt.want)
			}
		})
	}
}

// TestSpaceWithoutQuota: a plain WebDAV server (x/net/webdav) answers the
// quota properties with 404 propstats: unknown, not an error.
func TestSpaceWithoutQuota(t *testing.T) {
	t.Parallel()
	srv := startServer(t, nil)
	d := open(t, srv.config(t, ""))
	_, err := d.Space(t.Context())
	if !errors.Is(err, storage.ErrUnsupported) || storage.Classify(err) != storage.CodeUnsupported {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestSpaceNextcloudFake(t *testing.T) {
	t.Parallel()
	nc := startNextcloud(t, "alice", nil)
	d := open(t, nc.config(t, "backups"))
	if _, err := d.Space(t.Context()); !errors.Is(err, storage.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported (the fake has no quota)", err)
	}
}

func TestQuotaBytes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		want int64
	}{{"0", 0}, {" 42\n", 42}, {"9223372036854775807", 9223372036854775807}} {
		if n, ok := quotaBytes(tc.in); !ok || n != tc.want {
			t.Errorf("quotaBytes(%q) = %d, %v", tc.in, n, ok)
		}
	}
	for _, in := range []string{"", "-3", "1.5", "1e9", "9223372036854775808", "0x10", strings.Repeat("1", 30)} {
		if _, ok := quotaBytes(in); ok {
			t.Errorf("quotaBytes(%q) accepted", in)
		}
	}
}

func TestSpaceClosed(t *testing.T) {
	t.Parallel()
	srv := startServer(t, nil)
	d, err := Open(srv.config(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Space(t.Context()); !errors.Is(err, storage.ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
}
