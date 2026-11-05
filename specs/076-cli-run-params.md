# Spec 076: CLI Run Parameter Input

## Status

Implemented for local `dagu start` and `dagu enqueue`.

## Scope

This spec owns parameter-source selection and stdin parameter input before a
local run is started or enqueued.

It does not define parameter declaration schema, general runtime-parameter
grammar or type conversion, remote contexts, HTTP APIs, scheduler behavior,
queue processing, or history output formats.

[Spec 005: Value Resolution Params](005-value-resolution-params.md) owns
`${params}` and `${params.name}` after runtime input has been resolved.

## Goal

Callers can explicitly supply parameters through stdin without losing inherited
input intended for a shell loop or another command. Source precedence, value
boundaries, and input failures have predictable results.

## Behavior

### Source Selection

Both commands select one parameter source, in this order:

1. Arguments after an explicit `--` separator.
2. An explicitly supplied `--params` flag.
3. Piped or redirected stdin when `--params-stdin` is enabled.
4. Workflow defaults when no override is supplied.

Rules:

- A bare `--` selects an empty override. It suppresses `--params` and stdin.
- `--params=` selects an empty override. It suppresses stdin.
- An empty override retains workflow defaults; it does not supply an empty
  string value for a parameter.
- `--params-stdin` defaults to false. Omitting it or setting it to false leaves
  stdin unread, including input inherited inside a `while read` loop.
- A higher-precedence source leaves stdin uninspected and unread even when
  `--params-stdin` is enabled. An open pipe cannot block that invocation, and
  oversized or unreadable stdin cannot make it fail.
- Enabling `--params-stdin` with a terminal or other character device does not
  read or prompt for input. With no higher-precedence source, defaults apply.

### Reading Stdin

- Selected piped or redirected input is read through EOF. Read boundaries do
  not separate parameters or end the input; fragmented bytes, including UTF-8
  and escape sequences, produce the same values as contiguous input.
- The maximum selected input size is 1,048,576 bytes, including leading and
  trailing whitespace. Input at that limit is not rejected for size; larger
  input must fail.
- The limit applies before whitespace is removed. Rejection of larger input
  does not require waiting for EOF.
- Leading and trailing whitespace outside the parameter text is removed.
- Empty or whitespace-only input supplies no override and retains defaults.

### Supported Stdin Values

The following forms must be accepted for declared string parameters:

- A positional string can override the first declared parameter.
- Named `name=value` assignments supply values by parameter name.
- A JSON object supplies named values; a JSON array supplies positional values.
  A JSON string supplies its text. Any other JSON value supplies its JSON text
  with each number as written, so `98765432109876543` and `1.10` are not
  rounded.
- Double quotes delimit a single string containing whitespace. Whitespace
  inside those quotes is preserved, including leading and trailing spaces.
- An equals sign inside a double-quoted positional value is literal text,
  including text that resembles named assignments or contains escaped quotes.
- A quoted `""` is an explicit empty string and overrides a default.
- Quoted strings decode `\"`, `\\`, `\n`, and `\t` as a quote, backslash, newline,
  and tab. `\\n` preserves a literal backslash followed by `n`.
- Quoted strings may contain literal newlines. JSON strings use JSON escaping.
- Literal newlines and escape sequences may appear in the same quoted value;
  escape decoding must still preserve escaped quotes at either end.
- Unicode text is preserved. Parameter input does not evaluate shell variable
  references or command substitutions contained in a supplied value.

These rules specify accepted input forms, not the behavior of malformed
parameter text or undeclared parameter names.

### Quoted Value Compatibility

Quoted parameter text in YAML defaults, explicit `--params`, and selected stdin
shares value decoding. Existing shell line continuations must retain their
backslashes and literal newlines without requiring `--params-stdin`.

A quoted value containing an odd run of backslashes immediately before a
literal newline follows legacy literal handling for the entire value. Those
backslash-newline sequences are preserved; backslash pairs and `\n` or `\t`
sequences elsewhere in the value remain literal. When no odd run is present,
even runs decode backslash pairs and preserve the newline under the supported
quoted-value rules above.

### Saved Runs

`start --from-run-id` restores saved parameters. Enabling `--params-stdin` with
`--from-run-id` must fail even if stdin is empty, is still open, or would be
ignored by a higher-precedence parameter source. Disabling `--params-stdin`
does not create this conflict.

## Errors and Side Effects

- If selected stdin cannot be inspected or read, the command must exit nonzero
  and identify the stdin inspection or read failure on stderr.
- If selected stdin exceeds the byte limit, the command must exit nonzero and
  identify the size limit on stderr.
- The saved-run flag conflict must exit nonzero and identify the incompatible
  parameter input on stderr.
- These errors must occur before run admission: no new run or queue entry is
  created, and no workflow step executes.
- Resolving valid input supplies the decoded values to the started or enqueued
  run. This spec does not prescribe their history display serialization.

## Examples

Save this workflow as `params_stdin.yaml`:

```yaml
params:
  - name: value
    type: string
    default: default
working_dir: .
steps:
  - id: write
    run: printf '%s\n' "$value" > params_stdin.out
```

```sh
printf '%s\n' '" hello world "' | dagu start --params-stdin params_stdin.yaml
printf '%s\n' '""' | dagu start --params-stdin params_stdin.yaml
printf '%s\n' '{"value":"line1\nline2"}' | dagu enqueue --params-stdin params_stdin.yaml
printf '%s\n' 'value=stdin' | dagu start --params-stdin --params= params_stdin.yaml
```

Expected values are ` hello world `, an empty string, `line1` and `line2` separated
by a newline, and `default`, respectively. The last command leaves stdin unread.

## Conformance

`conformance/spec076_cli_run_params/` owns binary-level coverage:

| Contract | Tests |
| --- | --- |
| Supported forms, quoting, escapes, Unicode, empty input and defaults | `TestParamsStdinValues` |
| Shell line continuations in existing YAML defaults and explicit flags | `TestParamsLineContinuation` |
| Source precedence and preservation of inherited bytes | `TestParamsStdinPrecedence` |
| Open, oversized and unreadable unselected input | `TestParamsStdinUnusedInput` |
| Inherited shell-loop input | `TestParamsStdinShellLoop` |
| Fragmented input through EOF | `TestParamsStdinFragments` |
| Read failure before admission | `TestParamsStdinReadError` |
| Byte limit before trimming and rejection without EOF | `TestParamsStdinLimit`, `TestParamsStdinLimitOpenPipe` |
| Character-device input | `TestParamsStdinCharacterDevice` |
| Saved-run conflict independent of available input | `TestParamsStdinFromRunID` |

Stdin inspection failure uses lower-level coverage in `TestRunClosedStdin`
under `internal/cmd/`: an invalid descriptor cannot normally be inherited by
the child binary.

The binary harness does not attach interactive terminals. Terminal exclusion
is represented by character-device selection in `TestStdinHasParamsInput` and
the null-character-device binary tests. These check selection and defaults;
they do not directly exercise an attached terminal.
