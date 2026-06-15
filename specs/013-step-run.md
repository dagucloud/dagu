# Spec: Step Run

## Implementation Status

Not implemented. This spec describes target conformance behavior and must not be
treated as current product behavior.

## Scope

This spec defines local command execution through the step `run` field.

This spec does not define:

- `action: exec` direct argv execution.
- Legacy `command`, `script`, `cmd_with_args`, `shell`, `shell_args`, or `shell_packages` fields.
- Action-specific `with` inputs.
- Container or remote execution behavior, except where another executor explicitly reuses `run` semantics.

## Goal

Workflow authors can use `run` for local shell commands with a predictable boundary between Dagu value resolution and shell evaluation.

## Motivation

The `run` field contains shell command text. Dagu must be explicit about which syntax it owns and which syntax remains owned by the shell.

This prevents ambiguity between:

- Dagu-owned references, such as `${consts.name}`, `${params.name}`, `${env.NAME}`, and `${steps.build.outputs.image}`.
- Shell syntax, such as `$NAME`, `${NAME}`, `$()`, backticks, pipes, redirects, and command chaining.
- Direct argv execution, which belongs to `action: exec`, not `run`.

## Related Specs

- YAML schema: [Spec 002: YAML Schema](002-yaml-schema.md)
- Value resolution: [Spec 003: Value Resolution](003-value-resolution.md)
- Field evaluation: [Spec 010: Field Evaluation](010-field-evaluation.md)
- Dynamic evaluation: [Spec 011: Dynamic Evaluation](011-dynamic-evaluation.md)
- Step outputs: [Spec 012: Step Outputs](012-step-outputs.md)

## Behavior

### Field Shape

- `run` is optional.

- When present, `run` must be a string.

- The decoded `run` string must contain at least one non-whitespace character.

- Multi-line `run` strings are valid.

- Line breaks in multi-line `run` strings are preserved.

### Execution Model

- A step with `run` uses the local command executor.

- The command runs on the data-plane runner host.

- `run` is shell command text, not an argv list.

- Dagu passes the resolved `run` string to the selected shell as one command string.

- Dagu must not split `run` into argv.

- Direct process execution without shell parsing must use `action: exec`.

- Command path lookup is performed by the shell or operating system from the step process working directory.

### Shell Selection

- Shell selection order is:

  1. Step `with.shell`.
  2. Root `shell`.
  3. Runtime default shell configuration, including `DAGU_DEFAULT_SHELL`.
  4. Platform discovery fallback.

- On Unix-like platforms, platform discovery uses `$SHELL` when set and falls back to `sh` when available.

- On Windows, platform discovery prefers PowerShell Core, then Windows PowerShell, then `cmd`.

- `with.shell` may select a different shell for a `run` step.

- `with.shell_args` may select shell arguments for a `run` step.

- `with.shell_packages` may select shell packages for a `run` step when the selected shell supports packages.

- Accepted `with` keys for `run` are `shell`, `shell_args`, and `shell_packages`.

- A `run` step must not use any other `with` key.

### Value Resolution

- Dagu resolves Dagu-owned references in `run` before command execution.

- Value resolution for `run` follows the value resolution spec and field evaluation spec.

- If value resolution fails, the command must not start.

- Dagu must not resolve shell-style `$NAME` or `${NAME}` environment variable syntax in `run`.

- Dagu-owned environment references in `run` must use the namespaced `${env.NAME}` form.

- Shell variable syntax remains in the command string passed to the shell.

- Shell command-substitution syntax remains in the command string passed to the shell.

### Command Substitution

- Dagu must not execute `$()` command substitution while evaluating `run`.

- Dagu must not execute backtick command substitution while evaluating `run`.

- Dagu must leave `$()` text unchanged in `run`.

- Dagu must leave backtick text unchanged in `run`.

- The selected shell may execute `$()` or backticks after Dagu hands off the command string.

- Direct argv execution has no shell handoff, so `$()` and backticks remain literal argv text unless the invoked program interprets them.

- Dagu dynamic evaluation is available only where the dynamic evaluation spec explicitly allows it, such as `params[].eval`.

### Working Directory

- Root `working_dir` changes the default step process working directory.

- Step-level `working_dir` changes the process working directory for that step.

- If neither root `working_dir` nor step-level `working_dir` is set, the step process working directory is the per-run work directory exposed as `DAG_RUN_WORK_DIR`.

- The working directory path is value-resolved before the command starts.

### Environment

- The command inherits the runtime environment assembled for the step.

- Dagu-defined environment variables override inherited variables with the same name.

- Shell variables are expanded by the selected shell, not by Dagu value resolution.

- If the step declares outputs that require `DAGU_OUTPUT_FILE`, Dagu sets `DAGU_OUTPUT_FILE` before starting the command.

- If the step does not declare outputs that require `DAGU_OUTPUT_FILE`, `DAGU_OUTPUT_FILE` must be absent from the command environment.

### Runtime Results

- Dagu captures command stdout and stderr.

- Captured stdout and stderr are runtime streams.

