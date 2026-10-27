package spec

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/preamble"
)

var update = flag.Bool("update", false, "rewrite the golden spec snapshots")

const (
	testRunID = "0b7f5a3c-2c1e-4d7a-9a51-8c3f0e6d2b19"
	// sentinel is a secret that must never appear in a spec.
	sentinel = "Sup3r$ecret'\"\\pw"
)

func entry(t *testing.T, k engine.Kind, key string) engine.Entry {
	t.Helper()
	cat, err := engine.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	e, ok := cat.Lookup(k, key)
	if !ok {
		t.Fatalf("no %s %s", k, key)
	}
	return e
}

type scenario struct {
	name   string
	kind   engine.Kind
	key    string
	build  func(engine.Engine, engine.Entry, engine.Target) (engine.Command, error)
	target engine.Target
	alias  *HostAlias
	// volumes are the image's VOLUME paths (as the image inspect reports).
	volumes []string
}

func scenarios() []scenario {
	pgT := engine.Target{Host: "db.example.com", Port: 5432, User: "backup", TLS: engine.TLSPinned, ClientCert: true}
	myT := engine.Target{Host: "mysql.example.com", Port: 3306, User: "backup", TLS: engine.TLSPinned}
	return []scenario{
		{"postgres_dump", engine.KindPostgres, "17", func(e engine.Engine, c engine.Entry, t engine.Target) (engine.Command, error) {
			return e.Dump(c, t, "app_prod")
		}, pgT, &HostAlias{Name: "db.example.com", IP: netip.MustParseAddr("10.213.99.2")}, []string{"/var/lib/postgresql/data"}},
		{"mysql_restore", engine.KindMySQL, "8.4", func(e engine.Engine, c engine.Entry, t engine.Target) (engine.Command, error) {
			return e.Restore(c, t, "app", engine.RestoreOptions{})
		}, myT, &HostAlias{Name: "mysql.example.com", IP: netip.MustParseAddr("::ffff:10.213.99.3")}, []string{"/var/lib/mysql", "/tmp", "/var/lib/mysql"}},
		{"mariadb_dump_ip_literal", engine.KindMariaDB, "11.4", func(e engine.Engine, c engine.Entry, t engine.Target) (engine.Command, error) {
			t.Host = "10.20.0.5"
			return e.Dump(c, t, "app")
		}, myT, nil, nil},
	}
}

func params(t *testing.T, sc scenario) Params {
	t.Helper()
	ent := entry(t, sc.kind, sc.key)
	eng, err := engine.ForKind(sc.kind)
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := sc.build(eng, ent, sc.target)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	return Params{
		RunID: testRunID, Entry: ent, Command: cmd, Network: "bekup-run-0b7f5a3c",
		HostAlias: sc.alias, Limits: LimitsFromConfig(config.DefaultRunner().Docker),
		ImageVolumes: sc.volumes,
	}
}

