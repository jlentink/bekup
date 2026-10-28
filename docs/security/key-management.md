# Key management and escrow

> **Status:** the master keyring, credential encryption, key-check value,
> instance signing keys and key generators are implemented (task 0.5). The
> `instance_keys` table exists (migration 00005), the audit chain-head
> signer uses it (task 1.3, D-101), and `bekup serve` refuses to start when
> the keyring cannot open the active instance signing key or does not match
> the key-check values stored in `settings` (D-119). Not wired yet:
> re-encryption of stored credentials on rotation. Job encryption keys are task 3.2;
> the offline recovery tools and `bekup keys export-public` are task 4.7
> ([recovery.md](../recovery.md)).

bekup has three kinds of keys:

| Key | Protects | Where it lives | Lose it and... |
|---|---|---|---|
| **Master keyring** | Every credential stored in the app DB (DB/destination passwords, SSH keys, job passphrases, TOTP secrets, signing-key private keys) | `security.master_key_file` (a Docker secret, 0600) | every stored secret is gone for good |
| **Instance signing keys** (ed25519) | Authenticity of backup manifests and audit chain heads | `instance_keys` table, private key sealed with the keyring | new backups get a new key; escrowed fingerprints verify old ones |
| **Job encryption keys** (age) | The backup files themselves | passphrase sealed in `job_passphrases`, or an age identity you keep (never stored) | backups of that job cannot be decrypted |

## The master keyring

### Creating it

```sh
just keygen                         # writes secrets/bekup_master_key
# or
bekup keys generate --out /path/to/bekup_master_key
```

`bekup keys generate` draws a 256-bit key from the operating system CSPRNG
(Go `crypto/rand`), writes the file with mode `0600` atomically and **never
overwrites** an existing file (not even a dangling symlink). It prints the key
ID and the escrow guidance below; it never prints key material.

Point bekup at it with `security.master_key_file` (default
`/run/secrets/bekup_master_key`). bekup refuses to read the file if it is not
a regular file owned by the current user or root, is group- or
world-writable, or is world-readable (`config.ReadSecretFile`, D-041). In dev
mode only, the inline `security.master_key` may hold the same file content
(use a TOML multi-line string).

### File format (version 1)

```text
# Comments (#) and blank lines are ignored.
bekup-keyring v1
key 3f9a0c1e 2026-10-26T10:00:00Z q2V0b3BzZWNyZXRrZXltYXRlcmlhbC4uLi4uLi4uLi4=
key 81d4b7a2 2027-04-01T09:30:00Z ZXhhbXBsZW9ubHlkb25vdHVzZXRoaXNrZXkuLi4uLi4=
primary 81d4b7a2
```

- The first non-comment line must be exactly `bekup-keyring v1`.
- `key <id> <created> <material>`: the ID is 8 lowercase hex characters
  (4 random bytes); `created` is RFC 3339; the material is standard, padded
  base64 of exactly 32 bytes.
- Exactly one `primary <id>` line naming one of the keys. The **primary key
  encrypts**; **every key decrypts**.
- Duplicate IDs, duplicate key material, unknown directives, non-canonical
  base64, more than 256 keys and lines longer than 4 KiB are rejected. Error
  messages give the line number but never the line content.

(The example keys above are not real; never copy key material from docs.)

### Rotation

```sh
bekup keys rotate --keyring /path/to/bekup_master_key
```

Rotation generates a new key, adds it to the file and makes it the primary;
all existing keys are kept so everything encrypted earlier still decrypts.
The file is rewritten atomically (temporary file, fsync, rename, directory
fsync) with mode `0600`; if the path is a symlink, its target is replaced.
After rotating:

1. **Replace every escrow copy** with the new file. An old copy cannot
   decrypt anything sealed with the new key.
