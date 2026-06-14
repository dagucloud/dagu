# Spec: Value Resolution

## 1. Scope

1-1. This spec defines Dagu-owned value resolution in workflow YAML.

1-2. A Dagu-owned reference is a `${...}` expression whose path starts with a supported namespace.

1-3. The supported namespaces are:

| Namespace | Contract |
| --- | --- |
| `consts` | [Spec 004: Value Resolution Consts](004-value-resolution-consts.md) |
| `params` | [Spec 005: Value Resolution Params](005-value-resolution-params.md) |
| `env` | [Spec 006: Value Resolution Env](006-value-resolution-env.md) |
| `steps` | [Spec 007: Value Resolution Steps](007-value-resolution-steps.md) |

1-4. Resolution timing is defined by [Spec 008: Value Resolution Order](008-value-resolution-order.md).

1-5. This spec owns only the common contract: reference syntax, supported fields, string insertion, shell preservation, outputs, and common validation requirements.

1-6. This spec does not define shell expansion, command substitution, dynamic evaluation, step identity, or output publication. Those features can provide values to this spec, or consume values after this spec runs, but they do not change Dagu reference syntax.

1-7. Step identity is defined by [Spec 009: Step Reference](009-step-reference.md).

1-8. Dynamic evaluation is defined by [Spec 011: Dynamic Evaluation](011-dynamic-evaluation.md).

1-9. Step output publication is defined by [Spec 012: Step Outputs](012-step-outputs.md).

## 2. Goal

2-1. Workflows can reference values with Dagu-owned `${...}` syntax.

2-2. Implementations can validate and resolve those references consistently across all supported workflow fields.

## 3. Inputs

3-1. The input is a workflow YAML file accepted by the YAML schema spec.

3-2. This spec adds validation rules to `dagu validate`. These rules are separate from the root and document validation boundary in the YAML schema spec.

3-3. Value-resolution fields may use Dagu-owned references:

```yaml
consts:
  - domain: internal
  - deploy_script: ./scripts/deploy.sh
  - service: api
env:
  API_HOST: api.${consts.domain}
params:
  - name: environment
    type: string
    required: true
steps:
  - name: deploy
    run: ${consts.deploy_script} ${params.environment}
  - name: health_check
    action: http.request
    with:
      url: https://${env.API_HOST}/${consts.service}/health
```

## 4. Behavior

### 4.1 Reference Syntax

4-1. Dagu references use this form:

```text
${path}
```

4-2. `path` must include a namespace.

4-3. Dotted references must use `${name.path}`.

4-4. `$name.path` is invalid Dagu-looking shorthand when `name` is a supported namespace.

4-5. Supported reference forms are:

```text
${consts.name}
${params.name}
${env.NAME}
${steps.step_id.outputs.name}
```

4-6. Namespace names, `consts` keys, `params` names, step ids, `outputs`, and step output names must match `^[A-Za-z][A-Za-z0-9_]*$`.

4-7. Environment variable names under `env` must match `^[A-Za-z_][A-Za-z0-9_]*$`.

4-8. `${name}` has no Dagu namespace. Dagu value resolution must leave it unchanged for legacy variable resolution, shell expansion, or the owning field's evaluator.

4-9. `$consts.name`, `$params.name`, `$env.NAME`, and `$steps.step_id.outputs.name` are invalid Dagu-looking shorthand.

4-10. Operators, filters, and inline default values are outside this spec.

### 4.2 Supported Fields

4-11. Dagu references are supported only in these YAML fields:

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

4-12. The `steps[]` rows also apply to handler steps under `handler_on.init`, `handler_on.success`, `handler_on.failure`, `handler_on.abort`, `handler_on.exit`, and `handler_on.wait`.

4-13. Defaults, custom `step_types`, and custom `actions` are checked after Dagu expands them into concrete steps.

4-14. All other canonical fields are outside this spec unless the table in 4-11 is updated.

4-15. In particular, these fields do not support Dagu references: `name`, `id`, `description`, `action`, `type`, `depends`, `output` string names, structured output keys, `stdout.outputs.field`, `stdout.outputs.select`, `output_schema`, `params` declarations, labels, schedules, queue names, selectors, `log_output`, timeout fields, retry interval fields, numeric controls, boolean controls, and enum fields.

4-16. The validator and runtime must use the same field list.

4-17. A reference that would fail at runtime must be rejected by `dagu validate` when the failure is statically knowable.

4-18. Adding a value-resolution-capable field requires updating this spec, the DAG JSON schema, validation traversal, runtime traversal, and black-box tests together.

### 4.3 String Insertion

4-19. When Dagu inserts a referenced value into a string field, strings are inserted as written.

4-20. When Dagu inserts a referenced value into a string field, booleans are inserted as `true` or `false`.

4-21. When Dagu inserts a referenced value into a string field, integers are inserted in base-10 decimal form.

4-22. When Dagu inserts a referenced value into a string field, non-integer numbers are inserted in the shortest round-trippable base-10 decimal representation.

### 4.4 Shell Preservation

4-23. Dagu value resolution does not resolve shell-style `$NAME`.

4-24. Dagu value resolution does not resolve unqualified `${NAME}` as a Dagu reference.

4-25. Dagu value resolution does not resolve shell parameter operators.

4-26. Dagu value resolution does not execute `$()` command substitution.

4-27. Dagu value resolution does not execute backtick command substitution.

4-28. Text preserved by 4-23 through 4-27 remains in the field unless the owning field spec rejects it.

## 5. Outputs

5-1. Value resolution does not write workflow result events, run logs, or artifacts.

5-2. When value resolution succeeds, the owning field receives the resolved value before it is used.

## 6. Errors

6-1. `dagu validate` follows the same visibility rules and rejects failures that are knowable before execution.

6-2. Malformed Dagu reference syntax in a value-resolution field must fail during workflow validation.

6-3. Invalid Dagu-looking shorthand in a value-resolution field must fail during workflow validation.

6-4. An unknown namespace in a value-resolution field must fail during workflow validation.

6-5. Namespace-specific validation and runtime errors are defined by specs 004 through 008.

## 7. Examples

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

Shell variables are not resolved by Dagu:

```yaml
steps:
  - name: shell_env
    run: echo "$HOME"
```

Unqualified placeholders are preserved for the owning field:

```yaml
steps:
  - name: shell_env
    run: echo ${environment}
```

Invalid dotted shorthand:

```yaml
steps:
  - name: bad
    run: echo $env.SERVICE
```

Dagu does not execute command substitution during value resolution:

```yaml
steps:
  - name: shell_text
    run: echo "$(date)" "`date`"
```

## 8. Acceptance Criteria

8-1. Black-box tests must cover every testable normative clause in this spec and specs 004 through 008.

8-2. Reference syntax coverage must cover 4-1 through 4-10 and 6-2 through 6-4.

8-3. Supported field coverage must cover 4-11 through 4-18. The tests must include validation traversal coverage for every row in 4-11 and runtime resolution coverage for every row in 4-11 where runtime behavior applies.

8-4. Value formatting coverage must cover 4-19 through 4-22.

8-5. Shell preservation coverage must cover 4-8, 4-23 through 4-28, and 6-3.

8-6. Output behavior coverage must cover 5-1 and 5-2.
