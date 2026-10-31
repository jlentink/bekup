#!/bin/sh
# release_ci_test.sh: tests for scripts/release_ci.sh (run by `just test-scripts`).
set -u

here="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
rc="$here/release_ci.sh"
fail=0 count=0
tmp=$(mktemp -d)
# shellcheck disable=SC2317 # invoked by the trap
cleanup() {
	if [ -d "$tmp/gnupg" ] && command -v gpgconf >/dev/null 2>&1; then
		GNUPGHOME="$tmp/gnupg" gpgconf --kill all 2>/dev/null || true
	fi
	rm -rf "$tmp"
}
trap cleanup EXIT
trap 'exit 1' INT TERM

# want <expected-output> <cmd...>: the command succeeds and prints exactly that.
want() {
	exp=$1
	shift
	count=$((count + 1))
	got=$("$@" 2>/dev/null) || got="<error>"
	if [ "$got" != "$exp" ]; then
		printf 'FAIL: %s: got %s, want %s\n' "$*" "$got" "$exp" >&2
		fail=1
	fi
}

# refuse <cmd...>: the command fails.
refuse() {
	count=$((count + 1))
	if out=$("$@" 2>/dev/null); then
		printf 'FAIL: %s: want error, got %s\n' "$*" "$out" >&2
		fail=1
	fi
}

# --- check-tag ---------------------------------------------------------------
printf '1.2.3\n' >"$tmp/VERSION"
want 1.2.3 "$rc" check-tag v1.2.3 "$tmp/VERSION"
refuse "$rc" check-tag v1.2.4 "$tmp/VERSION"       # VERSION mismatch
refuse "$rc" check-tag 1.2.3 "$tmp/VERSION"        # no v prefix
refuse "$rc" check-tag v1.2.3-rc.1 "$tmp/VERSION"  # pre-release
refuse "$rc" check-tag v01.2.3 "$tmp/VERSION"      # leading zero
refuse "$rc" check-tag 'v1.2.3;id' "$tmp/VERSION"  # metacharacters
refuse "$rc" check-tag "$(printf 'v1.2.3\nv1.2.3')" "$tmp/VERSION"
refuse "$rc" check-tag v1.2.3 "$tmp/missing"
printf 'x.y.z\n' >"$tmp/BADVERSION"
refuse "$rc" check-tag v1.2.3 "$tmp/BADVERSION"

# --- is-latest ---------------------------------------------------------------
# shellcheck disable=SC2317 # invoked through want/refuse
latest() { # latest <version> <tags, one per line>
	printf '%s\n' "$2" | "$rc" is-latest "$1"
}
want true latest 1.2.3 ''
want true latest 1.2.3 'v1.2.3'
want true latest 1.2.3 "$(printf 'v0.9.0\nv1.2.2\nv1.2.3')"
want false latest 1.2.3 "$(printf 'v1.2.3\nv1.3.0')"
want false latest 1.2.4 "$(printf 'v1.2.3\nv1.10.0\nv1.2.4')" # patch of an older line
want true latest 1.10.0 "$(printf 'v1.9.9\nv1.10.0')"           # numeric, not lexical
want true latest 1.2.3 "$(printf 'v1.2.3\nv2.0.0-rc.1\nv9.9.9+build\nfoo\n9.9.9\nv9.9')" # ignored
want false latest 1.2.3 "$(printf 'v1.2.3\nv2.0.0')"
refuse latest v1.2.3 'v1.0.0'
refuse latest 1.2 'v1.0.0'

