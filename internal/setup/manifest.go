package setup

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// ManifestVersion is the version of the ownership manifest. Bump it when an
// entry is added or changes owner/mode; `setup upgrade` then generates new
// secrets and re-applies ownership.
const ManifestVersion = 1

// Container UIDs/GIDs the files are handed to.
const (
	// UIDBekup is distroless "nonroot", the user of every bekup container.
	UIDBekup = 65532
	// UIDPostgres is the postgres user of the alpine image.
	UIDPostgres = 70
)

// Class is the life-cycle class of a manifest entry.
type Class string

// Classes.
const (
	// ClassGenerated files are written by init and rewritten by upgrade
	// (deploy/**, reference/*, the state file, and setup-first the compose
	// file).
	ClassGenerated Class = "generated-upgradable"
	// ClassOperator files are written once by init and then belong to the
	// operator (.env, the TOML files, the override file), or never written
	// by setup at all (compose-first: compose.yaml, D-205).
	ClassOperator Class = "operator"
	// ClassSecret entries hold key material; their contents are never read
	// by check or upgrade.
	ClassSecret Class = "secret"
)

// Kind is the file type of an entry.
type Kind string

// Kinds.
const (
	KindDir  Kind = "dir"
	KindFile Kind = "file"
)

// Entry is one path of the deployment directory.
type Entry struct {
	// Path is slash-separated and relative to the deployment directory.
	Path  string
	Kind  Kind
	Mode  fs.FileMode
	UID   int
	GID   int
	Class Class
	// Secret marks key material (never read by check/upgrade).
	Secret bool
	// Group ties secret entries that are generated together (all or none).
	Group string
	// Optional entries may be absent (the CA directory once moved offline,
	// the override file without static TLS).
	Optional bool
	// Offline marks the CA key material: expected to be moved off the host.
	Offline bool
	// ModeIsMax makes Mode an upper bound: the operator may narrow it
	// (the deployment directory itself), setup only removes extra bits.
	ModeIsMax bool
}

// Secret groups.
const (
	groupMasterKey = "master-key"
	groupDatabase  = "database"
	groupCerts     = "certs"
)

// Layout resolves the paths of a deployment directory.
type Layout struct {
	// KeysDir is the secrets directory, relative (default "secrets").
	KeysDir string
}

func (l Layout) keys(p ...string) string {
	return path.Join(append([]string{l.KeysDir}, p...)...)
}

// Fixed file names of the deployment directory (names, not credentials).
//
//nolint:gosec // G101: these are file names of the secrets directory, not secret values
const (
	ComposeFile      = "compose.yaml"
	OverrideFile     = "compose.override.yaml"
	EnvFile          = ".env"
	BekupTOML        = "bekup.toml"
	RunnerTOML       = "runner.toml"
	StateFileName    = "bekup-setup.json"
	ReferenceDir     = "reference"
	DeployDir        = "deploy"
	ProxyTLSDir      = "proxy-tls"
	certsDirName     = "certs"
	caDirName        = "ca"
	caKeyName        = "ca.key"
	masterKeyName    = "bekup_master_key"
	appURLName       = "bekup_database_url"
	ownerURLName     = "bekup_database_owner_url"
	postgresPassName = "postgres_password"
	ownerPassName    = "bekup_owner_password"
	appPassName      = "bekup_app_password"
)

// Embedded asset roots (relative to the repository root and to the
// deployment directory alike).
var assetDirs = []string{"deploy/postgres", "deploy/traefik", "deploy/nginx"}

// referenceFiles maps reference/<name> to the embedded example file.
var referenceFiles = map[string]string{
	"reference/bekup.example.toml":  "bekup.example.toml",
	"reference/runner.example.toml": "runner.example.toml",
}

func rootFile(p string, mode fs.FileMode, class Class) Entry {
	return Entry{Path: p, Kind: KindFile, Mode: mode, Class: class}
}

func rootDir(p string, mode fs.FileMode, class Class) Entry {
	return Entry{Path: p, Kind: KindDir, Mode: mode, Class: class}
}

func secretFile(p string, uid int, group string) Entry {
	return Entry{Path: p, Kind: KindFile, Mode: 0o400, UID: uid, GID: uid, Class: ClassSecret, Secret: true, Group: group}
}

// ManifestOptions select the optional parts of the manifest.
type ManifestOptions struct {
	// StaticTLS adds the static-TLS override file and proxy-tls/.
	StaticTLS bool
	// ComposeOperator makes compose.yaml an operator file (compose-first:
	// setup never writes it, D-205); otherwise it is generated
	// (setup-first, --write-compose).
	ComposeOperator bool
}

