// Package setup implements `bekup setup` (D-203, D-205): a container-only
// compose install. From the embedded deployment assets (package bekup) it
// writes a complete deployment directory that needs no source checkout.
//
// Compose-first (the default, D-205): the operator saves a release compose
// file as compose.yaml and runs its `setup` service. Init reads it
// (composefile.go): the digest-pinned image comes from the single
// x-bekup-image line, the variant is recognized by rendering every release
// variant with that image (exact, same YAML model, or customized with
// --variant), and compose.yaml is never written. Setup-first
// (--write-compose) writes compose.yaml itself.
//
// Compose files are judged on the resolved model only (composemodel.go,
// composelint.go): compose-go, the Docker Compose loader, resolves
// compose.yaml + compose.override.yaml + .env (merges, !reset/!override,
// extends, interpolation; multi-document files and other compose files
// refused), and the allowlist lint checks that project.
//
// Variants (variants.go) are overlays of the repository compose file: edit
// lists applied by a path-addressed line editor (outline.go) with
// fragments below variants/; untouched text stays byte-identical and no
// fragment names an image.
//
// Init writes:
//
//   - deploy/** (only what the variant mounts) and reference/*.example.toml
//     (generated-upgradable: rewritten by `setup upgrade`), and setup-first
//     compose.yaml;
//   - .env, minimal bekup.toml / runner.toml and, for static proxy TLS,
//     compose.override.yaml (operator files: written once, validated with
//     the bekup config loaders);
//   - the secrets: database passwords and URLs, the master keyring and the
//     private CA with the runner, serve and postgres certificates (CA key
//     sealed with a passphrase; meant to live on offline media).
//
// One versioned ownership manifest (manifest.go) drives init, check and
// --fix-ownership: every path's kind, mode, owner (the consuming
// container's UID) and class. Every write goes through an os.Root on the
// deployment directory; files are created with O_EXCL|O_NOFOLLOW in their
// final mode, symlinks below the directory are refused, and a failed run
// removes everything it created (an existing CA is never touched). Modes
// are changed only when they differ (a file owned by a container user is
// taken over, chmod'ed and handed back), so the one-shot setup container
// needs only CAP_CHOWN; every change is made on a descriptor opened
// without following symlinks (entryfd_*.go).
//
// bekup-setup.json records the image, versions and the hashes of the
// generated files (no secrets) so upgrade can detect local edits and check
// can detect drift. Setup needs no network and runs before the database
// exists, so it is not audited; it prints the certificate fingerprints and
// the master key ID as evidence instead.
package setup
