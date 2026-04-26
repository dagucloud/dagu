# DAG YAML Schema

## Top-Level Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | filename | DAG name |
| `group` | string | — | Group for UI organization |
| `description` | string | — | Description |
| `labels` | map or array | — | Labels as a map or array. Array entries may be bare labels or `key=value` labels; map keys max 63 chars and values max 255 chars. `tags` is accepted as a deprecated alias. |
| `type` | string | `chain` | Execution type: `chain` (sequential), `graph` (parallel/dependency-based) |
| `steps` | array or map | — | Step definitions |
| `schedule` | string or array | — | Cron expression(s) |
| `stop_schedule` | string | — | Cron to stop |
| `restart_schedule` | string | — | Cron to restart |
| `skip_if_successful` | bool | false | Skip scheduled run if already ran successfully |
| `catchup_window` | string | — | Duration (e.g. `6h`) — enables catch-up on scheduler restart |
| `overlap_policy` | string | `skip` | `skip` or `all` when new run triggered while one active |
| `env` | map or array | — | Environment variables. Array: `[{KEY: val}]`, Map: `{KEY: val}` |
| `params` | string or map | — | Parameters (positional, key=value, or JSON) |
| `default_params` | string | — | Default parameter values |
| `shell` | string or array | — | Default shell (e.g. `bash -e` or `["bash", "-e"]`) |
| `working_dir` | string | — | Working directory |
| `dotenv` | string or array | — | Path(s) to .env file(s) |
| `timeout_sec` | int | — | Max execution time (seconds) |
| `delay_sec` | int | — | Delay before first step |
| `restart_wait_sec` | int | — | Wait time before restart |
| `max_active_steps` | int | — | Max concurrent steps per run |
| `max_clean_up_time_sec` | int | 5 | Max cleanup time |
| `max_output_size` | int | 1048576 (1MB) | Max step output capture (bytes) |
| `log_dir` | string | — | Log directory |
| `log_output` | string | `separate` | `separate` (.out/.err) or `merged` (.log) |
| `hist_retention_days` | int | 30 | History retention |
| `queue` | string | — | Queue name for concurrency control. Define queues in global config with `max_concurrency`. |
| `preconditions` | array | — | DAG-level preconditions (`condition`, `expected`, `negate`) |
| `retry_policy` | object | — | DAG-level automatic retry policy: `{limit, interval_sec, backoff, max_interval_sec}`. `limit: 0` disables automatic DAG retries. |
| `handler_on` | object | — | Event handlers: `init`, `success`, `failure`, `abort`, `exit`, `wait` (each is a step definition) |
| `smtp` | object | — | SMTP config: `host`, `port`, `username`, `password` |
| `error_mail` | object | — | Error mail: `from`, `to`, `prefix`, `attach_logs` |
| `info_mail` | object | — | Success mail (same structure) |
| `mail_on` | object | — | Mail triggers: `failure`, `success`, `wait` (booleans) |
| `container` | string or object | — | String: exec into existing container. Object: create new (see container fields) |
| `ssh` | object | — | Default SSH config: `user`, `host`, `port`, `key`, `password`, `shell`, `timeout`, `bastion` |
| `s3` | object | — | Default S3 config: `region`, `endpoint`, `bucket`, credentials |
| `redis` | object | — | Default Redis config: `url`/`host`/`port`, `password`, `db`, `mode`, TLS |
| `llm` | object | — | Default LLM config: `provider`, `model`, `system`, `temperature`, `max_tokens`, `tools` |
| `secrets` | array | — | Secret references: `{name, provider, key, options}` |
| `otel` | object | — | OpenTelemetry: `enabled`, `endpoint`, `headers`, `insecure`, `timeout` |
| `worker_selector` | string or map | — | `"local"` or label map for distributed execution |
| `registry_auths` | map | — | Docker registry auth per hostname |
| `defaults` | object | — | Default values for all steps: `continue_on`, `retry_policy`, `repeat_policy`, `timeout_sec`, `signal_on_stop`, `env`, `preconditions` |
| `run_config` | object | — | `disable_param_edit`, `disable_run_id_edit` |

## Top-Level Notes

- Omitting top-level `type:` means `chain`, not `graph`. In `graph` mode, steps without `depends:` may run in parallel.
- `catchup_window:` is opt-in. Missed scheduled runs are skipped unless you set it.
- `max_active_steps:` caps graph-mode concurrency for a single DAG run.
- `params:` values are resolved as strings.
- `handler_on` keys are exactly `init`, `success`, `failure`, `abort`, `exit`, and `wait`.
- `container:` is polymorphic: a string targets an existing container, while an object creates a new one.
- Top-level `retry_policy.limit: 0` disables DAG-level automatic retries.

