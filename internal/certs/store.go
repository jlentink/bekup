package certs

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jlentink/bekup/internal/config"
)

// Output layout (see docs/security/mtls-certs.md):
//
//	<dir>/ca/ca.crt            CA certificate (0644)
//	<dir>/ca/ca.key            CA private key (0600; age-encrypted by default) -- move offline
//	<dir>/ca/ca.next.{crt,key} next CA during a CA rotation (stage .. promote)
//	<dir>/ca/ca.prev.crt       previous CA during a CA rotation (promote .. finish)
//	<dir>/<component>/<component>.crt  leaf certificate (0644); for postgres
//	                                   the chain leaf + one-time ECDSA issuer
//	<dir>/<component>/<component>.key  leaf private key (0600)
//	<dir>/<component>/ca.crt           trust bundle: the CA(s) to trust (0644)
//
// Components: runner (server), serve and worker (clients), postgres (the app
// DB's TLS server certificate, D-100; postgres/ca.crt is also the bundle
// serve/worker/migrate use as database.tls_ca_file).
//
// A component directory never contains a CA key.
const (
	caDirName      = "ca"
	caCertName     = "ca.crt"
	caKeyName      = "ca.key"
	nextCACertName = "ca.next.crt"
	nextCAKeyName  = "ca.next.key"
	prevCACertName = "ca.prev.crt"
	bundleName     = "ca.crt"

	dirPerm  fs.FileMode = 0o700
	keyPerm  fs.FileMode = 0o600
	certPerm fs.FileMode = 0o644
)

// ErrExists is returned when init would overwrite existing material.
var ErrExists = errors.New("certificate material already exists (use --force to overwrite)")

// Layout resolves the paths below a certificate directory.
type Layout struct{ Dir string }

// CADir is the directory with the CA material.
func (l Layout) CADir() string { return filepath.Join(l.Dir, caDirName) }

// CACert is the current CA certificate.
func (l Layout) CACert() string { return filepath.Join(l.CADir(), caCertName) }

// CAKey is the current CA private key.
func (l Layout) CAKey() string { return filepath.Join(l.CADir(), caKeyName) }

func (l Layout) nextCACert() string { return filepath.Join(l.CADir(), nextCACertName) }
func (l Layout) nextCAKey() string  { return filepath.Join(l.CADir(), nextCAKeyName) }
func (l Layout) prevCACert() string { return filepath.Join(l.CADir(), prevCACertName) }

// ComponentDir is the directory with only the component's own material.
func (l Layout) ComponentDir(c string) string { return filepath.Join(l.Dir, c) }

// CertFile is the component's leaf certificate.
func (l Layout) CertFile(c string) string { return filepath.Join(l.Dir, c, c+".crt") }

// KeyFile is the component's private key.
func (l Layout) KeyFile(c string) string { return filepath.Join(l.Dir, c, c+".key") }

// BundleFile is the component's CA trust bundle.
func (l Layout) BundleFile(c string) string { return filepath.Join(l.Dir, c, bundleName) }

// keyOptions controls how a CA key is stored.
type keyOptions struct {
	// Passphrase seals the CA key with age; nil requires AllowUnencrypted.
	Passphrase []byte
	// AllowUnencrypted permits writing the CA key without a passphrase.
	AllowUnencrypted bool
	// workFactor overrides the scrypt work factor (tests only).
	workFactor int
}

func (k keyOptions) seal(keyPEM []byte) ([]byte, error) {
	if len(k.Passphrase) == 0 {
		if !k.AllowUnencrypted {
			return nil, errors.New("a CA key passphrase is required (or explicitly allow an unencrypted CA key)")
		}
		return bytes.Clone(keyPEM), nil
	}
	return sealKey(keyPEM, k.Passphrase, k.workFactor)
}

// InitOptions configures Init.
type InitOptions struct {
	Dir string
	// Components to issue (default: all).
	Components []string
	// RunnerNames are extra DNS names / IPs for the runner certificate;
	// DefaultRunnerDNSName is always included. They also become the CA's
	// name constraints.
	RunnerNames []string
	// PostgresNames are extra DNS names / IPs for the postgres certificate;
	// DefaultPostgresDNSName is always included. They also become part of
	// the CA's name constraints.
	PostgresNames []string
	CAValidity    time.Duration
	LeafValidity  time.Duration
	Passphrase    []byte
	// AllowUnencrypted permits an unencrypted CA key (not recommended).
	AllowUnencrypted bool
	// Force overwrites existing material.
	Force bool
	Now   func() time.Time

	workFactor int
}