# --- verify-signer -----------------------------------------------------------
fpr=1563553AE7CE3A740617A07A592C7E70AA120838
sub=4977100FFF2A2BA8006EBB205DAE9982E3744E93
status() { # status <validsig-fpr> <hash> <primary> [extra line]
	printf '[GNUPG:] NEWSIG\n'
	printf '[GNUPG:] KEY_CONSIDERED %s 0\n' "$fpr"
	printf '[GNUPG:] SIG_ID abc 2026-10-29 1761700000\n'
	printf '[GNUPG:] GOODSIG 592C7E70AA120838 Jason Lentink <developer@jasonlentink.nl>\n'
	printf '[GNUPG:] VALIDSIG %s 2026-10-29 1761700000 0 4 0 1 %s 00 %s\n' "$1" "$2" "$3"
	printf '[GNUPG:] TRUST_ULTIMATE 0 pgp\n'
	[ -z "${4:-}" ] || printf '%s\n' "$4"
}
signer() { # signer <expected> <status args...>
	exp=$1
	shift
	status "$@" | "$rc" verify-signer "$exp" >/dev/null
}
ok_signer() {
	count=$((count + 1))
	signer "$@" 2>/dev/null || {
		printf 'FAIL: verify-signer %s: want success\n' "$*" >&2
		fail=1
	}
}
ok_signer "$fpr" "$fpr" 10 "$fpr"
ok_signer "$fpr" "$fpr" 8 "$fpr"
ok_signer "$(printf %s "$fpr" | tr 'A-F' 'a-f')" "$fpr" 10 "$fpr" # case-insensitive
ok_signer "$fpr" "$sub" 10 "$fpr"                                   # signing subkey of the pinned key
refuse signer "$fpr" "$sub" 10 "$sub"                               # another primary key
refuse signer 0000000000000000000000000000000000000000 "$fpr" 10 "$fpr"
refuse signer "$fpr" "$fpr" 2 "$fpr"                                # SHA-1
refuse signer "$fpr" "$fpr" 10 "$fpr" "[GNUPG:] BADSIG 592C7E70AA120838 x"
refuse signer "$fpr" "$fpr" 10 "$fpr" "[GNUPG:] EXPKEYSIG 592C7E70AA120838 x"
refuse signer "$fpr" "$fpr" 10 "$fpr" "[GNUPG:] REVKEYSIG 592C7E70AA120838 x"
refuse signer "$fpr" "$fpr" 10 "$fpr" "[GNUPG:] GOODSIG 1111111111111111 other"
refuse signer "$fpr" "$fpr" 10 "$fpr" "[GNUPG:] VALIDSIG $fpr 2026-10-29 1 0 4 0 1 10 00 $fpr"
refuse signer short "$fpr" 10 "$fpr"
count=$((count + 1))
if printf '' | "$rc" verify-signer "$fpr" >/dev/null 2>&1; then
	echo "FAIL: verify-signer: unsigned (no status) accepted" >&2
	fail=1
fi
count=$((count + 1))
if printf '[GNUPG:] GOODSIG x\n[GNUPG:] VALIDSIG * * *\n' | "$rc" verify-signer "$fpr" >/dev/null 2>&1; then
	echo "FAIL: verify-signer: malformed VALIDSIG accepted" >&2
	fail=1
fi

# Real round trip with a throwaway key (skipped without gpg).
if command -v gpg >/dev/null 2>&1 && command -v git >/dev/null 2>&1; then
	GNUPGHOME="$tmp/gnupg"
	export GNUPGHOME
	mkdir -m 0700 "$GNUPGHOME"
	if gpg --batch --quiet --passphrase '' --quick-gen-key 'bekup test <test@example.invalid>' ed25519 sign never 2>/dev/null; then
		tfpr=$(gpg --batch --with-colons --list-keys 2>/dev/null | awk -F: '$1 == "fpr" { print $10; exit }')
		repo="$tmp/repo"
		git init -q "$repo"
		git -C "$repo" -c user.name=t -c user.email=test@example.invalid -c commit.gpgsign=false \
			commit -q --allow-empty -m init
		git -C "$repo" -c user.name=t -c user.email=test@example.invalid -c user.signingkey="$tfpr" \
			-c gpg.format=openpgp tag -s -m v1.0.0 v1.0.0 2>/dev/null
		git -C "$repo" -c user.name=t -c user.email=test@example.invalid tag -a -m v1.0.1 v1.0.1
		count=$((count + 1))
		if ! git -C "$repo" verify-tag --raw v1.0.0 2>&1 | "$rc" verify-signer "$tfpr" >/dev/null 2>&1; then
			echo "FAIL: verify-signer: real signed tag rejected" >&2
			fail=1
		fi
		count=$((count + 1))
		if git -C "$repo" verify-tag --raw v1.0.0 2>&1 | "$rc" verify-signer "$fpr" >/dev/null 2>&1; then
			echo "FAIL: verify-signer: real tag accepted for another fingerprint" >&2
			fail=1
		fi
		count=$((count + 1))
		if git -C "$repo" verify-tag --raw v1.0.1 2>&1 | "$rc" verify-signer "$tfpr" >/dev/null 2>&1; then
			echo "FAIL: verify-signer: unsigned annotated tag accepted" >&2
			fail=1
		fi
	else
		echo "release_ci_test: gpg could not create a test key; real signature round trip skipped" >&2
	fi
	unset GNUPGHOME
