#!/usr/bin/env bash
# gen_compose.sh: write the committed compose files (D-205 amendment 3)
# from docker-compose.yml with `bekup setup compose --variant <v>
# --database <bundled|external>` (no --image: the x-bekup-image line takes
# the image from BEKUP_IMAGE). Six files: three proxy variants, each with
# the bundled database and with your own PostgreSQL (D-208).
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
for spec in \
	traefik:bundled:compose.yaml \
	traefik-external:bundled:compose.traefik-external.yaml \
	npm:bundled:compose.npm.yaml \
	traefik:external:compose.external-db.yaml \
	traefik-external:external:compose.traefik-external.external-db.yaml \
	npm:external:compose.npm.external-db.yaml; do
	variant=${spec%%:*} rest=${spec#*:}
	database=${rest%%:*} name=${rest#*:}
	"$tmp/bekup" setup compose --variant "$variant" --database "$database" >"$tmp/$name"
	chmod 0644 "$tmp/$name"
	mv -f "$tmp/$name" "$out/$name"
done
