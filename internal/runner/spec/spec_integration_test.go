//go:build integration

package spec

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/runner/preamble"
	"github.com/jlentink/bekup/internal/testutil/dockertest"
)

// checkScript runs as "$@" after the preamble wrapper (in place of the
// tool) and reports what the wrapper produced: file modes, owners and
// hashes, the payload hash, whether printf is a builtin, the user, and
// whether the root file system and /tmp are locked down.
const checkScript = `cd /tmp/bekup
for f in pgpass my.cnf ca.pem client-cert.pem client-key.pem; do
  if [ -e "$f" ]; then echo "file $f $(stat -c '%a %u' "$f") $(sha256sum <"$f" | cut -d' ' -f1)"; fi
done
echo "dir $(stat -c '%a %u' /tmp/bekup)"
echo "payload $(sha256sum | cut -d' ' -f1)"
echo "printf $(command -v printf)"
echo "id $(id -u):$(id -g)"
if touch /etc/bekup-write-test 2>/dev/null; then echo "rootfs writable"; else echo "rootfs readonly"; fi
cp /bin/sh /tmp/sh 2>/dev/null && { /tmp/sh -c true 2>/dev/null && echo "tmp exec" || echo "tmp noexec"; }
env
`

func dockerClient(t *testing.T) *client.Client {
	t.Helper()
	c, err := client.New(client.FromEnv)
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	t.Cleanup(func() { discardErr(c.Close()) })
	if _, err := c.Ping(t.Context(), client.PingOptions{}); err != nil {
		dockertest.Skipf(t, "docker not available: %v", err)
	}
	return c
}

type runResult struct {
	exit           int64
	stdout, stderr string
}

// run creates the container from s, attaches, feeds stdin, waits and
// removes it.
func run(t *testing.T, c *client.Client, s Spec, stdin []byte) runResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	created, err := c.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: s.Config, HostConfig: s.HostConfig, NetworkingConfig: s.Networking,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := created.ID
	t.Cleanup(func() {
		_, err := c.ContainerRemove(context.Background(), id, client.ContainerRemoveOptions{Force: true})
		discardErr(err)
	})
	att, err := c.ContainerAttach(ctx, id, client.ContainerAttachOptions{Stream: true, Stdin: true, Stdout: true, Stderr: true})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer att.Close()
	if _, err := c.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	go func() {
		_, err := att.Conn.Write(stdin)
		discardErr(err)
		discardErr(att.CloseWrite())
	}()
	var out, errb bytes.Buffer
	if _, err := stdcopy.StdCopy(&out, &errb, att.Reader); err != nil {
		t.Fatalf("read output: %v", err)
	}
	w := c.ContainerWait(ctx, id, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case r := <-w.Result:
		return runResult{exit: r.StatusCode, stdout: out.String(), stderr: errb.String()}
	case err := <-w.Error:
		t.Fatalf("wait: %v", err)
	}
	return runResult{}
}

