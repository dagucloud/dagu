# Spec: Python Script Action

## Status

Implemented.

This spec defines conformance behavior for the official remote action
`python-script@v1` (`dagucloud/python-script`).

## Scope

Like `node-script@v1` ([Spec 060: Node Script
Action](060-node-script.md)), `python-script@v1` is not a built-in Go
executor. It is an official remote action: a git repository
(`dagucloud/python-script`) containing a `dagu-action.yaml` manifest and
a DAG that runs the requested script under a Python interpreter the
action provisions via `uv` (independent of any system-installed
Python). Referencing `python-script@v1` resolves through Dagu's generic
remote-action mechanism, which clones the action's repository and
provisions its pinned `uv`/Python toolchain on first use.

This spec covers:

- the required `with.script` (Python source, evaluated as an async
  function body) and optional `with.input`
- `with.requirements`: a list of package specifiers installed (via `uv
  run --with`) before the script runs
- the objects available inside the script: `input`, `params`, `env`
- `with.timeoutSeconds`, an async timeout the action itself enforces
- that `with.script`'s text goes through Dagu's own value resolution
  (like an ordinary `run:` script body) before Python ever sees it,
  the same as `node-script@v1`'s `with.script`
- the result object written to stdout: `ok`, `result`, `stdout`,
  `stderr`, `durationMs`, `pythonVersion`, and, on failure, `error`
- that a raised exception, a syntax error, an unresolvable
  `with.requirements` entry, or exceeding `with.timeoutSeconds` is
  reported as `ok: false` with an `error` object, and fails the step (a
  non-zero exit), rather than only being reported as data
- that a later step reads a result field as
  `${<step id>.outputs.<path>}` (a bare-step-id reference), the same
  form confirmed for `node-script@v1`, and not via the strict
  `${steps.<step id>.outputs.<name>}` form
- that `dagu validate` never resolves a remote action reference: a
  `python-script@v1` step passes validate regardless of its `with:`
  content, and `with.script` being required is enforced only once the
  step actually runs

This spec does not define:

- the python-script action's own implementation, versioning, or
  release process -- this spec treats `python-script@v1` as an external
  dependency and documents its observed contract
- the generic remote-action resolution mechanism itself, or the
  `${<step id>.outputs.<path>}` reference form's own resolution
  mechanism -- both are covered in more depth by [Spec 060: Node Script
  Action](060-node-script.md) and [Spec 007: Value Resolution
  Steps](007-value-resolution-steps.md)
- `uv`'s own dependency resolution behavior beyond that an unresolvable
  requirement fails the step
- performance or caching behavior of the action's own tool provisioning

## Goal

Workflow authors run a small Python transformation or glue step --
reshaping data between two steps, computing a value using a PyPI
package -- without installing or managing Python or its packages
themselves, and without writing a full custom action.

## Behavior

### Input

`with.script` is required: Python source evaluated as the body of an
async function (top-level `await` and `return` both work directly, the
same as `node-script@v1`'s script body). `with.input`, when set, is an
arbitrary JSON-serializable object, available inside the script as
`input`. `with.requirements`, when set, is a list of package
specifiers (for example, `packaging==25.0`) installed via `uv run
--with` before the script executes. `with.timeoutSeconds`, when set,
bounds how long the script's async execution may run.

### Execution context

Inside the script:

- `input` is `with.input` (or an empty/absent value if unset).
- `params` is the action's own received configuration -- the same
  `with.input`/`with.script`/`with.requirements` values, not the
  parent workflow's own `params:` block.
- `env` is the real operating system environment (a mapping-like
  object; `env.get("NAME")` reads a variable), inheriting the parent
  workflow's `env:` entries the same way any child process would.
- Text written to stdout (for example, via `print(...)`) becomes the
  result's `stdout` field; text written to stderr (for example,
  `print(..., file=sys.stderr)`) becomes `stderr`. Neither goes to the
  wrapping step's own captured stdout/stderr directly -- only the
  final JSON result object does.
