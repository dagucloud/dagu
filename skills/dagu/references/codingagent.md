# Coding Agent Integration

Use `action: harness.run` to run AI coding agent CLIs as DAG steps. The harness executor spawns the CLI as a subprocess in non-interactive mode.

## Supported Providers

| Provider | Binary | CLI invocation |
|----------|--------|----------------|
| `claude` | `claude` | `claude -p "<prompt>" [flags]` |
| `codex` | `codex` | `codex exec "<prompt>" [flags]` |
| `copilot` | `copilot` | `copilot -p "<prompt>" [flags]` |
| `opencode` | `opencode` | `opencode run "<prompt>" [flags]` |
| `pi` | `pi` | `pi -p "<prompt>" [flags]` |

The selected attempt's binary must be resolvable when it runs. Built-in providers use `PATH`; custom harnesses can use a binary name or an explicit path resolved from the step working directory.

## How `with` Works

Harness supports built-in providers and named custom harness definitions:

- `with.provider` selects a built-in provider such as `claude`, `codex`, or `copilot`
- top-level `harnesses.<name>` defines how to invoke a custom harness CLI
- `with.provider` can point at either a built-in provider or a custom `harnesses:` entry

All non-reserved `with` keys are passed directly as CLI flags:

- `key: "value"` → `--key value`
- `key: true` → `--key`
- `key: false` → omitted
- `key: 123` → `--key 123`
- built-in providers also normalize `snake_case` keys to kebab-case flags, so `max_turns` becomes `--max-turns`

Reserved keys are `prompt`, `stdin`, `provider`, and `fallback`.

`provider` may be parameterized with `${...}` and is resolved at runtime after interpolation.

## Custom Harness Registry

Define reusable custom harness adapters once at the DAG level:

```yaml
harnesses:
  gemini:
    binary: gemini
    prefix_args: ["run"]
    prompt_mode: flag
    prompt_flag: --prompt
    option_flags:
      model: --model

steps:
  - id: review
    action: harness.run
    with:
      prompt: "Review the current branch"
      provider: gemini
      model: gemini-2.5-pro
```

Custom harness definition fields:

- `binary` — CLI binary or path
- `prefix_args` — args that always appear before prompt placement and runtime flags
- `prompt_mode` — `arg`, `flag`, or `stdin`
- `prompt_flag` — required when `prompt_mode: flag`
- `prompt_position` — `before_flags` or `after_flags`
- `flag_style` — `gnu_long` or `single_dash`
- `option_flags` — per-option override from `with` key to exact flag token

## DAG-Level Defaults and Fallback

Use top-level `harness:` to define shared defaults for every harness step in the DAG.

```yaml
harness:
  provider: claude
  model: sonnet
  bare: true
  fallback:
    - provider: codex
      full-auto: true
    - provider: copilot
      yolo: true
      silent: true

steps:
  - id: step1
    action: harness.run
    with:
      prompt: "Write tests"

  - id: step2
    action: harness.run
    with:
      prompt: "Fix bugs"
      model: opus
      effort: high

  - id: step3
    action: harness.run
    with:
      prompt: "Generate docs"
      provider: copilot
      fallback:
        - provider: claude
          model: haiku
```

Merge rules:

- DAG-level primary harness config is the base
- Step-level `with` overlays it
- Step-level `with.fallback` replaces DAG-level `fallback`
- New DAGs should use `action: harness.run`; legacy `type: harness` inference exists only for backward compatibility.

## Pattern 1: Single Agent Step

```yaml
params:
  - PROMPT: "Explain the main function in this project"

harness:
  provider: claude
  model: sonnet
  bare: true

steps:
  - id: run_agent
    action: harness.run
    with:
      prompt: "${PROMPT}"
    output: RESULT
```

## Pattern 2: Multi-Agent Pipeline

Chain agents, passing output between steps via `output:` variables or `${step_id.stdout}` file references.

