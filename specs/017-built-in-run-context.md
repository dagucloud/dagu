# Spec: Built-In Run Context

## Status

Implemented.

## Scope

This spec defines Dagu-managed context values for one DAG run and the current
step or handler inside that run.

It covers:

- structured built-in context references such as `${run.id}`
- environment-variable projections such as `DAG_RUN_ID`
- availability rules for run, attempt, step, handler, trigger, path, profile,
  and push-back context values

This spec does not define:

- user-authored `env` declarations
- parameter declaration, validation, or individual `${params.name}` references
- step output declaration or `${steps.step_id.outputs.name}` references
- secret provider lookup or secret masking
- lifecycle handler execution order
- scheduler, queue, coordinator, worker, API, or UI behavior
- integration-specific convenience variables such as provider-owned GitHub
  variables

Environment precedence and protected-name behavior are defined by
[Spec 006: Value Resolution Env](006-value-resolution-env.md). Common value
resolution syntax, escaping, field support, and passive notices are defined by
[Spec 003: Value Resolution and Field Evaluation](003-value-resolution.md).

## Goal

Workflow authors can read stable DAG-run metadata without depending on ambient
host environment, storage layout, UI state, or scheduler internals.

Shell-oriented steps can continue to use small environment variables.
Object-valued payloads stay on their existing compatibility environment
variables or owning specs instead of becoming structured string references.

## Motivation

Built-in run metadata is a public workflow-language surface. Once a variable is
observable, workflow authors may depend on its name, value, timing, and
overwrite behavior.

Dagu needs one contract that separates:

- stable run identity, such as the DAG name and DAG-run ID
- current execution context, such as the current step name and stream files
- optional trigger context, such as scheduled time or webhook payload
- filesystem locations, such as logs, work directory, and artifacts directory
- compatibility environment variables used by existing shell workflows

The context must be deterministic for the current run. It must not read
unrecorded host state during a later step, retry, handler, or distributed
execution hop.

## Related Specs

- Value resolution: [Spec 003: Value Resolution and Field Evaluation](003-value-resolution.md)
- Environment values: [Spec 006: Value Resolution Env](006-value-resolution-env.md)
- Step identity: [Spec 009: Step Reference](009-step-reference.md)
- Step outputs: [Spec 012: Step Outputs](012-step-outputs.md)
- Step run: [Spec 013: Step Run](013-step-run.md)

## Behavior

### Context Values

A context value is Dagu-managed runtime data about the current DAG run or the
current step or handler.

Rules:

- Context values are read-only from the workflow author's perspective.
- User-authored `env`, dotenv, runtime-profile variables, step outputs, and
  secrets must not override protected context values in the final environment
  scope where Spec 006 says the name is protected.
- Context values must not contain Dagu-managed secret values.
- Context values must not expose the raw host process environment.
- Context values must be stable for the lifecycle scope that owns them.
- A value that is unavailable in a lifecycle scope is missing, not an empty
  string, unless a field definition below explicitly says the value is empty.
- Missing structured context references are preserved according to Spec 003
  missing-reference behavior.

### Structured Reference Syntax

This spec adds target Dagu-owned reference forms:

```text
${dag.name}
${run.id}
${run.status}
${run.started_at}
${run.scheduled_at}
${run.root_name}
${run.root_id}
${attempt.id}
${step.id}
${step.name}
${trigger.type}
${trigger.actor}
${paths.log_file}
${paths.work_dir}
${paths.artifacts_dir}
${paths.docs_dir}
${paths.step_stdout_file}
${paths.step_stderr_file}
${paths.step_output_file}
${profile.name}
${profile.resolved_at}
${pushback.iteration}
${pushback.previous_stdout_file}
```

Rules:

- These references are Dagu-owned references only in fields that support value
  resolution under Spec 003.
- All structured context references in this spec resolve to scalar string
  values.
- Object-valued context, such as webhook payload, webhook headers, profile
  entries, and push-back history, is outside this spec's structured string
  references.
- Unsupported context-looking text is preserved silently as ordinary string
  content under Spec 003.
- Escaped context-looking text follows Spec 003 escape behavior.
- `dag`, `run`, `attempt`, `step`, `trigger`, `paths`, `profile`, and
  `pushback` are reserved context namespaces.
