# Spec: Human Tasks

## Status

Implemented.

## Scope

This spec defines:

- the `action: human.task` step shape
- the root-DAG-only execution boundary
- prompt resolution
- integration with the shared scalar-input schema
- generated step outputs
- the `waiting` checkpoint
- the local completion command
- atomic completion, idempotency, and resume
- local and distributed root-run behavior
- dry-run and error behavior

This spec does not define:

- `approval` behavior
- scheduler algorithms, queue protocols, or worker negotiation
- REST, Web UI, MCP, notification, authentication, or authorization behavior
- nested form objects or arrays

## Goal

A root workflow can stop at a processless step, accept an acknowledgement or a
small typed object, and continue the same DAG run later. Waiting must not keep
an executor process or distributed worker slot occupied.

Declared form values become ordinary step outputs under
`${steps.<id>.outputs.<property>}`. The completion command identifies the task
by that explicit step `id`.

## Related Specs

- YAML schema: [Spec 002: YAML Schema](002-yaml-schema.md)
- Value resolution: [Spec 003: Value Resolution and Field Evaluation](003-value-resolution.md)
- Environment values: [Spec 006: Value Resolution Env](006-value-resolution-env.md)
- Step output references: [Spec 007: Value Resolution Steps](007-value-resolution-steps.md)
- Step identity: [Spec 009: Step Reference](009-step-reference.md)
- Step outputs: [Spec 012: Step Outputs](012-step-outputs.md)
- Preconditions: [Spec 023: Preconditions](023-preconditions.md)

## Terms

A human-task step has action exactly `human.task`.

The owning run is the DAG run containing the step.

A root DAG run has no parent DAG run. A child DAG run is invoked through
`dag.run`, `dag.enqueue`, or `parallel`.

An open human task is a human-task step in node status `waiting` that has not
accepted completion input.

Canonical input is the validated completion object after string coercion and
defaults. It determines idempotency and generated output values.

A waiting checkpoint is a finalized root-run state with overall status
`waiting` and no ordinary node still ready or running.

Resume continues the same logical root run under the same DAG-run ID while
preserving completed nodes and outputs.

## Behavior

### Step Shape

Minimal acknowledgement-only task:

```yaml
steps:
  - id: acknowledge
    action: human.task
    with:
      prompt: Confirm that maintenance has started
```

Fields owned by this spec:

| Field | Required | Meaning |
| --- | --- | --- |
| `action` | Yes | Must be `human.task`. |
| `id` | Yes | Step identity used for completion and output references. |
| `with.prompt` | Yes | Instructions for the operator. |
| `with.form` | No | Flat typed input object. Omit for acknowledgement only. |

Rules:

- The step must define an explicit `id` accepted by Spec 009.
- `name` does not satisfy the explicit `id` requirement.
- `with` must be an object containing only `prompt` and optional `form`.
- `with.prompt` must be a string containing a non-whitespace character before
  runtime value resolution.
- `with.form: null` is invalid. Acknowledgement-only tasks omit `form`.
- A human task starts no command, executor, container, or child DAG.
- `name`, `description`, `depends`, `working_dir`, `env`, `preconditions`, and
  `continue_on` retain their owning behavior.
- Defaults for `env`, `preconditions`, and `continue_on` apply.
- Process-oriented defaults do not add retry, repeat, timeout, notification,
  or signal behavior to a human task.

The following authored fields are invalid even when set to an empty or false
value:

- execution: `run`, `type`, `exec`, `command`, `script`, `call`, `config`,
  `llm`, `messages`, `params`, `routes`, and `value`
- shell: `shell`, `shell_args`, and `shell_packages`
- placement: `container` and step-level `worker_selector`
- lifecycle: `retry_policy`, `repeat_policy`, `timeout_sec`, and
  `signal_on_stop`
- iteration: `foreach` and `parallel`
- approval: `approval`
- output and logging: `stdout`, `stderr`, `log_output`, `output`,
  `output_schema`, and authored `outputs`
- failure notification: `mail_on_error`

