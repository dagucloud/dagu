# Spec: Step Outputs

## Scope

This spec defines the step-level top-level `outputs` field, `DAGU_OUTPUT_FILE`, and output publication.

The field shape is:

```yaml
steps:
  - id: build
    outputs:
      - name: image_tag
```

It does not define the existing singular `output` field, `stdout.outputs`, `outputs.write`, or the `outputs` schema in `dagu-action.yaml`.

Value-resolution references to published outputs are defined by [Spec 007: Value Resolution Steps](007-value-resolution-steps.md).

## Goal

Define a file-based way for a step to publish named values for later steps.

The producing step writes records to `DAGU_OUTPUT_FILE`. Dagu parses that file after the step succeeds. Later steps read the values through value resolution.

## Input

Input is a workflow YAML file accepted by the YAML schema spec.

Step output validation extends:

```sh
dagu validate <path/to/dag_file>
```

Workflow execution uses:

```sh
dagu run <workflow_target>
```

Rules:

- Validation checks output declarations.
- Validation must not execute steps.

Example:

```yaml
steps:
  - id: build
    run: |
      printf 'image_tag=v1.2.3\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: image_tag
```

## Output declarations

`outputs` is an optional step field.

Rules:

- When present, `outputs` must be a non-empty sequence.
- A step with `outputs` must define `id`.
- Output names must be unique inside one step.
- Unknown output item fields are invalid.
- JSON Schema is not part of this spec.

Output item fields:

| Field | Required | Rules |
| --- | --- | --- |
| `name` | Yes | Must match `^[A-Za-z][A-Za-z0-9_]*$`. |
| `type` | No | Must be `string` or `json` when present. Defaults to `string`. |

## Output reference inputs

Value-resolution references to step outputs use the published output names from this spec.

Rules:

- Output names are scoped to the producing step.
- Step outputs are scoped to one DAG document.
- Inline sub-DAG documents have independent output scopes.
- Reference syntax, dependency requirements, and runtime lookup behavior are defined by [Spec 007: Value Resolution Steps](007-value-resolution-steps.md).

## Output file lifecycle

For each step attempt, Dagu creates an empty output file before the executor starts.

Rules:

- Dagu exposes the output file path to the executor as `DAGU_OUTPUT_FILE`.
- Executor specs define how each executor receives `DAGU_OUTPUT_FILE`.
- Dagu must not read stdout, stderr, or logs as step outputs.
- Dagu parses the output file only after the executor exits successfully.
- If the executor fails, aborts, or times out, Dagu does not publish outputs from that attempt.
- If output parsing fails, the attempt fails.
- Normal retry behavior may retry the failed attempt.
- Failed attempts publish no outputs.
- Only the attempt that makes the step successful publishes outputs.
- Each retry attempt receives a new empty output file.

## Output file format

Dagu parses the output file as UTF-8 text. Invalid UTF-8 fails output parsing. Line endings are normalized to `\n` before parsing.

Record formats:

| Format | Syntax | Value |
| --- | --- | --- |
| Single-line | `name=value` | Text after the first `=`. The line ending is not part of the value. Empty values are valid. |
| Multi-line | `name<<DELIMITER`, value lines, then a closing `DELIMITER` line. | Text between the line ending after the opening record and the line ending before the delimiter line. |

Single-line rules:

- The output name is the text before the first `=`.
- The output value is the text after the first `=`.
- The line ending is not part of the value.
- Empty values are valid.

Multi-line rules:

- The delimiter must match `[A-Za-z_][A-Za-z0-9_]*`.
- The delimiter line must match the delimiter exactly.
- The delimiter line ending is not part of the value.
- An unclosed multi-line record fails output parsing.

Record validation rules:

- An empty line outside a multi-line value is invalid.
- Each emitted output name must be declared by the step.
- Each declared output must be emitted exactly once.
- An undeclared emitted output fails output parsing.
- A duplicate emitted output fails output parsing.

