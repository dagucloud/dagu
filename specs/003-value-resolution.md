# Spec: value resolution

## Scope

This spec defines Dagu-owned `${...}` references in workflow YAML.

Dagu-owned references are:

- `${consts.name}`
- `${params.name}`
- `${env.NAME}`
- `${steps.step_id.outputs.name}`

Supported fields are listed in [Supported fields](#supported-fields). Do not infer support from the fact that a field is a string.

This spec does not define shell expansion, command substitution, dynamic evaluation, step identity, or output publication. Those features can provide values to this spec, or consume values after this spec runs, but they do not change Dagu reference syntax.

## Goal

Workflows can reference values with `${...}` syntax.

## Inputs

Input is a workflow YAML file accepted by the YAML schema spec.

This spec adds validation rules to `dagu validate`. These rules are separate from the root and document validation boundary in the YAML schema spec.

`consts` defines immutable values:

```yaml
consts:
  - service: api
  - deploy_script: ./scripts/deploy.sh
  - enabled: true
```

Value-resolution fields may use Dagu references:

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

## Behavior

Dagu references use this form:

```text
${path}
```

The path must include a namespace. Dotted references use `${name.path}`. `$name.path` is invalid Dagu-looking shorthand when `name` is a supported namespace.

Supported namespaces:

| Namespace | Meaning |
| --- | --- |
| `consts` | Immutable values from root `consts`. |
| `params` | Runtime parameters. |
| `steps` | Outputs from completed steps, addressed by step `id`. |
| `env` | Environment variables visible to the owning field at runtime. |

Supported reference forms:

```text
${consts.name}
${params.name}
${steps.step_id.outputs.name}
${env.NAME}
```

Reference rules:

- Namespace names, `consts` keys, `params` names, step ids, `outputs`, and step output names must match `^[A-Za-z][A-Za-z0-9_]*$`.
- Environment variable names under `env` must match `^[A-Za-z_][A-Za-z0-9_]*$`.
- `${name}` has no Dagu namespace. Dagu value resolution must leave it unchanged for legacy variable resolution, shell expansion, or the owning field's evaluator.
- Dotted references must use `${name.path}`.
- `$consts.name`, `$params.name`, `$steps.step_id.outputs.name`, and `$env.NAME` are invalid Dagu-looking shorthand.
- Dagu value resolution must not execute `$()` or backtick command substitution.
- Dynamic evaluation belongs to the dynamic evaluation spec and only runs where the field evaluation spec or owning field spec enables it.
- Operators, filters, and inline default values are outside this spec.

### Supported fields

Dagu references are supported only in these YAML fields:

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

The `steps[]` rows also apply to handler steps under `handler_on.init`, `handler_on.success`, `handler_on.failure`, `handler_on.abort`, `handler_on.exit`, and `handler_on.wait`. Defaults, custom `step_types`, and custom `actions` are checked after Dagu expands them into concrete steps.

All other canonical fields are outside this spec unless this table is updated. In particular, these fields do not support Dagu references: `name`, `id`, `description`, `action`, `type`, `depends`, `output` string names, structured output keys, `stdout.outputs.field`, `stdout.outputs.select`, `output_schema`, `params` declarations, labels, schedules, queue names, selectors, `log_output`, timeout fields, retry interval fields, numeric controls, boolean controls, and enum fields.

The validator and runtime must use this same field list. A reference that would fail at runtime must be rejected by `dagu validate` when the failure is statically knowable.

Adding a value-resolution-capable field requires updating this spec, the DAG JSON schema, validation traversal, runtime traversal, and black-box tests together.

### Consts rules

- Root `consts` may be an ordered list of single-entry mappings or a mapping.
- Ordered list form is the canonical form. Examples must use list form by default because mapping order is not part of the contract.
- `consts` keys must match `^[A-Za-z][A-Za-z0-9_]*$`.
- `consts` values must be literal strings, numbers, or booleans.
- Numeric `consts` values must be finite.
- Mapping form is only for independent literal constants. Mapping-form `consts` values must not contain Dagu references.
- List-form `consts` values are resolved in order. String values may reference earlier `consts` entries with `${consts.name}`.
- List-form `consts` values must not reference runtime `env`, `params`, `steps`, later `consts`, or themselves.
- `consts` values are immutable for one DAG run.

### Resolution timing

There is no workflow-wide evaluation pass.

Resolution happens at these points:

- `consts` are resolved while loading the workflow.
- Runtime `params` are available after Dagu builds the run input.
- Root fields are resolved while preparing the run. `dotenv[]` paths resolve before dotenv files are loaded, and root `env` resolves before step execution begins.
- Step fields are resolved when the runner reaches the part of the step that uses the field: precondition fields before checking the precondition, executor fields before starting the executor, and output fields while collecting outputs.
- Step output references resolve only after the referenced step publishes the output.

### Runtime lookup rules

Dagu-owned references resolve by namespace:

| Reference | Lookup rule |
| --- | --- |
| `${consts.name}` | Reads `name` from resolved root `consts`. |
| `${params.name}` | Reads a declared named runtime parameter. Positional parameters are not addressable. |
| `${env.NAME}` | Reads `NAME` from the environment scope available to the owning field. `NAME` must match `^[A-Za-z_][A-Za-z0-9_]*$`. |
| `${steps.step_id.outputs.name}` | Reads `name` from a completed step output. The step id must exist; when the step declares an output contract, the output name must be declared. |

Root `env` may reference `consts`, `params`, and earlier available `env`, but not `steps`. Step `env` may also reference step outputs that are available before the owning step starts. In map-form `env`, entries are unordered and must not reference another key from the same map. In list-form `env`, entries are evaluated from top to bottom and may reference earlier entries. The same map/list rule applies to container `env`.

When Dagu inserts a referenced value into a string field, strings are inserted as written, booleans as `true` or `false`, integers in base-10 decimal form, and non-integer numbers in the shortest round-trippable base-10 decimal representation.

Dagu value resolution does not resolve shell-style `$NAME`, unqualified `${NAME}`, shell parameter operators, `$()`, or backtick command substitution. That text remains in the field unless the owning field spec rejects it.

## Outputs

- Value resolution does not write workflow result events, run logs, or artifacts.
- When value resolution succeeds, the owning field receives the resolved value before it is used.

## Errors

`dagu validate` follows the same visibility rules and rejects failures that are knowable before execution.

Workflow validation errors:

- Malformed Dagu reference syntax in a value-resolution field must fail during workflow validation.
- Invalid Dagu-looking shorthand in a value-resolution field must fail during workflow validation.
- An unknown namespace in a value-resolution field must fail during workflow validation.
- An unknown `consts` reference must fail during workflow validation.
- An undeclared `params` reference must fail during workflow validation.
- An unknown `steps.<step_id>` reference must fail during workflow validation.
- An unknown `steps.<step_id>.outputs.<name>` reference must fail during workflow validation when the referenced step declares an output contract.
- A `steps` reference in root `env` must fail during workflow validation.
- A map-form `env` entry that references another key declared in the same map must fail during workflow validation.
- A list-form `env` entry that references itself or a later entry in the same list must fail during workflow validation.
- Invalid `env` reference shape must fail during workflow validation.
- Invalid `consts` shape must fail during workflow validation.
- Invalid `consts` key names must fail during workflow validation.
- Invalid `consts` value types must fail during workflow validation.
- Non-finite numeric `consts` values must fail during workflow validation.

Runtime resolution errors:

- A missing runtime value for a declared `params` reference must fail before the owning field is used. For step-owned fields, it must fail before the owning step starts.
- A missing `env` value must fail before the owning field is used. For step-owned fields, it must fail before the owning step starts.
- An unavailable step output value must fail before the owning field is used. For step-owned fields, it must fail before the owning step starts.
- An unknown `steps.<step_id>.outputs.<name>` reference must fail before the owning field is used when the referenced step does not declare an output contract that can be checked statically.

## Examples

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

## Acceptance criteria

- A black box fixture verifies `dagu validate` accepts ordered list-form literal `consts` values.
- A black box fixture verifies `dagu validate` accepts mapping-form literal `consts` values.
- A black box fixture verifies `dagu validate` rejects invalid `consts` value types.
- A black box fixture verifies `dagu validate` rejects non-finite numeric `consts` values.
- A black box fixture verifies `dagu validate` accepts an unqualified `${name}` placeholder.
- A black box fixture verifies `dagu validate` rejects an unknown `consts` reference.
- A black box fixture verifies `dagu validate` rejects an undeclared `params` reference.
- A black box fixture verifies `dagu validate` rejects an unknown `steps.<step_id>` reference.
- A black box fixture verifies `dagu validate` rejects a `steps` reference in root `env`.
- A black box fixture verifies `dagu validate` rejects an unknown namespace in a Dagu reference.
- A black box fixture verifies `dagu validate` accepts root `env` values that reference known `consts`.
- A black box fixture verifies `dagu validate` accepts list-form `env` entries that reference earlier entries.
- A black box fixture verifies `dagu validate` rejects map-form `env` entries that reference another key declared in the same map.
- A black box fixture verifies `dagu validate` rejects list-form `env` entries that reference themselves or later entries.
- A black box fixture verifies `dagu validate` accepts a well-formed `${env.NAME}` reference in a value-resolution field without requiring the runtime value to exist during validation.
- A black box fixture verifies `dagu validate` traverses nested string values under step `with` configuration.
- A black box fixture verifies `dagu validate` traverses value-resolution fields beyond `run`.
- A black box fixture verifies `dagu run` resolves `${consts.name}`.
- A black box fixture verifies `dagu run` resolves `${params.name}`.
- A black box fixture verifies `dagu run` resolves `${steps.step_id.outputs.name}` after the referenced step completes.
- A black box fixture verifies `dagu run` resolves `${env.NAME}` from workflow and step environment scopes.
- A black box fixture verifies `dagu run` resolves root `env` values that reference `consts`.
- A black box fixture verifies `dagu run` resolves list-form `env` entries in order.
- A black box fixture verifies `dagu run` resolves Dagu references in a supported non-`run` field.
- A black box fixture verifies missing Dagu references fail before the owning step starts.
- A black box fixture verifies `$NAME` is not resolved by Dagu.
- A black box fixture verifies `$consts.name`, `$params.name`, `$steps.step_id.outputs.name`, and `$env.NAME` fail as invalid Dagu-looking shorthand.
- A black box fixture verifies Dagu value resolution does not execute `$()` or backtick command substitution.
