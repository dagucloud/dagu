# Dagu Workflow Schema v1

This document describes the legacy Dagu workflow schema: step execution is
defined with `command:`, `script:`, `exec:`, step-level `type:`, `call:`, and
top-level `step_types:`.

The current code still loads this syntax for compatibility, but new workflows
should prefer the v2 syntax documented in
[`README_SCHEMA_V2.md`](./README_SCHEMA_V2.md).

## Status

- v1 syntax is loadable for existing DAGs.
- v1 execution fields are deprecated by the current validator.
- `dagu validate` can emit deprecation warnings while still accepting valid v1
  files.
- This document intentionally does not use v2 `run:`, `action:`, or `actions:`
  examples.

## Minimal Example

```yaml
name: daily-report
type: graph

schedule:
  - "0 6 * * *"

steps:
  - id: extract
    command: ./extract.sh

  - id: transform
    command: ./transform.sh
    depends: [extract]

  - id: publish
    command: ./publish.sh
    depends: [transform]
```

Root-level `type:` and step-level `type:` are different fields:

- root `type:` controls DAG execution order: `chain`, `graph`, or `agent`
- step `type:` selects an executor such as `http`, `ssh`, `postgres`, or a
  custom legacy `step_types` name

## Root Fields

Common root fields are shared by v1 and v2.

| Field | Meaning |
|-------|---------|
| `name` | DAG name. Defaults to the YAML filename when omitted. |
| `description` | Human-readable DAG description. |
| `group` | UI grouping label. |
| `type` | DAG execution mode: `chain` by default, `graph` for dependency-driven execution. |
| `schedule` | Cron or typed schedule entries. |
| `working_dir` | Default working directory for the DAG. |
| `dotenv` | `.env` file path or paths. |
| `env` | DAG-level environment variables. |
| `params` | Start parameters and defaults. |
| `steps` | Step list or map. |
| `handler_on` | Handler steps for `init`, `success`, `failure`, `abort`, `exit`, or `wait`. |
| `step_types` | Deprecated reusable v1 step definitions. |
| `defaults` | Default step fields inherited by steps and handlers. |
| `container` | DAG-level container execution context. |
| `ssh`, `redis`, `s3`, `llm`, `harness`, `kubernetes` | DAG-level executor defaults. |
| `queue`, `worker_selector` | Queue and worker routing. |
| `max_active_runs`, `max_active_steps` | DAG concurrency limits. |
| `timeout_sec`, `delay_sec`, `restart_wait_sec`, `max_clean_up_time_sec` | Run lifecycle timing. |
| `retry_policy` | DAG-level retry policy for failed DAG runs. |
| `artifacts` | DAG run artifact storage settings. |
| `log_dir`, `log_output`, `max_output_size` | Log and output behavior. |
| `mail_on`, `smtp`, `error_mail`, `info_mail`, `wait_mail` | Email settings. |
| `labels`, `tags` | Labels. `tags` is legacy. |
| `preconditions` | DAG-level preconditions. |
| `webhook`, `run_config`, `secrets`, `registry_auths`, `otel` | Trigger, interaction, secret, registry, and tracing settings. |

Use `dagu schema dag` to inspect the generated schema for every root field.

## Steps

`steps` can be a list:

```yaml
steps:
  - id: build
    command: go build ./...
  - id: test
    command: go test ./...
    depends: [build]
```

or a map:

```yaml
steps:
  build:
    command: go build ./...
  test:
    command: go test ./...
    depends: [build]
```

Common step fields:

| Field | Meaning |
|-------|---------|
| `id` | Stable identifier for dependencies and output references. |
| `name` | Display name. If omitted, Dagu generates one. |
| `description` | Step description. |
| `depends` | Dependency name, list, or empty list for explicit no dependencies. |
| `env` | Step environment variables. |
| `working_dir` | Step working directory. |
| `timeout_sec` | Step timeout in seconds. |
| `retry_policy` | Retry behavior for the step. |
| `repeat_policy` | Repeat behavior for polling-style steps. |
| `continue_on` | Continue after `failed`, `skipped`, matching exit codes, or outputs. |
| `preconditions` | Step-level preconditions. |
| `worker_selector` | Required worker labels. |
| `mail_on_error` | Send email on step error. |
| `signal_on_stop` | Signal sent when the step is stopped. |
| `stdout`, `stderr`, `log_output` | Output log file behavior. |
| `output` | Captured stdout variable or structured step output map. |
| `output_schema` | JSON Schema object for stdout JSON validation. |
| `approval` | Human approval gate after the step completes. |
| `container` | Step-level container context. |

