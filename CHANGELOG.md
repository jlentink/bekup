# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
From the first release on, this file is maintained by
[git-cliff](https://git-cliff.org/) from Conventional Commits via `just bump`.

## [Unreleased]

### Added

- Project scaffold: Go module, package layout, license, documentation
  skeleton, decision log (`docs/DECISIONS.md`) and contributor/agent guide
  (`AGENTS.md`).
- Runtime config reload (D-200, proposed): `bekup serve` and `bekup worker`
  re-read `bekup.toml` on an admin request (Admin → Settings →
  Configuration, every process) or on SIGHUP (one process) and apply a
  conservative set of live keys; other changed keys are reported as
  needing a restart, an invalid file is refused, every reload is audited
  and a weakening change alerts every admin.
