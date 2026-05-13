# Schema v2 Migration Guide

This guide migrates legacy v1 Dagu YAML to the current v2 syntax:

- local shell execution uses `run:`
- named executors use `action: executor.verb`
- reusable definitions use `actions:`

It does not describe the abandoned `run: {type, input, with}` proposal.

## Success Criteria

After migration:

- local shell steps use `run:`
- builtin executor steps use `action:`
- executor inputs live under `with:`
- reusable legacy `step_types:` are replaced with `actions:`
- no step mixes v2 `run:` or `action:` with v1 execution fields
- `dagu validate <file>` succeeds

Root-level fields such as DAG `type: graph`, `schedule`, `params`, `env`,
`defaults`, `handler_on`, and `worker_selector` do not need to change unless
their handler steps still use v1 execution fields.

## What Not To Do

Do not migrate to this stale shape:

```yaml
steps:
  - run:
      type: postgres
      input: SELECT * FROM users
      with:
        dsn: ${DATABASE_URL}
```

The current parser rejects object-form `run:`. Use `action:` instead:

```yaml
steps:
  - action: postgres.query
    with:
      dsn: ${DATABASE_URL}
      query: SELECT * FROM users
```

## Field Mapping

| v1 | v2 |
|----|----|
| `command: echo hello` | `run: echo hello` |
| multi-line `command: |` | `run: |` |
| `script: |` | `run: |` |
| `shell`, `shell_args`, `shell_packages` | move under `with:` on the `run:` step |
| `exec:` | `action: exec` with `with.command` and `with.args` |
| `type: http` | `action: http.request` |
| `type: ssh` + `command` | `action: ssh.run` with `with.command` |
| `type: postgres` + `command` | `action: postgres.query` with `with.query` |
| `type: sqlite` + `command` | `action: sqlite.query` with `with.query` |
| SQL import config | `action: postgres.import` or `action: sqlite.import` |
| `type: jq` + `command` | `action: jq.filter` with `with.filter` |
| `type: docker` | `action: docker.run` |
| `type: container` | `action: container.run` |
| `type: k8s` or `type: kubernetes` | `action: k8s.run` or `action: kubernetes.run` |
| `call:` + `params:` | `action: dag.run` with `with.dag` and `with.params` |
| `parallel:` with `call:` | `parallel:` with `action: dag.run` |
| `type: router`, `value`, `routes` | `action: router.route` with `with.value` and `with.routes` |
| `type: chat`, `messages`, `llm` | `action: chat.completion` with `with.prompt` or `with.messages` plus LLM keys |
| `type: agent`, `messages`, `agent` | `action: agent.run` with `with.task`, `with.prompt`, or `with.messages` plus agent keys |
| `type: harness` + prompt in `command` | `action: harness.run` with `with.prompt` |
| `type: template` + `script` | `action: template.render` with `with.template` |
| `type: log` | `action: log.write` |
| `type: mail` | `action: mail.send` |
| `type: archive` | `action: archive.create`, `archive.extract`, or `archive.list` |
| `type: s3` | `action: s3.upload`, `s3.download`, `s3.list`, or `s3.delete` |
| `type: sftp` | `action: sftp.upload` or `sftp.download` |
| `type: redis` | `action: redis.<operation>` |
| `type: noop` | `action: noop` |
| `step_types:` | `actions:` |
| `config:` | `with:` |

## Local Command Steps

Before:

```yaml
steps:
  - id: test
    command: go test ./...
```

After:

```yaml
steps:
  - id: test
    run: go test ./...
```

Multi-line command/script before:

```yaml
steps:
  - id: build
    script: |
      set -e
      npm ci
      npm test
```

After:

```yaml
steps:
  - id: build
    run: |
      set -e
      npm ci
      npm test
```

Shell settings before:

```yaml
steps:
  - id: bash
    command: echo "$SHELL"
    shell: bash -e
    shell_args: [-c]
```

After:

```yaml
steps:
  - id: bash
    run: echo "$SHELL"
    with:
      shell: bash -e
      shell_args: [-c]
```

Only `shell`, `shell_args`, and `shell_packages` are allowed in `with:` for a
`run:` step.

## Direct Exec

Before:

```yaml
steps:
  - id: direct
    exec:
      command: /usr/bin/python3
      args: [-u, app.py, --limit, 10]
```

After:

```yaml
steps:
  - id: direct
    action: exec
    with:
      command: /usr/bin/python3
      args: [-u, app.py, --limit, 10]
```

## HTTP

Before:

```yaml
steps:
  - id: request
    type: http
    with:
      method: POST
      url: https://api.example.com/jobs
      body: '{"mode":"daily"}'
```

After:

```yaml
steps:
  - id: request
    action: http.request
    with:
      method: POST
      url: https://api.example.com/jobs
      body: '{"mode":"daily"}'
```

## SQL Query

Before:

```yaml
steps:
  - id: query
    type: postgres
    command: SELECT * FROM users
    with:
      dsn: ${DATABASE_URL}
```

After:

```yaml
steps:
  - id: query
    action: postgres.query
    with:
      dsn: ${DATABASE_URL}
      query: SELECT * FROM users
```

For SQLite, use `action: sqlite.query`.

## SSH

Before:

