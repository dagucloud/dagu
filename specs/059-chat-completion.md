# Spec: Chat Completion Action

## Status

Partially implemented.

## Scope

This spec covers `chat.completion` request construction, response output,
stream selection, model fallback, the agentic tool-calling
loop of `with.dags` and `with.tools`, and structured output through `output_schema`, using a local
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

`with.dags` names other DAGs as callable functions. Each entry has a `name` and
optional `params`. The list form of `tools` (a list
of DAG names) is deprecated. Use the key `dags`. The list form stays valid in
`with.tools` and in the `llm` block. Each name is an entry of `dags` with no
params, and the tool name is the name of the loaded DAG. See
[The list form of tools](090-agent-tools.md#the-list-form-of-tools-deprecated).
Each named DAG's declared parameters become the tool's JSON Schema (offered to
the model as `tools` on every request), and its name becomes the tool name.
[Spec 090](090-agent-tools.md) defines the key `dags`, the tool map with the
category `mcp`, and fixed inputs. A
tool-calling request always uses a non-streaming call, regardless of
`with.stream`.

When the model's response includes tool calls, each one runs the
corresponding DAG as a sub-DAG-run, passing the model's JSON arguments as
DAG params. The sub-DAG's declared outputs (`output:`) are JSON-encoded and
returned to the model as the tool result content, in a `tool` role message
addressed to the tool call's ID. The loop then sends another request
including that tool result, repeating until a response has no tool calls.

A tool call naming a tool outside the catalog, or whose sub-DAG-run itself
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

With `with.dags`, the tool DAGs are offered next to `respond`. A response
that calls `respond` ends the loop; other tool calls in that response do
not run. Reaching `with.max_tool_iterations` without an answer fails that
model, like an unusable answer: the next `with.model` entry starts over,
and the step fails when no model is left.

### Files

`with.files` names the files the model reads with the prompt: one path or
glob pattern, several of them as a list or one per line, or a list an
earlier step published, such as the emails `mail.search` found with
`save_attachments: true`. A leading `~` is the home directory, and a
relative path is under the step's working directory. Each file is read
once, in the order named, before any request.

A text file (`.txt`, `.md`, `.csv`, `.tsv`, `.json`, `.jsonl`, `.yaml`,
`.yml`, `.xml`, `.html`, `.htm`, `.log`), a Word document (`.docx`) and an
Excel workbook (`.xlsx`, `.xlsm`) are given as text, a paragraph or row a
line; a text file in Shift_JIS is decoded. A PDF is given page by page,
and a page without text of its own, such as a scan, is rendered to an
image. An image file (PNG, JPEG, GIF, WebP) is given as an image, scaled
down to a long edge of 1568 pixels when larger. Files of other kinds, a
file over 50 MB, a missing file, and a pattern matching nothing fail the
step before any request.

The text goes ahead of the step's last user message, each file in a
`<file name="…">` block, a PDF's pages set apart by `--- page N ---`
lines; the images go with that message, in the order they are named, and
the text names each by its place: `[Image 1 is receipt.png.]`, or
`--- page 2: image 1 ---` for a scanned page. Together the files may hold
up to 1,000,000 characters of text and 50 images.

The session keeps the text, so a later step of the session still reads it;
the images go with the one request and are not kept. A push-back resends
the feedback without the files. `files` is refused in a DAG-level `llm`
block, since it belongs with the prompt it goes with.

## Keys and endpoints

Status: Implemented. [Spec 094](094-ai-gateway.md) defines this section.

The new method is the block `with.endpoint`. It names an endpoint of the AI page, a model and an
optional effort. The administrator adds the key one time on that page. The key stays in the
coordinator, and the step never reads it.

```yaml
steps:
  - action: chat.completion
    with:
      endpoint:
        name: cle1
        model: claude-sonnet-4-6
        effort: high
      prompt: Summarize the supplied text.
```

The provider type comes from the endpoint, so `with.provider` is not needed. The key `name` and the key
`model` are required. A value reference is valid in `model` and not valid in `name`. The key `effort`
is optional. It is a literal, one of `low`, `medium`, `high`, `xhigh` and `max`. An `effort` has the same
effect as `thinking: { enabled: true, effort: <effort> }`. The block is closed: an unknown key is a
validation error. A value that is not a block is a validation error too.

A step that needs a fallback uses the list form of `with.model`. Each entry of the list names its own
`endpoint`, or it uses the old method with `provider`. An entry with neither is a validation error.
The block has no list.

The old method is deprecated. It stays valid and does not change. It uses the keys `provider`, `model`,
`base_url`, `api_key_name` and `thinking`. The key is then in the environment of the step, and the call
goes from the step process to the provider. Use the block `endpoint` in a new step.

`dagu validate` prints one warning for each step that uses `provider`, `base_url` or `api_key_name` in
`with` or in a row of a model list. The warning names each such key:

```text
Deprecated DAG syntax: steps[0].with.provider, steps[0].with.api_key_name: use with.endpoint, an endpoint of the AI page
```

The warning does not change the exit code. The loader gives no warning and does not change the old
method. The Model node of the canvas marks a provider of the old method as deprecated.

The keys `model`, `thinking`, `provider`, `base_url` and `api_key_name` of `with` contradict the block
`endpoint`. Each one of these keys in the same `with` is a validation error. The same holds for `endpoint` with `api_key_name`, `base_url` or
`provider` in the same model entry.

The block `endpoint` is valid in `with`. The key `endpoint` is valid in an entry of a model list. Neither
is valid in the key `llm` of a DAG, in an agent DAG, or in the `llm` block of another action.

When the step starts, Dagu checks each endpoint of its models. A failed check fails the step before
the first request, with the reason of Spec 094. Each model that names an endpoint sends its requests
to a relay that exists for that attempt only. Streaming, tool calling, structured output and model
fallback work as they do with the old method. A fallback entry that names another endpoint uses
its own relay.

The start check also looks for the model of each entry that has an endpoint in the models list of that
endpoint. A model that the list lacks fails the step before the first request, with the reason
`model_unknown`. A chat step has no model alias. See [Spec 093](093-ai-endpoints.md#use-in-a-step).

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

`dagu validate` also rejects the block `endpoint` with `api_key_name`, `base_url`, `provider`, `model` or
`thinking` in the same `with`. The error is `key "endpoint" and key "<key>" contradict each other;
<reason>`. The reason of `model` is `the block endpoint gives the model`, and the reason of `thinking`
is `the block endpoint gives the effort`. It rejects `endpoint` with `api_key_name`, `base_url` or `provider` in the
same model entry too. It rejects a value that is not a block, and a block with no `name` or no `model`.
It rejects an unknown key of the block, and an `effort` that is not one of the five levels. The error names the key and
the path. An entry of a model list with no `provider` and no `endpoint` gives the error
`model[<n>].provider: required, unless the model names an endpoint`. `dagu validate` does not check that
the endpoint exists, because it does not read the store.

When the first fallback model rejects a request and the next succeeds,
the step succeeds and writes the successful response. Exhausted fallback,
provider-specific errors, and lifecycle failures are outside this
conformance scope.

## Example

The examples use the deprecated old method with a local endpoint, because it needs no AI page. Use the
block `endpoint` in a new step.

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

Reading an invoice, PDF or photographed, into typed values:

```yaml
steps:
  - id: read
    action: chat.completion
    with:
      provider: local
      model: local-model
      base_url: http://localhost:8080
      prompt: Read the invoice and give its number, date, and total.
      files: ~/Downloads/invoice-*.pdf
    output_schema:
      type: object
      properties:
        number: {type: string}
        date: {type: string}
        total: {type: number}
      required: [number]
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
      dags:
        - name: get-weather
      max_tool_iterations: 5
---
name: get-weather
params: CITY
steps:
  - command: fetch-weather.sh "$CITY"
    output: WEATHER_JSON
```