```yaml
type: graph

params:
  - topic: ""

steps:
  - id: research
    action: harness.run
    with:
      prompt: "Research every approach to: ${topic}. List all approaches with pros, cons, and when to use each."
      provider: claude
      model: sonnet
      bare: true
    output: RESEARCH

  - id: review
    action: harness.run
    with:
      prompt: "Review the research provided on stdin for completeness and gaps"
      # Interpolated before execution, then piped to the harness CLI on stdin.
      stdin: |
        Review this research for completeness and gaps:

        ${RESEARCH}
      provider: codex
      full-auto: true
      skip-git-repo-check: true
    depends: [research]
    output: REVIEW

  - id: refine
    action: harness.run
    with:
      prompt: "Refine this research incorporating the review feedback provided via stdin."
      stdin: |
        === Research ===
        ${RESEARCH}

        === Review Feedback ===
        ${REVIEW}
      provider: claude
      model: sonnet
      bare: true
    depends: [review]
    output: REFINED
```

`with.prompt` is the prompt. For built-in providers and custom `arg`/`flag` harnesses, `with.stdin` is piped to stdin as supplementary context. For custom `stdin` harnesses, stdin receives the prompt, then a blank line, then `with.stdin` when both are present.

## Pattern 3: Parameterized

```yaml
params:
  - PROVIDER: claude
  - MODEL: sonnet
  - PROMPT: "Analyze this codebase"

steps:
  - id: agent
    action: harness.run
    with:
      prompt: "${PROMPT}"
      provider: "${PROVIDER}"
      model: "${MODEL}"
    output: RESULT
```

## Provider Examples

### Claude Code

```yaml
steps:
  - id: task
    action: harness.run
    with:
      prompt: "Write tests for the auth module"
      provider: claude
      model: sonnet
      effort: high
      max-turns: 20
      max-budget-usd: 2.00
      permission-mode: auto
      allowed-tools: "Bash,Read,Edit"
      bare: true
    timeout_sec: 300
    output: RESULT
```

### Codex

```yaml
steps:
  - id: task
    action: harness.run
    with:
      prompt: "Fix failing tests in src/"
      provider: codex
      full-auto: true
      sandbox: workspace-write
      ephemeral: true
      skip-git-repo-check: true
    timeout_sec: 300
```

### Copilot

```yaml
steps:
  - id: task
    action: harness.run
    with:
      prompt: "Refactor the authentication middleware"
      provider: copilot
      autopilot: true
      yolo: true
      silent: true
      no-ask-user: true
      no-auto-update: true
    timeout_sec: 300
```

### OpenCode

```yaml
steps:
  - id: task
    action: harness.run
    with:
      prompt: "Refactor the database layer"
      provider: opencode
      format: json
    timeout_sec: 300
```

### Pi

```yaml
steps:
  - id: task
    action: harness.run
    with:
      prompt: "Design a rate limiting middleware"
      provider: pi
      thinking: high
      tools: read,bash
    timeout_sec: 300
```

## Notes

1. **Model names** — Look up current model names from each provider's documentation. Do not rely on hardcoded names; they change frequently.
2. **Prompt as a parameter** — Expose the prompt via `params:` so users can customize from UI/CLI without editing the DAG.
3. **Timeouts** — Set `timeout_sec:` (300-600s+) on agent steps. Agent CLIs can run for minutes.
4. **Retry on transient failures** — Add `retry_policy: { limit: 3, interval_sec: 30 }` to handle rate limits and network errors.
5. **Working directory** — Use `working_dir:` on the step. The CLI operates relative to this directory.
6. **Output capture** — Use string-form `output: VAR_NAME` for small flat values, object-form `output:` for structured `${step_id.output.*}` access, and `stdout.artifact` / `stderr.artifact` when large agent output, reports, JSON, Markdown, or logs should be stored as DAG-run artifacts. Use `${step_id.stdout}` only when a downstream step needs the stdout log file path.
7. **Exit codes** — 0 = success, 1 = CLI error, 124 = step timed out. Last 1KB of stderr is included in the error message on failure.
8. **Fallback behavior** — If the primary harness config fails and the context is still active, fallback entries are tried in order. Failed-attempt stdout is discarded; stderr remains visible in logs.