fi

# --- changelog-section -------------------------------------------------------
cat >"$tmp/CHANGELOG.md" <<'EOF'
# Changelog

## [Unreleased]

### Added

- not yet

## [1.2.30] - 2026-11-01

- wrong prefix match

## [1.2.3] - 2026-10-29

### Added

- feature one
- feature two

### Fixed

- bug

## [1.2.2] - 2026-10-01

- older

## [1.0.0] - 2026-01-01

## [0.9.0] - 2025-12-01

- last
EOF
want "$(printf '### Added\n\n- feature one\n- feature two\n\n### Fixed\n\n- bug')" \
	"$rc" changelog-section 1.2.3 "$tmp/CHANGELOG.md"
want '- older' "$rc" changelog-section 1.2.2 "$tmp/CHANGELOG.md"
want '- last' "$rc" changelog-section 0.9.0 "$tmp/CHANGELOG.md"
want '- wrong prefix match' "$rc" changelog-section 1.2.30 "$tmp/CHANGELOG.md"
refuse "$rc" changelog-section 1.0.0 "$tmp/CHANGELOG.md" # empty section
refuse "$rc" changelog-section 1.2.4 "$tmp/CHANGELOG.md" # missing
refuse "$rc" changelog-section 1.2 "$tmp/CHANGELOG.md"
refuse "$rc" changelog-section '1.2.*' "$tmp/CHANGELOG.md"
refuse "$rc" changelog-section 1.2.3 "$tmp/nope.md"

# --- image-repo --------------------------------------------------------------
want ghcr.io/jlentink/bekup "$rc" image-repo jlentink/bekup
want ghcr.io/some-org/my.repo "$rc" image-repo Some-Org/My.Repo
refuse "$rc" image-repo jlentink
refuse "$rc" image-repo 'jlentink/bekup/x'
refuse "$rc" image-repo 'jlentink/bekup;id'
refuse "$rc" image-repo 'jlentink/-bekup'
refuse "$rc" image-repo "$(printf 'a/b\nc/d')"

