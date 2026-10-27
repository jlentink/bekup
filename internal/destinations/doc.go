// Package destinations manages backup destinations (task 2.12, D-131):
// reusable disk, S3 and SFTP targets stored in the destinations table and
// administered through the GraphQL API.
//
// Service is the only code that reads or writes the table. It validates
// every configuration per type when it is saved (S3 endpoints and SFTP
// hosts are resolved and checked against the SSRF guard, S3 settings go
// through s3store's validation, disk paths must be clean sub-paths of
// storage.disk_root), keeps credentials write-only (callers get
// HasSecretAccessKey / HasPassword / HasPrivateKey flags, never values),
// seals them with the master keyring under AAD destinations:<id>:secret
// and records every change in the audit log (changed field names only;
// credential fields appear as "changed").
//
// Open builds the storage.Destination of a destination for the backup,
// retention and restore workers (tasks 3.x) with the netguard guard;
// Test runs the storage probe for the "test destination" action with
// normalized results, rate limited per user. FetchHostKey is the TOFU step
// of the SFTP host-key pin (internal/tunnel.FetchHostKey through the
// guard); the pin is only set by ConfirmHostKey after the admin compared
// the fingerprint out of band.
package destinations
