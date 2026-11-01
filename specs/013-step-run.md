# Spec: Step Run

## Status

Partially implemented. This spec defines the shared contract for the step
`run` field. Command-form and script-form details are defined by the linked
`run` specs.

## Scope

This spec defines behavior common to every step `run` form.

The `run` field has two string forms:

- Command form: a single-line string. Defined by [Spec 014: Step Run Command](014-step-run-command.md).
- Script form: a multi-line string. Defined by [Spec 015: Step Run Script](015-step-run-script.md).

This spec also defines the accepted array form, where each array item is a
command-form `run` entry.

It also defines selected-step runs: a start request that runs only some steps,
with explicit input values and, optionally, no trace in the history.

This spec does not define:

- The `action: exec` direct argv execution contract.
- Non-`run` execution field syntax.
- Action-specific `with` inputs.
- Executor-specific container or remote process models.

Executor-specific behavior, such as Docker command wrapping or SSH remote script
construction, must be defined by the owning executor spec when those executors
reuse `run` command text.

## Goal

Workflow authors can use `run` for local shell work with a predictable boundary
between Dagu value resolution and shell evaluation.

## Motivation

The `run` field contains shell-owned text. Dagu must be explicit about which
syntax it owns and which syntax remains owned by the shell or script
interpreter.

This prevents ambiguity between:

- Dagu-owned references, such as `${consts.name}`, `${params.name}`, `${env.NAME}`, and `${steps.build.outputs.image}`.
- Shell syntax, such as `$NAME`, `${NAME}`, `$()`, backticks, pipes, redirects, and command chaining.
- Direct argv execution, which belongs to an exec action spec, not the `run` field.

## Related Specs

- YAML schema: [Spec 002: YAML Schema](002-yaml-schema.md)
- Value resolution: [Spec 003: Value Resolution and Field Evaluation](003-value-resolution.md)
- Dynamic evaluation: [Spec 011: Dynamic Evaluation](011-dynamic-evaluation.md)
- Step outputs: [Spec 012: Step Outputs](012-step-outputs.md)
- Step run command form: [Spec 014: Step Run Command](014-step-run-command.md)
- Step run script form: [Spec 015: Step Run Script](015-step-run-script.md)

## Behavior

### Field Shape

- `run` is optional.

- When `run` is a string, the decoded string must contain at least one non-whitespace character.

- Dagu selects command form or script form from the decoded string before trimming leading or trailing whitespace.

- A string `run` without line breaks is command form.

- A string `run` with one or more line breaks is script form.

- Line breaks in script form are preserved.

- Array form is accepted.

- Array-form `run` entries are command-form entries.

- Array-form `run` accepts string items.

- Array-form `run` accepts primitive scalar items and converts them to strings.

- Array-form `run` accepts single-key mapping items whose values are primitive scalars and converts them to command strings in `key: value` form.

- Empty or whitespace-only string entries are ignored in array form.

- Array-form `run` entries must not contain line breaks after scalar conversion.

- Multi-key mapping items, nested mapping items, and nested array items must fail validation.

### Form Selection

`run` form is selected from the YAML value shape:

| YAML shape | Form | Owning spec |
| --- | --- | --- |
| Single-line string | Command form | [Spec 014](014-step-run-command.md) |
| Multi-line string | Script form | [Spec 015](015-step-run-script.md) |
| Array | Ordered command forms | [Spec 014](014-step-run-command.md) |

Rules:

- Dagu must not expose `run` as a user-authored argv list.

- Direct process execution without shell parsing is outside this spec and belongs to an exec action spec.

- Array-form entries run in order.

- Array-form execution stops at the first failed entry.

- If all array-form entries are empty, validation must fail.

### Value Resolution

- Dagu resolves Dagu-owned references in `run` before the command or script starts.

- Value resolution for `run` follows the value resolution and field evaluation spec.

- Unresolved supported references in `run` remain literal before the command or script starts.
- Explicit inspection surfaces report a passive notice for each preserved reference.

- Dagu does not shell-escape resolved values or preserved references in `run`.

- A resolved value may contain text that is meaningful to the selected shell or script interpreter.

- Workflow authors must quote or escape values according to the selected shell or script interpreter when interpolating them directly into `run`.

- Arbitrary data is safer to pass through step `env`, files, or a direct-argv execution field when such a field is available.

- The selected shell or script interpreter receives the preserved text.

