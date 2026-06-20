# Spec: Value Resolution Env

## Status

Implemented.

## Scope

This spec defines environment values used by Dagu value resolution.

It covers:

- `${env.NAME}` references
- `$NAME` and `${NAME}` environment expansion
- ordering and validation rules for `env` declarations
- source precedence for environment lookups
- shell and direct-execution boundaries for unqualified environment syntax

Common value-resolution syntax, supported fields, string insertion, and timing are defined by [Spec 003: Value Resolution and Field Evaluation](003-value-resolution.md).

This spec does not define:

- which workflow fields opt into value resolution
- dotenv file syntax
- runtime profile selection
- secret provider configuration or secret masking
- shell language behavior after Dagu hands text to a shell, container, or remote host

## Goal

Workflow authors can define environment values once and use them in every field that supports environment expansion.

## Motivation

Environment values can come from YAML, dotenv files, runtime params, step outputs, container settings, step settings, and the process environment.

Dagu must preserve environment syntax when another runtime, such as a shell or executor, owns that syntax.

Dagu must also define which value wins when the same name appears in more than one source.
Otherwise a workflow can pass validation and still behave differently under the CLI, Web UI, scheduler, worker, container, or SSH executor.

## Behavior

### Environment Scope

An environment scope is the set of named string values visible when Dagu evaluates a field.

Rules:

- Dagu resolves an environment reference against the current environment scope for the field being evaluated.

- If the same environment name is defined by multiple visible sources, the later and more specific source wins.

- For a normal DAG run, the root run environment scope source precedence is,
  from lowest to highest:

  1. process environment entries explicitly inherited into the run by the Dagu
     runtime
  2. inherited runtime-profile defaults
  3. runtime params exposed as environment values
  4. Dagu-managed run environment values
  5. root `env` declarations
  6. dotenv values loaded into the DAG environment
  7. selected runtime-profile environment values
  8. protected Dagu-managed run environment values
  9. secret values exposed as environment values

- Step-owned fields extend the root run environment scope with the step-local
  sources that exist when the field is evaluated.
- For a normal step after the execution-attempt environment is ready, the
  step-local source precedence is, from lowest to highest:

  1. initial current-step Dagu-managed values available before `steps[].env`
     is evaluated
  2. output variables from completed predecessor steps that are available to
     the step before it starts
  3. the step's own `env` declarations
  4. environment declarations from the selected container for the step
  5. execution-attempt Dagu-managed values created for the current attempt

- Root `env` entries are evaluated top to bottom.
- If root `env` defines the same name as a protected Dagu-managed run
  environment value, later root `env` entries in the same declaration may read
  the workflow-authored value during root `env` evaluation.
- Protected Dagu-managed run environment values cannot be overridden in the
  final root run environment scope by root `env`, dotenv values,
  runtime-profile values, or runtime environment overrides.
- Step-local sources are intentionally outside that root-scope protection.
- If a predecessor output, step `env`, or selected `container.env` uses the
  same name as an initial current-step Dagu-managed value, that step-local
  value shadows the initial value for later fields and process environment
  belonging to that step.
- Execution-attempt Dagu-managed values, once available, override predecessor
  output variables, step `env`, and selected `container.env` for fields and
  process environment belonging to that attempt.
- Workflow authors should avoid reusing Dagu-managed names in step-local
  sources unless the step intentionally needs to shadow that name.
- A field evaluated before an execution-attempt Dagu-managed value exists
  cannot read that value. Missing-reference preservation rules apply.

- Predecessor output variables are defined below.
- Step outputs referenced as `${steps.step_id.outputs.name}` are owned by Spec
  007 and published through the output-owning specs.
- Step outputs are not automatically available as `${env.NAME}` unless the
  producing step also publishes an output variable with that environment name.

- When multiple predecessor steps publish the same output-variable name into a
  step's environment scope, the predecessor visited later in dependency order
  wins.
- Dependency order is breadth-first upstream order:
  1. Start with the consuming step's authored `depends` list in left-to-right
     order.
  2. Visit each listed predecessor at most once.
  3. After visiting a predecessor, append that predecessor's authored
     `depends` list to the end of the order in left-to-right order.
  4. Ignore a predecessor if it appears again after it has already been
     visited.
- A predecessor's output variables are applied when that predecessor is first
  visited.

