package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jlentink/bekup/internal/crypto"
)

func newKeysCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keys",
		Short: "Generate and rotate the master keyring",
		Long: "Manage the master keyring that encrypts every credential bekup stores (AES-256-GCM).\n" +
			"Format, rotation and escrow: docs/security/key-management.md.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newKeysGenerateCmd(), newKeysRotateCmd())
	return cmd
}

func newKeysGenerateCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "generate --out <path>",
		Short: "Create a new master keyring file (256-bit key, mode 0600, never overwrites)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(out) == "" {
				return errors.New("--out is required")
			}
			k, err := crypto.GenerateKeyring(time.Now())
			if err != nil {
				return err
			}
			defer k.Destroy()
			if err := crypto.WriteKeyringFile(out, k, false); err != nil {
				return err
			}
			return printEscrowGuidance(cmd.OutOrStdout(), out, k)
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "path of the keyring file to create (must not exist)")
	return cmd
}

func newKeysRotateCmd() *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "rotate --keyring <path>",
		Short: "Add a new primary key to the keyring file (old keys stay for decryption)",
		Long: "Adds a freshly generated key to the keyring and makes it the primary (encrypting) key.\n" +
			"The file is rewritten atomically with mode 0600. Every existing key is kept, so data\n" +
			"encrypted with older keys stays readable. Re-encrypting the stored credentials with the\n" +
			"new primary key is wired to the database in a later task; until then, never remove old keys.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(path) == "" {
				return errors.New("--keyring is required")
			}
			k, err := crypto.LoadKeyringFile(path)
			if err != nil {
				return err
			}
			defer k.Destroy()
			old := k.PrimaryID()
			id, err := k.AddPrimary(time.Now())
			if err != nil {
				return err
			}
			if err := crypto.WriteKeyringFile(path, k, true); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			_, err = fmt.Fprintf(w, `Rotated keyring %s
  new primary key ID: %s
  previous primary:   %s (kept for decryption)
  keys in keyring:    %d

Next steps:
  1. Replace EVERY escrow copy of the keyring with this new file. An old copy cannot
     decrypt anything encrypted with the new key.
  2. Restart bekup so it loads the new primary key.
  3. Do not remove old keys: stored credentials stay encrypted with them until they are
     re-encrypted (database re-encryption is wired in a later task).
`, path, id, old, len(k.IDs()))
			return err
		},
	}
	cmd.Flags().StringVar(&path, "keyring", "", "path of the existing keyring file")
	return cmd
}

func printEscrowGuidance(w io.Writer, path string, k *crypto.Keyring) error {
	_, err := fmt.Fprintf(w, `Created master keyring %s (mode 0600)
  primary key ID: %s

ESCROW THIS FILE NOW. It encrypts every credential bekup stores (database and
destination passwords, SSH keys, job backup passphrases, TOTP secrets).

  - Store at least one copy OFFLINE (e.g. a password manager vault, an encrypted USB
    stick in a safe, or a printed copy of the file), separate from the bekup host and
    from the backups themselves.
  - If every copy is lost, all stored credentials and passphrase-encrypted job keys
    are unrecoverable: you would have to re-enter every secret, and backups encrypted
    with stored passphrases can then only be decrypted with an exported passphrase.
  - Anyone with this file and a copy of the bekup database can decrypt every secret.
    Never commit it, mail it or put it on a shared volume.
  - Mount it into bekup as a Docker secret and set security.master_key_file.

See docs/security/key-management.md.
`, path, k.PrimaryID())
	return err
}