- The selected shell or script interpreter may interpret or reject the preserved text.

- Dagu must not resolve shell-style `$NAME` or `${NAME}` environment variable syntax in `run`.

- Dagu-owned environment references in `run` must use the namespaced `${env.NAME}` form.

- Shell variable syntax remains in the text handed to the shell or script interpreter.

- Shell command-substitution syntax remains in the text handed to the shell or script interpreter.

- Escaped Dagu-looking text such as `\${steps.build.outputs.image}` follows Spec 003 escape behavior.

### Command Substitution

- Dagu must not execute `$()` command substitution while evaluating `run`.

- Dagu must not execute backtick command substitution while evaluating `run`.

- Dagu must leave `$()` text unchanged in `run`.

- Dagu must leave backtick text unchanged in `run`.

- The selected shell or script interpreter owns `$()` and backtick execution after Dagu starts the command or script.

- Dagu dynamic evaluation is available only where the dynamic evaluation spec explicitly allows it, such as `params[].eval`.

### Shell Operators

- Dagu must preserve shell operator text in `run` except where Dagu-owned value references are replaced.

- Dagu must not parse, split, normalize, validate, or execute shell operators in `run`.

- Shell operator text includes pipes (`|`), redirects (`>`, `>>`, `<`, `2>`), command chaining (`&&`, `||`, `;`), background execution (`&`), grouping syntax, and shell-specific control syntax.

- This spec defines no cross-platform shell operator grammar.

- The selected shell or script interpreter defines which shell operators are valid and what they do.

- If the selected shell or script interpreter rejects shell operator syntax, the step fails according to the runtime error and exit-code rules.

### Shell Selection

#### Selection Order

Shell selection order is:

1. Step `with.shell`.
2. Root `shell`.
3. Runtime default shell configuration, including `DAGU_DEFAULT_SHELL`.
4. Platform discovery fallback.

#### Run Shell Inputs

- `with.shell` selects a different shell for a `run` step.

- `with.shell_args` selects shell arguments for the step shell selected by `with.shell`.

- `with.shell_args` must not be specified unless `with.shell` is specified.

- `with.shell_packages` selects shell packages for a `run` step when the selected shell supports packages.

- Accepted `with` keys for `run` are `shell`, `shell_args`, and `shell_packages`.

- A `run` step must not use any other `with` key.

- The shell value `direct` is not valid for `run`; direct process execution belongs to `action: exec`.

- An authored root or step shell value of `direct` is invalid for any `run` step.

- If the runtime default shell resolves to `direct`, Dagu must fail before the command or script starts.

#### Platform Fallback

- On Unix-like platforms, platform discovery uses `$SHELL` when set and falls back to `sh` when available.

- On Windows, platform discovery prefers PowerShell Core, then Windows PowerShell, then `cmd`.

### Working Directory

#### Precedence

- Step-level `working_dir` has highest precedence for the step process working directory.

- Explicit root `working_dir` changes the default step process working directory.

#### Fallbacks

- If neither step-level `working_dir` nor explicit root `working_dir` is set, the per-run work directory exposed as `DAG_RUN_WORK_DIR` is used when available.

- If `DAG_RUN_WORK_DIR` is unavailable, Dagu falls back to the resolved non-explicit DAG working directory.

- If no DAG working directory can be resolved, Dagu falls back to the process working directory, then to the user home directory.

- Relative root working directories are resolved against the workflow file directory when the workflow is loaded.

#### Value Resolution

- The working directory path is value-resolved before the command or script starts.

### Standard Input

#### Source

- Step-level `stdin` names a file whose contents are piped to the step process standard input.

- If `stdin` is not set, the step process receives empty standard input.

- Every entry of an array-form `run` receives the file from its start.

#### Path Resolution

- A leading `~` expands to the user home directory.

- A relative `stdin` path is resolved against the step process working directory.

#### Action Support

- An action that cannot consume standard input must reject `stdin`.

- The `ssh` action does not support `stdin`, because the remote shell reads its script from the session standard input channel.

#### Value Resolution

- The `stdin` path is value-resolved before the command or script starts.

- Unlike `run`, an unresolved supported reference in `stdin` must fail the step rather than remain literal.

### Environment

#### Runtime Environment

- The command or script inherits the runtime environment assembled for the step.

- Dagu-defined environment variables override inherited variables with the same name.

- Shell variables are expanded by the selected shell or script interpreter, not by Dagu value resolution.

