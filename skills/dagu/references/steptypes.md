# Actions

## run: Shell Commands And Scripts

Use top-level `run:` for local shell commands and scripts.

```yaml
steps:
  - id: hello
    run: echo "hello"

  - id: multi_line
    run: |
      echo "step 1"
      echo "step 2"

  - id: ordered
    run:
      - echo "first"
      - echo "second"

  - id: custom_shell
    run: |
      set -euo pipefail
      echo "running in bash"
    with:
      shell: /bin/bash
```

Fields:

- `run` - command string or multi-line shell script
- `with.shell` - shell interpreter, for example `/bin/bash`
- `with.shell_args` - shell interpreter arguments
- `with.shell_packages` - optional packages to install before execution

Notes:

- Single-line `run:` values are command-form entries.
- Array-form `run:` entries run one by one and stop on the first failing entry.
- Multi-line `run:` values are scripts.
- Dagu sends pipes, redirects, `&&`, and `;` to the selected shell. It does not split that shell syntax into separate Dagu commands.
- DAG-level `shell` and `shell_args` provide defaults for inherited `run` steps. Use `with.shell` and `with.shell_args` when one step needs a different shell invocation.
- Dagu resolves `${...}` references before the shell runs. For large or arbitrary text, prefer `printenv VAR_NAME`, reading `${step_id.stdout}` as a file, or `action: template.render`.
- Use scoped Dagu references for named values: `${consts.NAME}`, `${params.NAME}`, and `${env.NAME}`. Use shell `$NAME` only when the target shell should read the variable at execution time.
- When large command output should become an artifact, write it to stdout/stderr and attach the stream directly instead of redirecting inside shell:

```yaml
steps:
  - id: report
    run: ./generate-report --format markdown
    stdout:
      artifact: reports/report.md
```

- Use string-form `output: VAR_NAME` only for small stdout values. Large reports, JSON dumps, Markdown summaries, and logs belong in `stdout.artifact` / `stderr.artifact`.

## docker.run / container.run

Run commands in Docker containers.

```yaml
steps:
  - id: build
    action: docker.run
    with:
      image: golang:1.23
      pull: always
      auto_remove: true
      working_dir: /app
      volumes:
        - /local/src:/app
      command: go build ./...
```

`with` fields: `image`, `container_name`, `pull`, `auto_remove`, `working_dir`, `volumes`, `network`, `platform`, `command`.

Dagu can drive Docker or Podman through a Docker-compatible API. Runtime selection is service-level, not a DAG YAML field. Set `DAGU_CONTAINER_RUNTIME=podman` for Podman. Set `DAGU_PODMAN_HOST` only when the Podman socket is not the default.

## git.worktree.add / git.worktree.remove

Create isolated working directories for branches in an existing local Git repository. The actions discover the repository from the step `working_dir`; they do not clone, fetch, or push.

This example creates a generated branch, runs tests inside its worktree, and then removes the worktree explicitly:

```yaml
working_dir: ./repo

steps:
  - id: worktree
    action: git.worktree.add

  - id: test
    depends: worktree
    working_dir: "${steps.worktree.outputs.path}"
    run: go test ./...

  - id: remove_worktree
    depends: test
    action: git.worktree.remove
    with:
      path: "${steps.worktree.outputs.path}"
```

When `branch` is omitted, Dagu generates a stable branch name for that step and DAG run. The default path is `<repository-root>.worktrees/<branch>`.

To create an explicit branch from a local commit, branch, `origin` remote-tracking branch, or tag:

```yaml
working_dir: ./repo

steps:
  - id: worktree
    action: git.worktree.add
    with:
      branch: feature/api
      create_branch: true
      base: main
      path: ../worktrees/feature-api
```

`git.worktree.add` fields:

- `branch` - local branch to check out. Omit it to let Dagu generate one.
- `path` - worktree directory. Relative paths resolve from the repository root.
- `create_branch` - allow creation of an explicitly named branch. Defaults to `false`.
- `base` - local commit, branch, remote-tracking branch, or tag used when creating the branch. Defaults to repository `HEAD`.

The add action is idempotent. It reuses a matching registered worktree without resetting its branch or discarding local changes. Worktrees remain registered until an explicit remove action or an external Git command removes them.

