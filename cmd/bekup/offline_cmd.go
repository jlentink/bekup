package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/jlentink/bekup/internal/config"
	"github.com/jlentink/bekup/internal/crypto"
	"github.com/jlentink/bekup/internal/offline"
	"github.com/jlentink/bekup/internal/pipeline"
	"github.com/jlentink/bekup/internal/storage"
)

// Offline recovery commands (task 4.7, D-164): they work without the bekup
// database and without the master keyring. Details: docs/recovery.md.

// errVerifyFailed is returned after a report was printed for a failed
// verification (non-zero exit).
var errVerifyFailed = errors.New("verification FAILED (see the report above)")

// addSecretFlagTraps registers hidden flags that would take a secret as
// a flag value and makes them fail with guidance: secrets never come from
// argv (visible in the process list and shell history).
func addSecretFlagTraps(cmd *cobra.Command, names ...string) {
	for _, n := range names {
		cmd.Flags().String(n, "", "never use: secrets are not accepted as flag values")
		if err := cmd.Flags().MarkHidden(n); err != nil {
			panic(err)
		}
	}
}

func checkSecretFlagTraps(cmd *cobra.Command, names ...string) error {
	for _, n := range names {
		if cmd.Flags().Changed(n) {
			return fmt.Errorf("--%s: secrets are never accepted as flag values (they end up in the process list and shell history); use --passphrase-file, --identity-file or --stdin-passphrase", n)
		}
	}
	return nil
}

var secretFlagTraps = []string{"passphrase", "password", "identity", "key"}

func addPubkeyFlag(cmd *cobra.Command, p *[]string) {
	cmd.Flags().StringArrayVar(p, "pubkey", nil,
		"instance public key: a base64 ed25519 key or a file from `bekup keys export-public` (repeatable; retired keys verify the manifests they signed)")
}

// ---------------------------------------------------------------------------
// verify

type verifyFlags struct {
	pubkeys   []string
	artifacts []string
	json      bool
}

func newVerifyCmd() *cobra.Command {
	f := &verifyFlags{}
	cmd := &cobra.Command{
		Use:   "verify <manifest.json | run directory>",
		Short: "Verify a run's signed manifest and its artifacts offline (no database needed)",
		Long: "Verifies the ed25519 signature of a run's manifest.json with the instance public key(s) given by\n" +
			"--pubkey, then streams every artifact the manifest lists and compares its size and SHA-256.\n" +
			"Works on a disk destination's run directory or on files downloaded from S3/SFTP. Prints the\n" +
			"engine, databases, sizes, encryption and the completeness statistics; exits non-zero on any\n" +
			"failure. Check that the job and run shown are the ones you expect. See docs/recovery.md.",
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error { return runVerify(c, f, args[0]) },
	}
	addPubkeyFlag(cmd, &f.pubkeys)
	cmd.Flags().StringArrayVar(&f.artifacts, "artifact", nil,
		"check this artifact file (matched to the manifest by file name; repeatable); only the given artifacts are checked")
	cmd.Flags().BoolVar(&f.json, "json", false, "print the report as JSON")
	return cmd
}

// locateManifest returns the manifest path and the run directory for a
// verify/decrypt argument (a manifest.json or a directory holding one).
func locateManifest(arg string) (manifest, dir string, err error) {
	fi, err := os.Stat(arg)
	if err != nil {
		return "", "", err
	}
	if fi.IsDir() {
		return filepath.Join(arg, storage.ManifestFileName), arg, nil
	}
	return arg, filepath.Dir(arg), nil
}

func loadKeys(values []string) (*offline.KeySet, error) {
	if len(values) == 0 {
		return nil, errors.New("--pubkey is required: the manifest signature cannot be verified without the instance public key " +
			"(export it with `bekup keys export-public` or take it from your escrow, see docs/recovery.md)")
	}
	return offline.LoadPublicKeys(values)
}

type verifyJSON struct {
	*offline.Report
	Stats *offline.StatsSummary `json:"stats,omitempty"`
}

