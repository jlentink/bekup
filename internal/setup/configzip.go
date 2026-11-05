package setup

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"time"

	"github.com/jlentink/bekup/internal/deployguard"
)

// configFiles are the deployment files without key material (D-217): the
// browser setup offers them as a zip. Never add a file of a secret group
// or below the keys directory (TestConfigFilesHoldNoSecrets). .env and
// compose.override.yaml are operator files that may hold the operator's
// own values (D-217 amendment 1): the zip carries only the .env variables
// setup manages, and the override only while it is exactly what setup
// wrote (configZipEntries).
var configFiles = []string{ComposeFile, OverrideFile, EnvFile, BekupTOML, RunnerTOML, ExternalAPITOML, StateFileName}

// maxConfigFile bounds each file read into the zip.
const maxConfigFile = 4 << 20

// ConfigFiles returns the names of the files WriteConfigZip may include.
func ConfigFiles() []string { return append([]string(nil), configFiles...) }

// managedEnvKeys are the .env variables setup writes (envVars,
// deploymentEnv): the only lines of .env the config zip carries.
func managedEnvKeys() map[string]bool {
	keys := map[string]bool{
		ImageEnvVar: true, deployguard.EnvSetupVersion: true, deployguard.EnvImageMode: true,
		"COMPOSE_PROFILES": true, ExternalAPIEnvVar: true, "BEKUP_DOMAIN": true,
		"PROXY_TLS_MODE": true, "ACME_EMAIL": true, "ACME_CA_SERVER": true,
		"TRAEFIK_ENTRYPOINT": true, "TRAEFIK_CERTRESOLVER": true,
		"BEKUP_PROXY_NETWORK": true, "SERVE_PROXY_IP": true, "BEKUP_DB_NETWORK": true,
		"DOCKER_GID": true, "BEKUP_KEYS_DIR": true,
	}
	n := DefaultNetwork()
	for _, v := range n.vars() {
		keys[v.name] = true
	}
	return keys
}

// zipEnvHeader starts the .env of the zip.
const zipEnvHeader = "# .env from the browser setup's zip (D-217): only the variables bekup setup\n" +
	"# manages. Lines of your own stay in the .env on the server.\n"

// filterEnv keeps the KEY=value lines of the managed keys, in their order.
// dropped reports a line (other than comments and blank lines) left out.
func filterEnv(data []byte) (out []byte, dropped bool) {
	keys := managedEnvKeys()
	var b bytes.Buffer
	b.WriteString(zipEnvHeader)
	for line := range strings.SplitSeq(string(data), "\n") {
		l := strings.TrimSpace(line)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if k, _, ok := strings.Cut(l, "="); ok && keys[k] {
			b.WriteString(l + "\n")
			continue
		}
		dropped = true
	}
	return b.Bytes(), dropped
}

// expectedOverride is the compose.override.yaml setup writes for the
// recorded overlays (renderOverride), nil when it writes none.
func expectedOverride(st *State) []byte {
	static := Proxy("")
	if (st.Proxy == ProxyTraefik || st.Proxy == ProxyNginx) && st.TLSMode == TLSStatic {
		static = st.Proxy
	}
	doc := overlayDoc(st.overlays(), static)
	if doc == "" {
		return nil
	}
	return []byte(overlayHeader + doc)
}

// zipEntry is one file of the config zip.
type zipEntry struct {
	name string
	data []byte
}

// configZipEntries reads the files of the config zip and the notes on
// what it leaves out.
func configZipEntries(dir string) ([]zipEntry, []string, error) {
	st, err := ReadState(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("no deployment in %s yet: %w", dir, err)
	}
	var entries []zipEntry
	var notes []string
	for _, name := range configFiles {
		data, err := readInput(dir, name, maxConfigFile)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", name, err)
		}
		switch name {
		case EnvFile:
			var dropped bool
			if data, dropped = filterEnv(data); dropped {
				notes = append(notes, EnvFile+": only the variables bekup setup manages; your own lines stay on the server")
			}
		case OverrideFile:
			if want := expectedOverride(st); want == nil || !bytes.Equal(data, want) {
				notes = append(notes, OverrideFile+": left out, it holds changes of your own (it may contain credentials); copy it from the server yourself")
				continue
			}
		}
		entries = append(entries, zipEntry{name: name, data: data})
	}
	return entries, notes, nil
}

// ConfigZipNotes reports what WriteConfigZip leaves out of the files
// present in dir (for the browser setup's results screen).
func ConfigZipNotes(dir string) ([]string, error) {
	_, notes, err := configZipEntries(dir)
	return notes, err
}

// WriteConfigZip writes the configuration files present in dir to w as a
// zip (absent ones are skipped; .env and compose.override.yaml as
// configZipEntries decides). Each file is read through an os.Root with
// O_NOFOLLOW per component (readInput): a symlink fails the zip.
func WriteConfigZip(dir string, w io.Writer, now time.Time) (retErr error) {
	entries, _, err := configZipEntries(dir)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	defer func() { retErr = errors.Join(retErr, zw.Close()) }()
	for _, e := range entries {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: "bekup/" + e.name, Method: zip.Deflate, Modified: now})
		if err != nil {
			return err
		}
		if _, err := f.Write(e.data); err != nil {
			return err
		}
	}
	return nil
}
