# Dagu Workflow Schema v2

This document describes the current canonical Dagu workflow schema as
implemented by the parser and JSON Schema:

- `run:` for local shell commands and scripts
- `action:` for named builtin or custom actions
- `actions:` for reusable custom action definitions

This is not the abandoned `run: {type, input, with}` proposal. Object-form
`run:` is not supported by the current v2 parser.

## Status

- v2 is the canonical syntax for new DAGs.
- v1 execution fields remain loadable for compatibility.
- v1 execution fields are deprecated and cannot be mixed with `run:` or
  `action:` on the same step.
- `dagu validate` can warn on deprecated v1 syntax while accepting valid files.

## Minimal Example

```yaml
name: release-check
type: graph

steps:
  - id: test
    run: go test ./...

  - id: health
    action: http.request
    with:
      method: GET
      url: https://example.com/health
    depends: [test]
```

Root-level `type:` still controls DAG execution mode. It is not the v1
step-level executor selector.

## Execution Fields

A v2 step uses exactly one of these execution forms:

| Form | Use for | Shape |
|------|---------|-------|
| `run:` | Local shell command or script | string or array |
| `action:` | Builtin or custom named action | string action name plus optional `with:` |

Invalid:

```yaml
steps:
  - run:
      type: shell
      input: echo hello
```

The current parser rejects this because `run` must be a string or array.

Also invalid:

```yaml
steps:
  - run: echo hello
    action: log.write
```

`run` and `action` cannot be used together.

## `run`

`run:` is only for local command/script execution.

```yaml
steps:
  - id: hello
    run: echo hello
```

Multi-line `run:` is treated as a script.

```yaml
steps:
  - id: script
    run: |
      set -e
      echo "first"
      echo "second"
```

`run:` also accepts an array of local shell commands.

```yaml
steps:
  - id: build_and_test
    run:
      - go build ./...
      - go test ./...
```

Only shell settings are accepted in `with:` for `run:`:

```yaml
steps:
  - id: bash
    run: echo "$SHELL"
    with:
      shell: bash -e
      shell_args:
        - -c
      shell_packages:
        - curl
```

Allowed `run` `with:` keys:

| Key | Meaning |
|-----|---------|
| `shell` | Shell command, either string or string array. |
| `shell_args` | Additional shell arguments. |
| `shell_packages` | Packages for `nix-shell`. |

Any other `with:` key with `run:` is invalid.

## `action`

`action:` selects a named builtin or custom action. Action inputs go in
`with:`.

```yaml
steps:
  - id: request
    action: http.request
    with:
      method: POST
      url: https://api.example.com/jobs
      body: '{"mode":"daily"}'
```

The loader normalizes actions into the internal executor fields. For example,
`action: postgres.query` with `with.query` becomes a PostgreSQL executor step
with the query as its command.

Action names cannot contain `@`; versioned action references are reserved for a
future registry.

## Builtin Actions

Current builtin action names:

```text
agent.run
archive.create
archive.extract
archive.list
chat.completion
container.run
dag.run
docker.run
exec
harness.run
http.request
jq.filter
k8s.run
kubernetes.run
log.write
mail.send
noop
postgres.import
postgres.query
redis.<operation>
router.route
s3.delete
s3.download
s3.list
s3.upload
sftp.download
sftp.upload
sqlite.import
sqlite.query
ssh.run
template.render
```

`redis.<operation>` is dynamic. Examples: `redis.get`, `redis.set`,
`redis.del`.

## Action Reference

