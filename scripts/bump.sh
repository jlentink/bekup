#!/bin/sh
# bump.sh: release version bump for `just bump` (task 0.1b, D-072).
#
# Usage: scripts/bump.sh [patch|minor|major|x.y.z]   (run from the repo root)
#
# Refuses unless: inside a git repo with at least one commit, on branch main,
# the working tree is completely clean (untracked files included), the target
# tag does not exist yet and git-cliff is installed. Then it updates VERSION,
# web/package.json (if present) and CHANGELOG.md, creates a signed commit and
# a signed annotated tag vX.Y.Z. Nothing is pushed: see `just release`.
set -eu

here=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
bump=${1:-patch}

die() {
	printf 'bump: %s\n' "$*" >&2
	exit 1
}

# --- preflight: refuse early, before touching any file ---------------------
git rev-parse --is-inside-work-tree >/dev/null 2>&1 || die "not inside a git repository"
cd "$(git rev-parse --show-toplevel)"
git rev-parse --verify -q HEAD >/dev/null || die "the repository has no commits yet; commit first"
branch=$(git symbolic-ref --short -q HEAD || true)
[ "$branch" = main ] || die "releases are only cut from 'main' (current: ${branch:-detached HEAD})"
if [ -n "$(git status --porcelain --untracked-files=all)" ]; then
	die "working tree is not clean (commit, stash or remove changes and untracked files first)"
fi
[ -f VERSION ] || die "VERSION file not found"
current=$(tr -d ' \t\r\n' <VERSION)
next=$("$here/semver.sh" next "$current" "$bump") || exit 1
tag="v$next"
if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
	die "tag $tag already exists"
fi
command -v git-cliff >/dev/null 2>&1 ||
	die "git-cliff is not installed (brew install git-cliff | cargo install git-cliff | https://git-cliff.org/docs/installation)"
if [ -f web/package.json ]; then
	command -v node >/dev/null 2>&1 || die "web/package.json exists but node is not installed"
fi

# --- apply: roll back file changes if anything fails before the commit -----
committed=0
rollback() {
	status=$?
	if [ "$committed" = 0 ] && [ "$status" != 0 ]; then
		printf 'bump: failed, restoring the working tree\n' >&2
		# The tree was verified clean above, so a hard reset only drops our edits.
		git reset -q --hard HEAD 2>/dev/null || true
	fi
	exit "$status"
}
trap rollback EXIT
trap 'exit 130' INT TERM

printf 'bump: %s -> %s\n' "$current" "$next"
printf '%s\n' "$next" >VERSION
files="VERSION CHANGELOG.md"
if [ -f web/package.json ]; then
	# Rewrites only the "version" field (key order kept). The lockfiles do not
	# record the root package version for this single-package workspace.
	node -e '
		const fs = require("fs");
		const p = "web/package.json";
		const pkg = JSON.parse(fs.readFileSync(p, "utf8"));
		pkg.version = process.argv[1];
		fs.writeFileSync(p, JSON.stringify(pkg, null, 2) + "\n");
	' "$next"
	files="$files web/package.json"
fi
git-cliff --config cliff.toml --tag "$tag" --output CHANGELOG.md

# shellcheck disable=SC2086 # $files is a controlled, space-free list
git add -- $files
git commit -q -S -m "chore(release): $tag"
committed=1
git tag -s -a "$tag" -m "bekup $tag"
if ! git verify-tag "$tag" >/dev/null 2>&1; then
	printf 'bump: warning: could not verify the signature of %s locally (for SSH signing, set gpg.ssh.allowedSignersFile)\n' "$tag" >&2
fi

printf 'bump: created signed commit and tag %s\n' "$tag"
printf 'bump: review with "git show %s", then publish with "just release"\n' "$tag"
