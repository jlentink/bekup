#!/bin/sh
# semver.sh: strict semantic-version helpers for `just bump` (task 0.1b, D-072).
#
# Usage:
#   semver.sh valid <version>            exit 0 if <version> is a plain x.y.z
#   semver.sh next  <current> <bump>     print the next version, where <bump> is
#                                        patch | minor | major | x.y.z
#   semver.sh gt    <a> <b>              exit 0 if a > b, 1 if not (both x.y.z;
#                                        2 on invalid input). Used by the release
#                                        workflow's `latest` rule (D-204).
#
# Only plain MAJOR.MINOR.PATCH is accepted: no "v" prefix, no leading zeros, no
# pre-release or build metadata, at most 9 digits per component (keeps shell
# arithmetic far away from overflow). An explicit x.y.z must be strictly greater
# than <current>: bump never goes backwards or re-releases a version.
set -eu

die() {
	printf 'semver: %s\n' "$*" >&2
	exit 1
}

is_valid() {
	printf '%s\n' "$1" | grep -Eq '^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$' &&
		[ "$(printf '%s' "$1" | wc -l | tr -d ' ')" = 0 ]
}

# split <version>: sets MA, MI, PA.
split() {
	MA=${1%%.*}
	rest=${1#*.}
	MI=${rest%%.*}
	PA=${rest#*.}
}

# greater <a> <b>: exit 0 if a > b (both valid).
greater() {
	split "$1"
	a1=$MA a2=$MI a3=$PA
	split "$2"
	[ "$a1" -gt "$MA" ] && return 0
	[ "$a1" -lt "$MA" ] && return 1
	[ "$a2" -gt "$MI" ] && return 0
	[ "$a2" -lt "$MI" ] && return 1
	[ "$a3" -gt "$PA" ]
}

cmd=${1:-}
case $cmd in
valid)
	[ $# -eq 2 ] || die "usage: semver.sh valid <version>"
	is_valid "$2"
	;;
next)
	[ $# -eq 3 ] || die "usage: semver.sh next <current> <patch|minor|major|x.y.z>"
	current=$2
	bump=$3
	is_valid "$current" || die "current version '$current' is not a valid x.y.z version"
	split "$current"
	case $bump in
	patch) next="$MA.$MI.$((PA + 1))" ;;
	minor) next="$MA.$((MI + 1)).0" ;;
	major) next="$((MA + 1)).0.0" ;;
	*)
		is_valid "$bump" || die "'$bump' is not patch, minor, major or a plain x.y.z version"
		greater "$bump" "$current" || die "$bump is not greater than the current version $current"
		next=$bump
		;;
	esac
	is_valid "$next" || die "resulting version '$next' is out of range"
	printf '%s\n' "$next"
	;;
gt)
	[ $# -eq 3 ] || { printf 'semver: usage: semver.sh gt <a> <b>\n' >&2; exit 2; }
	is_valid "$2" || { printf "semver: '%s' is not a valid x.y.z version\n" "$2" >&2; exit 2; }
	is_valid "$3" || { printf "semver: '%s' is not a valid x.y.z version\n" "$3" >&2; exit 2; }
	greater "$2" "$3"
	;;
*)
	die "usage: semver.sh valid <version> | semver.sh gt <a> <b> | semver.sh next <current> <patch|minor|major|x.y.z>"
	;;
esac