2. Restart bekup so it loads the new primary key.
3. **Do not remove old keys.** Re-encrypting stored credentials with the new
   primary key (`Keyring.Rotate` / `RotateAll` in `internal/crypto`) is wired
   to the database in a later task; until it reports that no ciphertext uses
   an old key, removing it destroys data.
   Recovery codes are stored only as keyed hashes under the key that was
   primary when they were issued (D-111); they cannot be re-encrypted, so
   removing that key invalidates them and users must regenerate their
   codes.

Rotate when an escrow copy may have been exposed, when staff with access
leave, or on a schedule you choose. If the key itself is known to be
compromised, also rotate every stored credential at its source (database
passwords, S3 keys), because an attacker holding an old DB dump and the old
key can still decrypt the old values.

### Escrow and loss

The keyring is the single point of failure for every stored credential.

- Keep at least **one offline copy**, separate from the bekup host and from
  the backups: a password-manager vault, an encrypted USB stick in a safe, or
  a printed copy of the file.
- Re-escrow after every `bekup keys rotate`.
- Record the key IDs with the escrow copy so you can tell copies apart.
- **If every copy is lost**, nothing stored in the app DB can be decrypted:
  all connection, destination and notification secrets must be re-entered,
  TOTP must be re-enrolled, a new instance signing key is created, and
  backups protected by a stored job passphrase can only be decrypted with a
  previously **exported** passphrase (D-040). Backups encrypted to an age
  recipient whose identity you hold are unaffected.
- Anyone holding both the keyring and a copy of the bekup database can
  decrypt every secret. Never commit the file, mail it, or put it on a shared
  volume.

## Credential encryption design

Implemented in `internal/crypto` (D-046, D-086):

- **AES-256-GCM**, random 96-bit nonce per encryption.
- The master key is never used directly: HKDF-SHA256 derives a separate
  subkey for encryption and one for the key-check value.
- **Ciphertext format (v1):**
  `0x01 | key id (4 bytes) | nonce (12 bytes) | ciphertext + 16-byte tag`.
- **AAD = `table:row_id:field`**, for example
  `connections:0192f0c4-...:secret_enc`. The GCM additional data is the
  5-byte header followed by that string, so a ciphertext **cannot be copied
  to another row, table or field**, and its version and key ID cannot be
  changed, without decryption failing. The components are restricted
  (`table`/`field`: `^[a-z][a-z0-9_]{0,62}$`, `row_id`:
  `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`), so the encoding is unambiguous.
- Every authentication failure (tampering, wrong key material, wrong AAD)
  returns the same generic error. An unknown key ID is reported as such (the
  keyring is missing a key).
- No per-record data keys: credentials are tiny and rotation re-encrypts the
  record either way (rationale in D-086).
- **Key-check value:** `kcv1:<key id>:<base64url HMAC-SHA256>` of a fixed
  string under the KCV subkey. It reveals nothing about the encryption
  key. `bekup serve` keeps one KCV per master key ID in the `settings` row
  `security.key_check_values` and checks the keyring against them at every
  start, before anything is decrypted (constant-time, D-119):
  - first start: the KCVs of every key in the ring are stored (audited as
    `keys.check_value_stored`);
  - every stored KCV whose key is in the ring must verify, and at least
    one must; otherwise serve refuses to start with "the master key does
    not match this database" (a wrong, truncated or foreign keyring);
  - a stored key that is no longer in the ring only logs a warning (it
    was retired after re-encryption);
  - keys in the ring without a stored KCV, i.e. the new primary after
    `bekup keys rotate`, are added and recorded as `keys.rotated`.

### Secrets in memory

`crypto.Secret` wraps secret bytes: `fmt` (every verb), `slog`, JSON and text
marshaling all print `[REDACTED]`, and `Destroy()` zeroes the bytes.
`crypto.Zero` clears buffers. Go cannot guarantee that no copy remains: the
garbage collector may move objects, the runtime may spill values to stacks or
registers, `append` and string conversions copy, and pages can be swapped to
disk. Zeroing shortens the lifetime of secrets and removes the copies bekup
owns; it is not a guarantee. Some libraries (age) take passphrases as
strings, which cannot be zeroed at all.

## Instance signing keys