Use `git.worktree.remove` for explicit removal:

```yaml
working_dir: ./repo

steps:
  - id: worktree
    action: git.worktree.add

  - id: remove_worktree
    depends: worktree
    action: git.worktree.remove
    with:
      path: "${steps.worktree.outputs.path}"
      branch: "${steps.worktree.outputs.branch}"
      delete_branch: true
```

`git.worktree.remove` fields:

- `branch` and `path` - provide either selector or both. When both resolve to a worktree, they must identify the same registration.
- `force` - remove a dirty worktree. Defaults to `false`.
- `delete_branch` - delete the local branch after removing the worktree. Requires `branch`.
- `force_delete_branch` - allow deletion of an unmerged branch. Requires `delete_branch: true`.

`force` and `force_delete_branch` protect different data: `force` permits removal of local worktree changes, while `force_delete_branch` permits deletion of unmerged commits.

Both actions publish fixed outputs. Do not add `output`, `outputs`, or `stdout.outputs` to these steps. Read results through `${steps.<id>.outputs.<field>}`.

- Add outputs: `path`, `branch`, `commit`, `worktree_created`, `branch_created`.
- Remove outputs: `path`, `branch`, `worktree_removed`, `branch_deleted`.

Dagu refuses to remove the primary working tree. Worktree mutations against the same repository are serialized, but Git changes made outside Dagu are not covered by that lock.

## dag.run

Execute another DAG as a child DAG.

```yaml
steps:
  - id: child
    action: dag.run
    with:
      dag: child-workflow
      params:
        input: /data/file.csv
```

Sub-DAGs do not inherit parent env vars. Pass values explicitly via `with.params`.

## human.task

Pause a root DAG run until an operator completes a processless step. A human task does not execute a command and is distinct from an approval gate: completion always succeeds the step, with no reject or rewind operation.

```yaml
params:
  RELEASE: v1.2.3

steps:
  - id: review
    action: human.task
    with:
      prompt: Select a deployment window for ${params.RELEASE}
      form:
        type: object
        title: Deployment review
        properties:
          window:
            type: string
            enum: [morning, evening]
          ticket:
            type: string
            pattern: '^CHG-[0-9]+$'
          notify:
            type: boolean
            default: true
        required: [window, ticket]

  - id: deploy
    depends: [review]
    run: ./deploy --window '${steps.review.outputs.window}' --ticket '${steps.review.outputs.ticket}'
```

`with.prompt` is required and supports normal Dagu value references. `with.form` is optional; omit it for an acknowledgement-only task that accepts no input.

The form is a flat object JSON Schema:

- `type` must be `object`.
- Property names must start with a letter and contain only letters, digits, or `_`.
- Property types are `string`, `integer`, `number`, and `boolean`.
- Supported property constraints include `default`, `enum`, `oneOf` choices, `minimum`, `maximum`, `minLength`, `maxLength`, and `pattern`.
- `additionalProperties` defaults to `false`. Set it explicitly to `true` only when undeclared completion fields are intended.

Dagu derives outputs from form properties; do not add an `outputs:` field to the human task. Every declared property is a step output, published when submitted or defaulted, and available as `${steps.<step_id>.outputs.<name>}`.

Human tasks require an explicit `id` and cannot be used in sub-DAGs, lifecycle handlers, or `foreach.steps`. A root DAG containing human tasks can run locally or on a distributed worker selected by its DAG-level `worker_selector`. Executor, retry, repeat, timeout, container, step-level worker selector, output capture, and approval fields are not supported on the same step.

Complete a waiting task from a local CLI context:

```sh
dagu human-task complete --run-id=<run-id> --step=review --input window=morning --input ticket=CHG-123 <dag-name>
```

Use `--inputs-json` instead of repeated `--input` flags when input types must be preserved exactly.

Completing the last waiting human task resumes a local run directly. A distributed run is re-queued, so its scheduler must be running.

## Declared Step Outputs

Declare `outputs:` when a step should publish named values for later steps as `${steps.<step_id>.outputs.<name>}`.

