package setup

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/logging"
)

// passwordBytes is the entropy of every generated database password.
const passwordBytes = 32

// certComponents are the certificates of a compose deployment.
var certComponents = []string{"runner", "serve", "postgres"}

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
func certTargets(w *writer, l Layout) ([]string, error) {
	dir, err := w.abs(l.keys(certsDirName))
	if err != nil {
		return nil, err
	}
	abs, err := certs.InitTargets(dir, certComponents)
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
func existingCertTargets(w *writer, l Layout) ([]string, error) {
	targets, err := certTargets(w, l)
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
func refuseExistingCerts(w *writer, l Layout) error {
	existing, err := existingCertTargets(w, l)
	if err != nil || len(existing) == 0 {
		return err
	}
	caDir := l.keys(certsDirName, caDirName)
	for _, p := range existing {
		if strings.HasPrefix(p, caDir+"/") {
			return fmt.Errorf("existing CA found in %s (%s): setup never reuses or overwrites a CA. Reuse it by issuing the runner, serve and postgres certificates with `bekup certs rotate` first and re-run with --keep-existing (docs/security/mtls-certs.md), or remove it (move it away; keep it until the new deployment works) and re-run",
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
func generateCerts(w *writer, l Layout, pass []byte, initFn certsInitFunc) error {
	if len(pass) == 0 {
		return errors.New("a CA key passphrase is required")
	}
	if err := certs.ValidatePassphrase(pass); err != nil {
		return err
	}
	if err := refuseExistingCerts(w, l); err != nil {
		return err
	}
	targets, err := certTargets(w, l)
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
	for _, c := range certComponents {
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
	_, err = initFn(certs.InitOptions{Dir: dir, Components: certComponents, Passphrase: pass})
	if err != nil {
		return fmt.Errorf("creating the certificates: %w", err)
	}
	return nil
}
