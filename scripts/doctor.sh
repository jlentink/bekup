#!/usr/bin/env bash
# doctor.sh: check that this machine has every tool bekup development needs,
# at a supported version (`just doctor`). Exits non-zero when a required tool
# is missing or too old; optional tools and environment checks only warn.
#
# Keep the tables below in sync when a task introduces a new tool.
set -uo pipefail

if [[ -t 1 && -z "${NO_COLOR:-}" ]] || [[ -n "${FORCE_COLOR:-}" ]]; then
	OK=$'\e[32m✓\e[0m' BAD=$'\e[31m✗\e[0m' WARN=$'\e[33m!\e[0m' DIM=$'\e[2m' B=$'\e[1m' R=$'\e[0m'
else
	OK='ok' BAD='XX' WARN='!!' DIM='' B='' R=''
fi

os=$(uname -s)

# version_ge <have> <want>: numeric dotted-version comparison (1.10 > 1.9).
version_ge() {
	local i
	local -a have want
	IFS=. read -r -a have <<<"$1"
	IFS=. read -r -a want <<<"$2"
	for ((i = 0; i < ${#want[@]}; i++)); do
		local h=${have[i]:-0} w=${want[i]:-0}
		((10#$h > 10#$w)) && return 0
		((10#$h < 10#$w)) && return 1
	done
	return 0
}

# first_version <text>: extract the first x.y[.z] number from tool output.
first_version() { grep -Eo '[0-9]+\.[0-9]+(\.[0-9]+)?' <<<"$1" | head -n1; }

failed=0
warned=0

# check <required|optional> <name> <min-version> <version command> <brew hint> <linux hint> <purpose>
check() {
	local level=$1 name=$2 min=$3 cmd=$4 brew=$5 linux=$6 purpose=$7
	local hint=$linux out ver
	[[ $os == Darwin ]] && hint=$brew
	local bin=${cmd%% *}
	if ! command -v "$bin" >/dev/null 2>&1; then
		if [[ $level == required ]]; then
			printf '  %s %-15s %-12s %s\n' "$BAD" "$name" "missing" "${DIM}install: $hint${R}"
			failed=1
		else
			printf '  %s %-15s %-12s %s\n' "$WARN" "$name" "missing" "${DIM}optional, $purpose; install: $hint${R}"
			warned=1
		fi
		return
	fi
	out=$(eval "$cmd" 2>&1 | head -n3)
	ver=$(first_version "$out")
	if [[ -n $min && -n $ver ]] && ! version_ge "$ver" "$min"; then
		if [[ $level == required ]]; then
			printf '  %s %-15s %-12s %s\n' "$BAD" "$name" "$ver" "${DIM}need >= $min; upgrade: $hint${R}"
			failed=1
		else
			printf '  %s %-15s %-12s %s\n' "$WARN" "$name" "$ver" "${DIM}want >= $min; upgrade: $hint${R}"
			warned=1
		fi
		return
	fi
	printf '  %s %-15s %-12s %s\n' "$OK" "$name" "${ver:-?}" "${DIM}$purpose${R}"
}

# The pnpm version pinned by web/package.json "packageManager" wins over the default.
pnpm_min=11.0
if [[ -f web/package.json ]]; then
	pinned=$(grep -Eo '"packageManager": *"pnpm@[0-9.]+' web/package.json | grep -Eo '[0-9]+\.[0-9]+\.[0-9]+' || true)
	[[ -n $pinned ]] && pnpm_min=$pinned
fi
go_min=$(awk '$1 == "go" { print $2; exit }' go.mod 2>/dev/null)

printf '\n%sbekup doctor%s  %s(%s)%s\n\n' "$B" "$R" "$DIM" "$os" "$R"
printf '%sRequired%s\n' "$B" "$R"
check required just "1.44" "just --version" "brew install just" "https://just.systems/man/en/packages.html" "task runner"
check required go "${go_min:-1.27}" "go version" "brew install go" "https://go.dev/dl/" "backend toolchain (go.mod)"
check required git "2.34" "git --version" "brew install git" "apt install git" "VCS; >= 2.34 for SSH-signed tags"
check required docker "24.0" "docker version --format '{{.Client.Version}}'" "brew install --cask docker (or OrbStack/colima)" "https://docs.docker.com/engine/install/" "dump images, dev stack, testcontainers"
check required "docker compose" "2.20" "docker compose version --short" "ships with Docker Desktop" "apt install docker-compose-plugin" "dev/prod stacks"
check required node "22.0" "node --version" "brew install node@22 (or nvm/fnm)" "https://nodejs.org/ (or nvm/fnm)" "web build"
check required pnpm "$pnpm_min" "pnpm --version" "corepack enable pnpm" "corepack enable pnpm" "web package manager (pinned in web/package.json)"
check required golangci-lint "2.0" "golangci-lint version" "brew install golangci-lint" "https://golangci-lint.run/welcome/install/" "Go lint incl. gosec"
check required govulncheck "" "govulncheck -version | grep Scanner" "go install golang.org/x/vuln/cmd/govulncheck@latest" "go install golang.org/x/vuln/cmd/govulncheck@latest" "Go vulnerability scan"
check required osv-scanner "2.0" "osv-scanner --version" "brew install osv-scanner" "https://google.github.io/osv-scanner/installation/" "dependency vulnerability scan"
check required openssl "3.0" "openssl version" "brew install openssl" "apt install openssl" "dev secret generation"
check required sqlc "1.31" "go tool sqlc version" "pinned in go.mod (go tool sqlc)" "pinned in go.mod (go tool sqlc)" "SQL -> Go code generation (just gen)"

printf '\n%sOptional%s\n' "$B" "$R"
check optional git-cliff "2.0" "git-cliff --version" "brew install git-cliff" "cargo install git-cliff" "changelog for just bump"
check optional air "" "air -v" "go install github.com/air-verse/air@latest" "go install github.com/air-verse/air@latest" "native hot reload (dev stack runs it in a container)"
check optional mkcert "1.4" "mkcert -version" "brew install mkcert" "apt install mkcert" "local TLS for the reverse-proxy profiles"
check optional shellcheck "0.9" "shellcheck --version" "brew install shellcheck" "apt install shellcheck" "lint scripts/"
check optional actionlint "1.7" "actionlint -version" "brew install actionlint" "go install github.com/rhysd/actionlint/cmd/actionlint@latest" "lint .github/workflows (CI runs it)"
check optional zizmor "1.0" "zizmor --version" "brew install zizmor" "pipx install zizmor (or uvx zizmor)" "GitHub Actions security lint (CI runs it)"
# buildx is a docker plugin: check() only sees the docker binary, so a
# missing plugin is detected by its failing version command.
if command -v docker >/dev/null 2>&1 && docker buildx version >/dev/null 2>&1; then
	check optional "docker buildx" "0.12" "docker buildx version" "ships with Docker Desktop" "apt install docker-buildx-plugin" "image publishing (just publish)"
else
	hint="apt install docker-buildx-plugin"
	[[ $os == Darwin ]] && hint="ships with Docker Desktop"
	printf '  %s %-15s %-12s %s\n' "$WARN" "docker buildx" "missing" "${DIM}optional, image publishing (just publish); install: $hint${R}"
	warned=1
fi

printf '\n%sEnvironment%s\n' "$B" "$R"
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
	printf '  %s %-15s %s\n' "$OK" "docker daemon" "${DIM}reachable${R}"
else
	printf '  %s %-15s %s\n' "$BAD" "docker daemon" "${DIM}not reachable: start Docker Desktop/OrbStack/colima or check your user is in the docker group${R}"
	failed=1
fi
if [[ -n "$(git config --get user.signingkey 2>/dev/null)" ]]; then
	printf '  %s %-15s %s\n' "$OK" "git signing" "${DIM}user.signingkey set ($(git config --get gpg.format || echo openpgp))${R}"
else
	printf '  %s %-15s %s\n' "$WARN" "git signing" "${DIM}no user.signingkey: just bump creates signed commits/tags (git config user.signingkey ...)${R}"
	warned=1
fi

echo
if ((failed)); then
	printf '%s Required tools are missing or outdated; fix the %s items above.\n\n' "$BAD" "$BAD"
	exit 1
fi
if ((warned)); then
	printf '%s All required tools present; see the %s notes for optional improvements.\n\n' "$OK" "$WARN"
else
	printf '%s Everything is in place.\n\n' "$OK"
fi
