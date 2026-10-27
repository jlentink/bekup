// Package s3store is the S3 (and S3-compatible) storage backend (task 2.9).
//
// It implements storage.Destination on aws-sdk-go-v2 with a deliberately
// narrow client: static credentials only (no environment, shared config or
// instance-metadata chain, which would be an SSRF and credential-confusion
// vector), every connection dialed through the netguard (private endpoints
// such as minio must be allowlisted), https only unless dev mode, an
// optional custom CA bundle that replaces the system roots, redirects never
// followed and response bodies other than object downloads size-limited.
// Objects are streamed through bekup; presigned URLs are never created.
//
// Put: objects up to Config.PartSize use one PutObject; larger objects a
// multipart upload of PartSize parts (default 64 MiB), Concurrency parts in
// flight, so one Put holds at most PartSize × Concurrency bytes. The object
// becomes visible only on CompleteMultipartUpload; any failure, including
// cancellation, aborts the upload on a detached context. Complete itself
// runs detached (bounded); after a failed or ambiguous Complete a HEAD
// decides: only the exact object (size plus a strong content check) counts
// as uploaded (D-136). "If-None-Match: *"
// on PutObject and CompleteMultipartUpload makes never-overwrite atomic
// (Capabilities.ExclusiveCreate). With ConditionalWrites disabled (stores
// that do not support it) only the Stat pre-check guards the key: two
// concurrent Puts of the same key can then both succeed and the later one
// wins (on a versioned bucket the earlier version survives as a noncurrent
// version). The SHA-256 is computed while streaming; it is stored as
// x-amz-meta-bekup-sha256 when known before the upload starts (single-part
// objects, or multipart with PutOptions.ExpectedSHA256) and the native S3
// SHA-256 checksum (composite for multipart) is sent in when_supported
// mode. The SDK runs with RequestChecksumCalculation=WhenRequired; bekup
// sets the checksum headers itself.
//
// Object Lock: detected at Open (GetObjectLockConfiguration) and reported
// in Capabilities; lock options on other buckets fail closed. Delete on a
// versioned/locked bucket removes the key's versions permanently (never
// just a delete marker) and refuses locked versions with
// storage.ErrDeleteNotPermitted; governance retention is never bypassed.
// No-delete mode refuses every Delete. See D-129 in docs/DECISIONS.md.
package s3store