### Human Tasks Are Not Approval Steps

`human.task` is a standalone step that waits without first executing a
process. `approval` is attached to another executable step and is outside this
spec.

A human task supports one completion result: validated input makes the step
`succeeded`.

### Root DAG Boundary

Rules:

- A human-task step can execute only in a root DAG run.
- A root DAG containing human tasks can execute locally or on a distributed
  worker.
- Human tasks do not add an implicit local worker selector.
- DAG-level worker selection continues to apply.
- A child DAG containing a human task fails before the child starts or creates
  child-run state.
- The child rejection applies to `dag.run`, `dag.enqueue`, and `parallel`.
- A human task is invalid inside `foreach.steps` or any `handler_on` handler.

A DAG definition containing human tasks is valid when executed directly as a
root. Its possible use by another DAG does not make static validation fail.

For multi-document YAML, static validation accepts a later named document that
contains a human task. Invoking that document as a child fails at runtime.

### Preconditions And Prompt

Rules:

- Step preconditions run before the task opens according to Spec 023.
- If preconditions are not met, the step becomes `skipped` and accepts no
  completion.
- A precondition evaluation error follows Spec 023 failure behavior.
- `with.prompt` is value-resolved after dependencies and preconditions allow
  the step to start.
- Prompt resolution uses parameters, constants, environment values, built-in
  context, and dependency outputs defined by Specs 003, 006, and 007.
- The resolved prompt is fixed for that open task.
- Changing the DAG file or process environment after the task opens does not
  change the prompt observed by status or resume.
- A prompt-resolution error fails the step without opening it.
- Form schemas and their metadata are literal and are not value-resolved.

### Form Integration

When present, `with.form` is a flat object schema:

| Root field | Required | Meaning |
| --- | --- | --- |
| `type` | Yes | Must be `object`. |
| `title` | No | String display metadata. |
| `description` | No | String help metadata. |
| `properties` | No | Map of scalar field schemas using the same shape as schema-backed DAG parameters. Defaults to `{}`. |
| `required` | No | List of unique declared property names. Defaults to `[]`. |
| `additionalProperties` | No | Whether undeclared input is accepted. Defaults to `false`. |

Rules:

- A root field not listed in the table is invalid.
- Property names must match `^[A-Za-z][A-Za-z0-9_]*$`.
- Every `required` name must be declared in `properties`.
- Declared properties use the same scalar field shape, validation, and
  string-coercion behavior as schema-backed DAG parameters. Supported property
  types are `string`, `integer`, `number`, and `boolean`; supported fields are
  `type`, `title`, `description`, `default`, `enum`, `oneOf`, `minimum`,
  `maximum`, `minLength`, `maxLength`, and `pattern`.
- Nested declared objects and arrays are invalid.
- Defaults and declared values are validated by the shared parameter-field
  rules.
- Defaults are applied before this form enforces `required`.
- An omitted optional property without a default remains absent.
- `additionalProperties: false` rejects every undeclared input property.
- `additionalProperties: true` retains undeclared properties in canonical
  input without creating step outputs for them.
- Through `--input`, an accepted undeclared value is a string.
- Through `--inputs-json`, an accepted undeclared value retains any valid JSON
  shape, including `null`, objects, and arrays.

### Generated Step Outputs

Rules:

- Every declared form property creates an output declaration with the same
  name.
- Authors must not declare `outputs` on a human-task step.
- A declared `string` property creates a `string` output.
- A declared `integer`, `number`, or `boolean` property creates a `json`
  output.
- A property publishes a value only when canonical input contains it.
- Undeclared properties never become outputs.
- Output values come directly from canonical input. Human tasks do not use
  stdout or `DAGU_OUTPUT_FILE`.
- A consumer uses `${steps.<human-task-id>.outputs.<property>}` and must depend
  directly or transitively on the human-task step.
- Completed outputs remain available after resume.

String outputs expose their string contents. Other scalar outputs expose their
JSON scalar text. Human-task publication is an explicit exception to Spec
012's file-producer behavior.

