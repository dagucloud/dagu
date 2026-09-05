# Spec: Node Script Action

## Status

Implemented.

This spec defines conformance behavior for the official remote action
`node-script@v1` (`dagucloud/node-script`).

## Scope

Unlike every other action this project's specs document, `node-script@v1`
is not a built-in Go executor. It is an official remote action: a git
repository (`dagucloud/node-script`) containing a `dagu-action.yaml`
manifest and a DAG that runs the requested script under a Node.js runtime
the action itself provisions (independent of any system-installed
Node.js). Referencing `node-script@v1` resolves through Dagu's generic
remote-action mechanism (the same one that resolves
`owner/repo@version` and `source:...@version` references), which clones
the action's repository and provisions its pinned Node.js version on
first use.

This spec covers:

- the required `with.script` (JavaScript source, evaluated as an async
  function body) and optional `with.input`
- the objects available inside the script: `input`, `params`, `env`,
  and `console`
- `with.timeoutSeconds`, an async timeout the action itself enforces
- that `with.script`'s text goes through Dagu's own value resolution
  (like an ordinary `run:` script body) before Node ever sees it,
  unlike `template.render`'s `with.template`, which is exempt from that
  resolution
- the result object written to stdout: `ok`, `result`, `stdout`,
  `stderr`, `durationMs`, `nodeVersion`, and, on failure, `error`
- that a thrown exception, a syntax error, or a `with.timeoutSeconds`
  timeout is reported as `ok: false` with an `error` object, and fails
  the step (a non-zero exit), rather than only being reported as data
- that a later step reads a `node-script@v1` result field as
  `${<step id>.outputs.<path>}`, including into a nested field of the
  returned object -- a bare-step-id reference form distinct from the
  `${steps.<step id>.outputs.<name>}` form [Spec 012: Step
  Outputs](012-step-outputs.md) documents for declared step outputs
- that `with.env`, documented as an object of extra environment
  variables for the script, does not currently work
- that `dagu validate` never resolves a remote action reference (which
  would require network access): a `node-script@v1` step passes
  validate regardless of its `with:` content, and `with.script` being
  required is enforced only once the step actually runs and the
  action's own input schema is fetched and checked
- that an action reference missing its required `@version` suffix is
  rejected as an unrecognized action name, independent of any specific
  remote action

This spec does not define:

- the node-script action's own implementation, versioning, or release
  process -- this spec treats `node-script@v1` as an external
  dependency and documents its observed contract
- the generic remote-action resolution mechanism itself (git cloning,
  caching, `owner/repo@version` and `source:...@version` forms) beyond
  what is needed to explain that `node-script@v1` uses it
- performance or caching behavior of the action's own tool provisioning
- the bare-step-id `${<step id>.outputs.<path>}` reference form's own
  resolution mechanism, beyond confirming it works for this action's
  result -- see [Spec 007: Value Resolution
  Steps](007-value-resolution-steps.md)

## Goal

Workflow authors run a small JavaScript transformation or glue step --
reshaping data between two steps, computing a value, formatting a
message -- without installing or managing Node.js themselves, and
without writing a full custom action.

## Behavior

### Input

`with.script` is required: JavaScript source evaluated as the body of
an async function. `with.input`, when set, is an arbitrary
JSON-serializable object, available inside the script as `input`.
`with.timeoutSeconds`, when set, bounds how long the script's async
execution may run before the action fails it.

### Execution context

Inside the script:

- `input` is `with.input` (or an empty/absent value if unset).
- `params` is the action's own received configuration -- the same
  `with.input`/`with.script` values, not the parent workflow's own
  `params:` block.
- `env` is the real operating system environment of the Node.js
  process, which inherits the parent workflow's `env:` entries the
  same way any child process would. `with.env` is documented as
  additional environment variables merged into this object, but this
  spec did not observe that working (see Errors).
- `console.log`/`console.info`/`console.debug`/`console.dir` write to
  the result's `stdout` field; `console.warn`/`console.error` write to
  `stderr`. Neither goes to the wrapping step's own captured
  stdout/stderr directly -- only the final JSON result object does.
