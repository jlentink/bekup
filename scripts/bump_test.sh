#!/bin/sh
# bump_test.sh: tests scripts/bump.sh refusals and the happy path in throwaway
# git repositories under a temp dir (run by `just test-scripts`). Uses an
# isolated git config, a throwaway SSH signing key and a git-cliff stub.
set -eu

scripts="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
bump="$scripts/bump.sh"
tmp=$(mktemp -d "${TMPDIR:-/tmp}/bekup-bump-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT TERM
fail=0 count=0

# Isolated git configuration with SSH commit/tag signing.
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL="$tmp/gitconfig"
ssh-keygen -q -t ed25519 -N '' -C bump-test -f "$tmp/signing" >/dev/null
printf 'test@example.invalid %s\n' "$(cat "$tmp/signing.pub")" >"$tmp/allowed_signers"
git config --global user.name "bump test"
git config --global user.email test@example.invalid
git config --global init.defaultBranch main
git config --global gpg.format ssh
git config --global user.signingkey "$tmp/signing.pub"
git config --global gpg.ssh.allowedSignersFile "$tmp/allowed_signers"

# git-cliff stub: writes a recognizable changelog (or fails on demand).
mkdir -p "$tmp/bin"
cat >"$tmp/bin/git-cliff" <<'STUB'
#!/bin/sh
[ -n "${CLIFF_FAIL:-}" ] && { echo "stub git-cliff: forced failure" >&2; exit 1; }
tag='' out=''
while [ $# -gt 0 ]; do
	case $1 in --tag) tag=$2; shift ;; --output) out=$2; shift ;; esac
	shift
done
printf '# Changelog\n\n## [%s]\n' "$tag" >"$out"
STUB
chmod +x "$tmp/bin/git-cliff"
stub_path="$tmp/bin:$PATH"

new_repo() { # new_repo <name>: repo with one commit on main
	r="$tmp/$1"
	git init -q "$r"
	printf '0.0.0\n' >"$r/VERSION"
	printf '# Changelog\n' >"$r/CHANGELOG.md"
	if command -v node >/dev/null 2>&1; then
		mkdir -p "$r/web"
		printf '{\n  "name": "bekup-web",\n  "version": "0.0.0",\n  "private": true\n}\n' >"$r/web/package.json"
	fi
	git -C "$r" add -A
	git -C "$r" commit -q -m "chore: initial"
}

refuses() { # refuses <description> <expected stderr fragment> <dir> <args...>
	desc=$1 want=$2 dir=$3
	shift 3
	count=$((count + 1))
	if err=$(cd "$dir" && PATH="$stub_path" "$bump" "$@" 2>&1 >/dev/null); then
		printf 'FAIL: %s: bump succeeded, want refusal\n' "$desc" >&2
		fail=1
	elif ! printf '%s' "$err" | grep -qF -- "$want"; then
		printf 'FAIL: %s: want error containing "%s", got: %s\n' "$desc" "$want" "$err" >&2
		fail=1
	fi
}

check() { # check <description> <command...>
	desc=$1
	shift
	count=$((count + 1))
	if ! "$@" >/dev/null 2>&1; then
		printf 'FAIL: %s\n' "$desc" >&2
		fail=1
	fi
}

# --- refusals ---------------------------------------------------------------
git init -q "$tmp/empty"
printf '0.0.0\n' >"$tmp/empty/VERSION"
refuses "no commits" "no commits" "$tmp/empty" patch

mkdir -p "$tmp/nogit"
refuses "outside git" "not inside a git repository" "$tmp/nogit" patch

new_repo branch
git -C "$tmp/branch" switch -q -c feature
refuses "not on main" "only cut from 'main'" "$tmp/branch" patch
git -C "$tmp/branch" checkout -q --detach
refuses "detached HEAD" "detached HEAD" "$tmp/branch" patch

