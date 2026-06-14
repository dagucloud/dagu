# Spec: Command Substitution

Scope: shell-style command substitution inside Dagu value-resolved fields.

## Objective

Define dynamic values using standard shell command-substitution syntax instead of Dagu-specific function calls or Go template syntax.

## Inputs

Input is a workflow YAML file accepted by the YAML schema spec.

The validation requirements in this spec extend `dagu validate` when command-substitution validation is implemented. They are not part of the root/document validation boundary defined by the YAML schema spec.

**Input rules:**

- Command substitution may appear only in fields that explicitly support value resolution and command substitution.
- This spec requires command substitution for parameter `default` fields.
- Root `consts` do not support command substitution.
- Dagu-specific function-call syntax is not part of the public workflow language.

**Parameter defaults may use command substitution:**

```yaml
params:
  - name: build_date
    type: string
    default: $(date +%Y%m%d)
steps:
  - name: print
    run: echo ${params.build_date}
```

## Behavior

Command substitution uses shell-style syntax:

```text
$(command)
`command`
```

The `$()` form is the preferred form. Backtick command substitution is supported for compatibility with existing shell syntax.

**Command-substitution syntax rules:**

- `$(` starts command substitution and the matching `)` ends it.
- Backtick command substitution starts with `` ` `` and ends with the next unescaped `` ` ``.
- `$()` substitutions may be nested when the shell syntax is balanced.
- Backtick substitutions must not be nested.
- The command text is passed to the platform shell.
- Command substitutions may contain Dagu value references such as `${params.name}`; those references are resolved before the command is executed.
- Command substitutions may contain shell variables such as `$HOME`; those variables are left for the shell to expand.
- Dagu-specific function-call syntax is invalid.
- Go template function-call syntax is invalid.

**Invalid Go template function syntax:**

```text
${shell "date +%Y%m%d"}
```

## Shell Execution

The command body is executed through the platform shell.

**Shell execution rules:**

| Rule | Behavior |
| --- | --- |
| POSIX shell | `/bin/sh -c`. |
| Windows shell | `%ComSpec% /S /C`, or `cmd.exe /S /C` when `ComSpec` is unset. |
| Working directory | Project root. |
| Environment | Inherits the Dagu process environment and Dagu runtime scope available to the owning field. |
| Return value | Stdout as a string. |
| Successful stderr | Captured and ignored. |
| Sandbox | Not sandboxed by Dagu. |

**Output rules:**

- Trailing newline bytes produced by stdout are removed from the returned value, matching command-substitution behavior.
- Other stdout bytes are returned as written.
- File, process, and network side effects are the side effects of the executed command.

**Failure and evaluation rules:**

- If the command exits with a non-zero status, value resolution fails.
- If the workflow run is aborted or times out while the command is running, Dagu must terminate the command process and value resolution fails.
- When this spec is implemented, `dagu validate` parses command-substitution syntax but does not execute commands.
- Command substitutions in parameter defaults are evaluated only when the parameter value is not provided by the caller.
- An evaluated parameter default becomes the value of `params.<name>` for the DAG run.
- Command substitutions outside parameter defaults are evaluated when the owning field is resolved.
- Each command-substitution occurrence is evaluated independently.

## Outputs

**Output rules:**

- Command substitutions do not write workflow result events, run logs, or artifacts by themselves.
- When command substitution succeeds, its return value is inserted into the resolved field value.

## Errors

**Validation errors:**

- Malformed command-substitution syntax must fail during workflow validation when it is statically checkable.
- Dagu-specific function-call syntax must fail during workflow validation.
- Go template function-call syntax must fail during workflow validation.

**Runtime errors:**

- A failed command must fail before the owning step starts.
- An aborted or timed-out command must fail before the owning step starts.

## Examples

Parameter default from command substitution:

```yaml
params:
  - name: today
    type: string
    default: $(printf 20260131)
steps:
  - name: print
    run: echo ${params.today}
```

Command substitution with Dagu references:

```yaml
params:
  - name: environment
    type: string
    required: true
steps:
  - name: print
    run: echo $(printf '%s-api' ${params.environment})
```

Backtick compatibility syntax:

```yaml
params:
  - name: today
    type: string
    default: `printf 20260131`
steps:
  - name: print
    run: echo ${params.today}
```

Invalid Go template syntax:

```yaml
steps:
  - name: bad
    run: echo ${shell "printf api"}
```

## Acceptance Criteria

- A black-box fixture verifies `dagu validate` accepts `$(printf ok)` syntax in fields that support command substitution.
- A black-box fixture verifies `dagu validate` accepts backtick command substitution in fields that support command substitution.
- A black-box fixture verifies `dagu validate` rejects Go template function syntax.
- A black-box fixture verifies malformed command-substitution syntax fails validation when it is statically checkable.
- A black-box fixture verifies `dagu run` resolves a parameter default produced by `$()` command substitution.
- A black-box fixture verifies `dagu run` resolves a parameter default produced by backtick command substitution.
- A black-box fixture verifies an explicit runtime parameter skips evaluation of that parameter default.
- A black-box fixture verifies `dagu run` resolves command substitution in a step field.
- A black-box fixture verifies a non-zero command fails before the owning step starts.
