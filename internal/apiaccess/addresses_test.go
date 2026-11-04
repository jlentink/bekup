package apiaccess

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/jlentink/bekup/internal/serviceutil"
)

func fieldMessage(t *testing.T, err error) string {
	t.Helper()
	var fe *serviceutil.FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("error %v is not a field error", err)
	}
	if fe.Field != FieldAllowedCIDRs {
		t.Fatalf("field %q", fe.Field)
	}
	return fe.Message
}

func TestParseAddresses(t *testing.T) {
	ok := []struct {
		in   []string
		want []string
	}{
		{[]string{"203.0.113.0/24"}, []string{"203.0.113.0/24"}},
		{[]string{" 203.0.113.7 "}, []string{"203.0.113.7/32"}},
		{[]string{"2001:DB8::/32", "2001:db8:1::1"}, []string{"2001:db8::/32", "2001:db8:1::1/128"}},
		{[]string{"10.0.0.0/8", "10.1.0.0/16"}, []string{"10.0.0.0/8", "10.1.0.0/16"}},
		// The widest networks a list may hold.
		{[]string{"10.0.0.0/8", "2001:db8::/32"}, []string{"10.0.0.0/8", "2001:db8::/32"}},
		// IPv4-compatible and other IPv6 forms are plain IPv6 (as in the
		// database).
		{[]string{"::/96"}, []string{"::/96"}},
	}
	for _, c := range ok {
		got, err := ParseAddresses(c.in, false)
		if err != nil {
			t.Fatalf("%v: %v", c.in, err)
		}
		if s := AddressStrings(got); !slices.Equal(s, c.want) {
			t.Fatalf("%v = %v, want %v", c.in, s, c.want)
		}
	}
	full := make([]string, MaxAddresses)
	for i := range full {
		full[i] = fmt.Sprintf("192.0.2.%d", i)
	}
	if got, err := ParseAddresses(full, false); err != nil || len(got) != MaxAddresses {
		t.Fatalf("32 entries: %v %v", got, err)
	}
	if got, err := ParseAddresses(nil, true); err != nil || got != nil {
		t.Fatalf("any address: %v %v", got, err)
	}

	bad := []struct {
		in    []string
		any   bool
		match string
	}{
		{nil, false, "at least one network"},
		{[]string{}, false, "at least one network"},
		{[]string{"192.0.2.0/24"}, true, "never both"},
		{append(slices.Clone(full), "198.51.100.1"), false, "at most 32"},
		{[]string{""}, false, "entry 1 is empty"},
		{[]string{"192.0.2.0/24", "  "}, false, "entry 2 is empty"},
		{[]string{"example.com"}, false, "not an IPv4 or IPv6"},
		{[]string{"192.0.2.0/33"}, false, "not an IPv4 or IPv6"},
		{[]string{"192.0.2.010"}, false, "not an IPv4 or IPv6"},
		{[]string{strings.Repeat("1", 65)}, false, "too long"},
		{[]string{"fe80::1%eth0"}, false, "zones"},
		{[]string{"0.0.0.0/0"}, false, "allows every address"},
		{[]string{"::/0"}, false, "allows every address"},
		// Wider than /8 or /32 is any address in all but name (M1).
		{[]string{"0.0.0.0/1", "128.0.0.0/1"}, false, "entry 1 (0.0.0.0/1) is wider than /8"},
		{[]string{"10.0.0.0/8", "10.0.0.0/7"}, false, "entry 2 (10.0.0.0/7) is wider than /8"},
		{[]string{"2000::/3"}, false, "wider than /32"},
		{[]string{"2001:db8::/31"}, false, "allow from any address"},
		{[]string{"192.0.2.1/24"}, false, "did you mean 192.0.2.0/24?"},
		{[]string{"2001:db8::1/32"}, false, "did you mean 2001:db8::/32?"},
		{[]string{"::ffff:192.0.2.0/120"}, false, "IPv4-mapped"},
		{[]string{"::ffff:192.0.2.1"}, false, "IPv4-mapped"},
		{[]string{"::ffff:0:0/96"}, false, "IPv4-mapped"},
		{[]string{"192.0.2.0/24", "192.0.2.0/24"}, false, "repeats entry 1"},
		{[]string{"192.0.2.7", "192.0.2.7/32"}, false, "repeats entry 1"},
		{[]string{"2001:db8::/32", "2001:DB8:0::/32"}, false, "repeats entry 1"},
	}
	for _, c := range bad {
		_, err := ParseAddresses(c.in, c.any)
		if err == nil {
			t.Fatalf("%v (any=%v) accepted", c.in, c.any)
		}
		if m := fieldMessage(t, err); !strings.Contains(m, c.match) {
			t.Errorf("%v: message %q, want %q", c.in, m, c.match)
		}
	}
}

// FuzzParseAddresses: whatever is accepted satisfies the database's
// api_cidrs_valid rules (at most 32 distinct canonical networks, none
// wider than /8 (IPv4) or /32 (IPv6), no IPv4-mapped network).
func FuzzParseAddresses(f *testing.F) {
	for _, s := range []string{"203.0.113.0/24", "2001:db8::1", "::ffff:1.2.3.4", "10.0.0.1/8", "0.0.0.0/0", "fe80::1%x", "1.2.3.4,1.2.3.4"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		entries := strings.Split(in, ",")
		got, err := ParseAddresses(entries, false)
		if err != nil {
			var fe *serviceutil.FieldError
			if !errors.As(err, &fe) {
				t.Fatalf("non-field error %v", err)
			}
			return
		}
		if len(got) == 0 || len(got) > MaxAddresses || len(got) != len(entries) {
			t.Fatalf("%q: %d networks", in, len(got))
		}
		seen := map[netip.Prefix]bool{}
		for _, p := range got {
			if !p.IsValid() || p.Bits() < minPrefix(p) || p.Masked() != p || p.Addr().Is4In6() || p.Addr().Zone() != "" || seen[p] {
				t.Fatalf("%q: accepted %v", in, p)
			}
			seen[p] = true
			if back, err := netip.ParsePrefix(p.String()); err != nil || back != p {
				t.Fatalf("%v does not round-trip", p)
			}
		}
	})
}
