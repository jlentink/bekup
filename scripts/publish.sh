#!/usr/bin/env bash
# publish.sh: build the bekup image for several platforms and push it to the
# operator's own registry (`just publish`, D-202).
#
# Usage: scripts/publish.sh [repo] [--dirty] [--yes] [--dry-run]
#
# The repository (e.g. registry.example.com/team/bekup, no tag, no digest)
# comes from the argument, else the environment variable BEKUP_IMAGE_REPO,
# else the gitignored .just.env (see .just.env.example). It is never
# committed. Platforms: BEKUP_PLATFORMS (default linux/amd64,linux/arm64).
#
# A clean tree pushes <repo>:<VERSION>, <repo>:sha-<short> and <repo>:latest,
# built from a pristine export of HEAD (git archive), so ignored or hidden
# local files never enter the build context. A dirty tree (including files
# marked skip-worktree/assume-unchanged) is refused unless --dirty, which
# builds the working tree and pushes only <repo>:sha-<short>-dirty. The
# binary's version is VERSION on the release tag v<VERSION>, else
# <VERSION>+sha.<short> (and .dirty for --dirty). The image is built with
# buildx on the dedicated builder "bekup-publish" (docker-container driver on
# the local Docker) with max-mode provenance (without git remote info) and an
# SBOM, then the digest and a compose pin hint are printed.
#
# Registry credentials come only from the user's own `docker login`; they
# never pass through this script. Git tags and CHANGELOG are never touched.
set -euo pipefail

here=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)

usage() {
	cat <<'EOF'
Usage: just publish [repo] [--dirty] [--yes] [--dry-run]

  repo        registry/path of the image, e.g. registry.example.com/team/bekup
              (no tag, no digest, no scheme, no credentials). Default:
              $BEKUP_IMAGE_REPO, else BEKUP_IMAGE_REPO in .just.env.
  --dirty     allow uncommitted changes; pushes only sha-<short>-dirty
  --yes       do not ask for confirmation before pushing
  --dry-run   print the buildx command and exit without building

Platforms: $BEKUP_PLATFORMS (or .just.env), default linux/amd64,linux/arm64.
Log in first with: docker login <registry>
EOF
}

repo_arg='' dirty_ok=0 assume_yes=0 dry_run=0
while (($#)); do
	case $1 in
	--dirty) dirty_ok=1 ;;
	--yes | -y) assume_yes=1 ;;
	--dry-run | -n) dry_run=1 ;;
	-h | --help)
		usage
		exit 0
		;;
	-*)
		usage >&2
		printf 'error: unknown option %q\n' "$1" >&2
		exit 2
		;;
	*)
		[[ -z $repo_arg ]] || {
			usage >&2
			echo "error: more than one repository given" >&2
			exit 2
		}
		repo_arg=$1
		;;
	esac
	shift
done

git -C "$here" rev-parse --is-inside-work-tree >/dev/null 2>&1 || {
	echo "error: not inside a git repository" >&2
	exit 1
}
cd "$(git -C "$here" rev-parse --show-toplevel)"
# shellcheck source=scripts/lib.bash
source scripts/lib.bash