## Local Commands

If no executor `type:` is set, a step with `command:` runs as a local command.

```yaml
steps:
  - id: hello
    command: echo hello
```

`command:` accepts a string or an array. An array is treated as multiple
commands executed sequentially by the command executor.

```yaml
steps:
  - id: build_and_test
    command:
      - go build ./...
      - go test ./...
```

A multi-line `command:` string is treated as a script by the loader.

```yaml
steps:
  - id: script_from_command
    command: |
      set -e
      echo "first"
      echo "second"
```

`script:` is the explicit v1 script field.

```yaml
steps:
  - id: script
    script: |
      #!/usr/bin/env bash
      set -euo pipefail
      echo "script body"
```

`exec:` runs a program directly without shell parsing. It cannot be combined
with `command:`, `script:`, `shell:`, or `shell_packages:`.

```yaml
steps:
  - id: direct
    exec:
      command: /usr/bin/python3
      args:
        - -u
        - scripts/job.py
        - --limit
        - 10
```

Shell controls are v1 step fields:

```yaml
steps:
  - id: bash_step
    command: echo "$SHELL"
    shell: bash -e
    shell_args:
      - -c
    shell_packages:
      - curl
```

`shell` can be a string such as `bash -e` or an array such as
`["/bin/bash", "-e", "-u"]`.

## Built-in Step Executors

Step-level `type:` selects a named executor. Executor-specific options go in
`with:`. The older `config:` alias is still accepted in many places, but `with:`
is the preferred v1 spelling.

```yaml
steps:
  - id: request
    type: http
    with:
      method: GET
      url: https://example.com/health
```

Accepted builtin executor names include:

| Step `type:` | Primary v1 fields |
|--------------|-------------------|
| omitted, `command`, `shell` | `command`, `script`, `exec`, `shell`, `shell_args`, `shell_packages` |
| `http` | `with.method`, `with.url`, optional headers/body and other HTTP config |
| `ssh` | `command`, `with.host`, `with.user`, key/password config |
| `docker`, `container` | `command`, `with.image` or container config |
| `k8s`, `kubernetes` | `command`, Kubernetes job config in `with` |
| `postgres`, `sqlite` | `command` as SQL query, SQL config in `with` |
| `redis` | Redis command/config in `with`; DAG-level `redis` defaults can be inherited |
| `jq` | `command` as jq filter, optional `script` or `with.input` data source |
| `mail` | Mail config in `with` |
| `s3` | S3 operation/config in `command` and `with` |
| `sftp` | SFTP transfer config in `with` |
| `archive` | Archive operation in `command`, config in `with` |
| `log` | Log message config in `with` |
| `router` | `value` and `routes` |
| `chat` | `messages` and `llm` |
| `agent` | `messages` and `agent` |
| `harness` | `command` as prompt, harness config in `with` |
| `template` | `script` as template text, data/config in `with` |
| `noop` | no executable input required |

Executor capabilities are validated. For example, only executors that support
commands can use `command:`, and only LLM/agent executors can use `messages:`.

## Executor Examples

### HTTP

```yaml
steps:
  - id: call_api
    type: http
    with:
      method: POST
      url: https://api.example.com/jobs
      headers:
        Content-Type: application/json
      body: '{"mode":"daily"}'
```

### SSH

```yaml
steps:
  - id: deploy
    type: ssh
    command: cd /srv/app && git pull && systemctl restart app
    with:
      host: prod.example.com
      user: deploy
      key: ~/.ssh/id_rsa
```

### PostgreSQL

```yaml
steps:
  - id: query_users
    type: postgres
    command: SELECT id, email FROM users WHERE active = true
    with:
      dsn: ${DATABASE_URL}
```

### SQLite

```yaml
steps:
  - id: query_local
    type: sqlite
    command: SELECT count(*) FROM events
    with:
      file: ./data/app.db
```

### Docker

```yaml
steps:
  - id: build
    type: docker
    command: npm test
    with:
      image: node:20-alpine
      volumes:
        - .:/workspace
      working_dir: /workspace
```

### jq

```yaml
steps:
  - id: pick_name
    type: jq
    command: .name
    script: '{"name":"Alice"}'
```

### Router

```yaml
type: graph
steps:
  - id: route
    type: router
    value: ${STATUS}
    routes:
      success: [deploy]
      failed: [notify]

  - id: deploy
    command: ./deploy.sh

  - id: notify
    command: ./notify.sh
```