#### Step Outputs

- Step output declaration and `DAGU_OUTPUT_FILE` behavior are defined by [Spec 012: Step Outputs](012-step-outputs.md).

#### Stream Files

- Dagu exposes step stream file paths through `DAG_RUN_STEP_STDOUT_FILE` and `DAG_RUN_STEP_STDERR_FILE`.

### Runtime Results

#### Captured Streams

- Dagu captures stdout and stderr.

- Captured stdout and stderr are runtime streams.

#### Step Outputs

- Step outputs are published through the step outputs spec.

- Captured stdout and stderr are not step outputs unless published through the step outputs spec.

### Selected Step Runs

`POST /api/v1/dags/{fileName}/start` and `POST /api/v1/dag-runs` accept these body fields to run only some steps of a DAG.

- `steps` lists the steps to run, by step name or ID. It must name at least one step. Every other step is recorded as skipped by retry and does not run.

- `outputsFromRunId` names a finished run of the same DAG. Its step outputs and work directory are carried into the skipped steps that a selected step depends on. It requires `steps`.

- `outputsFromRuns` names, for single skipped steps, the run that the step carries its outputs from. It is an object that maps a step name or ID to a run ID. It requires `steps`.

- A step in `outputsFromRuns` takes its outputs from its own run, not from `outputsFromRunId`. The server seeds the step from the node of that run in the same way as for `outputsFromRunId`. The work directory still comes only from `outputsFromRunId`.

- The server MUST reject an entry of `outputsFromRuns` with HTTP 400 and the message `step X cannot be reused from run Y: <reason>` when one of these is true. The run does not exist for the DAG (it was removed, or it belongs to another DAG). The run is not finished. The run has no node for the step. The step did not succeed in the run and does not carry copied outputs. A normal run and an ephemeral run that still exists can both be the source.

- A step selected to run MUST NOT be a key of `outputsFromRuns`. An unknown step, a run ID that is not valid, or two keys that address one step is rejected with HTTP 400.

- `outputs` supplies values for the outputs of skipped steps, keyed by step name or ID and then by output name. It is for clients that are not the web UI: the web UI sends no values. It requires `steps` and works with or without `outputsFromRunId`. A supplied value takes precedence over the carried value of the same output.

- `outputsSources` names the run that each step of `outputs` was captured from. It is an object keyed by step name or ID. Each value has `runId` and an optional `capturedAt`. It requires `steps`.

- `outputsSources` is optional. A step in `outputs` with no entry is accepted and has no entry in `ReusedInputs`. An entry for a step that is not in `outputs` is rejected with HTTP 400.

- `runId` MUST be a valid run ID, as for `outputsFromRunId`. The run does not need to exist, because the values can come from a run that was removed. `capturedAt` is an RFC 3339 timestamp. An invalid `runId` or `capturedAt` is rejected with HTTP 400.

- A supplied output reaches `${steps.<id>.outputs.<name>}` in the selected steps. The name of a step's string-form `output: VAR` sets that variable.

- A request that supplies an output the step does not declare, an output of a selected step, or an output that is not valid for its declared type is rejected with HTTP 400.

- A reference to an output that nobody supplied is not an error of the request. The reference stays literal text, as Spec 007 defines, and the selected step runs with that text.

#### Command Line

`dagu start --only <step>` runs only the named steps, as the `steps` field does. Repeat `--only` to select more than one step.

- `--outputs-from <run-id>` is the command line form of `outputsFromRunId`. It carries the work directory, and the outputs of the skipped steps that the selected steps depend on, from one finished run.

- `--output-from <step>=<run-id>` is the command line form of `outputsFromRuns`. It names the run that gives the outputs of one skipped step. Repeat the flag for more than one step. The step is a step name or ID. It requires `--only`.

- The command applies the same rules and gives the same errors as the server does for `outputsFromRuns`, with the message `step X cannot be reused from run Y: <reason>`. The command reads the run with the same code as the server. It stops before it starts a run when one entry is not valid.

- A step selected by `--only` MUST NOT be named in `--output-from`. Two entries for one step are an error.

- A step named in `--output-from` takes its outputs from its own run, not from `--outputs-from`. The run records `ReusedInputs` with `via` set to `run`, as for the server.

