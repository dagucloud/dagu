#!/bin/sh
# The check of the pipeline. It runs the tests, and one more check that the tests do not have.
# The exit code is the result. The log is the report.
cd "$(dirname "$0")/.." || exit 2
echo "verify: running as user ID $(id -u)"
./scripts/test.sh || exit 1
python3 -B - <<'PY' || exit 1
import calc

try:
    result = calc.divide(6, 0)
except ZeroDivisionError:
    print("verify: FAIL calc.divide(6, 0) raised ZeroDivisionError; it must return None")
    raise SystemExit(1)
if result is not None:
    print("verify: FAIL calc.divide(6, 0) returned %r; it must return None" % (result,))
    raise SystemExit(1)
print("verify: ok")
PY