```yaml
steps:
  - id: deploy
    type: ssh
    command: cd /srv/app && git pull && systemctl restart app
    with:
      host: prod.example.com
      user: deploy
```

After:

```yaml
steps:
  - id: deploy
    action: ssh.run
    with:
      host: prod.example.com
      user: deploy
      command: cd /srv/app && git pull && systemctl restart app
```

## Child DAGs

Before:

```yaml
steps:
  - id: child
    call: workflows/process-account
    params:
      ACCOUNT_ID: acct_123
```

After:

```yaml
steps:
  - id: child
    action: dag.run
    with:
      dag: workflows/process-account
      params:
        ACCOUNT_ID: acct_123
```

Parallel before:

```yaml
steps:
  - id: fanout
    call: workflows/process-account
    params:
      ACCOUNT_ID: ${ITEM.account_id}
    parallel:
      items:
        - account_id: acct_1
        - account_id: acct_2
```

Parallel after:

```yaml
steps:
  - id: fanout
    action: dag.run
    with:
      dag: workflows/process-account
      params:
        ACCOUNT_ID: ${ITEM.account_id}
    parallel:
      items:
        - account_id: acct_1
        - account_id: acct_2
```

`parallel:` currently requires `action: dag.run`.

## Router

Before:

```yaml
steps:
  - id: route
    type: router
    value: ${STATUS}
    routes:
      success: [deploy]
      failed: [notify]
```

After:

```yaml
steps:
  - id: route
    action: router.route
    with:
      value: ${STATUS}
      routes:
        success: [deploy]
        failed: [notify]
```

## jq

Before:

```yaml
steps:
  - id: pick_name
    type: jq
    command: .name
    script: '{"name":"Alice"}'
```

After:

```yaml
steps:
  - id: pick_name
    action: jq.filter
    with:
      filter: .name
      data:
        name: Alice
```

Use `with.input` for a file path. Do not set both `with.data` and
`with.input`.

## Harness

Before:

```yaml
steps:
  - id: review
    type: harness
    command: Review the current branch and list actionable issues.
    with:
      provider: codex
```

After:

```yaml
steps:
  - id: review
    action: harness.run
    with:
      provider: codex
      prompt: Review the current branch and list actionable issues.
```

If the v1 harness used `script:` for stdin, use `with.stdin` in v2.

## Chat and Agent

Chat before:

```yaml
steps:
  - id: summarize
    type: chat
    messages:
      - role: user
        content: Summarize ${REPORT_PATH}
    llm:
      provider: openai
      model: gpt-4.1
```

Chat after:

```yaml
steps:
  - id: summarize
    action: chat.completion
    with:
      provider: openai
      model: gpt-4.1
      messages:
        - role: user
          content: Summarize ${REPORT_PATH}
```

Agent before:

```yaml
steps:
  - id: investigate
    type: agent
    messages:
      - role: user
        content: Inspect the failed run.
    agent:
      model: claude-sonnet
      safe_mode: true
```

Agent after:

```yaml
steps:
  - id: investigate
    action: agent.run
    with:
      task: Inspect the failed run.
      model: claude-sonnet
      safe_mode: true
```

## Template

Before:

```yaml
steps:
  - id: render
    type: template
    with:
      data:
        name: Alice
    script: |
      Hello, {{ .name }}!
```

After:

```yaml
steps:
  - id: render
    action: template.render
    with:
      data:
        name: Alice
      template: |
        Hello, {{ .name }}!
```

## `step_types` to `actions`

Before:

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

After:

```yaml
actions:
  greet:
    description: Print a greeting
    input_schema:
      type: object
      additionalProperties: false
      required: [message]
      properties:
        message:
          type: string
    template:
      run: echo {{ json .input.message }}

steps:
  - id: say_hello
    action: greet
    with:
      message: hello
```

Important differences:

- `actions.<name>.type` is not supported.
- `actions.<name>.template` must contain exactly one of `run` or `action`.
- Legacy execution fields are rejected inside action templates.
- Custom action names may contain dots, such as `slack.notify`.
- Custom action names cannot conflict with builtin action names.

## Mixed Syntax to Avoid

Do not leave v1 execution fields beside v2 fields:

```yaml
steps:
  - id: bad
    run: echo hello
    command: echo legacy
```

```yaml
steps:
  - id: bad
    action: http.request
    type: http
    with:
      method: GET
      url: https://example.com
```

The parser rejects these because `run:` and `action:` cannot be used with
legacy execution fields on the same step.

## Suggested Rollout

1. Start with one DAG and keep behavior unchanged.
2. Convert local `command:` and `script:` steps to `run:`.
3. Convert `type:` executor steps to the matching `action:`.
4. Convert `call:` steps to `action: dag.run`.
5. Convert `step_types:` to `actions:`.
6. Run validation.
7. Run the workflow in a staging or low-risk environment.
8. Repeat for the next DAG.

Useful commands:

```sh
dagu validate path/to/workflow.yaml
dagu schema dag
dagu schema dag steps
```

For source-level behavior, check:

- `internal/core/spec/step_v2.go`
- `internal/core/spec/step_v2_test.go`
- `internal/core/spec/step_types.go`
- `internal/cmn/schema/dag.schema.json`
