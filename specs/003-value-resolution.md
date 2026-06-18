# Spec: Value Resolution and Field Evaluation

## Status

Partially implemented.

This spec includes target conformance behavior.
Current product behavior covers only rows whose owning namespace or field spec is implemented.
Rows owned by a spec marked `Not implemented` in the spec index are target behavior until that owning spec is implemented.

## Scope

Dagu field evaluation defines what Dagu does to workflow YAML text before the owning field uses it.

It can:

- use the text exactly as written
- resolve Dagu-owned references
- run dynamic evaluation
- leave shell syntax for a later runtime such as `/bin/sh`

Value resolution is the field evaluation mode that resolves Dagu-owned references in workflow YAML before the field is used.

A Dagu-owned reference is a namespaced reference:

```text
${consts.name}
${params.name}
${env.NAME}
${steps.step_id.outputs.name}
```

This spec defines field evaluation modes, where Dagu-owned references are allowed, how their text is parsed, and when values are resolved.

Other specs define how each namespace gets its value.
Dynamic evaluation mechanics are defined by [Spec 011: Dynamic Evaluation](011-dynamic-evaluation.md).
Field shape belongs to the YAML schema spec or to the spec that owns that field.

## Goal

A workflow author can tell, for each field covered here, whether Dagu will:

- use the field exactly as written
- resolve Dagu value references
- run `params[].eval`
- leave shell syntax for a later runtime such as `/bin/sh`

## Motivation

Workflows need clear ownership boundaries between:

- Dagu-owned references, such as `${consts.name}` and `${params.name}`.
- Environment variables, such as `$NAME` and `${NAME}`.
- Shell syntax, such as `$()`, backticks, pipes, redirects, and command chaining.

Dagu must be explicit about which syntax it owns and which syntax remains owned by a later runtime.

## Related Specs

- `consts`: [Spec 004: Value Resolution Consts](004-value-resolution-consts.md)
- `params`: [Spec 005: Value Resolution Params](005-value-resolution-params.md)
- `env`: [Spec 006: Value Resolution Env](006-value-resolution-env.md)
- `steps`: [Spec 007: Value Resolution Steps](007-value-resolution-steps.md)
- Step identity: [Spec 009: Step Reference](009-step-reference.md)
- Dynamic evaluation: [Spec 011: Dynamic Evaluation](011-dynamic-evaluation.md)
- Step output publication: [Spec 012: Step Outputs](012-step-outputs.md)

## Behavior

### Field Names

These names are easy to confuse, so this spec uses them exactly:

- Step `output` is the singular step field. Its accepted forms are defined by its owning spec.
- Step `stdout.outputs` publishes DAG or action outputs from stdout.
- Step top-level `outputs` is a separate field owned by the step outputs spec.
- `dagu-action.yaml` top-level `outputs` is an action manifest schema. It is not a normal workflow root field.
- A normal workflow root does not have a root `output` or root `outputs` field in this spec.

### Evaluation Types

Dagu uses three evaluation types.

| Type | Meaning |
| --- | --- |
| Literal | Dagu uses the value exactly as written. It does not resolve `${...}` and does not run dynamic evaluation. |
| Value-resolved | Dagu resolves Dagu-owned references such as `${params.name}`. It does not run dynamic evaluation. |
| Dynamic-evaluated | Dagu runs the dynamic evaluation pipeline. In this spec, only `params[].eval` uses this type. |

### Reference Syntax

- Dagu-owned references are only the supported reference forms listed below.

- A supported reference form must use `${path}` syntax.

- Supported reference forms are:

```text
${consts.name}
${params.name}
${env.NAME}
${steps.step_id.outputs.name}
```

- Names in supported reference forms must match `^[A-Za-z][A-Za-z0-9_]*$`.
- This name rule applies to namespace names, `consts` keys, `params` names, step ids, `outputs`, and step output names.

- Environment variable names under `env` must match `^[A-Za-z_][A-Za-z0-9_]*$`.

- Unqualified `${NAME}` is handled by environment expansion in fields that support it.

- Braced text that does not match a supported reference form is not interpreted by Dagu.
- Dagu preserves unsupported braced text as ordinary string content.

- Namespace-specific specs define validation for supported reference forms.

- Unbraced namespace-looking text such as `$name.path` is not Dagu-owned reference syntax.

- `$consts.name`, `$params.name`, `$env.NAME`, and `$steps.step_id.outputs.name` are ordinary string content.

- Unsupported reference-looking text is preserved silently.

### Single-Quoted Environment References

- Single-quoted `$NAME` and `${NAME}` are preserved during Dagu environment expansion.

### Field Evaluation Matrix

Dagu-owned references are supported only in value-resolved fields and dynamic-evaluated fields listed here.

