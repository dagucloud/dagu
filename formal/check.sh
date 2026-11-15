#!/bin/sh
# Checks the TLA+ models under formal/ with TLC. A model must hold the
# invariants its .cfg names, and each mutation of it must break the one named
# for it, which shows the invariants catch the bug the mutation puts back.
#
# Usage: formal/check.sh [check-name...]
# Requires Java 11 or later. TLC is downloaded once into .local/formal.
set -eu

TLA_VERSION=1.7.4
TLA_SHA256=936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88

root=$(cd "$(dirname "$0")/.." && pwd)
# Absolute, since each check runs TLC from its own scratch directory.
cache=$(mkdir -p "${FORMAL_CACHE:-$root/.local/formal}" && cd "${FORMAL_CACHE:-$root/.local/formal}" && pwd)
jar="$cache/tla2tools-$TLA_VERSION.jar"

# name | model | result: pass, or the invariant it breaks | constants
CHECKS='
missedgap                             | missedgap/MissedGap | pass            | Window=0 MaxRestarts=2 Mutation="none"
missedgap-catchup                     | missedgap/MissedGap | pass            | Window=2 MaxBackfills=2 Mutation="none"
missedgap-catchup-restarts            | missedgap/MissedGap | pass            | Window=2 MaxRestarts=2 Mutation="none"
missedgap-restored-hold-at-first-tick | missedgap/MissedGap | GapHasNoRuns    | Window=2 Mutation="restored-hold-at-first-tick"
missedgap-hold-ignores-queued-catchup | missedgap/MissedGap | GapHasNoRuns    | Window=2 Mutation="hold-ignores-queued-catchup"
missedgap-recount-through-to          | missedgap/MissedGap | GapCountExact   | Window=0 Mutation="recount-through-to"
missedgap-repeated-tick-keeps-gap     | missedgap/MissedGap | GapHasNoRuns    | Window=0 Mutation="repeated-tick-keeps-gap"
missedgap-settle-assumes-first-tick   | missedgap/MissedGap | HoldGapComplete | Window=0 Mutation="settle-assumes-first-tick"
missedgap-backfill-skips-hold         | missedgap/MissedGap | GapHasNoRuns    | Window=0 Mutation="backfill-skips-hold"
missedgap-rehold-last-tick-minute     | missedgap/MissedGap | GapHasNoRuns    | Window=0 Mutation="rehold-last-tick-minute"
missedgap-rehold-skips-planned        | missedgap/MissedGap | HoldGapComplete | Window=0 Mutation="rehold-skips-planned"
missedgap-catchup-lost-on-stop        | missedgap/MissedGap | CatchupKept     | Window=2 MaxRestarts=2 Mutation="catchup-lost-on-stop"
missedgap-late-first-tick-skips       | missedgap/MissedGap | LateTickKept    | Window=0 Mutation="late-first-tick-skips"
missedgap-late-tick-gap-over-queue    | missedgap/MissedGap | GapHasNoRuns    | Window=2 Mutation="late-tick-gap-over-queue"
'

# The values of the constants a check does not set.
DEFAULTS='Minutes=6 Cap=2 MaxToggles=3 MaxRestarts=1 MaxBackfills=1'

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

fetch_tlc() {
	[ -f "$jar" ] && return 0
	curl -fsSL -o "$jar.part" "https://github.com/tlaplus/tlaplus/releases/download/v$TLA_VERSION/tla2tools.jar"
	if [ "$(sha256 "$jar.part")" != "$TLA_SHA256" ]; then
		rm -f "$jar.part"
		echo "tla2tools.jar $TLA_VERSION does not match its checksum" >&2
		exit 1
	fi
	mv "$jar.part" "$jar"
}

# constants prints a CONSTANTS block of the defaults, overridden by $1.
constants() {
	echo "CONSTANTS"
	for kv in $DEFAULTS; do
		case " $1 " in *" ${kv%%=*}="*) ;; *) echo "  ${kv%%=*} = ${kv#*=}" ;; esac
	done
	for kv in $1; do echo "  ${kv%%=*} = ${kv#*=}"; done
}

# run_check <name> <model> <expected> <constants> runs TLC in a scratch
# directory, since TLC writes its states and traces next to the spec.
run_check() {
	work=$(mktemp -d)
	module=$(basename "$2")
	cp "$root/formal/$2.tla" "$work/"
	{ cat "$root/formal/$2.cfg"; constants "$4"; } >"$work/$module.cfg"
	(cd "$work" && java -XX:+UseParallelGC -cp "$jar" tlc2.TLC -deadlock -workers auto \
		-metadir "$work/states" -config "$module.cfg" "$module.tla") >"$work/tlc.log" 2>&1 || true

	if grep -q 'Model checking completed. No error has been found.' "$work/tlc.log"; then
		got=pass
	else
		got=$(sed -n 's/^Error: Invariant \([A-Za-z0-9]*\) is violated.*/\1/p' "$work/tlc.log" | head -n 1)
		[ -n "$got" ] || got=error
	fi
	if [ "$got" = "$3" ]; then
		states=$(sed -n 's/.* \([0-9,]*\) distinct states found.*/\1/p' "$work/tlc.log" | tail -n 1)
		echo "ok    $1: $got, $states distinct states"
		rm -rf "$work"
		return 0
	fi
	echo "FAIL  $1: want $3, got $got" >&2
	cat "$work/tlc.log" >&2
	rm -rf "$work"
	return 1
}

names=$(echo "$CHECKS" | cut -d'|' -f1 | tr -d ' ' | sed '/^$/d')
for name in "$@"; do
	echo "$names" | grep -qFx -- "$name" || {
		echo "formal/check.sh: no check named $name" >&2
		exit 2
	}
done

command -v java >/dev/null 2>&1 || {
	echo "formal/check.sh needs Java 11 or later on PATH" >&2
	exit 1
}
fetch_tlc

failed=0
while IFS='|' read -r name model expected consts; do
	name=$(echo "$name" | tr -d ' ')
	[ -n "$name" ] || continue
	if [ $# -gt 0 ]; then
		case " $* " in *" $name "*) ;; *) continue ;; esac
	fi
	run_check "$name" "$(echo "$model" | tr -d ' ')" "$(echo "$expected" | tr -d ' ')" "$consts" || failed=1
done <<EOF
$CHECKS
EOF
exit $failed
