package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/imageref"
	"github.com/jlentink/bekup/internal/setup"
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
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Write and maintain a container-only compose deployment directory",
		Long: `Create, upgrade and check a compose deployment directory from the bekup image
alone (no source checkout; D-203, D-205).

Compose-first (the default): save a compose file as compose.yaml
(compose.yaml, compose.traefik-external.yaml or compose.npm.yaml: a release
asset from https://github.com/jlentink/bekup/releases/latest, pinned and
attested, or the committed file from deploy/compose/ in the repository,
which takes the image from BEKUP_IMAGE) and run its setup service, a
one-shot root container with only CAP_CHOWN, no network and no logs:

  DOCKER_GID=$(stat -c %g /var/run/docker.sock) docker compose run --rm setup
  docker compose up -d

With the committed file, prefix the first command with
BEKUP_IMAGE=<repo>:<tag>@sha256:<digest>; setup writes it to .env.

setup writes everything except compose.yaml: .env, bekup.toml, runner.toml,
the secrets and certificates, deploy/** and reference/*.

Setup-first (--write-compose --variant ...): setup also writes compose.yaml,
run with docker run and the same hardening:

  docker run --rm -it --log-driver none --network none --read-only --tmpfs /tmp \
    --user 0:0 --cap-drop ALL --cap-add CHOWN --security-opt no-new-privileges \
    -e BEKUP_IMAGE="$IMAGE" -v /opt/bekup:/deploy \
    "$IMAGE" setup init --write-compose --variant full --docker-gid "$(stat -c %g /var/run/docker.sock)"

--log-driver none keeps the container's output (the passphrase prompt, a
generated passphrase) out of Docker's logs. See docs/installation/compose.md.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error { return c.Help() },
	}
	cmd.AddCommand(newSetupInitCmd(), newSetupUpgradeCmd(), newSetupCheckCmd(), newSetupComposeCmd())
	return cmd
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
// are generated).
func (p *caPassFlags) source(c *cobra.Command, t *setup.Terminal) (func() ([]byte, error), error) {
	n := 0
	for _, b := range []bool{p.file != "", p.stdin, p.generate} {
		if b {
			n++
		}
	}
	if n > 1 {
		return nil, errors.New("--ca-passphrase-file, --ca-passphrase-stdin and --generate-ca-passphrase are mutually exclusive")
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
			out := append([]byte(nil), s.Bytes()...)
			w := c.ErrOrStderr()
			_, _ = fmt.Fprintln(w, "\nGenerated CA key passphrase (shown ONCE; store it offline with the CA, e.g. in a password manager).\nWARNING: Docker keeps container output in its logs unless the setup container runs with --log-driver none:")
			_, _ = fmt.Fprintf(w, "\n    %s\n\n", s.Bytes())
			return out, nil
		}, nil
	}
	return func() ([]byte, error) {
		pass, err := t.NewPassphrase()
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

func newSetupInitCmd() *cobra.Command {
	var (
		dir, imageRaw, proxy, domain, tlsMode, acmeEmail, keysDir, ownership string
		variant, serveIP, proxyIP                                            string
		acmeStaging, nonInteractive, keepExisting, force, regenerate         bool
		allowVersionMismatch, writeCompose                                   bool
		dockerGID                                                            int
		targetCIDRs, targetHosts, trusted                                    []string
		net                                                                  setup.Network
		link                                                                 setup.LinkNetwork
		traefikEntrypoint, traefikCertResolver                               string
		pf                                                                   caPassFlags
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Set up a deployment directory (config, secrets, certificates, deploy assets)",
		Long: `Set up a compose deployment directory: deploy/**, reference/*.example.toml,
.env, minimal bekup.toml and runner.toml, the database passwords and URLs,
the master keyring and the private CA with the runner/serve/postgres
certificates, then hand every file to the container user that reads it
(--ownership).

Compose-first (default, D-205): compose.yaml must exist (a release file, or
the committed file from deploy/compose/). setup takes the pinned image from
its x-bekup-image line (the committed file: from BEKUP_IMAGE, which must be
digest-pinned and is written to .env), recognizes the variant (traefik,
traefik-external, npm; a customized file needs --variant and passes the
hardening lint) and never writes compose.yaml. It asks only
for the domain, the ACME e-mail (own Traefik), the CA key passphrase and,
unless DOCKER_GID is set, the Docker socket's group.

Setup-first (--write-compose --variant traefik|traefik-external|npm|full):
setup also writes compose.yaml (full: every service, --proxy traefik, nginx
or none).

On a terminal, missing answers are prompted; --non-interactive requires the
flags instead. Existing files are never overwritten without --force (non-secret
files only) or --regenerate-secrets (terminal + typed confirmation).`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if imageRaw == "" {
				imageRaw = strings.TrimSpace(os.Getenv(envSetupImage))
			}
			mode, err := setup.ParseOwnershipMode(ownership)
			if err != nil {
				return err
			}
			fl := c.Flags()
			var v setup.Variant
			if variant != "" {
				if v, err = setup.ParseVariant(variant); err != nil {
					return err
				}
			}
			a := setup.Answers{
				Variant: v, Proxy: setup.Proxy(proxy), Domain: domain, TLSMode: setup.TLSMode(tlsMode),
				ACMEEmail: acmeEmail, ACMEStaging: acmeStaging, DockerGID: dockerGID,
				TargetCIDRs: targetCIDRs, TargetHosts: targetHosts, TrustedProxies: trusted,
				KeysDir: keysDir, Network: net, Link: link,
				TraefikEntrypoint: traefikEntrypoint, TraefikCertResolver: traefikCertResolver,
			}
			given := setup.Given{
				Image: imageRaw != "", Proxy: fl.Changed("proxy"), Domain: fl.Changed("domain"),
				TLSMode: fl.Changed("tls-mode"), ACMEEmail: fl.Changed("acme-email"),
				ACMEStaging: fl.Changed("acme-staging"), DockerGID: fl.Changed("docker-gid"),
				Targets:      fl.Changed("target-cidr") || fl.Changed("target-host"),
				TrustedProxy: fl.Changed("trusted-proxy"),
				ComposeFirst: !writeCompose,
			}
			if !given.DockerGID {
				if raw := strings.TrimSpace(os.Getenv(envDockerGID)); raw != "" {
					gid, err := strconv.Atoi(raw)
					if err != nil || gid < 0 {
						return fmt.Errorf("%s=%q is not a numeric group ID (stat -c %%g /var/run/docker.sock)", envDockerGID, raw)
					}
					a.DockerGID, given.DockerGID = gid, true
				}
			}
			out := c.OutOrStdout()
			if writeCompose {
				if v == "" {
					return errors.New("--write-compose needs --variant traefik, traefik-external, npm or full")
				}
			} else {
				info, err := setup.InspectCompose(dir, v)
				if err != nil {
					return err
				}
				if imageRaw != "" && imageRaw != info.Image.String() {
					if info.FromEnv {
						return fmt.Errorf("--image %s does not match BEKUP_IMAGE=%s, which the x-bekup-image line of compose.yaml resolves to: set BEKUP_IMAGE instead (BEKUP_IMAGE=<image> docker compose run --rm setup)", imageRaw, info.Image)
					}
					return fmt.Errorf("BEKUP_IMAGE/--image %s does not match the x-bekup-image line of compose.yaml (%s): run setup from that file (docker compose run --rm setup)", imageRaw, info.Image)
				}
				a.Variant, a.Image = info.Variant, info.Image
				from := ""
				if info.FromEnv {
					from = " (from BEKUP_IMAGE; setup keeps it in .env)"
				}
				_, _ = fmt.Fprintf(out, "compose.yaml: the %s variant (%s), image %s%s\n", info.Variant, info.Level, info.Image, from)
				for _, f := range info.Findings {
					_, _ = fmt.Fprintln(out, f.String())
				}
			}
			applyAddressFlags(&a, serveIP, proxyIP)
			t := setupTerminal(c)
			if err := setup.Complete(&a, given, imageRaw, t, nonInteractive); err != nil {
				return err
			}
			if !allowVersionMismatch {
				if err := setup.CheckVersionTag(a.Image, version); err != nil {
					return err
				}
			}
			if regenerate {
				if nonInteractive {
					return errors.New("--regenerate-secrets needs a terminal and a typed confirmation (not --non-interactive)")
				}
				if err := t.Confirm(regenerateWarning, "regenerate secrets"); err != nil {
					return err
				}
			}
			passFn, err := pf.source(c, t)
			if err != nil {
				return err
			}
			return setup.Init(setup.Options{
				Dir: dir, Version: version, Answers: a, Ownership: mode,
				KeepExisting: keepExisting, Force: force, RegenerateSecrets: regenerate,
				WriteCompose: writeCompose, CAPassphrase: passFn, Out: out,
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&dir, "dir", setupDefaultDir, "deployment directory (must exist)")
	f.BoolVar(&writeCompose, "write-compose", false, "setup-first: also write compose.yaml (needs --variant); default is compose-first (compose.yaml exists, never written)")
	f.StringVar(&variant, "variant", "", "traefik, traefik-external, npm or full (--write-compose; compose-first only to name the base of a customized compose.yaml)")
	f.StringVar(&imageRaw, "image", "", "bekup image pinned by digest, repo[:tag]@sha256:<digest> (default: $BEKUP_IMAGE; compose-first: must equal the x-bekup-image line, or BEKUP_IMAGE for the committed file)")
	f.BoolVar(&nonInteractive, "non-interactive", false, "never prompt; every answer must come from flags")
	f.StringVar(&proxy, "proxy", "", "variant full: reverse proxy traefik, nginx or none (your own proxy on the frontend network)")
	f.StringVar(&domain, "domain", "", "public host name of the UI (base_url https://<domain>)")
	f.StringVar(&tlsMode, "tls-mode", "", "bundled proxy TLS: acme (Let's Encrypt, default) or static (your certificate in proxy-tls/)")
	f.StringVar(&acmeEmail, "acme-email", "", "ACME account e-mail (--tls-mode acme)")
	f.BoolVar(&acmeStaging, "acme-staging", false, "use the Let's Encrypt staging CA (testing)")
	f.IntVar(&dockerGID, "docker-gid", 0, "group ID of the Docker socket on the host: stat -c %g /var/run/docker.sock (default: $DOCKER_GID)")
	f.StringSliceVar(&targetCIDRs, "target-cidr", nil, "CIDR of target databases the runner may reach (repeatable)")
	f.StringSliceVar(&targetHosts, "target-host", nil, "host name of a target database the runner may reach (repeatable)")
	f.StringSliceVar(&trusted, "trusted-proxy", nil, "CIDR of your own reverse proxy on the frontend network (--proxy none; repeatable)")
	f.StringVar(&keysDir, "keys-dir", setup.DefaultKeysDir, "secrets directory, relative to --dir (mount another volume there to keep secrets elsewhere)")
	f.StringVar(&ownership, "ownership", string(setup.OwnershipAuto), "auto, chown (needs CAP_CHOWN), print (show the chown commands) or none (Docker Desktop)")
	f.BoolVar(&keepExisting, "keep-existing", false, "keep existing files and create only the missing ones")
	f.BoolVar(&force, "force", false, "rewrite existing NON-SECRET files (deploy, reference, .env, TOMLs; compose.yaml only with --write-compose)")
	f.BoolVar(&regenerate, "regenerate-secrets", false, "delete and regenerate EVERY secret and certificate (terminal + typed confirmation)")
	f.BoolVar(&allowVersionMismatch, "allow-version-mismatch", false, "accept an image tag that differs from this bekup version")
	f.StringVar(&traefikEntrypoint, "traefik-entrypoint", "", "traefik-external: your Traefik's HTTPS entrypoint (default websecure)")
	f.StringVar(&traefikCertResolver, "traefik-certresolver", "", "traefik-external: your Traefik's certificate resolver (default letsencrypt)")
	f.StringVar(&link.Name, "proxy-network", "", "traefik-external/npm: name of the external link network (default bekup-proxy)")
	f.StringVar(&link.Subnet, "proxy-subnet", "", "traefik-external/npm: subnet of the link network (default 10.213.253.0/28)")
	f.StringVar(&serveIP, "serve-ip", "", "advanced: serve's fixed address towards the proxy: SERVE_FRONTEND_IP (default 10.213.250.10) or, traefik-external/npm, SERVE_PROXY_IP (default 10.213.253.10)")
	f.StringVar(&proxyIP, "proxy-ip", "", "advanced: the proxy's fixed address: PROXY_FRONTEND_IP (default 10.213.250.5) or, traefik-external/npm, your proxy's address on the link network (default 10.213.253.5)")
	f.StringVar(&net.FrontendSubnet, "frontend-subnet", "", "advanced: FRONTEND_SUBNET (default 10.213.250.0/24; not traefik-external/npm)")
	f.StringVar(&net.RunnerCtlSubnet, "runner-ctl-subnet", "", "advanced: RUNNER_CTL_SUBNET (default 10.213.251.0/24)")
	f.StringVar(&net.RunnerCtlIP, "runner-ip", "", "advanced: RUNNER_CTL_IP (default 10.213.251.10)")
	f.StringVar(&net.MetricsSubnet, "metrics-subnet", "", "advanced: METRICS_SUBNET (default 10.213.252.0/24)")
	f.StringVar(&net.ServeMetricsIP, "metrics-ip", "", "advanced: SERVE_METRICS_IP (default 10.213.252.10)")
	pf.register(cmd)
	return cmd
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
    the new passwords with ALTER ROLE;
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
		writeCompose                                bool
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
compose.yaml (run with docker run, see bekup setup --help).`,
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
			passFn, err := pf.source(c, setupTerminal(c))
			if err != nil {
				return err
			}
			return setup.Upgrade(setup.UpgradeOptions{
				Dir: dir, Version: version, Image: img, WriteCompose: writeCompose,
				AllowVersionMismatch: allowVersionMismatch, AllowDowngrade: allowDowngrade,
				Force: force, Ownership: mode, CAPassphrase: passFn, Out: c.OutOrStdout(),
			})
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
	pf.register(cmd)
	return cmd
}

func newSetupComposeCmd() *cobra.Command {
	var variant, imageRaw string
	cmd := &cobra.Command{
		Use:   "compose --variant traefik|traefik-external|npm|full [--image <pinned>]",
		Short: "Print a compose file variant (the release assets; local examples)",
		Long: `Print one compose file variant rendered from the compose file embedded in this
binary (D-205): traefik (bekup with its own Traefik; release asset
compose.yaml), traefik-external (behind your Traefik;
compose.traefik-external.yaml), npm (behind Nginx Proxy Manager;
compose.npm.yaml) or full (every service; setup-first).

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
			var img *imageref.Ref
			if imageRaw != "" {
				r, err := imageref.ParsePinned(imageRaw)
				if err != nil {
					return setup.PinHelp(err)
				}
				img = &r
			}
			out, err := setup.RenderRelease(v, img)
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
