# Spec: Expression Functions

Scope: function calls inside Dagu value expressions.

## Objective

Define dynamic values without using shell-style variables or Go template syntax
as the public workflow language.

## Inputs

Input is a workflow YAML file accepted by the YAML schema spec.

Function calls may appear inside `${{ ... }}` only in fields that support value
resolution.

This spec requires value resolution for parameter `default` fields.

Root `consts` do not support function calls.

Parameter defaults may use function calls:

```yaml
params:
  - name: build_date
    type: string
    default: ${{ shell("date +%Y%m%d") }}
steps:
  - name: print
    run: echo ${{ params.build_date }}
```

## Behavior

A function call uses this syntax:

```text
${{ name("argument") }}
```

Function names must use `snake_case`.

Arguments are separated by commas.

This spec supports only double-quoted string arguments.

Supported string escapes are `\"`, `\\`, `\n`, and `\t`.

Nested function calls are invalid.

Path references as function arguments are invalid.

Go template function syntax is invalid:

```text
${{ shell "date +%Y%m%d" }}
```

Unknown functions are invalid.

The only function defined by this spec is `shell`.

### `shell`

`shell(command)` executes `command` through the platform shell.

On POSIX systems, the platform shell is `/bin/sh -c`.

On Windows, the platform shell is `%ComSpec% /S /C`, or `cmd.exe /S /C` when
`ComSpec` is unset.

`shell` requires exactly one string argument.

`shell` runs with the project root as its working directory.

`shell` inherits the Dagu process environment.

`shell` returns stdout as a string.

If stdout ends with one `\n` or one `\r\n`, that line ending is removed from the
returned value.

Other stdout bytes are returned as written.

Successful `shell` stderr is captured and ignored.

`shell` is not sandboxed by Dagu.

File, process, and network side effects are the side effects of the executed
command.

If `shell` exits with a non-zero status, value resolution fails.

If the workflow run is aborted or times out while `shell` is running, Dagu must
terminate the `shell` process and value resolution fails.

`dagu workflow validate` parses function syntax, function names, and function
arity, but does not execute functions.

Function calls in parameter defaults are evaluated only when the parameter value
is not provided by the caller.

An evaluated parameter default becomes the value of `params.<name>` for the DAG
run.

Function calls outside parameter defaults are evaluated when the owning field is
resolved.

Each function call occurrence is evaluated independently.

## Outputs

Function calls do not write workflow result events, run logs, or artifacts by
themselves.

When a function call succeeds, its return value is inserted into the resolved
field value.

## Errors

Malformed function syntax must fail during workflow validation.

Invalid function names must fail during workflow validation.

Unknown functions must fail during workflow validation.

Invalid function arity must fail during workflow validation.

Invalid function argument syntax must fail during workflow validation.

A failed `shell` command must fail before the owning step starts.

An aborted or timed-out `shell` command must fail before the owning step starts.

## Examples

Parameter default from `shell`:

```yaml
params:
  - name: today
    type: string
    default: ${{ shell("printf 20260131") }}
steps:
  - name: print
    run: echo ${{ params.today }}
```

Direct field value from `shell`:

```yaml
steps:
  - name: print
    run: echo ${{ shell("printf api") }}
```

Invalid Go template syntax:

```yaml
steps:
  - name: bad
    run: echo ${{ shell "printf api" }}
```

## Acceptance Criteria

- A black-box fixture verifies `dagu workflow validate` accepts
  `${{ shell("printf ok") }}` syntax.
- A black-box fixture verifies `dagu workflow validate` rejects Go template
  function syntax.
- A black-box fixture verifies `dagu workflow validate` rejects unknown
  functions.
- A black-box fixture verifies `dagu workflow validate` rejects invalid
  `shell` arity.
- A black-box fixture verifies `dagu run` resolves a parameter default produced
  by `shell`.
- A black-box fixture verifies an explicit runtime parameter skips evaluation of
  that parameter default.
- A black-box fixture verifies `dagu run` resolves a direct `shell` call in a
  step field.
- A black-box fixture verifies a non-zero `shell` command fails before the
  owning step starts.
