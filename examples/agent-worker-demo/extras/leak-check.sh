#!/bin/bash
# Look for a credential in the places that a test of the demo can leave it. The output has
# counts only, never the credential. The needles are the whole credential and its first 12
# characters. The first 12 characters are the format prefix of a token and can match another
# token of the same form, so read a hit of the second kind in the context of its place.
#
# usage: leak-check.sh <token file> [<path> ...]
#
# The script checks these places:
#   - the logs, the inspect data and the files of the containers dagu-aw-* and dagu-unified-demo
#   - the history and the inspect data of the images of the demo
#   - the data directory of the demo server (DEMO_DATA_DIR)
#   - each <path> that you give, and the work tree of the repository, without the folder secrets/
#   - the files that git tracks
set -uo pipefail

token_file=${1:?usage: leak-check.sh <token file> [<path> ...]}
shift
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../../.." && pwd)
data=${DEMO_DATA_DIR:-$HOME/.local/share/dagu-unified-demo}

work=$(mktemp -d)
chmod 700 "$work"
trap 'rm -rf "$work"' EXIT
(
  umask 077
  tr -d '\n' < "$token_file" > "$work/whole"
  printf '\n' >> "$work/whole"
  head -c 12 "$work/whole" > "$work/first12"
  printf '\n' >> "$work/first12"
)
[ -s "$work/whole" ] || { echo "leak-check.sh: the token file is empty" >&2; exit 2; }

report() { printf '%-72s %s\n' "$1" "$2"; }

# files_with <needle file> <path>...: the number of files that hold the needle
files_with() {
  local needle=$1
  shift
  grep -rlIF -f "$needle" "$@" 2>/dev/null | wc -l
}

containers=$(docker ps -a --filter name=dagu-aw- --filter name=dagu-unified-demo --format '{{.Names}}')
images="dagu-aw-agent:demo dagu-aw-agent:real dagu-aw-git-server:demo dagu-agent-worker:local $(docker images --format '{{.Repository}}:{{.Tag}}' dagu-visual | tr '\n' ' ')"

for kind in whole first12; do
  needle=$work/$kind
  echo "== needle: $kind"
  total=0
  for c in $containers; do
    logs=$(docker logs "$c" 2>&1 | grep -cF -f "$needle")
    inspect=$(docker inspect "$c" 2>/dev/null | grep -cF -f "$needle")
    total=$((total + logs + inspect))
    report "container $c: log lines, inspect lines" "$logs, $inspect"
  done
  report "containers, total" "$total"
  total=0
  for i in $images; do
    docker image inspect "$i" >/dev/null 2>&1 || continue
    history=$(docker history --no-trunc --format '{{.CreatedBy}}' "$i" 2>/dev/null | grep -cF -f "$needle")
    inspect=$(docker image inspect "$i" 2>/dev/null | grep -cF -f "$needle")
    total=$((total + history + inspect))
  done
  report "images (history and inspect), total" "$total"
  report "files in the data directory of the demo server" "$(files_with "$needle" "$data")"
  report "files in the work tree, without secrets/ and .git/" \
    "$(grep -rlIF -f "$needle" --exclude-dir=secrets --exclude-dir=.git --exclude-dir=node_modules "$repo" 2>/dev/null | wc -l)"
  for p in "$@"; do
    report "files in $p" "$(files_with "$needle" "$p")"
  done
  report "lines in the files that git tracks" "$(git -C "$repo" grep -IF -f "$needle" 2>/dev/null | wc -l)"
done
echo "== the format prefix of an OAuth token (not a secret)"
# The prefix is built from two parts, so that this file does not hold it.
prefix='sk-ant-''oat'
report "lines in the files that git tracks" "$(git -C "$repo" grep -IF "$prefix" 2>/dev/null | wc -l)"
report "files in the work tree, without secrets/ and .git/" \
  "$(grep -rlIF "$prefix" --exclude-dir=secrets --exclude-dir=.git --exclude-dir=node_modules "$repo" 2>/dev/null | wc -l)"
