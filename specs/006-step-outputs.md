# Spec: Step Outputs

Scope: declared step outputs, output file parsing, and output references.

## Objective

Define how a step publishes values for later steps without parsing stdout, stderr, or logs.

## Inputs

Input is a workflow YAML file accepted by the YAML schema spec.

Step output validation extends:

```sh
dagu workflow validate <workflow_file>
```

Workflow execution uses:

```sh
dagu run <workflow_file>
```

**Command behavior:**

- When this spec is implemented, `dagu workflow validate` validates output declarations and output references.
- `dagu workflow validate` must not execute steps.

**A step may declare outputs:**

```yaml
steps:
  - id: build
    run: |
      printf 'image_tag=v1.2.3\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: image_tag
```

## Behavior

**Output declaration rules:**

- `outputs` is an optional step field.
- When present, `outputs` must be a non-empty sequence.
- A step with `outputs` must define `id`.
- Output names must be unique inside one step.
- Unknown output item fields are invalid.
- JSON Schema is not part of this spec.

**Output item fields:**

| Field | Required | Rules |
| --- | --- | --- |
| `name` | Yes. | Must match `[a-z][a-z0-9_]*`. |
| `type` | No. | Must be a string when present. Defaults to `string`. Supported values are `string` and `json`. |

**Output reference rules:**

An output reference must point to a declared output:

```text
${{ steps.step_id.outputs.output_name }}
```

- Output references use the step `id` rules from the step reference spec.
- Output references do not create dependencies.
- Fields without an owning step must not reference step outputs.
- The step containing an output reference must depend directly or transitively on the producing step.
- An output reference to the owning step's own output is invalid.
- Output names are scoped to the producing step.
- Step outputs are scoped to one DAG document.
- Inline sub-DAG documents have independent output scopes.

### Output File

**Output file lifecycle:**

- For each step attempt, Dagu creates an empty output file before the executor starts.
- Dagu exposes the output file path to the executor as `DAGU_OUTPUT_FILE`.
- Executor specs define how `DAGU_OUTPUT_FILE` is exposed for each executor.
- Dagu must not read stdout or stderr as step outputs.
- Dagu parses the output file only after the executor exits successfully.
- If the executor fails, aborts, or times out, Dagu must not publish outputs from that attempt.
- If output parsing fails, the attempt fails.
- Normal retry behavior may retry a failed attempt.
- Failed attempts publish no outputs.
- Only the attempt that makes the step successful publishes outputs.
- Each retry attempt receives a new empty output file.

### Output File Format

Dagu parses the output file as UTF-8 text. Invalid UTF-8 fails output parsing. Line endings are normalized to `\n` before parsing.

**Output record formats:**

| Format | Syntax | Value |
| --- | --- | --- |
| Single-line | `name=value` | Text after the first `=`. The line ending is not part of the value. Empty values are valid. |
| Multi-line | `name<<DELIMITER` followed by value lines and a closing `DELIMITER` line. | Text between the line ending after the opening record and the line ending before the delimiter line. |

**Single-line record rules:**

- The output name is the text before the first `=`.
- The output value is the text after the first `=`.
- The line ending is not part of the value.
- Empty values are valid.

**Multi-line record rules:**

- The delimiter must match `[A-Za-z_][A-Za-z0-9_]*`.
- The delimiter line must match the delimiter exactly.
- The delimiter line ending is not part of the value.
- An unclosed multi-line record fails output parsing.

**Record validation rules:**

- An empty line outside a multi-line value is invalid.
- Each emitted output name must be declared by the step.
- Each declared output must be emitted exactly once.
- An undeclared emitted output fails output parsing.
- A duplicate emitted output fails output parsing.

### Output Values

**Type behavior:**

| Type | Behavior |
| --- | --- |
| `string` | Stored as emitted text after line ending normalization. |
| `json` | Must parse as JSON. Any JSON value is valid: object, array, string, number, boolean, or null. |

**JSON and size rules:**

- Invalid JSON fails output parsing.
- JSON output references insert the emitted JSON text after line ending normalization.
- If `max_output_size` is specified, output file content larger than that many bytes fails output parsing.

## Outputs

Successful output parsing publishes declared step outputs for later step value resolution.

**Published output reference form:**

```text
${{ steps.step_id.outputs.output_name }}
```

**Output rules:**

- Step outputs are not stdout, stderr, logs, or artifacts.
- This spec does not define durable result storage format.

## Errors

**Validation errors:**

- Invalid `outputs` shape must fail during workflow validation.
- Missing output `name` must fail during workflow validation.
- Invalid output `name` syntax must fail during workflow validation.
- Duplicate output names in one step must fail during workflow validation.
- Unknown output item fields must fail during workflow validation.
- Invalid output `type` must fail during workflow validation.
- A step with `outputs` but no `id` must fail during workflow validation.
- An output reference to an undeclared output must fail during workflow validation.
- An output reference without a direct or transitive dependency on the producing step must fail during workflow validation.

**Runtime output errors:**

- Invalid output file UTF-8 must fail the producing attempt.
- Invalid output file syntax must fail the producing attempt.
- A missing declared output must fail the producing attempt.
- An undeclared emitted output must fail the producing attempt.
- A duplicate emitted output must fail the producing attempt.
- Invalid JSON for a `json` output must fail the producing attempt.

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
    run: ./deploy.sh ${{ steps.build.outputs.image_tag }}
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
    run: printf '%s\n' '${{ steps.inspect.outputs.metadata }}'
```

Runtime failure because stdout is not an output:

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
    run: ./deploy.sh ${{ steps.build.outputs.image_tag }}
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
    run: ./deploy.sh ${{ steps.build.outputs.image_tag }}
```

## Acceptance Criteria

- A black-box fixture verifies `dagu workflow validate` accepts a step with a declared output.
- A black-box fixture verifies `dagu workflow validate` rejects invalid output names.
- A black-box fixture verifies `dagu workflow validate` rejects duplicate output names in one step.
- A black-box fixture verifies `dagu workflow validate` rejects invalid output types.
- A black-box fixture verifies `dagu workflow validate` rejects a step with `outputs` but no `id`.
- A black-box fixture verifies `dagu workflow validate` rejects an output reference to an undeclared output.
- A black-box fixture verifies `dagu workflow validate` rejects an output reference without a direct or transitive dependency on the producing step.
- A black-box fixture verifies `dagu run` resolves a string output emitted through `DAGU_OUTPUT_FILE`.
- A black-box fixture verifies `dagu run` resolves a multi-line output emitted through `DAGU_OUTPUT_FILE`.
- A black-box fixture verifies `dagu run` does not resolve stdout as a step output.
- A black-box fixture verifies `dagu run` fails when a declared output is missing.
- A black-box fixture verifies `dagu run` fails when an undeclared output is emitted.
- A black-box fixture verifies `dagu run` fails when an output is emitted twice.
- A black-box fixture verifies `dagu run` resolves a valid JSON output.
- A black-box fixture verifies `dagu run` fails when a `json` output emits invalid JSON.
- A black-box fixture verifies retry publishes only outputs from the successful attempt.
