# Allow the database container on the proxy's network (own-proxy, join mode)

## Context

D-215 lets the app database be a container on a Docker network of this host
(`--db-location docker --db-network <net>`). If that network is the proxy's
network, setup refuses it in three places today:
- `Answers.validate` (`internal/setup/answers.go` ~728): "serve can join it only once"
- `State.validate` (`internal/setup/state.go` ~291): "db_network must not be the proxy network"
- the wizard's DB network input (`internal/setup/wizard/init.go` ~546)

There are two reasons for the refusal. The first is technical: the overlay
would attach serve to the same Docker network twice, once as `proxy` and
once as `database`, and compose fails on that. The second is a policy view
that a proxy network should not reach databases. You don't have a strong
view on the policy, so setup should accept this layout. If the database is
on the proxy's network, serve reaches it through the `proxy` network it
already joins, and only migrate and admin join that network in addition.

Scope:
- **Join mode only.** In link mode the network is the dedicated `bekup-proxy` /28 that the operator creates with a fixed ip-range for serve and the proxy only. The refusal stays there, with a clearer message.
- **Weak TLS:** you chose to allow every TLS choice in this layout. Setup shows the existing `require`/`disable` warnings plus one extra sentence: the reverse proxy itself is on that network.

## Changes

### 1. Overlay (`internal/setup/overlay.go`)
- Add `Overlays.DBOnProxy bool`. It means the database is on the proxy's network. It is only valid together with `DBNetwork` in join mode.
- `overlayDoc` when `DBOnProxy` is set:
  - migrate and admin get `networks: proxy: {}` with a comment: "YOUR proxy's network, where the database container is too (D-216)".
  - serve gets no `database` entry (it is already on `proxy`).
  - Top-level `networks:` keeps `backend: internal: true` and omits `database:` (no `BEKUP_DB_NETWORK`).
- `Any()` stays unchanged, because `DBNetwork` is also set.

### 2. Answers / state
- `answers.go` validate: in join mode with `a.DB.Network == a.ProxyNetwork`, accept the answers.
  - If the two names differ only in case, refuse them. Docker network names are case-sensitive, so this is almost certainly a typo.
  - Link mode keeps its refusal. The message changes to "the link network is only for serve and your proxy; put the database on its own network …".
- `answers.overlays()` and `state.overlays()`: `DBOnProxy = DBNetwork && join && DB.Network == ProxyNetwork`.
- `state.go` validate: allow `db_network == proxy_network` only when `proxy_network_mode == join` (exact match). A case-only difference, or any match in link mode, is still an error.
  - No new state field. The fact is derived from the two recorded names, so older state files keep working.
- `render.go` ~244: do not write `BEKUP_DB_NETWORK` when `DBOnProxy` is set.
- `check.go` `dbNetworkEnv`: when `DBOnProxy` is set, expect `BEKUP_DB_NETWORK` to be absent. If it is present, warn that it "has no effect: the database is on your proxy's network (BEKUP_PROXY_NETWORK)". `proxyNetworkEnv` already checks `BEKUP_PROXY_NETWORK`.
- Summary line in `setup.go` (~761) and the `database.go` weak-TLS path: when `DBOnProxy` is set, add the sentence about the shared network. Reuse the existing `DBTLSRequireWarning`/`DBTLSDisableWarning` plus one new constant `DBSharedProxyNetworkNote`. Show it in the wizard, the plain prompt (`prompt.go`) and the init report.

### 3. Wizard (`internal/setup/wizard/init.go` ~546)
- Drop the refusal for join mode. Keep it in link mode, comparing against `st.a.Link.Name`/default.
- When the answer equals the proxy network, show the shared-network note.
- `TestSetupWizardMatchesFlags`: add a case with `--db-network` equal to `--proxy-network`.

### 4. Lint
`expectedDocs(compose, ov)` already merges `overlayDoc`, so migrate and admin on `proxy` are expected and pass `lintProject`. A foreign service on `proxy` is already refused. Verify both with tests and don't change lint logic unless a test shows a gap.

### 5. Docs and decisions
- `docs/DECISIONS.md`: add **D-216 (proposed)**, "Database container may share the own proxy's network (join mode)". It amends D-215. The D-215 status becomes "proposed (amended by D-216)", and the index row is updated.
  - Record the security impact: the proxy can reach the database port directly, and every container on that network can ARP-spoof the database. verify-full and SCRAM channel binding mitigate this; with `require` or `disable` it is only warned.
  - Record the alternatives: refuse (status quo), refuse `disable`.
- `AGENTS.md`: in the D-215 Conventions item and the trust-boundary row, add that the database container may share the proxy network in join mode (D-216).
- `SECURITY.md`: update the "serve → app DB" boundary.
- `docs/installation/external-database.md` (and `compose.md` if it lists the overlay blocks): describe the shared layout and its override blocks.
- `docs/reference/cli/bekup_setup_init.md`: regenerate it with `just docs` if the flag help changes.

### 6. Tests
- `variants_test.go`:
  - `overlayCombos` adds `DBOnProxy` combinations (own-proxy, external DB, join, not link).
  - `TestOverlayDocs` asserts: serve has no `database`; migrate and admin are on `proxy`; there is no top-level `database`; `backend` is internal; the lint passes, and fails without the override.
- `database_test.go`: a variant of the compose-first init test with `Network == ProxyNetwork`. It checks the override contents, that `.env` has no `BEKUP_DB_NETWORK`, the state, and that `setup check` is clean. It also checks that a stray `BEKUP_DB_NETWORK` only warns.
- Answers/state validation tests: same name accepted in join mode; case-only difference refused; link-mode match refused.
- `wizard/wizard_test.go:715`: change the expectation to success in join mode, and add a link-mode refusal case.
- `composepin_test.go:54`: add `DBOnProxy` to the "all overlays" case if it applies.
- The `docker compose config` integration case for the new overlay (per the D-215 rule for new overlay shapes).

## Verification
- `go test ./internal/setup/...`, then `just lint`, `just sec` and `just test`. Run the integration tests with `DOCKER_API_VERSION=1.47`.
- Manual check: `bekup setup init --variant own-proxy --proxy-network web --proxy-container traefik --database external --db-location docker --db-network web …` into a temp directory, then:
  - `docker compose config` resolves;
  - `bekup setup check` is clean;
  - in the override, migrate and admin join `proxy`, and serve has no second network.
- End of milestone: a security review agent and a code review agent, briefed with the code. Don't commit; you commit.
