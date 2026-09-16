# Spec: Harness Executor

## Status

Partially implemented.

## Scope

Unlike specs 060-066, `harness` is a built-in Go executor, not a remote
action: it ships in the `dagu` binary and never reaches the network to
resolve itself. Its purpose is to run a third-party AI coding agent CLI
(for example `claude`, `codex`, `aider`, `cursor`, or a custom CLI) as a
subprocess, feeding it a prompt and capturing its output like an ordinary
command step.

This spec covers:

- the `action: harness.run` step shape: `with.prompt` (required, becomes
  the step's command/prompt text), `with.stdin` (optional, piped to the
  process's stdin), `with.provider` (required, selects a built-in CLI
  provider or a custom `harnesses:` entry), and `with.fallback` (an
  ordered list of alternative provider configs tried after the primary
  fails)
- any other `with:` key becomes a CLI flag passed to the provider's
  binary: a boolean `true` becomes a bare flag, a string or number becomes
  `--key value`, and an array repeats `--key` once per element
- the top-level `harnesses:` block, which defines a named, reusable custom
  provider (`binary`, `prefix_args`, `prompt_mode`, `prompt_flag`,
  `prompt_position`, `flag_style`, `option_flags`) that a step selects via
  `with.provider`, for a CLI the built-in provider catalog does not name
- capturing stdout through a declared `output: NAME` and reading it in a
  downstream step ([Spec 012: Step Outputs](012-step-outputs.md))
- that, unlike the remote actions in specs 060-066, `dagu validate` fully
  resolves and checks a harness step's provider configuration and any
  `harnesses:` definitions it references, since both are local to the DAG
  file and this binary -- the one thing it cannot check is whether the
  configured binary actually exists, since that is a real filesystem/PATH
  lookup deferred to run time
- fallback behavior: when the primary provider's process exits non-zero,
  the step retries with the next entry in `with.fallback`, in order, until
  one succeeds or the list is exhausted; a message naming both providers is
  written to the step's stderr before each retry

This spec also covers the configuration and dispatch failures of
containerized harness execution (a step-level `container:` block on a
`harness.run` step) that are reachable without a working container daemon
or a pulled image -- neither guaranteed in every environment this suite
runs in, so a step's *successful* containerized execution is not itself
covered here.

This spec does not define:

- the behavior of any specific built-in CLI provider (`claude`, `codex`,
  `aider`, and so on) -- none is installed in this conformance
  environment, so this spec exercises only custom `harnesses:` providers,
  which share the same invocation-building and fallback code paths
- a containerized harness step's successful execution (the actual agent CLI
  running inside a real container and producing output), the managed
  OpenCode execution path's successful session, or `type: agent` DAGs' own
  use of a harness step as one of several actions in a decision loop
  ([Spec 032: Agent DAGs](032-agent-dag.md))
- prompt engineering, model behavior, or the semantics of any particular
  agent CLI's own flags

## Goal

Workflow authors give an AI coding agent CLI a task and capture its output
as an ordinary Dagu step, whether that CLI is one of the built-in provider
names or an arbitrary custom CLI wired in through `harnesses:`.

## Behavior

### Step shape and invocation building

A harness step is written `action: harness.run` with `with.prompt`
required; `with.prompt` becomes the step's command text (equivalent to a
`run:` step's command), and `with.stdin`, when set, is piped to the
process's stdin as a separate script body. `with.provider` selects either
a built-in CLI provider name or a name defined under the DAG's top-level
`harnesses:` block; an unknown name fails with `unknown provider`.

For a custom (`harnesses:`-defined) provider, the invocation is built as:

1. `prefix_args`, if any.
2. The prompt and the flags built from the step's other `with:` keys, in
   the order `prompt_position` says (`before_flags`, the default, or
   `after_flags`), with the prompt delivered according to `prompt_mode`:
   - `arg`: the prompt is a bare positional argument.
   - `flag`: the prompt follows `prompt_flag` (required only for this
     mode; setting it for any other mode is itself an error).
   - `stdin`: no prompt argument at all; the prompt and `with.stdin` are
     joined with a blank line (either half omitted if empty) and both
     piped to the process's stdin instead.
3. `with.stdin`, when set and `prompt_mode` is not `stdin`, is still piped
   to the process's stdin as a separate stream from the argument-delivered
   prompt.

