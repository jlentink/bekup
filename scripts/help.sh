#!/bin/sh
# help.sh: styled help screen for `just` without arguments (task 0.1b, D-071).
#
# Renders a banner with the version from VERSION, then the recipes from
# `just --list`, grouped and colored. Colors are disabled when NO_COLOR is set
# (https://no-color.org/) or stdout is not a terminal; FORCE_COLOR forces them.
set -eu

just_bin=${JUST:-just}
justfile=${JUSTFILE:-justfile}
root=$(dirname -- "$justfile")
version=$(tr -d ' \t\r\n' <"$root/VERSION" 2>/dev/null || echo unknown)

color=0
if [ -z "${NO_COLOR:-}" ] && { [ -t 1 ] || [ -n "${FORCE_COLOR:-}" ]; }; then
	color=1
fi

if [ "$color" = 1 ]; then
	esc=$(printf '\033')
	B="${esc}[1m" D="${esc}[2m" R="${esc}[0m"
	G1="${esc}[38;5;45m" G2="${esc}[38;5;39m" G3="${esc}[38;5;33m" G4="${esc}[38;5;63m" G5="${esc}[38;5;99m" G6="${esc}[38;5;135m"
	HEAD="${esc}[1;38;5;213m" NAME="${esc}[1;38;5;87m" ARG="${esc}[38;5;221m" DOC="${esc}[38;5;252m" RULE="${esc}[38;5;240m"
else
	B='' D='' R='' G1='' G2='' G3='' G4='' G5='' G6='' HEAD='' NAME='' ARG='' DOC='' RULE=''
fi

cat <<BANNER

${G1}   _          _                    ${R}
${G2}  | |__   ___| | ___   _ _ __     ${R}
${G3}  | '_ \\ / _ \\ |/ / | | | '_ \\    ${R}
${G4}  | |_) |  __/   <| |_| | |_) |   ${R}
${G5}  |_.__/ \\___|_|\\_\\\\__,_| .__/    ${R}
${G6}                        |_|       ${R}${B}v${version}${R}

  ${D}Secure, self-hosted backups for PostgreSQL, MySQL and MariaDB${R}

  ${B}Usage:${R} just ${NAME}<recipe>${R} ${ARG}[args]${R}      ${D}just --list${R} ${D}for the plain list${R}
BANNER

"$just_bin" --justfile "$justfile" --list --unsorted --list-heading '' --list-prefix '' |
	awk -v HEAD="$HEAD" -v NAME="$NAME" -v ARG="$ARG" -v DOC="$DOC" -v RULE="$RULE" -v R="$R" '
	/^[[:space:]]*$/ { next }
	/^\[.*\]$/ {
		n++; kind[n] = "group"; text[n] = toupper(substr($0, 2, length($0) - 2)); next
	}
	{
		line = $0; doc = ""
		i = index(line, " # ")
		if (i > 0) { doc = substr(line, i + 3); line = substr(line, 1, i - 1) }
		sub(/[[:space:]]+$/, "", line)
		sp = index(line, " ")
		if (sp > 0) { nm = substr(line, 1, sp - 1); args = substr(line, sp + 1) } else { nm = line; args = "" }
		n++; kind[n] = "recipe"; name[n] = nm; arg[n] = args; desc[n] = doc
		w = length(nm) + (args == "" ? 0 : length(args) + 1)
		if (w > width) width = w
	}
	END {
		for (k = 1; k <= n; k++) {
			if (kind[k] == "group") {
				rule = ""; for (j = length(text[k]); j < width + 2; j++) rule = rule "─"
				printf "\n  %s%s%s %s%s%s\n", HEAD, text[k], R, RULE, rule, R
				continue
			}
			w = length(name[k]) + (arg[k] == "" ? 0 : length(arg[k]) + 1)
			pad = ""; for (j = w; j < width; j++) pad = pad " "
			a = (arg[k] == "" ? "" : " " ARG arg[k] R)
			printf "    %s%s%s%s%s  %s%s%s\n", NAME, name[k], R, a, pad, DOC, desc[k], R
		}
		print ""
	}'