# valid_repo <repo>: an image repository with an explicit registry host and
# a lowercase path, no tag, no digest, no scheme, no credentials.
valid_repo() {
	local r=$1 host path
	[[ -n $r ]] || die "no image repository: pass one, set BEKUP_IMAGE_REPO or copy .just.env.example to .just.env"
	[[ $r != *://* ]] || die "the image repository must not contain a scheme (use host[:port]/path)"
	[[ $r != *@* ]] || die "the image repository must not contain credentials or a digest ('@'); log in with docker login instead"
	((${#r} <= 255)) || die "the image repository is longer than 255 characters"
	[[ $r =~ ^[a-z0-9.-]+(:[0-9]+)?/[a-z0-9._/-]+$ ]] ||
		die "invalid image repository $(printf %q "$r"): want host[:port]/path, lowercase, no tag or digest"
	host=${r%%/*}
	path=${r#*/}
	[[ $host == *.* || $host == *:* || $host == localhost ]] ||
		die "the image repository must start with a registry host (e.g. docker.io/$r)"
	[[ $host =~ ^[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?$ ]] || die "invalid registry host $(printf %q "$host")"
	if [[ $host == *:* ]]; then
		local port=${host##*:}
		((10#$port >= 1 && 10#$port <= 65535)) || die "invalid registry port $(printf %q "$port"): want 1-65535"
	fi
	[[ /$path/ =~ ^(/[a-z0-9]+([._-][a-z0-9]+)*)+/$ ]] ||
		die "invalid repository path $(printf %q "$path"): components are lowercase alphanumerics separated by . _ -"
}

# valid_platforms <list>: comma-separated os/arch[/variant] entries.
valid_platforms() {
	[[ $1 =~ ^[a-z0-9]+/[a-z0-9]+(/v[0-9]+)?(,[a-z0-9]+/[a-z0-9]+(/v[0-9]+)?)*$ ]] ||
		die "invalid BEKUP_PLATFORMS '$1': want e.g. linux/amd64,linux/arm64"
}

# --- configuration ------------------------------------------------------------
load_local_env .just.env
repo=${repo_arg:-${BEKUP_IMAGE_REPO:-}}
valid_repo "$repo"
platforms=${BEKUP_PLATFORMS:-linux/amd64,linux/arm64}
valid_platforms "$platforms"

# --- tree and tags ------------------------------------------------------------
git rev-parse --verify -q HEAD >/dev/null || die "the repository has no commits yet"
version=$(read_version)
full_sha=$(git rev-parse HEAD)
short_sha=$(git rev-parse --short=12 HEAD)
tags=()
# Files marked skip-worktree or assume-unchanged hide their changes from
# git status (lowercase or "S" status in `git ls-files -v`).
hidden=$(git ls-files -v | grep -c '^[a-zS]' || true)
if [[ -n $(git status --porcelain --untracked-files=all) ]] || ((hidden > 0)); then
	((hidden == 0)) || {
		((dirty_ok)) || die "${hidden} file(s) are marked skip-worktree or assume-unchanged (see: git ls-files -v | grep '^[a-zS]'); their changes are hidden from git status. Clear the flags, or pass --dirty to push only sha-${short_sha}-dirty"
	}
	((dirty_ok)) || die "working tree is not clean (commit or stash first, or pass --dirty to push only sha-${short_sha}-dirty)"
	warn "working tree is dirty: pushing only ${repo}:sha-${short_sha}-dirty (no ${version}, no latest)"
	tags=("${repo}:sha-${short_sha}-dirty")
	vcs_ref="${full_sha}-dirty"
	build_version="${version}+sha.${short_sha}.dirty"
	pristine=0
else
	tags=("${repo}:${version}" "${repo}:sha-${short_sha}" "${repo}:latest")
	vcs_ref=$full_sha
	build_version=$version
	pristine=1
	if [[ $(git rev-parse -q --verify "refs/tags/v${version}^{commit}" 2>/dev/null || true) != "$full_sha" ]]; then
		warn "HEAD is not the release tag v${version}; the image is still tagged ${version}"
		build_version="${version}+sha.${short_sha}"
	fi
fi
build_date=$(date -u +%Y-%m-%dT%H:%M:%SZ)
builder=bekup-publish
# SBOM generator used by BuildKit for --sbom, pinned by digest (D-061, D-202);
# Renovate updates the digest from the tag in the comment (renovate.json).
# renovate: image=docker/buildkit-syft-scanner:stable-1
sbom_generator=docker/buildkit-syft-scanner@sha256:873a7458c3c8ac8e32c7b796c10e1600b07b963d2aa000a72a2d97aaaa2d6315

tmp=$(mktemp -d "${TMPDIR:-/tmp}/bekup-publish.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
trap 'exit 130' INT TERM
meta="$tmp/metadata.json"
# A clean build uses a pristine export of HEAD as the context (ignored,
# untracked or hidden local files can never enter it); --dirty builds the
# working tree.
if ((pristine)); then
	context="$tmp/src"
else
	context=.
fi

# BUILDX_GIT_INFO=false: provenance gets no git remote URL or local paths
# (the revision is passed explicitly as VCS_REF).
cmd=(env BUILDX_GIT_INFO=false docker buildx build --builder "$builder" --platform "$platforms"
	--target runtime --pull
	--build-arg "VERSION=${build_version}" --build-arg "VCS_REF=${vcs_ref}" --build-arg "BUILD_DATE=${build_date}"
	--provenance=mode=max "--sbom=generator=${sbom_generator}"
	--metadata-file "$meta")
for t in "${tags[@]}"; do
	cmd+=(-t "$t")
done
cmd+=(--push "$context")

info "repository: $repo"
info "platforms:  $platforms"
info "version:    $build_version"
if ((pristine)); then
	info "context:    pristine export of HEAD ($short_sha, git archive)"
else
	info "context:    working tree (--dirty)"
fi
for t in "${tags[@]}"; do
	note "$t"
done

if ((dry_run)); then
	info "dry run: would run"
	sep=''
	for a in "${cmd[@]}"; do
		if [[ $a =~ ^[A-Za-z0-9_./:=,@+-]+$ ]]; then
			printf '%s%s' "$sep" "$a"
		else
			printf '%s%q' "$sep" "$a"
		fi
		sep=' '
	done
	printf '\n'
	exit 0
fi

need_cmd docker "https://docs.docker.com/get-docker/"
docker buildx version >/dev/null 2>&1 || die "docker buildx is not available (Docker Desktop ships it; Linux: apt install docker-buildx-plugin)"

if ((!assume_yes)); then
	if ! { : </dev/tty; } 2>/dev/null; then
		die "no terminal to confirm on; pass --yes to push without confirmation"
	fi
	printf 'Push %d tag(s) to %s? [y/N] ' "${#tags[@]}" "$repo" >/dev/tty
	read -r answer </dev/tty || answer=''
	[[ $answer == [yY] || $answer == [yY][eE][sS] ]] || die "aborted"
fi

# --- builder ------------------------------------------------------------------
# The builder must run on the local Docker: the current context's endpoint
# is a local socket, and an existing builder has exactly one node on that
# context (never a remote or another context's daemon).
context_name=$(docker context show 2>/dev/null || true)
[[ -n $context_name ]] || die "cannot determine the current docker context"
context_host=$(docker context inspect --format '{{.Endpoints.docker.Host}}' "$context_name" 2>/dev/null || true)
[[ $context_host == unix://* || $context_host == npipe://* ]] ||
	die "the current docker context $(printf %q "$context_name") is not the local Docker (endpoint $(printf %q "$context_host")); switch to a local context (docker context use ...)"
if inspect=$(docker buildx inspect "$builder" 2>/dev/null) && [[ -n $inspect ]]; then
	driver=$(awk '$1 == "Driver:" { print $2; exit }' <<<"$inspect")
	[[ $driver == docker-container ]] ||
		die "buildx builder '$builder' uses driver $(printf %q "$driver"); remove it (docker buildx rm $builder) so it is recreated with docker-container"
	endpoints=$(awk '$1 == "Endpoint:" { print $2 }' <<<"$inspect")
	[[ -n $endpoints && $endpoints != *$'\n'* && ($endpoints == "$context_name" || $endpoints == "$context_host") ]] ||
		die "buildx builder '$builder' does not run on the local docker context $(printf %q "$context_name") (endpoints: $(printf %q "${endpoints//$'\n'/,}")); remove it (docker buildx rm $builder) so it is recreated"
else
	info "creating buildx builder $builder (docker-container driver)"
	docker buildx create --name "$builder" --driver docker-container --bootstrap "$context_name" >/dev/null
fi

# --- build context ----------------------------------------------------------
if ((pristine)); then
	mkdir "$context"
	git archive --format=tar HEAD | tar -x -C "$context" -f -
fi

# --- build and push -----------------------------------------------------------
info "building and pushing (credentials come from your docker login)"
"${cmd[@]}"

digest=$(grep -Eo '"containerimage\.digest"[[:space:]]*:[[:space:]]*"sha256:[0-9a-f]{64}"' "$meta" | grep -Eo 'sha256:[0-9a-f]{64}' | head -n1 || true)
[[ -n $digest ]] || die "the push finished but buildx reported no image digest"

printf '\nPushed:\n'
printf '  %s\n' "${tags[@]}"
printf 'Digest: %s\n\n' "$digest"
printf 'Pin it in compose (.env or the environment):\n  BEKUP_IMAGE=%s@%s\n' "$repo" "$digest"
