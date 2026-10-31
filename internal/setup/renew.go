package setup

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/crypto"
)

// Certificate renewal of a deployment directory (`bekup setup renew`,
// D-208): it lists the internal certificates with their expiry (warning
// from certs.ExpiryWarnDays, the threshold of the hourly security check),
// renews the leaf certificates from the CA the operator mounts (the CA key
// stays offline: nothing renews automatically), sets the manifest's owners
// and modes again and names exactly the compose services to recreate. The
// CA itself is rotated with the stage -> promote -> finish procedure
// (D-090), one phase per run, resumable: the files of the CA directory
// tell which phase comes next. setup has no database access, so a renewal
// is not audited; it prints the new fingerprints as evidence.

// RotationState is the progress of a CA rotation.
type RotationState string

// CA rotation states.
const (
	// RotationNone: no rotation in progress (next phase: stage).
	RotationNone RotationState = "none"
	// RotationStaged: the next CA exists and every bundle trusts both
	// (next phase: promote).
	RotationStaged RotationState = "staged"
	// RotationPromoted: the next CA is current, the leaves are reissued,
	// the bundles still trust the previous CA (next phase: finish).
	RotationPromoted RotationState = "promoted"
	// RotationUnknown: the CA directory is not mounted.
	RotationUnknown RotationState = "unknown"
)

// NextPhase is the CA rotation phase that follows the state ("" when the
// CA is not mounted).
func (r RotationState) NextPhase() certs.CAPhase {
	switch r {
	case RotationNone:
		return certs.CAStage
	case RotationStaged:
		return certs.CAPromote
	case RotationPromoted:
		return certs.CAFinish
	case RotationUnknown:
		return ""
	}
	return ""
}

// CertStatus is one internal certificate of the deployment.
type CertStatus struct {
	// Path is relative to the deployment directory.
	Path string
	// Component is runner, serve, postgres (leaves), ca, ca-next or
	// ca-prev.
	Component string
	Subject   string
	NotAfter  time.Time
	SHA256    string
	Expired   bool
	// Expiring: valid now, but expires within the warning period.
	Expiring bool
}

// RenewPlan is what a renew run found and will do.
type RenewPlan struct {
	Root         string
	Certificates []CertStatus
	Rotation     RotationState
	// CAMounted: <keys>/certs/ca holds the CA certificate and key.
	CAMounted bool
	// CADir is the CA directory relative to the deployment directory
	// (<keys>/certs/ca), where the operator mounts the offline media.
	CADir string
	// Components are the leaf components of the deployment.
	Components []string
	// Renew are the leaf components that will be renewed (leaf mode).
	Renew []string
	// Phase is the CA rotation phase that will run ("" in leaf mode).
	Phase certs.CAPhase
	// Restart are the compose services to recreate afterwards.
	Restart []string
	// WarnDays is the warning period used.
	WarnDays int
}

// RestartCommand is the command that loads the new files: secret files are
// bind mounts of the replaced files, so the containers are recreated.
func (p *RenewPlan) RestartCommand() string {
	if len(p.Restart) == 0 {
		return ""
	}
	return "docker compose up -d --force-recreate " + strings.Join(p.Restart, " ")
}

// RenewOptions configure Renew.
type RenewOptions struct {
	Dir string
	// WarnDays is the warning period (default certs.ExpiryWarnDays).
	WarnDays int
	// All renews every leaf certificate, not only the expiring ones.
	All bool
	// Components renews exactly these leaves (overrides the selection).
	Components []string
	// CAPhase runs one phase of the CA rotation instead of a leaf renewal.
	CAPhase certs.CAPhase
	// ListOnly prints the certificates and changes nothing.
	ListOnly  bool
	Ownership OwnershipMode
	// Passphrase returns the passphrase of the CA key (called only when
	// the key is needed); Renew zeroes it.
	Passphrase func() ([]byte, error)
	// Confirm is called with the plan before anything changes; an error
	// aborts without changes.
	Confirm func(*RenewPlan) error
	Out     io.Writer
	Now     func() time.Time

	sys       *sysEnv
	rotate    certsRotateFunc
	rotateCA  func(certs.RotateCAOptions) (*certs.Result, error)
	renewHook func() // tests: called after the certificates changed
}

