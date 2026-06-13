# Spec: YAML Schema

Scope: data-plane workflow YAML stream shape and root-level validation.

## Objective

Define the YAML stream accepted by the Dagu data plane before execution starts. This spec covers document structure and root fields only; detailed behavior for each field and sub-DAG execution belongs in later specs.

## Inputs

Input is one YAML file passed to a Dagu-compatible data-plane runner.

**YAML stream rules:**

- The YAML file must contain one or more DAG documents.
- Documents may be separated with YAML `---`.
- The first document is the entrypoint DAG executed by `dagu run`.
- Later documents are local DAG definitions available to sub-DAG references.
- Empty documents are invalid.

**Valid entrypoint example:**

```yaml
steps:
  - name: say-hello
    run: echo hello
```

## Command

Workflow validation commands:

```sh
dagu validate [<workflow>]
```

**Command behavior:**

- `dagu validate` without `<workflow>` validates the current project as defined by the project spec.
- `<workflow>` is a project workflow target as defined by the project spec.
- The command validates the YAML stream and root fields defined by this spec.
- The command does not validate internal field behavior that belongs to later specs.
- The command must not execute steps.

**Command output:**

| Condition | Exit Code | Stdout | Stderr |
| --- | --- | --- | --- |
| Success | `0` | Empty. | Empty. |
| Failure | Non-zero. | Empty. | Validation error. |

Validation failures must not print command usage text.

## Behavior

**Document rules:**

- The first DAG document is the entrypoint.
- The entrypoint document must not define `name`.
- Later DAG documents must have `name`.
- Later DAG document `name` values identify local sub-DAG definitions inside the YAML file.
- Later DAG documents are not executed by `dagu run` unless referenced by sub-DAG behavior.
- Each DAG document must contain `steps`.
- DAG document names must be unique inside one YAML file.
- `steps` must be a non-empty sequence.

**Field rules:**

- Root fields not listed in this spec must be rejected in every document.
- Duplicate root keys must be rejected in every document.
- Field names are case-sensitive.
- Field names use `snake_case`.
- The data-plane YAML schema must not require scheduler, coordinator, API server, UI, auth, queue, or history behavior.

## Root Fields

These root fields are part of the data-plane YAML schema:

| Field | Required | Value |
| --- | --- | --- |
| `name` | Entrypoint: forbidden; later documents: yes. | Local sub-DAG identifier. |
| `description` | No. | Human-readable description. |
| `working_dir` | No. | Default working directory. |
| `params` | No. | Workflow parameters. |
| `consts` | No. | Immutable literal values. |
| `defaults` | No. | Default step settings. |
| `steps` | Yes. | Executable steps. |
| `handler_on` | No. | Lifecycle handler steps. |
| `preconditions` | No. | Workflow start conditions. |
| `retry_policy` | No. | Workflow retry policy. |
| `timeout_sec` | No. | Workflow timeout in seconds. |
| `delay_sec` | No. | Workflow start delay in seconds. |
| `max_active_steps` | No. | Maximum concurrently running steps. |
| `max_clean_up_time_sec` | No. | Cleanup timeout in seconds. |
| `max_output_size` | No. | Maximum captured output size. |
| `container` | No. | Default Docker container settings. |
| `ssh` | No. | Default SSH settings. |
| `kubernetes` | No. | Default Kubernetes settings. |
| `llm` | No. | Default LLM settings. |
| `tools` | No. | Required tool declarations. |

Each listed field must have a dedicated spec before its behavior becomes accepted conformance behavior.

## Outputs

YAML validation happens before workflow execution.

**Output rules:**

- When validation succeeds, the runner may continue to execution.
- When validation fails, no step may start.
- Validation success by itself must not write workflow result events, run logs, or artifacts. Those outputs belong to runtime specs.

## Errors

**Validation errors:**

- Invalid YAML must fail before execution.
- An empty YAML document must fail before execution.
- A missing or empty `steps` field in any DAG document must fail before execution.
- A later DAG document without `name` must fail before execution.
- Duplicate DAG document names must fail before execution.
- An unknown root field in any DAG document must fail before execution.
- A duplicate root key in any DAG document must fail before execution.
- Validation errors should identify the invalid field path when possible.

## Examples

Minimal valid workflow:

```yaml
steps:
  - name: hello
    run: echo hello
```

Valid workflow with root metadata:

```yaml
description: deploy service
params:
  - name: environment
    type: string
    enum: [staging, production]
    required: true
  - name: version
    type: string
    required: true
steps:
  - name: deploy
    run: ./deploy.sh ${{ params.environment }} ${{ params.version }}
```

Valid workflow with inline sub-DAG:

```yaml
steps:
  - name: call-child
    action: dag.run
    with:
      dag: child

---
name: child
steps:
  - name: child-step
    run: echo child
```

Invalid workflow with unknown root field:

```yaml
unknown: true
steps:
  - name: hello
    run: echo hello
```

Invalid workflow without steps:

```yaml
name: empty
```

Invalid entrypoint with `name`:

```yaml
name: deploy
steps:
  - name: deploy
    run: ./deploy.sh
```

## Acceptance Criteria

- A black-box fixture verifies `dagu validate <workflow>` accepts the minimal valid workflow.
- A black-box fixture verifies `dagu validate <workflow>` does not execute steps.
- A black-box fixture accepts the minimal valid workflow.
- A black-box fixture rejects a workflow with no `steps`.
- A black-box fixture rejects a workflow with empty `steps`.
- A black-box fixture rejects mapping-shaped `steps`.
- A black-box fixture rejects an entrypoint document with `name`.
- A black-box fixture rejects an unknown root field.
- A black-box fixture rejects duplicate root keys.
- A black-box fixture accepts an inline sub-DAG separated by `---`.
- A black-box fixture rejects an empty YAML document.
- A black-box fixture rejects a later DAG document without `name`.
- A black-box fixture rejects duplicate DAG document names.
- Rejected workflows do not start any step.
- Validation failures do not print command usage text.
