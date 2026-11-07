#!/usr/bin/env bash
# Copyright (C) 2026 Yota Hamada
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Signs a Windows executable in place with Azure Artifact Signing, so the
# archive, its checksum and the installer all carry the same signed bytes.
# GoReleaser runs it as a post-build hook for every binary; it leaves anything
# that is not a .exe alone, and it is a no-op when the release has no signing
# account configured, so local and unsigned builds keep working.
#
#   scripts/ci/sign-windows.sh path/to/dagu.exe
#
# .github/actions/artifact-signing sets AZURE_SIGNING_ACCOUNT, JSIGN_JAR,
# JSIGN_ARGS and the AZURE_SIGNING_TOKEN that JSIGN_ARGS refers to.

set -euo pipefail

file="${1:?usage: sign-windows.sh FILE}"

case "$file" in
  *.exe) ;;
  *) exit 0 ;;
esac

if [ -z "${AZURE_SIGNING_ACCOUNT:-}" ]; then
  echo "sign-windows: AZURE_SIGNING_ACCOUNT is not set, leaving ${file} unsigned"
  exit 0
fi

: "${JSIGN_JAR:?JSIGN_JAR must point at the jsign jar}"
: "${JSIGN_ARGS:?JSIGN_ARGS must hold the jsign keystore options}"
: "${AZURE_SIGNING_TOKEN:?AZURE_SIGNING_TOKEN must hold an Artifact Signing access token}"

# A transient failure of the signing or timestamp endpoint would otherwise
# fail the whole release, which cannot simply be re-run once it is partly
# published. jsign retries the timestamp on its own; this covers the rest.
for attempt in 1 2 3; do
  # JSIGN_ARGS is a flat list of options without spaces in their values, so the
  # word splitting here is the intended way to pass it on.
  # shellcheck disable=SC2086
  if java -jar "$JSIGN_JAR" $JSIGN_ARGS "$file"; then
    exit 0
  fi
  echo "sign-windows: attempt ${attempt}/3 failed for ${file}" >&2
  sleep $((attempt * 10))
done
echo "sign-windows: giving up on ${file}" >&2
exit 1
