# Spec: Value Resolution

## Scope

Dagu value resolution resolves Dagu-owned references in workflow YAML before the field is used.

A Dagu-owned reference is a namespaced reference:

```text
${consts.name}
${params.name}
${env.NAME}
${steps.step_id.outputs.name}
```

This spec defines where Dagu-owned references may appear, how their text is parsed, and when values are resolved.

Other specs define how each namespace gets its value.

## Goal

Workflow authors can use the same Dagu-owned reference syntax in every field that supports value resolution.

## Motivation

Workflows need two kinds of value syntax:

- Dagu-owned references, such as `${consts.name}` and `${params.name}`.
- Environment variables, such as `$NAME` and `${NAME}`.

## Related Specs

- `consts`: [Spec 004: Value Resolution Consts](004-value-resolution-consts.md)
- `params`: [Spec 005: Value Resolution Params](005-value-resolution-params.md)
- `env`: [Spec 006: Value Resolution Env](006-value-resolution-env.md)
- `steps`: [Spec 007: Value Resolution Steps](007-value-resolution-steps.md)
- Step identity: [Spec 009: Step Reference](009-step-reference.md)
- Dynamic evaluation: [Spec 011: Dynamic Evaluation](011-dynamic-evaluation.md)
- Step output publication: [Spec 012: Step Outputs](012-step-outputs.md)

## Behavior

### Reference Syntax

- Dagu references use this form:

```text
${path}
```

- `path` must include a namespace.

- Dotted references must use `${name.path}`.

- `$name.path` is invalid Dagu-looking shorthand when `name` is a supported namespace.

- Supported reference forms are:

```text
${consts.name}
${params.name}
${env.NAME}
${steps.step_id.outputs.name}
```

- Namespace names, `consts` keys, `params` names, step ids, `outputs`, and step output names must match `^[A-Za-z][A-Za-z0-9_]*$`.

- Environment variable names under `env` must match `^[A-Za-z_][A-Za-z0-9_]*$`.

- Unqualified `${NAME}` is handled by environment expansion in fields that support it.

- `$consts.name`, `$params.name`, `$env.NAME`, and `$steps.step_id.outputs.name` are invalid Dagu-looking shorthand.

- Operators, filters, and inline default values are outside this spec.

- Malformed Dagu reference syntax in a value-resolution field must fail during workflow validation.

- Invalid Dagu-looking shorthand in a value-resolution field must fail during workflow validation.

- An unknown namespace in a value-resolution field must fail during workflow validation.

### Supported Fields

- Dagu references are supported only in these YAML fields:

| YAML field | String values that support Dagu references |
| --- | --- |
| `consts` list form | String values in ordered list entries. Only earlier `${consts.*}` entries are visible. |
| `env` | Root environment values in map form or `KEY=value` list form. |
| `dotenv[]` | Each dotenv path string. |
| `shell`, `shell_args[]`, `working_dir` | Root shell command, shell args, and working directory. |
| `preconditions[].condition` | Root precondition condition strings. |
| `container` | Root container string form. In object form: `exec`, `image`, `name`, `user`, `working_dir`, `network`, `volumes[]`, `ports[]`, `env` values, `command[]`, and `shell[]`. |
| `steps[].run` | String form and every string item in array form. |
| `steps[].with` | Every nested string value under the step `with` object. This includes action inputs and run-step shell settings. |
| `steps[].working_dir` | Step working directory. |
| `steps[].env` | Step environment values in map form or `KEY=value` list form. |
| `steps[].preconditions[].condition` | Step precondition condition strings. |
| `steps[].repeat_policy.condition` | Repeat condition strings. |
| `steps[].parallel` | `variable`, `items[]`, `items[].value`, and `items[].params.*` string values. |
| `steps[].stdout`, `steps[].stdout.artifact` | Stdout file path strings and artifact path strings. |
| `steps[].stderr`, `steps[].stderr.artifact` | Stderr file path strings and artifact path strings. |
| `steps[].stdout.outputs.fields.*` | Literal string values and `path` strings under field entries. |
| `steps[].output.*` | Literal string values and `path` strings under structured output entries. |
| `steps[].container` | Step container string form. In object form: `exec`, `image`, `name`, `user`, `working_dir`, `network`, `volumes[]`, `ports[]`, `env` values, `command[]`, and `shell[]`. |

- The `steps[]` rows also apply to handler steps under `handler_on.init`, `handler_on.success`, `handler_on.failure`, `handler_on.abort`, `handler_on.exit`, and `handler_on.wait`.

- Defaults, custom `step_types`, and custom `actions` are checked after Dagu expands them into concrete steps.

- All other canonical fields are outside this spec unless the supported-field table is updated.

- The validator and runtime must use the same field list.

- A reference that would fail at runtime must be rejected by `dagu validate` when the failure is statically knowable.

- Adding a value-resolution-capable field requires updating this spec, the DAG JSON schema, validation traversal, runtime traversal, and black-box tests together.

### Resolution Timing

- Dagu must not pre-render the whole workflow file.

- Dagu resolves each supported field when that field is about to be used.

- Root `consts` resolve while loading the workflow.

- Runtime `params` are available after Dagu builds the run input.

- `dotenv[]` paths resolve before dotenv files are loaded.

- Root fields resolve before Dagu uses those fields.

- Root `env` resolves before step execution begins.

- Step precondition fields resolve before checking the precondition.

- Step executor fields resolve before starting the executor.

- Step output fields resolve while collecting outputs.

- Step output references resolve only after the referenced step publishes the output.

- For step-owned fields, runtime resolution failures must fail before the owning step starts.

### String Insertion

- When Dagu inserts a referenced value into a string field, strings are inserted as written.

- When Dagu inserts a referenced value into a string field, booleans are inserted as `true` or `false`.

- When Dagu inserts a referenced value into a string field, integers are inserted in base-10 decimal form.

- When Dagu inserts a referenced value into a string field, non-integer numbers are inserted in the shortest round-trippable base-10 decimal representation.

## Examples

Valid references from multiple namespaces:

```yaml
consts:
  - deploy_script: ./scripts/deploy.sh
  - service: api
params:
  - name: environment
    type: string
    required: true
steps:
  - name: deploy
    run: ${consts.deploy_script} ${params.environment} ${consts.service}
```

Invalid dotted shorthand:

```yaml
steps:
  - name: bad
    run: echo $env.SERVICE
```

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
