# Fix: release Trivy scan fails on golang.org/x/net CVE-2026-78661

## Context
The v0.0.17 release workflow (`.github/workflows/release.yml`, D-204) stopped at
the Trivy step: `usr/local/bin/bekup` embeds `golang.org/x/net v0.61.0`, which has
HIGH CVE-2026-78661 (`x/net/html` DoS), fixed in `v0.62.0`. The release policy
fails on any HIGH/CRITICAL with a fix, and `.trivyignore` is only for cases
where no fix can be used — so the right fix is the upgrade, not an exception.
(bekup's own code does not import `x/net/html`; it comes in through the module
graph, but Trivy judges the module version, and a fix exists.)

## Steps
1. Branch from up-to-date `main`: `feature/p3-fix-xnet-cve-2026-78661` (D-230).
2. `go get golang.org/x/net@v0.62.0 && go mod tidy`
   - Review the `go.mod`/`go.sum` diff: x/net 0.62.0 may raise
     `x/crypto`, `x/sys`, `x/text`, `x/term` minimums — accept those bumps.
   - `third_party/clipboard` has no x/net dependency; nothing to change there.
3. No `.trivyignore` entry, no decision entry needed (routine dependency
   update under the existing D-204 policy).
4. Commit: `fix(deps): bump golang.org/x/net to v0.62.0 (CVE-2026-78661)` —
   git-cliff generates the CHANGELOG from it at `just bump`.

## Verification
- `just lint`, `just sec` (govulncheck + licenses), `just test`.
- Local image scan: `just docker` then
  `trivy image --severity HIGH,CRITICAL --ignore-unfixed <local image>` → 0 findings
  (or `go version -m` on the built binary shows `golang.org/x/net v0.62.0`).
- After merge, the user runs `just bump patch` + `just release` (v0.0.18):
  tag `v0.0.17` is already pushed and must not be reused; agents never run
  bump/release.
