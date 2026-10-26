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
