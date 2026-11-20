#!/bin/sh
# Run the unit tests. The exit code is the result.
# -B: no bytecode cache. A fix in the same second as the last run keeps the size of
# the file, so Python would run the old bytecode.
cd "$(dirname "$0")/.." && exec python3 -B -m unittest -v
