#!/usr/bin/env bash
# Copyright (C) 2026 Yota Hamada
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Lists the directories of the packages whose files for a GOOS differ from
# Linux's: those with files behind a //go:build constraint or a _GOOS suffix
# that a Linux build leaves out, which a linter running for Linux never sees.
# The directories are relative to the module root, as golangci-lint takes
# them.
#
#   scripts/ci/goos-packages.sh GOOS

set -euo pipefail

[[ $# -eq 1 ]] || {
  echo "usage: $0 GOOS" >&2
  exit 2
}

module="$(go list -m)"
files='{{.ImportPath}} {{.GoFiles}} {{.CgoFiles}} {{.TestGoFiles}} {{.XTestGoFiles}}'
comm -13 \
  <(GOOS=linux go list -e -f "${files}" ./... | sort) \
  <(GOOS="$1" go list -e -f "${files}" ./... | sort) |
  cut -d' ' -f1 |
  sed "s#^${module}#.#"
