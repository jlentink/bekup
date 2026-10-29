package netguard

import (
	"sync"
	"testing"
)

// TestPrepareBootstrap swaps the bootstrap list (config reload, D-200):
// default scopes follow, personal webhooks never gain anything, the
// runtime list stays, and the startup dev mode decides about loopback.
func TestPrepareBootstrap(t *testing.T) {
	s := mustSet(t, Options{AllowCIDRs: []string{"10.1.0.0/16"}})
	if err := s.SetAllowlist([]Entry{{Value: "10.7.0.0/16"}}, nil); err != nil {
		t.Fatal(err)
	}
	commit, err := s.PrepareBootstrap([]string{"10.2.0.0/16"}, []string{"new.internal"})
	if err != nil {
		t.Fatal(err)
	}
	if !allowed(s, ScopeDestinations, "10.1.0.1", "") || allowed(s, ScopeDestinations, "10.2.0.1", "") {
		t.Fatal("Prepare applied before the commit")
	}
	commit()
	for _, sc := range DefaultScopes() {
		if allowed(s, sc, "10.1.0.1", "") {
			t.Errorf("%s: removed entry still allowed", sc)
		}
		if !allowed(s, sc, "10.2.0.1", "") || !allowed(s, sc, "192.168.1.1", "new.internal") {
			t.Errorf("%s: new entry not applied", sc)
		}
		if !allowed(s, sc, "10.7.0.1", "") {
			t.Errorf("%s: runtime list lost", sc)
		}
	}
	if allowed(s, ScopePersonalWebhooks, "10.2.0.1", "") || allowed(s, ScopePersonalWebhooks, "192.168.1.1", "new.internal") {
		t.Fatal("bootstrap reload widened personal webhooks")
	}
	// Invalid lists are refused and change nothing.
	for _, bad := range [][2][]string{
		{{"0.0.0.0/0"}, nil},
		{{"169.254.0.0/16"}, nil},
		{{"127.0.0.0/8"}, nil}, // loopback outside dev mode
		{nil, {"*.co.uk"}},
	} {
		if _, err := s.PrepareBootstrap(bad[0], bad[1]); err == nil {
			t.Errorf("PrepareBootstrap(%v) accepted", bad)
		}
	}
	if !allowed(s, ScopeSMTP, "10.2.0.1", "") {
		t.Fatal("a refused list replaced the current one")
	}
	// Loopback is allowlistable only when the Set was built in dev mode.
	dev := mustSet(t, Options{LoopbackAllowable: true})
	if _, err := dev.PrepareBootstrap([]string{"127.0.0.0/8"}, nil); err != nil {
		t.Fatalf("dev mode: %v", err)
	}
}

func TestPrepareBootstrapConcurrent(t *testing.T) {
	s := mustSet(t, Options{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 500 {
			allowed(s, ScopeDestinations, "10.2.0.1", "")
		}
	}()
	go func() {
		defer wg.Done()
		for range 500 {
			commit, err := s.PrepareBootstrap([]string{"10.2.0.0/16"}, nil)
			if err != nil {
				t.Error(err)
				return
			}
			commit()
		}
	}()
	wg.Wait()
}
