# Spec: Value Resolution

## Scope

This spec defines Dagu reference syntax, root `consts`, and runtime value lookup.

It does not define dynamic evaluation, step identity, output publication, shell variable expansion, or field-specific opt-in rules outside the fields named by this spec.

## Goal

Define how workflow fields reference values using the current `${...}` format.

## Inputs

Input is a workflow YAML file accepted by the YAML schema spec.

The validation requirements in this spec extend `dagu validate` when value-resolution validation is implemented. They are not part of the root/document validation boundary defined by the YAML schema spec.

**Immutable values can be defined with `consts`:**

```yaml
consts:
  service: api
  deploy_script: ./scripts/deploy.sh
  enabled: true
```

**Step `run` fields may contain Dagu references:**

```yaml
steps:
  - name: deploy
    run: ${consts.deploy_script} ${params.environment}
```

## Behavior

Dagu references use this syntax:

```text
${path}
```

The path must be namespaced. Dotted references must use the `${name.path}` form. `$name.path` is rejected as invalid Dagu-looking reference syntax when `name` is a supported namespace.

**Supported namespaces:**

| Namespace | Meaning |
| --- | --- |
| `consts` | Immutable values from root `consts`. |
| `params` | Runtime parameters. |
| `steps` | Outputs from completed steps, addressed by step `id`. |

**Supported reference forms:**

```text
${consts.name}
${params.name}
${steps.step_id.outputs.name}
```

**Reference rules:**

- Reference path segments must match `^[A-Za-z][A-Za-z0-9_]*$`.
- Unqualified references such as `${name}` are invalid.
- Dotted references must use `${name.path}`.
- `$consts.name`, `$params.name`, and `$steps.step_id.outputs.name` are invalid Dagu-looking reference syntax.
- Dagu value resolution must not execute `$()` or backtick command substitution.
- Dynamic evaluation is defined by the dynamic evaluation spec and enabled only by the field evaluation spec or the owning field spec.
- Operators, filters, and inline default values are not part of this spec.
- This spec requires value resolution for step `run` fields.
- Other fields support value resolution only when their own specs say so.

**`consts` rules:**

- Root `consts` may be either a mapping or an ordered list of single-entry mappings.
- `consts` keys must match `^[A-Za-z][A-Za-z0-9_]*$`.
- `consts` values must be literal strings, numbers, or booleans.
- Numeric `consts` values must be finite.
- Mapping-form `consts` values must not contain Dagu references.
- List-form `consts` values are resolved in order and string values may reference earlier `consts` entries with `${consts.name}`.
- List-form `consts` values must not reference runtime `params`, `steps`, later `consts`, or themselves.
- `consts` values are immutable for one DAG run.

**Runtime lookup rules:**

- Runtime parameters are referenced only through `params`.
- Step outputs are referenced only through `steps.<step_id>.outputs.<name>`.
- Step `id` behavior is defined by the step reference spec.
- When a referenced value is inserted into a string field, strings are inserted as written and booleans are inserted as `true` or `false`.
- Numeric values are formatted from the parsed YAML value, not from the source spelling. Integers are inserted in base-10 decimal form. Non-integer numbers are inserted as the shortest round-trippable base-10 decimal representation.
- Dagu must not resolve shell-style `$NAME` syntax.
- Dagu must not execute `$()` or backtick command substitution during value resolution.
- Shell-style variables and command-substitution syntax remain part of the field value after Dagu value resolution unless the owning field spec rejects them. A shell or target runtime may expand them later only when the owning field spec permits that runtime behavior.

## Outputs

**Output rules:**

- Value resolution does not write workflow result events, run logs, or artifacts.
- When value resolution succeeds for a step field, the step receives the resolved field value.

## Errors

**Runtime resolution errors:**

- Malformed Dagu reference syntax must fail before the owning step starts.
- Invalid Dagu-looking shorthand reference syntax must fail before the owning step starts.
- An unknown namespace must fail before the owning step starts.
- An unknown `consts` reference must fail before the owning step starts.
- An unknown `params` reference must fail before the owning step starts.
- An unknown `steps` output reference must fail before the owning step starts.
- An unavailable step output must fail before the owning step starts.

**Validation errors:**

- Invalid `consts` shape must fail during workflow validation.
- Invalid `consts` key names must fail during workflow validation.
- Invalid `consts` value types must fail during workflow validation.
- Non-finite numeric `consts` values must fail during workflow validation.

## Examples

Valid consts and parameters:

```yaml
consts:
  deploy_script: ./scripts/deploy.sh
  service: api
params:
  - name: environment
    type: string
    required: true
steps:
  - name: deploy
    run: ${consts.deploy_script} ${params.environment} ${consts.service}
```

Ordered consts with const-to-const references:

```yaml
consts:
  - service: api
  - endpoint: http://localhost/${consts.service}
steps:
  - name: print_endpoint
    run: echo ${consts.endpoint}
```

Shell variables are not resolved by Dagu:

```yaml
steps:
  - name: shell_env
    run: echo "$HOME"
```

Invalid unqualified reference:

```yaml
steps:
  - name: bad
    run: echo ${environment}
```

Invalid consts reference:

```yaml
steps:
  - name: bad
    run: echo ${consts.missing}
```

Invalid dotted reference syntax:

```yaml
steps:
  - name: bad
    run: echo $consts.service
```

Dagu does not execute command substitution during value resolution:

```yaml
steps:
  - name: shell_text
    run: echo "$(date)" "`date`"
```

## Acceptance Criteria

- A black-box fixture verifies `dagu validate` accepts literal `consts` values.
- A black-box fixture verifies `dagu validate` rejects invalid `consts` value types.
- A black-box fixture verifies `dagu validate` rejects non-finite numeric `consts` values.
- A black-box fixture verifies `dagu validate` rejects an unqualified Dagu reference.
- A black-box fixture verifies `dagu run` resolves `${consts.name}`.
- A black-box fixture verifies `dagu run` resolves `${params.name}`.
- A black-box fixture verifies `dagu run` resolves `${steps.step_id.outputs.name}` after the referenced step completes.
- A black-box fixture verifies missing Dagu references fail before the owning step starts.
- A black-box fixture verifies `$NAME` is not resolved by Dagu.
- A black-box fixture verifies `$consts.name`, `$params.name`, and `$steps.step_id.outputs.name` fail as invalid Dagu-looking shorthand reference syntax.
- A black-box fixture verifies Dagu value resolution does not execute `$()` or backtick command substitution.
