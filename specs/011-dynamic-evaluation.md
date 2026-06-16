# Spec: Dynamic Evaluation

## Implementation Status

Not implemented.
This spec describes target conformance behavior.
It must not be treated as current product behavior.

## Scope

This spec defines Dagu dynamic evaluation for fields that explicitly opt in.

It does not make every shell syntax form part of the workflow language.

## Goal

Define what Dagu does when a field is marked dynamic-evaluated.

In this spec set, dynamic evaluation is available only for `params[].eval`.
Other fields must opt in through the field evaluation spec or their owning spec.

## Input

Input is a workflow YAML file accepted by the YAML schema spec.

Dynamic-evaluation validation extends `dagu validate` when that validation is implemented.
Validation parses dynamic-evaluation syntax, but it must not execute commands.

Example:

```yaml
params:
  - name: build_date
    type: string
    eval: `date +%Y%m%d`
steps:
  - name: print
    run: echo ${params.build_date}
```

## Evaluation pipeline

Dynamic evaluation for `params[].eval` runs these operations in order:

- Resolve Dagu value references such as `${params.name}`.
- Expand available environment variables according to the field's evaluation scope.
- Execute command substitutions.

Rules:

- Dagu expands value references before executing command substitutions.
- Dagu expands available environment variables such as `$HOME` and `${HOME}` according to the owning field's scope.
- Dagu executes command substitutions written in backtick form or `$()` form.
- Dagu inserts command stdout into the evaluated value after trimming surrounding whitespace.
- Backtick text and `$()` text in fields other than `params[].eval` are not dynamic evaluation.
  Dagu leaves them unchanged.

## Command Substitution Syntax

Rules:

- A backtick command starts with `` ` `` and ends with the next unescaped `` ` ``.
- An escaped backtick is preserved literally.
- An unclosed backtick is preserved literally.
- A `$()` command starts with `$(` and ends with the next unescaped `)`.
- An escaped `)` inside `$()` command text is preserved literally.
- An unclosed `$()` command is preserved literally.
- Command substitutions must not be nested.
- The command text is passed to the configured shell.

## Shell execution

Command substitution bodies run through the configured shell.

| Rule | Behavior |
| --- | --- |
| Shell | Dagu's configured default shell, the scoped `SHELL`, or the platform fallback selected by the implementation. |
| Environment | The command inherits the evaluation scope available to the owning field. |
| Return value | Stdout, trimmed of surrounding whitespace. |
| Successful stderr | Captured and ignored. |
| Timeout | The implementation applies a short bounded timeout. |
| Sandbox | Dagu does not sandbox the command. |

Command side effects are real side effects.
If the command writes files, starts processes, or uses the network, those effects are outside the workflow result model.

Dynamic evaluation itself does not write workflow events, run logs, artifacts, or result files.

## Failures

Rules:

- A command substitution that exits with a non-zero status makes dynamic evaluation fail.
- A command substitution that times out makes dynamic evaluation fail.
- Each command substitution occurrence is evaluated independently.
- The field evaluation spec defines field-specific fallbacks, such as `params[].eval` falling back to `default`.

## Outputs

When dynamic evaluation succeeds, Dagu inserts the evaluated value into the owning field.

Backtick text and `$()` text outside `params[].eval` remain part of the evaluated value.
A later phase or target runtime may still interpret them.

## Errors

Validation errors:

- Malformed Dagu-owned value references in a dynamic-evaluated field must fail during workflow validation.
  This applies when the references are statically checkable.
  Braced text that does not match a supported Dagu-owned reference form remains ordinary string content under Spec 003.

Runtime errors:

- A failed command substitution must fail before the owning field is consumed.
  This does not apply when the owning field defines a fallback.
- A timed-out command substitution must fail before the owning field is consumed.
  This does not apply when the owning field defines a fallback.

## Examples

Parameter value from backtick substitution:

```yaml
params:
  - name: today
    type: string
    eval: `printf 20260131`
steps:
  - name: print
    run: echo ${params.today}
```

Parameter value from `$()` substitution:

```yaml
params:
  - name: today
    type: string
    eval: $(printf 20260131)
steps:
  - name: print
    run: echo ${params.today}
```

Parameter `eval` with Dagu references:

```yaml
params:
  - name: environment
    type: string
    default: prod
  - name: service_name
    type: string
    eval: `printf '%s-api' ${params.environment}`
steps:
  - name: print
    run: echo ${params.service_name}
```

Command substitution outside `params[].eval` stays text for Dagu:

```yaml
env:
  TODAY: $(date)
```

## Acceptance criteria

- A black-box fixture verifies `dagu run` resolves a parameter value produced by backtick substitution in `eval`.
- A black-box fixture verifies `dagu run` resolves a parameter value produced by `$()` substitution in `eval`.
- A black-box fixture verifies dynamic evaluation expands available variables before command substitution.
- A black-box fixture verifies `$()` text outside `params[].eval`.
  The fixture proves Dagu preserves that text and does not execute it during dynamic evaluation.
- A black-box fixture verifies backtick text in step `run` is not evaluated by Dagu.
- A black-box fixture verifies backtick text in root `env` is not evaluated by Dagu.
- A black-box fixture verifies malformed Dagu-owned value references fail validation.
  This applies when the references are statically checkable.
- A black-box fixture verifies a non-zero command substitution fails before the owning field is consumed.
  This does not apply when that field defines a fallback.
