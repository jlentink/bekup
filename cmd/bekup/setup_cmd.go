package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/imageref"
	"github.com/jlentink/bekup/internal/setup"
	"github.com/jlentink/bekup/internal/setup/wizard"
)

// setupDefaultDir is where the operator bind-mounts the deployment
// directory into the one-shot setup container.
const setupDefaultDir = "/deploy"

// envSetupImage carries the image reference into the setup container (a
// container cannot know its own digest).
const envSetupImage = "BEKUP_IMAGE"

// envDockerGID carries the Docker socket's group into the setup container
// (the compose setup service passes ${DOCKER_GID:-}).
const envDockerGID = "DOCKER_GID"

// setupTerminal builds the prompt terminal on stdin/stderr. It is a
// variable so tests can replace it.
var setupTerminal = func(c *cobra.Command) *setup.Terminal {
	fd := int(os.Stdin.Fd()) // #nosec G115 -- file descriptors fit in int
	isTTY := term.IsTerminal(fd)
	return &setup.Terminal{
		In:         bufio.NewReader(c.InOrStdin()),
		Out:        c.ErrOrStderr(),
		IsTerminal: isTTY,
		ReadPassword: func() ([]byte, error) {
			p, err := term.ReadPassword(fd)
			_, _ = fmt.Fprintln(c.ErrOrStderr())
			return p, err
		},
	}
}

// stderrIsTerminal reports whether stderr is a TTY (a generated passphrase
// is shown only there, never on stdout, which is often captured). A
// variable for tests.
var stderrIsTerminal = func() bool {
	return term.IsTerminal(int(os.Stderr.Fd())) // #nosec G115 -- file descriptors fit in int
}

