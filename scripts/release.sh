#!/bin/sh
# release.sh: publish the release created by `just bump` (task 0.1b, D-072).
#
# Usage: scripts/release.sh [remote]   (default remote: origin)
#
# Refuses unless HEAD of a clean main branch carries the signed tag
# v$(cat VERSION) and that signature verifies locally. Then pushes main and the
# tag atomically; the tag triggers the release workflow (task 7.4).
set -eu

remote=${1:-origin}

die() {
	printf 'release: %s\n' "$*" >&2
	exit 1
}

git rev-parse --is-inside-work-tree >/dev/null 2>&1 || die "not inside a git repository"
cd "$(git rev-parse --show-toplevel)"
git rev-parse --verify -q HEAD >/dev/null || die "the repository has no commits yet"
branch=$(git symbolic-ref --short -q HEAD || true)
[ "$branch" = main ] || die "releases are only published from 'main' (current: ${branch:-detached HEAD})"
if [ -n "$(git status --porcelain --untracked-files=all)" ]; then
	die "working tree is not clean"
fi
version=$(tr -d ' \t\r\n' <VERSION)
"$(dirname -- "$0")/semver.sh" valid "$version" || die "VERSION '$version' is not a valid x.y.z version"
tag="v$version"
git rev-parse -q --verify "refs/tags/$tag" >/dev/null || die "tag $tag does not exist; run 'just bump' first"
[ "$(git rev-parse "$tag^{commit}")" = "$(git rev-parse HEAD)" ] ||
	die "tag $tag does not point at HEAD"
[ "$(git cat-file -t "$tag")" = tag ] || die "tag $tag is not an annotated tag"
git verify-tag "$tag" >/dev/null 2>&1 ||
	die "the signature of $tag does not verify (for SSH signing, set gpg.ssh.allowedSignersFile)"
git remote get-url "$remote" >/dev/null 2>&1 || die "git remote '$remote' does not exist"

printf 'release: pushing main and %s to %s\n' "$tag" "$remote"
git push --atomic "$remote" main "refs/tags/$tag"
