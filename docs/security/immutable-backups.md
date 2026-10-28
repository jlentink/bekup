# Immutable backups

> **Status:** partial. S3 Object Lock, versioning and no-delete mode exist
> (task 2.9); retention applies them (task 3.8, D-148). Delivery of the
> alerts on unexpected deletions follows in task 5.5 (until then they are
> logged at WARN and audited).

Ransomware and stolen credentials go after backups first. bekup's answer
(D-007) is to make deletion impossible for bekup itself, so a compromised
bekup server, a stolen destination key or a malicious admin account cannot
destroy the backups that already exist.

## Options at a glance

| Option | Protects against | Who enforces it | Retention done by |
|---|---|---|---|
| S3 Object Lock, compliance mode | Everyone, including the bucket owner, until the retention date | The S3 service | bekup (after the lock expires) or lifecycle rules |
| S3 Object Lock, governance mode | Everyone without `s3:BypassGovernanceRetention` | The S3 service | bekup or lifecycle rules |
| No-delete mode + key without delete permission | bekup and its key | IAM policy + bekup | Bucket lifecycle rules |
| Versioning only | Accidental overwrite/delete markers | The S3 service | Lifecycle rules for noncurrent versions |
| Disk / SFTP | Nothing beyond file permissions | — | bekup |

Use more than one destination (fan-out) and keep at least one off the
bekup host. Disk and SFTP destinations are never immutable from bekup's
point of view; use the SFTP server's permissions (see the destinations
guide) or an S3 copy for ransomware resistance.

## S3 Object Lock

Object Lock must be enabled when the bucket is created (it also turns on
versioning). bekup reads the bucket's Object Lock configuration when it
opens a destination and reports it in the destination's capabilities.

- **bekup-applied retention.** Set a lock mode and a retention in days on
  the destination: every artifact bekup writes carries
  `x-amz-object-lock-mode` and `x-amz-object-lock-retain-until-date`. A
  destination with a lock mode refuses to open on a bucket without Object
  Lock, and bekup never silently writes an unprotected object when a lock
  was requested (D-121: fail closed).
- **Bucket default retention.** Alternatively, configure a default
  retention on the bucket; it applies to every object, including bekup's
  test probes (which then stay until their retention ends).
- **Legal hold** is supported per object (for example for an incident).
- **Deletes.** When bekup deletes an artifact on a versioned or locked
  bucket it deletes the object's versions permanently. A locked version is
  refused by the store and bekup reports "deleting is not permitted"; it
  never adds a delete marker that would merely hide a locked object, and
  it never sends the governance-bypass header.
- **Compliance vs governance.** Compliance cannot be shortened or removed
  by anyone, including the AWS root user, until it expires: choose the
  retention carefully (storage costs). Governance can be lifted by a
  principal with `s3:BypassGovernanceRetention`; deny that permission to
  bekup's key.

## No-delete mode

A destination in no-delete mode refuses every delete inside bekup
(retention skips it without contacting it, D-148), and the destination test leaves its probe
in place. The real protection comes from the storage side: give bekup's
key **no** delete permission (`s3:DeleteObject`, `s3:DeleteObjectVersion`)
and no permission to change the bucket's lifecycle, versioning or policy.
Old backups are then expired by bucket lifecycle rules that only the
bucket owner can change. The destinations guide has an IAM deny statement
and a lifecycle example.

Combine no-delete mode with versioning (or Object Lock): an attacker with
bekup's key can still *write*, but uploads never overwrite (conditional
writes) and on a versioned bucket earlier versions survive any overwrite
that slipped through.

## What bekup still needs to be trusted for

- Writing new backups: a compromised bekup can stop producing backups or
  write garbage. Missed-run monitoring (task 5.6) and the signed manifest
  (task 3.1) detect this.
- Confidentiality: Object Lock does not encrypt. Backups are age-encrypted
  by default (D-008).
- Lifecycle expiry does not know bekup's "always keep the newest successful
  backup" rule; pick an expiry well above the backup interval.

## How retention treats protected backups (task 3.8)

- Runs with a recorded, unexpired lock are never even planned for
  deletion. A delete refused by the store (a lock bekup did not record, a
  bucket default retention, a legal hold) leaves the artifact `present`;
  the sweep summary (`retention.swept`) records the count and the latest
  retain-until date, and later sweeps retry after the lock expires.
- No-delete destinations are skipped entirely; lifecycle rules expire
  their objects.
- On every destination the newest complete backup is never deleted, and a
  destination without a complete backup is never pruned at all.

## Unexpected deletions

bekup checks an object before deleting it and checks the newest complete
backup on a destination before deleting anything there. An object that is
gone although the database lists it as present was deleted outside bekup:

- its artifact is marked `missing` (never `deleted`) and
  `retention.artifact_missing` is audited in the same transaction;
- the `retention_artifact_missing` alert is raised (delivered by task 5.5;
  logged at WARN until then);
- if it belongs to the newest complete backup, nothing is deleted on that
  destination in this sweep (`retention.aborted`, alerted as well), so
  retention can never finish off a destination an attacker has already
  thinned out.

Kept backups that retention does not touch are only checked when they
become the newest complete backup or eligible for deletion; full periodic
verification of all stored objects is a later task.