Canonical input and the generated-output object must each fit within the
owning DAG's `max_output_size`. A size error occurs before completion changes
the task.

### Opening A Waiting Checkpoint

When the step becomes ready, Dagu:

1. evaluates preconditions
2. resolves the prompt
3. changes the step to `waiting`
4. continues independent ready or running branches
5. finalizes the root run as `waiting` after no ordinary node remains ready or
   running
6. releases the execution process or distributed worker slot

Rules:

- Dependencies of the open task do not start.
- Several independent human tasks can be `waiting` in one checkpoint.
- A foreground local run that reaches a waiting checkpoint exits successfully.
- A distributed attempt that reaches a waiting checkpoint releases its worker
  capacity before the checkpoint is actionable.
- Once `dagu status` reports the waiting checkpoint, a valid completion command
  must not fail because the prior attempt is still finalizing.
- An open task has no step timeout or automatic expiration.

### Waiting Status Observation

The black-box status surface is:

```sh
dagu status --run-id <run-id> <root-dag-name>
```

For a waiting checkpoint, the command exits zero with empty stderr. Stdout must
identify:

- overall status `Waiting`
- each waiting human task's explicit step ID
- each task's resolved prompt
- each task's normalized form as JSON when a form exists

The displayed normalized form must:

- contain root `type`, `properties`, and explicit `additionalProperties`
- preserve authored root metadata and `required` names
- preserve supported scalar metadata and constraints
- contain each property's resolved scalar type

Omitted `properties` is displayed as `{}`. Other omitted optional fields can
remain omitted. Exact object-member order is not significant.

### Completion Command

The only completion operation in this spec is:

```sh
dagu human-task complete [flags] <root-dag-name>
```

| Flag | Required | Meaning |
| --- | --- | --- |
| `--run-id`, `-r` | Yes | Owning root DAG-run ID. |
| `--step` | Yes | Explicit human-task step ID. |
| `--input key=value` | No | One string input; repeat for multiple properties. |
| `--inputs-json object` | No | One typed JSON input object. |

Rules:

- The DAG name and run ID must identify one stored root run.
- `--step` is trimmed and matches the explicit step `id`, never `name` or a
  generated task identifier.
- The command runs only in a local CLI context with direct run-store access.
- The owning root run itself can have executed locally or remotely.
- `--input` and `--inputs-json` are mutually exclusive.
- Omitting both supplies `{}`.
- Completion uses the DAG snapshot and form belonging to the run. It does not
  reload the current DAG file.
- No reconciliation command is required before or after completion.

### `--input` Parsing

Rules:

- Each occurrence must contain `=`.
- The property name is the trimmed text before the first `=` and must not be
  empty.
- The value is every character after the first `=`; additional `=` characters
  are preserved.
- A property name cannot occur more than once.
- Declared values use the same string coercion as schema-backed DAG parameters.
- Accepted undeclared values remain strings.

### `--inputs-json` Parsing

Rules:

- The flag must contain exactly one non-null JSON object followed only by JSON
  whitespace.
- Object member names must be unique at every nesting level.
- Declared values keep their JSON type and do not undergo string coercion.
- JSON string `"3"` does not satisfy an `integer` property.
- Accepted undeclared values retain their complete JSON value.

### Canonical Input

Canonical input contains:

- supplied declared properties after applicable coercion
- defaults for omitted declared properties
- accepted undeclared properties

It excludes omitted optional properties without defaults.

Canonical equality ignores object-member order and insignificant JSON
whitespace. It compares the resulting JSON values after defaults and coercion.
Reordered `--input` flags and reordered JSON object members therefore produce
the same canonical input.

### Completion Results

Successful completion writes exactly one of these lines to stdout and writes
nothing to stderr:

| Condition | Stdout |
| --- | --- |
| New completion; another node remains waiting | `Completed human task <step>; DAG-run remains waiting.` |
| New completion; resume accepted | `Completed human task <step>; DAG-run resume requested.` |
| Identical repeat; no resume is needed | `Human task <step> was already completed.` |
| Identical repeat; the run still needs resume | `Human task <step> was already completed; DAG-run resume requested.` |

