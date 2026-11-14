# Context References, Scoped Values, And Step Outputs

Use `${context.*}` when DAG YAML needs metadata about the current DAG run. These values are resolved by Dagu before the step executor runs.

If a supported context value is not available in the current scope, Dagu keeps the original reference text. It does not replace the reference with an empty string.

## Scoped Value References

Use scoped references when Dagu should resolve a named value before execution.

| Reference | Meaning |
| --- | --- |
| `${consts.NAME}` | Top-level `consts:` value |
| `${dates.NAME}` | Top-level `dates:` entry, resolved from the run's schedule slot |
| `${params.NAME}` | Runtime parameter value |
| `${env.NAME}` | Value in Dagu's runtime environment scope for the current step |
| `${steps.<step_id>.outputs.<name>}` | Declared value output, human-task form value, or committed build path output |

Use shell `$NAME` or `printenv NAME` only when the target shell or process should read the variable at execution time.

`${steps.<step_id>.outputs.<name>}` is only for declared step outputs. Other step properties keep the step-ID form that the resolver supports, such as `${step_id.stdout}`, `${step_id.output.name}`, and `${step_id.outputs.name}`.

## Top-Level `consts:`

Use `consts:` for static DAG values that Dagu should resolve before step execution. Constants are not process environment variables. Use `${consts.NAME}` when Dagu should substitute the value into a DAG field. Copy a constant into `env:` only when a child process needs an environment variable.

```yaml
consts:
  - service: api
  - region: us-east-1
  - endpoint: https://${consts.service}.${consts.region}.example

env:
  - API_ENDPOINT=${consts.endpoint}

steps:
  - id: healthcheck
    run: curl -fsS '${consts.endpoint}/health'
```

Rules:

- `consts:` must use list form. Mapping form is invalid.
- Each list item must be a single-key mapping, such as `- service: api`.
- Names must start with a letter and then contain only letters, digits, or `_`.
- Values must be literal strings, finite numbers, or booleans. `null`, arrays, and objects are invalid.
- A const can reference inherited consts or earlier consts in the same list with `${consts.NAME}`.
- A const cannot read runtime values while it is being defined. References such as `${params.NAME}`, `${env.NAME}`, `${steps.step_id.outputs.name}`, self-references, later const references, and unknown const references remain unresolved text inside the const value.
- Consts from a base config are inherited. A DAG-local const with the same name overrides the inherited value.

Use `consts:` for fixed workflow configuration. Use `params:` for per-run user input. Use `env:` for values that should be available to the running process environment.

## Top-Level `dates:`

Use `dates:` for the dates a scheduled run loads, derived from its slot. Each run resolves them once when it is created, so a retry, a manual run for the slot (`dagu start --scheduled-at`), and the scheduler's run get the same values.

```yaml
schedule: "CRON_TZ=Asia/Tokyo 0 2 * * *"
calendar: jp-banking
dates:
  target_day: -1d
  target_compact: { from: target_day, format: date_compact }
  month_start: [-1d, month_start]
  last_open_day: -1bd
  window_days: { each: 1d }
steps:
  - id: load
    run: ./load.sh --date ${dates.target_day}
```

- An entry is a shift (`-1d`, `[-1d, month_start]`) or an object with `from`, `each`, `shift`, `format`, and `timezone`.
- Shifts: `+N`/`-N` with `min`, `h`, `d`, `w`, `mo`, `y`, `bd`; `hour_start` to `year_start` (weeks start Monday); `bd_or_next`, `bd_or_prev`. `m` is rejected: write `min` or `mo`.
- `from` is `scheduled_at` (default), `interval_start`, `interval_end`, `next_scheduled_at`, or another entry.
- `each` lists the window from `interval_start` up to the slot as a JSON array, for `parallel: ${dates.window_days}`. A step under a day needs `format`.
- Formats: `date` (default), `date_compact`, `datetime`, `rfc3339`, `unix`, or a strftime pattern.
- Business days use the calendar of the schedule entry that owns the slot, else the DAG's `calendar`.
- A run started without a slot, by hand, a webhook, or a watched file, gets the default slot: the latest fire at or before it was created. A monthly DAG started on the 15th loads what its run of the 1st loaded.
- On a DAG with no schedule, `scheduled_at` is the minute the run was created, and a date that needs a window is refused, except in a sub-DAG of a run with a slot, which resolves from its parent's slot.
- Dates are not environment variables. Pass one through `env:` when a script needs it.
- A param can follow the slot with `eval: ${dates.target_day}`. It is evaluated when the run starts, so a caller's value overrides it (`dagu start etl -- date=2026-11-01`) without changing the date.

