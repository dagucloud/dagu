# Spec: Step Run Command

## Status

Implemented. This spec defines command-form `run` behavior.

## Scope

This spec defines command-form `run`.

Command form is:

- A single-line string `run`.
- Each non-empty entry in array-form `run`.

Shared `run` behavior is defined by [Spec 013: Step Run](013-step-run.md).
Script-form `run` is defined by [Spec 015: Step Run Script](015-step-run-script.md).

This spec does not define:

- Multi-line string `run`.
- Direct argv execution through `action: exec`.
- Non-local executor behavior.

## Goal

Workflow authors can run shell command text with predictable shell handoff,
shell-specific behavior, and sequential behavior for array-form `run`.

## Behavior

### Command Text

- Command-form `run` is shell command text.

- Command-form `run` must not contain line breaks.

- Dagu resolves Dagu-owned references before the command starts.

- Value resolution must not reclassify command form as script form.

- If resolved command-form text contains a line break, the step fails before the selected shell starts.

- Dagu treats the resolved command text as the command payload for the selected
  shell.

- Shell-specific rules below define how Dagu gives that payload to the selected
  shell's command-string carrier.

- Dagu must not split command-form `run` into a user-authored argv list.

- Any command parsing, normalization, or display metadata produced by Dagu must not change the resolved command text payload.

- Shell syntax in the command text is owned by the selected shell.

- Dagu starts the selected shell with the step process working directory.

- Relative paths in command text are interpreted by the selected shell or operating system from that working directory.

- Bare command-name lookup is owned by the selected shell or operating system.

- Dagu must not add the step process working directory to command lookup rules.

### Shell Operators

- Command-form `run` is passed to the selected shell as one command string, so shell operators inside the command text remain inside that shell invocation.

- Dagu accepts shell operator text in command-form `run`, including pipes (`|`), redirects (`>`, `>>`, `<`, `2>`), command chaining (`&&`, `||`, `;`), background execution (`&`), grouping syntax, and shell-specific control syntax.

- The selected shell defines which operator forms are valid. For example, `sh`, Bash, PowerShell, and `cmd` do not share one operator grammar.

- Dagu must not split command-form `run` at shell operators.

- Dagu must not convert shell command chaining inside one command-form entry into array-form `run` entries.

- Array-form sequencing is Dagu-owned; shell operators inside an array-form entry are shell-owned for that entry.

- Dagu determines command-form success or failure from the selected shell
  process result, not from individual shell operators inside the command text.

- Pipeline, list, conditional, grouping, and subshell exit behavior is owned by
  the selected shell and its configured options.

- Background execution syntax is owned by the selected shell.

- Dagu determines the command-form result from the selected shell process, not
  from detached background work that outlives that shell process.

- A command that starts background work must wait for that work itself when the
  step result should include that work.

- A command must also wait for background work when stdout, stderr,
  `DAGU_OUTPUT_FILE` records, artifacts, or filesystem side effects from that
  work must be included deterministically in the step attempt.

- A workflow must not rely on detached background work that outlives the selected
  shell process to contribute deterministic stdout, stderr, `DAGU_OUTPUT_FILE`
  records, artifacts, or side effects.

### Array Form

- Array-form `run` is an ordered list of command-form entries.

- Each non-empty array entry runs as one command-form invocation.

- Array-form entries must not contain line breaks.

- If a resolved array-form entry contains a line break, the step fails before the selected shell starts.

- Entries run sequentially.

- Each started entry uses its own selected-shell invocation.

- Shell process state does not carry from one array entry to the next.

- State created outside the shell process, such as files written in the working
  directory, may be observed by later entries according to the operating system
  and selected shell behavior.

- Each entry starts with the same step process working directory selected for
  the step, unless external state changes make that directory unusable before a
  later entry starts.

- Each entry receives the step attempt environment assembled before command
  execution starts.

- Environment changes made inside one entry do not change the environment Dagu
  supplies to later entries.

- A workflow that needs shared shell state across multiple lines must use
  script-form `run`.

- Dagu captures stdout and stderr from started entries as the step attempt's
  stdout and stderr streams in entry order.

