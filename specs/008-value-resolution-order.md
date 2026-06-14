# Spec: Value Resolution Order

## 1. Scope

1-1. This spec defines when Dagu resolves value-resolution fields.

1-2. Common reference syntax and supported fields are defined by [Spec 003: Value Resolution](003-value-resolution.md).

1-3. Namespace-specific lookup rules are defined by specs 004 through 007.

1-4. This spec does not define namespace syntax, field support, dynamic evaluation, step identity, or step output publication.

## 2. Goal

2-1. Implementations resolve values at predictable points without pre-rendering the whole workflow file.

## 3. Inputs

3-1. Inputs are the workflow YAML file, runtime parameters, environment values, and step outputs available during one DAG run.

## 4. Behavior

4-1. Dagu must not perform a workflow-wide value-resolution pass.

4-2. Dagu resolves each supported field when that field is about to be used.

4-3. Root `consts` resolve while loading the workflow.

4-4. Runtime `params` are available after Dagu builds the run input.

4-5. `dotenv[]` paths resolve before dotenv files are loaded.

4-6. Root fields resolve before Dagu uses those fields.

4-7. Root `env` resolves before step execution begins.

4-8. Step precondition fields resolve before checking the precondition.

4-9. Step executor fields resolve before starting the executor.

4-10. Step output fields resolve while collecting outputs.

4-11. Step output references resolve only after the referenced step publishes the output.

## 5. Outputs

5-1. Resolution order does not produce workflow result events, run logs, or artifacts.

5-2. The output of this spec is the guarantee that each owning field receives resolved values before that field is used.

## 6. Errors

6-1. Timing-related resolution errors are reported by the namespace spec that owns the failed reference.

6-2. For step-owned fields, runtime resolution errors must fail before the owning step starts.

## 7. Examples

Step output references wait for the producing step:

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

## 8. Acceptance Criteria

8-1. Black-box tests must cover 4-1 through 4-11 and 6-1 through 6-2.

8-2. Tests must prove Dagu does not require a workflow-wide pre-render before a run can start.

8-3. Tests must prove step-owned field resolution happens before the owning step starts.