## Supported Context References

Run and step metadata:

| Reference | Meaning |
| --- | --- |
| `${context.dag.name}` | Current DAG name |
| `${context.run.id}` | Current DAG run ID |
| `${context.run.status}` | Current run status when status is available, such as lifecycle handler scopes |
| `${context.run.scheduled_at}` | Scheduled time for a scheduled run |
| `${context.run.interval_start}` | Previous fire time of the DAG's schedule before `scheduled_at` |
| `${context.run.interval_end}` | Same instant as `scheduled_at`; the window is `[interval_start, interval_end)` |
| `${context.run.next_scheduled_at}` | Next fire time of the DAG's schedule after `scheduled_at` |
| `${context.run.timezone}` | Zone of the slot's schedule, such as `Asia/Tokyo` |
| `${context.run.root_name}` | Root DAG name when this run is part of a nested run |
| `${context.run.root_id}` | Root run ID when this run is part of a nested run |
| `${context.attempt.id}` | Current attempt ID |
| `${context.attempt.started_at}` | Current attempt start time |
| `${context.step.id}` | Current step ID |
| `${context.step.name}` | Current step name |
| `${context.step.repeat_index}` | Zero-based execution index of a step with `repeat_policy`; also `DAG_RUN_STEP_REPEAT_INDEX` |
| `${context.trigger.type}` | Run trigger type when trigger metadata is available |
| `${context.trigger.actor}` | Trigger actor when trigger metadata includes one |

Path metadata:

| Reference | Meaning |
| --- | --- |
| `${context.paths.log_file}` | DAG run log file path |
| `${context.paths.work_dir}` | Per-run working directory when one exists |
| `${context.paths.artifacts_dir}` | Artifact directory when artifacts are enabled |
| `${context.paths.step_stdout_file}` | Current step stdout log file |
| `${context.paths.step_stderr_file}` | Current step stderr log file |
| `${context.paths.step_output_file}` | Current step output file path |

Other scoped metadata:

| Reference | Meaning |
| --- | --- |
| `${context.profile.name}` | Runtime profile name when profile metadata exists |
| `${context.profile.resolved_at}` | Runtime profile resolution time when profile metadata exists |
| `${context.pushback.iteration}` | Push-back iteration when the step is running in push-back flow |
| `${context.pushback.previous_stdout_file}` | Previous stdout file when push-back provides one |

Example:

```yaml
steps:
  - id: summarize
    run: ./summarize.sh '${context.dag.name}' '${context.run.id}'
```

Use single quotes around context references when the shell should receive the resolved value as one argument.

## Declared Value Outputs

A step can declare value outputs and write them during execution. Dagu reads the output file after the command succeeds and publishes the values as `${steps.<step_id>.outputs.<name>}`.

Human task form outputs use the same reference namespace without an authored `outputs:` field. Every declared property is published when submitted or defaulted.

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

- A step that declares `outputs:` must have an `id`.
- `outputs:` must be a non-empty sequence.
- Each output must declare `name`.
- `type` is optional. Supported values are `string` and `json`.
- The output file must contain valid UTF-8.
- Use `name=value` for a single-line value.
- Use `name<<DELIMITER`, value lines, and a matching `DELIMITER` for a multi-line value.
- Every declared output must be written exactly once.
- Writing an undeclared output fails the step.
- Writing a duplicate output fails the step.
- For `type: json`, the emitted value must be valid JSON.
- Dagu captures declared outputs only after the command itself succeeds.

Declared step outputs are different from DAG or remote action outputs. To publish caller-visible DAG/action outputs, use `stdout.outputs` or `action: outputs.write`.

In a `type: build` workflow, an `outputs:` entry may declare `path` instead of a value `type`. The owning step writes to `${outputs.<name>}`, and `${steps.<step_id>.outputs.<name>}` publishes the final path only after commit or reuse. See `references/build.md`.
