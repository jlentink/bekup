// Package spec builds the hardened Docker container specification of a dump
// or restore container (D-031, D-130). It is pure: it returns the Docker API
// structs (github.com/moby/moby/api) and never talks to the daemon (the
// executor of task 2.6 creates the container).
//
// Every field is fixed by this package or derived from validated inputs:
// the image comes from the allowlisted catalog entry and is referenced by
// digest only, the argv comes from internal/engine, the entrypoint is the
// constant preamble wrapper (internal/runner/preamble), the environment is
// a constant allowlist, and the hardening (read-only root, tmpfs /tmp,
// CapDrop ALL, no-new-privileges, non-root user, limits, no mounts) cannot
// be changed by a request. Secrets never enter the spec; they travel only
// through the stdin preamble.
package spec