- The selected container is the step container when the step defines one, or
  the root container when the step uses the root container.
- Container environment declarations are not a separate global source.
- If another spec makes both root-level container settings and step-level
  container settings applicable to a step, only the environment declarations
  on the selected container participate in this spec's precedence order.

- Runtime-profile specs own profile selection and profile inheritance.
- Secret specs own secret provider lookup and masking.
- This spec only defines how values behave after they are present in the environment scope.
- Secret declarations must not use names that start with `DAGU_` or names that
  collide with secret-reserved Dagu-managed environment names. After
  secret-owning validation accepts a secret name, the exposed secret value uses
  the secret precedence entry above.

- Explicitly inherited process environment entries are part of the root run
  environment scope after Dagu imports them.
- Host process environment fallback is different: it means reading a name
  directly from the Dagu process environment while evaluating a field.
- A field whose ownership row says host process environment fallback is `Not
  allowed` must not read host-only process environment values.
- Such a field can still read a value that originated from the process
  environment only if Dagu already imported that value into the run environment
  scope through an explicit source such as inherited base environment, root
  `env`, dotenv, runtime profile, or secret exposure.

- Environment names are matched exactly during Dagu value resolution.
- When Dagu builds a process environment on a platform where environment names are case-insensitive, duplicate names that differ only by case collapse according to the platform rule, and the higher-precedence Dagu source wins.

Protected Dagu-managed root run environment names:

- `DAG_NAME`
- `DAG_RUN_ID`
- `DAG_RUN_LOG_FILE`
- `DAG_DOCS_DIR`
- `DAG_RUN_WORK_DIR`
- `DAG_RUN_ARTIFACTS_DIR`
- `DAG_PARAMS_JSON`
- `DAGU_PARAMS_JSON`

Initial current-step Dagu-managed environment names include:

- `PWD`
- `DAG_RUN_STEP_NAME`

`PWD` is the resolved working directory for the current step.
`DAG_RUN_STEP_NAME` is the current step's effective runtime name. When a step
omits `name` and uses `id`, the effective runtime name is that `id`.

`DAG_RUN_STATUS`, when present for a handler or status-aware surface, is an
initial current-step Dagu-managed value for that surface.

Execution-attempt Dagu-managed environment names include:

- `DAG_RUN_STEP_STDOUT_FILE`
- `DAG_RUN_STEP_STDERR_FILE`
- `DAGU_OUTPUT_FILE`

Additional Dagu-managed names reserved for specialized run-control surfaces
include:

- `DAG_WAITING_STEPS`
- `DAG_PUSHBACK`
- `DAG_PUSHBACK_ITERATION`
- `DAG_PUSHBACK_PREVIOUS_STDOUT_FILE`
- `DAGU_EXTERNAL_STEP_RETRY`
- `DAGU_QUEUE_DISPATCH_RETRY`

These names are not part of the normal step environment scope unless their
owning surface makes them available.

Secret-reserved Dagu-managed environment names include every name starting with
`DAGU_` and every Dagu-managed projection name below:

- `DAG_NAME`
- `DAG_RUN_ID`
- `DAG_RUN_LOG_FILE`
- `DAG_RUN_STEP_NAME`
- `DAG_RUN_STEP_STDOUT_FILE`
- `DAG_RUN_STEP_STDERR_FILE`
- `DAG_RUN_STATUS`
- `DAG_WAITING_STEPS`
- `DAGU_PARAMS_JSON`
- `DAG_DOCS_DIR`
- `DAG_PARAMS_JSON`
- `DAG_RUN_WORK_DIR`
- `DAG_RUN_ARTIFACTS_DIR`
- `DAG_PUSHBACK`
- `DAG_PUSHBACK_ITERATION`
- `DAG_PUSHBACK_PREVIOUS_STDOUT_FILE`
- `DAGU_EXTERNAL_STEP_RETRY`
- `DAGU_QUEUE_DISPATCH_RETRY`

Other specs that introduce Dagu-managed environment names must state whether
those names are protected root run names, initial current-step names, or
execution-attempt names. They must also state whether the names are
secret-reserved. Future Dagu-managed projection names should prefer the
`DAGU_` prefix unless an owning spec explicitly extends an existing
compatibility family.

### Predecessor Output Variables

