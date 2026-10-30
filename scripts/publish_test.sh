#!/usr/bin/env bash
# publish_test.sh: tests scripts/publish.sh and load_local_env (scripts/lib.bash)
# in throwaway git repositories under a temp dir (run by `just test-scripts`).
# Docker is a stub that records its arguments: nothing is built or pushed.
set -uo pipefail

scripts="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
tmp=$(mktemp -d "${TMPDIR:-/tmp}/bekup-publish-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
trap 'exit 130' INT TERM
fail=0 count=0
export NO_COLOR=1
unset BEKUP_IMAGE_REPO BEKUP_PLATFORMS

export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL="$tmp/gitconfig"
git config --global user.name "publish test"
git config --global user.email test@example.invalid
git config --global init.defaultBranch main
git config --global commit.gpgsign false

# docker stub: logs every call; `buildx inspect` knows a builder only when
# STUB_DRIVER is set (one node per line of STUB_ENDPOINTS, default the
# context); `context show`/`inspect` report STUB_CONTEXT (desktop-linux) and
# STUB_CONTEXT_HOST (a local socket); `buildx build` writes a metadata file
# with a digest and records BUILDX_GIT_INFO and the context's file list.
mkdir -p "$tmp/bin"
cat >"$tmp/bin/docker" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$STUB_LOG"
case "$1 $2" in
"context show")
	printf '%s\n' "${STUB_CONTEXT-desktop-linux}"
	;;
"context inspect")
	printf '%s\n' "${STUB_CONTEXT_HOST-unix:///var/run/docker.sock}"
	;;
"buildx inspect")
	[[ -n ${STUB_DRIVER:-} ]] || exit 1
	printf 'Name:   bekup-publish\nDriver: %s\n\nNodes:\n' "$STUB_DRIVER"
	while IFS= read -r e; do
		printf 'Name:     bekup-publish0\nEndpoint: %s\n' "$e"
	done <<<"${STUB_ENDPOINTS-desktop-linux}"
	;;
