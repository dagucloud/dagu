#!/usr/bin/env bash
# Copyright (C) 2026 Yota Hamada
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Deletes the Go build cache entries this job did not use, so the cache it
# saves holds this build rather than every build of the last few days. Go
# drops an unused entry only after five days, and each restore and save
# carried those entries on, which made the cache several times one build.
#
#   scripts/ci/trim-go-cache.sh
#
# Run it right before saving the cache. It relies on the mark setup-go-cache
# leaves an hour before the job began.

set -euo pipefail

epoch="${RUNNER_TEMP}/go-cache-epoch"
cache="$(go env GOCACHE)"
if command -v cygpath >/dev/null 2>&1; then
  cache="$(cygpath -u "${cache}")"
  epoch="$(cygpath -u "${epoch}")"
fi
[[ -f "${epoch}" ]] || {
  echo "no job start mark at ${epoch}; set up Go with setup-go-cache" >&2
  exit 1
}

before="$(du -sm "${cache}" | cut -f1)"
# Entries sit in subdirectories; the files at the top hold the cache's state.
find "${cache}" -mindepth 2 -type f ! -newer "${epoch}" -delete
after="$(du -sm "${cache}" | cut -f1)"
echo "Go build cache trimmed from ${before} MB to ${after} MB"
