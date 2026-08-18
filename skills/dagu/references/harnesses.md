# External CLI Harnesses

Use `action: harness.run` to invoke external coding agents from DAG steps. Dagu selects the configured provider; its executable must be installed on the host or available in the selected container.

## Supported Providers

| Provider | Binary | Invocation | Supplementary input |
|----------|--------|------------|---------------------|
| `aider` | `aider` | `aider --message "<prompt>" [flags]` | Folded into the prompt |
| `amp` | `amp` | `amp -x "<prompt>" [flags]` | Piped to stdin |
| `claude` | `claude` | `claude -p "<prompt>" [flags]` | Piped to stdin |
| `cline` | `cline` | `cline [flags] "<prompt>"` | Piped to stdin |
| `codex` | `codex` | `codex exec "<prompt>" [flags]` | Piped to stdin |
| `copilot` | `copilot` | `copilot -p "<prompt>" [flags]` | Piped to stdin |
| `cursor` | `cursor-agent` | `cursor-agent -p "<prompt>" [flags]` | Folded into the prompt |
| `deepseek` | `dsh` | `dsh --profile headless [flags] "<prompt>"` | Folded into the prompt |
| `droid` | `droid` | `droid exec "<prompt>" [flags]` | Folded into the prompt |
| `gemini` | `gemini` | `gemini -p "<prompt>" [flags]` | Piped to stdin |
| `goose` | `goose` | `goose run --text "<prompt>" [flags]` | Folded into the prompt |
| `kiro` | `kiro-cli` | `kiro-cli chat --no-interactive "<prompt>" [flags]` | Piped to stdin |
| `opencode` | `opencode` | Managed session or `opencode run "<prompt>" [flags]` | Piped to stdin on the CLI path |
| `pi` | `pi` | `pi -p "<prompt>" [flags]` | Piped to stdin |
| `qwen` | `qwen` | `qwen -p "<prompt>" [flags]` | Piped to stdin |

Codex defaults to `skip_git_repo_check: true`, so its default invocation includes `--skip-git-repo-check`. Set `skip_git_repo_check: false` or `skip-git-repo-check: false` to omit it.

Cursor defaults to `output_format: text`, and Goose defaults to `quiet: true`. Explicit values in `with` override these defaults.

The `deepseek` adapter targets the official DeepSeek Harness `dsh` CLI and its headless profile. It does not select DeepSeek as the model backend for another harness.

Claude's `bare` flag skips keychain reads among other things, so a subscription login is invisible to the step and the run fails with `Not logged in`. Set it only when the credential comes from `ANTHROPIC_API_KEY` in the step environment.

For host subprocess runs, built-in provider adapters resolve binaries through `PATH`. Host custom harnesses can use a binary name or an explicit path; relative paths with a path separator are resolved from the step working directory. For containerized runs, the binary is executed inside the selected container and must be valid there.

## Feature Reference

- `with.prompt` is required and is passed to the selected provider according to its built-in adapter or custom harness definition. Multiline prompt text is preserved.
- `with.stdin` is optional supplementary input for host subprocess runs. Each built-in adapter either pipes it to stdin or folds it into the prompt as shown above. Containerized harness runs reject it.
- Built-in provider adapters are `aider`, `amp`, `claude`, `cline`, `codex`, `copilot`, `cursor`, `deepseek`, `droid`, `gemini`, `goose`, `kiro`, `opencode`, `pi`, and `qwen`. Non-reserved `with` keys become CLI flags.
- Custom providers must be declared under top-level `harnesses:`. A non-null custom definition shadows a built-in provider with the same name; deleting the custom definition exposes the built-in again.
- `fallback` is an ordered list of provider configs. Dagu tries the next config only when the previous attempt fails and the run context is still active. Fallback configs cannot contain another `fallback`.
- `provider` may use value references only if they resolve to a concrete provider string before executor creation. If `${...}` remains unresolved at runtime, the harness fails with an unresolved provider template error.
- `provider` and `fallback` are harness control keys. They are not passed as CLI flags.

### Managed OpenCode Sessions

Built-in `provider: opencode` steps use a managed OpenCode server session by default when they run from a standalone Dagu server, from `dagu start-all`, or on a distributed worker. The run page shows the agent timeline and lets an operator answer permission requests and questions. Each managed step has its own conversation by default; runs with multiple managed steps provide a step selector in the Agent tab. A waiting answer suspends the step durably; Dagu resumes the same OpenCode session on the host that owns it. The final assistant text becomes step stdout.

Managed mode starts one process-local OpenCode server under the Dagu service identity. Install a compatible `opencode` executable for that service and authenticate it with `opencode auth login` (normally stored in `~/.local/share/opencode/auth.json`) or pass selected provider credentials through the service configuration:

```yaml
opencode:
  executable: opencode
  env_passthrough:
    - OPENAI_API_KEY
```

The equivalent environment variables are `DAGU_OPENCODE_EXECUTABLE` and `DAGU_OPENCODE_ENV_PASSTHROUGH`. A DAG-level `tools:` or `secrets:` declaration affects step subprocesses and supplies only the CLI integration, not the long-lived managed server.