- `--output <step>.<name>=<value>` is the command line form of `outputs`. It is deprecated in favor of `--output-from`, and it still works: it requires `--only`, can be repeated, and takes precedence over `--outputs-from` and `--output-from` for that output. The command has no form of `outputsSources`, so a step set by `--output` has no entry in `ReusedInputs`.

#### Handlers of a Selected-Steps Run

A graph end step is a step that no other step depends on. A dependency is an entry of `depends`, an inferred dependency from a `${steps.<id>...}` reference, or the dependency that a router adds to each of its target steps. Dagu uses the dependency data of the DAG definition that the planner uses.

- A selected-steps run calls the handlers that run after the steps only when the selection has at least one graph end step. These handlers are `handler_on.success`, `failure`, `abort`, `wait` and `exit`.

- When the selection has no graph end step, the run does not call these handlers. This includes `exit`. The skipped handlers do not run, and the run status has no node for them.

- `handler_on.init` runs before the steps. A selected-steps run calls it as any other run does.

- The run status is computed from the selected steps in the same way as for any run. A skipped handler does not change it.

- The rule is the same for a normal run and an ephemeral run, and for every way to start the run: the API, the web UI and `dagu start --only`.

- The run status records the rule in `skipHandlers`. A retry of the run keeps the same rule. A request without `steps` does not set it, and the run calls its handlers as before.

#### Reused Inputs

A selected-steps run records where the carried outputs of its skipped steps come from. The record is `ReusedInputs` in the run status.

- `ReusedInputs` has one entry for each skipped step that carried outputs, except a step in `outputs` with no entry in `outputsSources`. The order of the entries is the order of the steps in the DAG.

- Only a skipped step that a selected step depends on, directly or through other steps, carries outputs. This includes a step that `outputs` or `outputsFromRuns` names. A skipped step that no selected step depends on, such as a step after the selected steps, carries no outputs, no log and no times. It is recorded as skipped, has no entry, and shows no mark of reuse.

- An entry has `step` (the step name), `runId`, `capturedAt`, `startedAt`, `finishedAt` and `via`. `capturedAt` is absent when it is unknown.

- `via` is `run` for a step whose outputs come from `outputsFromRunId`, or from `outputsFromRuns` for that step. Then `runId` is the run that gave the outputs. `startedAt` and `finishedAt` are the times that the step had in that run, with the precision that the source run stored (millisecond for a new run), and `capturedAt` is the finish time of the step in that run. A time is absent when the node of that run has none.

- A step in `outputsFromRuns` has the entry of its own run, not the entry of `outputsFromRunId`.

- `via` is `cache` for a step whose outputs come from `outputs`. Then `runId` and `capturedAt` come from `outputsSources`, and the entry has no `startedAt` and no `finishedAt`. The web UI does not use this form.

- A step that has a run and supplied outputs has the `cache` entry, because the values in `outputs` take precedence. Without an entry in `outputsSources`, it has no entry.

- A step that carried no outputs has no entry.

- The record stays on the run for all its attempts.

- The run details response gives the record as `reusedInputs`. It is absent when the list is empty.

#### Ephemeral Runs

`ephemeral` is an optional boolean, `false` by default. A request with `ephemeral: true` runs the selected steps in a run that leaves no trace.

- `ephemeral` is allowed only together with `steps`. A request for the whole DAG with `ephemeral: true` is rejected with HTTP 400.

- An ephemeral run executes like any run, with the same runner, logs, status, and outputs. Its run ID is a normal run ID, and the start endpoints answer as they do for any run.

- While it exists, the run is readable by its ID through the run details, step log, step outputs, spec, and SSE endpoints.

- The run is listed nowhere by default: not in the run lists, the DAG history (unless the request sets `includeEphemeral`), the latest run of a DAG, the queues, the dashboard and system counters, the artifact listing, or the events feed. It does not count in history retention.

- The run produces no event, so notification rules and incident policies do not see it. Being ephemeral does not change which `handler_on` handlers run: they belong to the definition and follow the rule for the handlers of a selected-steps run.

- The run cannot be retried, rescheduled, or edited. These requests answer HTTP 409.

- The server removes the run 10 minutes after it ends, and removes every ended ephemeral run when it starts. A run that has not ended is never removed.

- The audit log records the start request as for any run, marked `ephemeral`.

- The run details response and the run summary items have `ephemeral: true` for an ephemeral run. The field is absent for other runs.