The input keys `prompt` and `stdin`, and executor options `provider`,
`fallback`, and `managed`, do not become CLI flags. Other keys are sorted
by key for deterministic ordering. Each key becomes `--key` (or `-key`
when the definition's `flag_style` is `single_dash`) unless
`option_flags` maps that key to a different literal flag token. A boolean
`true` value produces the bare flag with no value (`false` is omitted
entirely); a string, integer, or float value produces `flag value`; an
array value repeats `flag value` once per element.

### Working-directory warnings

A harness step without an explicitly configured working directory produces a
non-fatal validation warning identifying its DAG and step. This includes steps
inside `foreach` bodies, lifecycle handlers, and local DAG documents.

- For host execution, a step `working_dir`, a root `working_dir` (including an
  inherited base configuration), or a supplied `--default-working-dir` satisfies
  the check. Automatically selected manifest or run directories do not.
- For container execution, the effective container configuration must declare
  `working_dir` to suppress the warning. A step container takes precedence over
  the root container. A host working directory does not set the agent's directory
  inside the container; the check also applies to `container.exec`.
- Relative paths and runtime expressions count as configuration. Validation does
  not check directory existence or change directory resolution or creation.

`dagu validate` prints the warning and exits successfully when there are no
errors. It accepts `--default-working-dir` to validate with the same default used
for execution. The spec API returns warnings separately from errors, and the DAG
editor displays them without blocking saves or execution. Warnings are transient
and do not change retry behavior.

For example, configure the workspace once for host harness steps:

```yaml
working_dir: ./repo
steps:
  - id: review
    action: harness.run
    with:
      provider: claude
      prompt: Review this repository.
```

For a containerized step, set `container.working_dir: /repo` and mount the intended
repository there using the existing container volume configuration.

### Containerized execution

A step-level `container:` block runs the resolved provider's binary inside
an image-created container, using that binary as its entrypoint.
`dagu validate` checks the general harness configuration. The following
container-specific restrictions are checked at runtime:

- `prompt_mode: stdin` fails with `harness: containerized harness does not
  support stdin input`.
- `container.name` is rejected for image-mode harness steps. Running inside
  an existing container uses `container.exec`.
- `provider: opencode` with `managed: true` fails with `harness: managed
  OpenCode is not supported inside containers`.

### Fallback

`with.fallback` is an array of provider configs (each shaped like the
step's own `provider` plus flags), tried in order after the primary
config's process exits non-zero. Before each retry, the step's stderr
receives a line naming both the failed and the next provider (`harness:
attempt <n>/<total> with <name> failed; trying fallback <n+1>/<total> with
<name>`). The step succeeds as a whole the moment any config succeeds, and
that config's stdout becomes the step's stdout; each earlier attempt's own
stderr remains part of the step's stderr. If every config fails, the step
fails with the last config's own error.

### Result

A harness step captures the provider process's stdout and stderr. A declared
`output: NAME` makes stdout available to downstream steps, following
[Spec 012: Step Outputs](012-step-outputs.md).

## Errors

### Validation

`dagu validate` resolves a harness step's provider configuration and any
`harnesses:` definitions fully -- both are local to the DAG file and this
binary, unlike a remote action's schema. It checks:

- `with.prompt` missing: `with.prompt is required`.
- `with.provider` missing, or naming neither a built-in provider nor a
  defined `harnesses:` entry: `config.provider is required` or `unknown
  provider "<name>"`.
- `with.binary` or `with.prompt_args` present: both are rejected outright
  (`config.binary is not supported...` / `config.prompt_args is not
  supported...`) -- a custom CLI must be defined under `harnesses:` and
  selected via `with.provider`, not configured inline on the step.
- A `harnesses:` entry missing `binary`: `binary is required`.
- A `harnesses:` entry's `prompt_mode` not one of `arg`, `flag`, `stdin`:
  `prompt_mode must be one of: arg, flag, stdin`.
- A `harnesses:` entry's `prompt_flag` set when `prompt_mode` is not
  `flag`, or unset when it is: `prompt_flag is only valid when prompt_mode
  is flag` / `prompt_flag is required when prompt_mode is flag`.

`dagu validate` does not check whether the configured binary actually
exists on disk or in `PATH` -- that is deferred to run time.

### Runtime

- The configured binary cannot be resolved (it does not exist, or is not
  on `PATH`): the step fails with `harness: failed to resolve binary
  "<name>": ...` before any process starts.
- The provider's process exits non-zero, with no `with.fallback`
  configured (or every fallback also fails): the step fails with that
  process's own real exit code, and its own stdout/stderr are captured as
  usual -- this is a real process failure, not a wrapper-level validation
  error.
- A containerized step (`container:` set) whose provider's `prompt_mode` is
  `stdin`, whose `container.name` is set (image mode), or whose
  `provider: opencode` step sets `managed: true`: each fails as described
  above in Containerized execution, before any container daemon is
  contacted.
- A containerized step whose configured container daemon cannot be reached
  (wrong socket, daemon not running): the step fails with `harness: failed
  to initialize container client: ...`.

## Related Specs

- Step outputs and reference syntax, for the standard `output: NAME`
  mechanism this executor uses instead of a JSON outputs contract: [Spec
  012: Step Outputs](012-step-outputs.md)
- Agent DAGs, which can use a harness step as one action among several in
  an LLM-driven decision loop: [Spec 032: Agent DAGs](032-agent-dag.md)

## Examples

Define a custom CLI provider and use it with structured flags:

```yaml
harnesses:
  my_agent:
    binary: /usr/local/bin/my-agent
    prompt_mode: flag
    prompt_flag: --task
    option_flags:
      max_turns: --turns

steps:
  - id: review
    action: harness.run
    with:
      provider: my_agent
      prompt: Review this patch for correctness.
      stdin: |
        diff --git a/main.go b/main.go
        ...
      max_turns: 3
    output: REVIEW_RESULT

  - id: print
    depends: review
    run: echo "${REVIEW_RESULT}"
```

Fall back to a second provider if the first fails:

```yaml
steps:
  - id: fix
    action: harness.run
    with:
      provider: claude
      prompt: Fix the failing test.
      fallback:
        - provider: codex
```