# --- release-notes -----------------------------------------------------------
digest=sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
notes=$("$rc" release-notes 1.2.3 ghcr.io/jlentink/bekup "$digest" https://github.com/jlentink/bekup "$tmp/CHANGELOG.md" 2>/dev/null) || notes='<error>'
# shellcheck disable=SC2016 # literal $IMAGE/$NEW/$(...) expected in the notes
for needle in \
	'- feature one' \
	"ghcr.io/jlentink/bekup:1.2.3@$digest" \
	"docker pull ghcr.io/jlentink/bekup@$digest" \
	"--certificate-identity 'https://github.com/jlentink/bekup/.github/workflows/release.yml@refs/tags/v1.2.3'" \
	'--certificate-oidc-issuer https://token.actions.githubusercontent.com' \
	"gh attestation verify oci://ghcr.io/jlentink/bekup@$digest --repo jlentink/bekup" \
	'compose.traefik-external.yaml' \
	'compose.npm.yaml' \
	'compose.external-db.yaml' \
	'compose.npm.external-db.yaml' \
	'docker compose run --rm migrate db bootstrap --admin-user <admin>' \
	"-e BEKUP_IMAGE=ghcr.io/jlentink/bekup:1.2.3@$digest" \
	'-v "$PWD":/deploy ghcr.io/jlentink/bekup:1.2.3@'"$digest"' setup' \
	'DOCKER_GID=$(stat -c %g /var/run/docker.sock) docker compose run --rm setup' \
	'docker compose run --rm setup upgrade' \
	'docker network create --internal --subnet 10.213.253.0/28 --gateway 10.213.253.1 --ip-range 10.213.253.12/30 bekup-proxy' \
	'sudo curl -fsSLO https://github.com/jlentink/bekup/releases/download/v1.2.3/$f' \
	'gh attestation verify "$f" --repo jlentink/bekup' \
	'[ "$f" = compose.yaml ] || sudo mv "$f" compose.yaml' \
	'cosign verify-blob --bundle "$f.sigstore.json"' \
	'docker compose -f compose.yaml run --rm setup check' \
	"cosign verify-blob --bundle compose.yaml.sigstore.json --certificate-identity 'https://github.com/jlentink/bekup/.github/workflows/release.yml@refs/tags/v1.2.3'" \
	'cosign v3.0 or newer'; do
	count=$((count + 1))
	case $notes in
	*"$needle"*) ;;
	*)
		printf 'FAIL: release-notes: missing %s\n' "$needle" >&2
		fail=1
		;;
	esac
done
count=$((count + 1))
case $notes in
*'- older'* | *'not yet'*)
	echo "FAIL: release-notes: contains another section" >&2
	fail=1
	;;
esac
refuse "$rc" release-notes 1.2.3 ghcr.io/jlentink/bekup sha256:abc https://github.com/jlentink/bekup "$tmp/CHANGELOG.md"
refuse "$rc" release-notes 1.2.3 docker.io/jlentink/bekup "$digest" https://github.com/jlentink/bekup "$tmp/CHANGELOG.md"
refuse "$rc" release-notes 1.2.3 'ghcr.io/jlentink/bekup:1.2.3' "$digest" https://github.com/jlentink/bekup "$tmp/CHANGELOG.md"
# shellcheck disable=SC2016 # a literal command substitution must be refused
refuse "$rc" release-notes 1.2.3 ghcr.io/jlentink/bekup "$digest" 'https://github.com/jlentink/bekup$(id)' "$tmp/CHANGELOG.md"
refuse "$rc" release-notes 1.2.3 ghcr.io/jlentink/bekup "$digest" http://github.com/jlentink/bekup "$tmp/CHANGELOG.md"
refuse "$rc" release-notes 1.2.4 ghcr.io/jlentink/bekup "$digest" https://github.com/jlentink/bekup "$tmp/CHANGELOG.md"

