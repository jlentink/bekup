# Plan: get the first GHCR release out (CI + release workflow fixes)

## Context

- **The symptom.** The user ran the README quick start, and `docker pull ghcr.io/jlentink/bekup:latest` returned `denied`. No bekup image exists on GHCR yet: the v0.0.1 and v0.0.2 release workflows failed, and v0.0.3 is still running (and will hit the same release bug). The later `no configuration file provided` errors only follow from setup never running.
- **The causes, from the GitHub Actions logs:**
  1. **Release `verify` job:** "v0.0.2 points at a commit that is not on main". `actions/checkout` (fetch-depth 0) on a tag push does not create `refs/remotes/origin/main`, so `git merge-base --is-ancestor "$GITHUB_SHA" refs/remotes/origin/main` fails although the tag *is* on main (checked locally for v0.0.3). This blocks every release.
  2. **CI Go job:** `internal/setup` unit tests hit the default 10-minute `go test` timeout on the runner (about 6 minutes locally; most of it is the age scrypt sealing of the CA key, work factor 18, about 256 MiB per run, in many tests).
  3. **CI integration job** (run 41198901570, before the latest fixes):
     - the five tests fixed since (TestDBBootstrapCLI, TestServiceCRUD, TestLocationChangeRefused, TestDestinationsS3Minio, TestUnrecordedFilesGraphQL);
     - Docker Hub `toomanyrequests` on image pulls (TestConnectionsGraphQLWithRunner, TestRestoreArtifactEndToEnd);
     - `TestInnerRunnerRPCs/roundtrip/mysql84` failed: cause unknown, to investigate.
- **Still running:** the v0.0.3 CI run (41215766054), which will show what remains after the latest fixes.

## Fixes

1. **Release workflow** (`.github/workflows/release.yml`, `verify` job):
   - before the ancestry check, fetch main explicitly with the token, the same way as the existing tag-object fetch: `git fetch --no-tags origin +refs/heads/main:refs/remotes/origin/main`, through the `GIT_CONFIG_*` env, never argv;
   - keep the check strict;
   - add a `release_ci_test.sh` case if the logic moves into the script;
   - actionlint plus zizmor.
2. **Slow setup tests:**
   - add a test-only hook so tests seal the CA key with a low scrypt work factor. `internal/certs`/`crypto` take the work factor from an internal variable, set lower only in `_test.go` via an `export_test.go`; production is unchanged, and a test asserts the production default;
   - parallelize independent setup tests (`t.Parallel()` where safe);
   - also set an explicit `-timeout 20m` for `go test` in the justfile (`just test`) and in the CI Go job, as a safety margin;
   - target: `internal/setup` well under 2 minutes locally.
3. **Docker Hub rate limits in CI:**
   - in the CI integration job (and the setup-install job), configure the Docker daemon with the pull-through mirror `mirror.gcr.io` (`/etc/docker/daemon.json` `registry-mirrors`, restart docker). All test images are digest-pinned, so the mirror cannot substitute content;
   - make the test image-pull helpers share one retry-with-backoff on rate-limit errors (`internal/testutil`). Today only one helper in `internal/graph` retries;
   - document it in `docs/development.md`.
4. **`roundtrip/mysql84`:** read the failure output of run 41198901570 and the v0.0.3 run. Fix the cause if it's real, or harden the test if it's timing or rate-limit related (with evidence).
5. **Whatever else the v0.0.3 CI run shows** once it finishes: triage and fix in the same round.
6. **Release v0.0.4:** release tags are immutable and the v0.0.3 workflow fails at `verify`. After these fixes are committed and pushed (by the user), the user runs `just bump patch` + `just release`. When the release workflow succeeds, make the GHCR package `bekup` public in its GitHub package settings (once), then the README quick start works.
7. **Until then:** the user can test the quick start with an own image: `just publish` to a registry, then `IMAGE=<that repo>@sha256:…` or pinned digest mode.

## Verification

- `just lint`, `just test` (with the new timeout; record the `internal/setup` duration), `just test-scripts`, actionlint/zizmor, the integration packages touched (`DOCKER_API_VERSION=1.47`, `-p 1`).
- After the user's push: the CI run is green; after `just bump patch` + `just release`: the release workflow is green, and `docker pull ghcr.io/jlentink/bekup:latest` works once the package is public.
- No commits or pushes by me.