- Step outputs are published only through the step outputs spec.

- Captured stdout is not automatically a step output under the step outputs spec.

### Exit, Abort, And Timeout

- Exit code `0` means command execution succeeded.

- A non-zero exit code means command execution failed.

- Termination by signal or platform termination request means command execution failed.

- If output parsing fails after the command exits successfully, the step attempt fails as defined by the step outputs spec.

- If the workflow run is aborted while the command is running, Dagu must terminate the command.

- If runtime timeout behavior stops the step while the command is running, Dagu must terminate the command.

- A step must not reach a terminal state while its command process is still running.

- Outputs from an aborted or timed-out command attempt must not be published.

### Validation Errors

Validation must fail when:

- `run` is not a string.

- `run` is empty or whitespace only.

- `run` contains a malformed Dagu-owned value reference that is statically checkable.

- `run` contains an invalid Dagu-looking shorthand reference.

- `run` contains an output reference that violates the step outputs spec.

- A `run` step contains a `with` key that is not accepted for `run`.

Validation must not:

- Execute `run`.

- Execute `$()` or backtick command substitution in `run`.

- Check whether the command path exists.

### Runtime Errors

Runtime must fail when:

- Value resolution in `run` fails.

- The shell cannot be started.

- The command cannot be started by the shell.

- The shell cannot find the command path.

- The command exits with a non-zero code.

- The command is terminated by abort or timeout.

- The command emits invalid declared outputs.

## Examples

Simple command:

```yaml
steps:
  - id: hello
    run: echo hello
```

Multi-line command:

```yaml
steps:
  - id: build
    run: |
      set -eu
      ./scripts/build.sh
      ./scripts/test.sh
```

Dagu references are resolved before the shell runs:

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
steps:
  - id: print
    run: echo ${params.today}
```

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

Command with declared step output:

```yaml
steps:
  - id: build
    run: |
      printf 'image_tag=v1.2.3\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: image_tag
```

Custom shell for a `run` step:

```yaml
steps:
  - id: bash_example
    run: printf "%s\n" "$BASH_VERSION"
    with:
      shell: bash
      shell_args: [-e, -o, pipefail]
```

Invalid empty command:

```yaml
steps:
  - id: bad
    run: ""
```

Invalid non-string command:

```yaml
steps:
  - id: bad
    run: [echo, hello]
```

## Acceptance Criteria

Validation:

- A black-box fixture verifies `dagu validate` accepts a step with a simple `run` string.
- A black-box fixture verifies `dagu validate` accepts a multi-line `run` string.
- A black-box fixture verifies `dagu validate` rejects a non-string `run`.
- A black-box fixture verifies `dagu validate` rejects an empty `run`.
- A black-box fixture verifies `dagu validate` accepts `$()` text in `run` and does not execute it.
- A black-box fixture verifies `dagu validate` accepts backtick text in `run` and does not execute it.
- A black-box fixture verifies `dagu validate` does not execute `run`.
- A black-box fixture verifies `dagu validate` does not require the command path to exist.
- A black-box fixture verifies `dagu validate` rejects unsupported `with` keys on `run`.

Value resolution:

- A black-box fixture verifies `dagu run` resolves Dagu-owned references before command execution.
- A black-box fixture verifies `$NAME` remains available for shell expansion.
- A black-box fixture verifies `$()` remains available for shell execution.
- A black-box fixture verifies backticks remain available for shell execution.
- A black-box fixture verifies a value-resolution failure prevents the command from starting.

Shell handoff:

- A black-box fixture verifies `dagu run` executes a simple `run` command.
- A black-box fixture verifies `dagu run` passes multi-line `run` strings to the shell with line breaks preserved.
- A black-box fixture verifies `dagu run` uses `with.shell` and `with.shell_args` for the selected shell.
- A black-box fixture verifies `dagu run` uses root `shell` when step `with.shell` is omitted.
- A black-box fixture verifies `dagu run` uses the runtime default shell when neither step nor root shell is configured.
- A black-box fixture verifies `run` is not split into argv by Dagu.
- A black-box fixture verifies direct argv execution is covered by `action: exec`, not `run`.

Runtime behavior:

- A black-box fixture verifies `dagu run` executes from `DAG_RUN_WORK_DIR` when neither root nor step working directory is set.
- A black-box fixture verifies `dagu run` respects root `working_dir`.
- A black-box fixture verifies `dagu run` respects step `working_dir`.
- A black-box fixture verifies command stdout is captured.
- A black-box fixture verifies command stderr is captured.
- A black-box fixture verifies stdout is not automatically treated as a step output.
- A black-box fixture verifies `DAGU_OUTPUT_FILE` is set when the step declares outputs that require it.
- A black-box fixture verifies `DAGU_OUTPUT_FILE` is absent when the step does not declare outputs that require it.
- A black-box fixture verifies non-zero command exit fails the step.
- A black-box fixture verifies a command path that the shell cannot find fails the step.
- A black-box fixture verifies abort terminates the running command.
- A black-box fixture verifies timeout termination fails the running command.
