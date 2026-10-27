package netguard

import (
	"net/netip"
	"testing"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		addr    string
		blocked bool
		hard    bool
		reason  Reason
	}{
		// public
		{"8.8.8.8", false, false, ""},
		{"93.184.216.34", false, false, ""},
		{"2606:4700:4700::1111", false, false, ""},
		{"::ffff:8.8.8.8", false, false, ""},
		{"64:ff9b::808:808", false, false, ""}, // NAT64 of 8.8.8.8
		{"2002:808:808::1", false, false, ""},  // 6to4 of 8.8.8.8
		// IPv4 hard
		{"0.0.0.0", true, true, ReasonThisNetwork},
		{"0.1.2.3", true, true, ReasonThisNetwork},
		{"127.0.0.1", true, true, ReasonLoopback},
		{"127.255.255.254", true, true, ReasonLoopback},
		{"169.254.169.254", true, true, ReasonMetadata},
		{"169.254.1.1", true, true, ReasonLinkLocal},
		{"100.100.100.200", true, true, ReasonMetadata},
		{"224.0.0.1", true, true, ReasonMulticast},
		{"239.255.255.250", true, true, ReasonMulticast},
		{"240.0.0.1", true, true, ReasonReserved},
		{"255.255.255.255", true, true, ReasonBroadcast},
		// IPv4 soft
		{"10.1.2.3", true, false, ReasonPrivate},
		{"172.16.0.1", true, false, ReasonPrivate},
		{"172.31.255.255", true, false, ReasonPrivate},
		{"192.168.1.1", true, false, ReasonPrivate},
		{"100.64.0.1", true, false, ReasonSharedAddress},
		{"192.0.0.8", true, false, ReasonProtocolAssignments},
		{"192.0.2.1", true, false, ReasonDocumentation},
		{"198.51.100.1", true, false, ReasonDocumentation},
		{"203.0.113.1", true, false, ReasonDocumentation},
		{"198.18.0.1", true, false, ReasonBenchmarking},
		{"192.88.99.1", true, false, ReasonReserved},
		{"172.32.0.1", false, false, ""},
		// IPv4-mapped forms follow the IPv4 verdict
		{"::ffff:127.0.0.1", true, true, ReasonLoopback},
		{"::ffff:169.254.169.254", true, true, ReasonMetadata},
		{"::ffff:10.0.0.1", true, false, ReasonPrivate},
		// IPv6 hard
		{"::", true, true, ReasonUnspecified},
		{"::1", true, true, ReasonLoopback},
		{"::127.0.0.1", true, true, ReasonIPv4Compatible},
		{"::8.8.8.8", true, true, ReasonIPv4Compatible},
		{"::ffff:0:7f00:1", true, true, ReasonIPv4Translated},
		{"64:ff9b::7f00:1", true, true, ReasonNAT64Embedded},
		{"64:ff9b::a9fe:a9fe", true, true, ReasonNAT64Embedded},
		{"64:ff9b::a00:1", true, true, ReasonNAT64Embedded},
		{"2002:7f00:1::1", true, true, Reason6to4Embedded},
		{"2002:a9fe:a9fe::", true, true, Reason6to4Embedded},
		{"2002:c0a8:101::1", true, true, Reason6to4Embedded},
		{"2001::1", true, true, ReasonTeredo},
		{"2001:0:4136:e378:8000:63bf:3fff:fdd2", true, true, ReasonTeredo},
		{"fe80::1", true, true, ReasonLinkLocal},
		{"fe80::1%eth0", true, true, ReasonLinkLocal},
		{"fd00:ec2::254", true, true, ReasonMetadata},
		{"ff02::1", true, true, ReasonMulticast},
		{"4000::1", true, true, ReasonReserved},
		{"1::1", true, true, ReasonReserved},
		// IPv6 soft
		{"fc00::1", true, false, ReasonUniqueLocal},
		{"fd12:3456::1", true, false, ReasonUniqueLocal},
		{"fec0::1", true, false, ReasonSiteLocal},
		{"100::1", true, false, ReasonDiscard},
		{"64:ff9b:1::1", true, false, ReasonNAT64Local},
		{"2001:db8::1", true, false, ReasonDocumentation},
		{"2001:2::1", true, false, ReasonBenchmarking},
		{"2001:10::1", true, false, ReasonProtocolAssignments},
		{"3fff::1", true, false, ReasonDocumentation},
		{"5f00::1", true, false, ReasonReserved},
	}
	for _, tc := range tests {
		t.Run(tc.addr, func(t *testing.T) {
			got := Classify(netip.MustParseAddr(tc.addr))
			want := Classification{Blocked: tc.blocked, Hard: tc.hard, Reason: tc.reason}
			if got != want {
				t.Fatalf("Classify(%s) = %+v, want %+v", tc.addr, got, want)
			}
		})
	}
}

func TestClassifyInvalidAndDevLoopback(t *testing.T) {
	if c := Classify(netip.Addr{}); !c.Blocked || !c.Hard {
		t.Fatalf("zero Addr = %+v, want hard block", c)
	}
	for _, s := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1"} {
		c := classify(netip.MustParseAddr(s), true)
		if !c.Blocked || c.Hard || c.Reason != ReasonLoopback {
			t.Fatalf("dev classify(%s) = %+v, want soft loopback", s, c)
		}
	}
	// Loopback embedded in NAT64/6to4 stays hard even in dev mode.
	for _, s := range []string{"64:ff9b::7f00:1", "2002:7f00:1::1", "::127.0.0.1"} {
		if c := classify(netip.MustParseAddr(s), true); !c.Hard {
			t.Fatalf("dev classify(%s) = %+v, want hard", s, c)
		}
	}
}

func FuzzClassify(f *testing.F) {
	for _, s := range [][]byte{
		{127, 0, 0, 1}, {10, 0, 0, 1}, {8, 8, 8, 8}, {169, 254, 169, 254},
		netip.MustParseAddr("::1").AsSlice(),
		netip.MustParseAddr("64:ff9b::7f00:1").AsSlice(),
		netip.MustParseAddr("2002:a00:1::").AsSlice(),
		netip.MustParseAddr("fd00::1").AsSlice(),
	} {
		f.Add(s)
	}
	// A guard with every allowlistable range allowed must still refuse
	// every hard-denied address.
	g, err := New(Options{AllowCIDRs: []string{
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10",
		"192.0.2.0/24", "198.18.0.0/15", "fc00::/7", "fec0::/10", "2001:db8::/32",
		"1.0.0.0/8", "2600::/12",
	}})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		addr, ok := netip.AddrFromSlice(b)
		if !ok {
			return
		}
		c := Classify(addr)
		if c.Blocked && c.Reason == "" {
			t.Fatalf("%s blocked without reason", addr)
		}
		if addr.Is4() {
			if m := Classify(netip.AddrFrom16(addr.As16())); m != c {
				t.Fatalf("mapped %s = %+v, IPv4 = %+v", addr, m, c)
			}
		}
		if c.Hard && g.Check(addr, "") == nil {
			t.Fatalf("hard-denied %s allowed", addr)
		}
		if !c.Blocked && g.Check(addr, "") != nil {
			t.Fatalf("allowed %s refused", addr)
		}
	})
}
