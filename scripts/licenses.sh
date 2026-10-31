#!/usr/bin/env bash
# licenses.sh: the setup wizard's dependencies (charmbracelet bubbletea,
# huh, lipgloss and everything they pull in; D-208) carry only permissive
# licenses: MIT, BSD, Apache-2.0 or ISC. Run by `just sec`.
#
# Usage: scripts/licenses.sh [package ...]   (default: ./internal/setup/wizard)
#
# For every module in the dependency closure of the packages (outside the
# standard library and this module) it reads the module's LICENSE file from
# the module cache and classifies it by its text; a missing or unknown
# license fails, and so does a failing `go list` or an empty closure. A
# module replaced by a local directory (third_party/clipboard, D-208) is
# checked in that directory.
set -euo pipefail

cd "$(dirname -- "$0")/.."
pkgs=("$@")
[[ ${#pkgs[@]} -gt 0 ]] || pkgs=(./internal/setup/wizard)
main=$(go list -m)
# go list runs first, on its own: its exit status is checked (a failure in
# a process substitution would go unnoticed and pass with zero modules).
if ! deps=$(go list -deps -f '{{with .Module}}{{.Path}} {{.Dir}}{{end}}' "${pkgs[@]}"); then
	printf 'licenses: go list failed for %s\n' "${pkgs[*]}" >&2
	exit 1
fi
fail=0
n=0
while read -r mod dir; do
	[[ -n $mod && $mod != "$main" ]] || continue
	n=$((n + 1))
	lic=""
	for f in "$dir"/LICENSE "$dir"/LICENSE.md "$dir"/LICENSE.txt "$dir"/LICENCE "$dir"/COPYING; do
		[[ -f $f ]] && lic=$f && break
	done
	if [[ -z $lic ]]; then
		printf 'licenses: %s: no LICENSE file\n' "$mod" >&2
		fail=1
		continue
	fi
	text=$(tr -s '[:space:]' ' ' <"$lic")
	case $text in
	*"Permission is hereby granted, free of charge"*) kind=MIT ;;
	*"Apache License"*"Version 2.0"*) kind=Apache-2.0 ;;
	*"Redistribution and use in source and binary forms"*) kind=BSD ;;
	*"Permission to use, copy, modify, and/or distribute this software"*) kind=ISC ;;
	*)
		printf 'licenses: %s: license not on the allowlist (MIT, BSD, Apache-2.0, ISC): %s\n' "$mod" "$lic" >&2
		fail=1
		continue
		;;
	esac
	printf '  %-60s %s\n' "$mod" "$kind"
done < <(printf '%s\n' "$deps" | sort -u)
((fail == 0)) || exit 1
if ((n == 0)); then
	printf 'licenses: no third-party modules found for %s (expected the charm modules)\n' "${pkgs[*]}" >&2
	exit 1
fi
printf 'licenses: %d modules, all MIT, BSD, Apache-2.0 or ISC\n' "$n"