| YAML field | Evaluation type | When it happens | Rule |
| --- | --- | --- | --- |
| `consts` list form | Value-resolved | Workflow load | String values in ordered list entries resolve only earlier `${consts.*}` entries. |
| `params[].default` | Literal | Run start, when needed | Dagu uses the default exactly as written. Declaration metadata is also literal. |
| Runtime parameter overrides | Literal | Caller input | Values from CLI, API, or sub-DAG calls are not evaluated. |
| `params[].eval` | Dynamic-evaluated | Before any step starts | Used only when the caller did not provide that parameter. Dagu resolves Dagu-owned references, then runs dynamic evaluation as defined by Spec 011. |
| `env` | Value-resolved | Run setup before step execution | Root environment values in map form, array-of-map form, or `KEY=value` list form resolve Dagu-owned references. |
| `dotenv[]` | Value-resolved | Before dotenv files are loaded | Each dotenv path string resolves Dagu-owned references. |
| `shell`, `shell_args[]`, `working_dir` | Value-resolved | Before the root field is used | Root shell command, shell args, and working directory resolve Dagu-owned references. |
| `preconditions[].condition` | Value-resolved | Before checking the precondition | Root precondition condition strings resolve Dagu-owned references. |
| `container` | Value-resolved | Before root container settings are used | Root container string form resolves Dagu-owned references. In object form, `exec`, `image`, `name`, `user`, `working_dir`, `network`, `volumes[]`, `ports[]`, `env` values, `command[]`, and `shell[]` resolve Dagu-owned references. |
| `steps[].run` | Value-resolved | Step start | The string `run` value and each array-form `run` entry resolve Dagu-owned references. Dagu leaves shell syntax for the selected shell or script interpreter. |
| `steps[].with` | Value-resolved | Step start | Every nested string value under the step `with` object resolves Dagu-owned references. This includes action inputs and run-step shell settings. |
| `steps[].working_dir` | Value-resolved | Step start | Step working directory resolves Dagu-owned references. |
| `steps[].env` | Value-resolved | Step start | Step environment values in map form, array-of-map form, or `KEY=value` list form resolve Dagu-owned references. |
| `steps[].preconditions[].condition` | Value-resolved | Before checking the step precondition | Step precondition condition strings resolve Dagu-owned references. |
| `steps[].repeat_policy.condition` | Value-resolved | Before checking the repeat policy | Repeat condition strings resolve Dagu-owned references. |
| `steps[].parallel` | Value-resolved | Before expanding the parallel step | `variable`, `items[]`, `items[].value`, and `items[].params.*` string values resolve Dagu-owned references. |
| `steps[].stdout`, `steps[].stdout.artifact` | Value-resolved | Step start or stdout setup | Stdout file path strings and artifact path strings resolve Dagu-owned references. |
| `steps[].stderr`, `steps[].stderr.artifact` | Value-resolved | Step start or stderr setup | Stderr file path strings and artifact path strings resolve Dagu-owned references. |
| `steps[].stdout.outputs.fields.*` | Value-resolved | Output publication | Literal string values under field entries resolve Dagu-owned references. |
| `steps[].output.*` | Value-resolved | Output publication | Literal string values and `path` strings under structured step `output` entries resolve Dagu-owned references. |
| `steps[].container` | Value-resolved | Step start | Step container string form resolves Dagu-owned references. In object form, `exec`, `image`, `name`, `user`, `working_dir`, `network`, `volumes[]`, `ports[]`, `env` values, `command[]`, and `shell[]` resolve Dagu-owned references. |
| `secrets[].key` and `secrets[].options` | Literal | Secret resolution | Provider inputs are literal strings. |

- The `steps[]` rows also apply to handler steps.
- Handler step surfaces are `handler_on.init`, `handler_on.success`, `handler_on.failure`, `handler_on.abort`, `handler_on.exit`, and `handler_on.wait`.

- For value-resolved fields, Dagu resolves Dagu-owned references.
- Dagu does not run dynamic evaluation or command substitution in value-resolved fields.

- For `steps[].run`, unqualified `$NAME` and `${NAME}` are shell syntax.
- Dagu preserves that shell syntax for the selected shell.
- Dagu-owned environment references in `run` must use `${env.NAME}`.

- Defaults, custom `step_types`, and custom `actions` are checked after Dagu expands them into concrete steps.

- All other canonical fields are outside this spec unless the field evaluation matrix is updated or an owning spec explicitly opts in.

- The validator and runtime must use the same field list.

- Adding a value-resolution-capable field requires coordinated updates.
- The coordinated update must cover this spec, the DAG JSON schema, validation traversal, runtime traversal, and black-box tests.

### Command Substitution

Dagu command substitution is intentionally narrow.

- The only field in this spec authorized to execute command substitution is `params[].eval`.
- In `params[].eval`, Dagu executes command substitutions written in backtick form or `$()` form as defined by Spec 011.
- Outside `params[].eval`, Dagu leaves backtick text and `$()` text unchanged.
- The presence of `$()` or backticks outside `params[].eval` is not a validation error by itself.