func (o *RenewOptions) defaults() {
	if o.WarnDays <= 0 {
		o.WarnDays = certs.ExpiryWarnDays
	}
	if o.Ownership == "" {
		o.Ownership = OwnershipAuto
	}
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.sys == nil {
		s := defaultSysEnv()
		o.sys = &s
	}
	if o.rotate == nil {
		o.rotate = certs.Rotate
	}
	if o.rotateCA == nil {
		o.rotateCA = certs.RotateCA
	}
}

// componentService maps a certificate component to its compose service.
var componentService = map[string]string{"runner": "runner", "serve": "serve", "postgres": "postgres"}

// InspectCertificates lists the internal certificates of the deployment
// directory dir (read-only) and the CA rotation state.
func InspectCertificates(dir string, now time.Time, warnDays int) (*RenewPlan, error) {
	if warnDays <= 0 {
		warnDays = certs.ExpiryWarnDays
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	st, err := readState(root)
	if err != nil {
		return nil, err
	}
	return inspectCerts(root, st, now, warnDays)
}

func inspectCerts(root string, st *State, now time.Time, warnDays int) (*RenewPlan, error) {
	l := Layout{KeysDir: st.KeysDir}
	p := &RenewPlan{Root: root, Components: certComponentsFor(st.dbMode()), WarnDays: warnDays, CADir: l.keys(certsDirName, caDirName)}
	warn := time.Duration(warnDays) * 24 * time.Hour
	add := func(rel, comp string) error {
		fi, err := checkNoSymlinks(root, rel)
		if err != nil || fi == nil {
			return err
		}
		abs, err := resolve(root, rel)
		if err != nil {
			return err
		}
		infos, err := certs.InspectFile(abs)
		if err != nil {
			return err
		}
		in := infos[0]
		p.Certificates = append(p.Certificates, CertStatus{
			Path: rel, Component: comp, Subject: in.Subject, NotAfter: in.NotAfter, SHA256: in.SHA256,
			Expired: in.Expired(now) || in.NotYetValid(now), Expiring: !in.Expired(now) && in.ExpiresWithin(now, warn),
		})
		return nil
	}
	for _, c := range p.Components {
		if err := add(l.keys(certsDirName, c, c+".crt"), c); err != nil {
			return nil, err
		}
	}
	caDir := l.keys(certsDirName, caDirName)
	for _, f := range []struct{ name, comp string }{{"ca.crt", "ca"}, {"ca.next.crt", "ca-next"}, {"ca.prev.crt", "ca-prev"}} {
		if err := add(caDir+"/"+f.name, f.comp); err != nil {
			return nil, err
		}
	}
	exists := func(rel string) (bool, error) {
		fi, err := checkNoSymlinks(root, rel)
		return fi != nil, err
	}
	crt, err := exists(caDir + "/ca.crt")
	if err != nil {
		return nil, err
	}
	key, err := exists(caDir + "/" + caKeyName)
	if err != nil {
		return nil, err
	}
	p.CAMounted = crt && key
	next, err := exists(caDir + "/ca.next.crt")
	if err != nil {
		return nil, err
	}
	prev, err := exists(caDir + "/ca.prev.crt")
	if err != nil {
		return nil, err
	}
	switch {
	case !crt:
		p.Rotation = RotationUnknown
	case next:
		p.Rotation = RotationStaged
	case prev:
		p.Rotation = RotationPromoted
	default:
		p.Rotation = RotationNone
	}
	return p, nil
}

// Due returns the leaf components whose certificate expired or expires
// within the warning period (or is missing).
func (p *RenewPlan) Due() []string {
	var out []string
	for _, c := range p.Components {
		found := false
		for _, cs := range p.Certificates {
			if cs.Component == c {
				found = true
				if cs.Expired || cs.Expiring {
					out = append(out, c)
				}
			}
		}
		if !found {
			out = append(out, c)
		}
	}
	return out
}

// Renew renews certificates of a deployment directory (see above).
func Renew(opts RenewOptions) (retErr error) {
	opts.defaults()
	root, err := filepath.Abs(opts.Dir)
	if err != nil {
		return err
	}
	st, err := readState(root)
	if err != nil {
		return err
	}
	p, err := inspectCerts(root, st, opts.Now(), opts.WarnDays)
	if err != nil {
		return err
	}
	l := Layout{KeysDir: st.KeysDir}
	if opts.ListOnly {
		return printCertList(opts.Out, p, l, opts.Now())
	}
	switch {
	case opts.CAPhase != "":
		if want := p.Rotation.NextPhase(); opts.CAPhase != want {
			return rotationPhaseError(p, opts.CAPhase, l)
		}
		p.Phase = opts.CAPhase
		for _, c := range p.Components {
			p.Restart = append(p.Restart, componentService[c])
		}
	default:
		if p.Rotation == RotationStaged || p.Rotation == RotationPromoted {
			return fmt.Errorf("a CA rotation is in progress (%s): continue it with `bekup setup renew --ca %s` before renewing single certificates", p.Rotation, p.Rotation.NextPhase())
		}
		switch {
		case len(opts.Components) > 0:
			for _, c := range opts.Components {
				if !slices.Contains(p.Components, c) {
					return fmt.Errorf("--component %q: this deployment has the certificates %s", bounded(c), strings.Join(p.Components, ", "))
				}
			}
			p.Renew = slices.Clone(opts.Components)
		case opts.All:
			p.Renew = slices.Clone(p.Components)
		default:
			p.Renew = p.Due()
		}
		for _, c := range p.Renew {
			p.Restart = append(p.Restart, componentService[c])
		}
	}
	if len(p.Renew) == 0 && p.Phase == "" {
		if err := printCertList(opts.Out, p, l, opts.Now()); err != nil {
			return err
		}
		_, err := fmt.Fprintf(opts.Out, "\nNothing to renew: no certificate expires within %d days (renew anyway with --all).\n", p.WarnDays)
		return err
	}
	needKey := p.Phase != certs.CAFinish
	if needKey && !p.CAMounted {
		ca := l.keys(certsDirName, caDirName)
		return fmt.Errorf("the CA key is needed but %s/ca.crt and %s/%s are not both there: mount the offline CA media at %s for this run, e.g.\n  docker run ... -v /media/usb/bekup-ca:/deploy/%s ... setup renew", ca, ca, caKeyName, ca, ca)
	}
	// No symlink anywhere on the paths the certs package writes.
	dir, err := resolve(root, l.keys(certsDirName))
	if err != nil {
		return err
	}
	targets, err := certs.InitTargets(dir, p.Components)
	if err != nil {
		return err
	}
	for _, t := range targets {
		rel, err := filepath.Rel(root, t)
		if err != nil {
			return err
		}
		if _, err := checkNoSymlinks(root, filepath.ToSlash(rel)); err != nil {
			return err
		}
	}
	if opts.Confirm != nil {
		if err := opts.Confirm(p); err != nil {
			return err
		}
	}
	var pass []byte
	if needKey {
		if opts.Passphrase == nil {
			return errors.New("the passphrase of the CA key is required")
		}
		if pass, err = opts.Passphrase(); err != nil {
			return err
		}
		defer crypto.Zero(pass)
	}
	var res *certs.Result
	if p.Phase != "" {
		res, err = opts.rotateCA(certs.RotateCAOptions{Dir: dir, Phase: p.Phase, Passphrase: pass, Now: opts.Now})
	} else {
		res, err = opts.rotate(certs.RotateOptions{Dir: dir, Components: p.Renew, Passphrase: pass, Now: opts.Now})
	}
	if err != nil {
		return fmt.Errorf("renewing: %w", err)
	}
	if opts.renewHook != nil {
		opts.renewHook()
	}
	// Owners and modes of the manifest (certs writes keys 0600 as root).
	var assets []string
	for _, a := range st.assetPaths() {
		if strings.HasPrefix(a, DeployDir+"/") {
			assets = append(assets, a)
		}
	}
	var certEntries []Entry
	for _, e := range Manifest(l, assets, st.manifestOptions()) {
		if e.Group == groupCerts || e.Path == l.keys(certsDirName) {
			certEntries = append(certEntries, e)
		}
	}
	mode, why := resolveOwnership(opts.Ownership, root, *opts.sys)
	if opts.Ownership == OwnershipAuto && st.Ownership == OwnershipNone {
		mode, why = OwnershipNone, "recorded by setup init"
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, r.Close()) }()
	var chown strings.Builder
	warnings, err := applyOwnership(r, certEntries, mode, *opts.sys, &chown)
	if err != nil {
		return fmt.Errorf("the certificates were renewed, but setting their owners failed: %w (fix it with `bekup setup check --fix-ownership`)", err)
	}
	after, err := inspectCerts(root, st, opts.Now(), opts.WarnDays)
	if err != nil {
		return err
	}
	return renewReport(opts.Out, p, after, res, l, mode, why, chown.String(), warnings)
}