- A workflow parameter named `run`, `step`, or another context namespace remains
  addressable through `${params.run}` or `${params.step}`. It must not shadow a
  context namespace.

### Namespace Fields

`dag` fields:

| Field | Availability | Meaning |
| --- | --- | --- |
| `dag.name` | All run, step, and handler scopes | Name of the DAG definition being executed. |

`run` fields:

| Field | Availability | Meaning |
| --- | --- | --- |
| `run.id` | All run, step, and handler scopes | Unique identifier for the current DAG run. |
| `run.status` | Status-aware scopes only | Canonical lowercase run status. |
| `run.started_at` | After run start is recorded | UTC RFC3339 timestamp for the start of this DAG run attempt. |
| `run.scheduled_at` | Scheduled, catchup, and one-off scheduled runs only | UTC RFC3339 timestamp for the logical schedule time that caused the run. |
| `run.root_name` | Sub-DAG runs only | DAG name of the root DAG run. |
| `run.root_id` | Sub-DAG runs only | DAG-run ID of the root DAG run. |

Rules:

- `run.id` is the durable identity for a DAG run.
- `run.scheduled_at` is not a unique run identifier. Scheduled, catchup, and
  retry behavior may create multiple runs associated with one logical schedule
  time. Workflow authors must use `run.id` when they need identity.
- `run.status` is available in lifecycle handlers and other explicitly
  status-aware surfaces. It is unavailable in normal step execution unless an
  owning spec explicitly makes that surface status-aware.
- Valid `run.status` values are `not_started`, `queued`, `running`,
  `succeeded`, `partially_succeeded`, `failed`, `aborted`, `waiting`,
  `rejected`, and `unknown`.

`attempt` fields:

| Field | Availability | Meaning |
| --- | --- | --- |
| `attempt.id` | Attempt-aware run, step, and handler scopes | Identifier for the current DAG-run attempt. |

Rules:

- `attempt.id` identifies the current attempt for one DAG run.
- `attempt.id` is not the same as `run.id`.
- Step retry attempts are outside this namespace unless a step-retry-owning
  spec adds a separate field.

`step` fields:

| Field | Availability | Meaning |
| --- | --- | --- |
| `step.id` | Current executable step only, when that step has an id | Stable value-reference alias from Spec 009. |
| `step.name` | Current step or handler only | Runtime step identity for the current step or handler. |

Rules:

- `step.name` is the runtime step identity defined by Spec 009.
- `step.id` is missing when the current executable step has no authored `id`.
- Handler scopes have `step.name` but do not have `step.id` unless a
  handler-owning spec defines an id for handlers.

`trigger` fields:

| Field | Availability | Meaning |
| --- | --- | --- |
| `trigger.type` | All run, step, and handler scopes when known | Canonical trigger type. |
| `trigger.actor` | Runs started by an authenticated or attributable actor | Stable actor identifier safe to expose to the workflow. |

Rules:

- Valid `trigger.type` values are `unknown`, `manual`, `scheduler`,
  `webhook`, `subdag`, `retry`, and `catchup`.
- `trigger.actor` must not contain credentials, bearer tokens, session IDs, or
  other authenticators.
- Webhook payload and headers are object-valued trigger context and are exposed
  through compatibility environment variables.

`paths` fields:

| Field | Availability | Meaning |
| --- | --- | --- |
| `paths.log_file` | All run, step, and handler scopes | Absolute path to the aggregated DAG-run log file. |
| `paths.work_dir` | When a per-run work directory is available | Absolute path to the per-run work directory. |
| `paths.artifacts_dir` | When artifact storage is active | Absolute path to the per-run artifacts directory or staging directory. |
| `paths.docs_dir` | When a per-DAG docs directory is configured | Absolute path to the per-DAG docs directory. |
| `paths.step_stdout_file` | Current executable step after stream files are assigned. Handler support is target behavior. | Absolute path to the current step stdout file. |
| `paths.step_stderr_file` | Current executable step after stream files are assigned. Handler support is target behavior. | Absolute path to the current step stderr file. |
| `paths.step_output_file` | Current step attempt after output publication is prepared | Absolute path to the current step output file used by Spec 012. |

Rules:

- Path values are filesystem paths as seen by the current step or handler.
- A path value may point to worker-local staging storage in an execution mode
  where final storage is remote or coordinator-owned.
