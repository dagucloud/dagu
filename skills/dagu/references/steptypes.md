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

- Dagu expands `${VAR}` before the shell runs. For large or arbitrary text, prefer `printenv VAR_NAME`, reading `${step_id.stdout}` as a file, or `action: template.render`.
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
    parallel: ${ITEMS}
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
        Authorization: "Bearer ${TOKEN}"
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
      path: ${DAG_RUN_ARTIFACTS_DIR}/reports

  - id: write_report
    action: file.write
    with:
      path: ${DAG_RUN_ARTIFACTS_DIR}/reports/summary.txt
      content: "status=ok\n"
      overwrite: true

  - id: copy_report
    action: file.copy
    with:
      source: ${DAG_RUN_ARTIFACTS_DIR}/reports/summary.txt
      destination: ${DAG_RUN_ARTIFACTS_DIR}/reports/latest.txt
      overwrite: true

  - id: list_reports
    action: file.list
    with:
      path: ${DAG_RUN_ARTIFACTS_DIR}/reports
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

## agent.run

AI agent loop with tools.

```yaml
steps:
  - id: research
    action: agent.run
    with:
      task: "Begin research on ${TOPIC}"
      model: claude-sonnet-4-20250514
      tools:
        enabled:
          - web_search
          - bash
      skills:
        - my-skill-id
      max_iterations: 50
      safe_mode: true
```

Use `with.task`, `with.prompt`, or `with.messages` for the user input.

## harness.run

Run coding agent CLIs such as Claude Code, Codex, Copilot, OpenCode, and Pi.

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

`with.prompt` is the prompt. `with.stdin` is piped to stdin as supplementary context. `with.provider` can reference a built-in provider or a top-level `harnesses:` entry.

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
      value: ${STATUS}
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