// Manifest returns every entry of a deployment directory: the fixed files
// plus one entry per embedded asset (assets lists the deploy/** files, see
// assetFiles). Parents come before children.
func Manifest(l Layout, assets []string, o ManifestOptions) []Entry {
	var es []Entry
	add := func(e ...Entry) { es = append(es, e...) }
	staticTLS := o.StaticTLS
	composeClass := ClassGenerated
	if o.ComposeOperator {
		composeClass = ClassOperator
	}

	// The deployment directory itself: root's, never group/world-writable
	// (whoever can write it can swap any file, including the compose file).
	add(Entry{Path: ".", Kind: KindDir, Mode: 0o755, Class: ClassOperator, ModeIsMax: true})
	add(rootFile(ComposeFile, 0o644, composeClass),
		rootFile(StateFileName, 0o644, ClassGenerated),
		rootFile(EnvFile, 0o644, ClassOperator),
		rootFile(BekupTOML, 0o644, ClassOperator),
		rootFile(RunnerTOML, 0o644, ClassOperator))
	ov := rootFile(OverrideFile, 0o644, ClassOperator)
	ov.Optional = !staticTLS
	add(ov)
	if staticTLS {
		// The operator's own proxy certificate goes here (tls.crt, tls.key).
		pt := rootDir(ProxyTLSDir, 0o755, ClassOperator)
		pt.Optional = true
		add(pt)
	}

	add(rootDir(ReferenceDir, 0o755, ClassGenerated))
	refs := make([]string, 0, len(referenceFiles))
	for r := range referenceFiles {
		refs = append(refs, r)
	}
	sort.Strings(refs)
	for _, r := range refs {
		add(rootFile(r, 0o644, ClassGenerated))
	}

	// deploy/**: directories 0755, *.sh 0755, everything else 0644.
	dirs := map[string]bool{}
	for _, a := range assets {
		for d := path.Dir(a); d != "." && !dirs[d]; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	sortedDirs := make([]string, 0, len(dirs))
	for d := range dirs {
		sortedDirs = append(sortedDirs, d)
	}
	sort.Strings(sortedDirs)
	for _, d := range sortedDirs {
		add(rootDir(d, 0o755, ClassGenerated))
	}
	sortedAssets := append([]string(nil), assets...)
	sort.Strings(sortedAssets)
	for _, a := range sortedAssets {
		mode := fs.FileMode(0o644)
		if strings.HasSuffix(a, ".sh") {
			mode = 0o755
		}
		add(rootFile(a, mode, ClassGenerated))
	}

	// Secrets. Directories are traversable but not listable (0711).
	add(Entry{Path: l.KeysDir, Kind: KindDir, Mode: 0o711, Class: ClassSecret})
	add(secretFile(l.keys(masterKeyName), UIDBekup, groupMasterKey),
		secretFile(l.keys(appURLName), UIDBekup, groupDatabase),
		secretFile(l.keys(ownerURLName), UIDBekup, groupDatabase),
		secretFile(l.keys(postgresPassName), UIDPostgres, groupDatabase),
		secretFile(l.keys(ownerPassName), UIDPostgres, groupDatabase),
		secretFile(l.keys(appPassName), UIDPostgres, groupDatabase))

	add(Entry{Path: l.keys(certsDirName), Kind: KindDir, Mode: 0o711, Class: ClassSecret})
	for _, c := range []struct {
		name string
		uid  int
	}{{"serve", UIDBekup}, {"runner", UIDBekup}, {"postgres", UIDPostgres}} {
		add(Entry{Path: l.keys(certsDirName, c.name), Kind: KindDir, Mode: 0o711, Class: ClassSecret, Group: groupCerts},
			Entry{Path: l.keys(certsDirName, c.name, c.name+".crt"), Kind: KindFile, Mode: 0o644, Class: ClassSecret, Group: groupCerts},
			Entry{Path: l.keys(certsDirName, c.name, "ca.crt"), Kind: KindFile, Mode: 0o644, Class: ClassSecret, Group: groupCerts},
			secretFile(l.keys(certsDirName, c.name, c.name+".key"), c.uid, groupCerts))
	}
	// The CA: root only, expected to live on offline media.
	add(Entry{Path: l.keys(certsDirName, caDirName), Kind: KindDir, Mode: 0o700, Class: ClassSecret, Group: groupCerts, Optional: true, Offline: true},
		Entry{Path: l.keys(certsDirName, caDirName, "ca.crt"), Kind: KindFile, Mode: 0o644, Class: ClassSecret, Group: groupCerts, Optional: true, Offline: true},
		Entry{Path: l.keys(certsDirName, caDirName, caKeyName), Kind: KindFile, Mode: 0o600, Class: ClassSecret, Secret: true, Group: groupCerts, Optional: true, Offline: true})
	return es
}

// ManifestTable renders the manifest as the Markdown table of
// docs/installation/compose.md (a test keeps both in sync).
func ManifestTable(l Layout) string {
	var b strings.Builder
	b.WriteString("| Path | Class | Owner (UID:GID) | Mode |\n|---|---|---|---|\n")
	for _, e := range Manifest(l, []string{"deploy/x"}, ManifestOptions{StaticTLS: true, ComposeOperator: true}) {
		cell := "`" + e.Path + "`"
		mode := fmt.Sprintf("%04o", e.Mode.Perm())
		if e.ModeIsMax {
			mode = "at most " + mode
		}
		switch {
		case e.Path == ".":
			cell = "`./` (the deployment directory)"
		case strings.HasPrefix(e.Path, DeployDir+"/") || e.Path == DeployDir:
			if e.Kind == KindDir {
				continue
			}
			cell = "`deploy/**` (`*.sh` and directories 0755)"
		case e.Kind == KindDir:
			cell = "`" + e.Path + "/`"
		}
		switch {
		case e.Offline:
			cell += " (offline)"
		case e.Path == OverrideFile:
			cell += " (static TLS only)"
		case e.Path == ComposeFile:
			cell += " (yours; generated-upgradable with `--write-compose`)"
		}
		fmt.Fprintf(&b, "| %s | %s | %d:%d | %s |\n", cell, e.Class, e.UID, e.GID, mode)
	}
	return b.String()
}
