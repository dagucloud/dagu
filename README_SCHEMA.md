# Dagu Workflow Schema at a Glance

This is the repository-level overview of Dagu's workflow YAML schema. It is
meant to help a visitor understand how a workflow is shaped, what the current
canonical syntax is, and where to look when they need the full field-level
reference.

Dagu workflows are DAGs described in YAML. The current schema has a simple
execution split:

- `run:` for local shell commands and scripts
- `action:` for named builtin or custom actions
- `actions:` for reusable custom action definitions

The older v1 execution fields (`command:`, `script:`, step-level `type:`,
`call:`, and `step_types:`) are still loadable for compatibility, but new
workflows should use the syntax below.

## Minimal Workflow

```yaml
name: release-check
type: graph

params:
  ENVIRONMENT: staging

steps:
  - id: test
    run: go test ./...

  - id: health
    action: http.request
    with:
      method: GET
      url: https://example.com/health
    depends: [test]

  - id: deploy
    run: ./deploy.sh ${ENVIRONMENT}
    depends: [health]
    retry_policy:
      limit: 3
      interval_sec: 10
```

The root `type:` controls how the workflow executes:

- `chain` runs steps in order. This is the default.
- `graph` runs steps according to `depends:` and can run independent steps in
  parallel.
- `agent` is reserved for agent-oriented execution.

Do not confuse root `type:` with legacy step-level `type:`. Step-level
`type:` is deprecated; use `action:` for named executors.

## Mental Model

A Dagu file has three layers:

| Layer | Fields | Purpose |
|-------|--------|---------|
| Workflow metadata | `name`, `description`, `group`, `labels` | Identify and organize the DAG. |
| Workflow runtime | `schedule`, `params`, `env`, `working_dir`, `queue`, `worker_selector`, `timeout_sec`, `max_active_runs`, `artifacts`, `log_output` | Configure when and where the DAG runs. |
| Step graph | `steps`, `handler_on`, `defaults`, `actions` | Define executable work, shared step defaults, lifecycle handlers, and custom actions. |

Most workflows only need `name`, `type`, `params`, and `steps`.

## Step Shape

Every step has common workflow-control fields plus one execution field.

```yaml
steps:
  - id: step_id
    run: echo hello
    depends: [previous_step]
    env:
      - LOG_LEVEL: info
    timeout_sec: 300
    retry_policy:
      limit: 2
      interval_sec: 5
    output: RESULT
```

Common step fields:

| Field | Meaning |
|-------|---------|
| `id` | Stable identifier for dependencies and output references. Prefer this on every step. |
| `name` | Optional display name. |
| `description` | Step description. |
| `depends` | Step dependency or dependency list. |
| `env` | Step environment variables. |
| `working_dir` | Step working directory. |
| `timeout_sec` | Step timeout in seconds. |
| `retry_policy` | Retry behavior for this step. |
| `repeat_policy` | Repeat or polling behavior. |
| `continue_on` | Continue after selected failure, skip, exit-code, or output conditions. |
| `preconditions` | Conditions that must pass before the step starts. |
| `worker_selector` | Required worker labels. |
| `stdout`, `stderr`, `log_output` | Step log output configuration. |
| `output` | Captured stdout variable or structured step output. |
| `output_schema` | JSON Schema for stdout JSON validation. |
| `approval` | Human approval gate after step execution. |
| `container` | Container context for a local `run:` command. |

## `run:` for Local Commands

Use `run:` for local shell commands and scripts.

```yaml
steps:
  - id: hello
    run: echo hello
```

Multi-line `run:` becomes a script:

```yaml
steps:
  - id: build
    run: |
      set -e
      npm ci
      npm test
```

`run:` can also be an array of commands:

```yaml
steps:
  - id: build_and_test
    run:
      - go build ./...
      - go test ./...
```

Only shell settings are accepted in `with:` for a `run:` step:

```yaml
steps:
  - id: bash_step
    run: echo "$SHELL"
    with:
      shell: bash -e
      shell_args: [-c]
      shell_packages: [curl]
```

Important: object-form `run:` is not part of the current schema. This is
invalid:

```yaml
steps:
  - run:
      type: shell
      input: echo hello
```

## `action:` for Named Execution

Use `action:` when the step is not just a local shell command.

```yaml
steps:
  - id: create_job
    action: http.request
    with:
      method: POST
      url: https://api.example.com/jobs
      body: '{"queue":"default"}'
```

`action:` names select builtin or custom actions. Inputs and executor-specific
options go under `with:`.

Current builtin actions:

