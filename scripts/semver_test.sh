#!/bin/sh
# semver_test.sh: table tests for scripts/semver.sh (run by `just test-scripts`).
set -u

semver="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)/semver.sh"
fail=0 count=0

ok() { # ok <expected> <args...>
	exp=$1
	shift
	count=$((count + 1))
	got=$("$semver" next "$@" 2>/dev/null) || got="<error>"
	if [ "$got" != "$exp" ]; then
		printf 'FAIL: semver next %s: got %s, want %s\n' "$*" "$got" "$exp" >&2
		fail=1
	fi
}

bad() { # bad <args...>: must fail
	count=$((count + 1))
	if out=$("$semver" next "$@" 2>/dev/null); then
		printf 'FAIL: semver next %s: want error, got %s\n' "$*" "$out" >&2
		fail=1
	fi
}

ok 0.0.1 0.0.0 patch
ok 0.1.0 0.0.9 minor
ok 1.0.0 0.9.9 major
ok 1.2.4 1.2.3 patch
ok 1.3.0 1.2.3 minor
ok 2.0.0 1.2.3 major
ok 1.2.10 1.2.9 patch
ok 1.10.0 1.9.7 minor
ok 10.0.0 9.4.2 major
ok 1.2.4 1.2.3 1.2.4
ok 1.3.0 1.2.3 1.3.0
ok 2.0.0 1.2.3 2.0.0
ok 1.10.0 1.9.0 1.10.0
ok 0.0.1 0.0.0 0.0.1

bad 1.2.3 1.2.3      # same version
bad 1.2.3 1.2.2      # downgrade
bad 1.2.3 1.1.9      # downgrade (minor)
bad 1.2.3 0.9.9      # downgrade (major)
bad 1.2.3 v1.2.4     # "v" prefix
bad 1.2.3 1.2.4-rc.1 # pre-release
bad 1.2.3 1.2.4+b1   # build metadata
bad 1.2.3 01.2.4     # leading zero
bad 1.2.3 1.2        # too short
bad 1.2.3 1.2.3.4    # too long
bad 1.2.3 ''         # empty
bad 1.2.3 PATCH      # case-sensitive
bad 1.2.3 '1.2.4;id' # shell metacharacters
bad 1.2.3 "$(printf '1.2.4\n1.2.5')" # embedded newline
bad 1.2.3 1234567890.0.0 # component too large
bad 1.2.x patch      # invalid current
bad '' patch         # empty current
bad 999999999.0.0 major # overflow beyond 9 digits
bad 1.2.3            # missing argument

count=$((count + 1))
"$semver" valid 1.2.3 || { echo "FAIL: valid 1.2.3" >&2; fail=1; }
count=$((count + 1))
if "$semver" valid 1.2.3-rc1 2>/dev/null; then echo "FAIL: valid 1.2.3-rc1" >&2; fail=1; fi

if [ "$fail" = 0 ]; then
	printf 'semver_test: %d cases passed\n' "$count"
fi
exit "$fail"
