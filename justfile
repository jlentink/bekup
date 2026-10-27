# bekup task runner: the single entry point for every recurring task
# (D-020, D-071, task 0.1b). Run `just` for the grouped help screen.
#
# Recipes whose tooling lands in a later task fail with a clear
# "not available yet (task X.Y)" message. Shared helpers: scripts/lib.bash.

set shell := ["bash", "-euo", "pipefail", "-c"]
set script-interpreter := ["bash", "-euo", "pipefail"]
# Recipe parameters reach scripts as "$1"... and are never interpolated into
# shell source, so arguments cannot inject commands.
set positional-arguments

# Tools installed with `go install` (govulncheck, ...) live in $(go env GOPATH)/bin,
# which is often not on the user's PATH.
export PATH := `go env GOPATH` + "/bin:" + env("PATH")

compose_dev := "docker compose -f docker-compose.yml -f docker-compose.dev.yml"
lib := "source scripts/lib.bash"

# ---------------------------------------------------------------------------
# dev
# ---------------------------------------------------------------------------

[doc('Show this help screen (default)')]
[group('dev')]
help:
    @JUST="{{ just_executable() }}" JUSTFILE="{{ justfile() }}" scripts/help.sh || [ $? -eq 141 ] # 141: output piped into e.g. head

[doc('Check this machine has every required dev tool at a supported version')]
[group('dev')]
doctor:
    @scripts/doctor.sh

