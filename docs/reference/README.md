# Reference

> **Status:** partial. The configuration key reference is generated today
> (see below). Still to come: the CLI reference (generated from cobra; needs
> a `bekup docs cli` command, task 7.5) and the artifact/manifest format
> (task 4.7). The GraphQL schema reference exists since task 1.2. Completed in task 7.5.

Reference material for bekup. Generated parts are written by `just docs`
(and `just gen`) and must not be edited by hand; CI fails when they are out
of date.

| Reference | Where | Source | Status |
|---|---|---|---|
| Configuration keys (`bekup.toml`, `runner.toml`) | [configuration.md](../configuration.md#key-reference), between the generated markers | `go run ./internal/config/gendocs` (from the config structs) | generated |
| CLI (`bekup` and every subcommand) | `docs/reference/cli/` | cobra/doc via `bekup docs cli` | task 7.5; until then use `bekup <command> --help` |
| GraphQL schema | [schema.graphql](schema.graphql) | `internal/graph/schema/*.graphqls` (concatenated by `just docs`) | generated |
| Artifact and manifest format | `docs/reference/artifact-format.md` (hand-written) | | task 4.7 |
