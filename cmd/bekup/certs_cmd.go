package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/config"
)

const day = 24 * time.Hour

// readPassword reads a passphrase from the terminal without echo. It is a
// variable so tests can replace it; ok=false means stdin is not a terminal.
var readPassword = func(prompt string, w io.Writer) (_ []byte, ok bool, _ error) {
	fd := int(os.Stdin.Fd()) // #nosec G115 -- file descriptors fit in int
	if !term.IsTerminal(fd) {
		return nil, false, nil
	}
	if _, err := fmt.Fprint(w, prompt); err != nil {
		return nil, true, err
	}
	p, err := term.ReadPassword(fd)
	_, _ = fmt.Fprintln(w)
	return p, true, err
}

// passphraseFlags are shared by the subcommands that handle the CA key.
type passphraseFlags struct {
	file        string
	unencrypted bool
}

func (p *passphraseFlags) register(cmd *cobra.Command, withUnencrypted bool) {
	cmd.Flags().StringVar(&p.file, "ca-passphrase-file", "",
		"file with the CA key passphrase (min 16 characters); default: prompt on the terminal")
	if withUnencrypted {
		cmd.Flags().BoolVar(&p.unencrypted, "unencrypted-ca-key", false,
			"write the CA key WITHOUT a passphrase (not recommended; only if the CA directory is moved offline at once)")
	}
}

// newPassphrase obtains a passphrase for sealing a new CA key: from the
// file, or prompted twice on the terminal. It returns nil when
// --unencrypted-ca-key is set.
func (p *passphraseFlags) newPassphrase(cmd *cobra.Command) ([]byte, error) {
	if p.unencrypted {
		if p.file != "" {
			return nil, errors.New("--unencrypted-ca-key and --ca-passphrase-file are mutually exclusive")
		}
		return nil, nil
	}
	if p.file != "" {
		return readPassphraseFile(p.file)
	}
	w := cmd.ErrOrStderr()
	first, ok, err := readPassword("New CA key passphrase (min 16 characters): ", w)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("no terminal for a passphrase prompt: use --ca-passphrase-file (or --unencrypted-ca-key)")
	}
	if err := certs.ValidatePassphrase(first); err != nil {
		clear(first)
		return nil, err
	}
	second, _, err := readPassword("Repeat the passphrase: ", w)
	defer clear(second)
	if err != nil {
		clear(first)
		return nil, err
	}
	if !bytes.Equal(first, second) {
		clear(first)
		return nil, errors.New("passphrases do not match")
	}
	return first, nil
}

// existingPassphrase obtains the passphrase to open the CA key in dir, or
// nil when that key is not encrypted.
func (p *passphraseFlags) existingPassphrase(cmd *cobra.Command, dir string) ([]byte, error) {
	enc, err := certs.CAKeyEncrypted(dir)
	if err != nil {
		return nil, fmt.Errorf("reading the CA key (rotation needs it; bring it back from offline storage): %w", err)
	}
	if !enc {
		return nil, nil
	}
	if p.file != "" {
		return readPassphraseFile(p.file)
	}
	pass, ok, err := readPassword("CA key passphrase: ", cmd.ErrOrStderr())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("the CA key is encrypted and there is no terminal: use --ca-passphrase-file")
	}
	return pass, nil
}

func readPassphraseFile(path string) ([]byte, error) {
	b, err := config.ReadSecretFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the passphrase file: %w", err)
	}
	if err := certs.ValidatePassphrase(b); err != nil {
		config.Zero(b)
		return nil, err
	}
	return b, nil
}

func newCertsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "certs",
		Short: "Create, rotate and inspect the private CA and component mTLS certificates",
		Long: `Manage the runner mTLS PKI: a private ed25519 CA plus one certificate per
component (runner = server, serve and worker = clients identified by the URI
SAN spiffe://bekup/<component>). See docs/security/mtls-certs.md.`,
	}
	cmd.AddCommand(newCertsInitCmd(), newCertsRotateCmd(), newCertsInspectCmd())
	return cmd
}

