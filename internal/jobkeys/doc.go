// Package jobkeys manages the encryption keys of backup jobs (task 3.2,
// D-016, D-142).
//
// Every job encrypts its backup files with age (on by default). A job is in
// one of three modes:
//
//   - passphrase: age scrypt with a versioned per-job passphrase, stored in
//     job_passphrases sealed with the master keyring under AAD
//     job_passphrases:<job_id>:v<version>. A new passphrase retires the
//     previous version; retired versions are kept forever because older
//     artifacts need them (PassphraseForVersion, restore task 4.1).
//   - recipient: age X25519 to a public recipient ("age1..."), versioned in
//     job_recipients. The private key (identity) is supplied at restore
//     time; when bekup generates the keypair it returns the identity once
//     and never stores or logs it.
//   - none: an admin opt-out (step-up, audited as security.opt_out,
//     alerted through AlertFunc, shown as a badge via Status.OptedOut).
//
// Authorization is enforced at the GraphQL layer and checked again here
// against the caller's principal in the context (role, step-up and that the
// actor is the caller): key changes need job.encryption_keys_edit (admin
// only, fresh step-up; also a key chosen at job creation, while the default
// generated passphrase comes with job.create), the opt-out
// job.encryption_opt_out and the export job.passphrase_export. The
// worker-side reads (KeyMaterialFor, PassphraseForVersion) take no
// principal.
//
// Secrets travel as *crypto.Secret; every plaintext buffer the service owns
// is zeroed after use. Callers own (and must Destroy) the secrets returned
// to them.
package jobkeys