```yaml
steps:
  - id: build
    run: |
      printf 'image_tag=v1.2.3\n' >> "$DAGU_OUTPUT_FILE"
      {
        printf 'metadata<<JSON\n'
        printf '{"commit":"abc123"}\n'
        printf 'JSON\n'
      } >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: image_tag
      - name: metadata
        type: json

  - id: deploy
    depends: [build]
    run: ./deploy.sh '${steps.build.outputs.image_tag}'
```

Rules:

- The step must have an `id`.
- `outputs:` must be a non-empty sequence.
- Each output requires `name`.
- `type` can be `string` or `json`. The default is `string`.
- The step writes output records to `$DAGU_OUTPUT_FILE`.
- Output records use `name=value` or heredoc form: `name<<DELIMITER`, value lines, matching `DELIMITER`.
- The output file must be valid UTF-8.
- Every declared output must be written exactly once.
- Undeclared, duplicate, missing, or invalid JSON outputs fail the step.
- Dagu captures declared outputs only after the command succeeds.

## outputs.write

Publish DAG or remote action outputs assembled from literals, parameters, or prior step values.

```yaml
steps:
  - id: send
    run: ./scripts/notify.sh "${params.text}"
    output:
      response:
        from: stdout
        decode: json

  - id: publish
    depends: [send]
    action: outputs.write
    with:
      values:
        messageId: ${send.output.response.id}
        status: sent
```

Published values are available as `${publish.outputs.messageId}` in the same DAG. When the step runs inside a remote action DAG, the parent action caller reads the final action outputs as `${action_step.outputs.messageId}`.

Notes:

- `values` must be a non-empty object.
- Keep values small and JSON-compatible; use artifacts for files, reports, logs, screenshots, or large JSON payloads.
- If the remote action manifest declares an `outputs` schema, Dagu validates the final collected action output object after the action DAG returns. `outputs.write` itself does not validate the manifest.

## state.get / state.set / state.delete / state.list / state.diff

Read and write persistent JSON state that survives across DAG runs. Use state actions for cursors, checkpoints, and comparing the current result with the previous run. Use artifacts or external storage for large files.

```yaml
steps:
  - id: load_cursor
    action: state.get
    with:
      key: cursors/feed
      default: null

  - id: save_cursor
    action: state.set
    with:
      key: cursors/feed
      value: ${fetch.output.nextCursor}

  - id: detect_change
    action: state.diff
    with:
      key: snapshots/feed
      value: ${fetch.output.items}
      update: true
```

Scope fields:

- `scope` - state scope: `dag` (default), `root_dag`, `global`, or `custom`
- `namespace` - namespace override. For `custom` scope, this is required.

Default namespaces:

- `dag` - current DAG name
- `root_dag` - root DAG name for nested DAG runs
- `global` - `_`
- `custom` - no default; set `namespace`

Operation fields:

- `state.get`: `key`, optional `default`, `required`
- `state.set`: `key`, `value`, optional `expected_version`, `create_only`
- `state.delete`: `key`
- `state.list`: optional `prefix`, `limit`, `include_values`
- `state.diff`: `key`, `value`, optional `expected_version`, `update`

All state actions write JSON to stdout. Common output fields include `operation`, `scope`, `namespace`, and key or prefix information.

- `state.get` returns `found`, and when found, `value`, `version`, and `hash`. If not found and `default` is set, `value` contains the default.
- `state.set` returns `version`, `hash`, and `created`.
- `state.delete` returns `deleted`.
- `state.list` returns `entries`; entry values are omitted unless `include_values` is true.
- `state.diff` returns `changed`, `foundPrevious`, `current`, optional `previous`, and `version` / `hash` when the stored value was written or already exists.

Values must be JSON-serializable. Dagu normalizes state values before storing them and enforces the state payload size limit after normalization.

## parallel

`parallel:` currently works only with `action: dag.run`.

```yaml
steps:
  - id: fan_out
    action: dag.run
    with:
      dag: process-item
    parallel:
      items:
        - item1
        - item2
        - item3
      max_concurrent: 5

  - id: fan_out_dynamic
    action: dag.run
    with:
      dag: process-item
    parallel: ${params.ITEMS}
```

Each child invocation receives the current item as `ITEM`.

## ssh.run / sftp.upload / sftp.download