// discardErr ignores errors of best-effort cleanup.
func discardErr(error) {}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// TestPreambleInImages runs the hardened spec with the real preamble
// wrapper in every pinned image (busybox ash, dash and bash) and checks the
// written files, their modes and owner, the untouched payload, and that
// no secret reaches the environment.
func TestPreambleInImages(t *testing.T) {
	c := dockerClient(t)
	cat, err := engine.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	netName := "bekup-it-" + strings.ToLower(rand.Text()[:10])
	if _, err := c.NetworkCreate(t.Context(), netName, client.NetworkCreateOptions{
		Internal: true, Labels: map[string]string{LabelManaged: "true", LabelRunID: testRunID},
	}); err != nil {
		t.Fatalf("network: %v", err)
	}
	t.Cleanup(func() {
		_, err := c.NetworkRemove(context.Background(), netName, client.NetworkRemoveOptions{})
		discardErr(err)
	})

	allBytes := make([]byte, 256)
	for i := range allBytes {
		allBytes[i] = byte(i)
	}
	big := make([]byte, preamble.MaxRecordBytes)
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	m := &preamble.Material{
		PGPass:     []byte("db.example.com:5432:*:backup:" + sentinel + "\\:$HOME`id`\n"),
		MyCnf:      []byte("[client]\npassword=\"" + sentinel + " $(id) \\\" ' \"\nnext line\n"),
		CA:         big,
		ClientCert: allBytes,
		ClientKey:  []byte("-----BEGIN PRIVATE KEY-----\n" + sentinel + "\n-----END PRIVATE KEY-----\n"),
	}
	recs := []preamble.Record{
		{Type: preamble.RecordPGPass, Data: m.PGPass},
		{Type: preamble.RecordMyCnf, Data: m.MyCnf},
		{Type: preamble.RecordCA, Data: m.CA},
		{Type: preamble.RecordClientCert, Data: m.ClientCert},
		{Type: preamble.RecordClientKey, Data: m.ClientKey},
	}
	enc, err := preamble.Encode(recs)
	if err != nil {
		t.Fatal(err)
	}
	payload := append([]byte("PGDMP\x00binary payload\nEND\nBEKUP-PREAMBLE 1\n"), big[:4096]...)

	for _, ent := range cat.Entries() {
		t.Run(string(ent.Kind())+"-"+ent.Key(), func(t *testing.T) {
			t.Parallel()
			// ContainerCreate never pulls: without this the test passes
			// only when another test pulled the image first.
			ensureImage(t, c, ImageRef(ent))
			eng, err := engine.ForKind(ent.Kind())
			if err != nil {
				t.Fatal(err)
			}
			cmd, err := eng.Dump(ent, engine.Target{Host: "db.example.com", Port: 5432, User: "backup", TLS: engine.TLSDisabled}, "app")
			if err != nil {
				t.Fatal(err)
			}
			p := Params{RunID: testRunID, Entry: ent, Command: cmd, Network: netName,
				Limits: LimitsFromConfig(config.DefaultRunner().Docker)}
			s, err := Build(p)
			if err != nil {
				t.Fatal(err)
			}
			uid := strconv.Itoa(ent.UID())

			// 1. The wrapper writes every record; the check script runs
			// in place of the tool with the same hardening.
			s.Config.Cmd = []string{"sh", "-c", checkScript}
			r := run(t, c, s, append(append([]byte(nil), enc...), payload...))
			if r.exit != 0 {
				t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", r.exit, r.stdout, r.stderr)
			}
			want := []string{
				"file pgpass 600 " + uid + " " + sum(m.PGPass),
				"file my.cnf 600 " + uid + " " + sum(m.MyCnf),
				"file ca.pem 600 " + uid + " " + sum(m.CA),
				"file client-cert.pem 600 " + uid + " " + sum(m.ClientCert),
				"file client-key.pem 600 " + uid + " " + sum(m.ClientKey),
				"dir 700 " + uid,
				"payload " + sum(payload),
				"printf printf", // a shell builtin: data never in a child's argv
				"id " + uid + ":" + uid,
				"rootfs readonly",
				"tmp noexec",
			}
			for _, w := range want {
				if !strings.Contains(r.stdout, w+"\n") {
					t.Errorf("missing %q in output:\n%s", w, r.stdout)
				}
			}
			if strings.Contains(r.stdout, sentinel) || strings.Contains(r.stderr, sentinel) {
				t.Errorf("secret visible in environment or output")
			}

			// 2. The real tool starts under the hardening.
			s2, err := Build(p)
			if err != nil {
				t.Fatal(err)
			}
			s2.Config.Cmd = []string{ent.Binaries().Dump, "--version"}
			r = run(t, c, s2, enc)
			version, _, _ := strings.Cut(ent.Tag(), "-")
			if r.exit != 0 || !strings.Contains(r.stdout, version) {
				t.Errorf("tool --version: exit %d stdout %q stderr %q", r.exit, r.stdout, r.stderr)
			}

			// 3. A malformed preamble stops before the tool runs.
			for name, bad := range map[string]string{
				"header":    "BEKUP-PREAMBLE 2\nEND\n",
				"type":      preamble.Header + "\npasswd 1\nAA==\nEND\n",
				"length":    preamble.Header + "\nca 2\nAA==\nEND\n",
				"zero lead": preamble.Header + "\nca 01\nAA==\nEND\n",
				"encoding":  preamble.Header + "\nca 1\n$(id)\nEND\n",
				"truncated": preamble.Header + "\nca 1\n",
				"duplicate": preamble.Header + "\nca 1\nAA==\nca 1\nAA==\nEND\n",
				"too large": preamble.Header + "\nca 65537\nAA==\nEND\n",
				"spaces":    preamble.Header + "\nca  1\nAA==\nEND\n",
			} {
				s3, err := Build(p)
				if err != nil {
					t.Fatal(err)
				}
				s3.Config.Cmd = []string{"sh", "-c", "echo TOOL-RAN"}
				r := run(t, c, s3, []byte(bad))
				if r.exit != preamble.ExitCode || strings.Contains(r.stdout, "TOOL-RAN") {
					t.Errorf("%s: exit %d stdout %q stderr %q", name, r.exit, r.stdout, r.stderr)
				}
				if !strings.HasPrefix(r.stderr, "bekup-preamble: ") || strings.Contains(r.stderr, "AA==") {
					t.Errorf("%s: stderr %q", name, r.stderr)
				}
			}
		})
	}
}

