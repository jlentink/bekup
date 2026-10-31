package setup

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/logging"
)

// passwordBytes is the entropy of every generated database password.
const passwordBytes = 32

// certComponentsFor returns the certificates of a compose deployment: the
// postgres server certificate only for the bundled database.
func certComponentsFor(db DBMode) []string {
	if db == DBExternal {
		return []string{"runner", "serve"}
	}
	return []string{"runner", "serve", "postgres"}
}

// randomPassword returns passwordBytes from crypto/rand, base64url encoded
// without padding (URL-safe in a connection URL's userinfo). Zero it after
// use.
func randomPassword() ([]byte, error) {
	raw := make([]byte, passwordBytes)
	defer crypto.Zero(raw)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("reading randomness: %w", err)
	}
	out := make([]byte, base64.RawURLEncoding.EncodedLen(len(raw)))
	base64.RawURLEncoding.Encode(out, raw)
	return out, nil
}

// databaseURL builds postgres://<role>:<password>@postgres:5432/bekup in
// one pre-sized buffer (no hidden copies). Zero it after use. TLS comes from
// bekup.toml [database] tls_mode, so the URL carries no sslmode.
func databaseURL(role string, password []byte) []byte {
	const scheme, host = "postgres://", "@postgres:5432/bekup"
	u := make([]byte, 0, len(scheme)+len(role)+1+len(password)+len(host))
	u = append(u, scheme...)
	u = append(u, role...)
	u = append(u, ':')
	u = append(u, password...)
	u = append(u, host...)
	return u
}

// generateMasterKey writes a new master keyring and returns its key ID.
func generateMasterKey(w *writer, l Layout, now time.Time) (string, error) {
	k, err := crypto.GenerateKeyring(now)
	if err != nil {
		return "", err
	}
	defer k.Destroy()
	data, err := k.Marshal()
	if err != nil {
		return "", err
	}
	defer crypto.Zero(data)
	if err := w.create(l.keys(masterKeyName), data, 0o400); err != nil {
		return "", err
	}
	return k.PrimaryID().String(), nil
}

// generateDatabase writes the three role passwords and the two connection
// URLs. Every value is registered with the log redaction registry while it
// exists and zeroed afterwards.
func generateDatabase(w *writer, l Layout) (retErr error) {
	var wipe []func()
	defer func() {
		for _, f := range wipe {
			f()
		}
	}()
	keep := func(b []byte) []byte {
		un := logging.DefaultRegistry.Register(b)
		wipe = append(wipe, func() { crypto.Zero(b); un() })
		return b
	}
	pass := map[string][]byte{}
	for _, name := range []string{postgresPassName, ownerPassName, appPassName} {
		p, err := randomPassword()
		if err != nil {
			return err
		}
		pass[name] = keep(p)
	}
	ownerURL := keep(databaseURL("bekup_owner", pass[ownerPassName]))
	appURL := keep(databaseURL("bekup_app", pass[appPassName]))
	for _, f := range []struct {
		name string
		data []byte
	}{
		{postgresPassName, pass[postgresPassName]},
		{ownerPassName, pass[ownerPassName]},
		{appPassName, pass[appPassName]},
		{ownerURLName, ownerURL},
		{appURLName, appURL},
	} {
		if err := w.create(l.keys(f.name), f.data, 0o400); err != nil {
			return err
		}
	}
	return nil
}

// certsInitFunc is certs.Init (replaceable in tests).
type certsInitFunc func(certs.InitOptions) (*certs.Result, error)