Remote command execution and file transfer over SSH.

```yaml
steps:
  - id: remote
    action: ssh.run
    with:
      user: deploy
      host: server.example.com
      key: ~/.ssh/id_rsa
      timeout: 60s
      command: systemctl restart app

  - id: upload
    action: sftp.upload
    with:
      user: deploy
      host: server.example.com
      key: ~/.ssh/id_rsa
      source: /local/file.tar.gz
      destination: /remote/file.tar.gz
```

Shared SSH fields: `user`, `host`, `port`, `key`, `password`, `timeout`, `strict_host_key`, `known_host_file`, `shell`, `shell_args`, `bastion`.

## http.request

HTTP requests.

```yaml
steps:
  - id: api_call
    action: http.request
    with:
      method: POST
      url: https://api.example.com/data
      headers:
        Authorization: "Bearer ${env.TOKEN}"
        Content-Type: application/json
      body: '{"key": "value"}'
      json: true
      timeout: 30
```

`with` fields: `method`, `url`, `timeout`, `headers`, `query`, `body`, `silent`, `debug`, `json`, `skip_tls_verify`.

## jq.filter

JSON processing.

```yaml
steps:
  - id: transform
    action: jq.filter
    with:
      filter: ".items[] | {name: .name, count: .quantity}"
      data:
        items:
          - name: a
            quantity: 1

  - id: transform_file
    action: jq.filter
    with:
      filter: .name
      input: ${fetch_json.stdout}
```

Use `with.data` for inline JSON or `with.input` for a JSON file path. Do not set both.

## template.render

Render text using Go `text/template`.

```yaml
steps:
  - id: render
    action: template.render
    with:
      data:
        name: Alice
      template: |
        Hello, {{ .name }}!
    output: RESULT
```

Set exactly one of `with.template` or `with.template_ref`. `with.template` is literal template text. `with.template_ref` must be one complete canonical Dagu reference such as `${env.TEMPLATE}` or `${steps.fetch.outputs.template}`; it resolves once to a non-empty string, and references inside the resulting template remain literal. The selected text is rendered as a template, not executed as shell. `with.output` writes rendered content to a file; top-level `output:` captures or publishes step output.

## file.stat / file.read / file.write / file.copy / file.move / file.delete / file.mkdir / file.list

Local filesystem operations.

```yaml
steps:
  - id: ensure_output_dir
    action: file.mkdir
    with:
      path: ${context.paths.artifacts_dir}/reports

  - id: write_report
    action: file.write
    with:
      path: ${context.paths.artifacts_dir}/reports/summary.txt
      content: "status=ok\n"
      overwrite: true

  - id: copy_report
    action: file.copy
    with:
      source: ${context.paths.artifacts_dir}/reports/summary.txt
      destination: ${context.paths.artifacts_dir}/reports/latest.txt
      overwrite: true

  - id: list_reports
    action: file.list
    with:
      path: ${context.paths.artifacts_dir}/reports
      pattern: "*.txt"
```

Use `path` for `file.stat`, `file.read`, `file.write`, `file.delete`, `file.mkdir`, and `file.list`. Use `source` and `destination` for `file.copy` and `file.move`. `file.write` also requires `content`.

`with` fields: `path`, `source`, `destination`, `content`, `mode`, `format`, `pattern`, `overwrite`, `create_dirs`, `atomic`, `recursive`, `missing_ok`, `dry_run`, `include_dirs`, `follow_symlinks`, `max_bytes`.

Safety defaults:

- `overwrite` defaults to false for write, copy, and move.
- `atomic` defaults to true for file writes.
- `recursive` is required for directory copy and directory delete.
- `file.delete` refuses to delete the filesystem root.
- Copy and move reject the same source and destination, and directory copy rejects destinations inside the source tree.

## postgres.query / sqlite.query / postgres.import / sqlite.import

SQL database queries and imports.

```yaml
steps:
  - id: query
    action: postgres.query
    with:
      dsn: "postgres://user:pass@localhost:5432/db"
      query: "SELECT * FROM users WHERE active = true"
      output_format: json
      timeout: 120
      transaction: true
```

