# Spec: Chat Completion Action

## Status

Partially implemented.

## Scope

This spec covers `chat.completion` request construction, response output,
stream selection, model fallback, the `with.tools` agentic tool-calling
loop, and structured output through `output_schema`, using a local
OpenAI-compatible endpoint. No external model service is required.

Provider-specific parameters, timeout, abort, and UI-facing tool sub-DAG
drill-down tracking belong to executor and lifecycle coverage.

## Goal

Workflow authors can send a prompt or message history to a model and use
its response as step output.

## Behavior

A nonempty `with.prompt` becomes a user message. When both `with.prompt`
and `with.messages` are present, the prompt takes precedence. Otherwise,
`with.messages` provides the ordered message history. `with.system`, when
set, precedes the user messages.

Streaming is enabled by default. `with.stream: false` selects a
non-streaming request. Both modes write the response text to stdout,
followed by a newline.

A `with.model` array with multiple entries tries the models in order until
one succeeds. Requests for these fallback models disable streaming. A
single model does not activate fallback.

### Tool calling

`with.tools` names other DAGs as callable functions. Each named DAG's
declared parameters become the tool's JSON Schema (offered to the model as
`tools` on every request), and its name becomes the tool name. A tool-calling
request always uses a non-streaming call, regardless of `with.stream`.

When the model's response includes tool calls, each one runs the
corresponding DAG as a sub-DAG-run, passing the model's JSON arguments as
DAG params. The sub-DAG's declared outputs (`output:`) are JSON-encoded and
returned to the model as the tool result content, in a `tool` role message
addressed to the tool call's ID. The loop then sends another request
including that tool result, repeating until a response has no tool calls.

A tool call naming a DAG outside `with.tools`, or whose sub-DAG-run itself
fails, is not fatal to the step: the result content reports the problem to
the model (`tool "<name>" not found`, or the execution error), and the loop
continues to request another response.

`with.max_tool_iterations` bounds how many such request/tool-call rounds
run, default 10. Reaching the limit does not fail the step: it is a second,
equally successful termination path alongside the model responding with no
further tool calls. A step with `output_schema` is the exception; see
below.

### Structured output

A step-level `output_schema` makes the model answer in that shape. The
schema must declare `type: object`, list at least one property under
`properties`, and list every `required` name under `properties`.

Each request offers a `respond` tool whose parameters are the schema and
requires a tool call (`tool_choice: required`). The first system message
gains an instruction to answer through `respond`; when there is none, the
instruction becomes the first message. The instruction is not part of the
saved session. Structured requests never stream, regardless of
`with.stream`.

The answer is the arguments of the model's `respond` call. A reply without
that call is accepted when its text is a JSON object, optionally inside a
`json` code fence. Properties the schema does not list are dropped, and the
remaining object is validated against the schema. The step writes the
object to stdout as one line of JSON with keys in sorted order, and each
listed property becomes `${steps.<id>.outputs.<name>}`. Because only listed
properties are published, a reference to any other output of the step is
reported as unknown, even when the schema allows additional properties.

An answer that is not a JSON object or does not match the schema gets one
correction: the next request carries the rejected answer as an assistant
message without tool calls, followed by a user message stating why it was
rejected. When the corrected answer is also unusable, that model has
failed and the next `with.model` entry is tried.

With `with.tools`, the tool DAGs are offered next to `respond`. A response
that calls `respond` ends the loop; other tool calls in that response do
not run. Reaching `with.max_tool_iterations` without an answer fails that
model, like an unusable answer: the next `with.model` entry starts over,
and the step fails when no model is left.

## Errors

`dagu validate` rejects missing prompt/message input and a configured
provider without a model. It exits nonzero with an error identifying the
invalid configuration.

With `output_schema`, `dagu validate` also rejects a schema without
`type: object`, a schema that lists no properties, a `required` name that
`properties` does not list, `with.web_search`, and a tool named `respond`.

When no model gives a usable structured answer, the step fails with an
error naming the last model and stating that no answer matched
`output_schema`. The error contains no part of the rejected answers; the
step's stderr receives each rejected answer with the reason.

When the first fallback model rejects a request and the next succeeds,
the step succeeds and writes the successful response. Exhausted fallback,
provider-specific errors, and lifecycle failures are outside this
conformance scope.

## Example

```yaml
steps:
  - action: chat.completion
    with:
      provider: local
      model: local-model
      base_url: http://localhost:8080
      prompt: Summarize the supplied text.
      stream: false
```

Structured output, published as step outputs:

```yaml
steps:
  - id: classify
    action: chat.completion
    with:
      provider: local
      model: local-model
      base_url: http://localhost:8080
      prompt: "Classify this note and extract the amount: please refund my 12.50"
    output_schema:
      type: object
      properties:
        category:
          type: string
          enum: [refund, complaint, question]
        amount:
          type: number
      required: [category]

  - id: record
    depends: classify
    run: echo "${steps.classify.outputs.category}"
```

Tool calling, using another DAG in the same file as a tool:

```yaml
steps:
  - action: chat.completion
    with:
      provider: local
      model: local-model
      base_url: http://localhost:8080
      prompt: What is the weather in Paris?
      tools:
        - get-weather
      max_tool_iterations: 5
---
name: get-weather
params: CITY
steps:
  - command: fetch-weather.sh "$CITY"
    output: WEATHER_JSON
```