For `steps[].run`, Dagu leaves shell syntax in the resolved run text.
Examples are `$NAME`, `${NAME}`, `$()`, and backticks.
After that, the selected shell or script interpreter owns interpretation of that syntax.
That behavior is shell or script interpretation, not Dagu field evaluation.

The same rule applies to each array-form `run` entry.

### Parameter Evaluation

`params[].eval` computes a parameter value only when the caller did not provide that parameter.

Rules:

- If the caller provides a parameter value, Dagu uses that value and does not evaluate `params[].eval`.
- If the caller does not provide a value and `eval` exists, Dagu evaluates `eval`.
- The evaluated value becomes `params.<name>` for the DAG run.
- If `eval` fails and `default` exists, Dagu uses `default` exactly as written.
- If `eval` fails and no `default` exists, the DAG run fails before any step starts.
- This `default` fallback is the only dynamic-evaluation failure fallback in this spec.

### Unresolved Supported References

A supported reference can be valid syntax but have no value when Dagu evaluates the field.
That condition is a passive notice for explicit inspection surfaces.
It is not a validation or execution error by itself.
Dagu must keep the original reference text in the field value.

The notice must identify the owning field and the original reference text.
The notice must not be shown as a normal validation warning.
Current inspection surfaces are `dagu validate`, the DAG spec inspection API response, and the Web UI spec editor.
Normal run execution must stay silent.
Dagu must not write these notices to run logs, workflow events, status files, history files, artifacts, or DAG-run detail responses.
Other specs that mention passive notices follow this inspection-only behavior.

This rule applies to these misses:

- Unknown const.
- Missing param value.
- Unavailable env value.
- Missing step output.
- Namespace unavailable in the current phase.
- Step-output reference that cannot resolve because of ordering or ownership.

If a typed field later consumes the preserved text, that field may still fail because the literal text is not valid for that field type.

### Resolution Timing

- Dagu must not pre-render the whole workflow file.

- Dagu resolves each supported field when that field is about to be used.

- Root `consts` resolve while loading the workflow.

- Runtime `params` are available after Dagu builds the run input.

- `dotenv[]` paths resolve before dotenv files are loaded.

- Root fields resolve before Dagu uses those fields.

- Root `env` resolves before step execution begins.

- Step precondition fields resolve before checking the precondition.

- Step executor fields resolve before starting the executor.

- Step output fields resolve while collecting outputs.

- Step output references resolve only after the referenced step publishes the output.

- For step-owned fields, unresolved supported references must remain literal before the owning step starts.

### String Insertion

- When Dagu inserts a referenced value into a string field, strings are inserted as written.

- When Dagu inserts a referenced value into a string field, booleans are inserted as `true` or `false`.

- When Dagu inserts a referenced value into a string field, integers are inserted in base-10 decimal form.

- When Dagu inserts a referenced value into a string field, non-integer numbers use base-10 decimal text.
- Non-integer decimal text must use the shortest round-trippable representation.

### Evaluation Outputs

Field evaluation does not write workflow events, run logs, result files, or artifacts by itself.

When field evaluation succeeds, Dagu gives the evaluated value to the field that asked for it.

## Errors

- A supported Dagu-owned reference that cannot resolve must preserve the original reference text.
- Explicit inspection surfaces must report a passive notice for that preserved reference.
- Braced text that does not match a supported Dagu-owned reference form remains ordinary string content.

- A preserved value-resolution literal may still fail if the owning typed field cannot accept that literal.
- A dynamic-evaluation failure must fail before the owning field is consumed.
- The exception is `params[].eval` with `default`.
- If `eval` fails and `default` exists, Dagu uses the literal `default` value.

## Examples

Valid references from multiple namespaces:

```yaml
consts:
  - deploy_script: ./scripts/deploy.sh
  - service: api
params:
  - name: environment
    type: string
    required: true
steps:
  - name: deploy
    run: ${consts.deploy_script} ${params.environment} ${consts.service}
```

Unbraced namespace-looking text is ordinary content:

```yaml
steps:
  - name: script
    run: |
      php -r '$params.name = "literal"; echo $params.name;'
```

Step output references wait for the producing step:

```yaml
steps:
  - id: build
    run: |
      printf 'image=v1.2.3\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: image

  - id: deploy
    depends: build
    run: ./deploy.sh ${steps.build.outputs.image}
```

Parameter value from dynamic evaluation:

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

Shell syntax preserved in `run`:

```yaml
steps:
  - name: print
    run: echo "$HOME ${HOME} $(date)"
```

Dagu leaves `$HOME`, `${HOME}`, and `$(date)` in the resolved run text.
The selected shell owns later expansion or command substitution.

Step object-form `output` value resolution:

```yaml
steps:
  - id: publish
    output:
      label: "release-${params.version}"
```

The string value `"release-${params.version}"` is a string leaf.
Dagu resolves `${params.version}` when publishing the step output.