`with` fields include `dsn`, `query`, `params`, `timeout`, `transaction`, `isolation_level`, `output_format`, `headers`, `null_string`, `max_rows`, `streaming`, `output_file`, and `import`.

## redis.<operation>

Redis operations use the operation in the action name.

```yaml
steps:
  - id: cache_set
    action: redis.set
    with:
      url: "redis://localhost:6379"
      key: mykey
      value: myvalue
      ttl: 3600
```

Connection fields: `url`, `host`, `port`, `password`, `username`, `db`, TLS fields, `mode`, `timeout`, `max_retries`.

## s3.upload / s3.download / s3.list / s3.delete

S3 object operations.

```yaml
steps:
  - id: upload
    action: s3.upload
    with:
      region: us-east-1
      bucket: my-bucket
      key: data/output.csv
      source: /local/output.csv
```

Connection fields: `region`, `endpoint`, `access_key_id`, `secret_access_key`, `session_token`, `profile`, `force_path_style`.

## mail.send

Send email.

```yaml
steps:
  - id: notify
    action: mail.send
    with:
      from: noreply@example.com
      to: team@example.com
      subject: "Build Complete"
      message: "The build finished successfully."
```

SMTP server settings come from global configuration.

## archive.create / archive.extract / archive.list

Archive operations.

```yaml
steps:
  - id: compress
    action: archive.create
    with:
      source: /data/output
      destination: /data/output.tar.gz
      format: tar.gz
      exclude:
        - "*.tmp"
```

`with` fields: `source`, `destination`, `format`, `compression_level`, `password`, `overwrite`, `strip_components`, `include`, `exclude`.

## harness.run

Invoke external coding-agent CLIs through built-in provider adapters or custom harness definitions.

```yaml
harnesses:
  gemini:
    binary: gemini
    prefix_args: ["run"]
    prompt_mode: flag
    prompt_flag: --prompt

harness:
  provider: gemini
  model: gemini-2.5-pro
  fallback:
    - provider: claude
      model: sonnet

steps:
  - id: generate_tests
    action: harness.run
    with:
      prompt: "Write unit tests for the auth module"
      yolo: true
    output: RESULT
```

`with.prompt` is required and is passed to the selected provider according to its built-in adapter or custom harness definition. `with.provider` can be a built-in provider adapter (`claude`, `codex`, `copilot`, `opencode`, `pi`) or a top-level `harnesses:` entry. For host subprocess runs, `with.stdin` is piped to stdin as supplementary context.

Harness behavior:

- Built-in provider adapters and custom providers pass non-reserved `with` keys as CLI flags. Built-in adapters normalize `snake_case` keys to kebab-case flags.
- `fallback` is an ordered list of provider configs. Nested fallback is not supported.
- Provider value references must resolve to a concrete provider string before execution. Unresolved `${...}` provider values fail at runtime.
- A harness step is named with `action: harness.run`. A top-level `harness:` config supplies defaults to those steps and does not set the type of any other step, so a step written with `run:`, `exec:`, or `script:` under one stays a local command.

Container support:

- Use root-level `container:` to run compatible harness steps inside the shared DAG-level container.
- Use step-level `container:` when only that step needs a container, or when it needs a different container from the root-level container.
- Step-level `container:` takes precedence for that step.
- The selected provider binary must exist inside the container that runs the step.
- `with.stdin` and custom `prompt_mode: stdin` are rejected for containerized harness steps.
- Do not set `container.name` for step-level image-mode harness steps. Use `container.exec` when the step must run inside an existing container.
- Docker or Podman is selected by the Dagu service process, not by a DAG YAML field.

## router.route

Conditional routing based on expression value. Routes reference existing step IDs.

```yaml
steps:
  - id: check_status
    run: "curl -s -o /dev/null -w '%{http_code}' https://example.com"
    output: STATUS

  - id: route
    action: router.route
    with:
      value: ${env.STATUS}
      routes:
        "200":
          - handle_ok
        "re:5\\d{2}":
          - handle_error
          - send_alert
    depends: [check_status]

  - id: handle_ok
    run: echo "success"

  - id: handle_error
    run: echo "server error occurred"

  - id: send_alert
    run: echo "alerting on-call"
```

Routes are evaluated in priority order: exact matches first, then regex, then catch-all.
