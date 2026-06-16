# Spec: Step Run Command

## Implementation Status

Partially implemented. This spec defines command-form `run` behavior.

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

Workflow authors can run a single shell command with predictable shell handoff,
shell-specific behavior, and sequential behavior for array-form `run`.

## Behavior

### Command Text

- Command-form `run` is shell command text.

- Command-form `run` must not contain line breaks.

- Dagu resolves Dagu-owned references before the command starts.

- Value resolution must not reclassify command form as script form.

- If resolved command-form text contains a line break, the step fails before the selected shell starts.

- Dagu passes the resolved command text to the selected shell as one command string.

- Dagu must not split command-form `run` into a user-authored argv list.

- Shell syntax in the command text is owned by the selected shell.

- Command path lookup is performed by the selected shell or operating system from the step process working directory.

### Shell Operators

- Command-form `run` is passed to the selected shell as one command string, so shell operators inside the command text remain inside that shell invocation.

- Dagu accepts shell operator text in command-form `run`, including pipes (`|`), redirects (`>`, `>>`, `<`, `2>`), command chaining (`&&`, `||`, `;`), background execution (`&`), grouping syntax, and shell-specific control syntax.

- The selected shell defines which operator forms are valid. For example, `sh`, Bash, PowerShell, and `cmd` do not share one operator grammar.

- Dagu must not split command-form `run` at shell operators.

- Dagu must not convert shell command chaining inside one command-form entry into array-form `run` entries.

- Array-form sequencing is Dagu-owned; shell operators inside an array-form entry are shell-owned for that entry.

### Array Form

- Array-form `run` is an ordered list of command-form entries.

- Each non-empty array entry runs as one command-form invocation.

- Array-form entries must not contain line breaks.

- If a resolved array-form entry contains a line break, the step fails before the selected shell starts.

- Entries run sequentially.

- Execution stops at the first failed entry.

- Later entries must not start after an earlier entry fails.

### Shell Construction

#### Common Rules

- Dagu constructs the shell command from the selected shell, configured shell arguments, command text, and configured shell packages.

- Shell-specific defaults are part of the command-form contract and must be covered by black-box tests before being changed.

#### Unix-Like Shells

- Unix-like shell execution includes `-c`; if configured shell arguments already include `-c`, that existing flag is used.

- Unix-like shell execution includes `-e` when the selected shell is Unix-like, no step-level shell is explicitly specified, and configured shell arguments do not already include `-e`.

#### PowerShell

- PowerShell execution includes `-NoProfile` and `-NonInteractive` unless configured shell arguments already include them.

- PowerShell execution includes `-Command` unless configured shell arguments already include `-Command` or `-C`.

#### Windows cmd

- `cmd` execution includes `/c` unless configured shell arguments already include `/c` or `/C`.

- `cmd` resolves the configured `COMSPEC` path when the selected shell is `cmd` or `cmd.exe` and `COMSPEC` points to an existing path.

#### Nix Shell

- Nix shell execution adds `-p <package>` for each configured shell package.

- Nix shell execution includes `--pure` unless configured shell arguments already include `--pure` or `--impure`.

- Nix shell execution includes `--run` unless configured shell arguments already include it.

## Errors

Runtime must fail when:

- The selected shell cannot be started.

- The command cannot be started by the selected shell.

- The selected shell cannot find the command path.

- The resolved command text contains a line break.

- The command exits with a non-zero code.

- An array-form entry fails.

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

Custom shell:

```yaml
steps:
  - id: bash_example
    run: printf "%s\n" "$BASH_VERSION"
    with:
      shell: bash
      shell_args: [-e, -o, pipefail]
```

## Acceptance Criteria

- A black-box fixture verifies `dagu run` executes a single-line `run` command.
- A black-box fixture verifies shell syntax in command-form `run` is interpreted by the selected shell.
- A black-box fixture verifies command-form `run` is not split at shell operators.
- A black-box fixture verifies command-form `run` uses `with.shell` and `with.shell_args`.
- A black-box fixture verifies command-form `run` is not exposed as a user-authored argv list.
- A black-box fixture verifies command-form `run` rejects line breaks in array-form entries.
- A black-box fixture verifies command-form `run` fails before shell start when value resolution inserts a line break.
- A black-box fixture verifies array-form `run` fails before shell start when value resolution inserts a line break.
- A black-box fixture verifies array-form `run` executes entries sequentially.
- A black-box fixture verifies array-form `run` stops at the first failed entry.
- A black-box fixture verifies Unix-like shell command construction uses the expected shell command-string invocation.
- A black-box fixture verifies PowerShell command construction uses the expected non-interactive command invocation.
- A black-box fixture verifies `cmd` command construction uses the expected command-string invocation.
- A black-box fixture verifies non-zero command exit fails the step.
- A black-box fixture verifies a command path that the shell cannot find fails the step.
