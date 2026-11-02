package setup

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// preWebOverride is compose.override.yaml as releases before D-217 wrote
// it: the DB-network overlay without the setup-web-db block.
func preWebOverride(ov Overlays) string {
	return overlayHeader + renderOverlay(ov, "", []string{"migrate", "admin"})
}

// TestPreWebOverrideWarns: a deployment whose override predates the
// browser setup (D-217) lacks setup-web-db on the database container's
// network. check and upgrade warn with the block to add instead of
// failing (setup never rewrites an existing override, D-215); any other
// change of setup-web-db stays an error.
func TestPreWebOverrideWarns(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		db Database
		ov Overlays
	}{
		"database network": {Database{Location: DBDocker, Network: "dbnet", Host: "postgres", TLS: DBTLSDisable}, Overlays{DBNetwork: true}},
		"proxy network":    {Database{Location: DBDocker, Network: "web", Host: "postgres", TLS: DBTLSRequire}, Overlays{DBNetwork: true, DBOnProxy: true}},
	} {
		t.Run("compose-first "+name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeReleaseDB(t, dir, VariantOwnProxy, DBExternal, variantImage)
			if out, err := composeFirstInit(t, dir, VariantOwnProxy, func(o *Options) { o.Answers.DB = c.db }); err != nil {
				t.Fatalf("init: %v\n%s", err, out)
			}
			if err := os.WriteFile(pathIn(dir, OverrideFile), []byte(preWebOverride(c.ov)), 0o600); err != nil {
				t.Fatal(err)
			}
			fs, report, err := runCheck(t, dir, nil)
			if err != nil || hasErrors(fs) || !hasFinding(fs, SeverityWarning, "setup-web-db:\n    networks:") {
				t.Fatalf("check of a pre-D-217 override: %v\n%s", err, report)
			}
			o := upgradeOptions(t, dir, variantImage)
			o.WriteCompose = false
			if err := Upgrade(o); err != nil {
				t.Fatalf("upgrade of a pre-D-217 override: %v", err)
			}
			if out := bufOf(t, o.Out).String(); !strings.Contains(out, "setup-web-db") {
				t.Fatalf("upgrade does not name the missing block:\n%s", out)
			}
			if got := string(readFile(t, dir, OverrideFile)); got != preWebOverride(c.ov) {
				t.Fatalf("upgrade rewrote the override:\n%s", got)
			}
			// setup-web-db on another network is still refused.
			bad := strings.Replace(preWebOverride(c.ov), "services:\n", "services:\n  setup-web-db:\n    networks:\n      edge: {}\n", 1)
			if err := os.WriteFile(pathIn(dir, OverrideFile), []byte(bad), 0o600); err != nil {
				t.Fatal(err)
			}
			if fs, report, err := runCheck(t, dir, nil); !errors.Is(err, ErrCheckFailed) || !hasFinding(fs, SeverityError, `"setup-web-db"`) {
				t.Fatalf("setup-web-db on another network:\n%s", report)
			}
			// The current override passes without the warning.
			if err := os.WriteFile(pathIn(dir, OverrideFile), []byte(overlayHeader+overlayDoc(c.ov, "")), 0o600); err != nil {
				t.Fatal(err)
			}
			if fs, report, err := runCheck(t, dir, nil); err != nil || hasErrors(fs) || hasFinding(fs, SeverityWarning, "setup-web-db") {
				t.Fatalf("current override:\n%s", report)
			}
		})
	}
	t.Run("setup-first", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		a := testAnswers(t, ProxyNone)
		ownProxyJoin(&a)
		a.DB = Database{Mode: DBExternal, Location: DBDocker, Network: "dbnet", Host: "postgres", TLS: DBTLSDisable}
		mustInit(t, dir, a)
		ov := Overlays{DBNetwork: true}
		if err := os.WriteFile(pathIn(dir, OverrideFile), []byte(preWebOverride(ov)), 0o600); err != nil {
			t.Fatal(err)
		}
		fs, report, err := runCheck(t, dir, nil)
		if err != nil || hasErrors(fs) || !hasFinding(fs, SeverityWarning, "setup-web-db:\n    networks:") {
			t.Fatalf("check of a pre-D-217 override: %v\n%s", err, report)
		}
		o := upgradeOptions(t, dir, a.Image.String())
		if err := Upgrade(o); err != nil {
			t.Fatalf("upgrade of a pre-D-217 override: %v", err)
		}
		if out := bufOf(t, o.Out).String(); !strings.Contains(out, "setup-web-db") {
			t.Fatalf("upgrade does not name the missing block:\n%s", out)
		}
		o2 := testOptions(t, dir, a)
		o2.Force = true
		if err := Init(o2); err != nil {
			t.Fatalf("init --force with a pre-D-217 override: %v", err)
		}
		if out := bufOf(t, o2.Out).String(); !strings.Contains(out, "setup-web-db") {
			t.Fatalf("init does not name the missing block:\n%s", out)
		}
	})
}
