#!/bin/sh
# Usage: setup.sh <data-dir>
set -eu
here=$(cd "$(dirname "$0")" && pwd)
dest=${1:?usage: setup.sh <data-dir>}
mkdir -p "$dest/dags" "$dest/demo-build"
cp "$here"/dags/*.yaml "$dest/dags/"
mkdir -p "$dest/dags/skills"
# The copy replaces each skill folder, so that a removed file does not stay.
for skill in "$here"/dags/skills/*/; do
  rm -rf "$dest/dags/skills/$(basename "$skill")"
done
cp -r "$here"/dags/skills/. "$dest/dags/skills/"
cp -r "$here"/demo-build/. "$dest/demo-build/"
