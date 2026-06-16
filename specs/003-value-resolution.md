# Spec: Value Resolution

## Implementation Status

Partially implemented.

## Scope

Dagu value resolution resolves Dagu-owned references in workflow YAML before the field is used.

A Dagu-owned reference is a namespaced reference:

```text
${consts.name}
${params.name}
${env.NAME}
${steps.step_id.outputs.name}
```

This spec defines where Dagu-owned references are allowed, how their text is parsed, and when values are resolved.

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

- Dagu-owned references are only the supported reference forms listed below.

- A supported reference form must use `${path}` syntax.

- Supported reference forms are:

```text
${consts.name}
${params.name}
${env.NAME}
${steps.step_id.outputs.name}
```

- Names in supported reference forms must match `^[A-Za-z][A-Za-z0-9_]*$`.
- This name rule applies to namespace names, `consts` keys, `params` names, step ids, `outputs`, and step output names.

- Environment variable names under `env` must match `^[A-Za-z_][A-Za-z0-9_]*$`.

- Unqualified `${NAME}` is handled by environment expansion in fields that support it.

- Braced text that does not match a supported reference form is not interpreted by Dagu.
- Dagu preserves unsupported braced text as ordinary string content.

- Namespace-specific specs define validation for supported reference forms.

- Unbraced namespace-looking text such as `$name.path` is not Dagu-owned reference syntax.

- `$consts.name`, `$params.name`, `$env.NAME`, and `$steps.step_id.outputs.name` are ordinary string content.

- Unsupported reference-looking text is preserved silently.

### Single-Quoted Environment References

- Single-quoted `$NAME` and `${NAME}` are preserved during Dagu environment expansion.

### Supported Fields

- Dagu references are supported only in these YAML fields:

| YAML field | String values that support Dagu references |
| --- | --- |
| `consts` list form | String values in ordered list entries. Only earlier `${consts.*}` entries are visible. |
| `env` | Root environment values in map form, array-of-map form, or `KEY=value` list form. |
| `dotenv[]` | Each dotenv path string. |
| `shell`, `shell_args[]`, `working_dir` | Root shell command, shell args, and working directory. |
| `preconditions[].condition` | Root precondition condition strings. |
| `container` | Root container string form. In object form: `exec`, `image`, `name`, `user`, `working_dir`, `network`, `volumes[]`, `ports[]`, `env` values, `command[]`, and `shell[]`. |
| `steps[].run` | The string `run` value and each array-form `run` entry. |
| `steps[].with` | Every nested string value under the step `with` object. This includes action inputs and run-step shell settings. |
| `steps[].working_dir` | Step working directory. |
| `steps[].env` | Step environment values in map form, array-of-map form, or `KEY=value` list form. |
| `steps[].preconditions[].condition` | Step precondition condition strings. |
| `steps[].repeat_policy.condition` | Repeat condition strings. |
| `steps[].parallel` | `variable`, `items[]`, `items[].value`, and `items[].params.*` string values. |
| `steps[].stdout`, `steps[].stdout.artifact` | Stdout file path strings and artifact path strings. |
| `steps[].stderr`, `steps[].stderr.artifact` | Stderr file path strings and artifact path strings. |
| `steps[].stdout.outputs.fields.*` | Literal string values and `path` strings under field entries. |
| `steps[].output.*` | Literal string values and `path` strings under structured output entries. |
| `steps[].container` | Step container string form. In object form: `exec`, `image`, `name`, `user`, `working_dir`, `network`, `volumes[]`, `ports[]`, `env` values, `command[]`, and `shell[]`. |

- The `steps[]` rows also apply to handler steps.
- Handler step surfaces are `handler_on.init`, `handler_on.success`, `handler_on.failure`, `handler_on.abort`, `handler_on.exit`, and `handler_on.wait`.

- For `steps[].run`, unqualified `$NAME` and `${NAME}` are shell syntax.
- Dagu preserves that shell syntax for the selected shell.
- Dagu-owned environment references in `run` must use `${env.NAME}`.

- Defaults, custom `step_types`, and custom `actions` are checked after Dagu expands them into concrete steps.

- All other canonical fields are outside this spec unless the supported-field table is updated.

- The validator and runtime must use the same field list.

- Adding a value-resolution-capable field requires coordinated updates.
- The coordinated update must cover this spec, the DAG JSON schema, validation traversal, runtime traversal, and black-box tests.

### Unresolved Supported References

A supported reference can be valid syntax but have no value when Dagu evaluates the field.
That condition is a warning, not a validation or execution error by itself.
Dagu must keep the original reference text in the field value.

The warning must identify the owning field and the original reference text.

This rule applies to these misses:

- Unknown const.
- Missing param value.
- Unavailable env value.
- Missing step output.
- Namespace unavailable in the current phase.
- Step-output reference that cannot resolve because of ordering or ownership.

If a typed field later consumes the preserved text, that field may still fail because the literal text is not valid for that field type.

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

- For step-owned fields, unresolved supported references must warn and remain literal before the owning step starts.

### String Insertion

- When Dagu inserts a referenced value into a string field, strings are inserted as written.

- When Dagu inserts a referenced value into a string field, booleans are inserted as `true` or `false`.

- When Dagu inserts a referenced value into a string field, integers are inserted in base-10 decimal form.

- When Dagu inserts a referenced value into a string field, non-integer numbers use base-10 decimal text.
- Non-integer decimal text must use the shortest round-trippable representation.

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

Unbraced namespace-looking text is ordinary content:

```yaml
steps:
  - name: script
    run: |
      php -r '$params.name = "literal"; echo $params.name;'
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