Managed mode forces OpenCode sharing off and does not support `share`; explicit sharing uses the CLI integration and may publish the conversation at a public URL.

Managed options are `agent`, `model`, `variant`, `session`, `fork`, `title`, `file`, `command`, and `format: default`. Set `managed: false` to force the one-shot CLI. Options outside that set, `share`, non-default formats, containers, standalone scheduler launches, embedded-engine runs, and direct Dagu CLI execution use the CLI integration. Set `managed: true` to require managed mode and fail clearly when the current execution topology or provider capabilities cannot support it. Automatic mode may fall back only before a managed session is created.

“Allow for this Dagu session” applies only provider-proposed wildcard patterns to the current Dagu session generation; Dagu never grants OpenCode's process-wide `always` permission. Dagu-created and forked sessions are retained until the DAG run is cleaned up, which also removes their provider resources. Sessions supplied with `session:` remain externally owned and are retained.

If the owning worker or OpenCode server disappears, the step remains waiting and the run page offers a clean-session restart. A clean restart discards the conversation and pending interaction, then submits the original prompt to a new session. A retry after a terminal session error also submits the original prompt in a new session generation. Previous Dagu-owned sessions remain available until DAG-run cleanup, and files already changed in the shared workspace are not reverted.

OpenCode persists managed conversations in the Dagu service user's OpenCode data directory; run `opencode debug paths` as that user to locate it. Dagu stores the session reference and UI timeline with the DAG run. If OpenCode's session data is lost, Dagu remains running and marks the affected step unavailable. Use **Start clean session** to create a new conversation and submit the original prompt again.

The managed server is a shared service-identity boundary for trusted workflows. Use separate Dagu workers or containerized CLI execution when workflows require isolation from one another. Managed compatibility is capability-checked at startup; OpenCode v1.18.11 is the currently tested release.

## How `with` Works

Harness supports built-in provider adapters and named custom harness definitions:

- `with.provider` selects a built-in provider adapter or a custom `harnesses:` entry
- top-level `harnesses.<name>` defines how to invoke a custom harness CLI

For built-in provider adapters and custom providers, non-reserved `with` keys are passed directly as CLI flags:

- `key: "value"` → `--key value`
- `key: true` → `--key`
- `key: false` → omitted
- `key: 123` → `--key 123`
- Arrays repeat the flag once per item
- Built-in provider adapters also normalize `snake_case` keys to kebab-case flags, so `max_turns` becomes `--max-turns`

Reserved keys are `prompt`, `stdin`, `provider`, `fallback`, and `managed`.

## Custom Harness Registry

Define reusable custom harness adapters once at the DAG level:

```yaml
harnesses:
  gemini-custom:
    binary: gemini
    prompt_mode: flag
    prompt_flag: --prompt
    option_flags:
      model: --model

steps:
  - id: review
    action: harness.run
    with:
      prompt: "Review the current branch"
      provider: gemini-custom
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
- Step-level `with.fallback: []` disables inherited fallback
- New DAGs should use `action: harness.run`. Legacy step-level `type: harness` remains loadable for backward compatibility.
- A top-level `harness:` config only supplies defaults. It does not set the type of a step that does not name one, so shell `run:` steps can sit alongside harness steps in the same DAG.
- A step that named its prompt with a bare `command:`, relying on the removed inference, is rejected while loading. Port it to `action: harness.run` with `with.prompt`, and `with.stdin` where it used `script:`.

## Containerized Harness Steps

`container:` is optional for `harness.run`. It can be defined at the DAG root or on a specific harness step.

Use root-level `container:` when all compatible steps should run in the same DAG-level container. Image-mode root containers create a shared container for the run; `container.exec` uses an existing container. A harness step without its own `container:` executes the provider CLI inside that shared container.

```yaml
container:
  image: my-codex-runner:latest
  pull_policy: always
  working_dir: /workspace
  volumes:
    - .:/workspace:rw

steps:
  - id: fix_tests
    action: harness.run
    with:
      provider: codex
      prompt: "Fix the failing tests in this repository"
      sandbox: workspace-write
      skip-git-repo-check: true
    timeout_sec: 600
```

Use step-level `container:` when only that harness step needs a container, or when it needs a different container from the DAG-level one. If both root-level and step-level containers are present, the step-level container is used for that step.

```yaml
steps:
  - id: fix_tests
    action: harness.run
    container:
      image: my-codex-runner:latest
      pull_policy: always
      working_dir: /workspace
      volumes:
        - .:/workspace:rw
    with:
      provider: codex
      prompt: "Fix the failing tests in this repository"
      sandbox: workspace-write
      skip-git-repo-check: true
    timeout_sec: 600
