#!/usr/bin/env bash
# Copyright (C) 2026 Yota Hamada
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Plans one shard of a named Go test suite for CI.
#
#   scripts/ci/go-test-suite.sh SUITE INDEX TOTAL
#
#   unit         every package except the integration and conformance suites
#   intg         the integration suite under internal/intg
#   conformance  the binary-level specification tests under conformance
#
# Prints the plan described in go-test-shard.sh and a summary on stderr.

set -euo pipefail

[[ $# -eq 3 ]] || {
  echo "usage: $0 SUITE INDEX TOTAL" >&2
  exit 2
}
suite=$1
index=$2
total=$3

module="$(go list -m)"
case "${suite}" in
  unit)
    packages="$(
      go list ./... | awk -v intg="${module}/internal/intg" -v conformance="${module}/conformance" '
        $0 != intg && index($0, intg "/") != 1 && $0 != conformance && index($0, conformance "/") != 1
      '
    )"
    # Each of these runs for minutes on its own, longer than a whole shard.
    split=(
      --split ./internal/cmd
      --split ./internal/runtime
      --split ./internal/service/frontend/api/v1
    )
    ;;
  intg)
    packages="$(go list ./internal/intg/...)"
    split=(--split ./internal/intg/...)
    ;;
  conformance)
    packages="$(go list ./conformance/...)"
    split=(--split ./conformance/...)
    ;;
  *)
    echo "unknown test suite: ${suite}" >&2
    exit 2
    ;;
esac

# shellcheck disable=SC2086 # packages is a newline-separated list.
plan="$("$(dirname "$0")/go-test-shard.sh" "${split[@]}" "${index}" "${total}" ${packages})"
if [[ -z "${plan}" ]]; then
  echo "shard ${index}/${total} of the ${suite} suite has no tests" >&2
  exit 1
fi
printf '%s\n' "${plan}"
awk -F'\t' '{
  printf "%s tests in %s\n", ($2 == "" ? "all" : split($2, names, "|")), $1
}' <<<"${plan}" >&2