| Action | Required `with` keys | Notes |
|--------|----------------------|-------|
| `http.request` | `method`, `url` | Other HTTP executor config remains in `with`. |
| `ssh.run` | `command` | SSH config such as `host`, `user`, and `key` also stays in `with`. |
| `exec` | `command` | Optional `args`; runs without shell parsing. |
| `docker.run` | none | Optional `command`; Docker config stays in `with`. |
| `container.run` | none | Optional `command`; container config stays in `with`. |
| `k8s.run`, `kubernetes.run` | none | Optional `command`; Kubernetes config stays in `with`. |
| `postgres.query`, `sqlite.query` | `query` | Query becomes the executor command. |
| `postgres.import`, `sqlite.import` | `import` | SQL import config stays in `with.import`. |
| `jq.filter` | `filter` | Optional `data` or `input`, but not both. |
| `dag.run` | `dag` | Optional `params`; required for `parallel`. |
| `router.route` | `value`, `routes` | Routes map patterns to target step names. |
| `chat.completion` | `prompt` or `messages` | Remaining `with` keys become LLM config. |
| `agent.run` | `task`, `prompt`, or `messages` | Remaining `with` keys become agent config. |
| `harness.run` | `prompt` | Optional `stdin`; remaining keys become harness config. |
| `template.render` | `template` | Template text becomes the template executor script. |
| `log.write` | `message` | Log executor config. |
| `mail.send` | mail executor config | Uses mail executor settings. |
| `archive.create`, `archive.extract`, `archive.list` | archive config | Operation is inferred from the action name. |
| `s3.upload`, `s3.download`, `s3.list`, `s3.delete` | S3 config | Operation is inferred from the action name. |
| `sftp.upload`, `sftp.download` | SFTP config | Direction is inferred from the action name. |
| `redis.<operation>` | Redis config | Redis command is inferred from the action suffix. |
| `noop` | none | `with` must be omitted or empty. |

Runtime-registered executor type names can also be used directly as actions.
Those normalize to an executor `type` with the supplied `with:` map.

## Examples

### HTTP

```yaml
steps:
  - id: create_job
    action: http.request
    with:
      method: POST
      url: https://api.example.com/jobs
      headers:
        Content-Type: application/json
      body: '{"queue":"default"}'
```

### Direct Exec

```yaml
steps:
  - id: direct
    action: exec
    with:
      command: /usr/bin/python3
      args:
        - -u
        - scripts/job.py
        - --limit
        - 10
```

### SQL Query

```yaml
steps:
  - id: query_users
    action: postgres.query
    with:
      dsn: ${DATABASE_URL}
      query: SELECT id, email FROM users WHERE active = true
```

### SQL Import

```yaml
steps:
  - id: import_users
    action: postgres.import
    with:
      dsn: ${DATABASE_URL}
      import:
        input_file: /data/users.csv
        table: users
```

### Docker

```yaml
steps:
  - id: node_tests
    action: docker.run
    with:
      image: node:20-alpine
      command: npm test
      volumes:
        - .:/workspace
      working_dir: /workspace
```

If `with.command` is omitted, the image default command is used.

### SSH

```yaml
steps:
  - id: deploy
    action: ssh.run
    with:
      host: prod.example.com
      user: deploy
      key: ~/.ssh/id_rsa
      command: cd /srv/app && git pull && systemctl restart app
```

### jq

```yaml
steps:
  - id: pick_name
    action: jq.filter
    with:
      filter: .name
      data:
        name: Alice
```

`jq.filter` rejects a step that sets both `with.data` and `with.input`.

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

### Parallel Child DAG

`parallel:` currently requires `action: dag.run`.

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

`parallel:` can be a variable reference, an array, or an object with `items`
and `max_concurrent`.

### Router

```yaml
type: graph

steps:
  - id: route
    action: router.route
    with:
      value: ${STATUS}
      routes:
        success: [deploy]
        failed: [notify]

  - id: deploy
    run: ./deploy.sh

  - id: notify
    run: ./notify.sh
```

### Chat

```yaml
steps:
  - id: summarize
    action: chat.completion
    with:
      provider: openai
      model: gpt-4.1
      prompt: Summarize ${REPORT_PATH}
```

`with.prompt` becomes one user message. `with.messages` can be used for
explicit messages.

### Agent

```yaml
steps:
  - id: investigate
    action: agent.run
    with:
      task: Inspect the failed run and write findings.
      model: claude-sonnet
      safe_mode: true
```

`with.task`, `with.prompt`, or `with.messages` supplies the agent messages.
Remaining keys configure the agent step.

### Harness

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
      stdin: |
        diff --git a/main.go b/main.go
        ...
```

`with.prompt` becomes the harness command prompt. `with.stdin` is moved to
script/stdin handling and is not retained in executor config.

### Template

```yaml
steps:
  - id: render
    action: template.render
    with:
      data:
        name: Alice
      template: |
        Hello, {{ .name }}!
    stdout: ${DAG_RUN_ARTIFACTS_DIR}/hello.txt
```

### Noop

```yaml
steps:
  - id: publish_literal
    action: noop
    output:
      version: v1.2.3