// certTargets returns every file certs.Init writes for a compose
// deployment (relative to the deployment directory), including the CA's
// rotation state.
func certTargets(w *writer, l Layout, comps []string) ([]string, error) {
	dir, err := w.abs(l.keys(certsDirName))
	if err != nil {
		return nil, err
	}
	abs, err := certs.InitTargets(dir, comps)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(abs))
	for _, a := range abs {
		rel, err := filepath.Rel(w.root, a)
		if err != nil {
			return nil, err
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out, nil
}

// existingCertTargets returns the certs.Init targets that exist (symlinks
// are refused).
func existingCertTargets(w *writer, l Layout, comps []string) ([]string, error) {
	targets, err := certTargets(w, l, comps)
	if err != nil {
		return nil, err
	}
	var existing []string
	for _, t := range targets {
		fi, err := w.lstat(t)
		if err != nil {
			return nil, err
		}
		if fi != nil {
			existing = append(existing, t)
		}
	}
	return existing, nil
}

// refuseExistingCerts fails before anything is written when certs.Init
// would meet existing files, above all an existing (offline) CA: setup
// never reuses, overwrites or rolls back a CA it did not create.
func refuseExistingCerts(w *writer, l Layout, comps []string) error {
	existing, err := existingCertTargets(w, l, comps)
	if err != nil || len(existing) == 0 {
		return err
	}
	caDir := l.keys(certsDirName, caDirName)
	for _, p := range existing {
		if strings.HasPrefix(p, caDir+"/") {
			return fmt.Errorf("existing CA found in %s (%s): setup never overwrites a CA. Issue the certificates from it with --use-existing-ca (its passphrase is asked), or remove it (move it away; keep it until the new deployment works) and re-run (docs/security/mtls-certs.md)",
				caDir, strings.Join(existing, ", "))
		}
	}
	return fmt.Errorf("certificate files exist already (%s): restore the rest of them from your escrow, or regenerate all secrets with --regenerate-secrets", strings.Join(existing, ", "))
}

// generateCerts creates the private CA (key sealed with the passphrase)
// and the runner, serve and postgres certificates below <keys>/certs. It
// refuses to start while any file certs.Init writes exists, so every path
// it tracks for the rollback was absent before (an existing offline CA is
// never deleted by a rollback).
func generateCerts(w *writer, l Layout, comps []string, pass []byte, initFn certsInitFunc) error {
	if len(pass) == 0 {
		return errors.New("a CA key passphrase is required")
	}
	if err := certs.ValidatePassphrase(pass); err != nil {
		return err
	}
	if err := refuseExistingCerts(w, l, comps); err != nil {
		return err
	}
	targets, err := certTargets(w, l, comps)
	if err != nil {
		return err
	}
	// Directories certs.Init may create (it never removes one).
	for _, d := range []string{l.keys(certsDirName), l.keys(certsDirName, caDirName)} {
		fi, err := w.lstat(d)
		if err != nil {
			return err
		}
		if fi == nil {
			w.track(d)
		}
	}
	for _, c := range comps {
		d := l.keys(certsDirName, c)
		fi, err := w.lstat(d)
		if err != nil {
			return err
		}
		if fi == nil {
			w.track(d)
		}
	}
	w.track(targets...) // all absent (refuseExistingCerts)
	dir, err := w.abs(l.keys(certsDirName))
	if err != nil {
		return err
	}
	_, err = initFn(certs.InitOptions{Dir: dir, Components: comps, Passphrase: pass})
	if err != nil {
		return fmt.Errorf("creating the certificates: %w", err)
	}
	return nil
}

// certsRotateFunc is certs.Rotate (replaceable in tests).
type certsRotateFunc func(certs.RotateOptions) (*certs.Result, error)

// checkExistingCA requires the CA mounted at <keys>/certs/ca (certificate
// and key, no staged rotation) and no leaf files yet: --use-existing-ca
// issues only the leaves and never touches the CA.
func checkExistingCA(w *writer, l Layout, comps []string) error {
	caDir := l.keys(certsDirName, caDirName)
	for _, f := range []string{"ca.crt", caKeyName} {
		fi, err := w.lstat(path.Join(caDir, f))
		if err != nil {
			return err
		}
		if fi == nil || !fi.Mode().IsRegular() {
			return fmt.Errorf("--use-existing-ca: %s/%s is missing: mount the CA media at %s (e.g. -v /media/usb/bekup-ca:/deploy/%s)", caDir, f, caDir, caDir)
		}
	}
	for _, f := range []string{"ca.next.crt", "ca.next.key", "ca.prev.crt"} {
		if fi, err := w.lstat(path.Join(caDir, f)); err != nil || fi != nil {
			if err != nil {
				return err
			}
			return fmt.Errorf("--use-existing-ca: a CA rotation is in progress (%s/%s exists): finish it with `bekup setup renew` first", caDir, f)
		}
	}
	var existing []string
	for _, c := range comps {
		for _, f := range []string{c + ".crt", c + ".key", "ca.crt"} {
			p := l.keys(certsDirName, c, f)
			fi, err := w.lstat(p)
			if err != nil {
				return err
			}
			if fi != nil {
				existing = append(existing, p)
			}
		}
	}
	if len(existing) > 0 {
		return fmt.Errorf("--use-existing-ca: certificate files exist already (%s): renew them with `bekup setup renew` instead", strings.Join(existing, ", "))
	}
	return nil
}

// issueFromExistingCA issues the leaf certificates of comps from the CA
// mounted at <keys>/certs/ca (its key decrypted with pass), tracking every
// new file and directory for the rollback; the CA itself is only read.
func issueFromExistingCA(w *writer, l Layout, comps []string, pass []byte, rotateFn certsRotateFunc) error {
	if err := checkExistingCA(w, l, comps); err != nil {
		return err
	}
	for _, c := range comps {
		d := l.keys(certsDirName, c)
		fi, err := w.lstat(d)
		if err != nil {
			return err
		}
		if fi == nil {
			w.track(d)
		}
		w.track(l.keys(certsDirName, c, c+".key"), l.keys(certsDirName, c, c+".crt"), l.keys(certsDirName, c, "ca.crt"))
	}
	dir, err := w.abs(l.keys(certsDirName))
	if err != nil {
		return err
	}
	if _, err := rotateFn(certs.RotateOptions{Dir: dir, Components: comps, Passphrase: pass}); err != nil {
		return fmt.Errorf("issuing the certificates from the existing CA: %w", err)
	}
	return nil
}

// maxKeyringFile bounds an imported master keyring.
const maxKeyringFile = 64 << 10

// readImportKey reads and parses a master keyring to import (a file below
// root, no symlinks) and returns it re-marshaled; with wantKCV set the
// key-check value must match. The caller zeroes the result.
func readImportKey(root, rel, wantKCV string) ([]byte, error) {
	if err := checkSourcePath("import-master-key", rel); err != nil {
		return nil, err
	}
	raw, err := readInput(root, rel, maxKeyringFile)
	if err != nil {
		return nil, fmt.Errorf("--import-master-key %s: %w", rel, err)
	}
	defer crypto.Zero(raw)
	k, err := crypto.ParseKeyring(raw)
	if err != nil {
		return nil, fmt.Errorf("--import-master-key %s: not a bekup master keyring: %w", rel, err)
	}
	defer k.Destroy()
	if wantKCV != "" {
		if err := k.VerifyKeyCheckValue(wantKCV); err != nil {
			return nil, fmt.Errorf("--import-master-key %s: the key-check value does not match (%w): this is not the keyring of that installation", rel, err)
		}
	}
	return k.Marshal()
}

// writeImportedKey writes an imported keyring and returns its primary key
// ID.
func writeImportedKey(w *writer, l Layout, data []byte) (string, error) {
	k, err := crypto.ParseKeyring(data)
	if err != nil {
		return "", err
	}
	defer k.Destroy()
	if err := w.create(l.keys(masterKeyName), data, 0o400); err != nil {
		return "", err
	}
	return k.PrimaryID().String(), nil
}

// InspectKeyring reads a master keyring to import (a file relative to the
// deployment directory dir) and returns its primary key ID; with kcv set
// the key-check value must match. Nothing is written.
func InspectKeyring(dir, rel, kcv string) (string, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	data, err := readImportKey(root, rel, kcv)
	if err != nil {
		return "", err
	}
	defer crypto.Zero(data)
	k, err := crypto.ParseKeyring(data)
	if err != nil {
		return "", err
	}
	defer k.Destroy()
	return k.PrimaryID().String(), nil
}
