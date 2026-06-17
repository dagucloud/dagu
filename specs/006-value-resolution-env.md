# Spec: Value Resolution Env

## Implementation Status

Not implemented.
This spec describes target conformance behavior.
It must not be treated as current product behavior.

## Scope

This spec defines environment values used by Dagu value resolution.

It covers:

- `${env.NAME}` references
- `$NAME` and `${NAME}` environment expansion
- ordering and validation rules for `env` declarations

Common value-resolution syntax, supported fields, string insertion, and timing are defined by [Spec 003: Value Resolution](003-value-resolution.md).

## Goal

Workflow authors can define environment values once and use them in every field that supports environment expansion.

## Motivation

Environment values can come from YAML, dotenv files, runtime params, step outputs, container settings, step settings, and the process environment.

Dagu must preserve environment syntax when another runtime, such as a shell or executor, owns that syntax.

## Behavior

### Environment Declarations

- `env` declarations may use map form, array-of-map form, or `NAME=value` list form.

- Each `NAME=value` list item must use `NAME=value` form.

- Env names must match `^[A-Za-z_][A-Za-z0-9_]*$`.

Ordering rules:

- Entries are evaluated from top to bottom.

- An entry may reference earlier entries from the same list.

- An entry cannot resolve itself or a later entry from the same list.

Allowed references:

- Root `env` may reference `consts`, `params`, and env values that are already available.

- Step `env` may reference `consts`, `params`, env values that are already available, and step outputs that are available before the step starts.

- Container `env` follows the same rule as the root or step that owns the container.

### Environment References

Forms:

```text
${env.NAME}
$NAME
${NAME}
```

Rules:

- All forms read the environment value named `NAME`.

- `NAME` must match `^[A-Za-z_][A-Za-z0-9_]*$`.

Missing values:

- Missing `${env.NAME}` emits a passive diagnostic and preserves the original reference text when the field is evaluated.

- Missing `$NAME` or `${NAME}` is preserved when environment expansion runs.

Validation:

- `dagu validate` must not reject a well-formed environment reference only because `NAME` is unavailable during validation.

Insertion:

- `${env.NAME}` uses the string insertion rules from Spec 003.

### Single-Quoted Environment References

- Single-quoted `$NAME` and `${NAME}` are preserved during Dagu environment expansion.

### Shell-Style Environment Expressions

- `${NAME:-default}` and `${NAME:4:10}` are environment references with shell-style operators.

- They read the environment value named `NAME`.

- These expressions are valid only in fields that support environment expansion.

- If `NAME` is unavailable when environment expansion runs, Dagu leaves the expression unchanged.

### Shell and Direct Execution

Step `run` with a shell:

- Dagu resolves `${env.NAME}` before the command starts.

- Dagu passes `$NAME`, `${NAME}`, and shell-style environment expressions to the shell.

- The shell reads environment values from the step process environment.

Direct execution without a shell:

- There is no later shell expansion.

- `$NAME`, `${NAME}`, and shell-style environment expressions are resolved only when Dagu expands environment values for the field.

- Unresolved environment references stay unchanged.

### Validation

- `dagu validate` must reject invalid `env` declaration shapes.

- `dagu validate` must reject invalid environment variable names in `env` declarations.

- Braced text that does not match a supported environment reference form is ordinary string content.

- A list-form `env` entry that references itself or a later entry in the same list must emit a passive diagnostic and preserve the original reference text.

- `dagu validate` must not require runtime environment values, process environment values, dotenv values, or predecessor step outputs to exist.

## Examples

Ordered environment entries:

```yaml
env:
  - SERVICE=api
  - API_HOST=${env.SERVICE}.internal
steps:
  - name: print_api_host
    run: echo ${env.API_HOST}
```

Step environment values:

```yaml
steps:
  - name: deploy
    env:
      - SERVICE=api
    with:
      host: api.${env.SERVICE}.internal
```

Environment expansion without a namespace:

```yaml
env:
  - SERVICE=api
steps:
  - name: deploy
    with:
      host: api.${SERVICE}.internal
```

Shell-backed `run`:

```yaml
env:
  - SERVICE=api
steps:
  - name: shell_run
    run: echo "$SERVICE ${env.SERVICE}"
```

Direct execution without a shell:

```yaml
env:
  - SERVICE=api
steps:
  - name: direct_exec
    action: exec
    with:
      command: /usr/bin/printf
      args:
        - '%s\n'
        - ${SERVICE}
```

Preserved missing environment value:

```yaml
steps:
  - name: optional_env
    run: echo ${OPTIONAL_ENV}
```