```

Object-form `output:` with only literal or file-sourced entries can also infer
`noop` for compatibility, but canonical v2 should use `action: noop`.

## Custom Actions

Top-level `actions:` defines reusable typed actions.

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

Definition fields:

| Field | Required | Meaning |
|-------|----------|---------|
| `description` | no | Copied to expanded steps unless the call site overrides it. |
| `input_schema` | yes | Inline JSON Schema object for the action's `with:` input. |
| `output_schema` | no | JSON Schema object for stdout JSON validation. |
| `template` | yes | Canonical step fragment using exactly one of `run` or `action`. |

Custom action names must match:

```text
^[A-Za-z][A-Za-z0-9_-]*(\.[A-Za-z][A-Za-z0-9_-]*)*$
```

Custom action rules:

- Names cannot conflict with builtin action names.
- `type:` is not supported inside an `actions:` definition.
- `template` must define exactly one of `run` or `action`.
- Legacy execution keys are rejected in templates: `command`, `script`, `type`,
  `call`, `params`, `messages`, `agent`, `llm`, `value`, `routes`, `exec`,
  `config`, `shell`, `shell_args`, and `shell_packages`.
- `with:` at the call site is validated against `input_schema`.
- Template strings use Go `text/template` with `.input` plus hermetic helper
  functions, including `json`.
- `{$input: path.to.value}` injects the validated value with its original JSON
  type instead of rendering it as a string.
- Base-config actions and DAG-local actions are merged per document; duplicate
  names across scopes are rejected.
- Custom actions can call other custom actions, but recursive references are
  rejected.

Allowed custom action call-site fields are workflow-control fields such as
`id`, `name`, `description`, `depends`, `continue_on`, `retry_policy`,
`repeat_policy`, `mail_on_error`, `preconditions`, `signal_on_stop`, `env`,
`timeout_sec`, `stdout`, `stderr`, `log_output`, `worker_selector`, `output`,
and `approval`.

Execution fields belong in the action template, not at the call site.

## Common Step Fields

These fields remain top-level step fields with either `run:` or `action:`.

| Field | Meaning |
|-------|---------|
| `id` | Stable identifier for dependencies and output references. |
| `name` | Display name. |
| `description` | Step description. |
| `depends` | Dependency name, list, or explicit empty list. |
| `env` | Step environment variables. |
| `working_dir` | Step working directory. |
| `timeout_sec` | Step timeout in seconds. |
| `retry_policy` | Step retry behavior. |
| `repeat_policy` | Step repeat behavior. |
| `continue_on` | Continue after selected failures, skips, exit codes, or outputs. |
| `preconditions` | Step preconditions. |
| `worker_selector` | Required worker labels. |
| `mail_on_error` | Send email on step error. |
| `signal_on_stop` | Signal sent when the step is stopped. |
| `stdout`, `stderr`, `log_output` | Output log file behavior. |
| `output` | Captured stdout variable or structured step output map. |
| `output_schema` | JSON Schema object for stdout JSON validation. |
| `approval` | Human approval gate after the step completes. |
| `container` | Step container context for running a `run:` command in a container. Use `docker.run` or `container.run` when you want the Docker/container executor directly. |

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
    run: echo "${inspect.output.version} ${inspect.output.artifact.url}"
    depends: [inspect]
```

Valid structured output sources are `stdout`, `stderr`, and `file`. Valid
decoders are `text`, `json`, and `yaml`. `select` requires `json` or `yaml`.

## Deprecated v1 Fields

The parser still accepts these v1 fields, but `dagu validate` can warn about
them:

```text
agent, call, command, config, exec, llm, messages, params, routes, script,
shell, shell_args, shell_packages, type, value, step_types
```

They cannot be mixed with `run:` or `action:` on the same step. For example,
this is invalid:

```yaml
steps:
  - run: echo hello
    command: echo legacy
```

This is also invalid:

```yaml
steps:
  - action: http.request
    type: http
    with:
      method: GET
      url: https://example.com
```

## Validation

Useful local checks:

```sh
dagu validate workflow.yaml
dagu schema dag
dagu schema dag steps
```

For implementation-level source of truth, see:

- `internal/core/spec/step_v2.go`
- `internal/core/spec/step_v2_test.go`
- `internal/core/spec/step_types.go`
- `internal/cmn/schema/dag.schema.json`
