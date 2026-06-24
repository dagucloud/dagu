# Spec: Value Resolution and Field Evaluation

## Status

Implemented.

## Scope

Dagu field evaluation defines what Dagu does to workflow YAML text before the owning field uses it.

It can:

- use the text exactly as written
- resolve Dagu-owned references
- run dynamic evaluation
- leave shell syntax for a later runtime such as `/bin/sh`

Value resolution is the field evaluation mode that resolves Dagu-owned references in workflow YAML before the field is used.

A Dagu-owned reference is a namespaced reference defined by this spec or by
another numbered spec:

```text
${consts.name}
${params.name}
${env.NAME}
${steps.step_id.outputs.name}
${foreach.item}
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
- Parallel and foreach item scopes: [Spec 018: Parallel Fan-Out and Foreach Iteration](018-parallel-and-foreach.md)

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

Unqualified environment expansion is a separate field-level ownership decision.
A value-resolved field always resolves Dagu-owned references defined by this
spec set.
That same field expands `$NAME`, `${NAME}`, or shell-style `${NAME...}` expressions only when Spec 006 says Dagu owns unqualified environment expansion for that field.
When Spec 006 says a later runtime owns unqualified environment syntax, Dagu preserves that syntax after resolving Dagu-owned references.

### Reference Syntax

- Dagu-owned references are only the unescaped supported reference forms
  defined by this spec set.

- An unescaped supported reference form must use `${path}` syntax.

- Base supported reference forms are:

```text
${consts.name}
${params.name}
${env.NAME}
${steps.step_id.outputs.name}
```

- A later numbered spec may add a supported reference namespace. That spec must
  define the namespace path grammar, value source, availability rules,
  validation behavior, missing-value behavior, and examples.
- New Dagu-managed runtime metadata must use a reserved namespace listed in the
  namespace registry below, or the owning spec must update that registry.

- Names in supported reference forms must match `^[A-Za-z][A-Za-z0-9_]*$`.
- This name rule applies to namespace names, `consts` keys, `params` names, step ids, `outputs`, and step output names.

- Environment variable names under `env` must match `^[A-Za-z_][A-Za-z0-9_]*$`.

- Unqualified `${NAME}` is handled by environment expansion in fields that support it.

- Braced text that does not match a supported reference form is not interpreted by Dagu.
- Dagu preserves unsupported braced text as ordinary string content.
- Unsupported braced text does not produce a passive notice.

- Escaped supported-looking text is not a Dagu-owned reference.
- Escape behavior is defined by "Escaped Dagu References" below.

- Namespace-specific specs define validation for supported reference forms.

- Unbraced namespace-looking text such as `$name.path` is not Dagu-owned reference syntax.

- `$consts.name`, `$params.name`, `$env.NAME`, and `$steps.step_id.outputs.name` are ordinary string content.

- Unsupported reference-looking text is preserved silently.

### Namespace Registry

The following top-level namespaces are Dagu-owned value-resolution namespaces.
An owning spec may reserve descendants so that unknown fields preserve at
runtime but produce inspection-only passive notices.

| Namespace | Owner | Path grammar | Missing behavior | Descendants reserved |
| --- | --- | --- | --- | --- |
| `consts` | Spec 004 | `consts.<name>` | Preserve with passive notice. | No |
| `params` | Spec 005 | `params.<name>` | Preserve with passive notice. | No |
| `env` | Spec 006 | `env.<NAME>` | Preserve with passive notice. | No |
| `steps` | Spec 007 | `steps.<step_id>.outputs.<name>` | Preserve with reason-specific passive notice. | No |
| `foreach` | Spec 018 | `foreach.index`, `foreach.key`, `foreach.<as>`, or `foreach.<as>.<field>` | Preserve with passive notice when item scope is unavailable or the item field is missing. | Yes |
| `context` | Spec 017 | `context.<namespace>.<field>` | Preserve with passive notice. | Yes |

Spec 017 also defines frozen top-level compatibility aliases `dag`, `run`,
`attempt`, `step`, `trigger`, `paths`, `profile`, and `pushback`. Only the
exact aliases listed by Spec 017 are Dagu-owned references. Other text under
those short roots stays unsupported ordinary string content, and new fields
must not be added to those alias roots.

### Escaped Dagu References

- A run of backslashes immediately before `$` controls whether that dollar token is escaped for Dagu field evaluation.

- If the run length is odd, the last backslash in the run is the Dagu escape marker.
- Dagu removes that escape marker before the owning field consumes the value.
- The dollar token is protected from Dagu field evaluation.

- If the run length is even, the dollar token is not escaped for Dagu field evaluation.
- Dagu removes no backslash from an even-length run.

- The escape protects the dollar token through Dagu-owned supported-reference detection, namespace validation, passive-notice collection, and command-substitution handling.

- Unqualified environment expansion has field-specific ownership rules in Spec 006.
- For unqualified `$NAME` and `${NAME}` syntax, this section does not by itself require every field to treat a preceding backslash as an environment-expansion escape marker.

- Escaped Dagu-looking text must not be resolved.

- Escaped Dagu-looking text must not produce a passive notice.

Examples:

```text
\${params.name}
\${steps.build.outputs.image}
```

The owning field receives these literal dollar forms:

```text
${params.name}
${steps.build.outputs.image}
```

Fields whose Spec 006 ownership rule treats backslash as an unqualified environment escape may also use that field-specific escape for `$NAME` and `${NAME}`.

YAML parsing happens before Dagu value resolution.
In a YAML double-quoted scalar, an author writes `\\${params.name}` for Dagu to receive `\${params.name}`.
In plain scalars, single-quoted scalars, and block scalars, `\${params.name}` reaches Dagu as written.

Assuming `${params.name}` resolves to `prod` in a value-resolved field:

| Text received by Dagu | Owning field receives |
| --- | --- |
| `\${params.name}` | `${params.name}` |
| `\\${params.name}` | `\\prod` |
| `\\\${params.name}` | `\\${params.name}` |

Unsupported braced text such as `${step.xxx.foo}` is already ordinary string content.
Escaping unsupported braced text is allowed but not required to avoid Dagu interpretation.

For shell-backed fields, Dagu escaping does not quote or escape for the shell.
After Dagu removes its escape marker, the selected shell or script interpreter may still interpret dollar syntax.
To pass a literal dollar form through a shell-backed field, the authored text must also protect it according to that shell or script language.

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
| `steps[].with` | Value-resolved | Step start | Nested string values under the step `with` object resolve Dagu-owned references unless a more specific row or owning action or executor spec defines another evaluation mode. This includes action inputs and run-step shell settings. |
| `steps[].working_dir` | Value-resolved | Step start | Step working directory resolves Dagu-owned references. |
| `steps[].env` | Value-resolved | Step start | Step environment values in map form, array-of-map form, or `KEY=value` list form resolve Dagu-owned references. |
| `steps[].preconditions[].condition` | Value-resolved | Before checking the step precondition | Step precondition condition strings resolve Dagu-owned references. |
| `steps[].retry_policy.limit` and `steps[].retry_policy.interval_sec` string forms | Value-resolved | Before the retry policy uses the value | Step retry policy string numeric fields resolve Dagu-owned references. Other retry policy fields remain literal unless an owning spec opts in. |
| `steps[].repeat_policy.condition` | Value-resolved | Before checking the repeat policy | Repeat condition strings resolve Dagu-owned references. |
| `steps[].repeat_policy.limit`, `steps[].repeat_policy.interval_sec`, and `steps[].repeat_policy.max_interval_sec` string forms | Value-resolved | Before the repeat policy uses the value | Step repeat policy string numeric fields resolve Dagu-owned references. Other repeat policy fields remain literal unless an owning spec opts in. |
| Sub-DAG invocation target and params | Value-resolved | Before the sub-DAG run or enqueue request is created | Canonical `dag.run` and `dag.enqueue` `with.dag` resolves Dagu-owned references. Scalar string `with.params` resolves as one value. Nested string leaves under object-form or array-form `with.params` resolve individually. Accepted legacy `call` and legacy `params` follow the same target and params rules. |
| `steps[].parallel` | Value-resolved | Before expanding the parallel step | `variable`, `items[]`, `items[].value`, and `items[].params.*` string values resolve Dagu-owned references. |
| `steps[].foreach` | Value-resolved | Before and during the foreach step | `items`, `key`, value-resolved string fields inside `foreach.steps`, and `collect` values resolve Dagu-owned references. Spec 018 defines the scoped `foreach` namespace available to those fields. |
| `steps[].stdout`, `steps[].stdout.artifact` | Value-resolved | Step start or stdout setup | Stdout file path strings and artifact path strings resolve Dagu-owned references. For unqualified environment syntax in these path strings, backslashes remain path text unless Spec 006 assigns them escape behavior. |
| `steps[].stderr`, `steps[].stderr.artifact` | Value-resolved | Step start or stderr setup | Stderr file path strings and artifact path strings resolve Dagu-owned references. For unqualified environment syntax in these path strings, backslashes remain path text unless Spec 006 assigns them escape behavior. |
| `steps[].stdout.outputs.fields.*` | Value-resolved | Output publication | Literal string values under field entries resolve Dagu-owned references. Selection and decode metadata remain literal unless an owning spec opts in. |
| `steps[].output.*` | Value-resolved | Output publication | Literal string values and `path` strings under structured step `output` entries resolve Dagu-owned references. |
| `steps[].container` | Value-resolved | Step start | Step container string form resolves Dagu-owned references. In object form, `exec`, `image`, `name`, `user`, `working_dir`, `network`, `volumes[]`, `ports[]`, `env` values, `command[]`, and `shell[]` resolve Dagu-owned references. |
| `steps[].messages[].content` | Value-resolved | Step start | Message content strings resolve Dagu-owned references. |
| LLM prompt and endpoint text fields | Value-resolved | Step start | For LLM-capable steps, `steps[].llm.system`, `steps[].llm.base_url`, and array-form `steps[].llm.model[].base_url` resolve Dagu-owned references. When a step inherits root LLM settings, inherited `llm.system`, `llm.base_url`, and array-form `llm.model[].base_url` follow the same rule. |
| `secrets[]` | Literal | Secret resolution | Secret names, provider names, provider keys, and provider options are literal strings. |

Explicitly literal or excluded field surfaces:

| YAML field or surface | Rule |
| --- | --- |
| Root identity and metadata, such as `name` and `description` | Dagu does not resolve value references in these fields. |
| Root run-control fields, such as `retry_policy`, `timeout_sec`, `delay_sec`, `max_active_steps`, `max_clean_up_time_sec`, and `max_output_size` | Dagu does not resolve value references in these fields unless an owning spec later opts in. |
| Step identity, graph, and action-selection fields, such as `steps[].id`, `steps[].name`, `steps[].description`, `steps[].depends`, and `steps[].action` | Dagu does not resolve value references in these fields. A value reference cannot select a step, dependency, or action. |
| `steps[].foreach.as`, `steps[].foreach.max_concurrent`, and body step identity or dependency fields | Dagu does not resolve value references in these fields. A value reference cannot select an item alias, concurrency limit, body step, or body dependency. |
| Step output declaration contracts, such as `steps[].outputs[].name` and `steps[].outputs[].type` | Dagu does not resolve value references in declaration metadata. Published output values are separate runtime data. |
| Step control fields not listed in the value-resolution matrix, such as `steps[].timeout_sec`, `steps[].continue_on`, `steps[].worker_selector`, `steps[].mail_on_error`, and `steps[].signal_on_stop` | Dagu does not resolve value references in these fields unless an owning spec later opts in. |
| LLM selection and credential fields, such as provider names, model names, API key names, and tool-name lists | This spec does not opt these fields into value resolution. They remain literal unless an LLM-owning spec explicitly opts in. |
| Template body fields owned by a template action or executor | Dagu does not resolve value references in template body text unless the template-owning spec explicitly opts in. Template data fields that are otherwise covered by `steps[].with` keep the `steps[].with` behavior. |
| Root executor, provider, and tool configuration fields not listed in the value-resolution matrix, such as `ssh`, `kubernetes`, and `tools` | This spec does not opt these fields into value resolution. They remain literal unless an owning executor, provider, or tool spec explicitly opts in. |

- The `steps[]` rows also apply to handler steps.
- Handler step surfaces are `handler_on.init`, `handler_on.success`, `handler_on.failure`, `handler_on.abort`, `handler_on.exit`, and `handler_on.wait`.

- For value-resolved fields, Dagu resolves Dagu-owned references.
- Dagu does not run dynamic evaluation or command substitution in value-resolved fields.

- For `steps[].run`, unqualified `$NAME` and `${NAME}` are shell syntax.
- Dagu preserves that shell syntax for the selected shell.
- Dagu-owned environment references in `run` must use `${env.NAME}`.

- Defaults, custom `step_types`, and custom `actions` are checked after Dagu expands them into concrete steps.

- For parallel sub-DAG invocations, Dagu resolves parallel item values before resolving the child DAG target and params for that item.

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

- Parameter declarations are processed in source order.
- If the caller provides a parameter value, Dagu uses that value and does not evaluate `params[].eval`.
- If the caller does not provide a value and `eval` exists, Dagu evaluates `eval`.
- The evaluated value becomes `params.<name>` for the DAG run.
- If `eval` fails and `default` exists, Dagu uses `default` exactly as written.
- If `eval` fails and no `default` exists, the DAG run fails before any step starts.
- This `default` fallback is the only dynamic-evaluation failure fallback in this spec.
- Caller-provided parameter values are available to later `params[].eval` expressions.
- A parameter value selected from an earlier declaration is available to later `params[].eval` expressions.
- A `params[].eval` expression cannot resolve its own computed value.
- A `params[].eval` expression cannot resolve a later computed or default value that has not been selected yet.
- Dagu does not topologically sort parameter declarations and does not retry preserved parameter references after later declarations are processed.
- If parameter declarations form a reference cycle, at least one reference in that cycle cannot resolve under the source-order rule and follows the unresolved-reference rules.

### Unresolved Supported References

A supported reference can be valid syntax but have no value when Dagu evaluates the field.
That condition is a passive notice for explicit inspection surfaces.
It is not a validation or execution error by itself.
Dagu must keep the original reference text in the field value.
This rule applies only to unescaped supported reference forms.
Escaped supported-looking text and unsupported braced text must not produce passive notices.

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

- Dagu performs one value-resolution pass for the field being evaluated.
- Inserted values are data.
- Dagu must not recursively scan an inserted value for more Dagu-owned references or dynamic evaluation syntax during the same field evaluation.

- When Dagu inserts a referenced value into a string field, strings are inserted as written.

- When Dagu inserts a referenced value into a string field, booleans are inserted as `true` or `false`.

- When Dagu inserts a referenced value into a string field, integers are inserted in base-10 decimal form.

- When Dagu inserts a referenced value into a string field, non-integer numbers use base-10 decimal text.
- Non-integer decimal text must use the shortest round-trippable representation.

- If a namespace can expose a non-scalar value, that namespace's owning spec must define the string insertion form for that value before it can be inserted by this spec.

- Dagu does not shell-escape, JSON-escape, URL-escape, or otherwise quote inserted values.
- The owning field and any later runtime interpret the resulting text.

- In shell-backed fields, inserted whitespace, quotes, command separators, command substitutions, redirects, glob characters, and other shell-significant text remain part of the shell input.
- Workflow authors must use the quoting, escaping, environment passing, files, or direct-argument execution surface appropriate for the later runtime when inserted data must be treated as data.

### Evaluation Outputs

Field evaluation does not write workflow events, run logs, result files, or artifacts by itself.

When field evaluation succeeds, Dagu gives the evaluated value to the field that asked for it.

## Errors

- A supported Dagu-owned reference that cannot resolve must preserve the original reference text.
- Explicit inspection surfaces must report a passive notice for that preserved reference.
- Braced text that does not match a supported Dagu-owned reference form remains ordinary string content.
- Escaped supported-looking text remains ordinary string content and must not produce a passive notice.

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

Escaped Dagu-looking text is ordinary content:

```yaml
steps:
  - name: script
    run: |
      cat > script.php <<'PHP'
      <?php
      echo '\${steps.build.outputs.image}', PHP_EOL;
      PHP
      php script.php
```

Dagu passes `${steps.build.outputs.image}` to the later PHP interpreter as code text.
The PHP single-quoted string then treats it as literal text.

Step output references require an authored dependency and wait for the producing step:

```yaml
steps:
  - id: build
    run: |
      printf 'image=v1.2.3\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: image

  - id: deploy
    depends: build
    env:
      IMAGE: ${steps.build.outputs.image}
    run: ./deploy.sh "$IMAGE"
```

Dagu does not create the `depends: build` relationship from the value reference.
Without that dependency, the reference is preserved and inspection surfaces report a passive notice.

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
