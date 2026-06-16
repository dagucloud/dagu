# Spec: Field Evaluation

## Implementation Status

Not implemented. This spec describes target conformance behavior.

## Scope

This spec answers one question.
When Dagu sees text in a workflow field, what does it do?
It can leave the text alone, resolve Dagu references such as `${params.name}`, or run Dagu dynamic evaluation.

It does not define the shape of every field.
Field shape belongs to the YAML schema spec or to the spec that owns that field.

## Goal

Make field evaluation explicit.
A workflow author should be able to tell, for each field covered here, whether Dagu will:

- use the field exactly as written
- resolve Dagu value references
- run `params[].eval`
- leave shell syntax for a later runtime such as `/bin/sh`

## Inputs

Input is a workflow YAML file accepted by the YAML schema spec and by the specs that own the fields used in the file.

These names are easy to confuse, so this spec uses them exactly:

- Step `output` is the singular step field. Its accepted forms are defined by its owning spec.
- Step `stdout.outputs` publishes DAG or action outputs from stdout.
- Step top-level `outputs` is a separate field owned by the step outputs spec.
- `dagu-action.yaml` top-level `outputs` is an action manifest schema. It is not a normal workflow root field.
- A normal workflow root does not have a root `output` or root `outputs` field in this spec.

## Evaluation types

Dagu uses three evaluation types.

| Type | Meaning |
| --- | --- |
| Literal | Dagu uses the value exactly as written. It does not resolve `${...}` and does not run dynamic evaluation. |
| Value-resolved | Dagu resolves Dagu value references such as `${params.name}`. It does not run dynamic evaluation. |
| Dynamic-evaluated | Dagu runs the dynamic evaluation pipeline. In this spec, only `params[].eval` uses this type. |

Field-specific behavior means this spec does not make the decision. The field or executor spec must say what happens.

The value-resolution field list is defined by the value resolution spec.
If this table and that spec disagree about value-resolved fields, the value resolution spec is authoritative.

## Field table

| Field | Evaluation type | When it happens | Rule |
| --- | --- | --- | --- |
| Root `consts` values | Value-resolved | Workflow load | Dagu resolves only references allowed by the consts value-resolution spec. |
| `params[].default` | Literal | Run start, when needed | Dagu uses the default exactly as written. |
| Runtime parameter overrides | Literal | Caller input | Values from CLI, API, or sub-DAG calls are not evaluated. |
| `params[].eval` | Dynamic-evaluated | Before any step starts | Used only when the caller did not provide that parameter. Runs command substitution in forms allowed by the dynamic evaluation spec. |
| Root `env` values | Value-resolved | DAG load or run setup | Dagu resolves Dagu references. It does not run command substitution. |
| `dotenv` paths | Value-resolved | Before loading the dotenv file | Dagu resolves the path. It does not run command substitution. |
| Step `run` | Value-resolved | Step start | Dagu resolves Dagu-owned references, then starts the command-form or script-form `run` as defined by the step run specs. Dagu does not resolve shell syntax such as `$NAME`, `${NAME}`, `$()`, or backticks in this field. The selected shell or script interpreter owns any later resolution or execution. |
| Step `env` values | Value-resolved | Step start | Dagu resolves Dagu references. It does not run command substitution. |
| Executor `with` fields | Value-resolved | Step start | Dagu resolves Dagu references in nested string values. It does not run command substitution. |
| Step object-form `output` string leaves | Value-resolved | Output publication | For string values inside step `output: {...}`, Dagu resolves Dagu references. It does not run dynamic evaluation or shell expansion. |
| `secrets[].key` and `secrets[].options` | Literal | Secret resolution | Provider inputs are literal strings. |

## Command-substitution rules

Dagu command substitution is intentionally narrow.

- The only field in this spec authorized to execute command substitution is `params[].eval`.
- In `params[].eval`, Dagu executes command substitutions written in backtick form or `$()` form.
- Outside `params[].eval`, Dagu leaves backtick text and `$()` text unchanged.
- The presence of `$()` or backticks outside `params[].eval` is not a validation error by itself.

