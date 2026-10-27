# shellcheck shell=bash
# lib.bash: helpers sourced by justfile recipes (task 0.1b, D-071).

if [[ -t 2 && -z "${NO_COLOR:-}" ]]; then
	_c_red=$'\e[31m' _c_yel=$'\e[33m' _c_grn=$'\e[32m' _c_dim=$'\e[2m' _c_off=$'\e[0m'
else
	_c_red='' _c_yel='' _c_grn='' _c_dim='' _c_off=''
fi

info() { printf '%s==>%s %s\n' "$_c_grn" "$_c_off" "$*" >&2; }
note() { printf '%s  - %s%s\n' "$_c_dim" "$*" "$_c_off" >&2; }
warn() { printf '%swarning:%s %s\n' "$_c_yel" "$_c_off" "$*" >&2; }
die() {
	printf '%serror:%s %s\n' "$_c_red" "$_c_off" "$*" >&2
	exit 1
}

# not_yet <what> <task>: fail with a clear "not available yet" message.
not_yet() { die "$1: not available yet (task $2)"; }

# need_file <path> <task> [what]: fail unless <path> exists.
need_file() {
	[[ -e "$1" ]] || not_yet "${3:-$1}" "$2"
}

# need_cmd <command> <install hint>: fail unless <command> is on PATH.
need_cmd() {
	command -v "$1" >/dev/null 2>&1 || die "$1 is not installed. Install it with: $2"
}

# tool <name>: print how to run a Go tool, preferring a version pinned in
# go.mod (`go tool <name>`) over one found on PATH. Fails if neither exists.
tool() {
	if go tool -n "$1" >/dev/null 2>&1; then
		printf 'go tool %s' "$1"
	elif command -v "$1" >/dev/null 2>&1; then
		printf '%s' "$1"
	else
		return 1
	fi
}

# has_web: the web UI (task 0.8) exists.
has_web() { [[ -f web/package.json ]]; }

# web_pm: the web package manager, chosen by lockfile (pnpm-lock.yaml -> pnpm).
web_pm() {
	if [[ -f web/pnpm-lock.yaml ]]; then
		need_cmd pnpm "corepack enable pnpm (or brew install pnpm)"
		printf pnpm
	else
		need_cmd npm "https://nodejs.org/ (or nvm/fnm)"
		printf npm
	fi
}

# web_deps: install web dependencies strictly from the lockfile if missing.
web_deps() {
	local pm
	pm=$(web_pm)
	if [[ ! -d web/node_modules ]]; then
		info "installing web dependencies ($pm, frozen lockfile)"
		if [[ $pm == pnpm ]]; then
			pnpm --dir web install --frozen-lockfile
		else
			npm --prefix web ci
		fi
	fi
}

# web_run <script> [args...]: run a web/package.json script.
web_run() {
	local pm
	pm=$(web_pm)
	if [[ $pm == pnpm ]]; then
		pnpm --dir web run "$@"
	else
		npm --prefix web run "$@"
	fi
}

# web_has_script <name>: web/package.json defines the script <name>.
web_has_script() {
	has_web && node -e 'process.exit(require("./web/package.json").scripts?.[process.argv[1]] ? 0 : 1)' "$1"
}

# bekup_has <subcommand...>: the bekup CLI implements the given subcommand.
# Relies on cobra's "Usage:" help output (task 0.3).
bekup_has() {
	go run ./cmd/bekup "$@" --help 2>/dev/null | grep -q "bekup $*"
}

# read_version: print VERSION after validating it, so it is safe to use in
# commands and -ldflags.
read_version() {
	local v
	v=$(tr -d ' \t\r\n' <VERSION)
	scripts/semver.sh valid "$v" || die "VERSION '$v' is not a valid x.y.z version"
	printf '%s' "$v"
}

# --- native dev workflow (`just dev` / `just run`, D-114) --------------------