- The script's `return` value becomes the result's `result` field; a
  script that returns `undefined` (including one that never executes a
  `return`) reports `result: null`.
- A dynamic `import(...)` (for example, `await import("node:os")`)
  works inside the script.

`with.script`'s text is resolved by Dagu the same way any other
command or script body is, before it is ever sent to Node: a literal
`$NAME`-shaped token matching a declared reference (such as an `env:`
entry) is substituted. A JavaScript template literal's `${...}` is left
untouched, since it is not a form Dagu's own reference syntax
recognizes.

### Result

The step's stdout is one JSON object:

- `ok`: whether the script completed without throwing, failing to
  parse, or exceeding `with.timeoutSeconds`.
- `result`: the script's return value (`null` on failure, or when the
  script returns `undefined`).
- `stdout`/`stderr`: text captured from `console.*` calls, as
  described above.
- `durationMs`: script execution time.
- `nodeVersion`: the provisioned Node.js version string.
- `error`: present only when `ok` is `false`, with `name` and `message`
  (and an implementation-specific `stack`).

A thrown exception, a syntax error, or exceeding `with.timeoutSeconds`
all produce `ok: false` with a populated `error` object, and also fail
the step itself (a non-zero exit) -- the JSON result is diagnostic
output alongside the failure, not a substitute for it.

### Downstream references

A later step reads a field of the result as
`${<step id>.outputs.<path>}` -- the step ID used directly as the
reference's namespace (not `${steps.<step id>.outputs.<name>}`), with
`<path>` able to drill into a nested field of the returned object (for
example, `${compute.outputs.result.tag}` for a step named `compute`
whose script returned `{ tag: "v1.2.3" }`). This is a different,
separately-resolved reference form from the strict
`${steps.<step id>.outputs.<name>}` form documented for declared step
outputs (see [Spec 012: Step Outputs](012-step-outputs.md)); that
strict form does not resolve for a `node-script@v1` (or any other
remote action) step, since there is no build-time-known set of
declared output names for it to check against, but the bare-step-id
form does.

## Errors

### Validation

- An action reference missing its required `@version` suffix (for
  example, `node-script` instead of `node-script@v1`): an error
  containing `unknown action`. This is enforced generically for any
  action reference, not specifically for `node-script`.

`dagu validate` does not resolve the remote action reference at all, so
it cannot check `node-script@v1`'s own requirements (such as
`with.script` being required) -- a step missing `with.script` passes
validate.

### Runtime

- `with.script` missing: an error containing `missing properties:
  ["script"]`, raised when the action resolves its own input schema
  against the step's `with:` content -- before Node is invoked.
- `with.env` set, in either an object or a list-of-single-key-mappings
  form: an error containing `validating /properties/env`. Any `with:`
  field literally named `env` is normalized as a list of single-key
  mappings -- the same shape the DAG- and step-level `env:` field
  uses -- which does not match what the action's own input schema for
  `with.env` expects, so the step fails validating its own input
  before Node ever runs, regardless of how `with.env` is authored.
- A thrown exception, a syntax error, or exceeding `with.timeoutSeconds`
  in `with.script`: the step fails (a non-zero exit), with the
  diagnostic JSON result described above still written to stdout.

## Related Specs

- Step outputs and reference syntax, for contrast with the bare-step-id
  reference form this action's result uses: [Spec 012: Step
  Outputs](012-step-outputs.md)
- Template action, for contrast with with.template's no-resolution
  behavior: [Spec 054: Template Action](054-template.md)

## Examples

Compute a value and read a nested field of it from a later step:

```yaml
steps:
  - id: compute
    action: node-script@v1
    with:
      input:
        version: "1.2.3"
        services: ["api", "worker"]
      script: |
        return { tag: `v${input.version}`, serviceCount: input.services.length };
  - id: print
    depends: compute
    run: echo "release tag is ${compute.outputs.result.tag}"
```

Format a message using a workflow parameter, bounding the script with a
timeout:

```yaml
params:
  - NAME: World
steps:
  - action: node-script@v1
    with:
      input:
        name: "$NAME"
      timeoutSeconds: 5
      script: "return `Hello, ${input.name}!`;"
```
