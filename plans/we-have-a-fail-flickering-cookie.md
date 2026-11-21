# Fix: `TestSchemaDump` fails on main (schema.sql missing `users.locale`)

## Context

The Go integration job fails on `main`, so the devel workflow (D-239) ran on
the merge of PR #6 but correctly published nothing (build/scan/publish
skipped). The only failing test is:

```
--- FAIL: TestSchemaDump  internal/db/db_integration_test.go:557:
    internal/db/schema.sql is out of date with the migrations; run `just db-schema`
```

Cause: migration `internal/db/migrations/00045_user_locale.sql` (task 8.2,
commit `58ebe8b`) adds `users.locale text` + `CONSTRAINT users_locale_known
CHECK (locale IN ('en','nl'))`, but the commit did not regenerate
`internal/db/schema.sql` (last regenerated in `fb20463`, migration 00044).
`TestSchemaDump` pg_dumps a database migrated with all migrations and
compares it byte for byte with the committed file. It has failed on every
CI run since (PRs #4, #5, #6). The 8.1 failure was a different, unrelated
flake (`TestIntegrationExecutorIsolation`: Docker "Pool overlaps").

Intended outcome: green integration job on `main`, after which the next
push to `main` publishes `:devel` / `:develop`.

## Approach

1. Branch `feature/p3-fix-schema-sql` from an up-to-date `origin/main`
   (own branch per change, D-230). The worktree `../bekop-db-devel`
   (devel branch, merged as #6) can be reused or removed; the 8.3b1 tree
   in the main checkout is merged as #5.
2. Start Docker locally, then regenerate with the existing recipe:
   `DOCKER_API_VERSION=1.47 just db-schema`
   (`justfile` → `BEKUP_UPDATE_SCHEMA=1 go test -tags integration -run
   '^TestSchemaDump$' ./internal/db/`; the API pin is needed on this
   machine for testcontainers, see memory note).
3. Review the diff of `internal/db/schema.sql`: it must contain only the
   `locale text` column in `CREATE TABLE public.users` and the
   `users_locale_known` CHECK constraint. Anything else means a second
   drift and needs a look before committing.
4. No code, migration, decision or doc changes: `schema.sql` is generated
   ("DO NOT EDIT"), and the migration is already released on main
   (never edit a released migration).

## Files

- `internal/db/schema.sql` (generated, the only change)

## Verification

- `DOCKER_API_VERSION=1.47 go test -count=1 -tags integration -run '^TestSchemaDump$' ./internal/db/` passes.
- `DOCKER_API_VERSION=1.47 go test -count=1 -tags integration -p 1 ./internal/db/` passes (whole package, as CI's failing package).
- Hand-over: the user commits, opens the PR; the PR's CI "Go integration tests" job is green; after merge, the "Devel image" run on `main` gets past CI and the build/scan/publish jobs run, and `ghcr.io/jlentink/bekup:devel` / `:develop` resolve to the signed digest.

## Follow-up (not in this change; mention to the user)

- PRs were merged with a red CI. Making the CI jobs required status checks
  in the branch protection of `main` stops that (D-218 already expects it).
- The 8.1 `TestIntegrationExecutorIsolation` "Pool overlaps" failure is a
  separate flake (per-run network pool vs. the runner's Docker networks);
  investigate only if it recurs.