| Action | Use for | Key inputs |
|--------|---------|------------|
| `http.request` | HTTP calls | `method`, `url`, headers/body config |
| `ssh.run` | Remote shell over SSH | `command`, SSH connection config |
| `exec` | Direct process execution without shell parsing | `command`, optional `args` |
| `docker.run` | Docker executor | optional `command`, Docker config |
| `container.run` | Container executor | optional `command`, container config |
| `k8s.run`, `kubernetes.run` | Kubernetes job execution | optional `command`, Kubernetes config |
| `postgres.query`, `sqlite.query` | SQL queries | `query`, database config |
| `postgres.import`, `sqlite.import` | SQL imports | `import`, database config |
| `redis.<operation>` | Redis operations | Redis config; operation comes from the action suffix |
| `jq.filter` | jq transforms | `filter`, plus `data` or `input` |
| `dag.run` | Child DAG execution | `dag`, optional `params` |
| `router.route` | Conditional routing | `value`, `routes` |
| `chat.completion` | LLM chat completion | `prompt` or `messages`, model config |
| `agent.run` | Agent step execution | `task`, `prompt`, or `messages`, agent config |
| `harness.run` | CLI coding-agent harnesses | `prompt`, provider config, optional `stdin` |
| `template.render` | Text/template rendering | `template`, optional data/config |
| `log.write` | Log messages | `message` |
| `mail.send` | Email sending | mail executor config |
| `archive.create`, `archive.extract`, `archive.list` | Archive operations | archive config |
| `s3.upload`, `s3.download`, `s3.list`, `s3.delete` | S3 operations | S3 config |
| `sftp.upload`, `sftp.download` | SFTP transfers | SFTP config |
| `noop` | Output-only or approval-only placeholder step | no `with`, or empty `with` |

`run:` and `action:` are mutually exclusive on a step. Do not combine either
with legacy execution fields such as `command:`, `script:`, step-level `type:`,
`call:`, `messages:`, `agent:`, `llm:`, `value:`, or `routes:`.

## Common Action Examples

### SQL Query

```yaml
steps:
  - id: active_users
    action: postgres.query
    with:
      dsn: ${DATABASE_URL}
      query: SELECT id, email FROM users WHERE active = true
```

### Child DAG

```yaml
steps:
  - id: process_account
    action: dag.run
    with:
      dag: workflows/process-account
      params:
        ACCOUNT_ID: acct_123
        REGION: us-east-1
```

`parallel:` currently requires `action: dag.run`:

```yaml
steps:
  - id: fanout
    action: dag.run
    with:
      dag: workflows/process-account
      params:
        ACCOUNT_ID: ${ITEM.account_id}
        REGION: ${ITEM.region}
    parallel:
      max_concurrent: 3
      items:
        - account_id: acct_1
          region: us-east-1
        - account_id: acct_2
          region: eu-west-1
```

### Agent Harness

```yaml
harnesses:
  codex-cli:
    binary: codex
    prefix_args: [exec]
    prompt_mode: arg

steps:
  - id: review
    action: harness.run
    with:
      provider: codex-cli
      prompt: Review the current branch and list actionable issues.
```

## Reusable Custom Actions

Use top-level `actions:` to define a validated reusable action.

```yaml
actions:
  release.announce:
    description: Print a release announcement
    input_schema:
      type: object
      additionalProperties: false
      required: [channel, version]
      properties:
        channel:
          type: string
          enum: [changelog, slack]
        version:
          type: string
    template:
      run: echo {{ json .input.channel }} {{ json .input.version }}

steps:
  - id: announce
    action: release.announce
    with:
      channel: changelog
      version: v1.2.3
```

Custom action rules:

- The action name must match
  `^[A-Za-z][A-Za-z0-9_-]*(\.[A-Za-z][A-Za-z0-9_-]*)*$`.
- `input_schema` is required and must resolve to an object schema.
- `template` is required and must contain exactly one of `run` or `action`.
- `type:` is not supported inside an `actions:` definition.
- Legacy execution fields are rejected inside action templates.
- `with:` at the call site is validated against `input_schema`.
- Custom actions can call other custom actions; recursive references are
  rejected.

Legacy `step_types:` remains loadable, but it is deprecated. Use `actions:` for
new reusable definitions.

## Outputs

String-form `output:` captures trimmed stdout into a flat variable:

```yaml
steps:
  - id: version
    run: git rev-parse --short HEAD
    output: VERSION

  - id: publish
    run: echo "Publishing ${VERSION}"
```

Object-form `output:` publishes structured step output:

```yaml
steps:
  - id: inspect
    run: echo '{"version":"v1.2.3","artifact":{"url":"https://example.test/app.tgz"}}'
    output:
      version:
        from: stdout
        decode: json
        select: .version
      artifact:
        from: stdout
        decode: json
        select: .artifact

  - id: publish
    depends: [inspect]
    run: echo "${inspect.output.version} ${inspect.output.artifact.url}"
```

Structured output sources are `stdout`, `stderr`, and `file`. Decoders are
`text`, `json`, and `yaml`. `select` requires `json` or `yaml`.

## Lifecycle Handlers

Use `handler_on` for lifecycle steps:

```yaml
handler_on:
  failure:
    action: log.write
    with:
      message: Workflow failed
  exit:
    run: ./cleanup.sh

steps:
  - id: main
    run: ./run.sh
```

Handlers use the same current step execution syntax as normal steps.

## Validation and Source of Truth

Use these commands when editing workflow YAML:

```sh
dagu validate workflow.yaml
dagu schema dag
dagu schema dag steps
```

Implementation-level references:

- `internal/cmn/schema/dag.schema.json` - generated JSON Schema used by editor
  tooling and schema navigation
- `internal/core/spec/step_v2.go` - `run:` and `action:` normalization
- `internal/core/spec/step_types.go` - custom `actions:` and legacy
  `step_types:` handling
- `internal/core/spec/deprecation.go` - deprecated v1 syntax warnings
- [`SCHEMA_MIGRATION.md`](./SCHEMA_MIGRATION.md) - migration notes from v1
  syntax to the current schema