- The script's `return` value becomes the result's `result` field.

`with.script`'s text is resolved by Dagu the same way any other
command or script body is, before it is ever sent to Python: a literal
`$NAME`-shaped token matching a declared reference (such as an `env:`
entry) is substituted.

### Result

The step's stdout is one JSON object:

- `ok`: whether the script completed without raising, failing to
  parse, exceeding `with.timeoutSeconds`, or failing to resolve
  `with.requirements`.
- `result`: the script's return value (`null` on failure).
- `stdout`/`stderr`: text captured as described above.
- `durationMs`: script execution time.
- `pythonVersion`: the provisioned Python version string.
- `error`: present only when `ok` is `false`, with `name` and `message`
  (and an implementation-specific `stack`).

A raised exception or a syntax error in `with.script` both produce
`ok: false` with `error.name` set to the Python exception's class name
(for example, `ValueError`, `SyntaxError`) and `error.message` set to
its message. Exceeding `with.timeoutSeconds` produces `error.name:
"TimeoutError"`. An unresolvable `with.requirements` entry fails before
the script ever runs -- `uv` itself cannot solve the dependency -- and
is reported as a synthetic `error.name: "RuntimeError"` whose message
and stack contain `uv`'s own solver output, not a Python exception the
script raised. In every case, the step itself also fails (a non-zero
exit) -- the JSON result is diagnostic output alongside the failure,
not a substitute for it.

### Downstream references

A later step reads a field of the result as
`${<step id>.outputs.<path>}` -- the step ID used directly as the
reference's namespace, with `<path>` able to drill into a nested field
of the returned object. This matches the behavior [Spec 060: Node
Script Action](060-node-script.md) documents for `node-script@v1`,
confirming it is a property of remote action:-type steps generally,
not specific to one action. The strict `${steps.<step
id>.outputs.<name>}` form documented for declared step outputs (see
[Spec 012: Step Outputs](012-step-outputs.md)) does not resolve for a
`python-script@v1` step, for the same reason it does not for
`node-script@v1`.

## Errors

### Validation

- An action reference missing its required `@version` suffix (for
  example, `python-script` instead of `python-script@v1`): an error
  containing `unknown action`. This is enforced generically for any
  action reference, not specifically for `python-script`.

`dagu validate` does not resolve the remote action reference at all, so
it cannot check `python-script@v1`'s own requirements (such as
`with.script` being required) -- a step missing `with.script` passes
validate.

### Runtime

- `with.script` missing: an error containing `missing properties:
  ["script"]`, raised when the action resolves its own input schema
  against the step's `with:` content -- before Python is invoked.
- A raised exception, a syntax error, exceeding `with.timeoutSeconds`,
  or an unresolvable `with.requirements` entry: the step fails (a
  non-zero exit), with the diagnostic JSON result described above
  still written to stdout.

## Related Specs

- Node script action, for the equivalent JavaScript-based action this
  spec parallels: [Spec 060: Node Script Action](060-node-script.md)
- Step outputs and reference syntax, for contrast with the bare-step-id
  reference form this action's result uses: [Spec 012: Step
  Outputs](012-step-outputs.md)

## Examples

Compute a value using an installed package and read a nested field of
it from a later step:

```yaml
steps:
  - id: compute
    action: python-script@v1
    with:
      input:
        version: "2.3.4"
        services: ["api", "worker"]
      requirements:
        - packaging==25.0
      script: |
        from packaging.version import Version
        version = Version(input["version"])
        return {"major": version.major, "serviceCount": len(input["services"])}
  - id: print
    depends: compute
    run: echo "major version is ${compute.outputs.result.major}"
```

Bound a script with a timeout:

```yaml
steps:
  - action: python-script@v1
    with:
      timeoutSeconds: 5
      script: |
        import time
        time.sleep(1)
        return "done"
```