"buildx build")
	printf 'BUILDX_GIT_INFO=%s\n' "${BUILDX_GIT_INFO-unset}" >>"$STUB_LOG"
	while (($#)); do
		[[ $1 == --metadata-file ]] && printf '{\n  "containerimage.digest": "sha256:%064d"\n}\n' 7 >"$2"
		last=$1
		shift
	done
	(cd "$last" && find . -mindepth 1 -maxdepth 1 | LC_ALL=C sort) >"$STUB_LOG.context"
	;;
esac
exit 0
STUB
chmod +x "$tmp/bin/docker"
export PATH="$tmp/bin:$PATH"
digest="sha256:$(printf '%064d' 7)"

new_repo() { # new_repo <name>: clean repo with VERSION 1.2.3 and the scripts
	local r="$tmp/$1"
	git init -q "$r"
	mkdir -p "$r/scripts"
	cp "$scripts/lib.bash" "$scripts/semver.sh" "$scripts/publish.sh" "$r/scripts/"
	printf '1.2.3\n' >"$r/VERSION"
	printf '# Changelog\n' >"$r/CHANGELOG.md"
	printf '.just.env\nlocal-only.cfg\n' >"$r/.gitignore"
	git -C "$r" add -A
	git -C "$r" commit -q -m "chore: initial"
}

# run <dir> <args...>: run publish.sh, capture stdout+stderr in $out, rc in $rc.
run() {
	local dir=$1
	shift
	: >"$tmp/docker.log"
	out=$(cd "$dir" && STUB_LOG="$tmp/docker.log" scripts/publish.sh "$@" 2>&1 </dev/null)
	rc=$?
}

refuses() { # refuses <description> <expected output fragment> <dir> <args...>
	local desc=$1 want=$2 dir=$3
	shift 3
	count=$((count + 1))
	run "$dir" "$@"
	if ((rc == 0)); then
		printf 'FAIL: %s: publish succeeded, want refusal\n%s\n' "$desc" "$out" >&2
		fail=1
	elif [[ $out != *"$want"* ]]; then
		printf 'FAIL: %s: want output containing "%s", got: %s\n' "$desc" "$want" "$out" >&2
		fail=1
	fi
}

succeeds() { # succeeds <description> <dir> <args...>
	local desc=$1 dir=$2
	shift 2
	count=$((count + 1))
	run "$dir" "$@"
	if ((rc != 0)); then
		printf 'FAIL: %s: exit %d: %s\n' "$desc" "$rc" "$out" >&2
		fail=1
	fi
}

has() { # has <description> <fragment>: the last run's output contains it
	count=$((count + 1))
	if [[ $out != *"$2"* ]]; then
		printf 'FAIL: %s: output lacks "%s":\n%s\n' "$1" "$2" "$out" >&2
		fail=1
	fi
}

lacks() { # lacks <description> <fragment>
	count=$((count + 1))
	if [[ $out == *"$2"* ]]; then
		printf 'FAIL: %s: output contains "%s":\n%s\n' "$1" "$2" "$out" >&2
		fail=1
	fi
}

check() { # check <description> <command...>
	local desc=$1
	shift
	count=$((count + 1))
	if ! "$@" >/dev/null 2>&1; then
		printf 'FAIL: %s\n' "$desc" >&2
		fail=1
	fi
}

file_lacks() { # file_lacks <description> <grep args...>: grep finds no match (status 1; 2 = unreadable fails too)
	local desc=$1 rc=0
	shift
	count=$((count + 1))
	grep -q "$@" || rc=$?
	if ((rc != 1)); then
		printf 'FAIL: %s\n' "$desc" >&2
		fail=1
	fi
}

new_repo r
R="$tmp/r"
short=$(git -C "$R" rev-parse --short=12 HEAD)

# --- arguments and repository validation ---------------------------------------
refuses "no repository" "no image repository" "$R" --dry-run
refuses "scheme" "must not contain a scheme" "$R" https://registry.example.com/bekup --dry-run
refuses "credentials" "must not contain credentials" "$R" user:pass@registry.example.com/bekup --dry-run
lacks "credentials are not echoed" "user:pass"
refuses "digest" "must not contain credentials or a digest" "$R" "registry.example.com/bekup@$digest" --dry-run
refuses "tag" "invalid image repository" "$R" registry.example.com/bekup:1.0 --dry-run
refuses "uppercase" "invalid image repository" "$R" registry.example.com/Bekup --dry-run
refuses "no registry host" "must start with a registry host" "$R" team/bekup --dry-run
refuses "no path" "invalid image repository" "$R" registry.example.com --dry-run
refuses "double slash" "invalid repository path" "$R" registry.example.com//bekup --dry-run
refuses "trailing slash" "invalid repository path" "$R" registry.example.com/bekup/ --dry-run
refuses "dot component" "invalid repository path" "$R" registry.example.com/../bekup --dry-run
refuses "shell metacharacters" "invalid image repository" "$R" 'registry.example.com/bekup;touch x' --dry-run
has "invalid repository quoted" 'registry.example.com/bekup\;touch\ x'
refuses "port too large" "invalid registry port" "$R" localhost:65536/bekup --dry-run
refuses "port zero" "invalid registry port" "$R" localhost:0/bekup --dry-run
succeeds "highest port" "$R" localhost:65535/bekup --dry-run
refuses "unknown option" "unknown option" "$R" --force
refuses "two repositories" "more than one repository" "$R" a.example/x b.example/y
succeeds "localhost with port" "$R" localhost:5000/bekup --dry-run
succeeds "nested path" "$R" registry.example.com/team/sub/bekup-db_x.y --dry-run
succeeds "docker.io" "$R" docker.io/someone/bekup --dry-run
count=$((count + 1))
BEKUP_PLATFORMS='linux/amd64;rm -rf /' run "$R" localhost:5000/bekup --dry-run
if ((rc == 0)) || [[ $out != *"invalid BEKUP_PLATFORMS"* ]]; then
	printf 'FAIL: invalid platforms accepted: %s\n' "$out" >&2
	fail=1
fi

# --- tags and dry run ----------------------------------------------------------
succeeds "clean dry run" "$R" localhost:5000/bekup --dry-run
has "version tag" "localhost:5000/bekup:1.2.3"
has "sha tag" "localhost:5000/bekup:sha-$short"
has "latest tag" "localhost:5000/bekup:latest"
has "buildx command" "env BUILDX_GIT_INFO=false docker buildx build --builder bekup-publish --platform linux/amd64,linux/arm64 --target runtime --pull"
has "build args (untagged: build metadata)" "--build-arg VERSION=1.2.3+sha.$short --build-arg VCS_REF=$(git -C "$R" rev-parse HEAD) --build-arg BUILD_DATE="
has "not-the-tag warning" "HEAD is not the release tag v1.2.3"
has "pristine context" "pristine export of HEAD"
has "attestations" "--provenance=mode=max --sbom=generator=docker/buildkit-syft-scanner@sha256:"
count=$((count + 1))
[[ $out =~ --sbom=generator=docker/buildkit-syft-scanner@sha256:[0-9a-f]{64}\ --metadata-file ]] || {
	printf 'FAIL: SBOM generator not pinned by a full digest: %s\n' "$out" >&2
	fail=1
}
count=$((count + 1))
[[ $out =~ -t\ localhost:5000/bekup:latest\ --push\ [^[:space:]]+/src$ ]] || {
	printf 'FAIL: clean builds do not use the exported context: %s\n' "$out" >&2
	fail=1
}
check "dry run calls no docker" test ! -s "$tmp/docker.log"
count=$((count + 1))
BEKUP_PLATFORMS=linux/arm64 run "$R" localhost:5000/bekup --dry-run
[[ $out == *"--platform linux/arm64 "* ]] || {
	printf 'FAIL: BEKUP_PLATFORMS not used: %s\n' "$out" >&2
	fail=1
}

# --- dirty tree ----------------------------------------------------------------
printf 'x\n' >"$R/untracked.txt"
refuses "dirty refused" "pass --dirty" "$R" localhost:5000/bekup --dry-run
succeeds "dirty allowed" "$R" localhost:5000/bekup --dirty --dry-run
has "dirty tag" "-t localhost:5000/bekup:sha-$short-dirty --push ."
has "dirty version" "--build-arg VERSION=1.2.3+sha.$short.dirty "
has "dirty context" "working tree (--dirty)"
has "dirty warning" "working tree is dirty"
lacks "no version tag when dirty" "localhost:5000/bekup:1.2.3"
lacks "no latest when dirty" "localhost:5000/bekup:latest"
lacks "no clean sha tag when dirty" "bekup:sha-$short "
rm "$R/untracked.txt"
printf 'y\n' >>"$R/CHANGELOG.md"
refuses "modified file refused" "not clean" "$R" localhost:5000/bekup --dry-run
git -C "$R" checkout -q CHANGELOG.md

# Changes hidden from git status by skip-worktree / assume-unchanged.
for flag in skip-worktree assume-unchanged; do
	git -C "$R" update-index "--$flag" CHANGELOG.md
	printf 'hidden\n' >>"$R/CHANGELOG.md"
	refuses "$flag refused" "skip-worktree or assume-unchanged" "$R" localhost:5000/bekup --dry-run
	succeeds "$flag with --dirty" "$R" localhost:5000/bekup --dirty --dry-run
	has "$flag dirty tag" "bekup:sha-$short-dirty"
	git -C "$R" update-index "--no-$flag" CHANGELOG.md
	git -C "$R" checkout -q CHANGELOG.md
done

# --- .just.env -----------------------------------------------------------------
E="$R/.just.env"
write_env() { # write_env <content>: private .just.env
	printf '%s' "$1" >"$E"
	chmod 0600 "$E"
}
write_env $'# comment\n\nBEKUP_IMAGE_REPO=registry.example.com/from/file\nBEKUP_PLATFORMS="linux/arm64"\n'
succeeds ".just.env repo" "$R" --dry-run
has ".just.env repo used" "registry.example.com/from/file:latest"
has ".just.env quoted platforms" "--platform linux/arm64 "
lacks ".just.env keeps the tree clean" "dirty"
count=$((count + 1))
BEKUP_IMAGE_REPO=registry.example.com/from/env run "$R" --dry-run
[[ $out == *"registry.example.com/from/env:latest"* && $out != *"from/file"* ]] || {
	printf 'FAIL: environment does not win over .just.env: %s\n' "$out" >&2
	fail=1
}
count=$((count + 1))
BEKUP_IMAGE_REPO=registry.example.com/from/env run "$R" registry.example.com/from/arg --dry-run
[[ $out == *"registry.example.com/from/arg:latest"* && $out != *"from/env"* ]] || {
	printf 'FAIL: argument does not win over the environment: %s\n' "$out" >&2
	fail=1
}

write_env $'export BEKUP_IMAGE_REPO=\'registry.example.com/single\'\nOTHER_SETTING=hunter2-value\nDOCKER_HOST=tcp://evil.example:2375\n'
succeeds "unknown keys ignored" "$R" --dry-run
has "export prefix and single quotes" "registry.example.com/single:latest"
has "unknown key warned" "ignoring unknown key OTHER_SETTING"
has "unknown DOCKER_HOST warned" "ignoring unknown key DOCKER_HOST"
lacks "unknown value never printed" "hunter2-value"
lacks "unknown value never printed (2)" "evil.example"

write_env $'BEKUP_IMAGE_REPO=$(touch '"$tmp"'/pwned1)\n'
refuses "command substitution is data" "invalid image repository" "$R" --dry-run
write_env $'touch '"$tmp"'/pwned2\nBEKUP_IMAGE_REPO=`touch '"$tmp"'/pwned3`\n'
refuses "bare command refused" "not a KEY=value line" "$R" --dry-run
write_env $'BEKUP_IMAGE_REPO=localhost:5000/bekup; touch '"$tmp"'/pwned4\n'
refuses "trailing command is data" "invalid image repository" "$R" --dry-run
check "nothing was executed" test ! -e "$tmp/pwned1" -a ! -e "$tmp/pwned2" -a ! -e "$tmp/pwned3" -a ! -e "$tmp/pwned4"

write_env $'BEKUP_IMAGE_REPO=localhost:5000/bekup\n'
chmod 0620 "$E"
refuses "group-writable refused" "group- or world-writable" "$R" --dry-run
chmod 0602 "$E"
refuses "world-writable refused" "group- or world-writable" "$R" --dry-run
chmod 0644 "$E"
succeeds "0644 accepted" "$R" --dry-run
rm "$E"
printf 'BEKUP_IMAGE_REPO=localhost:5000/bekup\n' >"$tmp/target.env"
chmod 0600 "$tmp/target.env"
ln -s "$tmp/target.env" "$E"
refuses "symlink refused" "is a symlink" "$R" --dry-run
rm "$E"
mkdir "$E"
refuses "directory refused" "not a regular file" "$R" --dry-run
rmdir "$E"
write_env "BEKUP_IMAGE_REPO=localhost:5000/$(printf 'a%.0s' {1..5000})"
refuses "oversized refused" "larger than 4 KiB" "$R" --dry-run
rm "$E"

# load_local_env directly: exports allowed keys, keeps environment values,
# never defines unknown keys, strips CRLF.
count=$((count + 1))
printf 'BEKUP_IMAGE_REPO=a.example/file\r\nBEKUP_PLATFORMS=linux/amd64\r\nUNKNOWN_KEY=1\r\n' >"$tmp/unit.env"
chmod 0600 "$tmp/unit.env"
got=$(
	BEKUP_PLATFORMS=linux/arm64
	# shellcheck source=scripts/lib.bash
	source "$scripts/lib.bash"
	load_local_env "$tmp/unit.env" 2>/dev/null
	printf '%s|%s|%s|' "$BEKUP_IMAGE_REPO" "$BEKUP_PLATFORMS" "${UNKNOWN_KEY-unset}"
	bash -c 'printf %s "$BEKUP_IMAGE_REPO"'
)
[[ $got == "a.example/file|linux/arm64|unset|a.example/file" ]] || {
	printf 'FAIL: load_local_env: got %s\n' "$got" >&2
	fail=1
}
count=$((count + 1))
# shellcheck source=scripts/lib.bash
if ! (source "$scripts/lib.bash" && load_local_env "$tmp/missing.env"); then
	echo "FAIL: load_local_env: a missing file must be fine" >&2
	fail=1
fi

# --- push (docker stub) ----------------------------------------------------------
if (: </dev/tty) 2>/dev/null; then
	printf 'publish_test: note: a terminal is attached, skipping the no-terminal confirmation case\n'
else
	refuses "confirmation needs a terminal" "pass --yes" "$R" localhost:5000/bekup
	file_lacks "no build without confirmation" -E "^buildx (build|create)" "$tmp/docker.log"
fi

printf 'local\n' >"$R/local-only.cfg"
succeeds "push with new builder" "$R" localhost:5000/bekup --yes
check "builder created on the local context" grep -qx 'buildx create --name bekup-publish --driver docker-container --bootstrap desktop-linux' "$tmp/docker.log"
check "build run" grep -q '^buildx build --builder bekup-publish ' "$tmp/docker.log"
check "no git info in provenance" grep -qx 'BUILDX_GIT_INFO=false' "$tmp/docker.log"
check "context is the export of HEAD" grep -qx './VERSION' "$tmp/docker.log.context"
file_lacks "ignored files stay out of the context" local-only "$tmp/docker.log.context"
file_lacks "no .git in the context" -x ./.git "$tmp/docker.log.context"
rm "$R/local-only.cfg"
has "digest printed" "Digest: $digest"
has "pin hint" "BEKUP_IMAGE=localhost:5000/bekup:1.2.3@$digest"
has "pin hint: version mismatch noted" "needs --allow-version-mismatch"
has "pushed tags listed" "  localhost:5000/bekup:sha-$short"

count=$((count + 1))
STUB_DRIVER=docker-container run "$R" localhost:5000/bekup --yes
if ((rc != 0)) || grep -q 'buildx create' "$tmp/docker.log"; then
	printf 'FAIL: existing docker-container builder not reused: %s\n' "$out" >&2
	fail=1
fi
count=$((count + 1))
STUB_DRIVER=docker run "$R" localhost:5000/bekup --yes
if ((rc == 0)) || [[ $out != *"uses driver docker"* ]] || grep -q 'buildx build' "$tmp/docker.log"; then
	printf 'FAIL: builder with the wrong driver accepted: %s\n' "$out" >&2
	fail=1
fi
count=$((count + 1))
STUB_DRIVER=docker-container STUB_ENDPOINTS=remote-builder run "$R" localhost:5000/bekup --yes
if ((rc == 0)) || [[ $out != *"does not run on the local docker context"* ]] || grep -q 'buildx build' "$tmp/docker.log"; then
	printf 'FAIL: builder on another context accepted: %s\n' "$out" >&2
	fail=1
fi
count=$((count + 1))
STUB_DRIVER=docker-container STUB_ENDPOINTS=$'desktop-linux\nremote-builder' run "$R" localhost:5000/bekup --yes
if ((rc == 0)) || [[ $out != *"does not run on the local docker context"* ]] || grep -q 'buildx build' "$tmp/docker.log"; then
	printf 'FAIL: builder with a second node accepted: %s\n' "$out" >&2
	fail=1
fi
count=$((count + 1))
STUB_CONTEXT_HOST=tcp://docker.example:2376 run "$R" localhost:5000/bekup --yes
if ((rc == 0)) || [[ $out != *"is not the local Docker"* ]] || grep -Eq 'buildx (build|create)' "$tmp/docker.log"; then
	printf 'FAIL: remote docker context accepted: %s\n' "$out" >&2
	fail=1
fi

# On the release tag: the plain version, no warning.
git -C "$R" tag v1.2.3
succeeds "tagged dry run" "$R" localhost:5000/bekup --dry-run
has "tagged version" "--build-arg VERSION=1.2.3 "
lacks "no warning on the tag" "HEAD is not the release tag"
git -C "$R" tag -d v1.2.3 >/dev/null

check "no git tags created" test -z "$(git -C "$R" tag -l)"
check "CHANGELOG untouched" test -z "$(git -C "$R" status --porcelain)"
check "no commits added" test "$(git -C "$R" rev-list --count HEAD)" = 1

if ((fail == 0)); then
	printf 'publish_test: %d cases passed\n' "$count"
fi
exit "$fail"
