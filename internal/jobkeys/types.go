package jobkeys

import (
	"bytes"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/crypto"
)

// Mode is the encryption mode of a job.
type Mode string

// Modes (the job_encryption_mode enum).
const (
	ModePassphrase Mode = "passphrase"
	ModeRecipient  Mode = "recipient"
	// ModeNone: an admin opted the job out of encryption.
	ModeNone Mode = "none"
)

// keyMode reports whether m is one of the two encrypting modes.
func (m Mode) keyMode() bool { return m == ModePassphrase || m == ModeRecipient }

// PassphraseInfo describes one passphrase version (never the value).
type PassphraseInfo struct {
	Version   int
	CreatedBy *uuid.UUID
	CreatedAt time.Time
	RetiredAt *time.Time
}

// RecipientInfo describes one recipient version (public data).
type RecipientInfo struct {
	Version     int
	Recipient   string
	Fingerprint string
	Label       string
	// Generated: bekup generated the keypair and showed the identity once.
	Generated bool
	CreatedBy *uuid.UUID
	CreatedAt time.Time
	RetiredAt *time.Time
}

// Status is the encryption state of a job, for the job editor and the
// badge. It never contains key material.
type Status struct {
	JobID uuid.UUID
	// Configured is false for a job without an encryption row (task 3.5
	// creates it with the job); KeyMaterialFor fails for such a job.
	Configured bool
	Mode       Mode
	// OptedOut is the badge flag: an admin turned encryption off.
	OptedOut     bool
	OptedOutBy   *uuid.UUID
	OptedOutAt   *time.Time
	OptOutReason string
	// Passphrase and Recipient are the active versions (nil if none).
	Passphrase *PassphraseInfo
	Recipient  *RecipientInfo
	UpdatedAt  time.Time
}

// GeneratedPassphrase is a freshly generated passphrase, returned once to
// the caller for display. Destroy it after use.
type GeneratedPassphrase struct {
	Info       PassphraseInfo
	Passphrase *crypto.Secret
}

// Destroy zeroes the passphrase.
func (g *GeneratedPassphrase) Destroy() {
	if g != nil {
		g.Passphrase.Destroy()
	}
}

// GeneratedKeypair is a freshly generated age keypair. Only the recipient
// is stored; Identity and IdentityFile are returned once and never
// persisted or logged. Destroy it after use.
type GeneratedKeypair struct {
	Info RecipientInfo
	// Identity is the "AGE-SECRET-KEY-1..." string.
	Identity *crypto.Secret
	// IdentityFile is the identity in the age-keygen file format (comment
	// lines with the creation time, public key and job, then the identity),
	// for download; `age -d -i` and `bekup decrypt` read it.
	IdentityFile *crypto.Secret
	// FileName is a suggested download name.
	FileName string
}

// Destroy zeroes the identity copies.
func (g *GeneratedKeypair) Destroy() {
	if g != nil {
		g.Identity.Destroy()
		g.IdentityFile.Destroy()
	}
}

// KeyMaterial is the encryption configuration the backup pipeline (task
// 3.1) uses for a new artifact: the current version of the job's key. The
// pipeline records PassphraseVersion (or RecipientVersion) on the artifact
// (artifacts.passphrase_version, task 3.6) so a restore can ask for the
// right version. Destroy it after use.
type KeyMaterial struct {
	JobID uuid.UUID
	Mode  Mode
	// Passphrase mode.
	Passphrase        *crypto.Secret
	PassphraseVersion int
	// Recipient mode: the X25519 recipients ("age1...") to encrypt to.
	Recipients           []string
	RecipientVersion     int
	RecipientFingerprint string
}

// Destroy zeroes the passphrase.
func (k *KeyMaterial) Destroy() {
	if k != nil {
		k.Passphrase.Destroy()
	}
}

// Encrypted reports whether artifacts are encrypted (false only for an
// opted-out job).
func (k *KeyMaterial) Encrypted() bool { return k != nil && k.Mode.keyMode() }

// ExportedPassphrase is one version in an export.
type ExportedPassphrase struct {
	PassphraseInfo
	Passphrase *crypto.Secret
}

// Export holds every passphrase version of a job for offline recovery.
// Destroy it after use.
type Export struct {
	JobID      uuid.UUID
	ExportedAt time.Time
	Versions   []ExportedPassphrase
}

// Destroy zeroes every passphrase.
func (e *Export) Destroy() {
	if e == nil {
		return
	}
	for i := range e.Versions {
		e.Versions[i].Passphrase.Destroy()
	}
}

// exportHeader is the first line of the export file format.
const exportHeader = "bekup-job-passphrases v1"

// File returns the export in its text file format (docs/guides/
// encryption.md): a header line, comment lines, then one block per version:
//
//	bekup-job-passphrases v1
//	# ...
//	job <job-id>
//	exported <RFC 3339 UTC>
//	version <n> created <RFC 3339 UTC> [retired <RFC 3339 UTC>]
//	passphrase <passphrase>
//
// The passphrase is the rest of the line verbatim (it contains spaces but
// no control characters). Destroy the returned secret after use.
func (e *Export) File() *crypto.Secret {
	// Grow once so that no partial copy of a passphrase is left behind in
	// a discarded buffer.
	size := 512
	for _, v := range e.Versions {
		size += 128 + v.Passphrase.Len()
	}
	var b bytes.Buffer
	b.Grow(size)
	b.WriteString(exportHeader + "\n")
	b.WriteString("# bekup job passphrases for offline recovery. SECRET: anyone with this file can decrypt\n")
	b.WriteString("# the job's passphrase-encrypted backups. Keep it offline; see docs/guides/encryption.md.\n")
	b.WriteString("job " + e.JobID.String() + "\n")
	b.WriteString("exported " + e.ExportedAt.UTC().Format(time.RFC3339) + "\n")
	for _, v := range e.Versions {
		b.WriteString("version " + strconv.Itoa(v.Version) + " created " + v.CreatedAt.UTC().Format(time.RFC3339))
		if v.RetiredAt != nil {
			b.WriteString(" retired " + v.RetiredAt.UTC().Format(time.RFC3339))
		}
		b.WriteString("\npassphrase ")
		b.Write(v.Passphrase.Bytes())
		b.WriteByte('\n')
	}
	return crypto.NewSecret(b.Bytes())
}

// FileName is a suggested download name of the export.
func (e *Export) FileName() string {
	return "bekup-job-" + e.JobID.String() + "-passphrases.txt"
}
