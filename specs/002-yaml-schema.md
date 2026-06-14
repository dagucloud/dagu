# Spec: YAML Schema

Scope: workflow YAML stream shape and root fields accepted before execution.

This spec validates the document structure and root keys. It does not define the full behavior of each field. Later specs define field behavior.

## Objective

Define the YAML file shape accepted by a Dagu data-plane runner.

The file may contain one DAG document or several DAG documents separated by `---`. The first document is the entrypoint. Later documents are local sub-DAGs available to the entrypoint.

## Input

Input is one YAML file.

Rules:

- The file must contain at least one non-empty YAML document.
- Documents may be separated with YAML `---`.
- Empty documents are invalid.
- The first document is the entrypoint DAG.
- Later documents define local sub-DAGs.

Minimal valid workflow:

```yaml
steps:
  - name: say-hello
    run: echo hello
```

## Command

Validation command:

```sh
dagu validate <path/to/dag_file>
```

Rules:

- `<path/to/dag_file>` is the YAML file to validate.
- The command validates the YAML stream and the root fields listed in this spec.
- The command must not execute steps.
- The command does not validate behavior owned by later specs except where those specs explicitly extend validation.

Output:

| Condition | Exit code | Stdout | Stderr |
| --- | --- | --- | --- |
| Success | `0` | Empty. | Empty. |
| Failure | Non-zero | Empty. | Validation error. |

Validation failures must not print command usage text.

## Documents

Rules:

- The entrypoint document must not define `name`.
- Every later document must define `name`.
- Later document names identify local sub-DAGs inside the same YAML file.
- Later documents are not executed by `dagu run` unless an execution spec references them as sub-DAGs.
- DAG document names must be unique inside one YAML file.
- Every document must contain `steps`.
- `steps` must be a non-empty sequence.

## Root fields

Root field rules:

- Root fields not listed in this spec are invalid.
- Duplicate root keys are invalid.
- Field names are case-sensitive.
- Field names use `snake_case`.
- This data-plane schema must not require scheduler, coordinator, API server, UI, auth, queue, or history behavior.

Accepted root fields:

| Field | Required | Meaning |
| --- | --- | --- |
| `name` | Entrypoint: forbidden. Later documents: required. | Local sub-DAG identifier. |
| `description` | No | Human-readable description. |
| `working_dir` | No | Default process working directory. |
| `params` | No | Workflow parameters. |
| `consts` | No | Immutable literal values. |
| `defaults` | No | Default step settings. |
| `steps` | Yes | Executable steps. |
| `handler_on` | No | Lifecycle handler steps. |
| `preconditions` | No | Workflow start conditions. |
| `retry_policy` | No | Workflow retry policy. |
| `timeout_sec` | No | Workflow timeout in seconds. |
| `delay_sec` | No | Workflow start delay in seconds. |
| `max_active_steps` | No | Maximum concurrently running steps. |
| `max_clean_up_time_sec` | No | Cleanup timeout in seconds. |
| `max_output_size` | No | Maximum captured output size. |
| `container` | No | Default Docker container settings. |
| `ssh` | No | Default SSH settings. |
| `kubernetes` | No | Default Kubernetes settings. |
| `llm` | No | Default LLM settings. |
| `tools` | No | Required tool declarations. |

Each accepted field needs its own behavior spec before its behavior becomes part of conformance.

## Outputs

YAML validation happens before workflow execution.

Rules:

- If validation succeeds, the runner may continue to execution.
- If validation fails, no step may start.
- Validation success does not write workflow events, run logs, artifacts, or result files.

## Errors

Validation must fail when:

- The file is not valid YAML.
- The file contains no non-empty document.
- Any document is empty.
- Any document is missing `steps`.
- `steps` is empty.
- `steps` is not a sequence.
- The entrypoint document defines `name`.
- A later document omits `name`.
- Two DAG documents use the same `name`.
- A document contains an unknown root field.
- A document contains duplicate root keys.

Validation errors should identify the invalid field path when possible.

## Examples

Valid workflow with metadata:

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
    run: ./deploy.sh ${params.environment} ${params.version}
```

Valid workflow with an inline sub-DAG:

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

Invalid unknown root field:

```yaml
unknown: true
steps:
  - name: hello
    run: echo hello
```

Invalid entrypoint name:

```yaml
name: deploy
steps:
  - name: deploy
    run: ./deploy.sh
```

## Acceptance criteria

- A black-box fixture verifies `dagu validate <path/to/dag_file>` accepts the minimal valid workflow.
- A black-box fixture verifies `dagu validate <path/to/dag_file>` does not execute steps.
- A black-box fixture rejects invalid YAML.
- A black-box fixture rejects an empty YAML document.
- A black-box fixture rejects a workflow with no `steps`.
- A black-box fixture rejects a workflow with empty `steps`.
- A black-box fixture rejects mapping-shaped `steps`.
- A black-box fixture rejects an entrypoint document with `name`.
- A black-box fixture rejects an unknown root field.
- A black-box fixture rejects duplicate root keys.
- A black-box fixture accepts an inline sub-DAG separated by `---`.
- A black-box fixture rejects a later DAG document without `name`.
- A black-box fixture rejects duplicate DAG document names.
- Rejected workflows do not start any step.
- Validation failures do not print command usage text.