# --- compose-assets / render-compose -------------------------------------------
cref=ghcr.io/jlentink/bekup:1.2.3@$digest
pg=postgres:17-alpine@sha256:266d3c257594e195b309ae9d1805e54c788f1d7b375f56bf6c367aac6c917e7b
# fixture <file> <x-bekup-image value> [extra line]: a minimal release file.
fixture() {
	{
		# shellcheck disable=SC2016 # a literal ${BEKUP_IMAGE} in a comment
		printf '# bekup compose file\n# ${BEKUP_IMAGE} in a comment is fine\nname: bekup\n\n'
		printf 'x-bekup-image: &bekup-image %s\n\n' "$2"
		printf 'x-h: &h\n  image: *bekup-image\n\nservices:\n  serve:\n    <<: *h\n'
		printf '  postgres:\n    image: %s\n' "$pg"
		[ -z "${3:-}" ] || printf '%s\n' "$3"
	} >"$1"
}
assets() { # assets <dir> [file-to-break value extra]
	rm -rf "$1" && mkdir -p "$1"
	for f in compose.yaml compose.traefik-external.yaml compose.npm.yaml compose.external-db.yaml compose.traefik-external.external-db.yaml compose.npm.external-db.yaml; do
		fixture "$1/$f" "$cref"
	done
}
assets "$tmp/ca"
want "release_ci: compose assets pinned to $cref" "$rc" compose-assets "$cref" "$tmp/ca"
refuse "$rc" compose-assets "ghcr.io/jlentink/bekup:1.2.3" "$tmp/ca"           # unpinned ref
refuse "$rc" compose-assets "ghcr.io/jlentink/bekup@$digest" "$tmp/ca"         # no version tag
refuse "$rc" compose-assets "$cref" "$tmp/missing"
# broken <name> <file> <x-bekup-image value> [extra line]
# shellcheck disable=SC2317 # invoked below
broken() {
	assets "$tmp/cb"
	fixture "$tmp/cb/$2" "$3" "${4:-}"
	refuse "$rc" compose-assets "$cref" "$tmp/cb"
}
broken other-digest compose.npm.yaml "ghcr.io/jlentink/bekup:1.2.3@sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
broken other-repo compose.yaml "ghcr.io/evil/bekup:1.2.3@$digest"
# shellcheck disable=SC2016 # a literal ${BEKUP_IMAGE...} is the point
broken variable compose.yaml '${BEKUP_IMAGE:-bekup:dev}'
broken second-line compose.yaml "$cref" "x-bekup-image: &x $cref"
broken unpinned-image compose.traefik-external.yaml "$cref" '  traefik:
    image: traefik:v3'
# shellcheck disable=SC2016 # literal variable reference
broken variable-image compose.yaml "$cref" '  traefik:
    image: ${TRAEFIK_IMAGE}'
broken build compose.yaml "$cref" '  x:
    build: .'
# shellcheck disable=SC2016 # literal variable reference
broken leftover-var compose.yaml "$cref" '  x:
    environment: {A: ${BEKUP_IMAGE}}'
assets "$tmp/cc" && rm "$tmp/cc/compose.npm.yaml"
refuse "$rc" compose-assets "$cref" "$tmp/cc"
assets "$tmp/cc" && rm "$tmp/cc/compose.npm.external-db.yaml"
refuse "$rc" compose-assets "$cref" "$tmp/cc"
broken external-other-digest compose.traefik-external.external-db.yaml "ghcr.io/jlentink/bekup:1.2.3@sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
assets "$tmp/cd" && rm "$tmp/cd/compose.yaml" && ln -s compose.npm.yaml "$tmp/cd/compose.yaml"
refuse "$rc" compose-assets "$cref" "$tmp/cd"
# render-compose: a stub docker that checks the hardening flags and prints
# a fixture for the requested variant.
cat >"$tmp/docker-render" <<'STUB'
#!/bin/sh
printf '%s\n' "$*" >>"$STUB_LOG"
case " $* " in
*" run --rm --network none --read-only --user 65532:65532 --cap-drop ALL --security-opt no-new-privileges --log-driver none "*) ;;
*) echo "unhardened docker run: $*" >&2; exit 9 ;;
esac
. "$STUB_LIB"
fixture /dev/stdout "$STUB_REF"
STUB
chmod +x "$tmp/docker-render"
{ printf 'pg=%s\n' "$pg"; sed -n '/^fixture() {/,/^}/p' "$0"; } >"$tmp/fixture.sh"
mkdir -p "$tmp/render"
# shellcheck disable=SC2317 # invoked through want/refuse
render() { # render <ref-to-print>
	STUB_LOG="$tmp/render.log" STUB_LIB="$tmp/fixture.sh" STUB_REF=$1 RELEASE_CI_DOCKER="$tmp/docker-render" \
		"$rc" render-compose "$cref" "$tmp/render"
}
want "release_ci: compose assets pinned to $cref" render "$cref"
count=$((count + 1))
if [ "$(grep -c "ghcr.io/jlentink/bekup@$digest setup compose --variant" "$tmp/render.log")" != 6 ] ||
	! grep -q -- "--variant traefik-external --database bundled --image $cref" "$tmp/render.log" ||
	! grep -q -- "--variant npm --database external --image $cref" "$tmp/render.log"; then
	printf 'FAIL: render-compose: docker runs:\n%s\n' "$(cat "$tmp/render.log")" >&2
	fail=1
