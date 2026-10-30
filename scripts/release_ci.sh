#!/bin/sh
# release_ci.sh: the testable logic of the release workflow
# (.github/workflows/release.yml, task 7.4, D-204).
#
# Usage:
#   release_ci.sh check-tag <tag> [version-file]
#       The tag must be v<x.y.z> and equal v$(cat VERSION). Prints x.y.z.
#   release_ci.sh is-latest <x.y.z>
#       Reads tag names from stdin (`git tag -l`). Prints "true" when x.y.z
#       is at least as high as every released version (plain vX.Y.Z tags;
#       pre-releases and anything else are ignored), else "false".
#   release_ci.sh verify-signer <fingerprint>
#       Reads `git verify-tag --raw` status lines (GnuPG) from stdin and
#       requires exactly one good, valid signature (SHA-2 hash) whose primary
#       key fingerprint equals <fingerprint> (40 hex digits).
#   release_ci.sh changelog-section <x.y.z> [changelog]
#       Prints the body of the "## [x.y.z]" section of CHANGELOG.md.
#   release_ci.sh image-repo <owner/repo>
#       Prints the GHCR repository ghcr.io/<owner/repo, lowercased>.
#   release_ci.sh release-notes <x.y.z> <image-repo> <digest> <repo-url> [changelog]
#       Prints the GitHub release notes: the changelog section, the pinned
#       image, pull/verify commands and the compose-first install (verify
#       the compose file under its release name, save it as compose.yaml,
#       the three steps, the link network of the external variants).
#   release_ci.sh sbom-generator [publish.sh]
#       Prints the digest-pinned SBOM generator (single pin in publish.sh,
#       D-202).
#   release_ci.sh tag-state <repo:x.y.z>
#       Prints "exists" or "absent" for an image tag (docker buildx imagetools
#       inspect; $RELEASE_CI_DOCKER replaces docker in tests). Only a
#       registry "not found" (manifest/name unknown, 404) counts as absent;
#       any other error (auth, network, rate limit) fails: the immutability
#       check of version tags fails closed.
#   release_ci.sh tag-object-name
#       Reads `git cat-file tag <ref>` from stdin and prints the tag name
#       recorded in the annotated tag object (header `tag` line).
#   release_ci.sh render-compose <repo:x.y.z@sha256:digest> <dir>
#       Renders the release compose files (D-205) into <dir> by running the
#       published image itself (`bekup setup compose`) with no network, a
#       read-only root, UID 65532 and no capabilities ($RELEASE_CI_DOCKER
#       replaces docker in tests), then checks them with compose-assets.
#   release_ci.sh compose-assets <repo:x.y.z@sha256:digest> <dir>
#       Checks the compose files in <dir>: each of compose.yaml,
#       compose.traefik-external.yaml and compose.npm.yaml exists, has
#       exactly one x-bekup-image line naming exactly the release reference,
#       no ${BEKUP_IMAGE} left, no build:, and every image: line pinned by
#       digest.
#   release_ci.sh committed-compose <dir> <committed-dir>
#       Checks that the committed compose files in <committed-dir>
#       (deploy/compose/, rendered by `just gen`, D-205 amendment 3) equal
#       the release files in <dir> except for the x-bekup-image line, which
#       must be exactly the BEKUP_IMAGE placeholder there: the tag's
#       committed files are the release assets without the pinned image.
#   release_ci.sh assets-digest <dir>
#       Prints one SHA-256 over the sha256sum lines of the release assets in
#       <dir> (the three compose files, SHA256SUMS and their .sigstore.json
#       bundles; each must be a regular file). The compose job publishes it
#       as a job output and the release job recomputes it before uploading.
#
# Every input is validated against a strict pattern before it is printed,
# so nothing from a tag name or a file can inject text into the notes or
# the workflow outputs.
set -eu

here=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
semver="$here/semver.sh"

die() {
	printf 'release_ci: %s\n' "$*" >&2
	exit 1
}

