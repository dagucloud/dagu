# Spec: Field Evaluation

Scope: workflow field evaluation classes, evaluation timing, and evaluator ordering.

## Objective

Define which workflow fields are literal, which fields resolve Dagu value references, and which fields run Dagu dynamic evaluation.

## Inputs

Input is a workflow YAML file accepted by the YAML schema spec.

The validation requirements in this spec extend `dagu validate` when field-evaluation validation is implemented. They are not part of the root/document validation boundary defined by the YAML schema spec.

**Input rules:**

- Field evaluation applies only to fields explicitly listed by this spec or by the owning field spec.
- A field that is not listed must not run Dagu dynamic evaluation unless its own spec explicitly opts in.
- Runtime parameter overrides are caller-provided values and are not evaluated.
- Shell command-substitution syntax in a field is not enough to make Dagu execute it.

**Parameter `eval` fields may compute a value:**

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

Fields opt in to value resolution and dynamic evaluation separately.

**Field classes:**

- Literal fields do not resolve Dagu value references and do not run Dagu dynamic evaluation.
- Value-resolved fields resolve Dagu `${...}` references but do not run Dagu dynamic evaluation.
- Dynamic-evaluated fields run the Dagu dynamic evaluation pipeline.
- Field-specific fields define evaluation behavior in the owning field or executor spec.

**Field evaluation surface:**

| Field | Class | Timing | Notes |
| --- | --- | --- | --- |
| Root `consts.*` | Literal | Validation/load | Literal strings, numbers, and booleans only. |
| `params[].default` | Literal | Run start fallback | Used as a literal fallback value. |
| Runtime parameter overrides | Literal | Caller input | CLI, API, and sub-DAG-provided values are not evaluated. |
| `params[].eval` | Dynamic-evaluated | Before the DAG run starts | Used only when the caller does not provide the parameter. |
| Root `env` values | Dynamic-evaluated | DAG load or run setup | Computes DAG-scoped environment values. |
| `dotenv` paths | Dynamic-evaluated | DAG load or run setup | Resolves the path before loading the dotenv file. |
| Step `run` | Value-resolved | Step start | Dagu resolves `${...}` references, then the target shell receives the command string. |
| Step `env` values | Field-specific | Step start | The owning executor or runtime path decides whether value resolution is enabled; dynamic evaluation is not implied. |
| Executor `with` fields | Field-specific | Step start | Each executor spec decides whether value resolution or dynamic evaluation is enabled. |
| Object-form `output` string leaves | Value-resolved | Output publication | Dagu dynamic evaluation and shell expansion are not run while publishing outputs. |
| `secrets[].key` and `secrets[].options` | Literal | Secret resolution | Provider inputs are literal strings. |

**Evaluation order:**

- Value-resolved fields run Dagu value resolution before the field is consumed.
- Dynamic-evaluated fields run Dagu dynamic evaluation before the field is consumed.
- Literal fields preserve `$()`, backticks, `$NAME`, and other shell syntax as literal text.
- Value-resolved fields preserve `$()`, backticks, and `$NAME` for the owning runtime to interpret later.
- Step `run` is shell text after Dagu value resolution. The target shell may evaluate `$()` or backticks after Dagu hands it the command string.

**Parameter evaluation rules:**

- If the caller provides a parameter value, Dagu uses that value and does not evaluate `params[].eval`.
- If the caller does not provide a parameter value and `eval` exists, Dagu evaluates `eval`.
- An evaluated parameter value becomes the value of `params.<name>` for the DAG run.
- If parameter `eval` fails and `default` exists, Dagu uses the literal default value.
- If parameter `eval` fails and no `default` exists, the DAG run fails before any step starts.

## Outputs

**Output rules:**

- Field evaluation does not write workflow result events, run logs, or artifacts by itself.
- When field evaluation succeeds, the resolved value is supplied to the owning field.

## Errors

**Validation errors:**

- A malformed value reference in a value-resolved or dynamic-evaluated field must fail during workflow validation when it is statically checkable.

**Runtime errors:**

- A value-resolution failure must fail before the owning field is consumed.
- A dynamic-evaluation failure must fail before the owning field is consumed.

## Examples

Literal default value:

```yaml
params:
  - name: today
    type: string
    default: `date +%Y%m%d`
steps:
  - name: print
    run: printf '%s\n' '${params.today}'
```

Parameter value from `eval`:

```yaml
params:
  - name: today
    type: string
    eval: `printf 20260131`
steps:
  - name: print
    run: echo ${params.today}
```

Step `run` is value-resolved by Dagu, then interpreted by the shell:

```yaml
params:
  - name: message
    type: string
    default: hello
steps:
  - name: print
    run: echo ${params.message} $(date +%Y)
```

## Acceptance Criteria

- A black-box fixture verifies `params[].default` is literal and does not run Dagu dynamic evaluation.
- A black-box fixture verifies `dagu run` resolves a parameter value produced by `params[].eval`.
- A black-box fixture verifies an explicit runtime parameter skips evaluation of that parameter `eval`.
- A black-box fixture verifies `dagu run` dynamically evaluates an opted-in non-`run` field such as root `env`.
- A black-box fixture verifies `$()` in `params[].eval` is not executed by Dagu.
- A black-box fixture verifies `$()` in `run` is not evaluated by Dagu before the shell receives the command string.
- A black-box fixture verifies object-form `output` string leaves resolve Dagu references without running Dagu dynamic evaluation.