// rotationPhaseError explains which CA rotation phase comes next.
func rotationPhaseError(p *RenewPlan, asked certs.CAPhase, l Layout) error {
	switch p.Rotation {
	case RotationUnknown:
		return fmt.Errorf("--ca %s: the CA is not mounted at %s (mount the offline media for this run)", asked, l.keys(certsDirName, caDirName))
	case RotationNone:
		return fmt.Errorf("--ca %s: no CA rotation is in progress; it starts with --ca stage", asked)
	case RotationStaged, RotationPromoted:
	}
	return fmt.Errorf("--ca %s: the CA rotation is %s; the next phase is --ca %s", asked, p.Rotation, p.Rotation.NextPhase())
}

// printCertList prints the certificates with their expiry and the CA
// rotation state.
func printCertList(out io.Writer, p *RenewPlan, l Layout, now time.Time) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Internal certificates of %s (warning from %d days before expiry):\n", p.Root, p.WarnDays)
	for _, c := range p.Certificates {
		state := "ok"
		switch {
		case c.Expired:
			state = "EXPIRED"
		case c.Expiring:
			state = "EXPIRES SOON"
		}
		days := int(c.NotAfter.Sub(now).Hours() / 24)
		fmt.Fprintf(&b, "  %-9s %-44s until %s (%d days)  %s\n", c.Component, c.Path, c.NotAfter.Format("2006-01-02"), days, state)
	}
	if !p.CAMounted {
		fmt.Fprintf(&b, "  (the CA is not mounted at %s; mount the offline media to renew)\n", l.keys(certsDirName, caDirName))
	}
	fmt.Fprintf(&b, "CA rotation: %s", p.Rotation)
	if next := p.Rotation.NextPhase(); next != "" && p.Rotation != RotationNone {
		fmt.Fprintf(&b, " (next: bekup setup renew --ca %s, after recreating every service)", next)
	}
	b.WriteString("\n")
	_, err := io.WriteString(out, b.String())
	return err
}