# one_line <value>: exit 0 if the value has no newline.
one_line() {
	[ "$(printf '%s' "$1" | wc -l | tr -d ' ')" = 0 ]
}

# matches <value> <ERE>: whole-value match on a single line.
matches() {
	one_line "$1" && printf '%s\n' "$1" | grep -Eq "$2"
}

valid_version() {
	"$semver" valid "$1"
}

check_tag() {
	tag=$1
	file=${2:-VERSION}
	case $tag in
	v*) ;;
	*) die "tag '$tag' does not start with v" ;;
	esac
	version=${tag#v}
	valid_version "$version" || die "tag '$tag' is not v<x.y.z>"
	[ -f "$file" ] || die "version file '$file' not found"
	want=$(tr -d ' \t\r\n' <"$file")
	valid_version "$want" || die "VERSION '$want' is not a valid x.y.z version"
	[ "$version" = "$want" ] || die "tag $tag does not match VERSION $want"
	printf '%s\n' "$version"
}

is_latest() {
	version=$1
	valid_version "$version" || die "'$version' is not a valid x.y.z version"
	latest=true
	while IFS= read -r t || [ -n "$t" ]; do
		case $t in
		v*) ;;
		*) continue ;;
		esac
		v=${t#v}
		valid_version "$v" || continue
		if "$semver" gt "$v" "$version"; then
			latest=false
		fi
	done
	printf '%s\n' "$latest"
}

verify_signer() {
	want=$1
	matches "$want" '^[0-9A-Fa-f]{40}$' || die "expected fingerprint must be 40 hex digits"
	want=$(printf '%s' "$want" | LC_ALL=C tr '[:lower:]' '[:upper:]')
	good=0 valid=0 primary='' hash=''
	set -f # no globbing while splitting status lines
	while IFS= read -r line || [ -n "$line" ]; do
		# shellcheck disable=SC2086 # word splitting of the status line is intended
		set -- $line
		[ "${1:-}" = '[GNUPG:]' ] || continue
		case ${2:-} in
		GOODSIG) good=$((good + 1)) ;;
		VALIDSIG)
			valid=$((valid + 1))
			# VALIDSIG <fpr> <date> <ts> <expire> <ver> <reserved> <pkalgo>
			#          <hashalgo> <class> [<primary-fpr>]
			[ $# -ge 11 ] || die "malformed VALIDSIG status line"
			hash=${10}
			primary=${12:-$3}
			;;
		BADSIG | ERRSIG | EXPSIG | EXPKEYSIG | REVKEYSIG | NO_PUBKEY | FAILURE)
			die "signature status $2: the tag signature does not verify"
			;;
		esac
	done
	set +f
	if [ "$good" != 1 ] || [ "$valid" != 1 ]; then
		die "want exactly one good signature (got GOODSIG=$good VALIDSIG=$valid)"
	fi
	case $hash in
	8 | 9 | 10) ;; # SHA-256, SHA-384, SHA-512
	*) die "the tag signature uses a weak or unknown hash algorithm ($hash)" ;;
	esac
	primary=$(printf '%s' "$primary" | LC_ALL=C tr '[:lower:]' '[:upper:]')
	[ "$primary" = "$want" ] ||
		die "the tag is signed by key $primary, not by the pinned release key $want"
	printf 'release_ci: tag signed by the pinned release key %s\n' "$want"
}

