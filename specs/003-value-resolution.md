# Spec: value resolution

## Scope

This spec covers Dagu reference syntax, root `consts`, and runtime value lookup.

It does not cover dynamic evaluation, step identity, output publication, shell variable expansion, or value resolution in fields that this spec does not name.

## Goal

Workflows can reference values with `${...}` syntax.

## Inputs

Input is a workflow YAML file accepted by the YAML schema spec.

This spec adds validation rules to `dagu validate`. These rules are separate from the root and document validation boundary in the YAML schema spec.

`consts` defines immutable values:

```yaml
consts:
  service: api
  deploy_script: ./scripts/deploy.sh
  enabled: true
```

Step `run` fields may use Dagu references:

```yaml
steps:
  - name: deploy
    run: ${consts.deploy_script} ${params.environment}
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

Supported reference forms:

```text
${consts.name}
${params.name}
${steps.step_id.outputs.name}
```

Reference rules:

- Reference path segments must match `^[A-Za-z][A-Za-z0-9_]*$`.
- `${name}` has no Dagu namespace. Dagu value resolution must leave it unchanged for legacy variable resolution, shell expansion, or the owning field's evaluator.
- Dotted references must use `${name.path}`.
- `$consts.name`, `$params.name`, and `$steps.step_id.outputs.name` are invalid Dagu-looking shorthand.
- Dagu value resolution must not execute `$()` or backtick command substitution.
- Dynamic evaluation belongs to the dynamic evaluation spec and only runs where the field evaluation spec or owning field spec enables it.
- Operators, filters, and inline default values are outside this spec.
- Step `run` fields support value resolution.
- Other fields support value resolution only when their own specs say so.

`consts` rules:

- Root `consts` may be a mapping or an ordered list of single-entry mappings.
- `consts` keys must match `^[A-Za-z][A-Za-z0-9_]*$`.
- `consts` values must be literal strings, numbers, or booleans.
- Numeric `consts` values must be finite.
- Mapping-form `consts` values must not contain Dagu references.
- List-form `consts` values are resolved in order. String values may reference earlier `consts` entries with `${consts.name}`.
- List-form `consts` values must not reference runtime `params`, `steps`, later `consts`, or themselves.
- `consts` values are immutable for one DAG run.

Runtime lookup rules:

- Runtime parameters are referenced only through `params`.
- A `params` reference must name a parameter declared by root `params`.
- Named parameters may come from legacy named parameter entries, rich parameter definitions, or explicit schema properties.
- Positional parameters are not addressable through `params`.
- Step outputs are referenced only through `steps.<step_id>.outputs.<name>`.
- A `steps` reference must name an existing step `id`.
- A `steps.<step_id>.outputs.<name>` reference must name a declared output when the referenced step declares an output contract.
- Step `id` behavior belongs to the step reference spec.
- When Dagu inserts a referenced value into a string field, strings are inserted as written and booleans are inserted as `true` or `false`.
- Numeric values are formatted from the parsed YAML value, not from the source spelling. Integers use base-10 decimal form. Non-integer numbers use the shortest round-trippable base-10 decimal representation.
- Dagu must not resolve shell-style `$NAME` syntax.
- Dagu must not resolve unqualified `${NAME}` syntax as a Dagu reference.
- Dagu must not execute `$()` or backtick command substitution during value resolution.
- Shell-style variables and command-substitution text remain in the field after Dagu value resolution unless the owning field spec rejects them. A shell or target runtime may expand them later only when the owning field spec allows it.

## Outputs

- Value resolution does not write workflow result events, run logs, or artifacts.
- When value resolution succeeds for a step field, the step receives the resolved field value.

## Errors

Workflow validation errors:

- Malformed Dagu reference syntax in a value-resolution field must fail during workflow validation.
- Invalid Dagu-looking shorthand in a value-resolution field must fail during workflow validation.
- An unknown namespace in a value-resolution field must fail during workflow validation.
- An unknown `consts` reference must fail during workflow validation.
- An undeclared `params` reference must fail during workflow validation.
- An unknown `steps.<step_id>` reference must fail during workflow validation.
- An unknown `steps.<step_id>.outputs.<name>` reference must fail during workflow validation when the referenced step declares an output contract.
- Invalid `consts` shape must fail during workflow validation.
- Invalid `consts` key names must fail during workflow validation.
- Invalid `consts` value types must fail during workflow validation.
- Non-finite numeric `consts` values must fail during workflow validation.

Runtime pre-step errors:

- A missing runtime value for a declared `params` reference must fail before the owning step starts.
- An unavailable step output value must fail before the owning step starts.
- An unknown `steps.<step_id>.outputs.<name>` reference must fail before the owning step starts when the referenced step does not declare an output contract that can be checked statically.

## Examples

Valid `consts` and parameters:

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

Ordered `consts` with const-to-const references:

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
    run: echo $consts.service
```

Dagu does not execute command substitution during value resolution:

```yaml
steps:
  - name: shell_text
    run: echo "$(date)" "`date`"
```

## Acceptance criteria

- A black box fixture verifies `dagu validate` accepts literal `consts` values.
- A black box fixture verifies `dagu validate` rejects invalid `consts` value types.
- A black box fixture verifies `dagu validate` rejects non-finite numeric `consts` values.
- A black box fixture verifies `dagu validate` accepts an unqualified `${name}` placeholder.
- A black box fixture verifies `dagu validate` rejects an unknown `consts` reference.
- A black box fixture verifies `dagu validate` rejects an undeclared `params` reference.
- A black box fixture verifies `dagu validate` rejects an unknown `steps.<step_id>` reference.
- A black box fixture verifies `dagu validate` rejects an unknown namespace in a Dagu reference.
- A black box fixture verifies `dagu run` resolves `${consts.name}`.
- A black box fixture verifies `dagu run` resolves `${params.name}`.
- A black box fixture verifies `dagu run` resolves `${steps.step_id.outputs.name}` after the referenced step completes.
- A black box fixture verifies missing Dagu references fail before the owning step starts.
- A black box fixture verifies `$NAME` is not resolved by Dagu.
- A black box fixture verifies `$consts.name`, `$params.name`, and `$steps.step_id.outputs.name` fail as invalid Dagu-looking shorthand.
- A black box fixture verifies Dagu value resolution does not execute `$()` or backtick command substitution.