func runVerify(c *cobra.Command, f *verifyFlags, arg string) error {
	keys, err := loadKeys(f.pubkeys)
	if err != nil {
		return err
	}
	manifestPath, dir, err := locateManifest(arg)
	if err != nil {
		return err
	}
	data, err := offline.ReadManifestFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	m, err := offline.VerifyManifest(data, keys.Verifier())
	if err != nil {
		return fmt.Errorf("manifest %s: %w", manifestPath, err)
	}
	ds, err := offline.OpenDir(dir)
	if err != nil {
		return err
	}
	defer offline.CloseQuietly(ds)
	var src offline.Source = ds
	opts := offline.VerifyOptions{}
	if len(f.artifacts) > 0 {
		fs := &offline.FileSource{Files: map[string]string{}, Next: ds}
		opts.Only = map[string]bool{}
		for _, p := range f.artifacts {
			name := filepath.Base(p)
			if !slices.ContainsFunc(m.Entries, func(e pipeline.Entry) bool { return e.Path == name }) {
				return fmt.Errorf("--artifact %s: the manifest lists no artifact named %q", p, offline.SafeText(name))
			}
			fs.Files[name] = p
			opts.Only[name] = true
		}
		src = fs
	}
	rep := offline.VerifyRun(c.Context(), data, m, src, keys, opts)
	w := c.OutOrStdout()
	if f.json {
		stats, err := offline.SummarizeStats(m)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(verifyJSON{Report: rep, Stats: stats}); err != nil {
			return err
		}
	} else {
		offline.WriteReport(w, rep)
	}
	if !rep.OK {
		return errVerifyFailed
	}
	return nil
}

// ---------------------------------------------------------------------------
// decrypt

type decryptFlags struct {
	out               string
	passphraseFile    string
	identityFile      string
	stdinPassphrase   bool
	passphraseVersion int
	manifest          string
	pubkeys           []string
	noVerify          bool
	force             bool
	maxWorkFactor     int
}

func newDecryptCmd() *cobra.Command {
	f := &decryptFlags{}
	cmd := &cobra.Command{
		Use:   "decrypt <artifact> --out <file|-> (--passphrase-file <f> | --identity-file <f> | --stdin-passphrase) (--manifest <f> --pubkey <k> | --no-verify)",
		Short: "Decrypt and decompress a backup artifact offline into a ready-to-use .dump/.sql",
		Long: "Decrypts an artifact (age) and decompresses it (zstd) into the raw dump that stock pg_restore,\n" +
			"psql, mysql or mariadb restore. With --manifest and --pubkey the manifest signature and the\n" +
			"artifact's size and SHA-256 are verified first and the decrypted dump must match the signed raw\n" +
			"SHA-256; the output only appears under --out after every check passed. Without a manifest you\n" +
			"must pass --no-verify explicitly (the content is then unauthenticated).\n\n" +
			"Keys: --passphrase-file takes a file with the passphrase or a passphrase export from bekup (the\n" +
			"version is picked from the manifest or --passphrase-version); --identity-file takes an age\n" +
			"identity file (X25519 only); --stdin-passphrase prompts on a terminal or reads one line from\n" +
			"stdin. Passphrases are never accepted as flag values. See docs/recovery.md.",
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error { return runDecrypt(c, f, args[0]) },
	}
	fl := cmd.Flags()
	fl.StringVar(&f.out, "out", "", "output file (mode 0600, never overwritten without --force) or - for stdout (required)")
	fl.StringVar(&f.passphraseFile, "passphrase-file", "", "file holding the job passphrase or a bekup passphrase export (owner-only permissions)")
	fl.StringVar(&f.identityFile, "identity-file", "", "age identity file (AGE-SECRET-KEY-1..., X25519 only; owner-only permissions)")
	fl.BoolVar(&f.stdinPassphrase, "stdin-passphrase", false, "read the passphrase from the terminal (no echo) or the first line of stdin")
	fl.IntVar(&f.passphraseVersion, "passphrase-version", 0, "passphrase version to use from a passphrase export (default: the manifest's)")
	fl.StringVar(&f.manifest, "manifest", "", "the run's manifest.json: verify the artifact before decrypting")
	addPubkeyFlag(cmd, &f.pubkeys)
	fl.BoolVar(&f.noVerify, "no-verify", false, "decrypt without verifying a signed manifest (DANGEROUS: unauthenticated content)")
	fl.BoolVar(&f.force, "force", false, "replace an existing --out file")
	fl.IntVar(&f.maxWorkFactor, "max-work-factor", crypto.DefaultScryptWorkFactor,
		fmt.Sprintf("highest scrypt work factor accepted from the artifact header (%d..%d; bekup writes %d)",
			crypto.DefaultScryptWorkFactor, crypto.MaxScryptWorkFactor, crypto.DefaultScryptWorkFactor))
	addSecretFlagTraps(cmd, secretFlagTraps...)
	return cmd
}

