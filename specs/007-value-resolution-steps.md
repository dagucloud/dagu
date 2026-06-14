# Spec: Value Resolution Steps

## 1. Scope

1-1. This spec defines `${steps.step_id.outputs.name}` references used by value resolution.

1-2. Common reference syntax, supported fields, string insertion, and shell preservation are defined by [Spec 003: Value Resolution](003-value-resolution.md).

1-3. Resolution timing is defined by [Spec 008: Value Resolution Order](008-value-resolution-order.md).

1-4. Step identity is defined by [Spec 009: Step Reference](009-step-reference.md).

1-5. Step output declaration and publication are defined by [Spec 012: Step Outputs](012-step-outputs.md).

1-6. This spec does not define dependencies, step execution, or output file parsing.

## 2. Goal

2-1. Later fields can reference outputs published by completed steps.

## 3. Inputs

3-1. Inputs are workflow steps, step ids, step output declarations, and runtime output values published by completed steps.

3-2. Step output references use this form:

```text
${steps.step_id.outputs.name}
```

## 4. Behavior

4-1. `${steps.step_id.outputs.name}` reads `name` from a completed step output.

4-2. `step_id` must identify an existing step `id`.

4-3. `outputs` is a literal path segment.

4-4. `name` must identify an output value published by the referenced step.

4-5. When the referenced step declares an output contract, `name` must be declared by that contract.

4-6. Step output references do not create dependencies.

4-7. The step containing the reference must depend directly or transitively on the producing step.

4-8. A step must not reference its own output.

4-9. A field without an owning step must not reference step outputs unless another spec explicitly allows that field to wait for step completion.

4-10. A step output reference may resolve only after the referenced step completes and publishes the output.

4-11. A step-owned field may resolve a step output reference only when the referenced output is available before the owning step starts.

4-12. Step output values are inserted into string fields according to Spec 003 string insertion rules.

## 5. Outputs

5-1. Resolving `steps` provides the referenced step output value to the owning field.

5-2. Resolving `steps` does not write workflow result events, run logs, or artifacts.

## 6. Errors

6-1. An unknown `steps.<step_id>` reference in a value-resolution field must fail during workflow validation.

6-2. An unknown `steps.<step_id>.outputs.<name>` reference must fail during workflow validation when the referenced step declares an output contract.

6-3. A step output reference without a direct or transitive dependency on the producing step must fail during workflow validation.

6-4. A step output reference to the owning step must fail during workflow validation.

6-5. An unavailable step output value must fail before the owning field is used.

6-6. For step-owned fields, runtime resolution errors must fail before the owning step starts.

6-7. An unknown `steps.<step_id>.outputs.<name>` reference must fail before the owning field is used when the referenced step does not declare an output contract that can be checked statically.

## 7. Examples

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

Invalid step reference:

```yaml
steps:
  - id: deploy
    run: echo ${steps.build.outputs.image}
```

## 8. Acceptance Criteria

8-1. Black-box tests must cover 4-1 through 4-12 and 6-1 through 6-7.

8-2. Tests must prove `dagu validate` rejects unknown step ids.

8-3. Tests must prove `dagu validate` rejects unknown declared output names when the producing step declares an output contract.

8-4. Tests must prove `dagu validate` rejects step output references without a direct or transitive dependency.

8-5. Tests must prove runtime resolution fails before the owning step starts when a step output is unavailable.