- A runtime feature that consumes captured stdout or stderr for the step attempt
  observes the aggregate captured stream from all started entries.

- The aggregate captured stream is deterministic only for work completed before
  each entry's selected shell process exits.

- All entries in one step attempt share the same `DAGU_OUTPUT_FILE` for that
  attempt.

- Dagu parses `DAGU_OUTPUT_FILE` only after all array entries complete
  successfully.

- `DAGU_OUTPUT_FILE` records are deterministic only for records written before
  the selected shell process for the final successful entry exits.

- If any array entry fails, aborts, or times out, the step attempt publishes no
  outputs from `DAGU_OUTPUT_FILE`.

- Execution stops at the first failed entry.

- Later entries must not start after an earlier entry fails.

- The step attempt result is the first failed entry result, or success when every
  non-empty entry succeeds.

### Diagnostics And Secrecy

- Dagu-generated diagnostics in normal step errors, run logs, status records,
  history records, workflow events, and DAG-run detail responses must not expose
  secret-backed values inserted into resolved command text by Dagu value
  resolution.

- Dagu-generated diagnostics may include authored command text, command text
  with secret-backed values masked, or a summarized command description.

- An explicit inspection or debug surface may show resolved command text only
  when workflow authors opt in to that surface.

- Any explicit inspection or debug surface that shows resolved command text must
  apply secret masking owned by the environment and secret specs.

- User command stdout and stderr are user-controlled output. Dagu captures them
  as defined by the shared `run` spec.

### Shell Construction

#### Common Rules

- Dagu constructs the shell invocation from the selected shell, configured shell
  arguments, command payload, and configured shell packages.

- Dagu supplies one command-string payload to the selected shell's command-string carrier.

- Unless a shell-specific rule below defines generated shell-owned wrapping, the
  command-string payload is the resolved command text.

- Generated shell-owned wrapping must preserve the resolved command text as the
  user-authored command payload and must not split it into a user-authored argv
  list.

- Configured shell arguments may provide shell options.

- Configured shell arguments must not provide a separate command-string operand.

- If configured shell arguments include the selected shell's command-string flag, that flag must be the final configured shell argument.

- If a configured command-string flag is followed by another configured shell argument, the step fails before the selected shell starts.

- Command-form shell invocation order is:

  1. Selected shell.
  2. Configured shell arguments before any configured command-string flag.
  3. Dagu-added shell options and shell package arguments.
  4. Command-string flag.
  5. One command-string payload.

- If configured shell arguments include the command-string flag, Dagu must insert Dagu-added shell options and shell package arguments before that configured flag.

- Configured shell packages are valid only when the selected shell family
  defines package behavior.

- If configured shell packages are present and the selected shell family does not
  define package behavior, the step fails before the selected shell starts.

- Shell-specific defaults are part of the command-form contract and must be covered by black-box tests before being changed.

- Shell family matching uses the normalized name of the selected shell.

- The normalized shell name is the selected shell executable basename after path components and an optional `.exe` suffix are removed.

- Shell family matching is ASCII case-insensitive.

#### Unix-Like Shells

- For this spec, Unix-like shells have normalized names `sh`, `bash`, `zsh`, `ksh`, `ash`, or `dash`.

- Unix-like shell execution includes `-c`; if configured shell arguments already include `-c`, that existing flag identifies where Dagu supplies the command-string operand.

- The only valid Unix-like command-string carrier token is exactly `-c`.

- A Unix-like configured shell argument that embeds a command string in the
  carrier token, such as `-cecho hello`, is invalid.

- A Unix-like configured shell argument that combines `c` with other options,
  such as `-lc`, is invalid and the step fails before the selected shell starts.

- Use separate shell arguments for other shell options. For example, use `[-l]`
  when login-shell behavior is needed; Dagu adds or uses `-c` separately.

- Unix-like shell execution includes `-e` when the selected shell is Unix-like, no step-level shell is explicitly specified, and configured shell arguments do not already include `-e`.

#### PowerShell

- PowerShell rules apply to normalized shell names `powershell` and `pwsh`.

- PowerShell execution includes `-NoProfile` and `-NonInteractive` unless configured shell arguments already include them.