fi
refuse render "ghcr.io/jlentink/bekup:1.2.4@$digest" # the image printed another ref
refuse "$rc" render-compose 'ghcr.io/jlentink/bekup:1.2.3@sha256:x;id' "$tmp/render"

# --- committed-compose ---------------------------------------------------------
# The committed deploy/compose/ files with the placeholder line replaced by
# the release reference are what the release renders.
placeholder_line=$(grep '^x-bekup-image:' "$here/../deploy/compose/compose.yaml")
rm -rf "$tmp/cr" && mkdir -p "$tmp/cr"
for f in compose.yaml compose.traefik-external.yaml compose.npm.yaml compose.external-db.yaml compose.traefik-external.external-db.yaml compose.npm.external-db.yaml; do
	sed "s|^x-bekup-image:.*\$|x-bekup-image: \&bekup-image $cref|" "$here/../deploy/compose/$f" >"$tmp/cr/$f"
done
want "release_ci: committed compose files match the release files" "$rc" committed-compose "$tmp/cr" "$here/../deploy/compose"
want "release_ci: compose assets pinned to $cref" "$rc" compose-assets "$cref" "$tmp/cr"
# committed_case <name> <sed expression on one committed copy>
committed_case() {
	rm -rf "$tmp/cm" && cp -R "$here/../deploy/compose" "$tmp/cm"
	sed "$2" "$tmp/cm/compose.npm.yaml" >"$tmp/cm/x" && mv "$tmp/cm/x" "$tmp/cm/compose.npm.yaml"
	refuse "$rc" committed-compose "$tmp/cr" "$tmp/cm"
}
committed_case body-edit 's/memory: 2G/memory: 3G/'
committed_case pinned-line "s|^x-bekup-image:.*\$|x-bekup-image: \&bekup-image $cref|"
# shellcheck disable=SC2016 # a literal ${BEKUP_IMAGE...} is the point
committed_case other-placeholder 's|^x-bekup-image:.*$|x-bekup-image: \&bekup-image ${BEKUP_IMAGE:-bekup:dev}|'
# shellcheck disable=SC2016 # sed's $ (last line), not a shell expansion
committed_case extra-line '$a\
# trailing'
rm -rf "$tmp/cm" && cp -R "$here/../deploy/compose" "$tmp/cm" && rm "$tmp/cm/compose.yaml"
refuse "$rc" committed-compose "$tmp/cr" "$tmp/cm" # missing file
rm -rf "$tmp/cm" && cp -R "$here/../deploy/compose" "$tmp/cm" && rm "$tmp/cm/compose.external-db.yaml"
refuse "$rc" committed-compose "$tmp/cr" "$tmp/cm" # missing external-db file
refuse "$rc" committed-compose "$tmp/missing" "$here/../deploy/compose"
count=$((count + 1))
if [ "$placeholder_line" = "" ] || ! grep -qF "placeholder='$placeholder_line'" "$rc"; then
	echo "FAIL: committed-compose: release_ci.sh does not carry the committed placeholder line" >&2
	fail=1
fi

# --- assets-digest -----------------------------------------------------------
install -d "$tmp/ad"
for f in compose.yaml compose.traefik-external.yaml compose.npm.yaml compose.external-db.yaml compose.traefik-external.external-db.yaml compose.npm.external-db.yaml SHA256SUMS; do
	printf '%s\n' "$f" >"$tmp/ad/$f"
	printf '{"b":"%s"}\n' "$f" >"$tmp/ad/$f.sigstore.json"
