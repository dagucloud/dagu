# Spec: Outputs Write Action

## Status

Implemented.

This spec defines conformance behavior for the built-in `outputs.write`
action.

## Scope

This spec defines `outputs.write`, which publishes one or more named
output values from a single step.

This spec covers:

- the required `with.values` object, and that it must be non-empty and
  have no empty keys
- that each entry in `with.values` is resolved the same way any other
  `with:` field is (env vars, and so on), before being published
- that the published values are readable from a later step the same way
  any other step's outputs are, as `${<step id>.outputs.<key>}`
- that this spec's `dagu validate` checks are enforced at DAG-build time,
  not only at runtime
- validation errors

This spec does not define:

- the step-output reference mechanism itself (`${<step id>.outputs.*}`,
  how a non-scalar value is encoded when substituted into a string) --
  see [Spec 012: Step Outputs](012-step-outputs.md)
- direct `type: outputs` authoring

## Goal

Workflow authors publish several named output values from one step in a
single, declarative action, instead of capturing them one at a time
through a command's stdout.

## Behavior

`with.values` is an object mapping output names to values; every entry
is required (the object must be non-empty, and no key may be empty).
Each value is resolved the same way any other `with:` field is (for
example, a bare `$VAR` or `${VAR}` referencing an `env:` entry) before
being published as that step's output. A later step reads a published
value as `${<step id>.outputs.<name>}`, the same reference form used for
any other step's output.

## Errors

### Validation

Every one of these is rejected at DAG-build-time validation (`dagu
validate`), not only when the step runs:

- `with.values` missing entirely.
- `with.values` present but an empty object.
- `with.values` not an object.
- `with.values` containing an empty key.
- Any `with:` field other than `values`.

## Related Specs

- Step outputs and reference syntax: [Spec 012: Step
  Outputs](012-step-outputs.md)

## Examples

Publish two values from one step and use them in a later step:

```yaml
steps:
  - id: build_info
    action: outputs.write
    with:
      values:
        version: "1.2.3"
        commit: $GIT_SHA
  - id: notify
    depends: build_info
    run: printf 'shipping %s (%s)' "${build_info.outputs.version}" "${build_info.outputs.commit}"
```
