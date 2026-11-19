# Spec 091: Agent Node

## Status

Partially implemented.

[Spec 090](090-agent-tools.md) and [Spec 092](092-agent-workers.md) are its companions.

Implemented:

- the action `agent.run` with the keys of [The action](#the-action). It runs a CLI provider. The
  loader compiles it to `harness.run`. The saved DAG holds the `agent.run` step and the compiled
  configuration.
- the prompt template
- the flags of the provider `claude`: the key `dags` and the tool map of [Spec 090](090-agent-tools.md),
  the skills and the system text
- the tool bridge, which gives the Dagu tools of the step to the CLI, and the key `max_tool_calls`
- the mount of the skills of the step as folders of the attempt
- the block `endpoint` and the relay of [Spec 094](094-ai-gateway.md), and the named form of an `mcp`
  entry of [Spec 095](095-mcp-servers.md)
- the typed result, the output `tool_calls` and the failure kinds

Dagu follows the default of each open item. See [Open items](#open-items).

Not implemented: a human checkpoint after a failure of the kind `auth_required`. See
[Open items](#open-items).

## Scope

This spec defines:

- the action `agent.run` and the keys of an agent node
- the flags of the `claude` provider
- the tool bridge of a CLI agent
- the mount of the skills of a CLI agent
- the typed result, `tool_calls` and the failure kinds
- the slots of the agent card in the editor

This spec does not define:

- the key `dags`, and the tool map with its categories `mcp` and `builtin`. See
  [Spec 090](090-agent-tools.md).
- the place where a CLI agent runs, the routing of a run to an agent worker and the isolation of
  its steps. See Spec 092.
- a model API. A step that calls a model API uses `chat.completion`. See [Spec
  059](059-chat-completion.md).
- `harness.run`. See [Spec 067](067-harness.md).
- the items in [Not covered by this spec](#not-covered-by-this-spec)

## Goal

An author builds one agent node. A CLI agent runs behind it. The node gives a typed result and a
failure kind that a workflow can use.

Dagu keeps no usage record of an agent node. The raw stream of the CLI has the token counts and the
cost that the CLI reports. The events `llm.usage.recorded` of Dagu are for the model calls that
Dagu makes itself.

## Terms

An agent node is one step with the action `agent.run`.

A CLI provider is a built-in provider of Spec 067, such as `claude`, or an entry of `harnesses`
that is not an ACP entry. A chat provider is a provider of Spec 059, such as `anthropic`, `openai`
or `local`.

An attempt is one run of the CLI agent. A retry of `retry_policy` makes a new attempt.

A turn is one model request.

## The action

A step with `action: agent.run` has a closed `with`. An unknown key, a duplicate key or a value of
the wrong type is a validation error. The error names the key, the expected type and the value.

A validation error has the form `field '<path>': <message>`. When the key has a value, the error ends
with ` (value: <value>)`. `dagu validate` prints it after the name of the block of the step, as in
`field 'steps': field 'with.endpoint': <message>`. The examples of this spec show the inner part.

| Key | Type | Rule |
| --- | --- | --- |
| `prompt` | string | A template. The step MUST give it. |
| `system` | string | Optional. Text that the CLI adds to its instructions. |
| `provider` | string | The step MUST give it. It MUST be a CLI provider. See [Provider](#provider). |
| `endpoint` | map | The step MUST give it with the provider `claude`. A block with the keys `name`, `model` and `effort`. See [Endpoint](#endpoint). |
| `model` | string | Optional. Only a CLI provider other than `claude` takes it. With the provider `claude`, it is an unknown key: the model is in the block `endpoint`. See [Model](#model). |
| `dags` | list | Optional. The DAG tools of [Spec 090](090-agent-tools.md). Each entry has a `name` and optional `params`. |
| `tools` | map | Optional. The tool map of [Spec 090](090-agent-tools.md): the categories `mcp` and `builtin`. |
| `skills` | list | Optional. A list of skill names. See [Skills](#skills). |
| `max_tool_iterations` | integer | Optional. At least 1. The limit of turns of the CLI. It has the name that `chat.completion` has. |
| `max_tool_calls` | integer | Optional. At least 1. Default 100. The limit of the calls of the tool bridge in one attempt. See [Tool bridge](#tool-bridge). |

These step keys apply to an agent node:

| Step key | Rule |
| --- | --- |
| `output_schema` | Optional. A closed JSON Schema. See [Typed result](#typed-result). |
| `timeout_sec` | Optional. Dagu stops the step at this limit. |
| `retry_policy` | Optional. As for any step, with the rules of [Failure kinds](#failure-kinds). |
| `repeat_policy` | Optional. [Spec 075](075-repeat-policy.md). Each repetition is a new execution of the step. |
| `preconditions` | Optional. A gate that Dagu evaluates before the step. |
| `secrets`, `untrusted` | Spec 092. An agent node is untrusted. In a DAG that has an agent node, a step that runs a process is untrusted by default. A step with `secrets` must also have `untrusted: false`. |

An unknown key gives the message `unknown key "<key>" in agent.run; valid keys are <keys>` on the
path of the key.
The list form of `tools` is valid on `chat.completion` only. On `agent.run` it is a validation error
that names the key `dags`.

`agent.run` compiles to `harness.run` when Dagu loads the DAG. The saved DAG holds the `agent.run`
step and the compiled configuration.

### Provider

The provider of the step MUST be a CLI provider. A literal name is required: a value reference
such as `${PROVIDER}` is a validation error.

- A built-in CLI provider that is not a chat provider is valid.
- An entry of `harnesses` of the DAG is valid, except an ACP entry.
- A chat provider is a validation error. The error names `with.provider` and tells the author to
  use `chat.completion`. The names `gemini` and `opencode` are chat providers and CLI providers.
  They are chat providers for this check.
- An ACP provider, an unknown provider or no provider is a validation error. With no provider, the
  error is `agent.run needs a provider; it names the CLI provider`.

Model names do not translate between providers.

### Endpoint

Implemented. [Spec 094](094-ai-gateway.md) defines this section.

The key `endpoint` is a block. It names an endpoint of the AI page. It also gives the model and the
effort of the step. The block is closed: it has these keys.

| Key | Type | Rule |
| --- | --- | --- |
| `name` | string | The step MUST give it. The name of an endpoint of the AI page. It MUST be a literal name: a value reference is a validation error. |
| `model` | string | The step MUST give it. A model name that the endpoint lists. See [Model](#model). |
| `effort` | string | Optional. No key means the default of the CLI. See [Effort](#effort). |

An unknown key in the block is a validation error. The error lists the keys `name`, `model` and `effort`.

The endpoint holds the address and the key of the model API. The protocol of the endpoint MUST match
the provider of the step. The provider `claude` needs the protocol `anthropic`. A mismatch fails the
step at start with the reason `endpoint_protocol_mismatch`.

With `endpoint`, Dagu starts a relay for the attempt. The CLI gets `ANTHROPIC_BASE_URL`, with the
address of the relay, and the relay secret in the place of `ANTHROPIC_API_KEY`. The CLI gets no other
variable that starts with `ANTHROPIC_` or `CLAUDE_CODE_USE_`, and no `CLAUDE_CODE_OAUTH_TOKEN`. The CLI never
sees the key. The deployment needs no model key for this step.

A step with the provider `claude` MUST give the block `endpoint`. The model key stays in the server, and
the agent worker never holds it. These cases are validation errors when Dagu loads the DAG, so the run
never starts:

```text
field 'with.endpoint': agent.run needs an endpoint; add one on the AI page and name it here
field 'with.endpoint': endpoint must be a block with name and model
field 'with.endpoint.name': agent.run needs the name of an endpoint
```

The first error is for a step with no `endpoint`. The second error is for a value that is not a block,
for example a string. The third error is for a block with no `name`. A `name` that is not a literal
endpoint name is also a validation error, on the path `with.endpoint.name`:

```text
name must be a non-empty string
name must match ^[a-z][a-z0-9-]{0,63}$
name must be a literal name, not a reference: Dagu checks the use rule of the endpoint with this name
```

A CLI provider other than `claude` has no endpoint. Its key comes from the environment of the worker,
or from the proxy of Spec 092.

### Model

A step with the provider `claude` MUST give `endpoint.model`. The model must be one that the endpoint
lists. A step with no `endpoint.model` is a validation error when Dagu loads the DAG, so the run never
starts. A model that is empty, blank or not a string is also a validation error. The error has this form:

```text
field 'with.endpoint.model': agent.run needs a model; choose one that the endpoint lists
```

A load that lacks both the name and the model reports both errors. A value reference such as `${MODEL}`
is valid. The start check looks for a literal model in the models list of the endpoint. A model that the
list lacks fails the step before the CLI starts, with the reason `model_unknown`. The aliases of the CLI,
such as `sonnet`, `opus` and `opusplan`, pass the check. The check passes when the endpoint has no models
list. See [Spec 093](093-ai-endpoints.md#use-in-a-step).

With the provider `claude`, `model` is not a key of `with`. It is an unknown key.

A CLI provider other than `claude` takes the key `model` of `with` as an option. A step with no `model`
uses the default model of the CLI.

### Effort

The key `endpoint.effort` sets the effort level of the model. The value is `low`, `medium`, `high`,
`xhigh` or `max`. These are the levels of the flag `--effort` of the CLI `claude`. Any other value is a
validation error on the path `with.endpoint.effort`. The error names the key and the allowed values. Dagu
does not change a value to make it fit.

A value reference such as `${EFFORT}` is valid, as it is for the model. Dagu then checks the resolved
value when the step starts. A resolved value outside the five levels fails the attempt before the CLI
starts. The failure kind is `error` and the reason is `effort_invalid`.

Dagu does not check that the model supports the level. The CLI reports an unsupported level when it
runs. The UI offers the levels of the model only, and a hand-written YAML can still name any of the five
levels.

The key `effort` of `with` is an unknown key with each provider. With the provider `claude`, the
effort is a key of the block `endpoint`. A CLI provider other than `claude` has no effort. See [Keys of
the other providers](#keys-of-the-other-providers).

## Orchestration stays in Dagu

An `agent.run` step is one step of a graph. The flow around it uses the features of Dagu. This spec
adds no controller.

| Need | Dagu feature |
| --- | --- |
| Stages and order | `depends` |
| A gate on a typed field | `preconditions` on `${steps.<id>.outputs.<name>}`, or a `run` step |
| Parallel judges | `parallel` or `foreach` with an `agent.run` child |
| An attempt loop | `retry_policy`, or `repeat_policy` on the agent step |
| A new round after a failed verify | A chain of `code_<n>` and `verify_<n>` steps. See [Spec 092](092-agent-workers.md#a-verify-loop). |
| A decision of a person | `human.task` |
| A wait | `wait.until`, `wait.file`, `wait.http` |

A gate is a step that Dagu runs. An agent never decides whether its own result passes.

`repeat_policy` repeats one step. A group of steps does not repeat. An agent step can run the tests
itself, with a `builtin` shell pattern. A verify step that Dagu runs gives the verdict. When the
verdict is a failure, the author chains more rounds of an agent step and a verify step in the same
DAG. See [Spec 092](092-agent-workers.md#a-verify-loop).

## Prompt template

The key `prompt` is a template. Dagu resolves the value references of Spec 003 in it, such as
`${PARAM}` and `${steps.<id>.outputs.<name>}`. Dagu runs no command substitution. The template has
no condition, no loop and no include. A reference to a DAG secret in `prompt` or `system` is a
validation error. The check covers each form that Dagu or a shell expands: `${NAME}`, `$NAME`,
`${NAME:-word}` and `${env.NAME}`.

## Keys of the other providers

A CLI provider other than `claude` supports only `model`, `prompt` and `timeout_sec`. This spec
defines the flags and the tool bridge of `claude` only. A step with another key of the table of [The
action](#the-action) and such a provider is a validation error, such as `endpoint`, `system`,
`dags` or `tools`. The error has this form:

```text
field '<path>': key "<key>" is not supported by the provider "<provider>": <reason>
```

## Keys and flags of `claude`

| Key | CLI value |
| --- | --- |
| `system` | `--append-system-prompt`. The CLI keeps its own prompt. |
| `prompt` | The prompt of `-p`. |
| `endpoint.model` | `--model`. A model alias or a full name. The step MUST give it. |
| `endpoint.effort` | `--effort`. One of the five levels. |
| `output_schema` | `--json-schema`. |
| `max_tool_iterations` | `--max-turns`. A step with no key passes no `--max-turns` flag. |
| `tools.builtin` | `--tools`, `--allowedTools` and `--disallowedTools`. See [Spec 090](090-agent-tools.md). |
| `dags` | The tool bridge, in `--mcp-config`. Each tool is the allow rule `mcp__dagu__<name>`. |
| `tools.mcp` | One server for each entry in `--mcp-config`. A named entry is the relay of the attempt. Each name of `allow` is the allow rule `mcp__<name>__<tool>`. |
| `skills` | Files in the agent directory, which `--add-dir` passes. The tool `Skill` is in `--tools` and `--allowedTools`. See [Skills](#skills). |
| `max_tool_calls` | The tool bridge refuses a call over the limit. |
| `timeout_sec` | Dagu stops the step. |

`agent.run` and `chat.completion` have the same key `max_tool_iterations`. The meaning at the limit
differs. For `agent.run`, the CLI stops at the limit and the attempt fails with the reason
`max_tool_iterations`. For `chat.completion`, the step still succeeds when the limit stops the loop
([Spec 059](059-chat-completion.md)).

## Flags of the claude provider

For the provider `claude`, Dagu builds the command in this order. A flag is absent when its key is
absent.

| Order | Flag | Source |
| --- | --- | --- |
| 1 | `-p <prompt>` | The prompt. |
| 2 | `--output-format stream-json --verbose` | Always. |
| 3 | `--no-session-persistence` | Always. |
| 4 | `--permission-mode dontAsk` | Always. |
| 5 | `--model <value>` | `endpoint.model`. |
| 6 | `--effort <level>` | `endpoint.effort`. It comes right after `--model`. |
| 7 | `--append-system-prompt <text>` | The merged `system`. |
| 8 | `--tools <names>` | The built-in tools. `""` when there is none. |
| 9 | `--allowedTools <rule>...` | The allow rules: the built-in tools, then the tools of the bridge, then the tools of each `mcp` entry. One flag. Each rule is one argument. |
| 10 | `--disallowedTools <rule>...` | The deny rules. |
| 11 | `--strict-mcp-config --mcp-config <path>` | Always. The path is the MCP config that Dagu writes for the attempt. With no bridge and no `mcp` entry, the config has no server. |
| 12 | `--add-dir <dir>` | The agent directory, when a skill is mounted. |
| 13 | `--max-turns <n>` | `max_tool_iterations`. |
| 14 | `--json-schema <json>` | `output_schema`, as one line. |

| Neutral entry | CLI value |
| --- | --- |
| `read`, `write`, `edit` | `Read`, `Write`, `Edit` |
| `search` | `Grep` and `Glob` |
| `shell` | `Bash`. Each pattern gives the rule `Bash(<pattern>)`, an allow or a deny rule. |
| A mounted skill | `Skill`, in `--tools` and in `--allowedTools`. |

Dagu does not pass `--bare`, because the bare mode does not read the credentials of a login. A
`with` key of an `agent.run` step never becomes a free CLI flag.

The MCP config has the server `dagu` of the tool bridge and one server for each `mcp` entry. The
servers are in the order of their names. The `type` of each server is `http`. Dagu resolves the `url` and the `headers` of an `mcp` entry
when the step starts. The file has the mode 0400. Dagu deletes it at the end of the attempt.

A named `mcp` entry has no `url`. Its server in the file has the address of the relay of the attempt
(`http://127.0.0.1:<port>/mcp/<name>`) and the header `Authorization: Bearer <relay secret>`. The
file has no credential of the MCP server entry. A step with a named entry starts the relay of the
attempt, and Dagu adds `127.0.0.1` and `localhost` to `NO_PROXY`. A step of a provider that has no
endpoint keeps its model variables. Before the CLI starts, Dagu checks each named entry with the
coordinator. A failed check fails the step with the reason `mcp_server_unknown` or
`mcp_server_forbidden`.

## Skills

The `skills` key of `agent.run` is a list of skill names. [Spec 096](096-skills.md) defines the
skill folders, the format, the rules that refuse a skill and the limits. [Spec 090](090-agent-tools.md)
defines the key. The key is valid with the provider `claude`. A CLI provider other than `claude`
does not support it.

Dagu mounts the whole folder of each skill in `<agent directory>/.claude/skills/<name>/`. The mount
holds the original bytes of `SKILL.md` and every file of `references/`, `assets/` and `scripts/`.
Dagu does not rewrite `SKILL.md`. The agent directory is a new directory of the attempt. Dagu passes
it with `--add-dir` and adds the tool `Skill` to the tool lists. Dagu deletes the directory at the end
of the attempt.

The CLI can read the agent directory and cannot write it. Each file has the mode 0400 and each
directory the mode 0700. When the step runs as the restricted user ([Spec 092](092-agent-workers.md)),
Dagu gives the user read access to the directory and no write access. Dagu never runs a script of a
skill and gives no file the execute permission. A skill holds no secret: Dagu does not evaluate value
references in the body of a skill.

The real CLI finds the mounted skills. Claude Code 2.1.311 lists each one in the field `skills` of the
event `system/init`, and lists the agent directory in `additional_directories`. The tool `Skill` gives
the text of `SKILL.md` to the model.

On an agent worker, the skills of the DAG reach the worker in the workspace bundle of the run. The
worker reads them from the bundle and does not read the skills directory of the coordinator.

An `agent.run` step has no tool `load_skill`, because the CLI reads the files. The record of the
step lists the skills in `skillDefinitions`: the name, the description and the hash of each skill.

## Tool bridge

The tool bridge gives a Dagu tool (an entry of `dags`) to a CLI agent. It is an MCP
server of Dagu. It is not the endpoint of [Spec 020](020-mcp-server.md). An entry of `mcp` does
not go through the bridge: the CLI connects to the server of the entry. An entry with no `url`
names a MCP server entry with credentials. The CLI then connects to the relay of [Spec
095](095-mcp-servers.md#the-mcp-relay), and not to the server.

**Lifetime.** Dagu builds the effective catalog of the Dagu tools as in Spec 090 when the step
starts. Then it starts the bridge. The bridge exists for one attempt. Dagu stops it when the attempt
ends, also when the attempt fails or Dagu stops the step. After this, no listener remains. A request
that is open at that time gets the status 401.

A step with no entry of `dags` starts no bridge. The bridge runs in the process that
executes the step. A tool call starts a child run in the normal way. The child run is trusted: it
does not run as the restricted user of Spec 092. A stop request of the step also stops the child
runs.

**Address.** The bridge uses the MCP streamable HTTP transport at the path `/mcp`, on the address
`127.0.0.1` and a random free port. The MCP config has one server named `dagu` with the URL and the
header `Authorization: Bearer <secret>`. The name `dagu` of an `mcp` entry is a validation error.
A proxy of the environment must not get the requests of the CLI to the bridge: put `127.0.0.1` and
`localhost` in `NO_PROXY` (Spec 092).

**Authentication.** Dagu makes a new secret for each attempt: 32 random bytes from the system random
generator. The secret is bound to the run and the step. The bridge refuses any other value with the
status 401 and compares in constant time. Dagu masks the secret in the raw stream and in the stderr
of the step. Dagu never writes it in a log, a record or an error. The MCP config holds the secret. It
has the mode 0400. Only the user that runs the CLI can read it. For an untrusted step with
isolation, this user is the restricted user (Spec 092, "Files of the attempt").

**List and call.** `tools/list` returns the effective catalog: the name, the description and the
input schema of each tool. Fixed inputs of `params` are not in the schema. `tools/call` runs the
tool as in Spec 090. The result is the JSON text of the outputs. A failed child run, an unknown tool
or a refused call gives a result with `isError` true, and the step does not fail.

**Names.** The CLI takes at most 64 characters for the name `mcp__dagu__<name>`. A tool with a
name of more than 53 characters fails the step when it starts. Dagu does not cut the name.

**Limits.** The bridge runs one call at a time, in order of arrival. Dagu cuts a result at
`agent_tools.tool_result_max_bytes` of the configuration and keeps valid UTF-8.
`agent_tools.max_tools` limits the catalog. A call over `max_tool_calls` gets an error result, and
the attempt continues. The built-in tools and the tools of an `mcp` entry do not count.

**Check at start.** The CLI reports each MCP server in the event `system/init` (`mcp_servers`, with
`name` and `status`). When the server `dagu` does not have the status `connected`, the attempt
fails with the kind `error` and the reason `bridge_unavailable`. A failure of the kind
`auth_required` or `rate_limited` keeps its kind. A bridge that cannot start gives the same
failure.

**Record.** Dagu stores the effective catalog of the bridge with the node, as for `chat.completion`,
and the child run of each call. The record has no secret and no header value.

## Typed result

A step with `output_schema` has a closed schema. Dagu checks these rules at load.

- The schema has `type: object` and `properties`.
- Each object schema in it has `additionalProperties: false`.
- Each name in `required` is in `properties`.
- No property is named `tool_calls`.
- The schema uses the keywords of draft 07. `format` is a note and is not enforced.
- The schema text is at most 100000 bytes.

**Extraction.** Dagu takes `structured_output` of the last message with `type: "result"` in the
stream. The stream has one JSON object per line. Dagu ignores a line that is not JSON. Dagu never
reads JSON from the free text field `result`.

**Validation.** Dagu validates the value against the schema. An unknown field or a duplicate field
is an error. The error names the key, the expected type and the value, cut at 80 bytes.

| Reason | Case |
| --- | --- |
| `no_result` | No result message, or no `structured_output`. |
| `invalid_json` | The result is not valid JSON, or has a duplicate field. |
| `schema_mismatch` | The value does not match the schema, or has an unknown field. The CLI reports `error_max_structured_output_retries`. |

Each reason is a failure of the kind `error`. A failed attempt publishes no output.

**Outputs.** On success, the step writes the result as one line of JSON to stdout, with keys in
sorted order. Each property is the output `${steps.<id>.outputs.<name>}`, as in Spec 012 and Spec
059. A step with no `output_schema` has a text result: the field `result` of the last result
message. Dagu writes it to stdout and publishes no property. The raw stream of the CLI goes to a
file next to the stdout log, with the suffix `.stream.jsonl`.

**tool_calls.** A successful attempt publishes the output `${steps.<id>.outputs.tool_calls}`. It is
a JSON array of strings, one entry for each tool call of the agent, in order. A workflow can compare
it with a claim of the agent.

The list has each `tool_use` block of the stream, except the tool `StructuredOutput`. A call that
the CLI denied is in the list. Dagu normalizes the names:

| Call | Name |
| --- | --- |
| `mcp__dagu__<name>` | `<name>` |
| `mcp__<name>__<tool>` | `<name>_<tool>` |
| `Read`, `Write`, `Edit` | `read`, `write`, `edit` |
| `Grep`, `Glob` | `search` |
| `Bash` | `shell` |
| Any other name | The name that the CLI gave. |

**Free text.** Dagu never puts the free text of an agent in another step. The stdout, the stream and
the `result` text are not interpolated. A prompt has only the references that the author wrote in
it.

## Failure kinds

Each attempt has one kind: `ok`, `auth_required`, `rate_limited` or `error`. Dagu writes
`agent: attempt ended <kind>` to stderr for each failed attempt of the CLI. A failure before the CLI
starts, such as a failed start check, writes no such line.

The step exit code is 77 for `auth_required` and 75 for `rate_limited`. For `error` it is the
process exit code, or 1. A workflow can select these codes with `continue_on` and `retry_policy`.

**Detection with claude.** Dagu uses the final result and the last assistant message. It does not
use the events `system/api_retry`, because the CLI can recover from them.

| Kind | Case |
| --- | --- |
| `ok` | Exit code 0, a result with `subtype: "success"` and `is_error: false`. |
| `auth_required` | `is_error: true` and the error `authentication_failed` on the last assistant message, or `api_error_status` 401. |
| `rate_limited` | `is_error: true` and the error `rate_limit` on the last assistant message, or `api_error_status` 429. |
| `error` | Any other case: `error_max_turns`, `error_during_execution`, `error_max_budget_usd`, or no result message. |

A failure of the kind `error` has a reason:

| Reason | Case |
| --- | --- |
| `exit` | The CLI exited with a code other than 0 and wrote no result message. Or it reported success and exited with a code other than 0. |
| `no_result` | The CLI exited with the code 0 and wrote no result message. Or the result has no `structured_output`. |
| `max_tool_iterations` | The result has the subtype `error_max_turns`. |
| `schema_mismatch` | The result has the subtype `error_max_structured_output_retries`, or the value does not match the schema. |
| `invalid_json` | See [Typed result](#typed-result). |
| `error_max_budget_usd`, `error_during_execution` | The result has this subtype. |
| The subtype of the result, or `error` | The result is an error with another subtype. |
| `interrupted` | A stop or a timeout of the step ended the CLI. |
| `bridge_unavailable` | See [Tool bridge](#tool-bridge). |
| `effort_invalid` | See [Effort](#effort). |
| `mcp_config`, `json_schema`, `stdout` | Dagu could not write the MCP config, the schema flag or the result. |
| `skills_mount` | Dagu could not mount a skill, or a binary file of a skill changed. See [Spec 096](096-skills.md#the-record-of-a-run). |
| A reason of Spec 094 | A start check or the relay refused the request. See [Spec 094](094-ai-gateway.md#failure-kinds). |

The reason of an attempt of the kind `auth_required` or `rate_limited` is the name of the kind.

A text test is a fallback when the structured fields are absent.

**Authentication.** An attempt of the kind `auth_required` fails the step with the code 77. Dagu
does not wait and `retry_policy` does not retry it. A new attempt with the same credential cannot
succeed.

**Rate limit.** A `rate_limited` attempt fails the step at once with the code 75. The step has no
wait of its own. A step that must wait for a limit uses `retry_policy` with `exit_code: [75]` and
`interval_sec`. Each retry is a new execution of the step, and Dagu waits `interval_sec` before it.

**Retry policy.**

- `retry_policy` never retries `auth_required`.
- `retry_policy` retries `rate_limited` and `error` as for any step. `retry_policy.exit_code` can
  select 75.

## UI contract

The agent card of the canvas has the same slots as a `chat.completion` card.

| Slot | Shows |
| --- | --- |
| Model | The endpoint and the model, when the step has an endpoint, and else the provider and the model. In the edit view, the node has an Endpoint select, a Model select and an Effort select. See [Spec 093](093-ai-endpoints.md#the-model-node). |
| Tools | The tools of the step: the Dagu tools, the MCP servers and the built-in tools. |
| Skills | The skills of the step. |

A slot with no tool or no skill is a small round "+" node. It opens the list of choices of the slot.
A slot with an entry is a card with a row for each entry. [Spec 090](090-agent-tools.md) defines the
placement of the slots in each direction of the graph.

The canvas draws an agent node in the same way as a `chat.completion` node. The run view draws it
in the same way.

The editor form of an agent node has these sections: Model, System message, Prompt, Tools, Skills,
Limits and Output. The provider is required, and the list of providers has the CLI providers only.
The section Model also has an Endpoint select, a Model field and an Effort field. The Endpoint select
lists the endpoints of the protocol `anthropic` that the DAG may use. It has no "(none)" item: a step
with no endpoint shows an error. The Model field shows the models of the endpoint as suggestions. A step
with an endpoint and no model shows an error. The form and the Model node write the three keys of the
block `endpoint`: `endpoint.name`, `endpoint.model` and `endpoint.effort`. The key `effort` stays optional.
[Spec 093](093-ai-endpoints.md#edits-of-the-endpoint-and-the-model) defines the edits when the endpoint
changes. The Limits section has the keys `max_tool_iterations` and `max_tool_calls`. The Tools section
shows the three kinds of tool: the DAG tools of `dags`, and the categories `mcp` and `builtin` of the tool map.

The editor shows `untrusted` for an agent node. The run view shows the same sections, read-only.

## Errors

`dagu validate` rejects these cases with a nonzero exit code.

- an unknown key, a duplicate key or a value of the wrong type in the `with` of `agent.run`. With the
  provider `claude`, `model` is an unknown key. `effort` is an unknown key with each provider.
- no provider, a chat provider, an unknown provider or an ACP provider
- no `endpoint`, with the provider `claude`: the error is `agent.run needs an endpoint; add one on the
  AI page and name it here`
- an `endpoint` that is not a block, with the provider `claude`: the error is `endpoint must be a block
  with name and model`
- no `endpoint.name`, with the provider `claude`: the error is `agent.run needs the name of an endpoint`.
  An empty, blank or non-literal name is also an error.
- no `endpoint.model`, with the provider `claude`: the error is `agent.run needs a model; choose one that
  the endpoint lists`. An empty, blank or non-string model is also an error.
- a key in the block `endpoint` other than `name`, `model` and `effort`
- a key that the provider cannot run
- the list form of `tools`. It is valid on `chat.completion` only.
- an error of the tool map of Spec 090, such as the key `if` on an entry or an `mcp` entry named
  `dagu`
- a `max_tool_calls` below 1
- an `endpoint.effort` that is not one of `low`, `medium`, `high`, `xhigh` and `max`, and the key
  `endpoint` with a CLI provider other than `claude`
- a skill that does not exist, a skill that breaks the format of Spec 096, or an item of `skills` that is not a name.
  See [Spec 090](090-agent-tools.md).
- the key `skills` with a CLI provider other than `claude`
- a `shell` entry with no `allow`, or a pattern that matches all commands
- an `output_schema` that is not closed, above 100000 bytes, or with a property named `tool_calls`
- a secret reference in `prompt` or `system`
- a `secrets` list on an agent node (Spec 092)

These cases fail the attempt at run time:

- an unknown field, a duplicate field or a schema mismatch in the result
- no `structured_output`
- the limit `max_tool_iterations` reached before an answer
- a tool bridge that the CLI did not reach (`bridge_unavailable`)

## Examples

A step:

```yaml
steps:
  - id: review
    action: agent.run
    output_schema:
      type: object
      additionalProperties: false
      properties:
        verdict: { type: string, enum: [accept, reject] }
      required: [verdict]
    with:
      provider: claude
      endpoint:
        name: cle1
        model: sonnet
      prompt: "Review the patch ${PATCH_ID}."
      system: Report only real defects.
      max_tool_iterations: 20
      tools:
        builtin: { read: true, search: true }
```

A step with two DAG tools, an MCP server and built-in tools. The DAG `ops_notify` is in the same
file as the step. Claude Code gets the tools `mcp__dagu__lookup-ticket`,
`mcp__dagu__ops_notify` and `mcp__docs__search`, and `Read` and `Grep`:

```yaml
  - id: support
    action: agent.run
    with:
      provider: claude
      endpoint:
        name: cle1
        model: sonnet
      prompt: "Answer the ticket of the customer ${CUSTOMER_ID}."
      max_tool_calls: 10
      dags:
        - name: lookup-ticket
          params:
            CUSTOMER_ID: ${CUSTOMER_ID}
            QUESTION:
              from_ai:
                description: The question of the customer
        - name: ops_notify
      tools:
        mcp:
          - name: docs
            url: ${MCP_URL}
            allow: [search]
        builtin: { read: true, search: true }
```

A gate on the typed result and on `tool_calls`:

```yaml
  - id: gate
    depends: review
    run: echo '${steps.review.outputs.tool_calls}' | jq -e 'index("search") != null'
    preconditions:
      - condition: ${steps.review.outputs.verdict}
        expected: accept
```

Wait up to one hour for a limit of the subscription: set `retry_policy` on the step. Twelve retries
at intervals of 300 seconds wait up to one hour, and only the code 75 starts a retry.

```yaml
  - id: review
    action: agent.run
    retry_policy: { limit: 12, interval_sec: 300, exit_code: [75] }
    with:
      provider: claude
      endpoint:
        name: cle1
        model: sonnet
      prompt: "Review the patch ${PATCH_ID}."
```

A step with an endpoint of the AI page. The step needs no model key in the worker:

```yaml
  - id: review
    action: agent.run
    with:
      endpoint:
        name: cle1
        model: claude-sonnet-4-6
      provider: claude
      prompt: "Review the patch ${PATCH_ID}."
```

A step with a chat provider fails:

```text
field 'with.provider': the provider "anthropic" is a chat provider, and agent.run runs a CLI provider; use chat.completion to call a model API
```

A step with the list form of `tools` fails:

```text
field 'with.tools': the list form of tools is valid on chat.completion only, and it is deprecated; an agent.run step lists its DAG tools in the key dags
```

A fixture puts a script named `claude` in `PATH`. The script prints this stream and exits with 0:

```text
{"type":"system","subtype":"init","model":"sonnet","mcp_servers":[]}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Grep","id":"t1","input":{}}]}}
{"type":"result","subtype":"success","is_error":false,"num_turns":2,"result":"done","structured_output":{"verdict":"accept"}}
```

The outputs are `verdict` equal to `accept` and `tool_calls` equal to `["search"]`.

## Conformance

Black-box tests are in `conformance/spec091_agent_node/`. They run a fake `claude` executable that
prints canned `stream-json` lines and records its arguments. For the tool bridge, the fake runs a
small MCP client (`testdata/bin/mcp_client.py`) that reads the MCP config and calls the bridge. The
tests of the bridge skip when no `python3` is in `PATH`. A local OpenAI-compatible endpoint serves the
test of `chat.completion`. A helper adds the endpoint `test-endpoint`, which the fixtures name, to the
data directory of each project. Its model API is a stub that the fake `claude` never calls. The
fixtures name the model `claude-test`. No test needs an external service or the user ID 0.

| Section | Tests |
| --- | --- |
| The action, Provider, Endpoint, Model, Effort | `TestValidateRejectsWhatTheProviderCannotRun`, `TestValidateAcceptsAgentRun`, `TestSavedFormOfAnAgentRun`. The start checks of the endpoint and the model: Spec 094, `TestRelayLocalMode`, and Spec 093, `TestModelCheckLocalMode`. |
| Effort at run time | `TestClaudeEffortFlag`, `TestClaudeEffortReferenceOutOfSet`. |
| Prompt template | `TestClaudeFlags`, and the secret row of `TestValidateRejectsWhatTheProviderCannotRun`. |
| Keys of the other providers | `TestOtherCLIProviderGetsTheModelAndThePromptOnly`, and the `codex` rows of `TestValidateRejectsWhatTheProviderCannotRun`. |
| Flags of the claude provider | `TestClaudeFlags`, `TestClaudeFlagsMinimal`. |
| Skills | `TestClaudeSkills`, and Spec 096, `TestAgentRunMountsTheFolderVerbatim`. |
| Tool bridge | `TestBridgeOffersTheCatalogAndRunsTheCalls`, `TestBridgeMaxToolCalls`, `TestBridgeFailedChildRunIsAnErrorResult`, `TestBridgeUnavailableFailsTheAttempt`, `TestBridgeSecretStaysOutOfTheLogs`, `TestBridgeWithTheThreeCategories`. The order of the calls and the limit of a result: not covered here. |
| Typed result | `TestClaudeTypedResultAndToolCalls`, `TestClaudeTextResult`, `TestClaudeStreamFile`, `TestClaudeTypedResultFailures`, and the schema rows of `TestValidateRejectsWhatTheProviderCannotRun`. |
| Failure kinds | `TestClaudeFailureKindsAndExitCodes`, `TestRetryPolicyDoesNotRetryAuthRequired`, `TestRetryPolicyRetriesRateLimited`, `TestRetryPolicyWaitsForARateLimit`. The reasons `interrupted`, `mcp_config`, `json_schema` and `stdout`: not covered. |
| `max_tool_iterations` of `chat.completion` | `TestMaxToolIterationsOfChatCompletion`, and Spec 059, `TestToolIterationLimit`. |
| UI contract | Not covered here. The tests of the UI cover it. |
## Not covered by this spec

- Untrusted values: Dagu would need a taint model for the whole DAG.
- Refinement loops between agents and gates: the graph features above define them.

## Open items

Each item has a default. The default applies until the owner decides.

| Item | Default |
| --- | --- |
| `--strict-mcp-config` works with a `--mcp-config` file that has no server. | Dagu always passes both flags. |
| The CLI finds skills in a folder that `--add-dir` adds, with the bare mode off. | Measured with Claude Code 2.1.311: it finds them. Mount as in [Skills](#skills). |
| `--tools ""` also removes the tool `Skill`. | Dagu adds `Skill` to `--tools` and `--allowedTools` when it mounts a skill. The CLI then lists `Skill` in its tools and calls it. |
| The `type` of a server of the MCP config, and the status of a reached server in `system/init`. | `http` and `connected`. Measured with Claude Code 2.1.311: the event lists each server with the status `connected`. |
| Commands that `dontAsk` runs with no rule (the read-only command set). | Accepted. |
| The way that the CLI matches a `shell` pattern with a shell operator. | Write patterns with no operator. |
| The result texts of a subscription limit and of an expired login, and the exit codes of the CLI. | As in [Failure kinds](#failure-kinds). |
| The key `endpoint` and its relay. | Spec 094 defines them. The real CLI works through the relay: see [Measured with Claude Code](094-ai-gateway.md#measured-with-claude-code). |
| A human checkpoint after `auth_required`. A step needs a way to enter `waiting` before it ends. | The step fails. The idea is a new executor result `waiting`. |
| The place of the stream file. | Next to the stdout log. |
| The match of a command pattern with no final ` *`, and of a command with a group or a control character. | The CLI decides. |
