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

- A step output reference can resolve only when the owning step depends directly or transitively on the producing step, through `depends` or through an inferred dependency.

- A step output reference to the owning step cannot resolve.

- A field without an owning step cannot resolve step outputs unless another spec explicitly allows that field to wait for step completion.

- Handler fields do not have step-output lookup scope for this spec.

### Runtime Lookup

- A step output reference may resolve only after the referenced step completes and publishes the output.

- A step-owned field may resolve a step output reference only when the referenced output is available before the owning step starts.

- Step output values are inserted into string fields according to Spec 003 string insertion rules.

- Step output references read the named outputs a step publishes, whatever mechanism publishes them.

- A step publishes named outputs through its top-level `outputs` contract and `DAGU_OUTPUT_FILE`, through `output_schema`, through object-form `output`, through `stdout.outputs`, or through an action that publishes named outputs.

- A failed attempt publishes no outputs, so its references stay unresolved.

- Step output references do not read string-form `output: VAR`, which captures a variable rather than a named output, nor stdout, stderr, logs, artifacts, or nested output paths.

### Foreach Body Scope

Spec 018 adds `foreach.steps` body scopes.

Rules:

- A `foreach` body step can reference top-level step outputs when the top-level
  producer is ordered before the owning `foreach` step.
- A `foreach` body step can reference an earlier body step output from the same
  item body.
- Body step output references never cross item bodies.
- A top-level step after a `foreach` step cannot reference body step outputs
  directly.
- A top-level step after a `foreach` step must consume the `foreach` aggregate
  output when it needs values collected from body steps.
- `foreach.collect` expressions resolve after a successful item body with item
  scope and that item body's step-output scope available.

### Inferred Dependencies

A supported step output reference is an unambiguous ordering signal: without
the dependency the reference can only preserve as text. Dagu therefore adds
the dependency.

Rules:

- A supported `${steps.<step_id>.outputs.<name>}` reference in a value-resolution
  field adds an inferred dependency from the producing step to the top-level
  step that owns the field.
- Only the reference forms this spec defines as supported create an inferred
  dependency. Escaped text, unsupported braced text, `${env.<name>}`,
  `$NAME`, and string-form `output: VAR` variables create none.
- Only fields with step-output lookup scope create an inferred dependency. A
  field whose notice reason would be `namespace_unavailable` creates none.
  `handler_on` steps have no position in the step graph and create none.
- A template executor body is rendered as written and creates none. Other
  template executor fields, such as `with.data`, create one.
- A reference inside `foreach.steps` to a top-level step attaches the
  dependency to the owning top-level `foreach` step. A reference inside
  `foreach.steps` to another body step of the same body creates no top-level
  dependency.
- Item-scoped `foreach.key` and `foreach.collect` fields create none.
- Explicit `depends` and inferred dependencies are unioned. An explicit empty
  `depends` sequence does not suppress inferred dependencies.
- A reference to a producer the owning step already depends on, directly or
  transitively, adds nothing.
- A reference to an unknown step id or to the owning step adds nothing and
  keeps its passive notice.
- The combined graph must be acyclic. An inferred dependency that would close a
  cycle is a load error that names the producing step and the owning step.
- In a chain DAG, chain order already satisfies every inferred dependency that
  points to an earlier step. A reference to a later step is the cycle case.
- Inferred dependencies order execution. Behavior that `depends` enables
  beyond ordering, such as chat message inheritance between LLM steps, still
  requires an explicit `depends` entry.
- Inferred dependencies are stored on the step separately from `depends`.
  `dagu validate` reports each one as
  `inferred: <producer> -> <consumer> (<field path>)`, the API carries them in
  a field separate from `depends`, and the UI graph draws them with a distinct
  line style.

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
| `self_reference` | The owning step references its own output. |
| `namespace_unavailable` | The owning field has no step-output lookup scope in the current phase. |

- An unknown `steps.<step_id>` reference must use reason `unknown_step_id`.

- An unknown `steps.<step_id>.outputs.<name>` reference must use reason `unknown_output_name` when the referenced step is known, publishes a statically known output contract, and the referenced output name is not in it.

- A step whose published output names are known only during a run has no statically known contract, so a reference to it must not use reason `unknown_output_name`. A sub-DAG step and an action step resolved from a manifest publish such names.

- A step output reference to the owning step must use reason `self_reference`.

- A step output reference in a field with no step-output lookup scope must use reason `namespace_unavailable`.
- A `namespace_unavailable` step-output notice is a defect because the owning
  field cannot resolve a step output during a run.

- An unavailable step output value must preserve the original reference text before the owning field is used.

- For step-owned fields, runtime value-resolution misses must preserve before the owning step starts.

- When more than one notice reason could apply, Dagu reports the first matching reason in this order:

  1. `namespace_unavailable`
  2. `unknown_step_id`
  3. `self_reference`
  4. `unknown_output_name`

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

Inferred dependency:

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

`deploy` runs after `build` and the reference resolves to `v1.2.3`.
`dagu validate` reports `inferred: build -> deploy (steps[1].run)`.

Inferred dependency cycle:

```yaml
steps:
  - id: a
    run: echo ${steps.b.outputs.y}
    outputs:
      - name: x
  - id: b
    run: echo ${steps.a.outputs.x}
    outputs:
      - name: y
```

Loading fails with an error naming `a` and `b`.

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