Manifests and audit chain heads are signed with an ed25519 **instance key**
(D-038, D-087):

- **Key ID** = the first 8 bytes of SHA-256(public key), in hex (16
  characters), so an ID always matches exactly one public key.
- **Fingerprint** = SHA-256 of the raw 32-byte public key, shown as hex and
  as `SHA256:<base64>`. **Escrow the public keys and their fingerprints**
  (see below) so `bekup verify` can check manifests offline.
- The private key (its 32-byte seed) is stored sealed with the keyring under
  AAD `instance_keys:<id>:private`; on load, the derived public key must
  match the stored one.
- Old public keys are kept, so manifests signed by retired keys still verify.
- Signatures are domain-separated: the signed bytes are
  `"bekup-sig-v1" 0x00 <purpose> 0x00 <message>` (purposes such as
  `manifest`), so a signature for one purpose is never valid for another.
  Third-party verifiers must prepend the same prefix.

### Escrowing the instance public keys

The offline tools verify manifests without the database, so they need the
public keys from somewhere else:

```sh
bekup keys export-public --out bekup-instance-keys.txt
```

reads only the public keys (active and retired) from `instance_keys`
(no keyring needed) and writes them in the format of
[artifact-format.md](../reference/artifact-format.md#instance-public-key-file),
printing each key ID and `SHA256:` fingerprint. The file never overwrites
an existing one without `--force`.

- Store the file with the keyring escrow, and **record the fingerprints
  separately** (for example on the printed escrow sheet). The file is not
  secret, but whoever can replace it can make forged manifests verify:
  compare the fingerprints before trusting a copy.
- Export again after a new instance key appears (first start, or a
  database that had no key). Keep retired keys: they verify the manifests
  they signed.
- Losing the public keys does not lose data (backups can still be
  decrypted with `bekup decrypt --no-verify`), but it removes the
  authenticity check.

### Escrowing job keys

- **Passphrase jobs:** an admin exports every passphrase version of a job
  (`exportJobPassphrases`: step-up, audited as `job.passphrases_exported`,
  security alert). `bekup decrypt --passphrase-file <export>` reads that file
  directly and picks the version named in the manifest. The export is
  **secret**: treat it like the keyring (offline, access-controlled, never in
  a ticket or chat). Re-export after every passphrase change.
- **Recipient jobs:** bekup never stores the age identity; keep the
  one-time download (or your own identity) in the escrow.
  `bekup decrypt --identity-file` reads it (X25519 only).
- Key files given to the offline tools must be regular files owned by you
  and not group- or world-accessible; secrets are never accepted as flag
  values.

## Key generators

All generators use `crypto/rand` (D-016, D-088):

- **Job passphrases:** 10 words chosen uniformly from the
  [EFF large wordlist](https://www.eff.org/dice) (7776 words), separated by
  spaces: 10 x log2(7776) = **129.2 bits** of entropy. The wordlist is by the
  Electronic Frontier Foundation, licensed
  [CC BY 3.0 US](https://creativecommons.org/licenses/by/3.0/us/); bekup
  embeds it with the dice-roll column removed (see `NOTICE`).
- **age keypairs** (X25519): the recipient (`age1...`) is stored with the
  job; the identity (`AGE-SECRET-KEY-1...`) is shown and downloaded once and
  **never stored**.
- **SSH keypairs** (ed25519): an OpenSSH-format private key (stored sealed
  with the keyring), an `authorized_keys` line to install on the server, and
  its `SHA256:` fingerprint.

### age encryption bounds

Backups are encrypted with [age](https://age-encryption.org). For passphrase
(scrypt) encryption bekup writes work factor 2^18 (256 MiB of memory) and
accepts at most 2^20 (1 GiB). Decryption sets age's `SetMaxWorkFactor`
(default 2^18, never above 2^20), so a crafted file cannot make bekup or the
offline tools burn unbounded CPU and memory. Identity-based decryption only
accepts X25519 identities, never scrypt or plugin identities.
