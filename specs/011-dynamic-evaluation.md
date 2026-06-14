# Spec: Dynamic Evaluation

## Scope

This spec defines Dagu dynamic evaluation for fields that explicitly opt in.

It does not make every shell syntax form part of the workflow language.

## Goal

Define what Dagu does when a field is marked dynamic-evaluated.

In this spec set, dynamic evaluation is available only for `params[].eval`. Other fields must opt in through the field evaluation spec or their owning spec.

## Input

Input is a workflow YAML file accepted by the YAML schema spec.

Dynamic-evaluation validation extends `dagu validate` when that validation is implemented. Validation may parse dynamic-evaluation syntax, but it must not execute commands.

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

1. Resolve Dagu value references such as `${params.name}`.
2. Expand available environment variables according to the field's evaluation scope.
3. Execute legacy backtick command substitutions.

Rules:

- Dagu expands value references before executing backtick commands.
- Dagu expands available environment variables such as `$HOME` and `${HOME}` according to the owning field's scope.
- Dagu executes commands enclosed in backticks.
- Dagu inserts backtick command stdout into the evaluated value after trimming surrounding whitespace.
- Backtick text in fields other than `params[].eval` is not dynamic evaluation. Dagu leaves it unchanged.
- `$()` command substitution is not supported by Dagu dynamic evaluation. Dagu leaves `$()` text unchanged.

## Backtick syntax

Rules:

- A backtick command starts with `` ` `` and ends with the next unescaped `` ` ``.
- Backtick commands must not be nested.
- An escaped backtick is preserved literally.
- An unclosed backtick is preserved literally.
- The command text is passed to the configured shell.

## Shell execution

Backtick command bodies run through the configured shell.

| Rule | Behavior |
| --- | --- |
| Shell | Dagu's configured default shell, the scoped `SHELL`, or the platform fallback selected by the implementation. |
| Environment | The command inherits the evaluation scope available to the owning field. |
| Return value | Stdout, trimmed of surrounding whitespace. |
| Successful stderr | Captured and ignored. |
| Timeout | The implementation applies a short bounded timeout. |
| Sandbox | Dagu does not sandbox the command. |

Command side effects are real side effects. If the command writes files, starts processes, or uses the network, those effects happen outside the workflow result model.

Dynamic evaluation itself does not write workflow events, run logs, artifacts, or result files.

## Failures

Rules:

- A backtick command that exits with a non-zero status makes dynamic evaluation fail.
- A backtick command that times out makes dynamic evaluation fail.
- Each backtick substitution occurrence is evaluated independently.
- The field evaluation spec defines field-specific fallbacks, such as `params[].eval` falling back to `default`.

## Outputs

When dynamic evaluation succeeds, Dagu inserts the evaluated value into the owning field.

`$()` text remains part of the evaluated value unless a later phase or target runtime interprets it.

## Errors

Validation errors:

- Malformed Dagu value references in a dynamic-evaluated field must fail during workflow validation when they are statically checkable.

Runtime errors:

- A failed backtick command must fail before the owning field is consumed, unless the owning field defines a fallback.
- A timed-out backtick command must fail before the owning field is consumed, unless the owning field defines a fallback.

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

Unsupported `$()` substitution stays text:

```yaml
params:
  - name: today
    type: string
    eval: $(date)
```

## Acceptance criteria

- A black-box fixture verifies `dagu run` resolves a parameter value produced by backtick substitution in `eval`.
- A black-box fixture verifies dynamic evaluation expands available variables before legacy backtick substitution.
- A black-box fixture verifies `$()` in `params[].eval` is preserved by Dagu and not executed during dynamic evaluation.
- A black-box fixture verifies backtick text in step `run` is not evaluated by Dagu.
- A black-box fixture verifies backtick text in root `env` is not evaluated by Dagu.
- A black-box fixture verifies malformed value references fail validation when they are statically checkable.
- A black-box fixture verifies a non-zero backtick command fails before the owning field is consumed, unless that field defines a fallback.
