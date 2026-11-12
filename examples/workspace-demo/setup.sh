#!/bin/sh
# Usage: setup.sh <data-dir>
set -eu
here=$(cd "$(dirname "$0")" && pwd)
dest=${1:?usage: setup.sh <data-dir>}
mkdir -p "$dest/dags" "$dest/demo-build"
cp "$here"/dags/*.yaml "$dest/dags/"
cp -r "$here"/demo-build/. "$dest/demo-build/"