Each line ends with one newline. Success exits zero.

A failed completion exits non-zero, writes no stdout, and writes a diagnostic
to stderr without command usage text.

### Atomicity And Persistence

A successful new completion atomically makes these observable changes:

- the selected node changes from `waiting` to `succeeded`
- canonical input becomes the immutable completion value
- generated outputs become available
- unrelated nodes remain unchanged

No observer can see only a subset of those changes. Input validation and size
checks occur before any change.

Completion state must survive the completion process exiting. The persistence
format and run-store file layout are outside this spec.

Changing or deleting the source DAG after the checkpoint does not change form
validation, defaults, outputs, or resume behavior for that run.

### Idempotency And Concurrency

Rules:

- Repeating completion with the same canonical input exits zero.
- An identical repeat does not change completion input or outputs.
- Repeating completion with different canonical input fails with a conflict.
- Two concurrent commands with the same canonical input both exit zero and
  converge on one completion.
- Two concurrent commands with different canonical input produce exactly one
  successful completion. The other command fails with a different-input
  conflict.
- Downstream steps execute at most once because of duplicate or concurrent
  completion commands.
- An identical repeat after the run leaves `waiting` never starts another
  resume.

### Resume And Multiple Waiting Nodes

After completion:

- If any node remains `waiting`, the run remains `waiting` and no resume is
  requested by that completion.
- If no node remains `waiting`, Dagu requests resume automatically.
- Resume keeps the same DAG-run ID and uses the stored DAG snapshot.
- Completed nodes, including human-task outputs, remain completed.
- Downstream nodes become eligible according to their dependencies.
- A later sequential human task can create another checkpoint in the same
  logical run.

Completion is durable before resume is requested. If resume preparation,
launch, queueing, or dispatch fails:

- completion remains successful and is not rolled back
- the task is not reopened
- the command exits non-zero and states that completion was stored but resume
  failed
- repeating the same completion retries resume without rewriting completion
  state

Only one successful resume can result from those retries.

### Distributed Root Runs

Rules:

- A distributed worker persists the waiting checkpoint and releases the
  attempt before completion becomes actionable.
- Completion uses the local CLI against the shared run store; it does not
  reconnect to the worker that opened the task.
- Resume keeps the same DAG-run ID and the root run's worker-selection rules.
- Resume is not forced to a local worker.
- A different selected worker can execute the resumed attempt.
- Generated outputs remain available when resume occurs on another worker.

Coordinator RPC shape, queue representation, and worker capability negotiation
remain outside this spec.

### Dry Run

Rules:

- Dry run evaluates preconditions and resolves the prompt.
- A successful dry-run human task becomes `succeeded` without entering
  `waiting`.
- Dry run does not accept completion input or publish form outputs, including
  defaults.
- Dry run starts no executor process for the human task.

## Errors

### Validation Errors

Validation must fail without executing a step when:

- the step shape violates Step Shape
- `form` is null or is not an object
- the form root has an unsupported field or a type other than `object`
- `properties` is not an object
- a property name is invalid
- a property violates the shared scalar parameter-field rules
- `required` is not a unique list of declared property names
- `additionalProperties` is not a boolean
- a human task appears in `foreach.steps` or a handler

The diagnostic must identify the invalid field path or unsupported step field.
Validation must not value-resolve the prompt or form.

### Runtime Opening Errors

The step fails without opening when prompt resolution fails or a precondition
evaluation fails. A child-DAG invocation fails before creating child-run state
and identifies that human tasks are not permitted in child DAGs.

### Completion Error Diagnostics

Completion diagnostics must contain the listed information:

| Failure class | Required diagnostic content |
| --- | --- |
| Remote CLI context | `human-task complete` and `local context` |
| Missing required flag | the missing flag name |
| Run store unavailable | `DAG-run store` |
| Missing run | requested DAG name and run ID |
| Unreadable run snapshot | `DAG` or `status`, plus the read failure |
| Invalid or missing step | requested step ID and `human task` |
| Run not waiting | run ID and `not waiting` |
| Invalid flag combination | `--input` and `--inputs-json` |
| Invalid `key=value` syntax | `--input` and `key=value` |
| Duplicate input property | property name and `duplicate` |
| Invalid JSON input | `--inputs-json` and `JSON` |
| Duplicate JSON member | member name and `duplicate` |
| Coercion or form validation | step ID, property name, and expected type or failed keyword |
| Acknowledgement input | step ID and `does not accept input` |
| Size limit | step ID and `maximum size` |
| Different prior input | step ID and `different input` |
| Concurrent run mutation | step ID and `changed` |
| Resume failure after completion | step ID, `completed`, and `could not be resumed` |

The task and downstream steps remain unchanged for every failure before atomic
completion.

### Abort, Timeout, And Cleanup

- A human task has no executor process to signal or clean up.
- Step retry, repeat, timeout, and signal fields are invalid.
- An open task does not expire automatically.
- A terminal or non-waiting run rejects a new completion.
- Concurrent terminal-state and completion operations cannot partially
  overwrite each other.
- General run abort and deletion behavior is outside this spec.

## Examples

### Acknowledgement And Resume

```yaml
steps:
  - id: maintenance_started
    action: human.task
    with:
      prompt: Confirm that maintenance has started

  - id: continue_maintenance
    depends: maintenance_started
    run: printf 'continued\n' > continued.txt
```

With run ID `spec031-ack`, status must expose the waiting step and completion is:

```sh
dagu human-task complete \
  --run-id spec031-ack \
  --step maintenance_started \
  acknowledgement.yaml
```

The command requests resume and `continued.txt` eventually contains
`continued\n`.

### Typed Form And Outputs

```yaml
steps:
  - id: release_review
    action: human.task
    with:
      prompt: Choose the release target
      form:
        type: object
        properties:
          environment:
            type: string
            enum: [staging, production]
          replicas:
            type: integer
            minimum: 1
            default: 2
          notify:
            type: boolean
            default: true
        required: [environment]

  - id: record_release
    depends: release_review
    env:
      TARGET: ${steps.release_review.outputs.environment}
      REPLICAS: ${steps.release_review.outputs.replicas}
      NOTIFY: ${steps.release_review.outputs.notify}
    run: printf '%s,%s,%s\n' "$TARGET" "$REPLICAS" "$NOTIFY" > release.txt
```

```sh
dagu human-task complete \
  --run-id spec031-form \
  --step release_review \
  --input environment=production \
  typed-form.yaml
```

Canonical input is
`{"environment":"production","notify":true,"replicas":2}` and the resumed
step writes `production,2,true\n`.

### Multiple Waiting Tasks

```yaml
steps:
  - id: review_a
    action: human.task
    with:
      prompt: Confirm A

  - id: review_b
    action: human.task
    with:
      prompt: Confirm B

  - id: deploy
    depends: [review_a, review_b]
    run: printf 'deployed\n' > deployed.txt
```

Completing `review_a` leaves the run waiting and does not create
`deployed.txt`. Completing `review_b` requests one resume and creates the file
once.

### Stored Form Snapshot

Start a run from a fixture whose required property is `environment`. After the
checkpoint, replace the source DAG with a second static fixture whose required
property is `region`. Completion with `environment` must still succeed and
publish the original output.

### Child DAG Rejection

```yaml
steps:
  - id: run_child
    action: dag.run
    with:
      dag: child

---
name: child
steps:
  - id: review
    action: human.task
    with:
      prompt: Review child work
```

Static validation succeeds. Running `run_child` fails before child-run state is
created.

### Dry Run

```yaml
steps:
  - id: review
    action: human.task
    with:
      prompt: Review ${env.TARGET}
      form:
        type: object
        properties:
          approved_for:
            type: string
            default: dry-run
```

With `TARGET=staging`, dry run resolves the prompt and succeeds without waiting
or publishing `approved_for`.