// validate checks the flag combination before anything is read.
func (f *decryptFlags) validate() error {
	if strings.TrimSpace(f.out) == "" {
		return errors.New("--out is required (a file, or - for stdout)")
	}
	n := 0
	for _, set := range []bool{f.passphraseFile != "", f.identityFile != "", f.stdinPassphrase} {
		if set {
			n++
		}
	}
	if n > 1 {
		return errors.New("use only one of --passphrase-file, --identity-file and --stdin-passphrase")
	}
	if f.passphraseVersion < 0 {
		return errors.New("--passphrase-version must be positive")
	}
	if f.maxWorkFactor < crypto.DefaultScryptWorkFactor || f.maxWorkFactor > crypto.MaxScryptWorkFactor {
		return fmt.Errorf("--max-work-factor must be %d..%d", crypto.DefaultScryptWorkFactor, crypto.MaxScryptWorkFactor)
	}
	switch {
	case f.manifest == "" && !f.noVerify:
		return errors.New("refusing to decrypt unverified content: pass --manifest and --pubkey to verify the artifact first, " +
			"or --no-verify to skip verification explicitly")
	case f.manifest != "" && !f.noVerify && len(f.pubkeys) == 0:
		return errors.New("--manifest needs --pubkey to verify the signature (or --no-verify)")
	case f.noVerify && len(f.pubkeys) > 0:
		return errors.New("--pubkey and --no-verify contradict each other")
	}
	return nil
}

const noVerifyWarning = `
WARNING: --no-verify: the artifact is NOT checked against a signed manifest.
WARNING: Its content is unauthenticated: anyone who could write to the backup
WARNING: storage (or holds the age recipient) could have replaced it. Inspect the
WARNING: dump before restoring it, and never restore it into a production database
WARNING: without review. Prefer: --manifest manifest.json --pubkey <key>.

`

func runDecrypt(c *cobra.Command, f *decryptFlags, artifact string) error {
	if err := checkSecretFlagTraps(c, secretFlagTraps...); err != nil {
		return err
	}
	if err := f.validate(); err != nil {
		return err
	}
	stderr := c.ErrOrStderr()
	if f.noVerify {
		if _, err := io.WriteString(stderr, noVerifyWarning); err != nil {
			return err
		}
	}
	name := filepath.Base(artifact)
	var m *pipeline.Manifest
	var entry *pipeline.Entry
	if f.manifest != "" {
		var err error
		m, err = loadDecryptManifest(f)
		if err != nil {
			return err
		}
		for i := range m.Entries {
			if m.Entries[i].Path == name {
				entry = &m.Entries[i]
			}
		}
		if entry == nil {
			return fmt.Errorf("the manifest lists no artifact named %q (keep the original file name)", offline.SafeText(name))
		}
	}
	dec, cleanup, err := f.decryption(c, name, m, entry)
	if err != nil {
		return err
	}
	defer cleanup()

	in, err := os.Open(artifact) //nolint:gosec // G304: operator-supplied artifact path
	if err != nil {
		return err
	}
	defer offline.CloseQuietly(in)
	if fi, err := in.Stat(); err != nil || !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", artifact)
	}
	if entry != nil && !f.noVerify {
		// First pass: never decrypt (or write to stdout) bytes that do not
		// match the signed manifest.
		if _, err := offline.CheckArtifact(c.Context(), in, entry); err != nil {
			return fmt.Errorf("refusing to decrypt %s: %w", artifact, err)
		}
		if _, err := in.Seek(0, io.SeekStart); err != nil {
			return err
		}
	}

	var w io.Writer
	var outFile *offline.OutputFile
	if f.out == "-" {
		if file, ok := c.OutOrStdout().(*os.File); ok && term.IsTerminal(int(file.Fd())) {
			return errors.New("refusing to write a dump to a terminal: redirect stdout or use --out <file>")
		}
		w = c.OutOrStdout()
	} else {
		if sameFile(artifact, f.out) {
			return errors.New("--out must not be the artifact itself")
		}
		outFile, err = offline.CreateOutput(f.out, f.force)
		if err != nil {
			return err
		}
		defer outFile.Abort()
		w = outFile
	}
	res, err := offline.Decrypt(c.Context(), w, in, dec, entry)
	if err != nil {
		if f.out == "-" {
			return fmt.Errorf("decryption failed, DISCARD the output already written to stdout: %w", err)
		}
		return fmt.Errorf("decryption failed, nothing was written to %s: %w", f.out, err)
	}
	if outFile != nil {
		if err := outFile.Commit(); err != nil {
			return err
		}
	}
	return printDecryptSummary(stderr, f, artifact, res, m, entry)
}

