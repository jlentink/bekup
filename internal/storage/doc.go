// Package storage defines the backup destination interface and its Disk
// (os.Root confined), S3 and SFTP backends.
//
// Every backend implements [Destination]. Object keys are [Key] values:
// validated, slash-separated relative paths built with [BuildKey] (layout
// {prefix}/{job-slug}/{timestamp}/{file}) and [ArtifactFileName] (which
// encodes database names with [EncodeDBName]). Arbitrary strings never reach
// a backend: [ParseKey] rejects absolute paths, "." and "..", segments that
// start with a dot (reserved for backend temporary files), control
// characters, backslashes and characters outside a small allowlist.
//
// Put is streaming and atomic: a partially written object is never visible
// under its final key, a failed or canceled Put leaves no final object, and
// an existing key is never overwritten ([ErrAlreadyExists]). Deletes are
// refused with [ErrDeleteNotPermitted] in no-delete mode or under an active
// object lock. [Capabilities] tells retention (task 3.8) and the pipeline
// (task 3.1) what a backend supports.
//
// [ParseLayoutKey] recognizes keys of bekup's layout; [DirPruner] (disk and
// SFTP) removes empty run and job directories of it (D-209).
//
// The conformance suite in package storagetest is run against every backend.
// See D-121 in docs/DECISIONS.md.
package storage
