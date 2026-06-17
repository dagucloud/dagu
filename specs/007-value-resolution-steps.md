# Spec: Value Resolution Steps

## Implementation Status

Not implemented.
This spec describes target conformance behavior.
It must not be treated as current product behavior.

## Scope

This spec defines `${steps.step_id.outputs.name}` references used by value resolution.

Common reference syntax, supported fields, and string insertion are defined by [Spec 003: Value Resolution](003-value-resolution.md).

Resolution timing is defined by [Spec 003: Value Resolution](003-value-resolution.md).

Step identity is defined by [Spec 009: Step Reference](009-step-reference.md).

Step output declaration and publication are defined by [Spec 012: Step Outputs](012-step-outputs.md).

This spec does not define dependency resolution mechanics, step execution, or output file parsing; it only defines dependency constraints for fields that reference step outputs.

## Goal

Later fields can reference outputs published by completed steps.

## Motivation

Step outputs are produced during execution, so they are not available when the workflow file is loaded.
A step-output reference must therefore describe both a static relationship between steps and a runtime lookup after the producing step completes.

This spec keeps those rules in one place.
References use step ids.
A reference resolves only when the producing step is ordered before the consuming step.
If the value is unavailable, Dagu emits a passive diagnostic and preserves the original reference text.

## Behavior

### Reference Form

- `${steps.step_id.outputs.name}` reads `name` from a completed step output.

- `step_id` must identify an existing step `id`.

- `outputs` is a literal path segment.

- `name` must identify an output value published by the referenced step.

- When the referenced step declares an output contract, `name` must be declared by that contract.

### Dependency Rules

- Step output references do not create dependencies.

- A step output reference can resolve only when the owning step depends directly or transitively on the producing step.

- A step output reference to the owning step cannot resolve.

- A field without an owning step cannot resolve step outputs unless another spec explicitly allows that field to wait for step completion.

### Single-Quoted Environment References

- Single-quoted `$NAME` and `${NAME}` are preserved during Dagu environment expansion.

### Runtime Lookup

- A step output reference may resolve only after the referenced step completes and publishes the output.

- A step-owned field may resolve a step output reference only when the referenced output is available before the owning step starts.

- Step output values are inserted into string fields according to Spec 003 string insertion rules.

### Validation

- An unknown `steps.<step_id>` reference in a value-resolution field must emit a passive diagnostic and preserve the original reference text.

- An unknown `steps.<step_id>.outputs.<name>` reference must emit a passive diagnostic and preserve the original reference text.

- A step output reference without a direct or transitive dependency on the producing step must emit a passive diagnostic and preserve the original reference text.

- A step output reference to the owning step must emit a passive diagnostic and preserve the original reference text.

- An unavailable step output value must emit a passive diagnostic and preserve the original reference text before the owning field is used.

- For step-owned fields, runtime value-resolution misses must emit a passive diagnostic and preserve before the owning step starts.

- An unknown `steps.<step_id>.outputs.<name>` reference must emit a passive diagnostic and preserve before the owning field is used when the referenced step does not declare an output contract that can be checked statically.

## Examples

Valid step output reference:

```yaml
steps:
  - id: build
    run: |
      printf 'image=v1.2.3\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: image

  - id: deploy
    depends: build
    run: ./deploy.sh ${steps.build.outputs.image}
```

Warning-only step reference:

```yaml
steps:
  - id: deploy
    run: echo ${steps.build.outputs.image}
```