## Output values

Type behavior:

| Type | Behavior |
| --- | --- |
| `string` | Stored as emitted text after line ending normalization. |
| `json` | Must parse as JSON. Any JSON value is valid: object, array, string, number, boolean, or null. |

Rules:

- Invalid JSON fails output parsing.
- A JSON output reference inserts the emitted JSON text after line ending normalization.
- If `max_output_size` is specified, output file content larger than that many bytes fails output parsing.

## Outputs

After successful parsing, Dagu publishes declared step outputs for later value resolution.

Published reference form:

```text
${steps.step_id.outputs.output_name}
```

Step outputs are not stdout, stderr, logs, artifacts, or durable result storage. This spec does not define how the published values are stored on disk.

## Errors

Validation must fail when:

- `outputs` has an invalid shape.
- An output item omits `name`.
- An output `name` has invalid syntax.
- One step declares the same output name more than once.
- An output item contains an unknown field.
- An output item uses an invalid `type`.
- A step declares `outputs` but has no `id`.

Runtime output parsing must fail when:

- The output file is not valid UTF-8.
- The output file syntax is invalid.
- A declared output is missing.
- The output file emits an undeclared output.
- The output file emits the same output more than once.
- A `json` output emits invalid JSON.

## Examples

Valid string output:

```yaml
steps:
  - id: build
    run: |
      printf 'image_tag=v1.2.3\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: image_tag

  - id: deploy
    depends: build
    run: ./deploy.sh ${steps.build.outputs.image_tag}
```

Valid JSON output:

```yaml
steps:
  - id: inspect
    run: |
      cat >> "$DAGU_OUTPUT_FILE" <<'EOF'
      metadata<<JSON
      {"image":"api","tag":"v1.2.3"}
      JSON
      EOF
    outputs:
      - name: metadata
        type: json

  - id: print
    depends: inspect
    run: printf '%s\n' '${steps.inspect.outputs.metadata}'
```

Runtime failure because stdout is not a step output:

```yaml
steps:
  - id: build
    run: echo image_tag=v1.2.3
    outputs:
      - name: image_tag
```

Invalid undeclared output reference:

```yaml
steps:
  - id: build
    run: echo ok
  - id: deploy
    depends: build
    run: ./deploy.sh ${steps.build.outputs.image_tag}
```

Invalid missing dependency:

```yaml
steps:
  - id: build
    run: |
      printf 'image_tag=v1.2.3\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: image_tag
  - id: deploy
    run: ./deploy.sh ${steps.build.outputs.image_tag}
```

## Acceptance criteria

- A black-box fixture verifies `dagu validate` accepts a step with a declared output.
- A black-box fixture verifies `dagu validate` rejects invalid output names.
- A black-box fixture verifies `dagu validate` rejects duplicate output names in one step.
- A black-box fixture verifies `dagu validate` rejects invalid output types.
- A black-box fixture verifies `dagu validate` rejects a step with `outputs` but no `id`.
- A black-box fixture verifies `dagu validate` rejects an output reference to an undeclared output.
- A black-box fixture verifies `dagu validate` rejects an output reference without a direct or transitive dependency on the producing step.
- A black-box fixture verifies `dagu run` resolves a string output emitted through `DAGU_OUTPUT_FILE`.
- A black-box fixture verifies `dagu run` resolves a multi-line output emitted through `DAGU_OUTPUT_FILE`.
- A black-box fixture verifies `dagu run` does not resolve stdout as a step output.
- A black-box fixture verifies `dagu run` fails when a declared output is missing.
- A black-box fixture verifies `dagu run` fails when an undeclared output is emitted.
- A black-box fixture verifies `dagu run` fails when an output is emitted twice.
- A black-box fixture verifies `dagu run` resolves a valid JSON output.
- A black-box fixture verifies `dagu run` fails when a `json` output emits invalid JSON.
- A black-box fixture verifies retry publishes only outputs from the successful attempt.