[doc('Start the dev infra on 127.0.0.1 (app Postgres with TLS, mailpit, adminer) and migrate')]
[group('dev')]
[script]
dev:
    {{ lib }}
    need_cmd docker "https://docs.docker.com/get-docker/"
    need_cmd openssl "brew install openssl (or apt install openssl)"
    dev_keys
    recreate=()
    if (( DEV_PG_CERT_CHANGED )); then
        recreate=(--force-recreate)
    fi
    info "starting postgres, mailpit and adminer"
    {{ compose_dev }} up --detach --wait ${recreate[@]+"${recreate[@]}"} postgres mailpit adminer
    dev_native_env
    dev_build
    dev_migrate
    keys=$(dev_keys_dir)
    keys=${keys#"$PWD"/}
    cat <<EOF

    Dev infrastructure is running (all on 127.0.0.1):
      app Postgres  127.0.0.1:${BEKUP_DEV_PG_PORT:-5432}  TLS only (verify-full with $keys/local/postgres_ca.crt)
      adminer       http://127.0.0.1:${BEKUP_DEV_ADMINER_PORT:-8081}  System PostgreSQL, server postgres, database bekup,
                    user bekup_app (password: $keys/bekup_app_password) or
                    bekup_owner ($keys/bekup_owner_password); the superuser is socket-only
      mailpit       http://127.0.0.1:${BEKUP_DEV_MAILPIT_PORT:-8025}  (SMTP 127.0.0.1:${BEKUP_DEV_SMTP_PORT:-1025})

    Next steps:
      just admin-create you@example.com "Your Name"   # first admin (password prompted)
      just run                                         # bekup serve on http://localhost:8080
      just web-dev                                     # optional: Vite hot reload on http://localhost:5173
      just dev-down                                    # stop (data is kept; just db-reset wipes it)
    EOF

[doc('Run bekup serve natively in dev mode against the dev infra (migrates first)')]
[group('dev')]
[script]
run:
    {{ lib }}
    dev_native_env
    dev_build
    dev_migrate
    info "bekup serve on http://localhost:8080 (dev mode; the plain-HTTP warning is expected, D-095)"
    exec bin/bekup-dev serve

[doc('Apply the migrations natively as bekup_owner against the dev infra')]
[group('dev')]
[script]
dev-migrate:
    {{ lib }}
    dev_native_env
    dev_build
    dev_migrate

[doc('Create an admin account on the dev infra (password prompted on the terminal)')]
[group('dev')]
[script]
admin-create email display_name:
    {{ lib }}
    dev_native_env
    dev_build
    exec bin/bekup-dev admin create --email="$1" --display-name="$2"

[doc('Start the Vite dev server (hot reload) against bekup serve on :8080')]
[group('dev')]
[script]
web-dev:
    {{ lib }}
    has_web || not_yet "the web UI" "0.8"
    web_deps
    BEKUP_DEV_BACKEND="${BEKUP_DEV_BACKEND:-http://localhost:8080}" web_run dev

[doc('Start the full containerised dev stack (air serve, runner, sample DBs, minio, sftp)')]
[group('dev')]
[script]
dev-full:
    {{ lib }}
    need_cmd docker "https://docs.docker.com/get-docker/"
    need_cmd openssl "brew install openssl (or apt install openssl)"
    dev_keys
    {{ compose_dev }} --profile full up --build --remove-orphans

[doc('Stop every dev container (all profiles); volumes are kept')]
[group('dev')]
[script]
dev-down:
    {{ lib }}
    need_cmd docker "https://docs.docker.com/get-docker/"
    {{ compose_dev }} --profile full --profile web --profile traefik --profile nginx down --remove-orphans

[doc('Run all code generators (sqlc, gqlgen, graphql-codegen, config reference)')]
[group('dev')]
[script]
gen:
    {{ lib }}
    ran=0
    if [[ -d internal/config/gendocs ]]; then
        info "config reference (docs/configuration.md)"
        go run ./internal/config/gendocs
        ran=1
    fi
    if [[ -f sqlc.yaml ]]; then
        info "sqlc generate"
        sqlc=$(tool sqlc) || die "sqlc not found (pin it with: go get -tool github.com/sqlc-dev/sqlc/cmd/sqlc)"
        $sqlc generate
        ran=1
    else
        note "sqlc: not available yet (task 0.4)"
    fi
    if [[ -f gqlgen.yml ]]; then
        info "gqlgen generate"
        go tool gqlgen generate
        ran=1
    else
        note "gqlgen: not available yet (task 1.2)"
    fi
    if web_has_script gen; then
        info "graphql-codegen"
        web_deps
        web_run gen
        ran=1
    else
        note "graphql-codegen: not available yet (task 0.8)"
    fi
    (( ran )) || not_yet "code generation" "0.4 / 0.8 / 1.2"

# ---------------------------------------------------------------------------
# quality
# ---------------------------------------------------------------------------

[doc('Lint Go (golangci-lint incl. gosec) and, if present, web (eslint + tsc)')]
[group('quality')]
[script]
lint:
    {{ lib }}
    need_cmd golangci-lint "brew install golangci-lint (https://golangci-lint.run/welcome/install/)"
    info "golangci-lint"
    golangci-lint run --build-tags integration ./...
    if [[ -f sqlc.yaml ]]; then
        sqlc=$(tool sqlc) || die "sqlc not found (pin it with: go get -tool github.com/sqlc-dev/sqlc/cmd/sqlc)"
        info "sqlc vet"
        $sqlc vet
        info "sqlc diff (generated code is up to date)"
        $sqlc diff || die "internal/db/sqlc is out of date: run 'just gen' and commit the result"
    else
        note "sqlc vet/diff: not available yet (task 0.4)"
    fi
    if has_web; then
        web_deps
        info "eslint"
        web_run lint
        info "tsc --noEmit"
        web_run typecheck
    else
        note "web lint (eslint, tsc): not available yet (task 0.8)"
    fi

[doc('Run unit tests: Go (race detector), script tests and, if present, vitest')]
[group('quality')]
[script]
test: test-scripts
    {{ lib }}
    info "go test"
    go test -race -count=1 ./...
    if has_web; then
        web_deps
        info "vitest"
        web_run test
    else
        note "vitest: not available yet (task 0.8)"
    fi

[doc('Test the release helper scripts (semver math, bump refusals and happy path)')]
[group('quality')]
test-scripts:
    @scripts/semver_test.sh
    @scripts/bump_test.sh

[doc('Run integration tests (testcontainers; needs Docker)')]
[group('quality')]
[script]
test-int:
    {{ lib }}
    if ! grep -rqsl --include='*_test.go' -E '^//go:build .*integration' cmd internal; then
        not_yet "integration tests" "0.4"
    fi
    need_cmd docker "https://docs.docker.com/get-docker/"
    go test -race -count=1 -tags integration -timeout 30m ./...

[doc('Run Playwright end-to-end tests against the compose stack')]
[group('quality')]
[script]
test-e2e:
    {{ lib }}
    need_file web/playwright.config.ts 7.2 "the E2E suite"
    web_deps
    web_run test:e2e

# ---------------------------------------------------------------------------
# security
# ---------------------------------------------------------------------------

[doc('Scan dependencies for known vulnerabilities (govulncheck, osv-scanner)')]
[group('security')]
[script]
sec:
    {{ lib }}
    missing=()
    if govulncheck=$(tool govulncheck); then
        info "govulncheck"
        $govulncheck ./...
    else
        missing+=("govulncheck: go install golang.org/x/vuln/cmd/govulncheck@latest (or brew install govulncheck)")
    fi
    if command -v osv-scanner >/dev/null 2>&1; then
        info "osv-scanner"
        rc=0
        osv-scanner scan source --recursive . || rc=$?
        # 128 = no package sources found (nothing to scan), not a failure.
        if (( rc == 128 )); then
            note "osv-scanner: no lockfiles found"
        elif (( rc != 0 )); then
            exit "$rc"
        fi
    else
        missing+=("osv-scanner: brew install osv-scanner (https://google.github.io/osv-scanner/installation/)")
    fi
    if (( ${#missing[@]} )); then
        printf '%s\n' "${missing[@]/#/  install }" >&2
        die "security scanners missing; 'just sec' cannot pass without them"
    fi

# ---------------------------------------------------------------------------
# build
# ---------------------------------------------------------------------------

[doc('Build the web UI (if present) and the static bekup binary into bin/')]
[group('build')]
[script]
build:
    {{ lib }}
    version=$(read_version)
    if has_web; then
        web_deps
        info "web build"
        web_run build
    else
        note "web build: not available yet (task 0.8)"
    fi
    info "go build bekup ${version}"
    mkdir -p bin
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${version}" -o bin/bekup ./cmd/bekup
    info "built bin/bekup"

[doc('Build the bekup container image (tagged bekup:<version> and bekup:dev)')]
[group('build')]
[script]
docker:
    {{ lib }}
    need_file Dockerfile 0.7
    need_cmd docker "https://docs.docker.com/get-docker/"
    version=$(read_version)
    docker build --pull --build-arg "VERSION=${version}" -t "bekup:${version}" -t bekup:dev .

# ---------------------------------------------------------------------------
# db
# ---------------------------------------------------------------------------

[confirm('This deletes ALL dev stack volumes (app DB, sample DBs, minio). Continue?')]
[doc('Destroy and recreate the dev stack data, then apply all migrations')]
[group('db')]
[script]
db-reset:
    {{ lib }}
    need_cmd docker "https://docs.docker.com/get-docker/"
    {{ compose_dev }} --profile full --profile web --profile traefik --profile nginx down --volumes --remove-orphans
    dev_keys
    {{ compose_dev }} up --detach --wait postgres mailpit adminer
    # Natively, through the published loopback port (D-114).
    dev_native_env
    dev_build
    dev_migrate

[doc('Regenerate internal/db/schema.sql (pg_dump of all migrations in a test container)')]
[group('db')]
[script]
db-schema:
    {{ lib }}
    need_file internal/db/migrations 0.4 "migrations"
    need_cmd docker "https://docs.docker.com/get-docker/"
    BEKUP_UPDATE_SCHEMA=1 go test -count=1 -tags integration -run '^TestSchemaDump$' ./internal/db/
    info "updated internal/db/schema.sql; review the diff"

[doc('Create a new goose migration: internal/db/migrations/NNNNN_<name>.sql')]
[group('db')]
[script]
migrate-new name:
    {{ lib }}
    name="$1"
    [[ "$name" =~ ^[a-z][a-z0-9_]{0,62}$ ]] || die "migration name must match ^[a-z][a-z0-9_]{0,62}$ (got '$name')"
    dir=internal/db/migrations
    need_file "$dir" 0.4 "the migrations directory"
    last=$(find "$dir" -maxdepth 1 -name '[0-9][0-9][0-9][0-9][0-9]_*.sql' -exec basename {} \; | sort | tail -n1 | cut -c1-5)
    next=$(printf '%05d' $(( 10#${last:-0} + 1 )))
    file="$dir/${next}_${name}.sql"
    [[ ! -e "$file" ]] || die "$file already exists"
    printf -- '-- +goose Up\n-- +goose StatementBegin\n\n-- +goose StatementEnd\n\n-- +goose Down\n-- +goose StatementBegin\n\n-- +goose StatementEnd\n' >"$file"
    info "created $file"

# ---------------------------------------------------------------------------
# keys
# ---------------------------------------------------------------------------

[doc('Generate the master keyring file (256-bit key, 0600, never overwritten)')]
[group('keys')]
[script]
keygen out="secrets/bekup_master_key":
    {{ lib }}
    out="$1"
    bekup_has keys generate || not_yet "bekup keys generate" "0.5"
    [[ ! -e "$out" ]] || die "$out already exists; refusing to overwrite a key"
    (umask 077 && mkdir -p "$(dirname -- "$out")")
    go run ./cmd/bekup keys generate --out "$out"

[doc('Create the private CA and per-component mTLS certificates (extra args go to `bekup certs init`)')]
[group('keys')]
[script]
certs dir="secrets/certs" *args:
    {{ lib }}
    dir="$1"
    shift
    bekup_has certs init || not_yet "bekup certs init" "0.6"
    (umask 077 && mkdir -p "$dir")
    go run ./cmd/bekup certs init --dir "$dir" "$@"

# ---------------------------------------------------------------------------
# docs
# ---------------------------------------------------------------------------

[doc('Regenerate generated docs (config key reference, CLI reference from cobra, GraphQL schema)')]
[group('docs')]
[script]
docs:
    {{ lib }}
    info "config reference (docs/configuration.md)"
    go run ./internal/config/gendocs
    if bekup_has docs cli; then
        info "CLI reference"
        go run ./cmd/bekup docs cli --dir docs/reference/cli
    else
        note "CLI reference: not available yet (task 7.5: needs a hidden 'bekup docs cli' command using cobra/doc)"
    fi
    shopt -s nullglob
    schemas=(internal/graph/schema/*.graphqls)
    if (( ${#schemas[@]} )); then
        info "GraphQL schema"
        cat "${schemas[@]}" >docs/reference/schema.graphql
    else
        note "GraphQL schema: not available yet (task 1.2)"
    fi

# ---------------------------------------------------------------------------
# release
# ---------------------------------------------------------------------------

[doc('Bump VERSION (patch|minor|major|x.y.z), regenerate CHANGELOG, signed commit + tag')]
[group('release')]
bump level="patch":
    @scripts/bump.sh "$1"

[confirm('Push main and the release tag to origin? This triggers the release workflow.')]
[doc('Publish the release: verify the signed tag, push main + tag atomically')]
[group('release')]
release:
    @scripts/release.sh origin
