package spec

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/engine"
)

func offlineParams(t *testing.T) Params {
	t.Helper()
	ent := entry(t, engine.KindPostgres, "17")
	eng, err := engine.ForKind(engine.KindPostgres)
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := eng.VerifyDump(ent)
	if err != nil {
		t.Fatal(err)
	}
	return Params{RunID: testRunID, Entry: ent, Command: cmd, Offline: true, Limits: LimitsFromConfig(config.DefaultRunner().Docker)}
}

func TestBuildOffline(t *testing.T) {
	s, err := Build(offlineParams(t))
	if err != nil {
		t.Fatal(err)
	}
	if s.HostConfig.NetworkMode != NetworkNone {
		t.Fatalf("network mode = %q", s.HostConfig.NetworkMode)
	}
	if len(s.Networking.EndpointsConfig) != 0 || len(s.HostConfig.ExtraHosts) != 0 {
		t.Fatal("offline spec has endpoints or extra hosts")
	}
	// The hardening is the same as for networked runs.
	if !s.HostConfig.ReadonlyRootfs || len(s.HostConfig.CapDrop) != 1 || s.HostConfig.Privileged {
		t.Fatal("offline spec lost hardening")
	}
}

func TestBuildOfflineInvalid(t *testing.T) {
	tests := map[string]func(*Params){
		"network":    func(p *Params) { p.Network = "bekup-run-0b7f5a3c" },
		"host alias": func(p *Params) { p.HostAlias = &HostAlias{Name: "db.example.com", IP: netip.MustParseAddr("10.0.0.2")} },
		"files":      func(p *Params) { p.Command.Files = []engine.File{engine.FilePGPass} },
	}
	for name, mut := range tests {
		t.Run(name, func(t *testing.T) {
			p := offlineParams(t)
			mut(&p)
			if _, err := Build(p); !errors.Is(err, ErrInvalidParams) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}