This section defines only the output-variable source used by environment
resolution.
It does not define the `outputs` field from Spec 012, `stdout.outputs`,
`outputs.write`, or structured object-form `output`.

Rules:

- A successful step with singular string-form `output: NAME` publishes one
  output variable named `NAME`.
- `NAME` should match the environment name rule `^[A-Za-z_][A-Za-z0-9_]*$`
  when a later step needs to read it through `$NAME`, `${NAME}`, or
  `${env.NAME}`.
- The output-variable value is the producing step's captured stdout with
  leading and trailing whitespace removed.
- A failed, aborted, timed-out, skipped, or not-yet-completed step publishes no
  output variable for this spec.
- Output variables enter only the environment scope of downstream steps that
  directly or transitively depend on the producing step.
- Output variables do not create dependencies.

### Environment Declarations

- `env` declarations may use map form, array-of-map form, or `NAME=value` list form.

- Sequence form may mix array-of-map entries and `NAME=value` entries.

- Map form is ordered by YAML source order.

- Each `NAME=value` list item must use `NAME=value` form and is split at the first `=`.

- `NAME=` is valid and defines an empty string value.

- `NAME=a=b` is valid and defines the value `a=b`.

- Env names must match `^[A-Za-z_][A-Za-z0-9_]*$`.

- Env values are environment strings.
- Scalar YAML values are converted to their string value before environment resolution.
- Workflow authors should quote values such as `false`, `0`, and `null` when the exact text must be preserved.

Ordering rules:

- Entries are evaluated from top to bottom.

- An entry may reference earlier entries from the same declaration.

- A later entry with the same name replaces the earlier entry for subsequent references and for the final environment visible to the owning step.

- An entry cannot resolve itself or a later entry from the same declaration.

Allowed references:

- Root `env` may reference `consts`, `params`, protected Dagu-managed run environment values, and env values that are already available.

- Step `env` may reference `consts`, `params`, root env values, earlier entries from the same step env declaration, step output references that are available before the step starts, and predecessor output variables that are already in the environment scope.
- Step `env` may also reference initial current-step Dagu-managed values that
  exist before `steps[].env` is evaluated.

- Container `env` follows the same rule as the root or step that owns the container.
- Entries in a container `env` declaration may reference earlier entries from the same container `env` declaration.

### Environment References

Forms:

```text
${env.NAME}
$NAME
${NAME}
```

Rules:

- `${env.NAME}` is a Dagu-owned environment reference.

- `$NAME` and `${NAME}` are unqualified environment expansion forms.

- `NAME` must match `^[A-Za-z_][A-Za-z0-9_]*$`.

- `$env.NAME` is ordinary string content.

- `${env.NAME}` reads `NAME` from the current environment scope.

- `$NAME` and `${NAME}` read `NAME` from the current environment scope only when Dagu owns unqualified environment expansion for the field being evaluated.

- The unqualified environment expansion ownership table below is authoritative
  for each field surface.

- When that table says Dagu preserves unqualified environment syntax for a
  later runtime, Dagu leaves `$NAME`, `${NAME}`, and shell-style environment
  expressions unchanged for that runtime.

Missing values:

- Missing `${env.NAME}` preserves the original reference text when the field is evaluated.
- Explicit inspection surfaces report a passive notice for that preserved reference.

- Missing `$NAME` or `${NAME}` is preserved when environment expansion runs.
- Passive notice reporting for missing unqualified references is defined by the
  validation rules below.

- If Dagu preserves an unqualified reference and later hands the field to a shell, container, or remote host, that later runtime may still expand the reference.
- For example, a POSIX shell normally expands an unset `$NAME` to an empty string.

Validation:

- `dagu validate` must not reject a well-formed environment reference only because `NAME` is unavailable during validation.

Insertion:

- `${env.NAME}` uses the string insertion rules from Spec 003.

### Unqualified Environment Expansion Ownership

This table refines the field evaluation matrix in Spec 003 for unqualified
environment syntax: `$NAME`, `${NAME}`, and shell-style `${NAME...}`
expressions.

`${env.NAME}` remains Dagu-owned in every value-resolved field that supports
environment references.

