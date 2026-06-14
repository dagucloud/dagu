# Spec: Dynamic Evaluation

Scope: Dagu dynamic evaluation for fields that explicitly opt in.

## Objective

Define current Dagu dynamic evaluation behavior without treating all shell syntax as a workflow-language feature.

## Inputs

Input is a workflow YAML file accepted by the YAML schema spec.

The validation requirements in this spec extend `dagu validate` when dynamic-evaluation validation is implemented. They are not part of the root/document validation boundary defined by the YAML schema spec.

Dagu dynamic evaluation is available only in fields classified as dynamic-evaluated fields by the field evaluation spec or by the owning field spec.

**Dynamic-evaluated field example:**

```yaml
params:
  - name: build_date
    type: string
    eval: `date +%Y%m%d`
steps:
  - name: print
    run: echo ${params.build_date}
```

## Behavior

Dynamic evaluation runs Dagu variable expansion and legacy backtick command substitution.

**Dynamic evaluation rules:**

- Dagu expands value references such as `${params.name}` before executing legacy backtick command substitution.
- Dagu expands available environment variables such as `$HOME` and `${HOME}` according to the owning field's evaluation scope.
- Dagu executes commands enclosed in backticks.
- Backtick command output is inserted into the evaluated field value.
- `$()` syntax is not Dagu dynamic evaluation and is preserved as text by Dagu.
- `$()` may still be interpreted later by a target shell if the owning field passes the string to a shell.
- Backtick substitution is legacy behavior. New fields must not enable it unless their spec explicitly opts in.

**Backtick syntax rules:**

- A backtick command starts with `` ` `` and ends with the next unescaped `` ` ``.
- Backtick commands must not be nested.
- An escaped backtick is preserved literally.
- An unclosed backtick is preserved literally.
- The command text is passed to the configured shell for execution.

## Shell Execution

Backtick command bodies are executed through the configured shell.

**Shell execution rules:**

| Rule | Behavior |
| --- | --- |
| Shell | Dagu's configured default shell, the scoped `SHELL`, or the platform fallback selected by the implementation. |
| Environment | Inherits the Dagu evaluation scope available to the owning field. |
| Return value | Stdout as a string after trimming surrounding whitespace. |
| Successful stderr | Captured and ignored. |
| Timeout | The implementation applies a short bounded timeout. |
| Sandbox | Not sandboxed by Dagu. |

**Output rules:**

- File, process, and network side effects are the side effects of the executed command.
- Dynamic evaluation does not write workflow result events, run logs, or artifacts by itself.

**Failure rules:**

- If the command exits with a non-zero status, dynamic evaluation fails.
- If the command times out, dynamic evaluation fails.
- When this spec is implemented, `dagu validate` parses dynamic-evaluation syntax but does not execute commands.
- Each backtick substitution occurrence is evaluated independently.

## Outputs

**Output rules:**

- When dynamic evaluation succeeds, its evaluated value is inserted into the owning field.
- `$()` text remains part of the evaluated value unless another evaluation phase or target runtime interprets it later.

## Errors

**Validation errors:**

- Malformed Dagu value references in a dynamic-evaluated field must fail during workflow validation when statically checkable.

**Runtime errors:**

- A failed backtick command must fail before the owning field is consumed.
- A timed-out backtick command must fail before the owning field is consumed.

## Examples

Parameter value from legacy backtick substitution:

```yaml
params:
  - name: today
    type: string
    eval: `printf 20260131`
steps:
  - name: print
    run: echo ${params.today}
```

Dynamic evaluation with Dagu references:

```yaml
params:
  - name: environment
    type: string
    required: true
env:
  - SERVICE_NAME: `printf '%s-api' ${params.environment}`
steps:
  - name: print
    run: echo ${SERVICE_NAME}
```

`$()` is preserved by Dagu dynamic evaluation:

```yaml
params:
  - name: literal
    type: string
    eval: $(printf 20260131)
steps:
  - name: print
    run: printf '%s\n' '${params.literal}'
```

## Acceptance Criteria

- A black-box fixture verifies `dagu run` resolves a parameter value produced by backtick substitution in `eval`.
- A black-box fixture verifies dynamic evaluation expands available variables before legacy backtick substitution.
- A black-box fixture verifies `$()` in `params[].eval` is preserved by Dagu and not executed during dynamic evaluation.
- A black-box fixture verifies dynamic evaluation is not enabled for step `run` before the shell receives the command string.
- A black-box fixture verifies malformed value references fail validation when they are statically checkable.
- A black-box fixture verifies a non-zero backtick command fails before the owning field is consumed.
