# Spec: Chat Completion Action

## Status

Implemented.

This spec defines conformance behavior for the built-in `chat.completion`
action.

## Scope

This spec covers:

- `with.prompt` (a single user message) and `with.messages` (an array of
  role/content pairs), exactly one of which is required
- `with.system`, sent as the first message ahead of any `with.messages`
- connection and model selection: `with.provider`, `with.model`,
  `with.base_url`, and that `with.model` may be a single model name or
  an array of model entries for fallback
- `with.stream` (default `true`): streamed output written as it
  arrives, versus a single write when `false` -- both producing the
  same final content
- `with.temperature`, `with.max_tokens`, `with.top_p`, forwarded on the
  underlying request
- model fallback: when `with.model` is an array, each entry is tried in
  order until one succeeds, and streaming is disabled for the whole
  request regardless of `with.stream`
- `with.tools`: naming other DAGs (including ones defined locally in
  the same file) as callable tools, and that a tool-calling request is
  never streamed regardless of `with.stream`
- that most `with:`/step-level `llm:` configuration errors this spec
  documents are rejected at DAG-build-time validation by dedicated Go
  validation code (not only the registered JSON Schema), except
  omitting the LLM configuration entirely, which surfaces only at
  runtime since `chat.completion` has no registered step validator
- validation and runtime errors

This spec does not define:

- behavior against a real cloud LLM provider (OpenAI, Anthropic,
  Gemini, OpenRouter); this spec's own tests run against a local
  OpenAI-compatible mock server, using `with.provider: local`
- extended thinking/reasoning (`with.thinking`), provider-native web
  search (`with.web_search`), or agent-only context management fields
  (`max_context_tokens`, `observation_max_bytes`,
  `observation_keep_recent`)
- the full tool-calling loop beyond one round trip (a tool call
  followed by a final response); multi-round loops, the max-iterations
  limit, and multiple simultaneous tool calls are configuration surface
  this spec does not exercise
- `type: chat` direct authoring

## Goal

Workflow authors call an LLM as a DAG step -- sending a prompt or a
message history, optionally letting the model call other DAGs as
tools -- without hand-rolling the request against a specific provider's
API.

## Behavior

### Input

Exactly one of `with.prompt` (a string, becoming a single `user`
message) or `with.messages` (a non-empty array of `{role, content}`
entries) is required. `with.system`, when set, is sent as the first
message, ahead of any messages from `with.messages`.

### Connection and model

`with.provider` selects the provider (`openai`, `anthropic`, `gemini`,
`local`, and others); `with.model` names the model. `with.base_url`
overrides the provider's default endpoint -- for `with.provider: local`,
this points at any OpenAI-compatible chat completions server (Ollama,
vLLM, or a custom implementation), with no API key required.
`with.model` may instead be an array of `{provider, name, ...}` entries;
when it is, each is tried in order until one succeeds, and streaming is
disabled for the entire request regardless of `with.stream` (mixing
streamed output from different models in sequence would corrupt the
step's stdout).

### Streaming

`with.stream` defaults to `true`: response content is written to
stdout as it arrives from the provider. With `with.stream: false`, the
executor waits for the complete response and writes it once. Both
paths write the same final content, followed by a trailing newline.

### Tool calling

`with.tools` names other DAGs (by name; a DAG defined locally in the
same file via a `---`-separated document is found the same way a
`dag`-type sub-DAG step would find it) as callable tools. When the
model responds with a tool call, the named DAG runs with the call's
arguments as parameters, and its declared step outputs (JSON-encoded)
become the tool result content fed back to the model on the next
request. A tool-calling request always uses the non-streaming request
path, regardless of `with.stream`.

## Errors

### Validation

Whenever a step's `with:` (beyond `with.prompt`/`with.messages`) sets
any LLM-related field, dedicated Go validation code -- not just the
registered JSON Schema -- checks it at DAG-build-time validation (`dagu
validate`), rejecting every one of these before the step runs:

- Neither `with.prompt` nor `with.messages` set: an error containing
  `"chat.completion requires with.prompt or with.messages"`.
- `with.provider` set to an unrecognized value: an error containing
  `"invalid provider"`.
- Any LLM field set without `with.model`: an error containing `"model
  must be specified when llm config is provided"`.
- `with.temperature` outside `0.0`-`2.0`: an error containing
  `"temperature must be between 0.0 and 2.0"`.
- `with.max_tokens` below `1`: an error containing `"max_tokens must be
  at least 1"`.
- `with.top_p` outside `0.0`-`1.0`: an error containing `"top_p must be
  between 0.0 and 1.0"`.

### Runtime

- No LLM configuration at all (a step with only `with.prompt`/
  `with.messages` and nothing else under `with:`, and no DAG-level
  `llm:` block): an error containing `"llm configuration is required
  for chat step"` -- this is the one case none of the validation checks
  above catch, since no LLM-related field is present for them to
  inspect.

## Related Specs

- Run context: [Spec 017: Built-In Run Context](017-built-in-run-context.md)
- Agent DAGs, which build on chat completion for multi-step reasoning:
  [Spec 032: Agent DAGs](032-agent-dag.md)

## Examples

Summarize input text with a system prompt:

```yaml
llm:
  provider: openai
  model: gpt-4o
  system: "Summarize the input in one sentence."
steps:
  - action: chat.completion
    with:
      prompt: "$ARTICLE_TEXT"
```

Let the model call another DAG as a tool, with a fallback model if the
primary is unavailable:

```yaml
steps:
  - action: chat.completion
    with:
      prompt: "What's the weather in Tokyo?"
      model:
        - provider: openai
          name: gpt-4o
        - provider: anthropic
          name: claude-sonnet-4-6
      tools: [weather-lookup]
---
name: weather-lookup
description: Looks up current weather for a city
params: CITY=Tokyo
steps:
  - run: ./lookup-weather.sh "$CITY"
    output: WEATHER
```
