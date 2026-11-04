//go:build integration

package setup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jlentink/bekup/internal/testutil/dockertest"
)

// TestIntegrationExternalRoleCommand (D-220 amendment 4) runs the bundled
// postgres service of a deployment setup wrote, as docker compose starts
// it: a new volume without BEKUP_EXTERNAL_API=enabled creates no
// bekup_external, and the exact command setup upgrade --external-api
// prints (bundledRoleCommand: `docker compose exec` as the service user
// 70, the superuser password from its secret through PGPASSWORD) creates
// it on that existing volume once postgres runs with the variable,
// including the pg_hba rule. Only the postgres service and its backend
// network are created, under a project name of its own, and removed.
func TestIntegrationExternalRoleCommand(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		dockertest.Skipf(t, "docker CLI not found: %v", err)
	}
	vctx, vcancel := context.WithTimeout(context.Background(), time.Minute)
	defer vcancel()
	if out, err := exec.CommandContext(vctx, "docker", "compose", "version").CombinedOutput(); err != nil {
		dockertest.Skipf(t, "docker compose not available: %v: %s", err, out)
	}
	dir := t.TempDir()
	a := testAnswers(t, ProxyTraefik)
	a.ExternalAPI = true
	mustInit(t, dir, a)
	// The test runs without --ownership chown: let the postgres user (70)
	// read the secrets and certificates of this throwaway directory.
	err := filepath.WalkDir(pathIn(dir, DefaultKeysDir), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if d.IsDir() {
			mode = 0o755
		}
		return os.Chmod(p, mode) //nolint:gosec // G122: a test tree below t.TempDir() that only this test writes
	})
	if err != nil {
		t.Fatal(err)
	}
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	project := "bekupit" + hex.EncodeToString(suffix)
	compose := func(ctx context.Context, extra []string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "docker", append([]string{"compose"}, args...)...) //nolint:gosec // G204: fixed compose arguments of this test
		cmd.Dir = dir
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "COMPOSE_PROJECT_NAME=" + project}, extra...)
		if v, ok := os.LookupEnv("DOCKER_HOST"); ok {
			cmd.Env = append(cmd.Env, "DOCKER_HOST="+v)
		}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if out, err := compose(ctx, nil, "down", "-v", "--remove-orphans"); err != nil {
			t.Logf("docker compose down: %v\n%s", err, out)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	// A shell variable overrides .env: the volume is initialized as before
	// the external API was enabled.
	if out, err := compose(ctx, []string{"BEKUP_EXTERNAL_API="}, "up", "-d", "--wait", "postgres"); err != nil {
		t.Fatalf("up postgres: %v\n%s", err, out)
	}
	if out, err := compose(ctx, nil, "logs", "postgres"); err != nil || !strings.Contains(out, "the external API is not enabled") {
		t.Fatalf("init logs: %v\n%s", err, out)
	}
	roleQuery := `PGPASSWORD="$(cat /run/secrets/postgres_password)" psql -U postgres -d postgres -tAc "SELECT rolcanlogin, rolconnlimit FROM pg_catalog.pg_roles WHERE rolname = 'bekup_external'"`
	if out, err := compose(ctx, nil, "exec", "-T", "postgres", "sh", "-c", roleQuery); err != nil || strings.TrimSpace(out) != "" {
		t.Fatalf("bekup_external before the command: %v %q", err, out)
	}
	// .env says enabled: up -d recreates postgres with the variable (the
	// first printed step), then the printed command creates the role.
	if out, err := compose(ctx, nil, "up", "-d", "--wait", "postgres"); err != nil {
		t.Fatalf("recreate postgres: %v\n%s", err, out)
	}
	cmdLine := strings.Replace(bundledRoleCommand, "docker compose exec postgres", "docker compose exec -T postgres", 1)
	sh := exec.CommandContext(ctx, "sh", "-c", cmdLine) //nolint:gosec // G204: the constant documented command
	sh.Dir = dir
	sh.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "COMPOSE_PROJECT_NAME=" + project}
	if v, ok := os.LookupEnv("DOCKER_HOST"); ok {
		sh.Env = append(sh.Env, "DOCKER_HOST="+v)
	}
	if out, err := sh.CombinedOutput(); err != nil || !strings.Contains(string(out), "role bekup_external ready") {
		t.Fatalf("the documented command: %v\n%s", err, out)
	}
	if out, err := compose(ctx, nil, "exec", "-T", "postgres", "sh", "-c", roleQuery); err != nil || strings.TrimSpace(out) != "t|20" {
		t.Fatalf("bekup_external after the command: %v %q", err, out)
	}
	if out, err := compose(ctx, nil, "exec", "-T", "postgres", "sh", "-c", `id -u; head -n 2 "$PGDATA/pg_hba.conf"`); err != nil ||
		!strings.HasPrefix(out, "70\n") || !strings.Contains(out, "hostnossl  all  bekup_external  all  reject") {
		t.Fatalf("user and pg_hba.conf: %v\n%s", err, out)
	}
}
