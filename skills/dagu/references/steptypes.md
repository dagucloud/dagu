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

`with.template` is required and is rendered as a template, not executed as shell. `with.output` writes rendered content to a file; top-level `output:` captures or publishes step output.

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
- Prefer `action: harness.run` for new workflows. A top-level `harness:` config still causes steps without an explicit executor type to infer the harness executor for compatibility.

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