```

Container rules:

- The selected provider binary must exist inside the container that runs the step.
- Built-in provider adapters and custom providers with `prompt_mode: arg` or `prompt_mode: flag` can run in a container.
- `with.stdin` is not supported in a container. Step-level container plus `with.stdin` is rejected during DAG validation; root shared-container stdin is rejected when the harness step runs.
- Custom providers with `prompt_mode: stdin` are not supported in a container.
- A step-level image-mode container creates a container for the step. Dagu uses the provider binary as the container entrypoint and passes provider arguments as the command.
- Do not set `container.name` for image-mode harness steps. Use `container.exec` when the step must execute inside an existing container.
- For a root-level container, Dagu executes the full provider command inside the shared DAG-level container.
- A step-level container inherits user-defined runtime values such as DAG env, step env, params, secrets, and step outputs. The engine process environment is not injected.
- A root-level shared-container harness env filters host-path runtime values such as `PWD`, DAG run log paths, artifact paths, and step stream paths. Do not rely on those host paths inside the shared container.
- Step-level `container.env` overrides inherited values with the same key.
- DAG resource limits are applied to created step-level harness containers and created DAG-level containers when the container runtime supports those limits. Existing-container exec mode cannot change that container's host resources.
- Provider flags still belong under `with:`. For example, Codex `sandbox: workspace-write` configures Codex inside the outer container boundary.
- Docker or Podman is selected by the Dagu service process. This is not configured in the DAG YAML.

## Pattern 1: Checkout a Repository and Run a Harness

```yaml
name: review-repository
type: graph

params:
  - REPOSITORY: https://github.com/dagucloud/dagu.git
  - REF: main
  - PROMPT: "Review this repository and identify the highest-impact improvement."

steps:
  - id: checkout
    action: git.checkout
    with:
      repository: "${params.REPOSITORY}"
      ref: "${params.REF}"
      path: "${context.paths.work_dir}/repo"

  - id: review
    depends: [checkout]
    working_dir: "${context.paths.work_dir}/repo"
    action: harness.run
    with:
      provider: opencode
      model: openrouter/deepseek/deepseek-v4-flash
      prompt: "${params.PROMPT}"
    timeout_sec: 600
```

`git.checkout` clones the repository when the target is absent and refreshes an existing checkout otherwise. The per-run work directory gives concurrent DAG runs separate checkouts, keeps the workspace available for retries, and removes it when the DAG run is cleaned up.

## Pattern 2: Multiple Harness Steps

Chain harness steps that invoke external CLIs, passing output between steps via env-scope `output:` variables or `${step_id.stdout}` file references.

```yaml
type: graph

params:
  - topic: ""

steps:
  - id: research
    action: harness.run
    with:
      prompt: "Research every approach to: ${params.topic}. List all approaches with pros, cons, and when to use each."
      provider: claude
      model: sonnet
    output: RESEARCH

  - id: review
    action: harness.run
    with:
      prompt: "Review the research provided on stdin for completeness and gaps"
      # Interpolated before execution, then piped to the harness CLI on stdin.
      stdin: |
        Review this research for completeness and gaps:

        ${env.RESEARCH}
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
        ${env.RESEARCH}

        === Review Feedback ===
        ${env.REVIEW}
      provider: claude
      model: sonnet
    depends: [review]
    output: REFINED
```

`with.prompt` is the prompt. For host subprocess runs, built-in adapters handle `with.stdin` according to the provider table. Custom `arg`/`flag` harnesses receive it on stdin. For custom `stdin` harnesses, stdin receives the prompt, then a blank line, then `with.stdin` when both are present.

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
      prompt: "${params.PROMPT}"
      provider: "${params.PROVIDER}"
      model: "${params.MODEL}"
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
      model: openrouter/deepseek/deepseek-v4-flash
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

### DeepSeek Harness

```yaml
steps:
  - id: task
    action: harness.run
    with:
      prompt: "Review the current branch and fix the failing tests"
      provider: deepseek
    timeout_sec: 300
```

## Notes

1. **Model names** — Look up current model names from each provider's documentation. Do not rely on hardcoded names; they change frequently.
2. **Prompt as a parameter** — Expose the prompt via `params:` so users can customize from UI/CLI without editing the DAG.
3. **Timeouts** — Set `timeout_sec:` (300-600s+) on harness steps. External CLI providers can run for minutes.
4. **Retry on transient failures** — Add `retry_policy: { limit: 3, interval_sec: 30 }` to handle rate limits and network errors.
5. **Working directory** — Use `working_dir:` on the step. The CLI operates relative to this directory.
6. **Output capture** — Use string-form `output: VAR_NAME` for small flat values, declared `outputs:` for explicit `${steps.<step_id>.outputs.<name>}` values, object-form `output:` for structured `${step_id.output.*}` access, and `stdout.artifact` / `stderr.artifact` when large provider output, reports, JSON, Markdown, or logs should be stored as DAG-run artifacts. Use `${step_id.stdout}` only when a downstream step needs the stdout log file path.
7. **Exit codes** — `0` means success. Provider process or container failures keep the provider's exit code when available; setup and internal errors use `1`; timeout and cancellation paths use `124`.
8. **Failure output** — Recent stderr is included in the error message on failure. When failed stdout exists, Dagu also includes a recent stdout tail and writes that tail to stderr.
9. **Fallback behavior** — If the primary harness config fails and the context is still active, fallback entries are tried in order. Failed-attempt stdout is not emitted to step stdout; its recent tail may be written to stderr and included in failure diagnostics. Stderr from failed attempts remains visible in logs.
