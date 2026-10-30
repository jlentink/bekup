#!/usr/bin/env bash
# Lints every shell script of the repository with shellcheck
# (`just lint` and the CI `scripts` job run the same thing).
#
# Uses the shellcheck on PATH when it is exactly SHELLCHECK_VERSION, else
# the official image pinned by digest (no network, read-only mount), so
# local results match CI. Runs from the repository root with -x (follows
# `source` directives; paths in `# shellcheck source=` are repo-relative).
set -euo pipefail

# The single shellcheck pin (Renovate updates tag and digest together);
# scripts/doctor.sh reads it too.
# renovate: image=koalaman/shellcheck:v0.9.0
shellcheck_image=koalaman/shellcheck@sha256:f35e8987b02760d4e76fc99a68ad5c42cc10bb32f3dd2143a3cf92f1e5446a45

cd "$(dirname -- "$0")/.."
SHELLCHECK_VERSION=$(sed -n 's/^# renovate: image=koalaman\/shellcheck:v\([0-9.]*\)$/\1/p' scripts/shellcheck.sh)
[[ -n $SHELLCHECK_VERSION ]] || {
	echo "shellcheck.sh: no version pin found" >&2
	exit 1
}

# The file list: scripts/*.sh, scripts/lib.bash and every *.sh below deploy/
# (bash 3.2 compatible: no globstar, no mapfile).
files=()
while IFS= read -r f; do
	files+=("$f")
done < <({
	find scripts -maxdepth 1 -type f -name '*.sh'
	echo scripts/lib.bash
	find deploy -type f -name '*.sh'
} | LC_ALL=C sort -u)
((${#files[@]} > 0)) || {
	echo "shellcheck.sh: no scripts found" >&2
	exit 1
}

local_version=""
if command -v shellcheck >/dev/null 2>&1; then
	local_version=$(shellcheck --version | awk '$1 == "version:" { print $2 }')
fi

if [[ $local_version == "$SHELLCHECK_VERSION" ]]; then
	exec shellcheck -x "${files[@]}"
fi

if ! command -v docker >/dev/null 2>&1; then
	echo "shellcheck.sh: shellcheck $SHELLCHECK_VERSION is needed (found: ${local_version:-none}) and docker is not available to run $shellcheck_image" >&2
	exit 1
fi
if [[ -n $local_version ]]; then
	echo "shellcheck.sh: local shellcheck is $local_version, CI uses $SHELLCHECK_VERSION: running the pinned image" >&2
fi
exec docker run --rm --network none --read-only --cap-drop ALL \
	--security-opt no-new-privileges --user "$(id -u):$(id -g)" \
	-v "$PWD:/mnt:ro" -w /mnt "$shellcheck_image" -x "${files[@]}"