done
ad1=$("$rc" assets-digest "$tmp/ad" 2>/dev/null) || ad1='<error>'
want "$ad1" "$rc" assets-digest "$tmp/ad" # stable
count=$((count + 1))
case $ad1 in
*[!a-f0-9]* | '') bad=1 ;;
*) bad=$((${#ad1} != 64)) ;;
esac
if [ "$bad" = 1 ]; then
	printf 'FAIL: assets-digest: got %s\n' "$ad1" >&2
	fail=1
fi
printf 'x\n' >>"$tmp/ad/compose.npm.yaml.sigstore.json" # a changed bundle changes the digest
ad2=$("$rc" assets-digest "$tmp/ad" 2>/dev/null) || ad2='<error>'
count=$((count + 1))
if [ "$ad2" = "$ad1" ] || [ "$ad2" = '<error>' ]; then
	echo "FAIL: assets-digest: a changed bundle kept the digest" >&2
	fail=1
fi
rm "$tmp/ad/SHA256SUMS.sigstore.json"
refuse "$rc" assets-digest "$tmp/ad" # missing bundle
ln -s compose.yaml "$tmp/ad/SHA256SUMS.sigstore.json"
refuse "$rc" assets-digest "$tmp/ad" # symlink
refuse "$rc" assets-digest "$tmp/missing"

# --- sbom-generator ----------------------------------------------------------
gen=$("$rc" sbom-generator 2>/dev/null) || gen='<error>'
count=$((count + 1))
case $gen in
docker/buildkit-syft-scanner@sha256:*) ;;
*)
	printf 'FAIL: sbom-generator: got %s\n' "$gen" >&2
	fail=1
	;;
esac
printf 'sbom_generator=docker/buildkit-syft-scanner:stable-1\n' >"$tmp/publish.sh"
refuse "$rc" sbom-generator "$tmp/publish.sh" # tag only, no digest

# --- tag-state ---------------------------------------------------------------
# A stub docker: prints $STUB_OUT and exits $STUB_RC.
cat >"$tmp/docker" <<'STUB'
#!/bin/sh
printf '%s\n' "$STUB_OUT"
exit "$STUB_RC"
STUB
chmod +x "$tmp/docker"
# shellcheck disable=SC2317 # invoked through want/refuse
state() { # state <rc> <output> [ref]
	STUB_RC=$1 STUB_OUT=$2 RELEASE_CI_DOCKER="$tmp/docker" "$rc" tag-state "${3:-ghcr.io/jlentink/bekup:1.2.3}"
}
want exists state 0 'Name: ghcr.io/jlentink/bekup:1.2.3'
want absent state 1 'ERROR: ghcr.io/jlentink/bekup:1.2.3: not found'
want absent state 1 'ERROR: failed to resolve: MANIFEST_UNKNOWN: manifest unknown'
want absent state 1 'ERROR: NAME_UNKNOWN: repository name not known to registry'
want absent state 1 'unexpected status from HEAD request: 404 Not Found'
refuse state 1 'ERROR: denied: requested access to the resource is denied'
refuse state 1 'ERROR: unexpected status: 401 Unauthorized'
refuse state 1 'ERROR: toomanyrequests: rate limit'
refuse state 1 'dial tcp: lookup ghcr.io: no such host'
refuse state 1 'sh: docker: not found'
refuse state 0 'Name: x' 'ghcr.io/jlentink/bekup:latest'   # only version tags
refuse state 0 'Name: x' 'ghcr.io/x/y:1.2.3;id'

# --- tag-object-name ----------------------------------------------------------
# shellcheck disable=SC2317 # invoked through want/refuse
tagobj() { printf '%s\n' "$@" | "$rc" tag-object-name; }
want v1.2.3 tagobj 'object 0123' 'type commit' 'tag v1.2.3' 'tagger A <a@b> 1 +0000' '' 'tag v9.9.9 in the message'
refuse tagobj 'object 0123' 'type commit' 'tagger A <a@b> 1 +0000' '' 'tag v1.2.3'
refuse tagobj 'object 0123' 'tag v1.2.3' 'tag v1.2.4' ''
refuse tagobj 'object 0123' 'tag v1.2.3;id' ''

refuse "$rc" unknown-command

if [ "$fail" = 0 ]; then
	printf 'release_ci_test: %d cases passed\n' "$count"
fi
exit "$fail"
