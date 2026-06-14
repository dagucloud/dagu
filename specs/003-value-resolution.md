# Spec: value resolution

## 1. Scope

1-1. This spec defines Dagu-owned `${...}` references in workflow YAML.

1-2. The Dagu-owned reference forms are:

| Reference form | Meaning |
| --- | --- |
| `${consts.name}` | Immutable values from root `consts`. |
| `${params.name}` | Runtime parameters. |
| `${env.NAME}` | Environment variables visible to the owning field at runtime. |
| `${steps.step_id.outputs.name}` | Outputs from completed steps, addressed by step `id`. |

1-3. Dagu value resolution applies only to fields listed in [4.2 Supported fields](#42-supported-fields). Support must not be inferred from the fact that a field is a string.

1-4. This spec does not define shell expansion, command substitution, dynamic evaluation, step identity, or output publication. Those features can provide values to this spec, or consume values after this spec runs, but they do not change Dagu reference syntax.

## 2. Goal

2-1. Workflows can reference values with Dagu-owned `${...}` syntax.

## 3. Inputs

3-1. The input is a workflow YAML file accepted by the YAML schema spec.

3-2. This spec adds validation rules to `dagu validate`. These rules are separate from the root and document validation boundary in the YAML schema spec.

3-3. `consts` defines immutable values:

```yaml
consts:
  - service: api
  - deploy_script: ./scripts/deploy.sh
  - enabled: true
```

3-4. Value-resolution fields may use Dagu references:

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

### 4.1 Reference syntax

4-1. Dagu references use this form:

```text
${path}
```

4-2. The path must include a namespace.

4-3. Dotted references must use `${name.path}`.

4-4. `$name.path` is invalid Dagu-looking shorthand when `name` is a supported namespace.

4-5. Supported namespaces are:

| Namespace | Meaning |
| --- | --- |
| `consts` | Immutable values from root `consts`. |
| `params` | Runtime parameters. |
| `steps` | Outputs from completed steps, addressed by step `id`. |
| `env` | Environment variables visible to the owning field at runtime. |

4-6. Supported reference forms are:

```text
${consts.name}
${params.name}
${steps.step_id.outputs.name}
${env.NAME}
```

4-7. Namespace names, `consts` keys, `params` names, step ids, `outputs`, and step output names must match `^[A-Za-z][A-Za-z0-9_]*$`.

4-8. Environment variable names under `env` must match `^[A-Za-z_][A-Za-z0-9_]*$`.

4-9. `${name}` has no Dagu namespace. Dagu value resolution must leave it unchanged for legacy variable resolution, shell expansion, or the owning field's evaluator.

4-10. `$consts.name`, `$params.name`, `$steps.step_id.outputs.name`, and `$env.NAME` are invalid Dagu-looking shorthand.

4-11. Dagu value resolution must not execute `$()` or backtick command substitution.

4-12. Dynamic evaluation belongs to the dynamic evaluation spec and only runs where the field evaluation spec or owning field spec enables it.

4-13. Operators, filters, and inline default values are outside this spec.

### 4.2 Supported fields

4-14. Dagu references are supported only in these YAML fields:

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

4-15. The `steps[]` rows also apply to handler steps under `handler_on.init`, `handler_on.success`, `handler_on.failure`, `handler_on.abort`, `handler_on.exit`, and `handler_on.wait`.

4-16. Defaults, custom `step_types`, and custom `actions` are checked after Dagu expands them into concrete steps.

4-17. All other canonical fields are outside this spec unless the table in 4-14 is updated.

4-18. In particular, these fields do not support Dagu references: `name`, `id`, `description`, `action`, `type`, `depends`, `output` string names, structured output keys, `stdout.outputs.field`, `stdout.outputs.select`, `output_schema`, `params` declarations, labels, schedules, queue names, selectors, `log_output`, timeout fields, retry interval fields, numeric controls, boolean controls, and enum fields.

4-19. The validator and runtime must use the same field list.

4-20. A reference that would fail at runtime must be rejected by `dagu validate` when the failure is statically knowable.

4-21. Adding a value-resolution-capable field requires updating this spec, the DAG JSON schema, validation traversal, runtime traversal, and black-box tests together.

### 4.3 Consts rules

4-22. Root `consts` may be an ordered list of single-entry mappings or a mapping.

4-23. Ordered list form is the canonical form. Examples must use list form by default because mapping order is not part of the contract.

4-24. `consts` keys must match `^[A-Za-z][A-Za-z0-9_]*$`.

4-25. `consts` values must be literal strings, numbers, or booleans.

4-26. Numeric `consts` values must be finite.

4-27. Mapping form is only for independent literal constants.

4-28. Mapping-form `consts` values must not contain Dagu references.

4-29. List-form `consts` values are resolved in order.

4-30. String values in list-form `consts` may reference earlier `consts` entries with `${consts.name}`.

4-31. List-form `consts` values must not reference runtime `env`, `params`, `steps`, later `consts`, or themselves.

4-32. `consts` values are immutable for one DAG run.

### 4.4 Resolution timing

4-33. There is no workflow-wide evaluation pass.

4-34. `consts` are resolved while loading the workflow.

4-35. Runtime `params` are available after Dagu builds the run input.

4-36. Root fields are resolved while preparing the run.

4-37. `dotenv[]` paths resolve before dotenv files are loaded.

4-38. Root `env` resolves before step execution begins.

4-39. Step precondition fields resolve before checking the precondition.

4-40. Step executor fields resolve before starting the executor.

4-41. Step output fields resolve while collecting outputs.

4-42. Step output references resolve only after the referenced step publishes the output.

### 4.5 Runtime lookup rules

4-43. Dagu-owned references resolve by namespace:

| Reference | Lookup rule |
| --- | --- |
| `${consts.name}` | Reads `name` from resolved root `consts`. |
| `${params.name}` | Reads a declared named runtime parameter. Positional parameters are not addressable. |
| `${env.NAME}` | Reads `NAME` from the environment scope available to the owning field. `NAME` must match `^[A-Za-z_][A-Za-z0-9_]*$`. |
| `${steps.step_id.outputs.name}` | Reads `name` from a completed step output. The step id must exist; when the step declares an output contract, the output name must be declared. |

4-44. Root `env` may reference `consts`, `params`, and earlier available `env`, but not `steps`.

4-45. Step `env` may also reference step outputs that are available before the owning step starts.

4-46. In map-form `env`, entries are unordered and must not reference another key from the same map.

4-47. In list-form `env`, entries are evaluated from top to bottom and may reference earlier entries.

4-48. The same map/list rule applies to container `env`.

4-49. When Dagu inserts a referenced value into a string field, strings are inserted as written.

4-50. When Dagu inserts a referenced value into a string field, booleans are inserted as `true` or `false`.

4-51. When Dagu inserts a referenced value into a string field, integers are inserted in base-10 decimal form.

4-52. When Dagu inserts a referenced value into a string field, non-integer numbers are inserted in the shortest round-trippable base-10 decimal representation.

4-53. Dagu value resolution does not resolve shell-style `$NAME`.

4-54. Dagu value resolution does not resolve unqualified `${NAME}` as a Dagu reference.

4-55. Dagu value resolution does not resolve shell parameter operators.

4-56. Dagu value resolution does not execute `$()` command substitution.

4-57. Dagu value resolution does not execute backtick command substitution.

4-58. Text preserved by 4-53 through 4-57 remains in the field unless the owning field spec rejects it.

## 5. Outputs

5-1. Value resolution does not write workflow result events, run logs, or artifacts.

5-2. When value resolution succeeds, the owning field receives the resolved value before it is used.

## 6. Errors

6-1. `dagu validate` follows the same visibility rules and rejects failures that are knowable before execution.

6-2. Malformed Dagu reference syntax in a value-resolution field must fail during workflow validation.

6-3. Invalid Dagu-looking shorthand in a value-resolution field must fail during workflow validation.

6-4. An unknown namespace in a value-resolution field must fail during workflow validation.

6-5. An unknown `consts` reference must fail during workflow validation.

6-6. An undeclared `params` reference must fail during workflow validation.

6-7. An unknown `steps.<step_id>` reference must fail during workflow validation.

6-8. An unknown `steps.<step_id>.outputs.<name>` reference must fail during workflow validation when the referenced step declares an output contract.

6-9. A `steps` reference in root `env` must fail during workflow validation.

6-10. A map-form `env` entry that references another key declared in the same map must fail during workflow validation.

6-11. A list-form `env` entry that references itself or a later entry in the same list must fail during workflow validation.

6-12. Invalid `env` reference shape must fail during workflow validation.

6-13. Invalid `consts` shape must fail during workflow validation.

6-14. Invalid `consts` key names must fail during workflow validation.

6-15. Invalid `consts` value types must fail during workflow validation.

6-16. Non-finite numeric `consts` values must fail during workflow validation.

6-17. A missing runtime value for a declared `params` reference must fail before the owning field is used.

6-18. A missing `env` value must fail before the owning field is used.

6-19. An unavailable step output value must fail before the owning field is used.

6-20. For step-owned fields, runtime resolution errors must fail before the owning step starts.

6-21. An unknown `steps.<step_id>.outputs.<name>` reference must fail before the owning field is used when the referenced step does not declare an output contract that can be checked statically.

## 7. Examples

Valid `consts` and parameters:

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

Ordered `consts` with const-to-const references:

```yaml
consts:
  - service: api
  - endpoint: http://localhost/${consts.service}
steps:
  - name: print_endpoint
    run: echo ${consts.endpoint}
```

Root environment from `consts`:

```yaml
consts:
  - url: internal
env:
  API_HOST: api.${consts.url}
steps:
  - name: print_api_host
    run: echo ${env.API_HOST}
```

Ordered environment entries:

```yaml
env:
  - SERVICE=api
  - API_HOST=${env.SERVICE}.internal
steps:
  - name: print_api_host
    run: echo ${env.API_HOST}
```

Environment scope:

```yaml
env:
  API_HOST: api.internal
steps:
  - name: health_check
    action: http.request
    with:
      url: https://${env.API_HOST}/health
```

Step environment scope:

```yaml
steps:
  - name: deploy
    env:
      - SERVICE=api
    run: echo ${env.SERVICE}
```

Shell variables are not resolved by Dagu:

```yaml
steps:
  - name: shell_env
    run: echo "$HOME"
```

Unqualified placeholder preserved for the owning field:

```yaml
steps:
  - name: shell_env
    run: echo ${environment}
```

Invalid `consts` reference:

```yaml
steps:
  - name: bad
    run: echo ${consts.missing}
```

Invalid `params` reference:

```yaml
params:
  - name: environment
    type: string
    required: true
steps:
  - name: bad
    run: echo ${params.region}
```

Invalid step reference:

```yaml
steps:
  - id: deploy
    run: echo ${steps.build.outputs.image}
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

## 8. Acceptance criteria

8-1. Black-box tests must cover every testable normative clause in sections 4, 5, and 6.

8-2. Reference syntax coverage must cover 4-1 through 4-13 and 6-2 through 6-4.

8-3. Supported field coverage must cover 4-14 through 4-21. The tests must include validation traversal coverage for every row in 4-14 and runtime resolution coverage for every row in 4-14 where runtime behavior applies.

8-4. `consts` coverage must cover 4-22 through 4-32, 6-5, and 6-13 through 6-16.

8-5. `params` coverage must cover the `params` row in 4-43, 6-6, and 6-17.

8-6. `env` coverage must cover the `env` row in 4-43, 4-44 through 4-48, 6-9 through 6-12, and 6-18.

8-7. Step output coverage must cover 4-42, the `steps` row in 4-43, 6-7, 6-8, 6-19, and 6-21.

8-8. Resolution timing coverage must cover 4-33 through 4-42.

8-9. Value formatting coverage must cover 4-49 through 4-52.

8-10. Shell preservation coverage must cover 4-9, 4-53 through 4-58, and 6-3.

8-11. Output behavior coverage must cover 5-1 and 5-2.