func marshal(t *testing.T, s Spec) []byte {
	t.Helper()
	b, err := json.MarshalIndent(map[string]any{
		"Config": s.Config, "HostConfig": s.HostConfig, "NetworkingConfig": s.Networking,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

// TestSnapshots compares the full specs with golden files (run with
// -update after a reviewed change) and proves that no secret, in plain
// text or base64, reaches Env, Cmd, Entrypoint, Labels or any other field.
func TestSnapshots(t *testing.T) {
	for _, sc := range scenarios() {
		t.Run(sc.name, func(t *testing.T) {
			p := params(t, sc)
			s, err := Build(p)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			got := marshal(t, s)

			// The preamble that would accompany this spec carries the
			// sentinel; the spec must not.
			m := &preamble.Material{
				PGPass: []byte("db.example.com:5432:*:backup:" + sentinel + "\n"),
				MyCnf:  []byte("[client]\npassword=\"" + sentinel + "\"\n"),
				CA:     []byte("CA " + sentinel), ClientCert: []byte("CERT"), ClientKey: []byte("KEY " + sentinel),
			}
			recs, err := preamble.ForCommand(p.Command, m)
			if err != nil {
				t.Fatal(err)
			}
			enc, err := preamble.Encode(recs)
			if err != nil {
				t.Fatal(err)
			}
			for _, needle := range [][]byte{
				[]byte(sentinel),
				[]byte(base64.StdEncoding.EncodeToString([]byte(sentinel))),
				[]byte("Sup3r"),
				[]byte(base64.StdEncoding.EncodeToString(m.PGPass)),
			} {
				if bytes.Contains(got, needle) {
					t.Fatalf("spec contains secret material %q", needle)
				}
			}
			if !bytes.Contains(enc, []byte(base64.StdEncoding.EncodeToString(recs[0].Data))) {
				t.Fatal("test setup: preamble does not carry the material")
			}

			// Renovate bumps catalog digests; the snapshot pins the shape.
			got = bytes.ReplaceAll(got, []byte(p.Entry.Digest()), []byte("sha256:<catalog-digest>"))
			golden := filepath.Join("testdata", sc.name+".golden.json")
			if *update {
				if err := os.MkdirAll("testdata", 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, got, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden) //nolint:gosec // fixed test path
			if err != nil {
				t.Fatalf("read golden (run go test -update): %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("spec differs from %s (review, then run with -update):\n%s", golden, got)
			}
		})
	}
}

// TestHardening asserts every hardening field explicitly (independent of
// the snapshots).
func TestHardening(t *testing.T) {
	for _, sc := range scenarios() {
		t.Run(sc.name, func(t *testing.T) {
			p := params(t, sc)
			s, err := Build(p)
			if err != nil {
				t.Fatal(err)
			}
			c, h := s.Config, s.HostConfig
			uid := p.Entry.UID()
			if uid <= 0 {
				t.Fatal("root uid")
			}
			// Image by digest only, from the catalog entry.
			if c.Image != p.Entry.Repository()+"@"+p.Entry.Digest() || strings.Contains(strings.SplitN(c.Image, "@", 2)[0], ":") {
				t.Errorf("image %q is not digest-only", c.Image)
			}
			if !strings.HasPrefix(strings.SplitN(c.Image, "@", 2)[1], "sha256:") {
				t.Errorf("image %q has no sha256 digest", c.Image)
			}
			if c.User == "" || strings.HasPrefix(c.User, "0") || c.User == "root" {
				t.Errorf("user %q", c.User)
			}
			if !equal(c.Entrypoint, preamble.Entrypoint()) || !equal(c.Cmd, p.Command.Argv) {
				t.Errorf("entrypoint/cmd %q %q", c.Entrypoint, c.Cmd)
			}
			if !equal(c.Env, Env()) {
				t.Errorf("env %q", c.Env)
			}
			if c.Tty || !c.OpenStdin || !c.StdinOnce || !c.AttachStdin || c.NetworkDisabled {
				t.Error("stdio settings")
			}
			if len(c.Labels) != 2 || c.Labels[LabelManaged] != "true" || c.Labels[LabelRunID] != testRunID {
				t.Errorf("labels %v", c.Labels)
			}
			if c.StopTimeout == nil || *c.StopTimeout != StopTimeoutSeconds || c.Healthcheck == nil || c.Healthcheck.Test[0] != "NONE" {
				t.Error("stop timeout / healthcheck")
			}
			if len(c.Volumes) != 0 || len(c.ExposedPorts) != 0 || len(c.OnBuild) != 0 || len(c.Shell) != 0 {
				t.Error("volumes/ports/onbuild/shell set")
			}

			if !h.ReadonlyRootfs || h.Privileged || h.AutoRemove || h.PublishAllPorts {
				t.Error("rootfs/privileged/autoremove/publish")
			}
			if !equal(h.CapDrop, []string{"ALL"}) || len(h.CapAdd) != 0 {
				t.Errorf("caps %v %v", h.CapDrop, h.CapAdd)
			}
			if !equal(h.SecurityOpt, []string{"no-new-privileges:true"}) {
				t.Errorf("security opts %v", h.SecurityOpt)
			}
			for _, o := range h.SecurityOpt {
				if strings.Contains(o, "seccomp") || strings.Contains(o, "apparmor") || strings.Contains(o, "label") {
					t.Errorf("profile override %q", o)
				}
			}
			tmp := h.Tmpfs["/tmp"]
			for _, opt := range []string{"noexec", "nosuid", "nodev", "size=64m", "mode=0700"} {
				if !strings.Contains(tmp, opt) {
					t.Errorf("tmpfs %q lacks %s", tmp, opt)
				}
			}
			if len(h.Tmpfs) != 1 {
				t.Errorf("tmpfs %v", h.Tmpfs)
			}
			if len(h.Binds) != 0 || len(h.VolumesFrom) != 0 || h.VolumeDriver != "" {
				t.Error("mounts present")
			}
			// Only the read-only tmpfs over the image's VOLUME paths.
			for _, m := range h.Mounts {
				if m.Type != "tmpfs" || !m.ReadOnly || m.Source != "" {
					t.Errorf("mount %+v", m)
				}
			}
			if len(h.Devices) != 0 || len(h.DeviceRequests) != 0 || len(h.DeviceCgroupRules) != 0 {
				t.Error("devices present")
			}
			if len(h.PortBindings) != 0 || len(h.Links) != 0 || len(h.DNS) != 0 || len(h.Sysctls) != 0 || len(h.GroupAdd) != 0 {
				t.Error("ports/links/dns/sysctls/groups present")
			}
			if h.PidMode != "" || h.UTSMode != "" || h.UsernsMode != "" || h.IpcMode != container.IPCModePrivate || h.CgroupnsMode != container.CgroupnsModePrivate {
				t.Error("namespace modes")
			}
			// nil keeps Docker's default masked/read-only /proc paths.
			if h.MaskedPaths != nil || h.ReadonlyPaths != nil {
				t.Error("masked/readonly paths overridden")
			}
			if h.Runtime != "" || h.CgroupParent != "" || h.OomKillDisable != nil {
				t.Error("runtime/cgroup/oom overrides")
			}
			if string(h.NetworkMode) != p.Network || len(s.Networking.EndpointsConfig) != 1 || s.Networking.EndpointsConfig[p.Network] == nil {
				t.Error("network")
			}
			if h.Memory != 1024<<20 || h.MemorySwap != h.Memory || h.NanoCPUs != 1e9 || h.PidsLimit == nil || *h.PidsLimit != 256 {
				t.Errorf("limits mem=%d swap=%d cpus=%d", h.Memory, h.MemorySwap, h.NanoCPUs)
			}
			if h.LogConfig.Type != "none" || h.RestartPolicy.Name != container.RestartPolicyDisabled {
				t.Error("log/restart")
			}
			core := false
			for _, u := range h.Ulimits {
				if u.Name == "core" && u.Soft == 0 && u.Hard == 0 {
					core = true
				}
			}
			if !core {
				t.Error("core dumps not disabled")
			}
			if sc.alias != nil {
				want := sc.alias.Name + ":" + sc.alias.IP.Unmap().String()
				if !equal(h.ExtraHosts, []string{want}) {
					t.Errorf("extra hosts %v, want %s", h.ExtraHosts, want)
				}
			} else if len(h.ExtraHosts) != 0 {
				t.Error("extra hosts without alias")
			}
		})
	}
}

func equal(a, b []string) bool { return slices.Equal(a, b) }

// TestEnvIsConstant: the environment holds only fixed KEY=value pairs.
func TestEnvIsConstant(t *testing.T) {
	allowed := map[string]bool{
		"HOME": true, "PATH": true, "TZ": true, "PGPASSFILE": true, "PGSYSCONFDIR": true,
		"PGSERVICEFILE": true, "PSQLRC": true, "PSQL_HISTORY": true, "MYSQL_HISTFILE": true,
		"MYSQL_TEST_LOGIN_FILE": true,
	}
	for _, kv := range Env() {
		k, _, ok := strings.Cut(kv, "=")
		if !ok || !allowed[k] {
			t.Errorf("unexpected env %q", kv)
		}
		if strings.Contains(strings.ToUpper(k), "PASSWORD") || k == "MYSQL_PWD" || k == "PGPASSWORD" {
			t.Errorf("credential env %q", kv)
		}
	}
	e := Env()
	e[0] = "X=1"
	if Env()[0] == "X=1" {
		t.Error("Env returns the shared slice")
	}
}

func TestBuildInvalid(t *testing.T) {
	base := func(t *testing.T) Params { return params(t, scenarios()[0]) }
	other := entry(t, engine.KindPostgres, "16")
	tests := []struct {
		name string
		mut  func(*Params)
	}{
		{"run id", func(p *Params) { p.RunID = "abc" }},
		{"run id upper", func(p *Params) { p.RunID = strings.ToUpper(testRunID) }},
		{"zero entry", func(p *Params) { p.Entry = engine.Entry{} }},
		{"entry mismatch", func(p *Params) { p.Entry = other }},
		{"image override", func(p *Params) { p.Command.Image = "alpine:latest" }},
		{"empty argv", func(p *Params) { p.Command.Argv = nil }},
		{"shell argv", func(p *Params) { p.Command.Argv = []string{"sh", "-c", "id"} }},
		{"absolute tool", func(p *Params) { p.Command.Argv[0] = "/bin/pg_dump" }},
		{"NUL arg", func(p *Params) { p.Command.Argv = append(p.Command.Argv, "a\x00b") }},
		{"huge arg", func(p *Params) { p.Command.Argv = append(p.Command.Argv, strings.Repeat("a", maxArgBytes+1)) }},
		{"many args", func(p *Params) { p.Command.Argv = append(p.Command.Argv, make([]string, maxArgs)...) }},
		{"unknown file", func(p *Params) { p.Command.Files = append(p.Command.Files, "/etc/passwd") }},
		{"host network", func(p *Params) { p.Network = "host" }},
		{"bridge", func(p *Params) { p.Network = "bridge" }},
		{"container net", func(p *Params) { p.Network = "container:abc" }},
		{"none", func(p *Params) { p.Network = "none" }},
		{"alias ip name", func(p *Params) { p.HostAlias = &HostAlias{Name: "10.0.0.1", IP: netip.MustParseAddr("10.0.0.2")} }},
		{"alias bad name", func(p *Params) { p.HostAlias = &HostAlias{Name: "a:b", IP: netip.MustParseAddr("10.0.0.2")} }},
		{"alias no ip", func(p *Params) { p.HostAlias = &HostAlias{Name: "db.example.com"} }},
		{"alias unspecified", func(p *Params) { p.HostAlias = &HostAlias{Name: "db.example.com", IP: netip.IPv4Unspecified()} }},
		{"alias loopback", func(p *Params) {
			p.HostAlias = &HostAlias{Name: "db.example.com", IP: netip.MustParseAddr("127.0.0.1")}
		}},
		{"alias zone", func(p *Params) {
			p.HostAlias = &HostAlias{Name: "db.example.com", IP: netip.MustParseAddr("fe80::1%eth0")}
		}},
		{"memory", func(p *Params) { p.Limits.MemoryMiB = 1 }},
		{"cpus", func(p *Params) { p.Limits.CPUs = 0 }},
		{"cpus nan", func(p *Params) { p.Limits.CPUs = nan() }},
		{"pids", func(p *Params) { p.Limits.PidsLimit = 0 }},
		{"prefix on pg_dump", func(p *Params) { p.Command.StdinPrefix = engine.PSQLRestrictPrefix() }},
		{"psql without prefix", func(p *Params) {
			p.Command.Argv = []string{"psql", "--file=-"}
		}},
		{"psql with a forged prefix", func(p *Params) {
			p.Command.Argv, p.Command.StdinPrefix = []string{"psql", "--file=-"}, []byte("\\restrict abc\n")
		}},
		{"relative volume", func(p *Params) { p.ImageVolumes = []string{"var/lib/x"} }},
		{"unclean volume", func(p *Params) { p.ImageVolumes = []string{"/var/../etc"} }},
		{"root volume", func(p *Params) { p.ImageVolumes = []string{"/"} }},
		{"trailing slash volume", func(p *Params) { p.ImageVolumes = []string{"/data/"} }},
		{"NUL volume", func(p *Params) { p.ImageVolumes = []string{"/da\x00ta"} }},
		{"too many volumes", func(p *Params) { p.ImageVolumes = make([]string, MaxImageVolumes+1) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := base(t)
			tt.mut(&p)
			if _, err := Build(p); !errors.Is(err, ErrInvalidParams) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

// TestPSQLPrefixAccepted: a psql command with the engine's prefix builds.
func TestPSQLPrefixAccepted(t *testing.T) {
	p := params(t, scenarios()[0])
	eng, err := engine.ForKind(engine.KindPostgres)
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := eng.CreateDatabase(p.Entry, engine.Target{Host: "db.example.com", Port: 5432, User: "u", TLS: engine.TLSDisabled}, "x__bekup_restore_1")
	if err != nil {
		t.Fatal(err)
	}
	p.Command = cmd
	if _, err := Build(p); err != nil {
		t.Fatal(err)
	}
}

// TestImageVolumesCovered: every VOLUME path except /tmp gets exactly one
// read-only tmpfs mount; no bind or volume mount is ever created.
func TestImageVolumesCovered(t *testing.T) {
	p := params(t, scenarios()[0])
	p.ImageVolumes = []string{"/var/lib/postgresql/data", "/tmp", "/b", "/var/lib/postgresql/data"}
	s, err := Build(p)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(s.HostConfig.Mounts))
	for _, m := range s.HostConfig.Mounts {
		if m.Type != "tmpfs" || !m.ReadOnly || m.Source != "" || m.TmpfsOptions == nil || m.TmpfsOptions.SizeBytes != volumeTmpfsBytes {
			t.Errorf("mount %+v", m)
		}
		got = append(got, m.Target)
	}
	if strings.Join(got, ",") != "/b,/var/lib/postgresql/data" {
		t.Fatalf("targets %v", got)
	}
	if len(s.HostConfig.Binds) != 0 || len(s.HostConfig.VolumesFrom) != 0 || len(s.Config.Volumes) != 0 {
		t.Fatal("binds or volumes set")
	}
}

func nan() float64 {
	var z float64
	return z / z
}

// TestBuildDoesNotAlias: mutating the returned spec never changes the
// inputs or later specs.
func TestBuildDoesNotAlias(t *testing.T) {
	p := params(t, scenarios()[0])
	s, err := Build(p)
	if err != nil {
		t.Fatal(err)
	}
	s.Config.Cmd[0] = "evil"
	s.Config.Env[0] = "EVIL=1"
	s.Config.Entrypoint[2] = "evil"
	if p.Command.Argv[0] == "evil" || Env()[0] == "EVIL=1" || preamble.Entrypoint()[2] == "evil" {
		t.Error("spec aliases shared state")
	}
}

func TestImageRefAndLabels(t *testing.T) {
	e := entry(t, engine.KindMariaDB, "12.3")
	if got := ImageRef(e); got != "mariadb@"+e.Digest() {
		t.Errorf("ImageRef = %q", got)
	}
	if l := Labels(testRunID); l[LabelManaged] != "true" || l[LabelRunID] != testRunID {
		t.Errorf("labels %v", l)
	}
}