func loadDecryptManifest(f *decryptFlags) (*pipeline.Manifest, error) {
	data, err := offline.ReadManifestFile(f.manifest)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	if f.noVerify {
		sm, err := pipeline.ParseManifestFile(data)
		if err != nil {
			return nil, fmt.Errorf("manifest %s: %w", f.manifest, err)
		}
		if err := sm.Manifest.Validate(); err != nil {
			return nil, fmt.Errorf("manifest %s: %w", f.manifest, err)
		}
		return &sm.Manifest, nil
	}
	keys, err := loadKeys(f.pubkeys)
	if err != nil {
		return nil, err
	}
	m, err := offline.VerifyManifest(data, keys.Verifier())
	if err != nil {
		return nil, fmt.Errorf("manifest %s: %w", f.manifest, err)
	}
	return m, nil
}

// decryption builds the key material for the artifact. The cleanup
// function zeroes it.
func (f *decryptFlags) decryption(c *cobra.Command, name string, m *pipeline.Manifest, e *pipeline.Entry) (pipeline.Decryption, func(), error) {
	nop := func() {}
	mode := pipeline.EncryptionMode("")
	if e != nil {
		mode = e.Encryption.Mode
	} else if !strings.HasSuffix(name, ".age") {
		mode = pipeline.EncryptNone
	}
	hasKey := f.passphraseFile != "" || f.identityFile != "" || f.stdinPassphrase
	switch {
	case mode == pipeline.EncryptNone:
		if hasKey {
			return pipeline.Decryption{}, nop, errors.New("the artifact is not encrypted (encryption opt-out): omit the key flags")
		}
		return pipeline.Decryption{None: true}, nop, nil
	case !hasKey:
		return pipeline.Decryption{}, nop, errors.New("the artifact is encrypted: pass --passphrase-file, --identity-file or --stdin-passphrase")
	case f.identityFile != "":
		if mode == pipeline.EncryptPassphrase {
			return pipeline.Decryption{}, nop, errors.New("the artifact is passphrase-encrypted: use --passphrase-file or --stdin-passphrase")
		}
		b, err := config.ReadSecretFile(f.identityFile)
		if err != nil {
			return pipeline.Decryption{}, nop, fmt.Errorf("--identity-file: %w", err)
		}
		return pipeline.Decryption{Identities: b}, func() { crypto.Zero(b) }, nil
	}
	if mode == pipeline.EncryptRecipients {
		return pipeline.Decryption{}, nop, errors.New("the artifact is encrypted to age recipients: use --identity-file")
	}
	p, err := f.passphrase(c, m, e)
	if err != nil {
		return pipeline.Decryption{}, nop, err
	}
	return pipeline.Decryption{Passphrase: p, MaxWorkFactor: f.maxWorkFactor}, func() { crypto.Zero(p) }, nil
}

// maxStdinPassphrase bounds a passphrase read from stdin.
const maxStdinPassphrase = 64 << 10

// passphrase returns the passphrase bytes (owned by the caller).
func (f *decryptFlags) passphrase(c *cobra.Command, m *pipeline.Manifest, e *pipeline.Entry) ([]byte, error) {
	if f.stdinPassphrase {
		if f.passphraseVersion != 0 {
			return nil, errors.New("--passphrase-version needs a passphrase export in --passphrase-file")
		}
		return readStdinPassphrase(c)
	}
	b, err := config.ReadSecretFile(f.passphraseFile)
	if err != nil {
		return nil, fmt.Errorf("--passphrase-file: %w", err)
	}
	if !offline.IsPassphraseExport(b) {
		if f.passphraseVersion != 0 {
			crypto.Zero(b)
			return nil, errors.New("--passphrase-version needs a passphrase export in --passphrase-file")
		}
		return b, nil
	}
	ex, err := offline.ParsePassphraseExport(b)
	crypto.Zero(b)
	if err != nil {
		return nil, fmt.Errorf("--passphrase-file: %w", err)
	}
	defer ex.Destroy()
	if m != nil && ex.JobID.String() != m.JobID {
		return nil, fmt.Errorf("--passphrase-file: the export is for job %s, the manifest for job %s", ex.JobID, m.JobID)
	}
	v := f.passphraseVersion
	switch {
	case e != nil && v != 0 && v != e.Encryption.PassphraseVersion:
		return nil, fmt.Errorf("--passphrase-version %d differs from the manifest's passphrase version %d", v, e.Encryption.PassphraseVersion)
	case e != nil:
		v = e.Encryption.PassphraseVersion
	case v == 0 && len(ex.Versions()) == 1:
		v = ex.Versions()[0]
	case v == 0:
		return nil, fmt.Errorf("the export holds passphrase versions %v: pick one with --passphrase-version (or pass --manifest)", ex.Versions())
	}
	s, ok := ex.Passphrase(v)
	if !ok {
		return nil, fmt.Errorf("the export holds no passphrase version %d (it has %v)", v, ex.Versions())
	}
	return slices.Clone(s.Bytes()), nil
}