- `GET /api/v1/dags/{fileName}/dag-runs` accepts the optional query parameter `includeEphemeral`, `false` by default. When it is `true`, the response also has the ephemeral runs of that DAG. They are merged by start time with the other runs, under the same limit and order, and each has `ephemeral: true`. No other list accepts this parameter.

An ephemeral run can be the `outputsFromRunId` source of another request while it exists.

### Exit, Abort, And Timeout

- Exit code `0` means the step succeeded.

- A non-zero exit code means the step failed.

- Termination by signal or platform termination request means the step failed.

- If output parsing fails after the command or script exits successfully, the step attempt fails as defined by the step outputs spec.

- If the workflow run is aborted while the command or script is running, Dagu must terminate it.

- If runtime timeout behavior stops the step while the command or script is running, Dagu must terminate it.

- A step must not reach a terminal state while its command or script process is still running.

- Outputs from an aborted or timed-out command or script attempt must not be published.

### Validation Errors

Validation must fail when:

- `run` is neither a string nor an accepted array form.

- string `run` is empty or whitespace only.

- array-form `run` has no non-empty command entries.

- A `run` step contains a `with` key that is not accepted for `run`.

- A `run` step specifies `with.shell_args` without `with.shell`.

- A `run` step selects `direct` through step `with.shell` or root `shell`.

- An array-form `run` entry contains a line break.

- `stdin` is set on an action that does not support standard input.

Validation must not:

- Execute `run`.

- Execute `$()` or backtick command substitution in `run`.

- Check whether the command path exists.

- Check whether the `stdin` file exists.

### Runtime Errors

Runtime must fail when:

- The selected `working_dir` value cannot be used as a process working directory.

- The `stdin` path resolves to an empty value, or still carries a supported reference.

- The `stdin` file cannot be opened for reading.

- The selected shell or script interpreter cannot be started.

- The command or script cannot be started.

- The selected shell or script interpreter cannot find the command path.

- The command or script exits with a non-zero code.

- The command or script is terminated by abort or timeout.

- The command or script emits invalid declared outputs.

## Examples

Command form:

```yaml
steps:
  - id: hello
    run: echo hello
```

Script form:

```yaml
steps:
  - id: build
    run: |
      set -eu
      ./scripts/build.sh
      ./scripts/test.sh
```

Array form:

```yaml
steps:
  - id: checks
    run:
      - go test ./internal/spec/...
      - go test ./internal/runtime/...
```

A file piped to standard input:

```yaml
steps:
  - id: fetch
    run: echo payload
  - id: summarize
    stdin: ${fetch.stdout}
    run: cat
    depends: fetch
```

Dagu references are resolved before the shell or script interpreter runs:

```yaml
params:
  - name: version
    type: string
    required: true
steps:
  - id: build
    run: ./scripts/build.sh ${params.version}
```

Use environment variables to avoid direct shell parsing of inserted data:

```yaml
params:
  - name: version
    type: string
    required: true
steps:
  - id: build
    env:
      VERSION: ${params.version}
    run: ./scripts/build.sh "$VERSION"
```

Shell variables are expanded by the shell, not by Dagu:

```yaml
steps:
  - id: home
    run: echo "$HOME"
```

Backticks are preserved for the shell:

```yaml
steps:
  - id: today
    run: echo "Today is `date +%Y-%m-%d`"
```

Escaped Dagu-looking text is preserved as language-owned code text:

```yaml
steps:
  - id: script
    run: |
      python3 - <<'PY'
      print('\${steps.build.outputs.image}')
      PY
```

In this example the author wrote `\${steps.build.outputs.image}` in the workflow.
Dagu removes its escape marker, does not resolve the reference, and gives `${steps.build.outputs.image}` to the Python script.

Use `params[].eval` when Dagu should compute a value before `run`:

```yaml
params:
  - name: today
    type: string
    eval: `date +%Y-%m-%d`
  - name: branch
    type: string
    eval: $(git branch --show-current)
steps:
  - id: print
    run: echo ${params.today} ${params.branch}
```

The `$()` form in `params[].eval` is defined by the dynamic evaluation spec.

Use `action: exec` for direct argv execution:

```yaml
steps:
  - id: process
    action: exec
    with:
      command: /usr/bin/python3
      args:
        - -u
        - app.py
```

Invalid empty command:

```yaml
steps:
  - id: bad
    run: ""
```

Invalid unsupported command shape:

```yaml
steps:
  - id: bad
    run:
      command: echo hello
```
