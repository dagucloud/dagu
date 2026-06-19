# Spec: Step Run Command

## Status

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

- Dagu supplies exactly one resolved command text value as the command-string operand for the selected shell.

- Configured shell arguments may provide shell options.

- Configured shell arguments must not provide a separate command-string operand.

- If configured shell arguments include the selected shell's command-string flag, that flag must be the final configured shell argument.

- If a configured command-string flag is followed by another configured shell argument, the step fails before the selected shell starts.

- Command-form shell invocation order is:

  1. Selected shell.
  2. Configured shell arguments before any configured command-string flag.
  3. Dagu-added shell options and shell package arguments.
  4. Command-string flag.
  5. Exactly one resolved command text operand.

- If configured shell arguments include the command-string flag, Dagu must insert Dagu-added shell options and shell package arguments before that configured flag.

- Shell-specific defaults are part of the command-form contract and must be covered by black-box tests before being changed.

- Shell family matching uses the normalized name of the selected shell.

- The normalized shell name is the selected shell executable basename after path components and an optional `.exe` suffix are removed.

- Shell family matching is ASCII case-insensitive.

#### Unix-Like Shells

- For this spec, Unix-like shells have normalized names `sh`, `bash`, `zsh`, `ksh`, `ash`, or `dash`.

- Unix-like shell execution includes `-c`; if configured shell arguments already include `-c`, that existing flag identifies where Dagu supplies the command-string operand.

- A Unix-like configured shell argument that combines `c` with other options, such as `-lc`, is invalid and the step fails before the selected shell starts.

- Unix-like shell execution includes `-e` when the selected shell is Unix-like, no step-level shell is explicitly specified, and configured shell arguments do not already include `-e`.

#### PowerShell

- PowerShell rules apply to normalized shell names `powershell` and `pwsh`.

- PowerShell execution includes `-NoProfile` and `-NonInteractive` unless configured shell arguments already include them.

- PowerShell execution includes `-Command` unless configured shell arguments already include `-Command` or `-C`; an existing command flag identifies where Dagu supplies the command-string operand.

#### Windows cmd

- Windows `cmd` rules apply to normalized shell name `cmd`.

- `cmd` execution includes `/c` unless configured shell arguments already include `/c` or `/C`; an existing command flag identifies where Dagu supplies the command-string operand.

- `cmd` resolves the configured `COMSPEC` path when the selected shell is `cmd` or `cmd.exe` and `COMSPEC` points to an existing path.

#### Nix Shell

- Nix shell rules apply to normalized shell name `nix-shell`.

- Nix shell execution adds `-p <package>` for each configured shell package.

- Nix shell execution includes `--pure` unless configured shell arguments already include `--pure` or `--impure`.

- Nix shell execution includes `--run` unless configured shell arguments already include it; an existing `--run` identifies where Dagu supplies the command-string operand.

#### Other Shells

- Shells not covered by Unix-like, PowerShell, Windows `cmd`, or Nix shell rules use `-c` as the command-string flag.

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