func readStdinPassphrase(c *cobra.Command) ([]byte, error) {
	in := c.InOrStdin()
	if file, ok := in.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		if _, err := io.WriteString(c.ErrOrStderr(), "Passphrase: "); err != nil {
			return nil, err
		}
		b, err := term.ReadPassword(int(file.Fd()))
		if err != nil {
			return nil, fmt.Errorf("read passphrase: %w", err)
		}
		if _, err := io.WriteString(c.ErrOrStderr(), "\n"); err != nil {
			crypto.Zero(b)
			return nil, err
		}
		return trimPassphrase(b)
	}
	r := bufio.NewReaderSize(io.LimitReader(in, maxStdinPassphrase+1), 4096)
	var buf []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		buf = append(buf, chunk...)
		if err != nil && !errors.Is(err, io.EOF) {
			crypto.Zero(buf)
			return nil, fmt.Errorf("read passphrase: %w", err)
		}
		if len(buf) > maxStdinPassphrase {
			crypto.Zero(buf)
			return nil, errors.New("passphrase on stdin is too long")
		}
		if !isPrefix || err != nil {
			break
		}
	}
	return trimPassphrase(buf)
}

func trimPassphrase(b []byte) ([]byte, error) {
	t := bytes.Clone(bytes.TrimSpace(b))
	crypto.Zero(b)
	if len(t) == 0 {
		return nil, errors.New("empty passphrase")
	}
	return t, nil
}

// sameFile reports whether a and b are the same existing file.
func sameFile(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	return err == nil && os.SameFile(fa, fb)
}

func printDecryptSummary(w io.Writer, f *decryptFlags, artifact string, res offline.DecryptResult, m *pipeline.Manifest, e *pipeline.Entry) error {
	out := f.out
	if out == "-" {
		out = "stdout"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "decrypted %s -> %s\n", artifact, out)
	fmt.Fprintf(&b, "  dump: %s, sha256 %s\n", offline.HumanBytes(res.RawSize), res.RawSHA256)
	switch {
	case e != nil && !f.noVerify:
		fmt.Fprintf(&b, "  verified: signed manifest (job %s, run %s, %s), artifact and dump digests match\n",
			m.JobSlug, m.RunID, m.RunTimestamp.Format("2006-01-02T15:04:05Z"))
	case e != nil:
		b.WriteString("  NOT VERIFIED: the manifest's signature was not checked (--no-verify); the digests match the unverified manifest\n")
	default:
		b.WriteString("  NOT VERIFIED (--no-verify)\n")
	}
	if hint := restoreHint(artifact, f.out, m, e); hint != "" {
		b.WriteString("  restore with: " + hint + "\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// restoreHint suggests the stock restore command for the decrypted dump.
func restoreHint(artifact, out string, m *pipeline.Manifest, e *pipeline.Entry) string {
	if out == "-" {
		out = "<file>"
	}
	name := filepath.Base(artifact)
	globals := strings.HasPrefix(name, pipeline.GlobalsFileBase+"."+pipeline.GlobalsFileBase+".")
	if e != nil {
		globals = e.Kind == pipeline.KindGlobals
	}
	engine := ""
	if m != nil {
		engine = m.Engine.Kind
	}
	switch {
	case strings.Contains(name, ".dump."):
		return "createdb <target> && pg_restore --no-owner --exit-on-error --dbname=<target> " + out
	case globals:
		return "psql --set ON_ERROR_STOP=1 --dbname=postgres --file " + out + "   (review the roles first)"
	case engine == "postgres":
		return "psql --set ON_ERROR_STOP=1 --dbname=<target> --file " + out
	case engine == "mariadb":
		return "mariadb <target> < " + out
	case engine == "mysql":
		return "mysql <target> < " + out
	default:
		return "mysql <target> < " + out + "   (MySQL/MariaDB), or psql --dbname=<target> --file " + out
	}
}