## Step-Level Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `id` | string | — | Unique step identifier. **Always set this.** Required for `${id.stdout}`, `${id.stderr}`, `${id.exit_code}` references and `depends:`. Regex: `^[a-zA-Z][a-zA-Z0-9_]*$`, max 40 chars. Reserved: `env`, `params`, `args`, `stdout`, `stderr`, `output`, `outputs`. |
| `name` | string | — | Display name. **Omit this** — auto-set from `id` when absent. Only use if you need a different display name than the ID. |
| `command` | string | — | Shell command |
| `script` | string | — | Script content |
| `shell` | string or array | — | Override shell |
| `working_dir` | string | — | Override working directory |
| `env` | map or array | — | Step environment variables |
| `timeout_sec` | int | — | Step timeout |
| `output` | string or object | — | Capture stdout to variable. String: `MY_VAR`. Object: `{name, key, omit}` |
| `stdout` | string | — | File to write stdout |
| `stderr` | string | — | File to write stderr |
| `depends` | string or array | — | Dependency step IDs. In `graph` mode only. |
| `continue_on` | string or object | — | Continue on: `"skipped"`, `"failed"`, or `{skipped, failed, exit_code: [codes], mark_success}` |
| `preconditions` | array | — | Step conditions: `{condition, expected, negate}` |
| `retry_policy` | object | — | `{limit, interval_sec, exit_code: [codes], backoff, max_interval_sec}` |
| `repeat_policy` | object | — | `{interval_sec, limit, condition, expected, exit_code, backoff}` |
| `signal_on_stop` | string | — | Signal on stop (e.g. `KILL`, `TERM`) |
| `mail_on_error` | bool | — | Send mail on step error |
| `container` | string or object | — | Step-level container config |
| `type` | string | — | Executor type override |
| `with` | map | — | Executor-specific config. Legacy alias: `config`; do not specify both fields |
| `call` | string | — | Sub-DAG name (for `dag` executor) |
| `params` | string or map | — | Sub-DAG parameters |
| `parallel` | object or array | — | Parallel execution: `{items, max_concurrent}` (default max_concurrent: 10) |
| `approval` | object | — | Approval gate: `{prompt, input: [fields], required: [fields], rewind_to}` |
| `llm` | object | — | LLM config for `chat` steps |
| `messages` | array | — | Chat messages: `[{role, content}]` |
| `agent` | object | — | Agent config: `{model, tools, skills, soul, memory, prompt, max_iterations, safe_mode}` |
| `value` | string | — | Router expression |
| `routes` | map | — | Router pattern→target step ID mappings |
| `worker_selector` | map | — | Labels for distributed execution |

## Step Notes

- `depends:` is only valid in `graph` mode. In `chain` mode it is a validation error.
- `retry_policy:` requires both `limit` and `interval_sec`.
- `continue_on:` is evaluated after retries are exhausted.
- `preconditions:` compare exact strings. Use a command that exits cleanly and prints only the expected value.
- DAG-level `preconditions:` block the entire run. Step-level `preconditions:` only skip that step.
- `repeat_policy.limit` must be a literal integer in YAML, not a variable reference.

## Step Reference Properties

Steps with an `id` expose properties accessible via `${step_id.property}` in downstream steps:

| Reference | Type | Description |
|-----------|------|-------------|
| `${step_id.stdout}` | string | **File path** to the step's stdout log file |
| `${step_id.stderr}` | string | **File path** to the step's stderr log file |
| `${step_id.exit_code}` | string | Exit code as a string (e.g., `"0"`) |

Slicing: `${step_id.stdout:start:length}` — substring from `start` for `length` characters.

**Note:** `${step_id.stdout}` is the **file path**, not the content. To capture stdout **content** into a variable, use the `output:` field and reference it as `${VAR_NAME}`.

Resolution priority for `${foo.bar}`: step property lookup first, then JSON path on variable `foo`.

## Output Notes

- `output:` captures trimmed stdout only. Stderr stays in the step log files.
- `${VAR.key}` JSON extraction only works when the captured stdout is clean JSON with no extra prefix or suffix text.
- Captured output is limited by top-level `max_output_size:` (default 1 MB).
- Multiline `output:` is still a single string. If you need line-by-line processing, read `${step_id.stdout}` as a file or convert the data into an array for `parallel:`.
