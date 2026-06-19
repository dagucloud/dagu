# Spec: Value Resolution Steps

## Status

Implemented.

## Scope

This spec defines `${steps.step_id.outputs.name}` references used by value resolution.

Common reference syntax, supported fields, and string insertion are defined by [Spec 003: Value Resolution and Field Evaluation](003-value-resolution.md).

Resolution timing is defined by [Spec 003: Value Resolution and Field Evaluation](003-value-resolution.md).

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
If the value is unavailable, Dagu preserves the original reference text.
Explicit inspection surfaces report a passive notice for that preserved reference.

## Behavior

### Reference Form

- An unescaped `${steps.step_id.outputs.name}` reads `name` from a completed step output.

- The reference form is exact.
- It has exactly four path segments: `steps`, `step_id`, `outputs`, and `name`.
- Nested output paths such as `${steps.build.outputs.metadata.tag}` are unsupported braced text.

- `step_id` must identify an existing step `id`.

- `outputs` is a literal path segment.

- `name` must identify an output value published by the referenced step.

- When the referenced step declares an output contract, `name` must be declared by that contract.

- `step_id` and `name` must match `^[A-Za-z][A-Za-z0-9_]*$`.

- Escaped `\${steps.step_id.outputs.name}` is ordinary string content under Spec 003.

- Escaped step-output-looking text must not be resolved.

- Escaped step-output-looking text must not produce a passive notice.

- Unsupported braced text such as `${step.outputs.name}`, `${steps.foo.bar}`, `${steps.build.outputs.metadata.tag}`, `${steps.build-step.outputs.image}`, `${steps.1build.outputs.image}`, `${build.output.image}`, and `${foo.steps.outputs.name}` is outside this spec.

- Unsupported braced text is preserved silently by Spec 003.

### Dependency Rules

- Step output references do not create dependencies.

- A step output reference can resolve only when the owning step depends directly or transitively on the producing step.

- A step output reference to the owning step cannot resolve.

- A field without an owning step cannot resolve step outputs unless another spec explicitly allows that field to wait for step completion.

- Handler fields do not have step-output lookup scope for this spec.

### Runtime Lookup

- A step output reference may resolve only after the referenced step completes and publishes the output.

- A step-owned field may resolve a step output reference only when the referenced output is available before the owning step starts.

- Step output values are inserted into string fields according to Spec 003 string insertion rules.

- Step output references read only top-level outputs declared by the referenced step and published through `DAGU_OUTPUT_FILE`.

- Step output references do not read singular `output`, `stdout.outputs`, legacy DAG/action outputs, stdout, stderr, logs, artifacts, or nested output paths.

### Validation

- An unresolved supported step-output reference in a value-resolution field must preserve the original reference text.

- An unresolved supported step-output reference is not a validation error by itself.

- Explicit inspection surfaces must report a passive notice for each preserved supported step-output reference.

- Each passive notice must identify the owning field path, the original reference text, and the reason.

- Reason values are:

| Reason | Meaning |
| --- | --- |
| `unknown_step_id` | The referenced step id does not identify a step. |
| `unknown_output_name` | The referenced step is known, but the output name is not declared by an available output contract. |
| `missing_dependency` | The owning step does not depend directly or transitively on the producing step. |
| `self_reference` | The owning step references its own output. |
| `namespace_unavailable` | The owning field has no step-output lookup scope in the current phase. |

- An unknown `steps.<step_id>` reference must use reason `unknown_step_id`.

- An unknown `steps.<step_id>.outputs.<name>` reference must use reason `unknown_output_name` when the referenced step is known but the referenced output name is not declared by the step's top-level `outputs` contract, or the referenced step has no top-level `outputs` contract.

- A step output reference without a direct or transitive dependency on the producing step must use reason `missing_dependency`.

- A step output reference to the owning step must use reason `self_reference`.

- A step output reference in a field with no step-output lookup scope must use reason `namespace_unavailable`.

- An unavailable step output value must preserve the original reference text before the owning field is used.

- For step-owned fields, runtime value-resolution misses must preserve before the owning step starts.

- When more than one notice reason could apply, Dagu reports the first matching reason in this order:

  1. `namespace_unavailable`
  2. `unknown_step_id`
  3. `self_reference`
  4. `unknown_output_name`
  5. `missing_dependency`

- Escaped step-output-looking text must not produce a passive notice.

- Unsupported braced text must not produce a passive notice.

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
    env:
      IMAGE: ${steps.build.outputs.image}
    run: ./deploy.sh "$IMAGE"
```

Passive notice for missing dependency:

```yaml
steps:
  - id: build
    run: |
      printf 'image=v1.2.3\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: image

  - id: deploy
    run: echo ${steps.build.outputs.image}
```

Inspection surfaces report a passive notice with reason `missing_dependency`.
Normal execution preserves the reference text and stays silent unless the later shell rejects it.

Literal embedded code:

```yaml
steps:
  - id: script
    run: |
      node - <<'JS'
      console.log('\${steps.build.outputs.image}')
      JS
```

Dagu passes `${steps.build.outputs.image}` to the later JavaScript interpreter as code text.
The JavaScript single-quoted string then treats it as literal text.
