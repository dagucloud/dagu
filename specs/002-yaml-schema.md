# Spec: YAML Schema

Scope: data-plane workflow YAML stream shape and root-level validation.

## Objective

Define the YAML stream accepted by the Dagu data plane before execution
starts.

This spec covers document structure and root fields only. Detailed behavior for
each field and sub-DAG execution belongs in later specs.

## Inputs

Input is one YAML file passed to a Dagu-compatible data-plane runner.

The YAML file must contain one or more DAG documents.

Documents may be separated with YAML `---`.

The first document is the entrypoint DAG executed by `dagu run`.

Later documents are local DAG definitions available to sub-DAG references.

Empty documents are invalid.

Valid entrypoint example:

```yaml
name: hello
steps:
  - name: say-hello
    run: echo hello
```

## Behavior

The first DAG document is the entrypoint.

Later DAG documents must have `name`.

Later DAG documents are not executed by `dagu run` unless referenced by
sub-DAG behavior.

Each DAG document must contain `steps`.

DAG document names must be unique inside one YAML file.

`steps` must be a non-empty sequence or mapping.

Root fields not listed in this spec must be rejected in every document.

Duplicate root keys must be rejected in every document.

Field names are case-sensitive.

Field names use `snake_case`.

The data-plane YAML schema must not require scheduler, coordinator, API server,
UI, auth, queue, or history behavior.

## Root Fields

These root fields are part of the data-plane YAML schema:

| Field | Required | Value |
| --- | --- | --- |
| `name` | entrypoint: no; later documents: yes | DAG name |
| `description` | no | human-readable description |
| `working_dir` | no | default working directory |
| `params` | no | workflow parameters |
| `defaults` | no | default step settings |
| `steps` | yes | executable steps |
| `actions` | no | reusable packaged DAG actions |
| `handler_on` | no | lifecycle handler steps |
| `preconditions` | no | workflow start conditions |
| `retry_policy` | no | workflow retry policy |
| `timeout_sec` | no | workflow timeout in seconds |
| `delay_sec` | no | workflow start delay in seconds |
| `max_active_steps` | no | maximum concurrently running steps |
| `max_clean_up_time_sec` | no | cleanup timeout in seconds |
| `max_output_size` | no | maximum captured output size |
| `ssh` | no | default SSH settings |
| `kubernetes` | no | default Kubernetes settings |
| `llm` | no | default LLM settings |
| `tools` | no | required tool declarations |

Each listed field must have a dedicated spec before its behavior becomes
accepted conformance behavior.

## Outputs

YAML validation happens before workflow execution.

When validation succeeds, the runner may continue to execution.

When validation fails, no step may start.

Validation success by itself must not write workflow result events, run logs, or
artifacts. Those outputs belong to runtime specs.

## Errors

Invalid YAML must fail before execution.

An empty YAML document must fail before execution.

A missing or empty `steps` field in any DAG document must fail before
execution.

A later DAG document without `name` must fail before execution.

Duplicate DAG document names must fail before execution.

An unknown root field in any DAG document must fail before execution.

A duplicate root key in any DAG document must fail before execution.

Validation errors should identify the invalid field path when possible.

## Examples

Minimal valid workflow:

```yaml
steps:
  - name: hello
    run: echo hello
```

Valid workflow with root metadata:

```yaml
name: deploy
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
    run: ./deploy.sh ${environment} ${version}
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

## Acceptance Criteria

- A black-box fixture accepts the minimal valid workflow.
- A black-box fixture rejects a workflow with no `steps`.
- A black-box fixture rejects a workflow with empty `steps`.
- A black-box fixture rejects an unknown root field.
- A black-box fixture rejects duplicate root keys.
- A black-box fixture accepts an inline sub-DAG separated by `---`.
- A black-box fixture rejects an empty YAML document.
- A black-box fixture rejects a later DAG document without `name`.
- A black-box fixture rejects duplicate DAG document names.
- Rejected workflows do not start any step.