For step `run`, Dagu leaves shell syntax in the resolved run text.
Examples are `$NAME`, `${NAME}`, `$()`, and backticks.
After that, the selected shell or script interpreter owns interpretation of that syntax.
That behavior is not Dagu field evaluation.

The same rule applies to each array-form `run` entry.

## Parameter evaluation

`params[].eval` computes a parameter value only when the caller did not provide that parameter.

Rules:

- If the caller provides a parameter value, Dagu uses that value and does not evaluate `params[].eval`.
- If the caller does not provide a value and `eval` exists, Dagu evaluates `eval`.
- The evaluated value becomes `params.<name>` for the DAG run.
- If `eval` fails and `default` exists, Dagu uses `default` exactly as written.
- If `eval` fails and no `default` exists, the DAG run fails before any step starts.
- This `default` fallback is the only dynamic-evaluation failure fallback in this spec.

Example:

```yaml
params:
  - name: build_date
    type: string
    eval: `date +%Y%m%d`
    default: unknown
steps:
  - name: print
    run: echo ${params.build_date}
```

If the command in `eval` succeeds, `${params.build_date}` uses the command output.
If it fails, `${params.build_date}` is `unknown`.

## Step `run`

Step `run` is value-resolved by Dagu and then started according to the step run specs.

```yaml
params:
  - name: message
    type: string
    default: hello
steps:
  - name: print
    run: echo ${params.message}
```

Dagu resolves `${params.message}` to `hello`.

Dagu leaves unqualified shell variables for the shell:

```yaml
steps:
  - name: print
    run: echo "$HOME ${HOME}"
```

Dagu leaves `echo "$HOME ${HOME}"` in the resolved run text. The selected shell owns expansion of `$HOME` and `${HOME}`.

Dagu does not execute this command substitution:

```yaml
steps:
  - name: print
    run: echo $(date)
```

Dagu leaves `echo $(date)` in the resolved run text. The selected shell owns execution of `$(date)`.

## Step object-form `output`

This section refers to the step-level singular `output` field:

```yaml
steps:
  - id: publish
    output:
      label: "release-${params.version}"
```

The string value `"release-${params.version}"` is a string leaf.
Dagu resolves `${params.version}` when publishing the step output.

Dagu does not run shell syntax in these string leaves:

```yaml
steps:
  - id: publish
    output:
      label: "`date`"
      other: "$(date)"
```

The published values contain the backtick text and `$()` text as ordinary text.

This is different from `stdout.outputs`, `outputs.write`, and the action manifest `outputs` schema.
Those surfaces publish DAG or action outputs and are defined by their owning specs.

## Outputs

Field evaluation does not write workflow events, run logs, result files, or artifacts by itself.

When field evaluation succeeds, Dagu gives the evaluated value to the field that asked for it.

## Errors

Validation warnings:

- A supported Dagu-owned reference that cannot resolve must warn and preserve the original reference text.
- Braced text that does not match a supported Dagu-owned reference form remains ordinary string content under Spec 003.

Runtime errors:

- A preserved value-resolution literal may still fail if the owning typed field cannot accept that literal.
- A dynamic-evaluation failure must fail before the owning field is consumed.
- The exception is `params[].eval` with `default`.
  If `eval` fails and `default` exists, Dagu uses the literal `default` value.

## Acceptance criteria

- A black-box fixture verifies `params[].default` is literal and does not run dynamic evaluation.
- A black-box fixture verifies `dagu run` resolves a parameter value produced by `params[].eval`.
- A black-box fixture verifies an explicit runtime parameter skips that parameter's `eval`.
- A black-box fixture verifies a failed `params[].eval` uses the literal `default` value when `default` exists.
- A black-box fixture verifies a failed `params[].eval` fails before any step starts when no `default` exists.
- A black-box fixture verifies root `env` values resolve Dagu references without running command substitution.
- A black-box fixture verifies `$()` in `params[].eval` is executed by Dagu.
- A black-box fixture verifies command-substitution syntax in step `run`.
  The fixture proves Dagu preserves the syntax before handing the command string to the shell.
- A black-box fixture verifies command-substitution syntax in root `env` is not evaluated by Dagu.
- A black-box fixture verifies step object-form `output` string leaves.
  The fixture proves those leaves resolve Dagu references without running dynamic evaluation.