# dev_keys_dir: absolute path of the dev keys directory written by
# deploy/dev/gen-dev-keys.sh (BEKUP_DEV_KEYS_DIR, default ./secrets).
dev_keys_dir() {
	local k="${BEKUP_DEV_KEYS_DIR:-secrets}"
	[[ $k == /* ]] || k="$PWD/$k"
	printf '%s' "$k"
}

# dev_keys_ready: every file the default dev profile and a native bekup need
# exists, the local DB URLs use BEKUP_DEV_PG_PORT and the postgres
# certificate names 127.0.0.1 (D-114). Reads no secret contents except a
# port match on the local URL file (never printed).
dev_keys_ready() {
	local k l f
	k=$(dev_keys_dir)
	l="$k/local"
	for f in "$k/postgres_password" "$k/bekup_owner_password" "$k/bekup_app_password" \
		"$k/certs/postgres/postgres.crt" "$k/certs/postgres/postgres.key" "$k/certs/postgres/ca.crt" \
		"$l/bekup_database_url" "$l/bekup_database_owner_url" "$l/bekup_master_key" "$l/postgres_ca.crt"; do
		[[ -e $f ]] || return 1
	done
	grep -q "@127\.0\.0\.1:${BEKUP_DEV_PG_PORT:-5432}/bekup\$" "$l/bekup_database_url" || return 1
	cmp -s "$k/certs/postgres/ca.crt" "$l/postgres_ca.crt" || return 1
	openssl x509 -in "$k/certs/postgres/postgres.crt" -noout -ext subjectAltName 2>/dev/null |
		grep -q 'IP Address:127.0.0.1'
}

# dev_keys: run deploy/dev/gen-dev-keys.sh unless dev_keys_ready. Sets
# DEV_PG_CERT_CHANGED=1 when the postgres certificate changed (running
# containers keep the old bind-mounted file and must be re-created).
dev_keys() {
	DEV_PG_CERT_CHANGED=0
	dev_keys_ready && return 0
	local crt before after
	crt="$(dev_keys_dir)/certs/postgres/postgres.crt"
	before=$({ cksum <"$crt"; } 2>/dev/null || true)
	info "generating dev keys (deploy/dev/gen-dev-keys.sh)"
	deploy/dev/gen-dev-keys.sh
	after=$({ cksum <"$crt"; } 2>/dev/null || true)
	[[ $before == "$after" ]] || DEV_PG_CERT_CHANGED=1
}

# dev_native_env: environment for a bekup process on the host against the
# dev infrastructure: deploy/dev/bekup.local.toml plus absolute *_file paths
# into <keys>/local/. The owner URL is added only for migrations
# (dev_migrate), never for serve.
dev_native_env() {
	local l
	l="$(dev_keys_dir)/local"
	[[ -e "$l/bekup_database_url" && -e "$l/bekup_master_key" && -e "$l/postgres_ca.crt" ]] ||
		die "no dev keys for native use in ${l#"$PWD"/}: run 'just dev' first"
	export BEKUP_CONFIG="$PWD/deploy/dev/bekup.local.toml"
	export BEKUP_DATABASE_URL_FILE="$l/bekup_database_url"
	export BEKUP_DATABASE_TLS_CA_FILE="$l/postgres_ca.crt"
	export BEKUP_SECURITY_MASTER_KEY_FILE="$l/bekup_master_key"
	unset BEKUP_DATABASE_OWNER_URL_FILE BEKUP_DATABASE_OWNER_URL BEKUP_DATABASE_URL BEKUP_SECURITY_MASTER_KEY
	# The BEKUP_DEV_* compose/script variables (ports, keys dir) are no bekup
	# configuration keys: keep their values in this shell but do not pass
	# them on (bekup warns about unknown BEKUP_* variables).
	# BEKUP_DEV_ENABLED is the real [dev] enabled key.
	local v
	for v in $(compgen -e); do
		if [[ $v == BEKUP_DEV_* && $v != BEKUP_DEV_ENABLED ]]; then
			export -n "$v"
		fi
	done
}

# dev_build: build the native dev binary bin/bekup-dev (gitignored).
dev_build() {
	info "go build bin/bekup-dev"
	go build -trimpath -o bin/bekup-dev ./cmd/bekup
}

# dev_migrate: apply the migrations as bekup_owner with bin/bekup-dev
# (call dev_native_env and dev_build first).
dev_migrate() {
	info "bekup migrate up (bekup_owner, verify-full via 127.0.0.1:${BEKUP_DEV_PG_PORT:-5432})"
	BEKUP_DATABASE_OWNER_URL_FILE="$(dev_keys_dir)/local/bekup_database_owner_url" bin/bekup-dev migrate up
}