// Result lists the files written.
type Result struct {
	Written []string
	Removed []string
}

// Init creates a new CA and the component certificates.
func Init(opts InitOptions) (*Result, error) {
	l := Layout{Dir: opts.Dir}
	comps, err := normalizeComponents(opts.Components)
	if err != nil {
		return nil, err
	}
	ko := keyOptions{Passphrase: opts.Passphrase, AllowUnencrypted: opts.AllowUnencrypted, workFactor: opts.workFactor}
	if len(ko.Passphrase) == 0 && !ko.AllowUnencrypted {
		return nil, errors.New("a CA key passphrase is required (or explicitly allow an unencrypted CA key)")
	}
	if len(ko.Passphrase) > 0 {
		if err := ValidatePassphrase(ko.Passphrase); err != nil {
			return nil, err
		}
	}
	targets := []string{l.CACert(), l.CAKey(), l.nextCACert(), l.nextCAKey(), l.prevCACert()}
	for _, c := range comps {
		targets = append(targets, l.CertFile(c), l.KeyFile(c), l.BundleFile(c))
	}
	if !opts.Force {
		var existing []string
		for _, t := range targets {
			if _, err := os.Lstat(t); err == nil {
				existing = append(existing, t)
			} else if !errors.Is(err, fs.ErrNotExist) {
				return nil, err
			}
		}
		if len(existing) > 0 {
			return nil, fmt.Errorf("%w: %s", ErrExists, strings.Join(existing, ", "))
		}
	}

	ca, err := NewCA(CAOptions{
		Validity:    opts.CAValidity,
		ServerNames: caServerNames(opts.RunnerNames, opts.PostgresNames),
		Now:         opts.Now,
	})
	if err != nil {
		return nil, err
	}
	defer clear(ca.Key)

	res := &Result{}
	if err := ensureDir(l.Dir); err != nil {
		return nil, err
	}
	if err := ensureDir(l.CADir()); err != nil {
		return nil, err
	}
	// A forced re-init starts a fresh PKI: stale rotation state must go.
	for _, p := range []string{l.nextCACert(), l.nextCAKey(), l.prevCACert()} {
		if removed, err := removeIfExists(p); err != nil {
			return nil, err
		} else if removed {
			res.Removed = append(res.Removed, p)
		}
	}
	if err := writeCAKey(l.CAKey(), ca.Key, ko, res); err != nil {
		return nil, err
	}
	if err := writeFile(l.CACert(), ca.CertPEM, certPerm, res); err != nil {
		return nil, err
	}
	for _, c := range comps {
		lo := LeafOptions{Validity: opts.LeafValidity, ServerNames: serverNamesFor(c, opts.RunnerNames, opts.PostgresNames), Now: opts.Now}
		if err := issueTo(l, ca, c, lo, ca.CertPEM, res); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// RotateOptions configures Rotate.
type RotateOptions struct {
	Dir string
	// Components to reissue; empty with All = every existing component.
	Components []string
	All        bool
	// RunnerNames overrides the runner's extra names; nil keeps the names
	// in the current runner certificate.
	RunnerNames []string
	// PostgresNames overrides the postgres certificate's extra names; nil
	// keeps the names in the current postgres certificate.
	PostgresNames []string
	LeafValidity  time.Duration
	// Passphrase decrypts the CA key when it is encrypted.
	Passphrase []byte
	Now        func() time.Time
}

// Rotate reissues leaf certificates (new keys) from the existing CA.
func Rotate(opts RotateOptions) (*Result, error) {
	l := Layout{Dir: opts.Dir}
	comps, err := selectComponents(l, opts.Components, opts.All)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(l.nextCACert()); err == nil {
		return nil, errors.New("a CA rotation is staged: run the promote phase (rotate --ca promote) instead")
	}
	ca, err := LoadCA(l.CACert(), l.CAKey(), opts.Passphrase)
	if err != nil {
		return nil, err
	}
	defer clear(ca.Key)
	bundle, err := currentBundle(l)
	if err != nil {
		return nil, err
	}
	res := &Result{}
	for _, c := range comps {
		lo := LeafOptions{Validity: opts.LeafValidity, Now: opts.Now}
		if names := serverNamesFor(c, opts.RunnerNames, opts.PostgresNames); names != nil {
			lo.ServerNames = names
		} else if IsServer(c) {
			lo.ServerNames = existingServerNames(l.CertFile(c), DefaultDNSName(c))
		}
		if err := issueTo(l, ca, c, lo, bundle, res); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// CAPhase is a step of the CA rotation.
type CAPhase string

// CA rotation phases. Restart every component after each phase.
const (
	// CAStage creates the next CA and adds it to every trust bundle.
	CAStage CAPhase = "stage"
	// CAPromote makes the next CA current and reissues all leaves from it;
	// bundles keep trusting the previous CA.
	CAPromote CAPhase = "promote"
	// CAFinish removes the previous CA from every trust bundle.
	CAFinish CAPhase = "finish"
)

// RotateCAOptions configures RotateCA.
type RotateCAOptions struct {
	Dir          string
	Phase        CAPhase
	CAValidity   time.Duration
	LeafValidity time.Duration
	// RunnerNames and PostgresNames override the extra runner / postgres
	// names in the next CA's name constraints (stage); when both are nil the
	// current CA's constraints are kept. "runner" and "postgres" are always
	// included, so staging a CA created before the postgres component
	// existed adds "postgres" (D-100).
	RunnerNames   []string
	PostgresNames []string
	// Passphrase decrypts the current/next CA key and seals the next one.
	Passphrase       []byte
	AllowUnencrypted bool
	Now              func() time.Time

	workFactor int
}

// RotateCA runs one phase of the CA rotation (see the package docs and
// docs/security/mtls-certs.md for the overlap procedure).
func RotateCA(opts RotateCAOptions) (*Result, error) {
	l := Layout{Dir: opts.Dir}
	comps := existingComponents(l)
	if len(comps) == 0 {
		return nil, fmt.Errorf("no component directories found in %s", l.Dir)
	}
	switch opts.Phase {
	case CAStage:
		return stageCA(l, comps, opts)
	case CAPromote:
		return promoteCA(l, comps, opts)
	case CAFinish:
		return finishCA(l, comps, opts.Now)
	default:
		return nil, fmt.Errorf("unknown CA rotation phase %q (stage, promote or finish)", opts.Phase)
	}
}

func stageCA(l Layout, comps []string, opts RotateCAOptions) (*Result, error) {
	for _, p := range []string{l.nextCACert(), l.nextCAKey(), l.prevCACert()} {
		if _, err := os.Lstat(p); err == nil {
			return nil, fmt.Errorf("a CA rotation is already in progress (%s exists)", p)
		}
	}
	cur, err := LoadCA(l.CACert(), l.CAKey(), opts.Passphrase)
	if err != nil {
		return nil, err
	}
	clear(cur.Key) // loaded only to prove possession and check the passphrase
	ko := keyOptions{Passphrase: opts.Passphrase, AllowUnencrypted: opts.AllowUnencrypted, workFactor: opts.workFactor}
	var names []string
	if opts.RunnerNames == nil && opts.PostgresNames == nil {
		names = append(cur.ServerNames(), DefaultRunnerDNSName, DefaultPostgresDNSName)
	} else {
		runner, pg := opts.RunnerNames, opts.PostgresNames
		if runner == nil {
			runner = existingServerNames(l.CertFile(ComponentRunner), DefaultRunnerDNSName)
		}
		if pg == nil {
			pg = existingServerNames(l.CertFile(ComponentPostgres), DefaultPostgresDNSName)
		}
		names = caServerNames(runner, pg)
	}
	next, err := NewCA(CAOptions{Validity: opts.CAValidity, ServerNames: names, Now: opts.Now})
	if err != nil {
		return nil, err
	}
	defer clear(next.Key)
	res := &Result{}
	if err := writeCAKey(l.nextCAKey(), next.Key, ko, res); err != nil {
		return nil, err
	}
	if err := writeFile(l.nextCACert(), next.CertPEM, certPerm, res); err != nil {
		return nil, err
	}
	bundle := slices.Concat(cur.CertPEM, next.CertPEM)
	for _, c := range comps {
		if err := writeFile(l.BundleFile(c), bundle, certPerm, res); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func promoteCA(l Layout, comps []string, opts RotateCAOptions) (*Result, error) {
	next, err := LoadCA(l.nextCACert(), l.nextCAKey(), opts.Passphrase)
	if err != nil {
		return nil, fmt.Errorf("loading the staged CA (run the stage phase first): %w", err)
	}
	defer clear(next.Key)
	curPEM, err := config.ReadPublicFile(l.CACert())
	if err != nil {
		return nil, err
	}
	res := &Result{}
	// Order: keep a copy of the old CA, then move the key before the
	// certificate. LoadCA rejects a key/certificate mismatch if this is
	// interrupted.
	if err := writeFile(l.prevCACert(), curPEM, certPerm, res); err != nil {
		return nil, err
	}
	if err := renameSync(l.nextCAKey(), l.CAKey()); err != nil {
		return nil, err
	}
	if err := renameSync(l.nextCACert(), l.CACert()); err != nil {
		return nil, err
	}
	res.Written = append(res.Written, l.CAKey(), l.CACert())
	bundle := slices.Concat(next.CertPEM, curPEM)
	for _, c := range comps {
		lo := LeafOptions{Validity: opts.LeafValidity, Now: opts.Now}
		if IsServer(c) {
			lo.ServerNames = existingServerNames(l.CertFile(c), DefaultDNSName(c))
		}
		if err := issueTo(l, next, c, lo, bundle, res); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func finishCA(l Layout, comps []string, now func() time.Time) (*Result, error) {
	if _, err := os.Lstat(l.prevCACert()); err != nil {
		return nil, fmt.Errorf("no CA rotation to finish (%s missing; run promote first): %w", l.prevCACert(), err)
	}
	if _, err := os.Lstat(l.nextCACert()); err == nil {
		return nil, errors.New("a CA rotation is staged but not promoted: run promote first")
	}
	caPEM, err := config.ReadPublicFile(l.CACert())
	if err != nil {
		return nil, err
	}
	cas, err := ParseCertificates(caPEM)
	if err != nil || len(cas) != 1 {
		return nil, fmt.Errorf("%s: expected exactly one CA certificate", l.CACert())
	}
	// Refuse while any leaf is still issued by the old CA.
	for _, c := range comps {
		data, err := config.ReadPublicFile(l.CertFile(c))
		if err != nil {
			return nil, err
		}
		leaves, err := ParseCertificates(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", l.CertFile(c), err)
		}
		if err := verifyLeafAt(leaves[0], leaves[1:], cas[0], clock(now)); err != nil {
			return nil, fmt.Errorf("%s is not issued by the current CA (%w); run promote or rotate it first", l.CertFile(c), err)
		}
	}
	res := &Result{}
	for _, c := range comps {
		if err := writeFile(l.BundleFile(c), caPEM, certPerm, res); err != nil {
			return nil, err
		}
	}
	if err := os.Remove(l.prevCACert()); err != nil {
		return nil, err
	}
	res.Removed = append(res.Removed, l.prevCACert())
	return res, nil
}

func verifyLeafAt(leaf *x509.Certificate, intermediates []*x509.Certificate, ca *x509.Certificate, now time.Time) error {
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	inter := x509.NewCertPool()
	for _, c := range intermediates {
		inter.AddCert(c)
	}
	_, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}})
	return err
}

// LoadCA loads a CA certificate and its (possibly encrypted) key and checks
// that they belong together.
func LoadCA(certFile, keyFile string, passphrase []byte) (*CA, error) {
	certPEM, err := config.ReadPublicFile(certFile)
	if err != nil {
		return nil, fmt.Errorf("reading CA certificate: %w", err)
	}
	certs, err := ParseCertificates(certPEM)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", certFile, err)
	}
	if len(certs) != 1 || !certs[0].IsCA {
		return nil, fmt.Errorf("%s: expected exactly one CA certificate", certFile)
	}
	raw, err := config.ReadSecretFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("reading CA key: %w", err)
	}
	defer config.Zero(raw)
	keyPEM, err := openKey(raw, passphrase)
	if err != nil {
		return nil, err
	}
	defer clear(keyPEM)
	key, err := parseKey(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", keyFile, err)
	}
	pub, ok := certs[0].PublicKey.(ed25519.PublicKey)
	if !ok || !pub.Equal(key.Public()) {
		clear(key)
		return nil, fmt.Errorf("%s does not match %s", keyFile, certFile)
	}
	return &CA{Cert: certs[0], CertPEM: certPEM, Key: key}, nil
}

// CAKeyEncrypted reports whether the CA key in dir is encrypted.
func CAKeyEncrypted(dir string) (bool, error) {
	l := Layout{Dir: dir}
	path := l.CAKey()
	if _, err := os.Lstat(l.nextCAKey()); err == nil {
		path = l.nextCAKey()
	}
	raw, err := config.ReadSecretFile(path)
	if err != nil {
		return false, err
	}
	defer config.Zero(raw)
	return IsEncryptedKey(raw), nil
}

func currentBundle(l Layout) ([]byte, error) {
	cur, err := config.ReadPublicFile(l.CACert())
	if err != nil {
		return nil, err
	}
	prev, err := config.ReadPublicFile(l.prevCACert())
	if errors.Is(err, fs.ErrNotExist) {
		return cur, nil
	}
	if err != nil {
		return nil, err
	}
	return slices.Concat(cur, prev), nil
}

func issueTo(l Layout, ca *CA, c string, lo LeafOptions, bundle []byte, res *Result) error {
	leaf, err := ca.Issue(c, lo)
	if err != nil {
		return err
	}
	defer clear(leaf.KeyPEM)
	if err := ensureDir(l.ComponentDir(c)); err != nil {
		return err
	}
	// Key first: a crash leaves a new key with an old cert, which fails
	// loudly at startup instead of silently pairing mismatched material.
	if err := writeFile(l.KeyFile(c), leaf.KeyPEM, keyPerm, res); err != nil {
		return err
	}
	if err := writeFile(l.CertFile(c), leaf.CertPEM, certPerm, res); err != nil {
		return err
	}
	return writeFile(l.BundleFile(c), bundle, certPerm, res)
}

func writeCAKey(path string, key ed25519.PrivateKey, ko keyOptions, res *Result) error {
	keyPEM, err := encodeKey(key)
	if err != nil {
		return err
	}
	defer clear(keyPEM)
	data, err := ko.seal(keyPEM)
	if err != nil {
		return err
	}
	defer clear(data)
	return writeFile(path, data, keyPerm, res)
}

// caServerNames is the name-constraint list of a CA: both server
// components' default names plus their extra names.
func caServerNames(runner, postgres []string) []string {
	return slices.Concat([]string{DefaultRunnerDNSName}, runner, []string{DefaultPostgresDNSName}, postgres)
}

// serverNamesFor returns the extra names override for component c, or nil
// (keep / none).
func serverNamesFor(c string, runner, postgres []string) []string {
	switch c {
	case ComponentRunner:
		return runner
	case ComponentPostgres:
		return postgres
	default:
		return nil
	}
}

// existingServerNames returns the extra names (beyond defaultName) of an
// existing server certificate, or nil.
func existingServerNames(certFile, defaultName string) []string {
	data, err := config.ReadPublicFile(certFile)
	if err != nil {
		return nil
	}
	certs, err := ParseCertificates(data)
	if err != nil {
		return nil
	}
	var names []string
	for _, n := range certs[0].DNSNames {
		if n != defaultName {
			names = append(names, n)
		}
	}
	for _, ip := range certs[0].IPAddresses {
		names = append(names, ip.String())
	}
	return names
}

func normalizeComponents(in []string) ([]string, error) {
	if len(in) == 0 {
		return Components(), nil
	}
	var out []string
	for _, c := range in {
		if !ValidComponent(c) {
			return nil, fmt.Errorf("unknown component %q (known: %s)", c, strings.Join(Components(), ", "))
		}
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out, nil
}

func existingComponents(l Layout) []string {
	var out []string
	for _, c := range Components() {
		if fi, err := os.Stat(l.ComponentDir(c)); err == nil && fi.IsDir() {
			out = append(out, c)
		}
	}
	return out
}

func selectComponents(l Layout, in []string, all bool) ([]string, error) {
	switch {
	case all && len(in) > 0:
		return nil, errors.New("use either a component list or all, not both")
	case all:
		comps := existingComponents(l)
		if len(comps) == 0 {
			return nil, fmt.Errorf("no component directories found in %s", l.Dir)
		}
		return comps, nil
	case len(in) == 0:
		return nil, errors.New("no component selected")
	default:
		return normalizeComponents(in)
	}
}