| Field surface | Unqualified environment behavior | Host process environment fallback |
| --- | --- | --- |
| Root `env` values | Dagu expands while constructing the root environment scope. | Allowed. |
| `steps[].env` values | Dagu expands while constructing the step environment scope. | Not allowed. |
| Selected root or step `container.env` values | Dagu expands while constructing the selected container environment scope. | Not allowed. |
| `dotenv[]` path strings | Dagu expands before loading dotenv files. | Allowed. |
| `params[].eval` | Dagu expands before dynamic evaluation runs. | Allowed. |
| `params[].default`, runtime parameter overrides, `secrets[].key`, and `secrets[].options` | Dagu does not expand unqualified environment syntax. | Not applicable. |
| `shell`, `shell_args[]`, root `working_dir`, `steps[].working_dir`, `preconditions[].condition`, `steps[].preconditions[].condition`, and `steps[].repeat_policy.condition` | Dagu expands against the current environment scope before the field is used. | Not allowed unless the owning field spec explicitly defines a host-process fallback. |
| `steps[].run` command form, script form, and array entries | Dagu preserves unqualified environment syntax for the selected shell or script interpreter. | Not allowed during Dagu value resolution. |
| `steps[].with.shell`, `steps[].with.shell_args`, and `steps[].with.shell_packages` for `run` steps | Dagu expands against the step environment scope before selecting the shell invocation. | Not allowed. |
| Direct local process command fields, including direct command paths and argv strings owned by an exec-style action | Dagu expands before starting the process. There is no later shell expansion. | Allowed when the owning action spec identifies the field as host-local direct execution. |
| SSH command text fields owned by SSH executor specs | Dagu expands values from the DAG or step environment scope and preserves unresolved unqualified references in command text. SSH executor specs own remote-host handoff behavior. | Not allowed. |
| SSH executor configuration strings | Dagu expands values from the DAG or step environment scope. Unresolved unqualified references remain literal local configuration text. | Not allowed. |
| Container command text fields owned by container executor specs | Dagu expands values from the DAG or step environment scope and preserves unresolved unqualified references in command text. Container executor specs own container runtime and process handoff behavior. | Not allowed. |
| Container executor configuration strings | Dagu expands values from the DAG or step environment scope. Unresolved unqualified references remain literal local configuration text. | Not allowed. |
| General `steps[].with` nested strings for action and executor inputs | Dagu expands against the step environment scope unless a more specific row or owning action spec applies. | Not allowed. |
| Root `container` object strings and `steps[].container` object strings other than `env` | Dagu expands against the owning root or step environment scope. | Not allowed. |
| `steps[].stdout`, `steps[].stderr`, artifact paths, `steps[].stdout.outputs.fields.*` literal strings, and `steps[].output.*` literal or path strings | Dagu expands against the step environment scope when the owning output surface is evaluated. | Not allowed. |
| `steps[].parallel` strings, sub-DAG names, sub-DAG params, handler step fields, and nested value-resolved workflow strings not listed above | Dagu expands against the current environment scope when the owning field is evaluated. | Not allowed. |
| Template script text or fields owned by a template executor | Dagu does not expand unqualified environment syntax unless the owning template spec explicitly opts in. | Not applicable. |

When host process environment fallback is not allowed, an unqualified reference
that is not present in the current environment scope is preserved. Validation
notice reporting is limited to the surfaces defined below.

SSH and container executor specs own the behavior after Dagu hands resolved or
preserved command text to a remote host, container runtime, or container
process.

### Single-Quoted Environment References

- Single-quoted `$NAME` and `${NAME}` are preserved during Dagu environment expansion.

- "Single-quoted" means the reference starts inside a single-quoted span in the field text after YAML parsing.

- YAML single-quoted scalar style does not by itself make an environment reference single-quoted unless the resulting field text contains the quote characters.

- `${env.NAME}` is Dagu-owned syntax and is not protected by shell-style single quotes.

### Shell-Style Environment Expressions

- A shell-style environment expression is one of the supported braced forms in
  the table below.

- Most forms start with a valid environment name.
- The length form starts with `#` followed by a valid environment name.

- These expressions are valid only in fields that support environment expansion.

- They read the environment value named `NAME` when Dagu owns environment
  expansion for the field.

- If `NAME` is unavailable when environment expansion runs, Dagu leaves the
  entire expression unchanged, regardless of the operator.

- Missing required-value forms such as `${NAME:?message}` do not fail Dagu
  value resolution; they are preserved.

- If `NAME` is available, Dagu evaluates supported shell parameter operators
  for the expression result.

