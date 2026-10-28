# Reference

> **Status:** partial. The configuration key reference is generated today
> (see below). Still to come: the CLI reference (generated from cobra; needs
> a `bekup docs cli` command, task 7.5). The artifact/manifest format is
> documented since task 3.1. The GraphQL schema reference exists since task 1.2. Completed in task 7.5.

Reference material for bekup. Generated parts are written by `just docs`
(and `just gen`) and must not be edited by hand; CI fails when they are out
of date.

| Reference | Where | Source | Status |
|---|---|---|---|
| Configuration keys (`bekup.toml`, `runner.toml`) | [configuration.md](../configuration.md#key-reference), between the generated markers | `go run ./internal/config/gendocs` (from the config structs) | generated |
| CLI (`bekup` and every subcommand) | `docs/reference/cli/` | cobra/doc via `bekup docs cli` | task 7.5; until then use `bekup <command> --help` |
| GraphQL schema | [schema.graphql](schema.graphql) | `internal/graph/schema/*.graphqls` (concatenated by `just docs`) | generated |
| Artifact and manifest format | [artifact-format.md](artifact-format.md) (hand-written) | `internal/pipeline` (task 3.1, D-144) | written; offline tools (`bekup verify`, `decrypt`, `reindex`, `keys export-public`, task 4.7) implemented |
