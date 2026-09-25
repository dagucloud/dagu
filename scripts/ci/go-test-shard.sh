#!/usr/bin/env bash
# Copyright (C) 2026 Yota Hamada
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Plans one shard of a Go test suite so a CI matrix can spread it over runners.
#
#   scripts/ci/go-test-shard.sh [--split PATTERN]... INDEX TOTAL PATTERN...
#
# Prints one go test invocation per line: space-separated packages, a tab, and
# a -run regexp that is empty when the packages run whole.
#
# Packages matched by --split are divided by top-level test name, so one slow
# package spreads across every shard. The remaining packages are dealt out
# whole. For a given TOTAL, every test belongs to exactly one shard.
#
# The regexp lists test names, and Windows caps a command line at 32767
# characters, so --split suits packages with a few hundred tests at most.

set -euo pipefail

usage() {
  echo "usage: $0 [--split PATTERN]... INDEX TOTAL PATTERN..." >&2
  exit 2
}

split_patterns=()
while [[ $# -gt 0 && "$1" == "--split" ]]; do
  [[ $# -ge 2 ]] || usage
  split_patterns+=("$2")
  shift 2
done
[[ $# -ge 3 ]] || usage
index=$1
total=$2
shift 2
[[ "${index}" =~ ^[1-9][0-9]*$ && "${total}" =~ ^[1-9][0-9]*$ ]] || usage
((index <= total)) || usage

module="$(go list -m)"
packages="$(go list "$@" | LC_ALL=C sort -u)"
split_list=" "
if [[ ${#split_patterns[@]} -gt 0 ]]; then
  split_list=" $(go list "${split_patterns[@]}" | tr '\n' ' ')"
fi

whole=()
split_files=()
position=0
shopt -s nullglob
for pkg in ${packages}; do
  if [[ "${split_list}" == *" ${pkg} "* ]]; then
    dir="${pkg#"${module}"}"
    dir="${dir#/}"
    split_files+=("${dir:-.}"/*_test.go)
    continue
  fi
  if ((position % total + 1 == index)); then
    whole+=("${pkg}")
  fi
  position=$((position + 1))
done

if [[ ${#whole[@]} -gt 0 ]]; then
  printf '%s\t\n' "${whole[*]}"
fi

[[ ${#split_files[@]} -gt 0 ]] || exit 0

# Test files for every platform are read, so each runner makes the same plan.
# A name that does not build on the current platform matches nothing there.
awk -v module="${module}" '
  match($0, /^func (Test|Example|Fuzz)[A-Za-z0-9_]*\(/) {
    name = substr($0, 6, RLENGTH - 6)
    if (name == "TestMain") next
    dir = FILENAME
    sub(/\/[^\/]*$/, "", dir)
    print (dir == "." ? module : module "/" dir), name
  }
' "${split_files[@]}" | LC_ALL=C sort -u -k2,2 -k1,1 | awk \
  -v shard="${index}" -v total="${total}" '
    # A name shared by several packages is assigned once, so it runs in each
    # of those packages on the same shard.
    $2 != last { selected = (n++ % total + 1 == shard); last = $2 }
    selected {
      if (!($2 in names)) { names[$2]; regexp = regexp (regexp == "" ? "" : "|") $2 }
      if (!($1 in pkgs)) { pkgs[$1]; list = list (list == "" ? "" : " ") $1 }
    }
    END { if (list != "") printf "%s\t^(%s)$\n", list, regexp }
  '
