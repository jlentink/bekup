## What and why

<!-- What does this change do, and why? Link the plan task (e.g. "task 2.5") and issues. -->

## Checklist (global acceptance criteria, see AGENTS.md)

- [ ] Unit tests cover the new logic (integration tests for DB/Docker/destination code)
- [ ] `just lint` is green (golangci-lint incl. gosec; eslint + tsc for web changes)
- [ ] `just test` is green (and `just test-int` if integration code changed)
- [ ] `just sec` is green (govulncheck, osv-scanner)
- [ ] `just gen` was run and the generated code is committed
- [ ] No secrets in logs, errors, fixtures or test output
- [ ] Authz matrix updated for every new or changed GraphQL operation
- [ ] Every decision is recorded in `docs/DECISIONS.md` (+ summary in AGENTS.md)
- [ ] The relevant `docs/` page is updated for user-visible changes
- [ ] `SECURITY.md` is updated if a trust boundary changed
- [ ] New GitHub Actions are pinned by full commit SHA with a `# vX.Y.Z` comment

## Security notes

<!-- Anything a reviewer should look at closely: new inputs, trust boundaries, crypto, authz, SSRF, file paths. Write "none" if not applicable. -->