new_repo dirty
touch "$tmp/dirty/untracked.txt"
refuses "untracked file" "not clean" "$tmp/dirty" patch
rm "$tmp/dirty/untracked.txt"
printf 'x\n' >>"$tmp/dirty/CHANGELOG.md"
refuses "modified file" "not clean" "$tmp/dirty" patch
git -C "$tmp/dirty" add CHANGELOG.md
refuses "staged change" "not clean" "$tmp/dirty" patch

new_repo args
refuses "invalid level" "is not patch, minor, major" "$tmp/args" bogus
refuses "downgrade" "not greater" "$tmp/args" 0.0.0
refuses "pre-release" "is not patch, minor, major" "$tmp/args" 1.0.0-rc.1
git -C "$tmp/args" tag v0.0.1
refuses "existing tag" "already exists" "$tmp/args" patch
git -C "$tmp/args" tag -d v0.0.1 >/dev/null

if command -v git-cliff >/dev/null 2>&1; then
	printf 'bump_test: note: git-cliff is installed, skipping the missing-git-cliff case\n'
else
	count=$((count + 1))
	if err=$(cd "$tmp/args" && "$bump" patch 2>&1 >/dev/null); then
		echo "FAIL: missing git-cliff: bump succeeded" >&2
		fail=1
	elif ! printf '%s' "$err" | grep -qF "git-cliff is not installed"; then
		printf 'FAIL: missing git-cliff: unexpected error: %s\n' "$err" >&2
		fail=1
	fi
fi

# --- rollback when changelog generation fails --------------------------------
export CLIFF_FAIL=1
refuses "git-cliff failure" "restoring the working tree" "$tmp/args" minor
unset CLIFF_FAIL
check "rollback leaves a clean tree" test -z "$(git -C "$tmp/args" status --porcelain --untracked-files=all)"
check "rollback keeps VERSION" grep -qx 0.0.0 "$tmp/args/VERSION"
check "rollback creates no tag" test -z "$(git -C "$tmp/args" tag -l)"

# --- happy path --------------------------------------------------------------
new_repo happy
count=$((count + 1))
if ! (cd "$tmp/happy" && PATH="$stub_path" "$bump" minor >/dev/null 2>&1); then
	echo "FAIL: happy path: bump minor failed" >&2
	(cd "$tmp/happy" && PATH="$stub_path" "$bump" minor) >&2 || true
	fail=1
fi
h="$tmp/happy"
check "VERSION is 0.1.0" grep -qx 0.1.0 "$h/VERSION"
check "CHANGELOG regenerated" grep -qF '## [v0.1.0]' "$h/CHANGELOG.md"
check "tag v0.1.0 is annotated" test "$(git -C "$h" cat-file -t v0.1.0 2>/dev/null)" = tag
check "tag v0.1.0 points at HEAD" test "$(git -C "$h" rev-parse 'v0.1.0^{commit}')" = "$(git -C "$h" rev-parse HEAD)"
check "tag signature verifies" git -C "$h" verify-tag v0.1.0
check "commit signature verifies" git -C "$h" verify-commit HEAD
check "commit message" test "$(git -C "$h" log -1 --format=%s)" = "chore(release): v0.1.0"
check "tree clean after bump" test -z "$(git -C "$h" status --porcelain --untracked-files=all)"
if [ -f "$h/web/package.json" ]; then
	check "web/package.json version" grep -q '"version": "0.1.0"' "$h/web/package.json"
fi
count=$((count + 1))
if ! (cd "$h" && PATH="$stub_path" "$bump" 1.0.0 >/dev/null 2>&1); then
	echo "FAIL: explicit bump to 1.0.0 failed" >&2
	fail=1
fi
check "VERSION is 1.0.0" grep -qx 1.0.0 "$h/VERSION"
check "tag v1.0.0 exists" git -C "$h" rev-parse -q --verify refs/tags/v1.0.0

if [ "$fail" = 0 ]; then
	printf 'bump_test: %d cases passed\n' "$count"
fi
exit "$fail"