- In the table, `word` and `pattern` are shell-word text inside the same braced
  expression.
- Dagu resolves environment references inside `word` and `pattern` using the
  same environment scope.
- Dagu does not run command substitution inside `word` or `pattern` unless the
  owning field also permits command substitution.

- Supported operator behavior:

  | Expression form | Result when `NAME` is available |
  | --- | --- |
  | `${NAME}` | The value of `NAME`. |
  | `${NAME:-word}` | The value of `NAME` when it is non-empty; otherwise `word`. |
  | `${NAME-word}` | The value of `NAME`, including an empty value. |
  | `${NAME:+word}` | `word` when `NAME` is non-empty; otherwise an empty string. |
  | `${NAME+word}` | `word`, even when `NAME` is empty. |
  | `${NAME:?word}` | The value of `NAME` when it is non-empty; otherwise the original expression is preserved. |
  | `${NAME?word}` | The value of `NAME`, including an empty value. |
  | `${NAME:=word}` | The value of `NAME` when it is non-empty; otherwise the original expression is preserved. |
  | `${NAME=word}` | The value of `NAME`, including an empty value. If `NAME` is missing, the original expression is preserved. |
  | `${NAME:offset}` | The substring from `offset` to the end of the value. |
  | `${NAME:offset:length}` | The substring from `offset`, limited by `length`. |
  | `${#NAME}` | The decimal count of Unicode code points in the value. |
  | `${NAME#pattern}` | The value after removing the shortest matching prefix. |
  | `${NAME##pattern}` | The value after removing the longest matching prefix. |
  | `${NAME%pattern}` | The value after removing the shortest matching suffix. |
  | `${NAME%%pattern}` | The value after removing the longest matching suffix. |

- Substring `offset` and `length` are decimal integers.
- Substring positions are zero-based byte positions in the UTF-8 encoded value.
- A negative `offset` counts backward from the end of the value.
- A missing `length` selects through the end of the value.
- A non-negative `length` selects at most that many bytes after `offset`.
- A negative `length` excludes that many bytes from the end of the value after
  applying `offset`.
- Out-of-range substring positions are clamped to the value boundary.
- A substring that splits a multibyte code point is outside portable behavior;
  workflow authors should use substring operators only on ASCII values when
  they need portable results.

- Pattern-removal `pattern` uses shell glob pattern matching, not regular
  expressions.
- Pattern removal does not perform filesystem globbing.
- If the pattern is invalid, Dagu leaves the original value unchanged for that
  expression.

- Assignment-like forms such as `${NAME:=word}` and `${NAME=word}` must not
  mutate the environment scope used by later references.
- If `NAME` is missing, Dagu preserves the assignment-like expression under the
  missing-value rule for shell-style environment expressions.
- If `NAME` is present but empty and `${NAME:=word}` would assign `word`, Dagu
  preserves shell-style expressions in that field.
- Simple `$NAME`, `${NAME}`, and `${env.NAME}` references in that field still
  resolve according to their normal rules.

- Malformed or unsupported shell-style expressions are preserved unchanged when
  Dagu owns environment expansion for the field.

- If Dagu preserves a shell-style environment expression and later hands the
  field to a shell, that shell owns any defaulting, slicing, required-value, or
  other shell behavior.

- Braced expressions whose base name is not a valid environment name are ordinary string content for this spec.

### Shell and Direct Execution

Namespaced Dagu references:

- Dagu resolves `${env.NAME}` before the owning field is consumed.

- This rule applies even when the field is later executed by a shell, container, or remote host.

Unqualified environment syntax:

- In shell-backed local `run` fields, Dagu leaves `$NAME`, `${NAME}`, and shell-style environment expressions for the selected shell unless the owning field spec explicitly says Dagu owns that syntax.

- In PowerShell-backed fields, `$env:NAME` is PowerShell syntax, not Dagu environment-reference syntax.

- In SSH-backed fields, Dagu must not resolve local process environment values into command text.
- Unqualified environment syntax that is not resolved from the DAG environment scope is preserved for the remote host.

- In container-backed fields, Dagu must not resolve local process environment values into container command text.
- Unqualified environment syntax that is not resolved from the DAG environment scope is preserved for the container runtime or container process.

- In direct local execution without a shell, `$NAME`, `${NAME}`, and shell-style environment expressions are resolved only when Dagu expands environment values for the field.

