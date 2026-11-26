# Spec: DAG Outputs

## Status

Implemented.

## Scope

This spec defines the root-level `outputs` field of a workflow: a result contract that maps named results to published step outputs, its validation, its persistence with a run, and the projection of a run's results.

It does not define how any API, protocol adapter, UI, or sub-DAG step exposes or consumes the results. It does not change step outputs (Spec 012), value resolution (Spec 003, Spec 007), or `dagu-action.yaml` `outputs`.

## Goal

Let a DAG declare which step outputs are its reusable result, so a reader can build the result from a finished run without scanning steps. Readers include protocol adapters such as RFC 023's A2A agents.

## Behavior

### Field

```yaml
outputs:
  summary: ${diagnose.outputs.summary}
  findings: ${steps.diagnose.outputs.findings}

steps:
  - id: diagnose
    run: ./diagnose.sh
    outputs:
      - name: summary
      - name: findings
        type: json
```

Rules:

- `outputs` is an optional root field. It is a mapping of result name to reference. An empty or absent mapping declares no results.
- A result name matches `^[A-Za-z][A-Za-z0-9_]*$`, the rule for step output names (Spec 012).
- A value is exactly one step-output reference and nothing else. Two forms are accepted:
  - the short form `${<step_id>.outputs.<name>}`
  - the canonical form `${steps.<step_id>.outputs.<name>}` of Spec 007
- Both forms mean the same. Dagu normalizes a declaration to the producing step ID and output name, and stores that normalized form with the run.
- `step_id` and `name` match `^[A-Za-z][A-Za-z0-9_]*$`. Surrounding whitespace inside the braces is ignored.
- The reference names a top-level step of the same DAG document by `id`. Steps of `foreach.steps` bodies and `handler_on` steps are not in the top-level scope and are invalid targets. Inline sub-DAG documents have independent scopes: each document declares and validates its own `outputs`, and the root document cannot name a step of another document.
- The step must declare the output through its `outputs` contract, or through another mechanism that publishes statically known names (Spec 007).
- A step whose published names are known only at run time, such as a sub-DAG step, an action resolved from a manifest, or a step whose output schema carries no inline properties, cannot be checked for the output name. Validation accepts any well-formed name for such a step. A run that never publishes the value leaves the result out (see Projection).

### Not value resolution

- Root `outputs` is not a value-resolution field (Spec 003). Dagu does not resolve its references, adds no inferred dependency (Spec 007) from them, and never reports passive notices for them.
- Adding root `outputs` does not change when any step runs.
- A DAG without root `outputs` behaves as before.

### Stored definition

- The normalized declaration is part of the DAG definition stored with the run. A result is built from that stored definition, so editing the source DAG later does not change the contract of an existing run.

### Projection

A result projection takes a run's stored DAG definition and its run status and returns the results as a mapping of result name to JSON value. It reads no other store and writes nothing.

Rules:

- A projection is built only when the run status is `succeeded` or `partially_succeeded`. For any other status (`failed`, `aborted`, `rejected`, `queued`, `running`, `waiting`, `not_started`) it returns no results.
- The value of a result is the output the referenced step published in that run.
- A run stores each declared output as text. An output declared `type: json` is embedded as the JSON value that text parses to; text that is not valid JSON stays a JSON string, and a stored value that is not text is embedded as it is. An output of type `string`, the default, is a JSON string; a stored value that is not text becomes its compact JSON text. An output of a step with run-time-only names has no declared type and is embedded as the JSON value it was published as.
- A result is omitted when its step published no value, for example because the step was skipped or did not succeed.
- A result is omitted when secret masking changed the stored output text (`MaskedOutputs`), because the original value is not stored and the masked text is not the value. A reader must not substitute the masked text.
- The projection does not apply `max_output_size`. The limit applies to each step's captured output when the step runs. A reader that needs a bound on the whole result measures the encoded JSON of the projection against its own limit, and owns what it reports when the result exceeds it.

## Errors

Validation must fail, naming the field path `outputs.<name>`, when:

- `outputs` is not a mapping.
- A result name is invalid.
- A value is not a string, or is not exactly one step-output reference: a literal, a reference embedded in other text, several references, an escaped `\${...}` reference, a nested path such as `${build.outputs.meta.tag}`, or an unsupported form.
- The referenced step ID is not found among the top-level steps (it does not exist, has no `id`, or is a `foreach` body step or a handler step).
- The step has statically known output names and does not declare the referenced output.

Validation must not start any step.

## Examples

Valid, short and canonical forms:

```yaml
outputs:
  a: ${build.outputs.tag}
  b: ${steps.build.outputs.tag}
steps:
  - id: build
    run: echo ok
    outputs:
      - name: tag
```

Invalid, literal:

```yaml
outputs:
  tag: v1
steps:
  - id: build
    run: echo ok
```

Invalid, undeclared output:

```yaml
outputs:
  tag: ${build.outputs.missing}
steps:
  - id: build
    run: echo ok
    outputs:
      - name: tag
```

Projection of a succeeded run whose `diagnose` step published `summary=ok` and `findings={"n":1}` (JSON):

```json
{"summary":"ok","findings":{"n":1}}
```
