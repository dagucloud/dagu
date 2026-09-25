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
- `stdin` - optional file path piped to the command's standard input, for example `stdin: ${fetch.stdout}`
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
- `stdin` resolves `${...}` references before the command runs; `${step_id.stdout}` gives the path to that step's captured stdout file. A leading `~` expands, relative paths resolve against the step working directory, and the host process environment is not a fallback for `$NAME`.
- A `stdin` path that resolves to nothing, or that still carries a reference, fails the step rather than silently leaving standard input empty.
- `stdin` needs an action that can consume standard input. A DAG-level `container:` makes every inherited step a container step, and `ssh` feeds the remote shell its script over standard input, so neither accepts `stdin`.
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

Pause a root DAG run until an operator completes a processless step. A human task does not execute a command and is distinct from an approval gate: completion always succeeds the step, and there is no reject operation. With `with.push_back`, the operator can instead send the work back to an upstream step with feedback.

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

`with.prompt` is required and supports normal Dagu value references. `with.form` is optional; omit it for an acknowledgement-only task that accepts no input. `with.artifacts` is optional and lists artifact-relative paths to show the operator as review context:

```yaml
steps:
  - id: test
    run: ./run-tests.sh
    stdout:
      artifact: reports/test-report.html

  - id: review
    depends: [test]
    action: human.task
    with:
      prompt: The test report is attached. Approve the release?
      artifacts:
        - reports/test-report.html
```

Artifact paths reject absolute paths, `~`, and `..` segments. They are value-resolved when the task opens, like the prompt, and the resolved path is re-checked under the same rules, so a parameter cannot inject `..`. Referencing an artifact does not enable artifact storage; a step must still write it. A missing artifact never blocks completion.

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

Completing a human task resumes the run when it unblocks a step or no other step is waiting. A distributed run is re-queued, so its scheduler must be running.

Add `with.push_back` when the reviewer may request changes instead of completing:

```yaml
steps:
  - id: implement
    action: harness.run
    with:
      provider: codex
      prompt: Implement the requested change

  - id: review
    depends: [implement]
    action: human.task
    with:
      prompt: Review the implementation
      push_back:
        rewind_to: implement
        form:
          type: object
          required: [feedback]
          properties:
            feedback:
              type: string
```

- `rewind_to` is required and must name a step the task depends on directly or transitively, by `id` or `name`. It cannot be the task itself.
- `form` is optional and follows the `with.form` rules, but `additionalProperties` must stay `false`. Feedback properties never become step outputs.
- A push-back resets `rewind_to` and every step that depends on it directly or transitively, including the task and, in `type: build` DAGs, steps that consume its output paths. The run is queued again when no other step is waiting, or when the rewind target can run, declares no build inputs, and no step is failed, aborted, rejected, or retrying. Otherwise it keeps waiting until those steps are resolved.
- Rewound steps receive `DAG_PUSHBACK`, `DAG_PUSHBACK_ITERATION`, `${context.pushback.iteration}`, and one environment variable per feedback property, even approval steps whose `approval.input` does not list it. `harness.run` and `chat.completion` also get the feedback in their prompt.
- Feedback is limited to 16 KiB as JSON because it is passed as environment variables. `DAG_PUSHBACK` keeps the most recent history entries that fit in 30 KiB.
- The task then opens again with its prompt re-resolved. It can be completed or pushed back again.
- `push_back` is invalid in `type: agent` DAGs.

Push back from a local CLI context, passing the iteration that was reviewed:

```sh
dagu human-task push-back --run-id=<run-id> --step=review --input feedback="Add tests" --expected-iteration 0 <dag-name>
```

## Declared Value Outputs

Declare value-form `outputs:` when a step should publish named values for later steps as `${steps.<step_id>.outputs.<name>}`. Build file outputs use `path` instead; see `references/build.md`.

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

A completed `parallel` step publishes a JSON array of each successful child run's outputs on its step outputs channel, so a later step in the same DAG can read `${fan_out.outputs}` or `${fan_out.outputs[0].NAME}`. Entries follow `parallel.items` order with failed children removed, so index `N` is the Nth successful child rather than the Nth item, and a successful child that published nothing contributes an empty object. An entry merges the child's output variables with its declared outputs, so values published through `outputs:`, `stdout.outputs` or `outputs.write` are included. The array does not merge into the run's collected outputs or into a parent run's `${step.outputs}`.

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

