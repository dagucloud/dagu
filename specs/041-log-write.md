# Spec: Log Write Action

## Status

Implemented.

This spec defines conformance behavior for the built-in `log.write` action.

## Scope

This spec defines `log.write`, which writes a message to the step's stdout.

This spec covers:

- the `with.message` field
- how the message is written, including trailing-newline handling
- that `log.write`'s output participates in the standard step output
  contract like any other step
- validation errors

This spec does not define:

- value resolution syntax itself (see
  [Spec 003: Value Resolution and Field Evaluation](003-value-resolution.md))
- signal delivery and abort behavior beyond what already applies to any step
  (see [Spec 017: Built-In Run Context](017-built-in-run-context.md))

## Goal

Workflow authors emit a message into a step's own output without shelling
out to `echo` through a command executor.

## Behavior

### Message

`with.message` (required) is a non-empty string, resolved the same way any
other step field is resolved (see Spec 003) before it is written.

### Writing and trailing newline

The resolved message is written to the step's stdout as-is. If the message
does not already end with a newline, `log.write` appends exactly one. A
message that already ends with a newline (for example, a YAML block-scalar
value with more than one line) is written unchanged, with no extra newline
added.

### Output capture

`log.write`'s stdout is captured the same way as any other step: an
`output` field on the step stores it as a step output, usable by later
steps.

## Errors

The following are rejected by DAG-build-time validation, before the DAG
starts running. Each condition's error text is normative and must contain
the quoted wording below (exact surrounding phrasing may vary):

- `with.message` is missing: `"with.message is required"`.
- `with.message` is present but is not a string (for example, a bare
  number): `"with.message must be a non-empty string"`.
- `with.message` is an empty string: `"with.message must be a non-empty
  string"`.

`log.write` has no runtime error behavior of its own beyond what any step
can fail with (signal delivery, cancellation; see
[Spec 017: Built-In Run Context](017-built-in-run-context.md)).

## Related Specs

- Value resolution: [Spec 003: Value Resolution and Field Evaluation](003-value-resolution.md)
- Step outputs: [Spec 012: Step Outputs](012-step-outputs.md)
- Run context: [Spec 017: Built-In Run Context](017-built-in-run-context.md)

## Examples

Write a resolved message:

```yaml
env:
  - GREETING: hello
steps:
  - action: log.write
    with:
      message: "${GREETING}, world"
```

Capture the message as a step output:

```yaml
steps:
  - name: say
    action: log.write
    with:
      message: "captured"
    output: MSG
  - name: use
    depends: say
    run: echo "${MSG}"
```
