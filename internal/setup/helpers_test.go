package setup

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/imageref"
)

const (
	testDigest  = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testDigest2 = "sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	testPass    = "correct horse battery staple x"
)

func mustRef(t testing.TB, s string) imageref.Ref {
	t.Helper()
	r, err := imageref.ParsePinned(s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func testAnswers(t testing.TB, proxy Proxy) Answers {
	a := Answers{
		Image:       mustRef(t, "ghcr.io/jlentink/bekup:1.2.3@"+testDigest),
		Proxy:       proxy,
		Domain:      "backup.example.com",
		DockerGID:   999,
		TargetCIDRs: []string{"10.20.0.0/16"},
		TargetHosts: []string{"db.internal.example.com"},
	}
	switch proxy {
	case ProxyNone:
		a.TrustedProxies = []string{"10.213.250.6/32"}
	default:
		a.TLSMode = TLSACME
		a.ACMEEmail = "ops@example.com"
	}
	return a
}

// fakeSys records chmod/chown calls and keeps virtual owners: lchown only
// records the new owner, and chmod of an entry whose (virtual) owner is not
// euid fails with EPERM, like the kernel without CAP_FOWNER. Entries
// without a recorded owner belong to euid. failOn makes chmod/lchown of the
// given paths fail with that error (offline media without Unix modes).
type fakeSys struct {
	mu     sync.Mutex
	mounts []Mount
	euid   int
	calls  []string
	owners map[string][2]int
	failOn map[string]error
}

func (f *fakeSys) ownerOf(rel string) (int, int) {
	if o, ok := f.owners[rel]; ok {
		return o[0], o[1]
	}
	return f.euid, f.euid
}

// setOwner records a virtual owner (a tree already handed to the
// containers).
func (f *fakeSys) setOwner(rel string, uid, gid int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.owners == nil {
		f.owners = map[string][2]int{}
	}
	f.owners[rel] = [2]int{uid, gid}
}

func (f *fakeSys) env() *sysEnv {
	return &sysEnv{
		mountinfo: func() ([]Mount, error) { return f.mounts, nil },
		euid:      func() int { return f.euid },
		owner: func(_ *os.Root, rel string, _ fs.FileInfo) (int, int, bool) {
			f.mu.Lock()
			defer f.mu.Unlock()
			uid, gid := f.ownerOf(rel)
			return uid, gid, true
		},
		lchown: func(_ *os.Root, rel string, _ bool, uid, gid int) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.calls = append(f.calls, fmt.Sprintf("chown %s %d:%d", rel, uid, gid))
			if err := f.failOn[rel]; err != nil {
				return err
			}
			if f.owners == nil {
				f.owners = map[string][2]int{}
			}
			_, oldGID := f.ownerOf(rel)
			if gid == -1 {
				gid = oldGID
			}
			f.owners[rel] = [2]int{uid, gid}
			return nil
		},
		chmod: func(r *os.Root, rel string, dir bool, m fs.FileMode) error {
			f.mu.Lock()
			f.calls = append(f.calls, fmt.Sprintf("chmod %s %04o", rel, m.Perm()))
			uid, _ := f.ownerOf(rel)
			fail := f.failOn[rel]
			f.mu.Unlock()
			if fail != nil {
				return fail
			}
			if uid != f.euid {
				return &fs.PathError{Op: "chmod", Path: rel, Err: syscall.EPERM}
			}
			return entryChmod(r, rel, dir, m)
		},
	}
}

// fastCerts skips the scrypt sealing of the CA key in most tests (the
// passphrase path is covered by TestInitRealPassphrase).
func fastCerts(o certs.InitOptions) (*certs.Result, error) {
	o.Passphrase = nil
	o.AllowUnencrypted = true
	return certs.Init(o)
}

func testOptions(t testing.TB, dir string, a Answers) Options {
	t.Helper()
	sys := &fakeSys{euid: 1000}
	return Options{
		Dir: dir, Version: "1.2.3", Answers: a, Ownership: OwnershipNone, WriteCompose: true,
		CAPassphrase: func() ([]byte, error) { return []byte(testPass), nil },
		Out:          &bytes.Buffer{},
		Now:          func() time.Time { return time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC) },
		sys:          sys.env(),
		certsInit:    fastCerts,
	}
}

func mustInit(t testing.TB, dir string, a Answers) *bytes.Buffer {
	t.Helper()
	o := testOptions(t, dir, a)
	if err := Init(o); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return bufOf(t, o.Out)
}

func readFile(t testing.TB, dir, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(pathIn(dir, rel)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// snapshot maps every regular file below dir to its content + mode.
func snapshot(t testing.TB, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := mustRel(t, dir, p)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			b, err := fs.ReadFile(os.DirFS(dir), rel)
			if err != nil {
				return err
			}
			out[rel] = info.Mode().String() + "|" + string(b)
		} else if d.IsDir() {
			out[rel+"/"] = info.Mode().String()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func diffSnapshots(a, b map[string]string) []string {
	var out []string
	for k, v := range a {
		if w, ok := b[k]; !ok {
			out = append(out, "removed "+k)
		} else if v != w {
			out = append(out, "changed "+k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			out = append(out, "added "+k)
		}
	}
	return out
}

// repoRoot is the repository root (two levels up).
func repoRoot(t testing.TB) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func hasLine(s, line string) bool {
	for _, l := range strings.Split(s, "\n") {
		if l == line {
			return true
		}
	}
	return false
}

func bufOf(t testing.TB, w io.Writer) *bytes.Buffer {
	t.Helper()
	b, ok := w.(*bytes.Buffer)
	if !ok {
		t.Fatalf("output is %T, not a buffer", w)
	}
	return b
}

func mustState(t testing.TB, dir string) *State {
	t.Helper()
	st, err := readState(dir)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func saveState(t testing.TB, dir string, st *State) {
	t.Helper()
	data, err := st.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathIn(dir, StateFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustStat(t testing.TB, p string) fs.FileInfo {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

func mustRel(t testing.TB, base, p string) string {
	t.Helper()
	rel, err := filepath.Rel(base, p)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(rel)
}

// pathIn maps a slash-separated relative path below dir.
func pathIn(dir, rel string) string {
	return filepath.Join(dir, filepath.FromSlash(rel))
}