Upload files as multipart form data by mapping server field names to local paths. Relative paths resolve from the step working directory:

```yaml
steps:
  - id: upload
    action: http.request
    with:
      method: POST
      url: https://api.example.com/uploads
      form:
        description: nightly-report
      files:
        document: ./report.pdf
```

Do not set the multipart `Content-Type` header manually. `body` cannot be combined with `form` or `files`.

`with` fields: `method`, `url`, `timeout`, `headers`, `query`, `body`, `form`, `files`, `silent`, `debug`, `format`, `output`, `json`, `skip_tls_verify`.

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

`with.args` binds named variables available as `$<key>` in the filter. Values keep their YAML type; string values, including nested strings, resolve Dagu references once through executor-config resolution. Reference-derived numeric strings need `tonumber` for numeric comparisons.

Supplying `args`, including `{}`, makes the entire filter literal jq source. Pass Dagu references through `args`; steps without `args` retain their existing filter interpolation. Keys `name` and `$name` both bind `$name` and cannot be supplied together.

```yaml
params:
  - MIN_PRICE: '4'
steps:
  - id: filter
    action: jq.filter
    with:
      filter: '.items[] | select(.price > ($min_price | tonumber)) | .name'
      data:
        items:
          - {name: apple, price: 5}
          - {name: pear, price: 3}
      raw: true
      args:
        min_price: ${params.MIN_PRICE}
```

Output: `apple`

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

Custom endpoints may include a path prefix. Dagu preserves the prefix when constructing and signing S3 requests. For example, Supabase can be configured at the DAG level:

```yaml
s3:
  endpoint: "${env.SUPABASE_API_URL}/storage/v1/s3"
  region: "${env.SUPABASE_S3_REGION}"
  access_key_id: "${env.SUPABASE_S3_ACCESS_KEY_ID}"
  secret_access_key: "${env.SUPABASE_S3_SECRET_ACCESS_KEY}"
  force_path_style: true

steps:
  - id: upload
    action: s3.upload
    with:
      bucket: reports
      key: daily/output.csv
      source: /local/output.csv
```

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
  gemini-custom:
    binary: gemini
    prompt_mode: flag
    prompt_flag: --prompt

harness:
  provider: gemini-custom
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

`with.prompt` is required and is passed to the selected provider according to its built-in adapter or custom harness definition. `with.provider` can be a built-in provider adapter (`aider`, `amp`, `claude`, `cline`, `codex`, `copilot`, `cursor`, `deepseek`, `droid`, `gemini`, `goose`, `kilo`, `kiro`, `opencode`, `pi`, `qwen`) or a top-level `harnesses:` entry. For host subprocess runs, each built-in adapter either pipes `with.stdin` to stdin or folds it into the prompt; see `references/harnesses.md` for the provider table.

Harness behavior:

- Built-in provider adapters and custom providers pass non-reserved `with` keys as CLI flags. Built-in adapters normalize `snake_case` keys to kebab-case flags.
- A non-null custom definition shadows a built-in provider with the same name. Deleting the custom definition exposes the built-in again.
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

## browser.extract / browser.run

Automate a website in a local Chrome with natural-language operations. The model comes from the DAG-level `llm` block, or `with.llm`, which replaces it entirely; use a model that follows tool-call schemas reliably. Chrome or Chromium must be installed on the host that runs the step (`browser.executable` or `CHROME_PATH` selects it). Of the container images, only `ghcr.io/dagucloud/dagu:dev` includes Chromium, on amd64 and arm64; run it with `deploy/docker/browser/compose.yaml` (`docker compose up -d`), which applies the bundled seccomp profile so the browser sandbox can start, and a 1 GB `/dev/shm`. The profile applies to every process in the container and lets DAG steps call `clone`, `setns`, and `unshare` without argument filters, so use it only when the container's DAG steps are trusted.