### Chat

```yaml
llm:
  provider: openai
  model: gpt-4.1

steps:
  - id: summarize
    type: chat
    messages:
      - role: user
        content: Summarize ${REPORT_PATH}
```

### Agent

```yaml
steps:
  - id: investigate
    type: agent
    messages:
      - role: user
        content: Inspect the failed run and write findings.
    agent:
      model: claude-sonnet
      safe_mode: true
```

### Harness

```yaml
harnesses:
  codex-cli:
    binary: codex
    prefix_args: [exec]
    prompt_mode: arg

steps:
  - id: review
    type: harness
    command: Review the current branch and list actionable issues.
    with:
      provider: codex-cli
```

### Template

```yaml
steps:
  - id: render
    type: template
    with:
      data:
        name: Alice
    script: |
      Hello, {{ .name }}!
    stdout: ${DAG_RUN_ARTIFACTS_DIR}/hello.txt
```

## Sub-DAGs

Use `call:` to run another DAG.

```yaml
steps:
  - id: child
    call: workflows/process-account
    params:
      ACCOUNT_ID: acct_123
      REGION: us-east-1
```

When `parallel:` is present, the step must run a child DAG. Dagu exposes the
current item as `ITEM`.

```yaml
steps:
  - id: fanout
    call: workflows/process-account
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

`parallel:` can be:

- a variable reference such as `${ITEMS}`
- a static array
- an object with `items` and `max_concurrent`

## Handlers

`handler_on` entries use the same v1 step execution fields.

```yaml
handler_on:
  failure:
    command: ./notify-failure.sh
  exit:
    command: ./cleanup.sh

steps:
  - id: main
    command: ./run.sh
```

## Outputs

String-form `output:` captures trimmed stdout into a flat variable.

```yaml
steps:
  - id: version
    command: git rev-parse --short HEAD
    output: VERSION

  - id: publish
    command: echo "Publishing ${VERSION}"
```

Object-form `output:` publishes structured step output.

```yaml
steps:
  - id: inspect
    command: echo '{"version":"v1.2.3","artifact":{"url":"https://example.test/app.tgz"}}'
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
    command: echo "${inspect.output.version} ${inspect.output.artifact.url}"
    depends: [inspect]
```

Valid structured output sources are `stdout`, `stderr`, and `file`. Valid
decoders are `text`, `json`, and `yaml`. `select` requires `json` or `yaml`.

## Legacy `step_types`

`step_types` defines reusable v1 execution templates.

```yaml
step_types:
  greet:
    type: command
    description: Print a greeting
    input_schema:
      type: object
      additionalProperties: false
      required: [message]
      properties:
        message:
          type: string
    template:
      command: echo {{ json .input.message }}

steps:
  - id: say_hello
    type: greet
    with:
      message: hello
```

Rules:

- `step_types.<name>.type` is required and must resolve to a builtin executor
  type.
- `input_schema` is required and must resolve to an object schema.
- `template` is required.
- `template.type` is not allowed; the target executor is declared in
  `step_types.<name>.type`.
- `with:` or `config:` at the call site supplies validated input.
- `with:` and `config:` cannot both be present.
- Call sites cannot add execution fields such as `command`, `script`, `exec`,
  `call`, `parallel`, `container`, `llm`, `messages`, `agent`, `value`, or
  `routes`.
- `env` and `preconditions` compose from defaults, template, and call site.
- Template strings use Go `text/template` with `.input` plus hermetic template
  helpers, including `json`.
- `{$input: path.to.value}` injects a typed input value without stringifying it.

`step_types` can be declared in a DAG file or inherited from base config.
Duplicate names across scopes are rejected.

## Deprecated but Loadable

The following v1 execution fields are deprecated in current Dagu, but remain
loadable for compatibility:

```text
agent, call, command, config, exec, llm, messages, params, routes, script,
shell, shell_args, shell_packages, type, value, step_types
```

Do not mix `with:` and `config:` on the same step. Use `with:` when maintaining
v1 files.

## v1 to v2 Pointers

The most important migration rules are:

- `command:` and `script:` -> `run:`
- local shell `shell`, `shell_args`, `shell_packages` -> `run:` with `with:`
- step-level `type:` -> `action:`
- `call:` and child `params:` -> `action: dag.run`
- `step_types:` -> `actions:`

See [`SCHEMA_V2_MIGRATION.md`](./SCHEMA_V2_MIGRATION.md) for the full migration
guide.