- Workflow authors must not infer storage retention, public URL shape, or UI
  availability from a path value.

`profile` fields:

| Field | Availability | Meaning |
| --- | --- | --- |
| `profile.name` | Runs started with a selected runtime profile | Runtime profile selected for the run. |
| `profile.resolved_at` | Runs started with a selected runtime profile | UTC RFC3339 timestamp at which the selected profile was resolved. |

Rules:

- Profile values are metadata only.
- Profile variables and profile secrets are owned by the runtime-profile and
  secret specs, not by this spec.

`pushback` fields:

| Field | Availability | Meaning |
| --- | --- | --- |
| `pushback.iteration` | Steps re-executed after approval push-back | Current push-back iteration as a decimal string. |
| `pushback.previous_stdout_file` | Rewound steps that had previous stdout | Absolute path to the previous stdout log for the current step. |

Rules:

- Push-back inputs and history are object-valued context and are exposed through
  compatibility environment variables.
- Push-back context is missing on the first execution before any push-back
  occurs.

### Environment Projection

Environment projection is the flat process-environment view of selected context
values.

Rules:

- Environment projection is for compatibility with shell scripts and tools that
  read process environment variables.
- Structured references are the canonical surface for new scalar context
  fields.
- Environment projection must not expose object-valued context except as JSON
  strings for compatibility variables already defined by this spec.
- Environment projection names are reserved according to Spec 006.
- A context value unavailable in the current scope must not be projected unless
  the variable definition below explicitly allows an empty value.

Run-level projection:

| Environment variable | Source | Availability |
| --- | --- | --- |
| `DAG_NAME` | `dag.name` | All steps and handlers. |
| `DAG_RUN_ID` | `run.id` | All steps and handlers. |
| `DAG_RUN_LOG_FILE` | `paths.log_file` | All steps and handlers. |
| `DAG_RUN_WORK_DIR` | `paths.work_dir` | When a per-run work directory is available. |
| `DAG_RUN_ARTIFACTS_DIR` | `paths.artifacts_dir` | When artifact storage is active. |
| `DAG_DOCS_DIR` | `paths.docs_dir` | When a per-DAG docs directory is configured. |
| `DAG_PARAMS_JSON` | Parameter payload JSON | When resolved parameters exist. |
| `DAGU_PARAMS_JSON` | Same value as `DAG_PARAMS_JSON` | Compatibility alias when resolved parameters exist. |

Step and handler projection:

| Environment variable | Source | Availability |
| --- | --- | --- |
| `PWD` | Current process working directory | Current step only. |
| `DAG_RUN_STEP_NAME` | `step.name` | Current step or handler only. |
| `DAG_RUN_STEP_STDOUT_FILE` | `paths.step_stdout_file` | Current executable step after stream files are assigned. Handler support is target behavior. |
| `DAG_RUN_STEP_STDERR_FILE` | `paths.step_stderr_file` | Current executable step after stream files are assigned. Handler support is target behavior. |
| `DAGU_OUTPUT_FILE` | `paths.step_output_file` | Current step attempt after output publication is prepared. |

Status and wait-handler projection:

| Environment variable | Source | Availability |
| --- | --- | --- |
| `DAG_RUN_STATUS` | `run.status` | Lifecycle handlers and status-aware surfaces only. |
| `DAG_WAITING_STEPS` | Waiting step names | Wait handler only. |

Push-back projection:

| Environment variable | Source | Availability |
| --- | --- | --- |
| `DAG_PUSHBACK` | Push-back metadata JSON | Steps re-executed after approval push-back only. |
| `DAG_PUSHBACK_ITERATION` | `pushback.iteration` | Steps re-executed after approval push-back only. |
| `DAG_PUSHBACK_PREVIOUS_STDOUT_FILE` | `pushback.previous_stdout_file` | Rewound steps that had previous stdout. |

Webhook projection:

| Environment variable | Source | Availability |
| --- | --- | --- |
| `WEBHOOK_PAYLOAD` | Trigger payload JSON | Webhook-triggered runs only. |
| `WEBHOOK_HEADERS` | Allowlisted trigger headers JSON | Webhook-triggered runs only. |

Rules:

- `WEBHOOK_PAYLOAD` and `WEBHOOK_HEADERS` are compatibility runtime-param
  environment values.
- They are not protected Dagu-managed environment names under Spec 006.
- They follow Spec 006 runtime-param precedence after parameter resolution.
- When a DAG explicitly declares either name as a runtime parameter, that name
  behaves as a normal user parameter for that DAG.
- When a webhook trigger supplies either name and the DAG does not declare it
  as a runtime parameter, the trigger-provided value is an internal
  runtime-param value.

Reserved specialized run-control names:

- `DAGU_EXTERNAL_STEP_RETRY`
- `DAGU_QUEUE_DISPATCH_RETRY`

These names are reserved for owning run-control surfaces. They are not normal
workflow context values unless an owning spec explicitly adds them.

### Availability

Availability rules:

- DAG and run identity are available after the DAG run is created.
- Attempt identity is available only in scopes that know the current DAG-run
  attempt.
- Step context is available only while evaluating or executing the current step
  or handler.
- Step stream paths are available only after stdout and stderr files are
  assigned for the current executable step. Handler support is target behavior.
- Step output file path is available only after step output publication is
  prepared for the current step attempt.
- Artifact directory is available only when artifact storage is active.
- Docs directory is available only when a docs directory is configured.
- Profile context is available only when a runtime profile was selected.
- Webhook context is available only for webhook-triggered runs.
- Push-back context is available only for step executions caused by an
  approval push-back cycle.

Validation rules:

- `dagu validate` must not fail only because a structured context reference is
  unavailable during validation.
- Explicit inspection surfaces must report passive notices for unresolved
  supported structured context references.
- Normal run execution must not emit passive value-reference notices as run
  logs, workflow events, status data, history data, artifacts, or DAG-run detail
  data.

### Security

Rules:

- Context values must be treated as ordinary workflow input by the step that
  consumes them.
- Trigger payloads and trigger headers are untrusted external input.
- Compatibility JSON-string environment values must not include Dagu-managed
  secret values or authenticators.
- Compatibility JSON-string environment values must not include unallowlisted
  webhook headers.
- Environment projection must not introduce new secret exposure paths beyond
  variables explicitly owned by secret or profile specs.
- A context reference that resolves to a string must not be shell-escaped by
  Dagu. Shell-backed fields follow the quoting rules in Spec 013.

### Compatibility

Rules:

- Existing environment variable names listed in this spec remain supported.
- `DAG_PARAMS_JSON` is the canonical parameter payload environment variable.
- `DAGU_PARAMS_JSON` remains a compatibility alias with the same value whenever
  `DAG_PARAMS_JSON` is set.
- New structured context fields must be additive.
- New environment projection variables should be added only when a value is
  broadly useful to shell scripts and cannot reasonably be represented as a
  structured reference or an existing compatibility variable.

## Errors

Validation must fail when:

- a workflow attempts to define a secret whose name collides with a
  secret-reserved context environment variable under Spec 006.
- a field shape owned by another spec is invalid before context references are
  evaluated.

Runtime must fail before starting the current step or handler when:

- a projected context environment variable cannot be represented as a process
  environment entry for the target platform.
- a context value required by the current executor surface cannot be made
  available after that surface declares it required.

Runtime must preserve the original reference text when:

- a supported structured context reference is unavailable in the current
  lifecycle scope.
- a supported structured context reference is unavailable because an optional
  feature, such as artifacts, docs, profile, webhook, or push-back, is not
  active for this run.

## Examples

Use stable run identity in a notification:

```yaml
steps:
  - id: notify
    run: notify.sh '${dag.name}' '${run.id}'
```

Use a per-run work directory when it is available:

```yaml
steps:
  - id: write_scratch
    run: |
      mkdir -p '${paths.work_dir}'
      printf '%s\n' '${run.id}' > '${paths.work_dir}/run-id.txt'
```

Use handler-only run status:

```yaml
handler_on:
  exit:
    run: notify.sh '${dag.name}' '${run.id}' '${run.status}'
```

Unsupported normal-step status reference is preserved until a status-aware
surface owns it:

```yaml
steps:
  - id: print_status
    run: printf '%s\n' '${run.status}'
```

In a normal step, `run.status` is unavailable. Dagu preserves the reference text
and hands it to the selected shell according to Spec 013.
