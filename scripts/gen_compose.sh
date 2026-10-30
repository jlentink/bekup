#!/usr/bin/env bash
# gen_compose.sh: write the committed compose files (D-205 amendment 3)
# from docker-compose.yml with `bekup setup compose --variant <v>` (no
# --image: the x-bekup-image line takes the image from BEKUP_IMAGE).
#
# Usage: scripts/gen_compose.sh <dir>     (`just gen`: deploy/compose)
#
# The files are generated, never edited by hand; the setup drift test
# (internal/setup TestCommittedComposeFiles) and CI (`just gen` + no diff)
# fail until they are regenerated. Each file is written to a temporary
# name first and renamed, so a failed render never leaves a partial file.
set -euo pipefail

[[ $# -eq 1 ]] || {
	echo "usage: scripts/gen_compose.sh <dir>" >&2
	exit 2
}
out=$1
cd "$(dirname -- "$0")/.."
mkdir -p "$out"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
go build -o "$tmp/bekup" ./cmd/bekup
for pair in traefik:compose.yaml traefik-external:compose.traefik-external.yaml npm:compose.npm.yaml; do
	variant=${pair%%:*} name=${pair#*:}
	"$tmp/bekup" setup compose --variant "$variant" >"$tmp/$name"
	chmod 0644 "$tmp/$name"
	mv -f "$tmp/$name" "$out/$name"
done