func newCertsInitCmd() *cobra.Command {
	var (
		dir        string
		components []string
		runnerSANs []string
		caDays     int
		leafDays   int
		force      bool
		pf         passphraseFlags
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a new private CA and the component certificates",
		Long: `Create a private ed25519 CA (name-constrained to the runner names and the
spiffe://bekup trust domain) and ed25519 leaf certificates. Layout:

  <dir>/ca/ca.crt, ca.key         CA (move ca.key offline!)
  <dir>/runner/runner.{crt,key}, ca.crt
  <dir>/serve/serve.{crt,key}, ca.crt
  <dir>/worker/worker.{crt,key}, ca.crt

The CA key is encrypted with an age passphrase unless --unencrypted-ca-key.
Existing material is never overwritten without --force.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if dir == "" {
				return errors.New("--dir is required")
			}
			pass, err := pf.newPassphrase(c)
			if err != nil {
				return err
			}
			defer clear(pass)
			res, err := certs.Init(certs.InitOptions{
				Dir:              dir,
				Components:       components,
				RunnerNames:      runnerSANs,
				CAValidity:       time.Duration(caDays) * day,
				LeafValidity:     time.Duration(leafDays) * day,
				Passphrase:       pass,
				AllowUnencrypted: pf.unencrypted,
				Force:            force,
			})
			if err != nil {
				return err
			}
			if err := printResult(c, res); err != nil {
				return err
			}
			return caKeyWarning(c.ErrOrStderr(), certs.Layout{Dir: dir}.CAKey(), pf.unencrypted)
		},
	}
	f := cmd.Flags()
	f.StringVar(&dir, "dir", "", "output directory (required)")
	f.StringSliceVar(&components, "component", nil, "components to issue: runner, serve, worker (repeatable; default all)")
	f.StringSliceVar(&runnerSANs, "runner-san", nil, `extra DNS name or IP for the runner certificate (repeatable; "runner" is always included; also limits the CA's name constraints)`)
	f.IntVar(&caDays, "ca-days", int(certs.DefaultCAValidity/day), "CA validity in days (max 3650)")
	f.IntVar(&leafDays, "leaf-days", int(certs.DefaultLeafValidity/day), "leaf certificate validity in days (max 825)")
	f.BoolVar(&force, "force", false, "overwrite existing certificate material (creates a NEW CA: every component must be redeployed)")
	pf.register(cmd, true)
	return cmd
}

func newCertsRotateCmd() *cobra.Command {
	var (
		dir        string
		components []string
		all        bool
		caPhase    string
		runnerSANs []string
		caDays     int
		leafDays   int
		pf         passphraseFlags
	)
	cmd := &cobra.Command{
		Use:   "rotate",
		Short: "Reissue component certificates, or rotate the CA in three phases",
		Long: `Reissue leaf certificates (new keys) from the existing CA:

  bekup certs rotate --dir <dir> --component serve [--component worker]
  bekup certs rotate --dir <dir> --all

Rotate the CA itself with an overlap period; restart every component after
each phase:

  --ca stage    create the next CA; every trust bundle trusts old + next
  --ca promote  the next CA becomes current; reissue all leaves from it
  --ca finish   drop the old CA from every trust bundle

Needs the CA key (bring it back from offline storage temporarily).`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if dir == "" {
				return errors.New("--dir is required")
			}
			if caPhase != "" && (all || len(components) > 0) {
				return errors.New("--ca cannot be combined with --component or --all")
			}
			if caPhase == "" && !all && len(components) == 0 {
				return errors.New("select --component <name>, --all or --ca <phase>")
			}
			if caPhase == "" && pf.unencrypted {
				return errors.New("--unencrypted-ca-key only applies to --ca stage")
			}
			var (
				res *certs.Result
				err error
			)
			if caPhase == "" {
				var pass []byte
				if pass, err = pf.existingPassphrase(c, dir); err != nil {
					return err
				}
				defer clear(pass)
				opts := certs.RotateOptions{
					Dir: dir, Components: components, All: all,
					LeafValidity: time.Duration(leafDays) * day, Passphrase: pass,
				}
				if c.Flags().Changed("runner-san") {
					opts.RunnerNames = append([]string{}, runnerSANs...)
				}
				res, err = certs.Rotate(opts)
			} else {
				res, err = rotateCA(c, dir, certs.CAPhase(caPhase), &pf, runnerSANs, caDays, leafDays)
			}
			if err != nil {
				return err
			}
			if err := printResult(c, res); err != nil {
				return err
			}
			_, err = fmt.Fprintln(c.ErrOrStderr(), "Restart the affected components so they load the new files.")
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&dir, "dir", "", "certificate directory created by certs init (required)")
	f.StringSliceVar(&components, "component", nil, "component to reissue: runner, serve, worker (repeatable)")
	f.BoolVar(&all, "all", false, "reissue every component present in --dir")
	f.StringVar(&caPhase, "ca", "", "rotate the CA: stage, promote or finish")
	f.StringSliceVar(&runnerSANs, "runner-san", nil, "replace the runner's extra DNS names/IPs (default: keep the current ones)")
	f.IntVar(&caDays, "ca-days", int(certs.DefaultCAValidity/day), "validity of the next CA in days (--ca stage)")
	f.IntVar(&leafDays, "leaf-days", int(certs.DefaultLeafValidity/day), "leaf certificate validity in days")
	pf.register(cmd, true)
	return cmd
}

func rotateCA(c *cobra.Command, dir string, phase certs.CAPhase, pf *passphraseFlags,
	runnerSANs []string, caDays, leafDays int,
) (*certs.Result, error) {
	opts := certs.RotateCAOptions{
		Dir: dir, Phase: phase,
		CAValidity: time.Duration(caDays) * day, LeafValidity: time.Duration(leafDays) * day,
		AllowUnencrypted: pf.unencrypted,
	}
	if c.Flags().Changed("runner-san") {
		if phase != certs.CAStage {
			return nil, errors.New("--runner-san with --ca only applies to the stage phase")
		}
		opts.RunnerNames = append([]string{}, runnerSANs...)
	}
	switch phase {
	case certs.CAStage:
		// Prove possession of the current key; the same passphrase seals
		// the next one (unless --unencrypted-ca-key).
		pass, err := pf.existingPassphrase(c, dir)
		if err != nil {
			return nil, err
		}
		if pass == nil && !pf.unencrypted {
			if pass, err = pf.newPassphrase(c); err != nil {
				return nil, err
			}
		}
		defer clear(pass)
		opts.Passphrase = pass
	case certs.CAPromote:
		pass, err := pf.existingPassphrase(c, dir)
		if err != nil {
			return nil, err
		}
		defer clear(pass)
		opts.Passphrase = pass
	case certs.CAFinish:
		// Needs no key: only trust bundles change.
	}
	return certs.RotateCA(opts)
}

func newCertsInspectCmd() *cobra.Command {
	var (
		dir      string
		warnDays int
	)
	cmd := &cobra.Command{
		Use:   "inspect [path...]",
		Short: "Show subject, SANs, validity and fingerprints; fail if anything expires soon",
		Long: `Print every certificate in --dir (all *.crt files, recursively) and/or the
given files or directories. Exits non-zero when a certificate is expired, not
yet valid, or expires within --warn-days days.`,
		RunE: func(c *cobra.Command, args []string) error {
			paths := args
			if dir != "" {
				paths = append([]string{dir}, paths...)
			}
			if len(paths) == 0 {
				return errors.New("give --dir <dir> or one or more paths")
			}
			if warnDays < 0 {
				return errors.New("--warn-days must not be negative")
			}
			var infos []certs.ExpiryInfo
			for _, p := range paths {
				in, err := certs.Inspect(p)
				if err != nil {
					return err
				}
				infos = append(infos, in...)
			}
			now := time.Now().UTC()
			warn := time.Duration(warnDays) * day
			if err := printInfos(c.OutOrStdout(), infos, now, warn); err != nil {
				return err
			}
			if n := len(certs.Expiring(infos, now, warn)); n > 0 {
				return fmt.Errorf("%d certificate(s) expired, not yet valid, or expiring within %d days", n, warnDays)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "certificate directory to inspect")
	cmd.Flags().IntVar(&warnDays, "warn-days", 30, "fail when a certificate expires within this many days")
	return cmd
}

func printInfos(w io.Writer, infos []certs.ExpiryInfo, now time.Time, warn time.Duration) error {
	var b strings.Builder
	for i := range infos {
		e := &infos[i]
		if i > 0 {
			b.WriteString("\n")
		}
		status := "OK"
		switch {
		case e.Expired(now):
			status = "EXPIRED"
		case e.NotYetValid(now):
			status = "NOT YET VALID"
		case e.ExpiresWithin(now, warn):
			status = "EXPIRING"
		}
		path := e.Path
		if e.Index > 0 {
			path = fmt.Sprintf("%s [#%d]", path, e.Index+1)
		}
		kind := "leaf"
		if e.IsCA {
			kind = "CA"
		}
		fmt.Fprintf(&b, "%s\n", path)
		fmt.Fprintf(&b, "  status:      %s (%d days left)\n", status, e.DaysLeft(now))
		fmt.Fprintf(&b, "  type:        %s\n", kind)
		fmt.Fprintf(&b, "  subject:     %s\n", e.Subject)
		fmt.Fprintf(&b, "  issuer:      %s\n", e.Issuer)
		if sans := e.SANs(); len(sans) > 0 {
			fmt.Fprintf(&b, "  SANs:        %s\n", strings.Join(sans, ", "))
		}
		fmt.Fprintf(&b, "  not before:  %s\n", e.NotBefore.Format(time.RFC3339))
		fmt.Fprintf(&b, "  not after:   %s\n", e.NotAfter.Format(time.RFC3339))
		fmt.Fprintf(&b, "  serial:      %s\n", e.Serial)
		fmt.Fprintf(&b, "  sha256:      %s\n", e.SHA256)
		fmt.Fprintf(&b, "  spki sha256: %s\n", e.SPKISHA256)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func printResult(c *cobra.Command, res *certs.Result) error {
	w := c.OutOrStdout()
	for _, p := range res.Written {
		if _, err := fmt.Fprintf(w, "wrote   %s\n", p); err != nil {
			return err
		}
	}
	for _, p := range res.Removed {
		if _, err := fmt.Fprintf(w, "removed %s\n", p); err != nil {
			return err
		}
	}
	return nil
}

func caKeyWarning(w io.Writer, caKey string, unencrypted bool) error {
	msg := `
WARNING: %s is the CA PRIVATE KEY. Whoever holds it can mint certificates that
the runner accepts, and the runner controls Docker. Move it OFFLINE now
(encrypted USB stick, password manager, HSM) and delete it from this host; it
is only needed again for "bekup certs rotate". Never mount it into a
container or put it on a shared volume. Give each component only its own
directory (runner/, serve/, worker/) via Docker secrets.
`
	if unencrypted {
		msg += `The CA key is NOT ENCRYPTED (--unencrypted-ca-key). Treat it as plaintext key material.
`
	}
	_, err := fmt.Fprintf(w, msg, caKey)
	return err
}