- PowerShell execution includes `-Command` unless configured shell arguments already include `-Command` or `-C`; an existing command-string flag identifies where Dagu supplies the command-string operand.

- PowerShell command-string carrier tokens are `-Command` and `-C`.

- PowerShell command-string flags are matched ASCII case-insensitively.

- A configured PowerShell command-string carrier with an inline value, such as
  `-Command:Write-Host ok`, is invalid.

- PowerShell `-EncodedCommand` is invalid for command-form `run`.

- PowerShell command execution normalizes PowerShell error handling and UTF-8
  text encoding before user command text runs.

- Under PowerShell normalization, a PowerShell error written by `Write-Error`
  fails the step unless the command handles the error.

- Dagu determines PowerShell command success from the PowerShell process exit
  code.

- Native executable error semantics inside the command remain PowerShell-owned
  unless Dagu's PowerShell normalization explicitly changes them.

#### Windows cmd

- Windows `cmd` rules apply to normalized shell name `cmd`.

- `cmd` execution includes `/c` unless configured shell arguments already include `/c` or `/C`; an existing command flag identifies where Dagu supplies the command-string operand.

- `cmd` command-string carrier tokens are `/c` and `/C`.

- A configured `cmd` command-string carrier with an inline command body, such as
  `/cecho hello`, is invalid.

- `cmd` resolves the configured `COMSPEC` path when the selected shell is `cmd` or `cmd.exe` and `COMSPEC` points to an existing path.

#### Nix Shell

- Nix shell rules apply to normalized shell name `nix-shell`.

- Nix shell execution adds `-p <package>` for each configured shell package.

- Nix shell execution includes `--pure` unless configured shell arguments already include `--pure` or `--impure`.

- Nix shell execution includes `--run` unless configured shell arguments already include it; an existing `--run` identifies where Dagu supplies the command-string operand.

- The only valid Nix shell command-string carrier token is exactly `--run`.

- A configured Nix shell command-string carrier with an inline command body, such
  as `--run=echo hello`, is invalid.

- Nix shell command execution includes fail-fast behavior when no step-level
  `with.shell` is explicitly specified.

- Under Nix shell fail-fast behavior, `echo before; false; echo after` must not
  execute `echo after` unless the command handles the failing command.

#### Other Shells

- Shells not covered by Unix-like, PowerShell, Windows `cmd`, or Nix shell rules use `-c` as the command-string flag.

- For other shells, the only valid command-string carrier token is exactly `-c`.

- For other shells, a configured command-string carrier with an inline command
  body, such as `-cecho hello`, is invalid.

- Dagu does not add shell-specific default options for other shells.

- If another shell does not support `-c` command-string execution, the step fails according to the normal runtime error rules.

## Errors

Runtime must fail when:

- The selected shell cannot be started.

- The command cannot be started by the selected shell.

- The selected shell cannot find the command path.

- The resolved command text contains a line break.

- The command exits with a non-zero code.

- An array-form entry fails.

- Configured shell arguments provide a separate command-string operand or use an
  invalid command-string carrier.

- Configured shell packages are present for a shell family that does not define
  package behavior.

## Examples

Single command:

```yaml
steps:
  - id: hello
    run: echo hello
```

Shell command syntax remains shell-owned:

```yaml
steps:
  - id: list
    run: find . -name "*.go" | sort
```

Array form:

```yaml
steps:
  - id: checks
    run:
      - go test ./internal/core/...
      - go test ./internal/runtime/...
```

Array entries do not share shell state:

```yaml
steps:
  - id: build_ui
    run:
      - cd ui && pnpm install
      - cd ui && pnpm build
```

Use script form when shell state must carry across lines:

```yaml
steps:
  - id: build_ui
    run: |
      cd ui
      pnpm install
      pnpm build
```

Wait for background work that belongs to the command result:

```yaml
steps:
  - id: background
    run: './long-task.sh & task_pid=$!; wait "$task_pid"'
```

Custom shell:

```yaml
steps:
  - id: bash_example
    run: printf "%s\n" "$BASH_VERSION"
    with:
      shell: bash
      shell_args: [-e, -o, pipefail]
```
