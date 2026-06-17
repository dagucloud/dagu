# Spec: Step Run

## Implementation Status

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
- Value resolution: [Spec 003: Value Resolution](003-value-resolution.md)
- Field evaluation: [Spec 010: Field Evaluation](010-field-evaluation.md)
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

- Value resolution for `run` follows the value resolution spec and field evaluation spec.

- Unresolved supported references in `run` warn and remain literal before the command or script starts.

- Dagu does not shell-escape unresolved references in `run`.

- The selected shell or script interpreter receives the preserved text.

- The selected shell or script interpreter may interpret or reject the preserved text.

- Dagu must not resolve shell-style `$NAME` or `${NAME}` environment variable syntax in `run`.

- Dagu-owned environment references in `run` must use the namespaced `${env.NAME}` form.

- Shell variable syntax remains in the text handed to the shell or script interpreter.

- Shell command-substitution syntax remains in the text handed to the shell or script interpreter.

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

Validation must not:

- Execute `run`.

- Execute `$()` or backtick command substitution in `run`.

- Check whether the command path exists.

### Runtime Errors

Runtime must fail when:

- The selected `working_dir` value cannot be used as a process working directory.

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
      - go test ./internal/core/...
      - go test ./internal/runtime/...
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