// renewReport prints what changed, the evidence and the restart command.
func renewReport(out io.Writer, p, after *RenewPlan, res *certs.Result, l Layout, mode OwnershipMode, why, chown string, warnings []string) error {
	var b strings.Builder
	if p.Phase != "" {
		fmt.Fprintf(&b, "\nbekup setup renew: CA rotation phase %q done (%s)\n", p.Phase, p.Root)
	} else {
		fmt.Fprintf(&b, "\nbekup setup renew: renewed %s (%s)\n", strings.Join(p.Renew, ", "), p.Root)
	}
	fmt.Fprintf(&b, "  files written: %d, removed: %d; ownership %s (%s)\n", len(res.Written), len(res.Removed), mode, why)
	for _, w := range warnings {
		fmt.Fprintf(&b, "WARNING: %s\n", w)
	}
	if chown != "" {
		b.WriteString("\nOwnership is NOT applied yet (--ownership print). " + chown)
	}
	b.WriteString("\nEvidence (SHA-256 fingerprints; setup has no database access, so this is not in the\naudit log: keep it with your change records):\n")
	for _, c := range after.Certificates {
		fmt.Fprintf(&b, "  %-9s %s until %s\n", c.Component, c.SHA256, c.NotAfter.Format("2006-01-02"))
	}
	fmt.Fprintf(&b, "\nNext: %s\n", p.RestartCommand())
	switch after.Rotation {
	case RotationStaged:
		b.WriteString("Then, once every service runs with the new trust bundles: bekup setup renew --ca promote\n")
	case RotationPromoted:
		b.WriteString("Then, once every service runs with its new certificate: bekup setup renew --ca finish\n")
	case RotationNone, RotationUnknown:
	}
	fmt.Fprintf(&b, "Keep the CA key offline: unmount the media at %s when done.\n", l.keys(certsDirName, caDirName))
	_, err := io.WriteString(out, b.String())
	return err
}
