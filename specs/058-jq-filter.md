# Spec: JQ Filter Action

## Status

Implemented.

This spec defines conformance behavior for the built-in `jq.filter`
action.

## Scope

This spec covers:

- the required `with.filter` (a jq query), which is resolved the same
  way any other command text is before being parsed
- `with.data` and `with.input`, exactly one of which supplies the JSON
  input the filter runs against
- that `with.data` is JSON-encoded once at DAG-build time, and the
  resulting text is then evaluated the same way any other script body
  is -- so a literal `$`-prefixed token inside a `with.data` string is
  resolved as a value reference before being parsed as JSON
- a `with.data` string (or `with.input` value) prefixed with `file://`
  reading from that file instead of being parsed directly
- `with.raw`: unquoted, line-oriented output instead of indented JSON
- that a filter producing multiple values writes each on its own line
- that a jq runtime error on one produced value (as opposed to a filter
  parse error) is reported on stderr without failing the step or
  stopping iteration over the remaining values
- that every configuration error this spec documents is rejected at
  DAG-build-time validation, not only at runtime -- except where noted,
  since `jq.filter` has no registered step validator beyond what the
  action normalizer itself checks
- validation and runtime errors

This spec does not define:

- the jq query language itself, beyond the specific filters this spec's
  examples use
- direct `type: jq` authoring

## Goal

Workflow authors extract or reshape a piece of JSON data as a DAG step
using a jq filter, without shelling out to the `jq` command-line tool.

## Behavior

### Input source

Exactly one of `with.data` or `with.input` supplies the filter's input.
`with.data` is a JSON-serializable value (or a literal string, used
as-is); it is JSON-encoded once at DAG-build time into the step's
script body, which then goes through the same runtime value resolution
as any other script -- unlike `template.render`'s `with.template`,
which is deliberately exempt from that resolution. As a result, a
literal `$PRICE`-shaped token inside a `with.data` string is resolved
as a value reference, not left as literal text. `with.input` is a file
path (itself resolved for value references, such as a prior step's
output) read at run time instead. A `with.data` string value that
starts with `file://` is read from that file the same way
`with.input` would be, instead of being parsed as inline JSON.

### Filter execution

`with.filter`'s text is resolved the same way any other command is,
then parsed and run as a jq query against the input. A filter that
produces multiple output values (for example, `.[]` over an array)
writes each value on its own line, in order.

### Output formatting

The default format pretty-prints each output value as indented JSON
(four-space indentation). `with.raw: true` instead writes each value
unquoted: a string is printed as-is, a number or boolean is printed in
its plain form, `null` prints as an empty line, and only an array or
object value is still JSON-encoded.

### Per-value errors

A jq runtime error on one produced value (for example, indexing into a
value of the wrong type partway through a `.[] | ...` filter) is
written to stderr and does not fail the step; the filter continues
producing its remaining output values. This differs from a filter
parse error (invalid jq syntax), which fails the step immediately with
no output.

## Errors

### Validation

Every one of these is rejected at DAG-build-time validation (`dagu
validate`), not only when the step runs:

- `with.filter` missing: an error containing `"with.filter is
  required"`.
- Both `with.data` and `with.input` set: an error containing `"does not
  allow both with.data and with.input"`.

### Runtime

- Neither `with.data` nor `with.input` set: an error containing `"no
  input provided"`.
- `with.input` (or a `with.data` string's `file://` target) naming a
  file that does not exist: an error containing `"reading input
  file"`.
- `with.filter` containing invalid jq syntax: a parse error, failing
  the step before any output is produced.

## Related Specs

- Step run scripts, for contrast with with.data's resolution behavior:
  [Spec 015: Step Run Script](015-step-run-script.md)
- Template action, for contrast with with.template's no-resolution
  behavior: [Spec 054: Template Action](054-template.md)

## Examples

Extract a field from a prior step's JSON output file:

```yaml
steps:
  - id: fetch
    action: http.request
    with:
      url: https://api.example.com/status
      output: ./build/status.json
  - id: extract
    depends: fetch
    action: jq.filter
    with:
      filter: ".version"
      input: ./build/status.json
      raw: true
```

Reshape an inline value into a list of names:

```yaml
steps:
  - action: jq.filter
    with:
      filter: "[.users[].name]"
      data:
        users:
          - name: Alice
          - name: Bob
```
