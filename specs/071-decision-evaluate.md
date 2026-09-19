# Spec: Decision Evaluation Action

## Status

Implemented.

## Scope

This spec defines `decision.evaluate`, its provider connection, typed questions,
response validation, and automatic or explicit output capture.

## Goal

Workflow authors can classify, score, and evaluate yes/no questions against
shared context, then route execution using the structured answers.

## Behavior

`with.provider`, `with.model`, `with.state`, and `with.questions` are required.
Connection settings belong to the action; the DAG-level `llm` block is not used.
The model is a single string passed unchanged to the endpoint. State accepts a
string, object, or array. Questions are a nonempty map of IDs to questions.

Each question has `type`, `instructions` (string, object, or array), and
type-specific `criteria`:

- `choice`: 2–255 named options, each with a string description or null.
- `score`: 2–10 string descriptions ordered from level zero upward.
- `noul`: an optional object with both `true` and `false` string descriptions.

Question IDs correlate answers; instructions contain the question's meaning.
Standard runtime references resolve in provider, model, base URL, and string
leaves of state, instructions, and criteria. Object and array structure is
preserved. The API key variable name is a literal environment variable name.

### Connection

| Provider | Default base URL | Request path | Default API key variable |
| --- | --- | --- | --- |
| `openrouter` | `https://openrouter.ai/api/alpha` | `/decisions` | `OPENROUTER_API_KEY` |
| `typesafe` | `https://api.typesafe.ai/v1` | `/systemone` | `TYPESAFE_API_KEY` |

`with.base_url` overrides the API root, including its version prefix. A trailing
slash is accepted. Query and fragment delimiters, including empty trailing
`?` and `#`, are rejected. The URL must use HTTPS, except HTTP is allowed for
localhost and loopback IP addresses. `with.api_key_name` optionally names another
environment variable; it is not the key's value.
The key is resolved from the workflow environment, including declared secrets.
An absent or empty key fails before sending a request.

Requests use JSON HTTP POST with bearer authentication. The body contains only
`model`, `state`, and `questions`. All questions share one request. Requests
use the existing LLM HTTP retry policy and respect step timeout and cancellation.
Without a step timeout a request has no deadline of its own.
There is no streaming, model fallback, chat history, or tool execution.

### Answers and outputs

A successful response is one JSON object containing `model`, `answers`, and
`usage`. Each requested question must have an answer of the matching type:

- `choice`: a selected criterion, probabilities for every option, and confidence.
- `score`: a numeric score within the configured scale, probabilities and legend
  entries for every level, and confidence.
- `noul`: a probability between zero and one, not a boolean.

Probabilities and confidence must be numbers between zero and one. Low
confidence is a successful result, not an execution error. Provider metadata
is retained, including the precision and representation of JSON numbers through
capture and persistence. Direct output references retain the precision of every
number and the representation of integers a float64 cannot hold exactly.
Whitespace and object-key order may change. Diagnostics do not appear in the JSON stdout response.

Without explicit output configuration, Dagu captures the response automatically.
The existing `${classify.output.answers.department.choice}` JSON lookup works
for a dependent step. Top-level `answers`, `model`, and `usage` are also named
outputs accessible through `${steps.classify.outputs.answers}` and equivalent
top-level references. Strict step-output references do not support nested paths.

Explicit `output`, `output_schema`, or `stdout.outputs` uses existing capture
semantics instead of the automatic default. `output: RESULT` captures the raw
response, and object-form output can select or rename fields. Output size limits,
secret masking, and persisted run output behavior apply. The raw provider
response is also bounded by `max_output_size` (1 MiB by default), before JSON
decoding or masking. An oversized response fails without publishing output.
Failed attempts publish no successful named outputs. Each retry captures only its own response.

## Errors

Missing required fields, unsupported providers or question types, invalid
criteria, and invalid literal URLs fail validation. Values containing runtime
references are checked after resolution. Unknown action configuration fields
are rejected. Invalid JSON responses, missing answers, mismatched answer types,
invalid answer fields, exhausted HTTP retries, cancellation, and timeout fail
the step. Response validation errors must not include the response content.

## Example

```yaml
type: graph
secrets:
  - name: OPENROUTER_API_KEY
    provider: env
    key: OPENROUTER_API_KEY
steps:
  - id: classify
    action: decision.evaluate
    with:
      provider: openrouter
      model: typesafe/jev-1.13
      state: I was charged twice.
      questions:
        department:
          type: choice
          instructions: Which department should handle this request?
          criteria:
            billing: Charges and refunds
            other: Anything else
  - id: consume
    depends: [classify]
    action: log.write
    with:
      message: ${classify.output.answers.department.choice}
```