func newSetupCmd() *cobra.Command {
	var (
		dir, ownership              string
		plain, allowVersionMismatch bool
	)
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Write and maintain a container-only compose deployment directory (wizard on a terminal)",
		Long: `Create, upgrade and check a compose deployment directory from the bekup image
alone (no source checkout; D-203, D-205, D-208).

On a terminal, ` + "`bekup setup`" + ` without a subcommand starts the setup wizard: it asks
for the reverse proxy, the domain, the database (bundled, or your own
PostgreSQL), the master key, the internal CA and the Docker group, shows a
summary and writes everything, compose.yaml included (it is kept when it
exists). In an existing deployment it offers check, upgrade and certificate
renewal. The main path, in an empty directory:

  docker run --rm -it --log-driver none --network none --read-only --tmpfs /tmp \
    --user 0:0 --cap-drop ALL --cap-add CHOWN --security-opt no-new-privileges \
    -e BEKUP_IMAGE="$IMAGE" -e DOCKER_GID="$(stat -c %g /var/run/docker.sock)" \
    -v "$PWD":/deploy "$IMAGE" setup
  docker compose up -d

--log-driver none keeps the container's output (a generated passphrase) out
of Docker's logs. The wizard needs stdin and stdout on a terminal (a
generated passphrase is shown once on the alternate screen, never into a
pipe or the scrollback). NO_COLOR and TERM=dumb drop the colors; --plain
uses a plain-text interface (numbered choices, no escape sequences; a
generated passphrase then stays in the scrollback).
Without a terminal use the subcommands with flags (setup init
--non-interactive ..., setup renew, setup upgrade, setup check).

Compose-first: save a release compose file as compose.yaml and run its
setup service (the init subcommand with prompts):

  DOCKER_GID=$(stat -c %g /var/run/docker.sock) docker compose run --rm setup

See docs/installation/compose.md.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			mode, err := setup.ParseOwnershipMode(ownership)
			if err != nil {
				return err
			}
			ui, ok := setupUI(c, plain)
			if !ok {
				return errors.New("the setup wizard needs a terminal on stdin and stdout (docker run -it ..., no pipe): without one, use `bekup setup init --non-interactive` with flags (bekup setup init --help), or setup check / upgrade / renew")
			}
			return runSetupWizard(ui, dir, mode, allowVersionMismatch)
		},
	}
	f := cmd.Flags()
	f.StringVar(&dir, "dir", setupDefaultDir, "deployment directory (must exist)")
	f.StringVar(&ownership, "ownership", string(setup.OwnershipAuto), "auto, chown, print or none")
	f.BoolVar(&plain, "plain", false, "plain-text interface: numbered choices, no escape sequences (a generated passphrase stays in the scrollback)")
	f.BoolVar(&allowVersionMismatch, "allow-version-mismatch", false, "accept an image tag that differs from this bekup version")
	cmd.AddCommand(newSetupInitCmd(), newSetupUpgradeCmd(), newSetupCheckCmd(), newSetupComposeCmd(), newSetupRenewCmd())
	return cmd
}

// setupUI returns the wizard interface on the terminal (false: stdin or
// stdout is not a terminal; the wizard shows a generated passphrase on
// stdout, which must never be a pipe or a file). The plain interface only
// with --plain: NO_COLOR and TERM=dumb keep the terminal interface, whose
// renderer drops the colors, and its alternate screen for secrets. A
// variable for tests.
var setupUI = func(c *cobra.Command, plain bool) (wizard.UI, bool) {
	fd := int(os.Stdin.Fd())     // #nosec G115 -- file descriptors fit in int
	outFD := int(os.Stdout.Fd()) // #nosec G115 -- file descriptors fit in int
	if !term.IsTerminal(fd) || !term.IsTerminal(outFD) {
		return nil, false
	}
	readPassword := func() ([]byte, error) { return term.ReadPassword(fd) }
	if plain {
		return wizard.NewPlain(c.InOrStdin(), os.Stdout, readPassword), true
	}
	return wizard.NewTUI(os.Stdin, os.Stdout, readPassword), true
}

// runSetupWizard runs the setup wizard: the init questions in a new
// directory, otherwise the choice of check, upgrade or renew.
func runSetupWizard(ui wizard.UI, dir string, mode setup.OwnershipMode, allowVersionMismatch bool) error {
	if _, err := os.Lstat(filepath.Join(dir, setup.StateFileName)); err == nil {
		return runSetupHub(ui, dir, mode, allowVersionMismatch)
	}
	plan, err := wizard.RunInit(wizard.Env{
		Dir: dir, DefaultDir: setupDefaultDir, Version: version, Image: strings.TrimSpace(os.Getenv(envSetupImage)),
		DockerGID: strings.TrimSpace(os.Getenv(envDockerGID)), AllowVersionMismatch: allowVersionMismatch,
		Ownership: mode, UI: ui,
	})
	if err != nil {
		return err
	}
	defer plan.Destroy()
	if err := setupInit(plan.Options); err != nil {
		return err
	}
	_, err = fmt.Fprintf(ui.Out(), "\nThe same without the wizard (no secrets in it): %s\n", strings.Join(plan.Flags, " "))
	if err == nil && plan.FlagsNote != "" {
		_, err = fmt.Fprintf(ui.Out(), "(%s)\n", plan.FlagsNote)
	}
	return err
}

// Hub actions of an existing deployment.
const (
	hubCheck   = "check"
	hubUpgrade = "upgrade"
	hubRenew   = "renew"
	hubQuit    = "quit"
)

// runSetupHub offers the maintenance commands of an existing deployment.
func runSetupHub(ui wizard.UI, dir string, mode setup.OwnershipMode, allowVersionMismatch bool) error {
	upgradeLabel := "upgrade it to this image (setup rewrites its compose.yaml)"
	if st, err := setup.ReadState(dir); err == nil && st.ComposeMode == setup.ComposeOperator {
		upgradeLabel = "upgrade it to this image (after replacing compose.yaml with the new release file)"
	}
	action, err := ui.Select("bekup setup", "This directory holds a bekup deployment ("+setup.StateFileName+"). What now?", []wizard.Option{
		{Value: hubCheck, Label: "check it (read-only)"},
		{Value: hubUpgrade, Label: upgradeLabel},
		{Value: hubRenew, Label: "renew certificates"},
		{Value: hubQuit, Label: "quit"},
	}, hubCheck)
	if errors.Is(err, wizard.ErrBack) || err == nil && action == hubQuit {
		return nil
	}
	if err != nil {
		return err
	}
	switch action {
	case hubCheck:
		_, err := setup.Check(setup.CheckOptions{Dir: dir, Version: version, Ownership: mode, Out: ui.Out()})
		return err
	case hubRenew:
		return runRenewWizard(ui, dir, mode, 0)
	default:
		st, err := setup.ReadState(dir)
		if err != nil {
			return err
		}
		var img imageref.Ref
		if raw := strings.TrimSpace(os.Getenv(envSetupImage)); raw != "" {
			if img, err = imageref.ParsePinned(raw); err != nil {
				return setup.PinHelp(err)
			}
		}
		return setupUpgrade(setup.UpgradeOptions{
			Dir: dir, Version: version, Image: img, WriteCompose: st.ComposeMode == setup.ComposeGenerated,
			AllowVersionMismatch: allowVersionMismatch,
			Ownership:            mode, Confirm: wizard.UpgradeConfirm(ui), Out: ui.Out(),
			CAPassphrase: func() ([]byte, error) {
				return wizard.NewPassphrase(ui, "This version adds certificates; at least 16 characters (asked twice).")
			},
		})
	}
}

// setupUpgrade is setup.Upgrade (tests capture the options).
var setupUpgrade = setup.Upgrade

// setupRenew is setup.Renew (tests capture the options).
var setupRenew = setup.Renew

// runRenewWizard is the guided renewal (warnDays 0: the default period).
func runRenewWizard(ui wizard.UI, dir string, mode setup.OwnershipMode, warnDays int) error {
	opts, done, err := wizard.RunRenew(ui, dir, time.Now(), warnDays)
	if err != nil || done {
		return err
	}
	opts.Ownership = mode
	return setupRenew(opts)
}

// caPassFlags select the CA key passphrase source of setup init.
type caPassFlags struct {
	file     string
	stdin    bool
	generate bool
}

func (p *caPassFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&p.file, "ca-passphrase-file", "", "file with the CA key passphrase (min 16 characters; checked like every secret file)")
	f.BoolVar(&p.stdin, "ca-passphrase-stdin", false, "read the CA key passphrase from the first line of stdin")
	f.BoolVar(&p.generate, "generate-ca-passphrase", false, "generate a passphrase and show it ONCE on stderr (only when stderr is a terminal; run with --log-driver none)")
}

// source returns the passphrase callback (called only when certificates
// are generated). existingCA: the passphrase unlocks an existing CA key
// (asked once, never generated).
func (p *caPassFlags) source(c *cobra.Command, t *setup.Terminal, existingCA bool) (func() ([]byte, error), error) {
	n := 0
	for _, b := range []bool{p.file != "", p.stdin, p.generate} {
		if b {
			n++
		}
	}
	if n > 1 {
		return nil, errors.New("--ca-passphrase-file, --ca-passphrase-stdin and --generate-ca-passphrase are mutually exclusive")
	}
	if existingCA && p.generate {
		return nil, errors.New("--use-existing-ca needs the passphrase of the existing CA key, not --generate-ca-passphrase")
	}
	switch {
	case p.file != "":
		return func() ([]byte, error) { return readPassphraseFile(p.file) }, nil
	case p.stdin:
		return func() ([]byte, error) { return readPassphraseLine(c.InOrStdin()) }, nil
	case p.generate:
		return func() ([]byte, error) {
			if !stderrIsTerminal() {
				return nil, errors.New("--generate-ca-passphrase shows the passphrase once and needs stderr to be a terminal (docker run -it --log-driver none)")
			}
			s, err := crypto.GeneratePassphrase(crypto.MinPassphraseWords)
			if err != nil {
				return nil, err
			}
			defer s.Destroy()
			w := c.ErrOrStderr()
			_, _ = fmt.Fprintln(w, "\nGenerated CA key passphrase (shown ONCE; store it offline with the CA, e.g. in a password manager).\nWARNING: Docker keeps container output in its logs unless the setup container runs with --log-driver none:")
			// Written as bytes: never a Go string or an fmt buffer.
			if _, err := io.WriteString(w, "\n    "); err != nil {
				return nil, err
			}
			if _, err := w.Write(s.Bytes()); err != nil {
				return nil, err
			}
			if _, err := io.WriteString(w, "\n\n"); err != nil {
				return nil, err
			}
			return bytes.Clone(s.Bytes()), nil
		}, nil
	}
	return func() ([]byte, error) {
		ask := t.NewPassphrase
		if existingCA {
			ask = t.ExistingPassphrase
		}
		pass, err := ask()
		if errors.Is(err, setup.ErrNoTerminal) {
			return nil, errors.New("no terminal for the CA key passphrase prompt: use --ca-passphrase-file, --ca-passphrase-stdin or --generate-ca-passphrase")
		}
		return pass, err
	}, nil
}

// maxPassphraseLine bounds the passphrase line read from stdin.
const maxPassphraseLine = 1024

// maxEmptyReads bounds consecutive (0, nil) reads of the passphrase line.
const maxEmptyReads = 100

// readPassphraseLine reads the first line of r as a passphrase: byte by
// byte up to the newline (it never waits for more input after it) or EOF,
// at most maxPassphraseLine bytes.
func readPassphraseLine(r io.Reader) ([]byte, error) {
	buf := make([]byte, 0, maxPassphraseLine)
	defer func() { clear(buf[:cap(buf)]) }()
	var one [1]byte
	empty := 0
	for {
		n, err := r.Read(one[:])
		if n == 0 && err == nil {
			// A reader that returns neither data nor an error would spin
			// forever (io.Reader allows it, but discourages it).
			empty++
			if empty >= maxEmptyReads {
				return nil, fmt.Errorf("reading the passphrase from stdin: %w", io.ErrNoProgress)
			}
			continue
		}
		empty = 0
		if n == 1 {
			if one[0] == '\n' {
				break
			}
			if len(buf) == maxPassphraseLine {
				return nil, fmt.Errorf("the passphrase line on stdin is longer than %d bytes", maxPassphraseLine)
			}
			buf = append(buf, one[0])
			continue
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading the passphrase from stdin: %w", err)
		}
	}
	one[0] = 0
	out := append([]byte(nil), bytes.TrimSpace(buf)...)
	if err := certs.ValidatePassphrase(out); err != nil {
		config.Zero(out)
		return nil, err
	}
	return out, nil
}

// setupInit is setup.Init (tests capture the options).
var setupInit = setup.Init

// initFlags are the flags of `setup init` (the wizard builds the same
// setup.Options from its answers).
type initFlags struct {
	dir, imageRaw, proxy, domain, tlsMode, acmeEmail, keysDir, ownership string
	variant, serveIP, proxyIP, database                                  string
	acmeStaging, nonInteractive, keepExisting, force, regenerate         bool
	allowVersionMismatch, writeCompose, useExistingCA                    bool
	dockerGID                                                            int
	targetCIDRs, targetHosts, trusted                                    []string
	net                                                                  setup.Network
	link                                                                 setup.LinkNetwork
	traefikEntrypoint, traefikCertResolver                               string
	db                                                                   setup.Database
	dbAuth                                                               string
	importMasterKey, masterKeyCheckValue                                 string
	pf                                                                   caPassFlags
}

func newSetupInitCmd() *cobra.Command {
	var fl initFlags
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Set up a deployment directory (config, secrets, certificates, deploy assets)",
		Long: `Set up a compose deployment directory: deploy/**, reference/*.example.toml,
.env, minimal bekup.toml and runner.toml, the database passwords, the master
keyring and the private CA with the runner/serve(/postgres) certificates,
then hand every file to the container user that reads it (--ownership).
The interactive wizard (` + "`bekup setup`" + ` on a terminal) asks the same
questions and writes the same files.

Compose-first (default, D-205): compose.yaml must exist (a release file, or
the committed file from deploy/compose/). setup takes the pinned image from
its x-bekup-image line (the committed file: from BEKUP_IMAGE, which must be
digest-pinned and is written to .env), recognizes the variant (traefik,
traefik-external, npm) and the database mode (the *.external-db.yaml files:
your own PostgreSQL; a customized file needs --variant and passes the
hardening lint) and never writes compose.yaml. It asks only for the domain,
the ACME e-mail (own Traefik), your PostgreSQL (external database), the CA
key passphrase and, unless DOCKER_GID is set, the Docker socket's group.

Setup-first (--write-compose --variant traefik|traefik-external|npm|full
[--database external]): setup also writes compose.yaml (full: every
service, --proxy traefik, nginx or none).

Your own PostgreSQL (--database external, D-207, D-208): --db-host,
--db-ca-file (its CA bundle, inside the deployment directory; copied) and
--db-auth: bootstrap (default: setup generates the two role passwords and
prints the one ` + "`docker compose run --rm migrate db bootstrap`" + ` command), existing-roles
(the roles exist: their passwords from --db-password-file /
--db-owner-password-file or the terminal), dba-sql (setup writes
<keys>/bekup-roles.sql with SCRAM verifiers for your DBA) or single-account
(--db-user: one account for everything; an explicit weakening). setup has no
network: it never connects to the database.

On a terminal, missing answers are prompted; --non-interactive requires the
flags instead. Existing files are never overwritten without --force (non-secret
files only) or --regenerate-secrets (terminal + typed confirmation).`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			opts, err := fl.options(c)
			if err != nil {
				return err
			}
			return setupInit(opts)
		},
	}
	fl.register(cmd)
	return cmd
}

func (fl *initFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&fl.dir, "dir", setupDefaultDir, "deployment directory (must exist)")
	f.BoolVar(&fl.writeCompose, "write-compose", false, "setup-first: also write compose.yaml (needs --variant); default is compose-first (compose.yaml exists, never written)")
	f.StringVar(&fl.variant, "variant", "", "traefik, traefik-external, npm or full (--write-compose; compose-first only to name the base of a customized compose.yaml)")
	f.StringVar(&fl.database, "database", "", "bundled (the postgres service, default) or external (your own PostgreSQL, D-208; compose-first: must match compose.yaml)")
	f.StringVar(&fl.imageRaw, "image", "", "bekup image pinned by digest, repo[:tag]@sha256:<digest> (default: $BEKUP_IMAGE; compose-first: must equal the x-bekup-image line, or BEKUP_IMAGE for the committed file)")
	f.BoolVar(&fl.nonInteractive, "non-interactive", false, "never prompt; every answer must come from flags")
	f.StringVar(&fl.proxy, "proxy", "", "variant full: reverse proxy traefik, nginx or none (your own proxy on the frontend network)")
	f.StringVar(&fl.domain, "domain", "", "public host name of the UI (base_url https://<domain>)")
	f.StringVar(&fl.tlsMode, "tls-mode", "", "bundled proxy TLS: acme (Let's Encrypt, default) or static (your certificate in proxy-tls/)")
	f.StringVar(&fl.acmeEmail, "acme-email", "", "ACME account e-mail (--tls-mode acme)")
	f.BoolVar(&fl.acmeStaging, "acme-staging", false, "use the Let's Encrypt staging CA (testing)")
	f.IntVar(&fl.dockerGID, "docker-gid", 0, "group ID of the Docker socket on the host: stat -c %g /var/run/docker.sock (default: $DOCKER_GID)")
	f.StringSliceVar(&fl.targetCIDRs, "target-cidr", nil, "CIDR of target databases the runner may reach (repeatable)")
	f.StringSliceVar(&fl.targetHosts, "target-host", nil, "host name of a target database the runner may reach (repeatable)")
	f.StringSliceVar(&fl.trusted, "trusted-proxy", nil, "CIDR of your own reverse proxy on the frontend network (--proxy none; repeatable)")
	f.StringVar(&fl.keysDir, "keys-dir", setup.DefaultKeysDir, "secrets directory, relative to --dir (mount another volume there to keep secrets elsewhere)")
	f.StringVar(&fl.ownership, "ownership", string(setup.OwnershipAuto), "auto, chown (needs CAP_CHOWN), print (show the chown commands) or none (Docker Desktop)")
	f.BoolVar(&fl.keepExisting, "keep-existing", false, "keep existing files and create only the missing ones")
	f.BoolVar(&fl.force, "force", false, "rewrite existing NON-SECRET files (deploy, reference, .env, TOMLs; compose.yaml only with --write-compose)")
	f.BoolVar(&fl.regenerate, "regenerate-secrets", false, "delete and regenerate EVERY secret and certificate (terminal + typed confirmation)")
	f.BoolVar(&fl.allowVersionMismatch, "allow-version-mismatch", false, "accept an image tag that differs from this bekup version")
	f.StringVar(&fl.traefikEntrypoint, "traefik-entrypoint", "", "traefik-external: your Traefik's HTTPS entrypoint (default websecure)")
	f.StringVar(&fl.traefikCertResolver, "traefik-certresolver", "", "traefik-external: your Traefik's certificate resolver (default letsencrypt)")
	f.StringVar(&fl.link.Name, "proxy-network", "", "traefik-external/npm: name of the external link network (default bekup-proxy)")
	f.StringVar(&fl.link.Subnet, "proxy-subnet", "", "traefik-external/npm: subnet of the link network (default 10.213.253.0/28)")
	f.StringVar(&fl.serveIP, "serve-ip", "", "advanced: serve's fixed address towards the proxy: SERVE_FRONTEND_IP (default 10.213.250.10) or, traefik-external/npm, SERVE_PROXY_IP (default 10.213.253.10)")
	f.StringVar(&fl.proxyIP, "proxy-ip", "", "advanced: the proxy's fixed address: PROXY_FRONTEND_IP (default 10.213.250.5) or, traefik-external/npm, your proxy's address on the link network (default 10.213.253.5)")
	f.StringVar(&fl.net.FrontendSubnet, "frontend-subnet", "", "advanced: FRONTEND_SUBNET (default 10.213.250.0/24; not traefik-external/npm)")
	f.StringVar(&fl.net.RunnerCtlSubnet, "runner-ctl-subnet", "", "advanced: RUNNER_CTL_SUBNET (default 10.213.251.0/24)")
	f.StringVar(&fl.net.RunnerCtlIP, "runner-ip", "", "advanced: RUNNER_CTL_IP (default 10.213.251.10)")
	f.StringVar(&fl.net.MetricsSubnet, "metrics-subnet", "", "advanced: METRICS_SUBNET (default 10.213.252.0/24)")
	f.StringVar(&fl.net.ServeMetricsIP, "metrics-ip", "", "advanced: SERVE_METRICS_IP (default 10.213.252.10)")
	f.StringVar(&fl.db.Host, "db-host", "", "external database: host name or IP address of your PostgreSQL (15-17)")
	f.IntVar(&fl.db.Port, "db-port", 0, "external database: port (default 5432)")
	f.StringVar(&fl.db.Name, "db-name", "", "external database: name of bekup's database (default bekup)")
	f.StringVar(&fl.db.CAFile, "db-ca-file", "", "external database: CA bundle (PEM) of the server certificate, a path inside --dir (copied to <keys>/bekup_database_ca.crt; verified with tls_mode verify-full)")
	f.StringVar(&fl.db.TLSServerName, "db-tls-server-name", "", "external database: host name the server certificate names, when --db-host is an IP address")
	f.StringVar(&fl.dbAuth, "db-auth", "", "external database: bootstrap (default), existing-roles, dba-sql or single-account (an explicit weakening)")
	f.StringVar(&fl.db.User, "db-user", "", "external database, single-account: the account's role name")
	f.StringVar(&fl.db.AdminUser, "db-admin-user", "", "external database, bootstrap: the administrator role named in the printed bootstrap command")
	f.StringVar(&fl.db.PasswordFile, "db-password-file", "", "external database: file (inside --dir) with the bekup_app (single-account: the account's) password instead of a generated or prompted one; copied")
	f.StringVar(&fl.db.OwnerPasswordFile, "db-owner-password-file", "", "external database: file (inside --dir) with the bekup_owner password instead of a generated or prompted one; copied")
	f.StringVar(&fl.importMasterKey, "import-master-key", "", "import an existing master keyring (a file inside --dir, e.g. restored from escrow) instead of generating one")
	f.StringVar(&fl.masterKeyCheckValue, "master-key-check-value", "", "with --import-master-key: the expected key-check value (kcv1:...)")
	f.BoolVar(&fl.useExistingCA, "use-existing-ca", false, "issue the certificates from the CA mounted at <keys>/certs/ca (offline media; its passphrase is asked) instead of creating a new CA")
	fl.pf.register(cmd)
}

// options builds the setup.Options of `setup init` from the flags (and
// the terminal for the missing answers).
func (fl *initFlags) options(c *cobra.Command) (setup.Options, error) {
	imageRaw := fl.imageRaw
	if imageRaw == "" {
		imageRaw = strings.TrimSpace(os.Getenv(envSetupImage))
	}
	mode, err := setup.ParseOwnershipMode(fl.ownership)
	if err != nil {
		return setup.Options{}, err
	}
	f := c.Flags()
	var v setup.Variant
	if fl.variant != "" {
		if v, err = setup.ParseVariant(fl.variant); err != nil {
			return setup.Options{}, err
		}
	}
	db := fl.db
	if fl.database != "" {
		if db.Mode, err = setup.ParseDBMode(fl.database); err != nil {
			return setup.Options{}, err
		}
	}
	if fl.dbAuth != "" {
		if db.Auth, err = setup.ParseDBAuth(fl.dbAuth); err != nil {
			return setup.Options{}, err
		}
	}
	if f.Changed("db-port") && (db.Port < 1 || db.Port > 65535) {
		return setup.Options{}, fmt.Errorf("--db-port must be between 1 and 65535 (got %d; omit it for 5432)", db.Port)
	}
	a := setup.Answers{
		Variant: v, Proxy: setup.Proxy(fl.proxy), Domain: fl.domain, TLSMode: setup.TLSMode(fl.tlsMode),
		ACMEEmail: fl.acmeEmail, ACMEStaging: fl.acmeStaging, DockerGID: fl.dockerGID,
		TargetCIDRs: fl.targetCIDRs, TargetHosts: fl.targetHosts, TrustedProxies: fl.trusted,
		KeysDir: fl.keysDir, Network: fl.net, Link: fl.link,
		TraefikEntrypoint: fl.traefikEntrypoint, TraefikCertResolver: fl.traefikCertResolver,
		DB: db,
	}
	given := setup.Given{
		Image: imageRaw != "", Proxy: f.Changed("proxy"), Domain: f.Changed("domain"),
		TLSMode: f.Changed("tls-mode"), ACMEEmail: f.Changed("acme-email"),
		ACMEStaging: f.Changed("acme-staging"), DockerGID: f.Changed("docker-gid"),
		Targets:      f.Changed("target-cidr") || f.Changed("target-host"),
		TrustedProxy: f.Changed("trusted-proxy"),
		ComposeFirst: !fl.writeCompose,
		Database:     f.Changed("database"),
		DBHost:       f.Changed("db-host"), DBCAFile: f.Changed("db-ca-file"), DBAuth: f.Changed("db-auth"),
	}
	if !given.DockerGID {
		if raw := strings.TrimSpace(os.Getenv(envDockerGID)); raw != "" {
			gid, err := strconv.Atoi(raw)
			if err != nil || gid < 0 {
				return setup.Options{}, fmt.Errorf("%s=%q is not a numeric group ID (stat -c %%g /var/run/docker.sock)", envDockerGID, raw)
			}
			a.DockerGID, given.DockerGID = gid, true
		}
	}
	out := c.OutOrStdout()
	if fl.writeCompose {
		if v == "" {
			return setup.Options{}, errors.New("--write-compose needs --variant traefik, traefik-external, npm or full")
		}
	} else {
		info, err := setup.InspectCompose(fl.dir, v, db.Mode)
		if err != nil {
			return setup.Options{}, err
		}
		if imageRaw != "" && imageRaw != info.Image.String() {
			if info.FromEnv {
				return setup.Options{}, fmt.Errorf("--image %s does not match BEKUP_IMAGE=%s, which the x-bekup-image line of compose.yaml resolves to: set BEKUP_IMAGE instead (BEKUP_IMAGE=<image> docker compose run --rm setup)", imageRaw, info.Image)
			}
			return setup.Options{}, fmt.Errorf("BEKUP_IMAGE/--image %s does not match the x-bekup-image line of compose.yaml (%s): run setup from that file (docker compose run --rm setup)", imageRaw, info.Image)
		}
		a.Variant, a.Image, a.DB.Mode = info.Variant, info.Image, info.DB
		given.Database = true
		from := ""
		if info.FromEnv {
			from = " (from BEKUP_IMAGE; setup keeps it in .env)"
		}
		dbNote := ""
		if info.DB == setup.DBExternal {
			dbNote = ", your own PostgreSQL"
		}
		_, _ = fmt.Fprintf(out, "compose.yaml: the %s variant%s (%s), image %s%s\n", info.Variant, dbNote, info.Level, info.Image, from)
		for _, f := range info.Findings {
			_, _ = fmt.Fprintln(out, f.String())
		}
	}
	applyAddressFlags(&a, fl.serveIP, fl.proxyIP)
	t := setupTerminal(c)
	if err := setup.Complete(&a, given, imageRaw, t, fl.nonInteractive); err != nil {
		return setup.Options{}, err
	}
	if !fl.allowVersionMismatch {
		if err := setup.CheckVersionTag(a.Image, version); err != nil {
			return setup.Options{}, err
		}
	}
	if fl.regenerate {
		if fl.nonInteractive {
			return setup.Options{}, errors.New("--regenerate-secrets needs a terminal and a typed confirmation (not --non-interactive)")
		}
		if err := t.Confirm(regenerateWarning, "regenerate secrets"); err != nil {
			return setup.Options{}, err
		}
	}
	passFn, err := fl.pf.source(c, t, fl.useExistingCA)
	if err != nil {
		return setup.Options{}, err
	}
	var dbPassword func(string) ([]byte, error)
	if !fl.nonInteractive && t.IsTerminal {
		dbPassword = t.DBPassword
	}
	return setup.Options{
		Dir: fl.dir, Version: version, Answers: a, Ownership: mode,
		KeepExisting: fl.keepExisting, Force: fl.force, RegenerateSecrets: fl.regenerate,
		WriteCompose: fl.writeCompose, CAPassphrase: passFn, Out: out,
		UseExistingCA: fl.useExistingCA, ImportMasterKey: fl.importMasterKey,
		MasterKeyCheckValue: fl.masterKeyCheckValue, DBPassword: dbPassword,
	}, nil
}

// applyAddressFlags maps --serve-ip / --proxy-ip to the network the
// variant's proxy shares with serve: the link network behind an external
// proxy, the frontend network otherwise.
func applyAddressFlags(a *setup.Answers, serveIP, proxyIP string) {
	if a.Variant.External() {
		a.Link.ServeIP, a.Link.ProxyIP = serveIP, proxyIP
		return
	}
	a.Network.ServeFrontendIP, a.Network.ProxyFrontendIP = serveIP, proxyIP
}

const regenerateWarning = `WARNING: --regenerate-secrets DELETES every secret and certificate in the
deployment directory and creates new ones:
  - the app database volume keeps the OLD role passwords (they are set only
    when the volume is initialized): serve and migrate fail until you reset
    the volume (docker compose down -v DESTROYS the bekup database) or set
    the new passwords with ALTER ROLE; your own PostgreSQL (external
    database) keeps them too: set the new ones with
    docker compose run --rm migrate db bootstrap --admin-user <admin> \
      --adopt-existing-roles --reset-passwords;
  - a new master key cannot decrypt any credential stored with the old one
    (connections, destinations, job passphrases, TOTP secrets): restore the
    old key from escrow instead if the database is to be kept;
  - every component needs the new certificates (restart all services);
  - the CA in <keys>/certs/ca/ is replaced as well, INCLUDING an offline CA
    mounted there: its old key and certificate are deleted from the media
    once the run succeeded (unmount the media first to keep the old CA).
The old secrets are kept until the run succeeded; after that there is no undo.`

func newSetupUpgradeCmd() *cobra.Command {
	var (
		dir, imageRaw, ownership                    string
		allowDowngrade, allowVersionMismatch, force bool
		writeCompose, nonInteractive, plain         bool
		pf                                          caPassFlags
	)
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Rewrite the generated files (deploy/**, reference/*) for a new image",
		Long: `Rewrite only the generated files for a new image: deploy/** (stale assets are
removed), reference/* and bekup-setup.json. .env, the TOML files
(re-validated with the new validators) and existing secrets are never
touched; secrets that are new in the manifest are generated. Local edits of
generated files are refused, or with --force copied to <file>.bak-<timestamp>
first. Back up before upgrading (docs/installation/upgrades.md).

Compose-first (D-205): replace compose.yaml with the new release file first,
then run its setup service; the new image comes from compose.yaml, the
variant must stay the same, and compose.yaml is never written:

  docker compose run --rm setup upgrade
  docker compose pull && docker compose up -d

With the committed file from deploy/compose/ (the image comes from
BEKUP_IMAGE), name the new image in the shell, so compose runs the new
image's setup, which rewrites the BEKUP_IMAGE line of .env:

  BEKUP_IMAGE=<repo>:<tag>@sha256:<digest> docker compose run --rm setup upgrade

Setup-first: --write-compose --image <new pinned image> also rewrites
compose.yaml (run with docker run, see bekup setup --help).

On a terminal the upgrade is guided (D-208): it shows the versions, the
compose changes, new secrets and the restart, asks for the backup and then
for confirmation before writing anything (--non-interactive skips it).`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if imageRaw == "" {
				imageRaw = strings.TrimSpace(os.Getenv(envSetupImage))
			}
			var img imageref.Ref
			if imageRaw != "" {
				var err error
				if img, err = imageref.ParsePinned(imageRaw); err != nil {
					return setup.PinHelp(err)
				}
			}
			mode, err := setup.ParseOwnershipMode(ownership)
			if err != nil {
				return err
			}
			passFn, err := pf.source(c, setupTerminal(c), false)
			if err != nil {
				return err
			}
			opts := setup.UpgradeOptions{
				Dir: dir, Version: version, Image: img, WriteCompose: writeCompose,
				AllowVersionMismatch: allowVersionMismatch, AllowDowngrade: allowDowngrade,
				Force: force, Ownership: mode, CAPassphrase: passFn, Out: c.OutOrStdout(),
			}
			if !nonInteractive {
				if ui, ok := setupUI(c, plain); ok {
					opts.Confirm = wizard.UpgradeConfirm(ui)
				}
			}
			return setupUpgrade(opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&dir, "dir", setupDefaultDir, "deployment directory created by setup init")
	f.BoolVar(&writeCompose, "write-compose", false, "setup-first deployments: rewrite compose.yaml for --image (refused compose-first)")
	f.StringVar(&imageRaw, "image", "", "the new bekup image pinned by digest (default: $BEKUP_IMAGE; compose-first: must equal the x-bekup-image line, or BEKUP_IMAGE for the committed file)")
	f.BoolVar(&allowDowngrade, "allow-downgrade", false, "allow a bekup version older than the recorded one")
	f.BoolVar(&allowVersionMismatch, "allow-version-mismatch", false, "accept an image tag that differs from this bekup version")
	f.BoolVar(&force, "force", false, "replace locally edited generated files (a .bak-<timestamp> copy is kept)")
	f.StringVar(&ownership, "ownership", string(setup.OwnershipAuto), "auto, chown, print or none")
	f.BoolVar(&nonInteractive, "non-interactive", false, "no guided confirmation on a terminal")
	f.BoolVar(&plain, "plain", false, "plain-text interface for the guided upgrade (NO_COLOR only drops the colors)")
	pf.register(cmd)
	return cmd
}

func newSetupRenewCmd() *cobra.Command {
	var (
		dir, ownership, caPhase, passFile string
		passStdin, all, list              bool
		nonInteractive, plain             bool
		components                        []string
		warnDays                          int
	)
	cmd := &cobra.Command{
		Use:   "renew",
		Short: "List and renew the internal certificates (runner, serve, postgres; CA rotation)",
		Long: `List the internal certificates of the deployment directory with their expiry
and renew them (D-208). Warnings start ` + strconv.Itoa(certs.ExpiryWarnDays) + ` days before expiry, the threshold
of the hourly security check and setup check.

The CA key stays offline, so nothing renews automatically: mount the CA
media at <keys>/certs/ca for the run, e.g.

  docker run --rm -it --log-driver none --network none --read-only --tmpfs /tmp \
    --user 0:0 --cap-drop ALL --cap-add CHOWN --security-opt no-new-privileges \
    -v "$PWD":/deploy -v /media/usb/bekup-ca:/deploy/secrets/certs/ca "$IMAGE" setup renew

On a terminal it is guided. Otherwise (or with --non-interactive):

  setup renew --list                 show the certificates, change nothing
  setup renew                        renew the expiring leaf certificates
  setup renew --all                  renew every leaf certificate
  setup renew --component serve      renew exactly these
  setup renew --ca stage|promote|finish   one phase of the CA rotation (D-090)

It sets the owners and modes of the manifest again and prints the services
to recreate (docker compose up -d --force-recreate <services>). A CA
rotation is resumable: the files of the CA directory tell the next phase;
recreate every service after each phase. setup has no database access, so
the renewal is not audited: it prints the new fingerprints as evidence.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			mode, err := setup.ParseOwnershipMode(ownership)
			if err != nil {
				return err
			}
			guided := !nonInteractive && !list && !all && len(components) == 0 && caPhase == "" && passFile == "" && !passStdin
			if guided {
				if ui, ok := setupUI(c, plain); ok {
					return runRenewWizard(ui, dir, mode, warnDays)
				}
			}
			opts := setup.RenewOptions{
				Dir: dir, WarnDays: warnDays, All: all, Components: components, CAPhase: certs.CAPhase(caPhase),
				ListOnly: list, Ownership: mode, Out: c.OutOrStdout(),
			}
			switch caPhase {
			case "", string(certs.CAStage), string(certs.CAPromote), string(certs.CAFinish):
			default:
				return fmt.Errorf("--ca must be stage, promote or finish (got %q)", caPhase)
			}
			switch {
			case passFile != "" && passStdin:
				return errors.New("--ca-passphrase-file and --ca-passphrase-stdin are mutually exclusive")
			case passFile != "":
				opts.Passphrase = func() ([]byte, error) { return readPassphraseFile(passFile) }
			case passStdin:
				opts.Passphrase = func() ([]byte, error) { return readPassphraseLine(c.InOrStdin()) }
			default:
				t := setupTerminal(c)
				opts.Passphrase = func() ([]byte, error) {
					pass, err := t.ExistingPassphrase()
					if errors.Is(err, setup.ErrNoTerminal) {
						return nil, errors.New("no terminal for the CA key passphrase prompt: use --ca-passphrase-file or --ca-passphrase-stdin")
					}
					return pass, err
				}
			}
			return setupRenew(opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&dir, "dir", setupDefaultDir, "deployment directory created by setup init")
	f.BoolVar(&list, "list", false, "list the certificates and their expiry; change nothing")
	f.BoolVar(&all, "all", false, "renew every leaf certificate, not only the expiring ones")
	f.StringSliceVar(&components, "component", nil, "renew exactly this leaf certificate: runner, serve or postgres (repeatable)")
	f.StringVar(&caPhase, "ca", "", "run one phase of the CA rotation: stage, promote or finish")
	f.IntVar(&warnDays, "warn-days", certs.ExpiryWarnDays, "renew (and warn about) certificates expiring within this many days")
	f.StringVar(&passFile, "ca-passphrase-file", "", "file with the CA key passphrase")
	f.BoolVar(&passStdin, "ca-passphrase-stdin", false, "read the CA key passphrase from the first line of stdin")
	f.StringVar(&ownership, "ownership", string(setup.OwnershipAuto), "auto, chown, print or none")
	f.BoolVar(&nonInteractive, "non-interactive", false, "never start the guided renewal")
	f.BoolVar(&plain, "plain", false, "plain-text interface for the guided renewal (NO_COLOR only drops the colors)")
	return cmd
}

func newSetupComposeCmd() *cobra.Command {
	var variant, imageRaw, database string
	cmd := &cobra.Command{
		Use:   "compose --variant traefik|traefik-external|npm|full [--database bundled|external] [--image <pinned>]",
		Short: "Print a compose file variant (the release assets; local examples)",
		Long: `Print one compose file variant rendered from the compose file embedded in this
binary (D-205): traefik (bekup with its own Traefik; release asset
compose.yaml), traefik-external (behind your Traefik;
compose.traefik-external.yaml), npm (behind Nginx Proxy Manager;
compose.npm.yaml) or full (every service; setup-first). --database external
(D-208) renders the form for your own PostgreSQL (no postgres service; release
assets compose.external-db.yaml, compose.traefik-external.external-db.yaml,
compose.npm.external-db.yaml).

With --image the x-bekup-image line names that digest-pinned reference (the
release workflow renders its assets this way); without it the line takes the
image from BEKUP_IMAGE with no default (the committed files in
deploy/compose/, written by just gen; setup requires BEKUP_IMAGE to be
digest-pinned and keeps it in .env). just compose-examples writes all
variants to dist/compose/.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			v, err := setup.ParseVariant(variant)
			if err != nil {
				return err
			}
			db, err := setup.ParseDBMode(database)
			if err != nil {
				return err
			}
			var img *imageref.Ref
			if imageRaw != "" {
				r, err := imageref.ParsePinned(imageRaw)
				if err != nil {
					return setup.PinHelp(err)
				}
				img = &r
			}
			out, err := setup.RenderRelease(v, db, img)
			if err != nil {
				return err
			}
			_, err = c.OutOrStdout().Write(out)
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&variant, "variant", "", "traefik, traefik-external, npm or full (required)")
	f.StringVar(&imageRaw, "image", "", "pin the x-bekup-image line to this repo[:tag]@sha256:<digest>")
	f.StringVar(&database, "database", string(setup.DBBundled), "bundled (the postgres service) or external (your own PostgreSQL, D-208)")
	return cmd
}

func newSetupCheckCmd() *cobra.Command {
	var (
		dir, ownership string
		fix            bool
		warnDays       int
	)
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Verify a deployment directory (read-only; never reads secret contents)",
		Long: `Check the deployment directory against the ownership manifest (presence,
type, mode, owner, no symlinks), that every image is digest-pinned and the
generated files are unchanged, that bekup.toml / runner.toml validate and
agree with .env (base_url, trusted_proxies, listen addresses, the link
network of the external-proxy variants, Docker GID), certificate expiry,
shell variables overriding .env, and whether the CA key is still on disk.

Compose-first (D-205) it also recognizes compose.yaml (the recorded variant,
the pinned x-bekup-image; a newer image asks for setup upgrade). With the
committed file from deploy/compose/, BEKUP_IMAGE in .env must be pinned,
equal the recorded image and not be overridden by the environment. The
hardening lint checks the project docker compose would run: compose.yaml,
compose.override.yaml and .env resolved with the Docker Compose loader
(every document, merge key, !reset/!override and ${VAR}): bekup's own
services, networks, volumes, secrets and configs must equal the shipped
variant's except for the operator-tunable attributes (resource limits,
restart, the documented proxy, runner and serve overrides); services you
add keep an allowlist (pinned image, read-only, cap_drop ALL,
no-new-privileges, non-root, binds only from relative paths inside the
deployment directory and never of bekup's files, no host namespaces,
devices, privileged hooks, ports, bekup's volumes, secrets or internal
networks); only bekup's compose variables, and only where the shipped file
uses them. Multi-document files and DOCKER_HOST-style variables in .env are
refused. The lint is a guardrail: only the unchanged release file without
an override counts as verified (docs/installation/compose.md):

  docker compose -f compose.yaml run --rm setup check

-f compose.yaml keeps compose.override.yaml out of the container that runs
the check (an override could otherwise change the setup service itself);
check still reads and lints the override. A tampered compose.yaml also
defines the checking container: without compose (or for a file you do not
trust), run the attested release image with the same hardening:

  docker run --rm --log-driver none --network none --read-only --tmpfs /tmp \
    --user 0:0 --cap-drop ALL --cap-add CHOWN --security-opt no-new-privileges \
    -v /opt/bekup:/deploy:ro "$IMAGE" setup check

Exits non-zero on errors. --fix-ownership re-applies the
manifest's modes and owners first (needs CAP_CHOWN).`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			mode, err := setup.ParseOwnershipMode(ownership)
			if err != nil {
				return err
			}
			_, err = setup.Check(setup.CheckOptions{
				Dir: dir, Version: version, FixOwnership: fix, Ownership: mode,
				WarnDays: warnDays, Out: c.OutOrStdout(),
			})
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&dir, "dir", setupDefaultDir, "deployment directory created by setup init")
	f.BoolVar(&fix, "fix-ownership", false, "re-apply the manifest's modes and owners (e.g. after certs rotate)")
	f.StringVar(&ownership, "ownership", string(setup.OwnershipAuto), "auto, chown, print or none (owner checks are skipped with none)")
	f.IntVar(&warnDays, "warn-days", 30, "warn about certificates expiring within this many days")
	return cmd
}