- Direct local execution has no later shell expansion.

- Unresolved environment references stay unchanged.

Secret-sensitive values:

- `${env.NAME}` inserts the resolved value into the field text.

- If `NAME` is secret-backed, the resulting field is still ordinary resolved
  field text unless the secret-owning spec defines additional masking for that
  surface.

- In a shell-backed local `run` field, Dagu preserves unqualified `$NAME` and
  `${NAME}` for the shell. If the step process environment contains `NAME`, the
  shell may still expand that value into the command it executes.

- Spec 006 does not guarantee that a shell, container process, remote host,
  subprocess, CLI argument list, log, or network target keeps a secret hidden
  after Dagu hands the field to that runtime.

- On surfaces where Dagu preserves unqualified environment syntax for a later
  runtime, such as shell-backed local `run`, workflow authors can avoid Dagu
  inserting a secret into field text by passing the secret through the
  environment and using `$NAME` or that runtime's native environment syntax.
  The target runtime's own exposure behavior still applies.

- On Dagu-expanded surfaces, including general action inputs and direct local
  process fields, `$NAME`, `${NAME}`, and `${env.NAME}` can insert a secret into
  resolved field text.

### Validation

- `dagu validate` must reject invalid `env` declaration shapes.

- `dagu validate` must reject invalid environment variable names in `env` declarations.

- `dagu validate` must reject a sequence `env` string item that does not contain `=`.

- `dagu validate` must reject secret declarations whose names start with
  `DAGU_` or collide with secret-reserved Dagu-managed environment names.

- Braced text that does not match a supported environment reference form is ordinary string content.

- An `env` entry that references itself or a later entry in the same declaration must preserve the original reference text.
- Explicit inspection surfaces must report a passive notice for that preserved reference.

- `dagu validate` must report passive notices for unresolved namespaced value
  references, such as `${env.NAME}` and `${steps.step_id.outputs.name}`, in
  inspected value-resolved fields.

- `dagu validate` must report passive notices for unresolved unqualified
  `$NAME` and `${NAME}` references in `env` declarations.

- `dagu validate` is not required to report every unresolved unqualified
  reference outside `env` declarations.
- A direct local process field with host process environment fallback allowed
  may validate without a passive notice even when a missing unqualified
  reference would be preserved during validation.

- `dagu validate` must not require runtime environment values, process
  environment values, dotenv values, predecessor step outputs, or current-step
  Dagu-managed values to exist.

## Errors

- Invalid `env` declaration shape must fail validation.

- Invalid environment variable name in an `env` declaration must fail validation.

- A sequence string entry without `=` must fail validation.

- A supported environment reference with no value preserves the original text.

- Explicit inspection surfaces report passive notices according to the
  validation rules above.

- Normal run execution must not emit passive value-reference notices as run logs, workflow events, status data, history data, artifacts, or DAG-run detail data.

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

Map-form ordering:

```yaml
env:
  SERVICE: api
  API_HOST: ${env.SERVICE}.internal
steps:
  - name: print_api_host
    run: echo ${env.API_HOST}
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

In this example Dagu resolves `${env.SERVICE}` before the command starts.
The shell reads `$SERVICE` from the step process environment.

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

Missing environment value preserved by Dagu, then consumed by the shell:

```yaml
steps:
  - name: optional_env
    run: echo ${OPTIONAL_ENV}
```

Dagu leaves `${OPTIONAL_ENV}` unchanged before starting the shell.
A POSIX shell then expands the unset value to an empty string.

Missing environment value preserved in direct execution:

```yaml
steps:
  - name: direct_exec
    action: exec
    with:
      command: /usr/bin/printf
      args:
        - '%s\n'
        - ${OPTIONAL_ENV}
```

Because no shell runs after Dagu, the direct process receives the literal argument `${OPTIONAL_ENV}`.

Secret-sensitive command text:

```yaml
secrets:
  - name: API_TOKEN
    provider: env
    key: PROD_API_TOKEN
steps:
  - name: call_api
    run: curl -H "Authorization: Bearer $API_TOKEN" https://api.example.com
```

The step process reads `API_TOKEN` from its environment.
Dagu does not insert the token while resolving the `run` field because
`$API_TOKEN` is shell-owned syntax. The shell may still expand the token after
Dagu starts the step process.
