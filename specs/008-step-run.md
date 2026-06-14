# Spec: Step Run

Scope: local command execution through the step `run` field.

## Objective

Define how a step runs a local command on the data-plane runner host.

## Inputs

Input is a workflow YAML file accepted by the YAML schema spec.

Step `run` validation extends:

```sh
dagu validate [<workflow>]
```

Workflow execution uses:

```sh
dagu run <workflow>
```

**Command behavior:**

- When this spec is implemented, `dagu validate` validates the `run` field shape and statically checkable value references.
- `dagu validate` must not execute commands.
- `dagu validate` must not check whether the command path exists.
- `dagu run` accepts project workflow targets as defined by the project spec.
- Dagu must not append `.yaml` or `.yml` to extensionless workflow targets.
- Dagu must not fall back from an unknown project workflow target to any other filesystem path.
- `dagu run` executes the command when the step is ready to start.

**A step may define `run`:**

```yaml
steps:
  - id: build
    run: ./scripts/build.sh ${params.version}
```

## Behavior

**Field rules:**

| Field | Required | Rules |
| --- | --- | --- |
| `run` | No. | Must be a string when present. The decoded string must contain at least one non-whitespace character. Dagu does not evaluate command-substitution syntax in this field. |

**Execution rules:**

- A step with `run` uses the local command executor.
- The command executes on the data-plane runner host.
- The command string is passed to the platform shell as one command string.
- Dagu must not split `run` into argv.
- Multi-line `run` strings are valid and line breaks are preserved.
- Command path lookup is performed by the platform shell or operating system from the step process working directory.
- This spec does not define a `shell` step field.
- To use a different shell, invoke that shell inside `run`.

**Platform shell:**

| Platform | Shell |
| --- | --- |
| POSIX | `/bin/sh -c` |
| Windows | `%ComSpec% /S /C`, or `cmd.exe /S /C` when `ComSpec` is unset. |

**Value resolution rules:**

- Dagu value resolution runs before command execution.
- Value resolution for `run` follows the value resolution spec and field evaluation spec.
- If value resolution fails, the command must not start.
- Dagu must not resolve shell-style `$NAME` syntax.
- Dagu must leave `$()` and backtick text unchanged.
- Shell-style variables and command-substitution text remain in the command string passed to the platform shell.

**Working directory rules:**

- The step process working directory is defined by the project spec.
- In project mode, the default step process working directory is `project_root`.
- A root `working_dir` value changes the default step process working directory as defined by the project spec.
- This spec does not define a step-level `working_dir` field.

**Environment rules:**

- The command inherits the Dagu process environment.
- Dagu-defined environment variables override inherited variables with the same name.
- If the step declares outputs, Dagu sets `DAGU_OUTPUT_FILE` to the output file path defined by the step outputs spec.
- If the step does not declare outputs, `DAGU_OUTPUT_FILE` must be absent from the command environment.

**Exit rules:**

- Exit code `0` means the command execution succeeded.
- A non-zero exit code means the command execution failed.
- Termination by signal or platform termination request means the command execution failed.
- If output parsing fails after command execution succeeds, the step attempt fails as defined by the step outputs spec.

**Abort and timeout rules:**

- If the workflow run is aborted while the command is running, Dagu must terminate the command.
- If runtime timeout behavior stops the step while the command is running, Dagu must terminate the command.
- A step must not reach a terminal state while its command process is still running.
- Outputs from an aborted or timed-out command attempt must not be published.

## Outputs

**Runtime output rules:**

- Dagu captures command stdout.
- Dagu captures command stderr.
- Captured stdout and stderr are runtime outputs, not step outputs.
- Step outputs are published only through the step outputs spec.
- This spec does not define durable result storage format.

## Errors

**Validation errors:**

- A non-string `run` value must fail during workflow validation.
- An empty or whitespace-only `run` value must fail during workflow validation.
- A malformed value reference in `run` must fail during workflow validation when it is statically checkable.
- An output reference in `run` that violates the step outputs spec must fail during workflow validation.

**Runtime errors:**

- A value resolution failure in `run` must fail before the command starts.
- A command that cannot be started must fail the step attempt.
- A command path that the shell cannot find must fail the step attempt.
- A command that exits with a non-zero code must fail the step attempt.
- A command terminated by abort or timeout must fail the step attempt.
- A command that emits invalid declared outputs must fail the step attempt as defined by the step outputs spec.

## Examples

Valid simple command:

```yaml
steps:
  - id: hello
    run: echo hello
```

Valid command with value resolution:

```yaml
params:
  - name: version
    type: string
    required: true
steps:
  - id: build
    run: ./scripts/build.sh ${params.version}
```

Valid multi-line command:

```yaml
steps:
  - id: build
    run: |
      set -eu
      ./scripts/build.sh
      ./scripts/test.sh
```

Shell variables are expanded by the shell, not Dagu:

```yaml
steps:
  - id: home
    run: echo "$HOME"
```

Valid command with declared output:

```yaml
steps:
  - id: build
    run: |
      printf 'image_tag=v1.2.3\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: image_tag
```

Use another shell explicitly:

```yaml
steps:
  - id: bash_example
    run: bash -lc 'printf "%s\n" "$BASH_VERSION"'
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

- A black-box fixture verifies `dagu validate` accepts a step with a simple `run` string.
- A black-box fixture verifies `dagu validate` accepts a multi-line `run` string.
- A black-box fixture verifies `dagu validate` rejects a non-string `run`.
- A black-box fixture verifies `dagu validate` rejects an empty `run`.
- A black-box fixture verifies `dagu validate` accepts `$()` text in `run` and does not execute it.
- A black-box fixture verifies `dagu validate` accepts backtick text in `run` and does not execute it.
- A black-box fixture verifies `dagu validate` does not execute `run`.
- A black-box fixture verifies `dagu validate` does not require the command path to exist.
- A black-box fixture verifies `dagu run` accepts a discovered project workflow target.
- A black-box fixture verifies `dagu run` does not append `.yaml` or `.yml` to an extensionless workflow target.
- A black-box fixture verifies `dagu run` does not fall back from an unknown project workflow target to any other filesystem path.
- A black-box fixture verifies `dagu run` executes a simple `run` command.
- A black-box fixture verifies `dagu run` executes from `project_root` by default.
- A black-box fixture verifies `dagu run` respects root `working_dir`.
- A black-box fixture verifies `dagu run` resolves Dagu references before command execution.
- A black-box fixture verifies `$NAME` remains available for shell expansion.
- A black-box fixture verifies command stdout is captured.
- A black-box fixture verifies command stderr is captured.
- A black-box fixture verifies stdout is not treated as a step output.
- A black-box fixture verifies non-zero command exit fails the step.
- A black-box fixture verifies a command path that the shell cannot find fails the step.
- A black-box fixture verifies `DAGU_OUTPUT_FILE` is set when the step declares outputs.
- A black-box fixture verifies `DAGU_OUTPUT_FILE` is absent when the step does not declare outputs.
- A black-box fixture verifies abort terminates the running command.
- A black-box fixture verifies timeout termination fails the running command.