changelog_section() {
	version=$1
	file=${2:-CHANGELOG.md}
	valid_version "$version" || die "'$version' is not a valid x.y.z version"
	[ -f "$file" ] || die "changelog '$file' not found"
	body=$(awk -v head="## [$version]" '
		index($0, "## [") == 1 {
			if (found) exit
			if (substr($0, 1, length(head)) == head) { found = 1; next }
		}
		found { print }
		END { if (!found) exit 3 }
	' "$file") || die "CHANGELOG has no section for $version"
	# Trim leading/trailing blank lines; an empty section is an error.
	body=$(printf '%s\n' "$body" | awk 'NF { seen = 1 } seen' | awk '
		{ lines[NR] = $0 } NF { last = NR }
		END { for (i = 1; i <= last; i++) print lines[i] }')
	[ -n "$body" ] || die "the CHANGELOG section for $version is empty"
	printf '%s\n' "$body"
}

image_repo() {
	matches "$1" '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$' || die "invalid repository '$1': want owner/repo"
	lower=$(printf '%s' "$1" | LC_ALL=C tr '[:upper:]' '[:lower:]')
	matches "$lower" '^[a-z0-9]+([._-][a-z0-9]+)*/[a-z0-9]+([._-][a-z0-9]+)*$' ||
		die "repository '$1' is not a valid image path"
	printf 'ghcr.io/%s\n' "$lower"
}

release_notes() {
	version=$1 repo=$2 digest=$3 url=$4
	file=${5:-CHANGELOG.md}
	valid_version "$version" || die "'$version' is not a valid x.y.z version"
	matches "$repo" '^ghcr\.io/[a-z0-9]+([._-][a-z0-9]+)*/[a-z0-9]+([._-][a-z0-9]+)*$' ||
		die "invalid image repository '$repo'"
	matches "$digest" '^sha256:[a-f0-9]{64}$' || die "invalid digest '$digest'"
	matches "$url" '^https://[a-z0-9.-]+/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$' ||
		die "invalid repository URL '$url'"
	section=$(changelog_section "$version" "$file")
	gh_repo=${url#https://*/}
	identity="$url/.github/workflows/release.yml@refs/tags/v$version"
	cat <<EOF
$section

## Container image

Multi-arch (linux/amd64, linux/arm64), signed with cosign (keyless, GitHub
OIDC), with SBOM and build provenance attestations. Always deploy by digest:

\`\`\`
$repo:$version@$digest
\`\`\`

Pull and verify it before use (cosign v3.0 or newer: the signatures and
attestations use the Sigstore bundle format):

\`\`\`sh
docker pull $repo@$digest
cosign verify \\
  --certificate-identity '$identity' \\
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \\
  $repo@$digest
# or with the GitHub CLI (build provenance):
gh attestation verify oci://$repo@$digest --repo $gh_repo
\`\`\`

## Install with Docker Compose

Linux host, Docker Engine 26+, Compose 2.24+ and a DNS name. Pick ONE of the
compose files attached to this release (pinned to the image above, attested,
with a cosign bundle each):

| File | Variant |
|---|---|
| \`compose.yaml\` | bekup with its own Traefik (ports 80/443, Let's Encrypt) |
| \`compose.traefik-external.yaml\` | behind your existing Traefik |
| \`compose.npm.yaml\` | behind Nginx Proxy Manager |

1. Download it under its release name into an empty directory, verify it,
   then save it as \`compose.yaml\` (\`f\` is the file you picked):

   \`\`\`sh
   sudo install -d -m 0755 /opt/bekup && cd /opt/bekup
   f=compose.yaml   # or compose.traefik-external.yaml / compose.npm.yaml
   sudo curl -fsSLO $url/releases/download/v$version/\$f
   gh attestation verify "\$f" --repo $gh_repo
   [ "\$f" = compose.yaml ] || sudo mv "\$f" compose.yaml
   \`\`\`

   \`gh attestation verify\` needs a signed-in GitHub CLI (\`gh auth login\`).
   Without it, check the sums and the cosign bundle of the same file before
   the \`mv\` (the names must still be the release names):

   \`\`\`sh
   sudo curl -fsSLO $url/releases/download/v$version/SHA256SUMS
   sudo curl -fsSLO $url/releases/download/v$version/\$f.sigstore.json
   sha256sum -c SHA256SUMS --ignore-missing
   cosign verify-blob --bundle "\$f.sigstore.json" \\
     --certificate-identity '$identity' \\
     --certificate-oidc-issuer https://token.actions.githubusercontent.com "\$f"
   \`\`\`

   For \`compose.yaml\` itself that is
   \`cosign verify-blob --bundle compose.yaml.sigstore.json --certificate-identity '$identity' --certificate-oidc-issuer https://token.actions.githubusercontent.com compose.yaml\`.

2. \`DOCKER_GID=\$(stat -c %g /var/run/docker.sock) docker compose run --rm setup\`
3. \`docker compose up -d\`, then
   \`docker compose run --rm admin create --email you@example.com --display-name "Your Name"\`

Behind your own proxy, create the link network once first
(\`docker network create --internal --subnet 10.213.253.0/28 --gateway 10.213.253.1 --ip-range 10.213.253.12/30 bekup-proxy\`;
the range keeps Docker's automatic addresses off the fixed ones) and add it
to the proxy's networks, next to the ones it already has, with the address
10.213.253.5 (docs/installation/reverse-proxy.md).

Upgrade (docs/installation/upgrades.md): back up, replace \`compose.yaml\` with
this release's file of the same variant, then:

\`\`\`sh
docker compose run --rm setup upgrade
docker compose pull && docker compose up -d
docker compose -f compose.yaml run --rm setup check
\`\`\`
EOF
}

tag_state() {
	ref=$1
	matches "$ref" '^ghcr\.io/[a-z0-9]+([._-][a-z0-9]+)*/[a-z0-9]+([._-][a-z0-9]+)*:[0-9]+\.[0-9]+\.[0-9]+$' ||
		die "invalid image tag reference '$ref'"
	docker=${RELEASE_CI_DOCKER:-docker}
	if out=$("$docker" buildx imagetools inspect "$ref" 2>&1); then
		printf 'exists\n'
		return 0
	fi
	lower=$(printf '%s' "$out" | LC_ALL=C tr '[:upper:]' '[:lower:]')
	case $lower in
	*"manifest unknown"* | *manifest_unknown* | *"name unknown"* | *name_unknown* | *"$ref: not found"* | *"404 not found"*)
		printf 'absent\n'
		;;
	*)
		printf '%s\n' "$out" | head -n 5 >&2
		die "cannot tell whether $ref exists (not a registry 'not found'); refusing to continue"
		;;
	esac
}

tag_object_name() {
	name=$(awk 'NF == 0 { exit } $1 == "tag" { print $2; n++ } END { if (n != 1) exit 3 }') ||
		die "want exactly one tag header in the tag object"
	matches "$name" '^v[0-9]+\.[0-9]+\.[0-9]+$' || die "invalid tag name '$name' in the tag object"
	printf '%s\n' "$name"
}

# compose_variants: "<variant>:<asset file>" pairs of the release assets.
compose_variants='traefik:compose.yaml traefik-external:compose.traefik-external.yaml npm:compose.npm.yaml'

valid_release_ref() {
	matches "$1" '^ghcr\.io/[a-z0-9]+([._-][a-z0-9]+)*/[a-z0-9]+([._-][a-z0-9]+)*:[0-9]+\.[0-9]+\.[0-9]+@sha256:[a-f0-9]{64}$'
}

compose_assets() {
	ref=$1 dir=$2
	valid_release_ref "$ref" || die "invalid release image reference '$ref'"
	[ -d "$dir" ] || die "'$dir' is not a directory"
	for pair in $compose_variants; do
		f="$dir/${pair#*:}"
		if [ ! -f "$f" ] || [ -L "$f" ]; then
			die "${pair#*:} is missing or not a regular file"
		fi
		n=$(grep -c '^x-bekup-image:' "$f" || true)
		[ "$n" = 1 ] || die "${pair#*:}: want exactly one x-bekup-image line, found $n"
		line=$(grep '^x-bekup-image:' "$f")
		[ "$line" = "x-bekup-image: &bekup-image $ref" ] ||
			die "${pair#*:}: the x-bekup-image line does not name $ref"
		# shellcheck disable=SC2016 # a literal ${BEKUP_IMAGE is searched for
		if grep -v '^[[:space:]]*#' "$f" | grep -q '\${BEKUP_IMAGE'; then
			die "${pair#*:}: a \${BEKUP_IMAGE} reference remains"
		fi
		if grep -Eq '^[[:space:]]*build:' "$f"; then
			die "${pair#*:}: build: is not allowed"
		fi
		# Every image: value that is not an alias is pinned by digest.
		bad=$(grep -E '^[[:space:]]*(-[[:space:]]+)?image:' "$f" |
			sed -E 's/^[[:space:]]*(-[[:space:]]+)?image:[[:space:]]*(&[A-Za-z0-9_-]+[[:space:]]+)?//' |
			grep -v '^\*' | grep -Ev '^[a-z0-9]+([._/:-][a-z0-9]+)*@sha256:[a-f0-9]{64}[[:space:]]*$' || true)
		[ -z "$bad" ] || die "${pair#*:}: image not pinned by digest: $(printf '%s' "$bad" | head -n 1)"
		grep -q '^services:' "$f" || die "${pair#*:}: no services"
	done
	printf 'release_ci: compose assets pinned to %s\n' "$ref"
}

# placeholder: the x-bekup-image line of the committed compose files
# (internal/setup ComposePlaceholderLine; a Go test keeps them equal).
# shellcheck disable=SC2016 # the literal ${BEKUP_IMAGE...} line
placeholder='x-bekup-image: &bekup-image ${BEKUP_IMAGE:?set BEKUP_IMAGE to the digest-pinned bekup image repo:tag@sha256:digest, see the README}'

committed_compose() {
	dir=$1 committed=$2
	[ -d "$dir" ] || die "'$dir' is not a directory"
	[ -d "$committed" ] || die "'$committed' is not a directory"
	for pair in $compose_variants; do
		name=${pair#*:}
		f="$dir/$name" c="$committed/$name"
		for x in "$f" "$c"; do
			if [ ! -f "$x" ] || [ -L "$x" ]; then
				die "$x is missing or not a regular file"
			fi
		done
		n=$(grep -c '^x-bekup-image:' "$c" || true)
		[ "$n" = 1 ] || die "$c: want exactly one x-bekup-image line, found $n"
		[ "$(grep '^x-bekup-image:' "$c")" = "$placeholder" ] ||
			die "$c: the x-bekup-image line is not the BEKUP_IMAGE placeholder (run 'just gen')"
		# Line by line, the image line replaced by a marker in both.
		tmp=$(mktemp) || die "mktemp failed"
		sed 's/^x-bekup-image:.*$/x-bekup-image: <image>/' "$c" >"$tmp" || {
			rm -f "$tmp"
			die "reading $c failed"
		}
		if ! sed 's/^x-bekup-image:.*$/x-bekup-image: <image>/' "$f" | cmp -s - "$tmp"; then
			rm -f "$tmp"
			die "$name differs from the committed $c beyond the image line (run 'just gen' and commit before tagging)"
		fi
		rm -f "$tmp"
	done
	printf 'release_ci: committed compose files match the release files\n'
}

render_compose() {
	ref=$1 dir=$2
	valid_release_ref "$ref" || die "invalid release image reference '$ref'"
	[ -d "$dir" ] || die "'$dir' is not a directory"
	docker=${RELEASE_CI_DOCKER:-docker}
	bydigest="${ref%%:*}@${ref#*@}"
	for pair in $compose_variants; do
		"$docker" run --rm --network none --read-only --user 65532:65532 --cap-drop ALL \
			--security-opt no-new-privileges --log-driver none "$bydigest" \
			setup compose --variant "${pair%%:*}" --image "$ref" >"$dir/${pair#*:}" ||
			die "rendering ${pair#*:} failed"
	done
	compose_assets "$ref" "$dir"
}

assets_digest() {
	dir=$1
	[ -d "$dir" ] || die "'$dir' is not a directory"
	set --
	for pair in $compose_variants; do
		set -- "$@" "${pair#*:}" "${pair#*:}.sigstore.json"
	done
	set -- "$@" SHA256SUMS SHA256SUMS.sigstore.json
	for f in "$@"; do
		if [ ! -f "$dir/$f" ] || [ -L "$dir/$f" ]; then
			die "$f is missing or not a regular file"
		fi
	done
	sum=$(cd "$dir" && sha256sum -- "$@" | sha256sum | cut -d' ' -f1)
	matches "$sum" '^[a-f0-9]{64}$' || die "sha256sum failed"
	printf '%s\n' "$sum"
}

sbom_generator() {
	file=${1:-$here/publish.sh}
	[ -f "$file" ] || die "'$file' not found"
	gen=$(sed -n 's/^sbom_generator=\(.*\)$/\1/p' "$file")
	matches "$gen" '^[a-z0-9./-]+@sha256:[a-f0-9]{64}$' ||
		die "no digest-pinned sbom_generator in $file"
	printf '%s\n' "$gen"
}

cmd=${1:-}
[ $# -gt 0 ] && shift
case $cmd in
check-tag)
	if [ $# -lt 1 ] || [ $# -gt 2 ]; then die "usage: release_ci.sh check-tag <tag> [version-file]"; fi
	check_tag "$@"
	;;
is-latest)
	[ $# -eq 1 ] || die "usage: release_ci.sh is-latest <x.y.z> <tags"
	is_latest "$1"
	;;
verify-signer)
	[ $# -eq 1 ] || die "usage: release_ci.sh verify-signer <fingerprint> <status"
	verify_signer "$1"
	;;
changelog-section)
	if [ $# -lt 1 ] || [ $# -gt 2 ]; then die "usage: release_ci.sh changelog-section <x.y.z> [changelog]"; fi
	changelog_section "$@"
	;;
image-repo)
	[ $# -eq 1 ] || die "usage: release_ci.sh image-repo <owner/repo>"
	image_repo "$1"
	;;
release-notes)
	if [ $# -lt 4 ] || [ $# -gt 5 ]; then
		die "usage: release_ci.sh release-notes <x.y.z> <image-repo> <digest> <repo-url> [changelog]"
	fi
	release_notes "$@"
	;;
sbom-generator)
	[ $# -le 1 ] || die "usage: release_ci.sh sbom-generator [publish.sh]"
	sbom_generator "$@"
	;;
tag-state)
	[ $# -eq 1 ] || die "usage: release_ci.sh tag-state <repo:x.y.z>"
	tag_state "$1"
	;;
tag-object-name)
	[ $# -eq 0 ] || die "usage: release_ci.sh tag-object-name <tag-object"
	tag_object_name
	;;
render-compose)
	[ $# -eq 2 ] || die "usage: release_ci.sh render-compose <repo:x.y.z@sha256:digest> <dir>"
	render_compose "$1" "$2"
	;;
compose-assets)
	[ $# -eq 2 ] || die "usage: release_ci.sh compose-assets <repo:x.y.z@sha256:digest> <dir>"
	compose_assets "$1" "$2"
	;;
committed-compose)
	[ $# -eq 2 ] || die "usage: release_ci.sh committed-compose <dir> <committed-dir>"
	committed_compose "$1" "$2"
	;;
assets-digest)
	[ $# -eq 1 ] || die "usage: release_ci.sh assets-digest <dir>"
	assets_digest "$1"
	;;
*)
	die "usage: release_ci.sh check-tag | is-latest | verify-signer | changelog-section | image-repo | release-notes | sbom-generator | tag-state | tag-object-name | render-compose | compose-assets | committed-compose | assets-digest"
	;;
esac
