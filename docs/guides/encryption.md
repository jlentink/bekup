# Backup encryption

bekup encrypts every backup file with [age](https://age-encryption.org)
before it leaves the server. Encryption is **on by default** for every job;
only an admin can turn it off for a job (see [Opting out](#opting-out)).
Decisions: D-008, D-016, D-040, D-088, D-142.

> **Status:** implemented: the key management service and its GraphQL
> fields (`Job.encryption`, `createJob(input.encryption)`,
> `setJobPassphrase`, `generateJobPassphrase`, `setJobRecipient`,
> `generateAgeKeypair`, `enableJobEncryption`, `disableJobEncryption`,
> `exportJobPassphrases`), the offline `bekup decrypt` tool
> ([recovery.md](../recovery.md)) and the web UI described below.

## In the web UI

- **At creation** (**Backups → Jobs → New job**, section **Encryption**):
  **Generate a passphrase (recommended)** is the default and the only
  choice for maintainers; admins can also set their own passphrase, set
  an age recipient or generate an age keypair. A generated passphrase or
  private key is shown **once** after saving, with copy and download;
  it is dropped from the page as soon as you confirm "I've saved it
  somewhere safe" and never enters the browser's cache (D-186).
- **Later** (**Edit job → Encryption**): the current mode, passphrase
  version, recipient and fingerprint, and for admins the key actions
  **Generate passphrase**, **Set passphrase**, **Generate age keypair**,
  **Set recipient**, **Turn encryption off** (with a reason and an
  acknowledgement) and **Export passphrases**. Each asks for a step-up.
- Jobs without encryption carry the hazard-hatched *Unencrypted* badge on
  the job list, the job page and the dashboard.
- **Administration → Settings → Recovery** lists every job's key mode and
  exports a job's passphrases (with a warning, an acknowledgement and a
  step-up) next to the instance public keys needed for offline
  verification (D-194).

## Modes

Each job uses one of two modes. A job keeps the keys of both modes; the key
you set or generated last decides the mode.

| Mode | How files are encrypted | What you need to restore |
|---|---|---|
| **Passphrase** (default) | age scrypt with the job's passphrase | Nothing extra: bekup stores the passphrase encrypted with its master key and uses it for restores |
| **Recipient** | age X25519 to a public key (`age1...`) | The matching private key (identity, `AGE-SECRET-KEY-1...`), supplied at restore time. bekup never stores it |

Use **recipient** mode when bekup itself must not be able to decrypt the
backups (for example when a compromise of the bekup server must not expose
old backups). Use **passphrase** mode when bekup should be able to restore
unattended (scheduled test restores need it).

## Versions

Passphrases and recipients are **versioned**. Setting or generating a new
one creates the next version and *retires* the previous one; retired
versions are never deleted, because older backups can only be decrypted
with the version they were written with. Every backup records the version
it used (and, in recipient mode, the recipient's fingerprint: the lowercase
hex SHA-256 of the `age1...` string), so restores pick the right key
automatically.

Changing a key affects only **new** backups. Existing backups keep their
original key.

## Generating and setting keys

Only **admins** edit a job's keys (generate or set a passphrase or a
recipient, opt back in; D-154). Every change needs a fresh step-up and is
recorded in the audit log. Maintainers can create jobs, which get a
generated passphrase automatically, but cannot choose or change a job's
key.

- **Generate passphrase:** bekup draws 10 words from the EFF large word list
  (about 129 bits of entropy) and shows the passphrase **once**. It is
  stored encrypted; write it down only if you need it outside bekup (an
  admin can export it later, see below).
- **Set passphrase:** your own passphrase must be at least 20 characters,
  without control characters or leading/trailing spaces, and rated as hard
  to guess (zxcvbn score 4). Backups are exposed to offline guessing for as
  long as they are kept, so prefer a generated one.
- **Generate age keypair:** bekup creates an X25519 keypair, stores only the
  public key and shows the private key **once**, with a download
  (`bekup-job-<job-id>-v<version>.agekey`, the `age-keygen` file format).
  bekup never stores or logs the private key: if you lose it, the backups
  of that recipient version cannot be restored by anyone.
- **Set recipient:** paste an existing `age1...` public key (for example
  from `age-keygen` or a hardware-backed identity exported as X25519). Only
  X25519 recipients are accepted; SSH and plugin recipients are not.

Store downloaded identities offline (password manager, hardware-encrypted
storage, printed copy in a safe), separate from the backups themselves.

## Opting out

Only an **admin** can turn encryption off for a job, with a fresh step-up
and a reason. The opt-out is:

- recorded in the audit log (`job.encryption_opted_out` and
  `security.opt_out`, with the reason),
- raised as a security alert,
- shown as a permanent badge on the job until encryption is turned on again.

The keys are kept. Turning encryption back on (choose the mode, or set or
generate a new key) only needs the normal key-editing right, because it
makes the job more secure.

Backups written while a job was opted out stay unencrypted at the
destination.

## Exporting passphrases (offline recovery)

An admin can export **all** passphrase versions of a job (step-up required;
audited as `job.passphrases_exported` and raised as a security alert). Keep
the export for disaster recovery when the bekup database is lost: with it,
the offline tools (`bekup decrypt --passphrase-file <export>`, which picks
the version from the run's manifest, or plain `age -d`) decrypt the backups
without bekup.

The export is a text file (`bekup-job-<job-id>-passphrases.txt`):

```
bekup-job-passphrases v1
# comment lines
job <job-id>
exported <RFC 3339 UTC>
version 1 created <RFC 3339 UTC> retired <RFC 3339 UTC>
passphrase <the passphrase, rest of the line verbatim>
version 2 created <RFC 3339 UTC>
passphrase <...>
```

Anyone holding this file can decrypt the job's passphrase-encrypted
backups. Treat it like the master key: offline, access-controlled, never in
a ticket or chat.

## Recovery without bekup

Preferred: `bekup decrypt <artifact> --out <file> --passphrase-file <export>`
(or `--identity-file <identity>`) `--manifest manifest.json --pubkey
<instance keys>`; it verifies the signed manifest first. With the stock
tools:

- **Passphrase mode:** `age -d -o dump.zst file.age` and enter the
  passphrase of the artifact's version (from the export), then
  `zstd -d dump.zst`.
- **Recipient mode:** `age -d -i bekup-job-<id>-v<n>.agekey -o dump.zst
  file.age`, then `zstd -d dump.zst`.

See [recovery.md](../recovery.md) for the full procedure, including the
manifest and signature checks.
