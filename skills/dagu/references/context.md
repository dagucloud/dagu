# Context References, Scoped Values, And Step Outputs

Use `${context.*}` when DAG YAML needs metadata about the current DAG run. These values are resolved by Dagu before the step executor runs.

If a supported context value is not available in the current scope, Dagu keeps the original reference text. It does not replace the reference with an empty string.

## Scoped Value References

Use scoped references when Dagu should resolve a named value before execution.

| Reference | Meaning |
| --- | --- |
| `${consts.NAME}` | Top-level `consts:` value |
| `${params.NAME}` | Runtime parameter value |
| `${env.NAME}` | Value in Dagu's runtime environment scope for the current step |
| `${steps.<step_id>.outputs.<name>}` | Declared step output written through `$DAGU_OUTPUT_FILE` |

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

## Supported Context References

Run and step metadata:

| Reference | Meaning |
| --- | --- |
| `${context.dag.name}` | Current DAG name |
| `${context.run.id}` | Current DAG run ID |
| `${context.run.status}` | Current run status when status is available, such as lifecycle handler scopes |
| `${context.run.scheduled_at}` | Scheduled time for a scheduled run |
| `${context.run.root_name}` | Root DAG name when this run is part of a nested run |
| `${context.run.root_id}` | Root run ID when this run is part of a nested run |
| `${context.attempt.id}` | Current attempt ID |
| `${context.attempt.started_at}` | Current attempt start time |
| `${context.step.id}` | Current step ID |
| `${context.step.name}` | Current step name |
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

## Declared Step Outputs

A step can declare outputs and write them during execution. Dagu reads the output file after the command succeeds and publishes the values as `${steps.<step_id>.outputs.<name>}`.

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