// pullLocks serializes ensureImage per image reference (parallel
// subtests and tests sharing an image pull it once).
var pullLocks struct {
	sync.Mutex
	byRef map[string]*sync.Mutex
}

func pullLock(ref string) *sync.Mutex {
	pullLocks.Lock()
	defer pullLocks.Unlock()
	if pullLocks.byRef == nil {
		pullLocks.byRef = map[string]*sync.Mutex{}
	}
	mu := pullLocks.byRef[ref]
	if mu == nil {
		mu = new(sync.Mutex)
		pullLocks.byRef[ref] = mu
	}
	return mu
}

// ensureImage pulls ref when it is not present.
func ensureImage(t *testing.T, c *client.Client, ref string) client.ImageInspectResult {
	t.Helper()
	mu := pullLock(ref)
	mu.Lock()
	defer mu.Unlock()
	if ins, err := c.ImageInspect(t.Context(), ref); err == nil {
		return ins
	}
	resp, err := c.ImagePull(t.Context(), ref, client.ImagePullOptions{})
	if err != nil {
		t.Fatalf("pull %s: %v", ref, err)
	}
	if err := resp.Wait(t.Context()); err != nil {
		t.Fatalf("pull %s: %v", ref, err)
	}
	ins, err := c.ImageInspect(t.Context(), ref)
	if err != nil {
		t.Fatalf("inspect %s: %v", ref, err)
	}
	return ins
}

// TestImageVolumesInImages: every pinned image declares a VOLUME (the
// data directory); the spec covers each with a read-only tmpfs, so Docker
// creates no anonymous volume and the tool cannot write there.
func TestImageVolumesInImages(t *testing.T) {
	c := dockerClient(t)
	cat, err := engine.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, ent := range cat.Entries() {
		t.Run(string(ent.Kind())+"-"+ent.Key(), func(t *testing.T) {
			t.Parallel()
			ins := ensureImage(t, c, ImageRef(ent))
			var vols []string
			if ins.Config != nil {
				for v := range ins.Config.Volumes {
					vols = append(vols, v)
				}
			}
			if len(vols) == 0 {
				t.Fatal("the image declares no VOLUME; the test would prove nothing")
			}
			// The data directory the tool would otherwise write to.
			probe := "/var/lib/mysql"
			if ent.Kind() == engine.KindPostgres {
				probe = "/var/lib/postgresql/data"
			}
			eng, err := engine.ForKind(ent.Kind())
			if err != nil {
				t.Fatal(err)
			}
			cmd, err := eng.Dump(ent, engine.Target{Host: "db.example.com", Port: 5432, User: "backup", TLS: engine.TLSDisabled}, "app")
			if err != nil {
				t.Fatal(err)
			}
			cmd.Files = nil
			s, err := Build(Params{RunID: testRunID, Entry: ent, Command: cmd, Offline: true,
				Limits: LimitsFromConfig(config.DefaultRunner().Docker), ImageVolumes: vols})
			if err != nil {
				t.Fatal(err)
			}
			script := `for d in "$@"; do mkdir -p "$d" 2>/dev/null; if touch "$d/bekup-write-test" 2>/dev/null; then echo "writable $d"; else echo "readonly $d"; fi; done`
			s.Config.Cmd = append([]string{"sh", "-c", script, "sh", probe}, vols...)
			enc, err := preamble.Encode(nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			created, err := c.ContainerCreate(ctx, client.ContainerCreateOptions{Config: s.Config, HostConfig: s.HostConfig, NetworkingConfig: s.Networking})
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			cins, err := c.ContainerInspect(ctx, created.ID, client.ContainerInspectOptions{})
			_, rerr := c.ContainerRemove(context.Background(), created.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
			discardErr(rerr)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range cins.Container.Mounts {
				if m.Type != "tmpfs" {
					t.Errorf("mount %s of type %s (an anonymous volume?)", m.Destination, m.Type)
				}
			}
			r := run(t, c, s, enc)
			if r.exit != 0 {
				t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", r.exit, r.stdout, r.stderr)
			}
			if strings.Contains(r.stdout, "writable") || !strings.Contains(r.stdout, "readonly "+probe+"\n") {
				t.Fatalf("a volume path is writable:\n%s", r.stdout)
			}
		})
	}
}