```yaml
secrets:
  - name: VENDOR_USER
    provider: env
    key: VENDOR_USER
  - name: VENDOR_PASSWORD
    provider: env
    key: VENDOR_PASSWORD

llm:
  provider: anthropic
  model: claude-sonnet-5

steps:
  - id: invoice
    action: browser.run
    with:
      url: https://portal.example.com/billing
      browser:
        profile: vendor
        # Every host the site loads from, including CDNs and sign-in pages.
        allowed_domains: ["*.example.com"]
      variables:
        user: ${VENDOR_USER}
        password: ${VENDOR_PASSWORD}
      do:
        - act: Type %user% into the email field
          when: {selector: "form#login"}
        - act: Type %password% into the password field
          when: {selector: "form#login"}
        - act: Click the Sign in button
          when: {selector: "form#login"}
        - expect: {text: Invoices}
        - extract:
            instruction: The latest invoice
            schema:
              type: object
              properties:
                invoice_number: {type: string}
        - act: Download the latest invoice PDF

  - id: book
    depends: invoice
    run: ./book.sh "${steps.invoice.outputs.invoice_number}"
```

Browser behavior:

- Each `do` item sets exactly one of `goto`, `act`, `extract`, `expect`, `wait` (`selector` or `duration`), `screenshot`, or `ask`, plus optional `when` (skip unless it holds) and `timeout`.
- An `act` performs one action: "Sign in with %user% and %password%" types into one field and stops. Write one act per field and one for the button.
- `expect` and `when` take a statement the model judges, or a fixed check `{text}`, `{selector}`, or `{url}` that reads the page without a model call. Prefer fixed checks for monitoring; they give the same result on every run. A fixed `when` reads the page once; add `within: 10s` when the page may still be loading.
- Declare secrets under `secrets:`, pass them in `variables`, and reference them as `%name%`. The browser gets the value, the model only the name. An instruction containing a declared secret value (4+ characters) fails the step; do not write `${SECRET}` inside an instruction. A `%name%` that is not a variable or an earlier `ask.as` fails validation.
- Model requests carry the instruction, the page's elements and visible text, and the extract schema; never variable values, typed field text, or screenshots. Declared secrets shown on the page are masked; plain variables are not.
- "the model (...) answered that no element on the page matches the instruction" means the model chose no element. If the failure screenshot shows the element, the model answers that way for every request (for example `google/gemini-2.5-flash` via OpenRouter); switch models. Otherwise fix the instruction.
- The top-level properties of each `extract` schema become `${steps.<id>.outputs.<name>}`. The same property in two extracts is a validation error.
- Successful `act` operations are replayed on later runs on the same host without a model call; `extract` and model-judged conditions still call the model. A replay can hit a different element after a layout change, so follow important acts with an `expect`.
- The browser runtime applies `allowed_domains` to the page's HTTP(S) requests, so list CDN and sign-in hosts too; WebSockets are not covered. `example.com` matches only that host; `*.example.com` matches its subdomains. Dagu also fails the step when the page URL leaves the list. Blocked requests are counted per host in the timeline and appended to a failed step's error, so a missing CDN or sign-in host shows up there.
- `ask: {prompt, as}` puts the step in Waiting until someone answers in the Web UI; the answer becomes `%<as>%`. Not supported on Windows. Answers are stored in run history, so use it for short-lived codes.
- Downloads started by an act or goto are saved under `browser/<step id>/downloads/` in the run artifacts and awaited, up to the longest act or goto timeout, before the step ends. Screenshots are saved on failure by default (`screenshots: final` also keeps one of a successful end). Artifacts are not masked.
- JavaScript dialogs are accepted automatically (a `prompt` gets its default text) and listed in the timeline, so an act that raises "Are you sure?" goes through.
- Where the browser sandbox cannot start, the host setting `browser.sandbox: false` or `DAGU_BROWSER_SANDBOX=false` turns it off for every browser step; a compromised page then runs with the Dagu process's permissions, so prefer the seccomp profile. DAGs cannot change it. With the sandbox on, a browser step fails when `CI` is set or Dagu runs as root on Linux, because the browser would run without the sandbox there; set `DAGU_BROWSER_SANDBOX=false` to allow it.
- Profiles and the replay cache live on the host that runs the step. Pin such steps with `worker_selector` in distributed mode.
- After a site redesign, clear recorded acts with `dagu browser cache clear <dag> [--step <id>]` on that host instead of editing the instruction or setting `cache: false`.

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
