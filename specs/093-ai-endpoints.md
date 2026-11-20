# Spec 093: AI Endpoints

## Status

Implemented.

This spec is the companion of [Spec 059: Chat Completion](059-chat-completion.md), [Spec 090: Agent
Tools](090-agent-tools.md), [Spec 091: Agent Node](091-agent-node.md), [Spec 092: Agent
Workers](092-agent-workers.md), [Spec 094: AI Gateway](094-ai-gateway.md), [Spec 095: MCP
Servers](095-mcp-servers.md) and [Spec 096: Skills](096-skills.md). It also uses [Spec 069: Secrets
Providers](069-secrets-providers.md) and [Spec 070: Runtime Profiles](070-runtime-profiles.md).

Spec 094 defines the gateway that uses the endpoints of this spec. Spec 095 defines the MCP servers
with credentials.

The values of the [provider presets](#provider-presets) come from the documentation of each
provider, read on 2026-11-04. A value that the author could not verify has the text "To verify". See
[Open items](#open-items).

## Scope

This spec defines:

- the AI page of the UI and its tab Endpoints
- the AI endpoint: a named entry that holds the address and the key of a model API
- the provider presets
- the list of models of an endpoint, and the model check of a step
- the choice of the endpoint and of the model in the step forms and in the Model node
- the keys of the block `ai` of the Dagu configuration

This spec does not define:

- the gateway: the relay in the worker and the stream to the coordinator. See [Spec
  094](094-ai-gateway.md).
- the block `endpoint` of `agent.run` and of `chat.completion`, and the key `endpoint` of a model
  entry. See Spec 094.
- the MCP servers with credentials, the tab MCP and the MCP relay. See [Spec
  095](095-mcp-servers.md).
- the keys of `agent.run`, the typed result and the failure kinds of an attempt. See Spec 091.
- the key `dags`, the tool map, the skills and the key `allow`. See Spec 090.
- the agent worker, the pools, the restricted user and the egress proxy. See Spec 092.
- the secret providers and the runtime profiles. See Spec 069 and Spec 070.
- the keys of the `harness.run` action. See [Spec 067](067-harness.md).
- the skills of the server and the tab Skills. See Spec 096.

## Goal

A workflow author names a model API in one key of a step. The author never writes the API key in a
DAG, in a file of the worker or in the environment of an agent. An administrator adds the key one
time, on the AI page, and chooses which DAGs may use it.

The key stays in the coordinator. [Spec 094](094-ai-gateway.md) defines the gateway that adds the key
to a request.

## Terms

An endpoint is a named entry of the AI page. It holds a protocol, a base URL, an authentication form,
a key and the rules about who may use it.

A protocol is `anthropic` or `openai`. It names the form of the HTTP API: the paths, the headers and
the bodies.

A preset is a row of the table of [Provider presets](#provider-presets). It gives default values to
an endpoint.

A scope is `global` or one workspace. The scope of an endpoint is fixed when an administrator creates
it.

An upstream is the HTTP server of the provider.

The gateway, the relay and the relay secret are as in Spec 094. The coordinator and an agent worker
are as in Spec 092. A local run is a run that one process does without a coordinator.

## Threat model

Spec 094 gives the assets, the trust levels and the threats of the gateway. See [Threat
model](094-ai-gateway.md#threat-model). This table gives the threat of the endpoint pages.

| Threat | Control | What remains |
| --- | --- | --- |
| A page shows the key to an administrator or a log. | The API and the UI show a prefix and a fingerprint only. No log holds the key. | The prefix and the fingerprint. |

## The endpoint

### Fields

| Field | Type | Rule |
| --- | --- | --- |
| `name` | string | Required. Matches `^[a-z][a-z0-9-]{0,63}$`. Unique in its scope. For example `cle1`. |
| `description` | string | Optional. At most 200 characters. |
| `preset` | string | Required. A name of the table of presets, or `custom`. |
| `protocol` | string | Required. `anthropic` or `openai`. A preset gives a default. |
| `base_url` | string | Required. A preset gives a default. See [Base URL](#base-url). |
| `auth` | string | Required. `x-api-key`, `bearer` or `anthropic-oauth`. A preset gives a default. |
| `models_url` | string | Optional. Valid with the preset `custom` only. See [The models list](#the-models-list). |
| `scope` | string | Required. `global` or `workspace:<name>`. Fixed at creation. |
| `allow.workspaces` | list | Required for the scope `global`. Workspace names, or `*`. Not valid in a workspace scope. |
| `allow.dags` | list | Required. DAG names, or `*`. |
| `key` | secret | Required. Write-only. See [The key](#the-key). |

An endpoint with no `allow` is not valid. No list means no access, so the administrator makes a
choice. A name is an exact name. The value `*` is the only pattern, and it must be the only item of
its list.

The JSON of the API writes the names in camelCase, as the other routes do: `baseUrl`, `modelsUrl`,
`keyPrefix`, `keyFingerprint` and `keyUpdatedAt`. The text of this spec uses the snake_case names of
the fields. The field `allow` is an object with the lists `workspaces` and `dags`.

The authentication forms are the forms of Spec 092:

| Form | Header that the coordinator sets |
| --- | --- |
| `x-api-key` | `x-api-key: <key>` |
| `bearer` | `authorization: Bearer <key>` |
| `anthropic-oauth` | `authorization: Bearer <key>`, and the beta `oauth-2025-04-20` in `anthropic-beta`. Valid with the protocol `anthropic` only. |

The coordinator removes the headers `x-api-key`, `authorization` and `proxy-authorization` of the
client before it sets its own header.

### Base URL

The scheme is `https`. The presets Local model and Custom also accept `http`. The URL has no user
information, no query and no fragment. The UI shows a warning for an `http` URL, because the key
goes over a network with no encryption.

The coordinator resolves the host when it makes a request. A host name of a local server, such as
`localhost`, names a place on the host of the coordinator, not on the worker. The note of the preset
Local model repeats this.

### Scope and name

The scope of an endpoint is `global` or `workspace:<name>`. The person who adds the key chooses the
scope. It follows the layers of the runtime profiles: `_global` and `_workspaces` (Spec 070).

A step names an endpoint by its `name`. A DAG with the label `workspace=<name>` resolves the name in
the scope of that workspace first, and then in the global scope. A workspace endpoint with the same
name hides the global endpoint for the DAGs of that workspace. A DAG with no workspace label
resolves in the global scope only.

A workspace endpoint has no `allow.workspaces`: its own workspace is the only one. It needs `allow.dags`
only.

### Use rule

A DAG may use an endpoint when all these conditions are true:

- the endpoint is in the global scope, or in the workspace of the DAG
- the workspace of the DAG is in `allow.workspaces`, for a global endpoint
- the name of the DAG is in `allow.dags`

The value `*` matches each name, and also a DAG with no workspace. A list of names does not match a
DAG with no workspace.

The DAG is the DAG of the run that holds the step. A sub-DAG run has its own name. The author of a
tool DAG lists it too.

### The key

The key is write-only, as a secret of a runtime profile. After the save, the API and the UI never
show the key. They show these values:

| Value | Rule |
| --- | --- |
| `key_prefix` | The first characters of the key. The length is the smaller of 6 and 10 percent of the key length, rounded down. A key of fewer than 10 characters has no prefix. |
| `key_fingerprint` | The first 12 hexadecimal digits of the SHA-256 of the key. |
| `key_updated_at` | The time of the last write. |

A rotation replaces the value. The old value stays in the store as an old version, encrypted, as for
each managed secret. No route reads it. The UI has no "show" button and no "copy" button.

A key is not empty. It is valid UTF-8 and has no control character. It has at most 8192 bytes. The save does not call
the provider. The presets of a local model accept a create request with no key. Such an endpoint
has no key, and the coordinator sets no credential header. A rotation can add a key later.

### Storage

The key is a managed secret of the secret store of Spec 069. It has the provider type
`dagu-managed`, the workspace of the scope (`.global` for the global scope) and the ref
`ai/endpoint/<name>`. The store encrypts the value. This spec adds no second store for keys.

The other fields are not secret. They are a record of a collection of the persistence layer, as a
runtime profile is. The record holds the ID of the secret. A delete removes the record and the secret.

The secret routes of Spec 069 show the metadata of the key secret, and never the value. Only an
administrator can create, change, enable, disable or delete a secret with a ref that starts with `ai/`.

The prefix `ai/` is reserved. No DAG can read a secret with this prefix. `dagu validate` rejects a
`secrets:` entry whose `ref` starts with `ai/`, and the error names the ref and the rule. The resolver of
DAG secrets also refuses such a ref at run time, in the local mode, on a worker and for a runtime
profile. It returns no value, because an old or queued DAG can skip the validation. The command
`dagu secret resolve` refuses such a ref too. The gateway and the models reader read the key by the ID of
the secret. They do not use this resolver.

A key and a profile secret use the same encryption key of the server. A process that cannot read it
cannot read an endpoint.

### Routes and audit

All the routes need the role admin, except the route of choices.

| Route | Effect |
| --- | --- |
| `GET /ai/endpoints` | List the endpoints with their fields, prefix and fingerprint. |
| `POST /ai/endpoints` | Create an endpoint with its key. |
| `PATCH /ai/endpoints/{name}` | Change the fields `description`, `base_url`, `auth`, `models_url` and `allow`. |
| `PUT /ai/endpoints/{name}/key` | Set or rotate the key. |
| `DELETE /ai/endpoints/{name}` | Delete the endpoint and its key. |
| `GET /ai/endpoints/{name}/models` | Read the models list. |
| `POST /ai/endpoints/{name}/models/reload` | Reload the models list now. |
| `GET /ai/endpoint-presets` | List the [provider presets](#provider-presets) of the server. |
| `GET /ai/endpoint-choices?dag=<name>&workspace=<name>` | List the endpoints that the DAG may use. Roles that edit DAGs in the workspace can call it. |

The scope is a field of the body of `POST`. A create in the scope of a workspace that does not exist
gives the status 404. The routes that name an endpoint read the scope from the query `workspace`. The
value `global`, or no query, means the global scope. Another value is the name of a workspace. The
route of choices needs the query `dag`, and gives the status 400 without it. A server with no store of
endpoints gives the status 503 to each route.

The route of presets gives the table of [Provider presets](#provider-presets) as the server holds it. It
groups the rows by preset. Each preset has `id`, `label` and `needsKey`. It has the list `protocols`, and
each item has `protocol`, `baseUrl`, `auth`, `modelsUrl`, `chatType` and `consumers`. A value that the table
does not give is absent. A value that this spec marks "To verify" is served as the table holds it. The
preset `custom` is the last item.

A change cannot set the name, the preset, the protocol, the scope or the key. A change of `allow`
replaces the whole block.

The route `GET /ai/endpoints/{name}/models` gives the list that the server keeps. It does not call the
provider. The route `POST /ai/endpoints/{name}/models/reload` calls the provider, keeps the result and
gives the same answer. A failed read is not an error of the route: the answer has the error. A reload
of an endpoint that has no models URL gives the status 400. Both routes use the query `workspace` for
the scope.

Both routes give an object with these fields:

| Field | Rule |
| --- | --- |
| `supported` | `false` when the endpoint has no models URL. It then has no list. |
| `count` | The number of names in the last good list. |
| `models` | The names of the last good list, in the order of the provider. |
| `modelInfo` | One item for each name of `models`, in the same order. See below. |
| `refreshedAt` | The time of the last good read. It is absent before the first good read. |
| `error` | The text of the error of the last read. It is absent when the last read was good. |
| `errorAt` | The time of the error. It is present with `error`. |

The list of endpoints has the same object without `models`, in its field `models`.

An item of `modelInfo` has these fields:

| Field | Rule |
| --- | --- |
| `id` | The name of the model. It is the same text as the item of `models`. |
| `displayName` | The name that a person reads, for example `Claude Haiku 5.5`. It is absent when the provider gives none. |
| `efforts` | The effort levels that the model supports, from the lowest: `low`, `medium`, `high`, `xhigh` and `max`. It is absent when the provider gives none. |

The server always sends the fields `models` and `modelInfo`.

The route of choices takes the workspace of the DAG from the query `workspace`: this is the value of
the label `workspace` of the DAG. It gives the name, the protocol, the preset and the scope of each
endpoint. The field `models` holds the names of the last good list. The field `modelInfo` has the rule
above. Both fields are empty when the endpoint has no list. The route gives no key data and no base URL. A role that edits DAGs can read the models of an
endpoint that its DAG may use, and it needs no other route for them.

An endpoint request never has the key in its response. It has the key in the body of `POST` and `PUT`
only. The server never writes a body in a log.

The JSON body of a request to a route under `/ai/` has a closed schema. An unknown field gives the
status 400. A body above 1 MiB gives the status 413. The error never quotes a value of the body.

Each create, change, rotation, delete and reload writes one line to the audit log, with the category
`secret`. The actions are `ai_endpoint_create`, `ai_endpoint_update`, `ai_endpoint_key_rotate`,
`ai_endpoint_delete` and `ai_endpoint_models_reload`. The line has the user, the endpoint name, the
scope and, for a create and a rotation, the fingerprint. It never has a key or a prefix. A call that
fails writes no line.

An endpoint in use can be deleted. The next relay request of a run that names it fails with
`endpoint_unknown`.

## Provider presets

A preset gives default values to a new endpoint. The table is data of the server. It is not a list in
a DAG. A new row needs no change of a DAG.

Each row is one protocol of a preset. An endpoint uses one row. A preset with two rows gives two kinds
of endpoint.

The column Consumers names the clients that can use the row. The CLI `claude` needs the protocol
`anthropic`. The CLI `codex` needs the protocol `openai`, and `agent.run` does not run it yet (Spec
091). The action `chat.completion` has the provider types `openai`, `anthropic`, `gemini`,
`openrouter`, `zai`, `local` and `openai-codex`, and `opencode`. The column Chat type gives the type
that an endpoint of the row selects.

| Preset | Protocol | Base URL | Auth | Models URL | Consumers | Chat type |
| --- | --- | --- | --- | --- | --- | --- |
| Anthropic | `anthropic` | `https://api.anthropic.com` | `x-api-key`, `anthropic-oauth` | `https://api.anthropic.com/v1/models` | CLI `claude` | `anthropic` |
| OpenAI | `openai` | `https://api.openai.com/v1` | `bearer` | `https://api.openai.com/v1/models` | CLI `codex` (later), `chat.completion` | `openai` |
| Google Gemini | `openai` | `https://generativelanguage.googleapis.com/v1beta/openai` | `bearer` | `https://generativelanguage.googleapis.com/v1beta/openai/models` | `chat.completion` | `openai` |
| Mistral | `openai` | `https://api.mistral.ai/v1` | `bearer` | `https://api.mistral.ai/v1/models` | CLI `codex` (later), `chat.completion` | `openai` |
| DeepSeek | `openai` | `https://api.deepseek.com` | `bearer` | To verify: `https://api.deepseek.com/models` | CLI `codex` (later), `chat.completion` | `openai` |
| DeepSeek | `anthropic` | `https://api.deepseek.com/anthropic` | `x-api-key`, `bearer` | As the OpenAI row of the preset. To verify. | CLI `claude` | `anthropic` |
| xAI (Grok) | `openai` | `https://api.x.ai/v1` | `bearer` | `https://api.x.ai/v1/models` | CLI `codex` (later), `chat.completion` | `openai` |
| Groq | `openai` | `https://api.groq.com/openai/v1` | `bearer` | `https://api.groq.com/openai/v1/models` | CLI `codex` (later), `chat.completion` | `openai` |
| OpenRouter | `openai` | `https://openrouter.ai/api/v1` | `bearer` | `https://openrouter.ai/api/v1/models` | CLI `codex` (later), `chat.completion` | `openrouter` |
| OpenRouter | `anthropic` | `https://openrouter.ai/api` | `bearer` | `https://openrouter.ai/api/v1/models` | CLI `claude` | `anthropic` |
| Z.ai | `openai` | `https://api.z.ai/api/paas/v4` | `bearer` | To verify | CLI `codex` (later), `chat.completion` | `zai` |
| Z.ai | `anthropic` | `https://api.z.ai/api/anthropic` | To verify: `bearer` | To verify | CLI `claude` | `anthropic` |
| Together AI | `openai` | `https://api.together.ai/v1` | `bearer` | `https://api.together.ai/v1/models` | CLI `codex` (later), `chat.completion` | `openai` |
| Local model (Ollama) | `openai` | `http://localhost:11434/v1` | `bearer`, key optional | `http://localhost:11434/v1/models` | CLI `codex` (later), `chat.completion` | `local` |
| Local model (Ollama) | `anthropic` | `http://localhost:11434` | `bearer`, key optional | `http://localhost:11434/v1/models` | CLI `claude` | `anthropic` |
| Local model (vLLM) | `openai` | None. To verify: `http://<host>:8000/v1` | To verify: `bearer` | `<base_url>/models` | CLI `codex` (later), `chat.completion` | `local` |
| Local model (vLLM) | `anthropic` | None. To verify: `http://<host>:8000` | To verify | `<base_url>/v1/models` | CLI `claude` | `anthropic` |
| Local model (LiteLLM) | `openai` | None. To verify: `http://<host>:4000/v1` | To verify: `bearer` | `<base_url>/models` | CLI `codex` (later), `chat.completion` | `local` |
| Local model (LiteLLM) | `anthropic` | None. To verify: `http://<host>:4000` | `x-api-key` | To verify | CLI `claude` | `anthropic` |
| Custom | `anthropic` or `openai` | Set by the administrator | Set by the administrator | `models_url`, or none | By protocol | `anthropic` or `openai` |

The identifier of a preset is the lower case name of its row. The identifiers are `anthropic`,
`openai`, `google-gemini`, `mistral`, `deepseek`, `xai`, `groq`, `openrouter`, `zai`, `together`,
`ollama`, `vllm`, `litellm` and `custom`. A request names the preset and the protocol. The server finds the
row from these two values, and it refuses a pair that has no row.

The three presets of a local model, `ollama`, `vllm` and `litellm`, accept the scheme `http` and an
endpoint with no key. A row with no base URL in the table, such as `vllm`, needs a base URL in the
request. The preset `custom` needs a base URL and an authentication form.

What the author verified in the provider documentation, on 2026-11-04:

- Anthropic: the base URL, `GET /v1/models` and the header `anthropic-version`, which the page lists
  as optional. The query `limit` goes up to 1000. The cursor `after_id` takes the field `last_id` of
  the last page. The key goes in `x-api-key` or in `authorization: Bearer`. The documentation calls
  `x-api-key` a legacy form that stays supported.
- OpenAI: the base URL, `GET /models` and the bearer form.
- Google Gemini: the base URL of the OpenAI compatibility, the bearer form and `GET .../openai/models`.
  The native Gemini API (the type `gemini` of `chat.completion`) is not an endpoint protocol.
- Mistral: the base URL, `/v1/chat/completions`, `GET /v1/models` and the bearer form.
- DeepSeek: both base URLs, the bearer form for the OpenAI format, and both headers for the Anthropic
  format. The path `GET /models` has no host in the page that the author read.
- xAI: the base URL, `GET /v1/models` and the bearer form. A third-party page says that xAI
  deprecated its Anthropic compatibility. The table has no Anthropic row for xAI.
- Groq: the base URL, `GET /models` and the bearer form.
- OpenRouter: both base URLs, `GET /api/v1/models` and the bearer form. Claude Code sends the variable
  `ANTHROPIC_AUTH_TOKEN` as a bearer token.
- Z.ai: the two base URLs and the bearer form of the OpenAI format. No page that the author read
  gives a models URL. The coding plan has its own base URL, which the author did not read.
- Together AI: the base URL, `GET /v1/models` and the bearer form. No Anthropic endpoint is
  documented.
- Ollama: the OpenAI base URL, the Anthropic base URL, `GET /v1/models`, and a key that the server
  ignores. vLLM documents `/v1/models` and `/v1/messages`. LiteLLM documents the default port 4000,
  `GET /models`, and `/v1/messages` with the header `x-api-key` in its examples.

Rules of the table:

- A preset sets defaults only. The administrator can change each field that the table gives, except
  the protocol of a row.
- The first form of the column Auth is the default. The route of presets serves the default form only.
  An endpoint of each preset accepts the three forms, with the rule of `anthropic-oauth`.
- A row with "None" in the column Base URL has no default. The text after "To verify" is a hint for
  the administrator.
- A Custom endpoint has the protocol and the auth that the administrator chooses. The form
  `anthropic-oauth` is valid with the protocol `anthropic` only.
- An endpoint of the protocol `anthropic` has the type `anthropic` for `chat.completion`. An endpoint
  of the protocol `openai` has the type of its row, or `openai` for Custom.
- The type `openai-codex` (the subscription of ChatGPT) and the type `opencode` have no row. They need
  no endpoint in this version.

## The models list

For each endpoint, the server keeps a list of model names and the date of its last good read. The
list is a help for the author. It is not a check: a step can name a model that the list does not hold.

### Read

The server calls the models URL with the key and the form of the endpoint. The call is a `GET`. It uses
the same upstream settings as the gateway: the proxy of `ai.upstream.proxy_url` only, TLS verification
and no redirect. A redirect is an error. The key goes to the host of the models URL only.

For the protocol `anthropic`, the call adds the header `anthropic-version` with the value `2023-06-01`
and the query `limit=1000`. It follows the pages while `has_more` is true, up to 10 pages. The cursor of
the next page is the field `last_id` of the response, sent as the query `after_id`. For the protocol
`openai`, the call has no query, and it reads the field `data` of one response. The name of a model is
the field `id` of each item. A response with no field `data` is an error.

The timeout `ai.models_timeout_sec` limits the whole read of one endpoint, all its pages together.

The call sends the headers `accept: application/json` and `accept-encoding: identity`. A response with
`has_more` true and no new value of `last_id` is an error. After 10 pages, the read stops and keeps the
names that it has.

The server accepts a response of at most 4 MiB. It keeps at most 5000 names, the first ones. A name has
at most 256 characters, and the server drops a longer name, an empty name and a name that is not valid
UTF-8. It drops a name that repeats. Dagu does not change a name. For example, the OpenAI form of Google Gemini gives names that
start with `models/`, and the list holds them as they are.

For the protocol `anthropic`, the server also keeps two details of each model. The first detail is
`display_name`, the name that a person reads. The second detail is the list of the effort levels that the
model supports. The server reads the levels from the field `capabilities.effort`. A level counts when its
field `supported` is true. The levels are `low`, `medium`, `high`, `xhigh` and `max`, in this order. A model
whose `capabilities.effort.supported` is false has no level.

The value of a level is an object with the field `supported`, or a boolean. A level with another value
does not count. A model with no field `capabilities.effort.supported` has the levels that its fields
give.

The server drops a display name that is empty, is not a string, has a control character or has more than
256 characters. The model stays in the list with no display name. A bad detail never fails the read. The
protocol `openai` gives no detail, so its list has names only.

The models URL of a preset follows the base URL of the endpoint. The text `{base_url}` of the table
stands for the base URL. Some URLs of the table start with the default base URL of the row. For such a
URL, the server puts the base URL of the endpoint in place of the default, and keeps the rest. The preset Custom uses
its field `models_url`.

The endpoint has no list when the models URL is empty. This case is valid. The preset Custom with no
`models_url` is this case, and so is a row of the table with no models URL. The field `model` of a step
then stays free text.

The error of a failed read is the status of the provider, such as `the provider answered with the
status 403 Forbidden`, or the cause of the failure of the call. The server never reads the body of an
error answer, because a provider can write the credential in it. The server also replaces the key in the
error text, if it finds it there.

### Refresh and reload

The server refreshes each list once every `ai.models_refresh_hours` hours. The default is 24. The
value 0 stops the automatic refresh. The first read comes when an administrator creates the endpoint,
rotates the key or presses Reload. The create and the rotation start the read and answer at once: the
save of a key does not wait for the provider. An administrator can reload one endpoint at any time. The
Reload waits for the provider, up to `ai.models_timeout_sec`. A reload writes the audit action
`ai_endpoint_models_reload`, with the field `ok` that tells if the read was good. A change of the base
URL, the form or the models URL does not start a read.

The server process runs the refresh. The coordinator and the workers do not. The server keeps the list,
the time of the last read, good or not, and the last error in the collection `ai_endpoint_models`. The
period counts from the last read. A restart does not read again at once. A provider that fails gets at
most one call in a period. The refresher sleeps at most one hour between two scans, so it sees
an endpoint that another process created. It adds a random time to its start, at most 30 seconds. It
adds a random time to each sleep, at most one tenth of the sleep and at most 5 minutes. So the servers on
one data directory do not call a provider at the same moment. The refresher never delays the start of
the server.

On a failure, the server keeps the last good list. It keeps the error with its time. The AI page shows
both: the error and the list with its date. The error never has the key.

An OAuth token of a subscription can lack the right to list models. The read then fails with the
status of the provider. The UI shows this error, and the author writes the model name by hand. On
2026-11-04 the Anthropic API accepted such a token for this call.

### Use in a step

The Model field of the form of a step shows the list of the chosen endpoint as suggestions. A free text
value is always typeable. The model check of this section decides if the step runs.

#### The model check

The model check is part of the [Start checks](094-ai-gateway.md#start-checks). It applies when all these conditions are
true:

- the step names an endpoint, and the check of the endpoint passed
- the model of the step is a literal string after resolution. A `${...}` reference is resolved at the
  start of the step, and the check uses the result.
- the endpoint has a list that a read made. It has a models URL, one read was good, and the list has a
  name

The model MUST be in the list. The match is exact: Dagu does not change the case, a prefix or a suffix.
A model that the list lacks fails the step before the CLI starts and before the first request. The
reason is `model_unknown`. The message follows the style of the other reasons:

```text
step "code_1": endpoint "cle1": model_unknown: the model "claude-sonet-4-6" is not in the models list of the endpoint (read at 2026-11-05T09:14:02Z); reload the list on the AI page, or fix the model
```

The date is the time of the last good read, in UTC. The failed step has the exit code 1 and the kind
`error`, as each other failure of the start check.

A chat step checks each model entry that has an endpoint, in the order of the entries, with the same
rule. It checks all entries when it starts. An entry that is only a fallback also fails the step at start.
An entry on the old method has no endpoint, so it has no check.

#### The refresh on demand

A provider can release a model after the last read. A miss therefore starts one refresh before the step
fails:

1. The coordinator reads the list again, with `RefreshModels`. A local run does it in its own process.
   The read ends with `ai.models_timeout_sec`.
2. The check runs again on the new list. A model that the new list has passes.
3. A model that the new list lacks fails the step. The date of the message is the date of the new read.

An endpoint gets at most one refresh on demand in 60 seconds, in each process that holds the gateway. A
miss in this window uses the list that the process holds. A failed refresh keeps the last good list, and
the check uses it. The refresh on demand does not change the period of the automatic refresh.

#### Cases with no check

The check is skipped, and the step goes on, in these cases:

- The endpoint has no models URL. The preset Custom with no `models_url` is this case.
- The list was never read, or the last read failed and no good list exists.
- The list is empty.
- The model is empty after resolution. An `agent.run` step of the provider `claude` MUST name a model, so only a reference can give an empty value.
- The process holds no models store, for example a coordinator that cannot open the data directory. The log
  of the process says it.

The provider then answers at the first request. Its status 4xx for an unknown model is an
`upstream_error`. This is the only error of a model that the check cannot find.

#### The aliases of the CLI claude

The CLI `claude` accepts model names that the models API does not list. For `agent.run` with the provider
`claude`, these model values pass the check with no list:

- `default`, `best`, `sonnet`, `opus`, `haiku`, `fable`, `mythos` and `opusplan`
- each of these names with the suffix `[1m]`, for example `sonnet[1m]`

The match of the name of an alias ignores the case. The suffix `[1m]` is in lower case. A listed model
with the suffix `[1m]` also passes.

The list is data in one place of the code: `CLIModelAliases` of `internal/aiendpoint`. It comes from Claude Code
version 2.1.311: its `--model` option and its table of aliases. Dagu does not translate an alias. It
passes the value to the CLI.

A `chat.completion` step has no alias. It sends the name to the API as it is.

The route of choices gives the list as the field `cliModelAliases`, so that the editor applies the same
rule.

#### The models in the form and on the node

The form suggests the models of the endpoint, and a free text stays typeable. The Model node lists them.
A name that the list lacks is not an error of the form. The form shows a muted line, and the node shows an
amber marker. Both say that the step fails when it starts, unless a refresh of the list finds the name. A
reference and an alias of the CLI never get this line. See [The step forms](#the-step-forms).

`dagu validate` does not check the model. It does not read the store.

## Configuration

The block `ai` of the Dagu configuration sets the operational values of the gateway and of the models
list. The coordinator, the server and a local process read it. The worker reads none of its keys. Each
process that loads the configuration checks the whole block, also a process that does not use it.

The schema is closed. An unknown key, a duplicate key or a wrong type stops the process at start. A
value outside its range also stops it. The error names the key, the expected type or range, and the
value. No value is changed to make it fit. A number with a decimal point, such as `3.0`, is not an
integer.

| Key | Type | Default | Range |
| --- | --- | --- | --- |
| `ai.models_refresh_hours` | integer | 24 | 0 to 720. 0 stops the automatic refresh. |
| `ai.models_timeout_sec` | integer | 30 | 1 to 300 |
| `ai.upstream.proxy_url` | string | empty | An `http` or `https` URL with no user information. Empty means a direct call. |
| `ai.relay.max_request_bytes` | integer | 16777216 | 1024 to 268435456 |
| `ai.relay.max_response_bytes` | integer | 268435456 | 1024 to 1073741824 |
| `ai.relay.max_header_bytes` | integer | 32768 | 1024 to 1048576 |
| `ai.relay.max_streams_per_run` | integer | 8 | 1 to 256 |
| `ai.relay.max_streams` | integer | 128 | 1 to 65536 |
| `ai.relay.connect_timeout_sec` | integer | 10 | 1 to 120 |
| `ai.relay.response_header_timeout_sec` | integer | 600 | 1 to 3600 |
| `ai.relay.idle_timeout_sec` | integer | 300 | 1 to 3600 |
| `ai.relay.total_timeout_sec` | integer | 3600 | 1 to 86400 |

These pairs of keys contradict each other and stop the process at start:

- `ai.relay.max_streams_per_run` above `ai.relay.max_streams`
- `ai.relay.response_header_timeout_sec` above `ai.relay.total_timeout_sec`
- `ai.relay.idle_timeout_sec` above `ai.relay.total_timeout_sec`

The coordinator never reads the variables `HTTP_PROXY`, `HTTPS_PROXY` or `NO_PROXY`. A deployment that
needs a proxy sets `ai.upstream.proxy_url`.

No key of the block holds a secret. A key of a provider is not a key of the file. The block has no
`api_key`, and the keys have no variable of the environment. See [Open items](#open-items) for the
files of GitOps.

The block `ai` is file only. If the variable `DAGU_AI` exists in the environment, the process stops at
start. The error names the variable and the key, and never shows the value:

```text
config: the environment variable DAGU_AI is not read: set the block ai in the configuration file
```

`dagu config` prints each key of the block with its value and its source (`file` or `default`). The
coordinator and the server log the same values at start. The table of keys in the code gives the
schema, the defaults and the printed values, as for the block `agent_tools`. The schema file
`config.schema.json` describes the block. A test checks the table of this spec, the schema file and
the table of the code against each other.

## The UI contract

### The AI page

The entry "AI" of the sidebar opens the AI page. Its route is `/ai`. The page has three tabs:

| Tab | Route | Content |
| --- | --- | --- |
| Endpoints | `/ai/endpoints` | The endpoints: the keys and the addresses of the model APIs. |
| MCP | `/ai/mcp` | The MCP server entries with their credentials. |
| Skills | `/ai/skills` | The skills of the server. A skill is a folder of files, not a secret. See Spec 096. |

Each user sees the entry "AI". An administrator sees the three tabs, and `/ai` opens the tab Endpoints. A
user with another role sees the tab Skills only, with no row of tabs. The API refuses the routes of the
endpoints and of the MCP servers to such a user with the status 403.

The tab Endpoints has a list and a dialog:

- The list has one row for each endpoint. The columns are Name, Provider, Scope, Key, Key updated,
  Allowed and Models. The column Provider shows the label of the preset and, as a muted text, the
  protocol. The column Key shows the prefix and the fingerprint of the key, or "No key".
- The dialog "Add an endpoint" has the fields of [Fields](#fields). The preset select fills the
  defaults. The scope select has "Global" and each workspace. The fields `allow.workspaces` and
  `allow.dags` are lists with a choice of names that exist, and a check box for the value `*`. The dialog
  of a change shows the preset, the protocol, the name and the scope as values that the user cannot
  change.
- The field Key is a password field. It is empty when the dialog opens. It is required on a create,
  except for a preset of a local model. A change has no field Key: the action "Rotate key" of the row
  sets a new key. The field shows no value after the save.
- The page shows the prefix and the fingerprint as text. It has no "show" button.
- The column Models has the number of models, the date of the last good read, a Reload button and the
  last error. A failed read keeps the last good list on the screen. The number opens the names with a
  search box. An endpoint with no models URL shows "No model list" and has no Reload button. The Reload
  button shows a busy state while the read runs. An endpoint can have a models URL and no result of a
  first read. Then the list asks the server again every 2.5 seconds, at most 12 times. A create, a change and
  a rotation start the count again. For an endpoint of the form `anthropic-oauth`, the error has an added
  sentence: the token of a subscription can lack the right to list models.
- A delete asks for a confirmation that names the endpoint.

### The step forms

The Model section of the form of `agent.run` and of `chat.completion` has an Endpoint select, the Model
field and the Effort field. In the form of `agent.run`, the Provider field comes first.

The Endpoint select writes `endpoint.name`, the Model field writes `endpoint.model` and the Effort field
writes `endpoint.effort`. For a step of `chat.completion` on the old method, the Model field writes
`with.model`.

- The Endpoint select lists the endpoints that the DAG may use, from the route of choices. For
  `agent.run` it lists the endpoints of the protocol `anthropic` only. For `chat.completion` it lists
  each endpoint.
- The select of `agent.run` has no "(none)" item. A step with no endpoint shows the error `agent.run
  needs an endpoint; add one on the AI page and name it here` under the select.
- The select of `chat.completion` has the first item "(none)" while the step has no block `endpoint`.
  "(none)" keeps the old method. A step with the block `endpoint` has no "(none)" item, and a block with
  no name shows the error `chat.completion needs the name of an endpoint`.
- When the DAG may use no endpoint, the form shows the text "No endpoint is available for this DAG."
  in the place of the select. A link to the AI page follows the text.
- A name that the server does not know, or that the DAG may not use, stays as an item. The form shows an
  amber issue under the select. A wrong protocol shows an amber issue too. The form shows the
  contradictions of the server under the select.
- For `chat.completion`, the Provider field is not drawn while the step has an endpoint and no provider.
  A provider that the YAML holds with the endpoint stays visible, so that the contradiction can go.
- The Model field is free text, and a `${...}` reference is valid. It shows the names of the models of
  the chosen endpoint as suggestions. A suggestion replaces the whole text. The field writes on blur.
  With no endpoint, or an endpoint with no list, it shows no suggestion.
- A step with an endpoint and no model shows the error `agent.run needs a model; choose one that the
  endpoint lists`, or the same error of `chat.completion`, on the field.
- A name that the list lacks is not an error of the form. A muted line says that the step fails when it
  starts, unless a refresh of the list finds the name. A `${...}` reference and, for `agent.run` with the
  provider `claude`, an alias of the CLI get no line. An endpoint with no list gets a muted line, and a
  link to the AI page for an administrator, who can reload the list there.
- The Effort field has the first item "Default", which removes the key, and the levels of the chosen
  model from `efforts`. For `agent.run`, a model with no `efforts` gets the five levels of the CLI:
  `low`, `medium`, `high`, `xhigh` and `max`. For `chat.completion`, a model with no `efforts` gets no
  level. A step of `chat.completion` on the old method has no Effort field: the author sets `thinking`
  in the YAML.
- A row of a model list has its own Endpoint select, and no Provider select while it has an endpoint.
  The select of a row has the item "(none)". A pick of an endpoint removes the `provider` of the row.

#### Edits of the endpoint and the model

The form and the Model node make the same edits. One undo step reverts each edit.

- A pick of an endpoint on a step of `chat.completion` on the old method removes the keys of the old
  method. These keys are `provider`, `base_url`, `api_key_name`, `model` and `thinking`, when the YAML
  holds them. In the same
  edit, it writes `with.endpoint.name`, moves the model to `with.endpoint.model`, and moves
  `thinking.effort` to `with.endpoint.effort`.
- When the endpoint name of a step changes, the editor removes the model if the new endpoint has a models
  list that lacks it. A `${...}` reference and an alias of the CLI claude stay. The editor removes no model
  before the choices arrive, and none for a server that sends no `cliModelAliases`.
- When the endpoint name changes and the model stays, the editor removes `endpoint.effort` if the model
  does not support the level. A pick of a model that does not support the level removes it too.
- A row of a model list keeps its model when its endpoint changes. An entry of a list is a map, and the
  server refuses a bare name.

#### The Model node

The Model node of the canvas is the place where the author picks the endpoint and the model of a step. In
the edit view, in a canvas with details, the node is a card with the controls. A compact canvas and a run
view draw a round node. It shows the model and, as its detail, the endpoint or the provider (Spec 091).
Its tooltip lists the name, the endpoint and the provider of each model. The run view stays read-only and
asks no endpoint.

The card has one block for each model. An `agent.run` step has one block. A `chat.completion` step has one
block for each entry of its model list. The title of the block is "Model n", where n is the number of
the entry. A
step with the block `endpoint` has one block. The controls have the class `nodrag nopan`, and a keyboard
reaches them:

- The Endpoint select. It lists the endpoints of the route of choices for the DAG. For `agent.run` it lists
  the protocol `anthropic` only, and for `chat.completion` it lists both protocols. The select of a step has
  no "(none)" item. The select of an entry of a model list that has an endpoint has the item "(none)". For
  a step on the old method, the closed select shows the provider with the text "(provider)". A name that
  the YAML holds and the server does not know stays visible as a value, with an amber marker.
- The Model select. It lists the models of the endpoint, and for `agent.run` with the provider `claude` the
  aliases of the CLI. Each item shows the display name of the model from `modelInfo`, or the name when the
  model has no display name. The name stays visible as a muted second text, because the YAML holds the
  name, and a pick writes the name. A `${...}` reference that the YAML holds stays as an item. The closed
  select shows the muted text "(choose a model)" in two cases: the step has no endpoint, or the model
  is not in the list. "(choose a model)" is never an item. With no list, the control is the free text field of
  the form, with the same rule: it writes on blur.
- The Effort select, for a step with one model. It works as the Effort field of the form. A model with no
  level shows a disabled select.

The `agent.run` card also shows the CLI provider, for example `claude`, as a muted line. The form edits the
CLI provider. A double click on the card opens the form.

A marker with a one-line tooltip sits on the control that is wrong. A red marker shows an error that stops
the load of the DAG:

- A step of `agent.run`, or an entry of `chat.completion` with no provider, has no endpoint.
- A block `endpoint` has no name, or a step with an endpoint has no model.

An amber marker shows a problem that fails the step when it starts. The rules are the rules of the
server:

- The server does not know the endpoint, or the DAG may not use it. Or the endpoint has a protocol that
  the step does not accept.
- The endpoint has a models list, the model is a literal, and the list lacks it. An alias of the CLI claude
  and a `${...}` reference never get this marker. The marker makes no claim before the choices have
  arrived, for an endpoint with no list, and for a server that sends no `cliModelAliases`.

An amber marker also shows on a step of `chat.completion` that uses the old method with a provider. The
tooltip says that the provider is deprecated, and that an endpoint keeps its key in the server.

The round node of a compact canvas shows the first amber marker of the server rules. It shows no red
marker.

The node asks the route of choices, the request that the Endpoint select of the form already makes. The
card shows its controls disabled while the canvas is frozen.

## Errors

`dagu validate` rejects these cases with a nonzero exit code:

- a `secrets:` entry of the DAG with a `ref` that starts with `ai/`. The error has the text `registry ref
  "<ref>" is reserved for the AI feature`. A queued DAG that skips the validation fails at the resolver
  with the same rule, and the value stays in the coordinator.

These cases stop a process at start with the exit code 1:

- an invalid or unknown key of the block `ai`, or a contradiction of two keys

These cases fail a step at run time, with the reasons of [Failure kinds](094-ai-gateway.md#failure-kinds):

- a literal model that the models list of the endpoint lacks, at the start check, after one refresh on
  demand: the reason is `model_unknown`

The errors of the block `endpoint` and of the relay are in [Spec 094](094-ai-gateway.md#errors). The
errors of the MCP server entries are in [Spec 095](095-mcp-servers.md#errors).

## Examples

### An endpoint

The create request of an endpoint. The key is in the body of the request only.

```json
{
  "name": "cle1",
  "preset": "anthropic",
  "protocol": "anthropic",
  "scope": "global",
  "allow": { "workspaces": ["*"], "dags": ["fix-bug", "review"] },
  "key": "<the API key>"
}
```

The response of a read:

```json
{
  "name": "cle1",
  "preset": "anthropic",
  "protocol": "anthropic",
  "baseUrl": "https://api.anthropic.com",
  "auth": "x-api-key",
  "scope": "global",
  "allow": { "workspaces": ["*"], "dags": ["fix-bug", "review"] },
  "keyPrefix": "sk-ant",
  "keyFingerprint": "0123456789ab",
  "keyUpdatedAt": "2026-11-04T10:00:00Z",
  "models": { "supported": true, "count": 14, "refreshedAt": "2026-11-04T10:00:05Z" },
  "createdAt": "2026-11-04T10:00:00Z",
  "updatedAt": "2026-11-04T10:00:00Z"
}
```

The response of a create, a change and a rotation has the same form. A field with no value, such as
`description` or `modelsUrl`, is absent.

### A configuration

The block `ai` of the Dagu configuration of the coordinator:

```yaml
ai:
  models_refresh_hours: 24
  relay:
    max_request_bytes: 16777216
    idle_timeout_sec: 300
```

An invalid value:

```text
config: key "ai.relay.max_streams_per_run": expected an integer from 1 to 256, got 0 (int)
```

The text after `got` shows the value and its type.

## Conformance

The tests run the Dagu binary on static fixtures in `conformance/spec093_ai_endpoints/`. They use
these fakes. The fake upstream is a local HTTP server that records each request and serves SSE. The
fake `claude` is an executable that calls `ANTHROPIC_BASE_URL` with `ANTHROPIC_API_KEY`. No test needs
an external service. The fixtures name the endpoint `test-endpoint` and the model `claude-test`,
except where the name of a model is the subject of the test.

The tests of a worker run a coordinator with mutual TLS and the pools of Spec 092. A test of an agent
worker changes to the restricted user. It needs Linux, the user ID 0 and the flag
no-new-privileges. It skips on another host. The table marks these tests "root". A test of a
general worker runs with each user ID.

Each test that has a key checks that the key is in no response, no output, no log and no file of the
worker.

| Section | Tests |
| --- | --- |
| The endpoint: fields, key, storage, routes | `TestTheKeyIsNeverShown`, `TestKeyDisplayRule`, `TestRotationChangesTheFingerprint`, `TestScopeAndUniqueness`, `TestChoicesFollowTheUseRule`, `TestDeleteRemovesTheEndpointAndItsKey`, `TestInvalidRequests`, `TestRoutesNeedAuthentication`. |
| The reserved prefix `ai/` | `TestSecretsBlockRefusesReservedRefs`. The refusal of the resolver, of the coordinator and of a runtime profile: Go tests. |
| Provider presets | `TestInvalidRequests` for the presets of a local model. The route of presets: Go tests of the handlers. |
| The models list: read, refresh and reload | `TestTheServerReadsTheModelsListWithTheKey`, `TestTheModelsCallUsesTheFormOfTheEndpoint`, `TestAFailedReloadKeepsTheLastGoodList`, `TestAnEndpointWithoutAModelsURLHasNoList`, `TestAKeyThatCannotListModelsGivesAnErrorAndTheEndpointStillWorks`, `TestTheChoicesCarryTheModels`, `TestTheModelInfoCarriesTheDisplayNameAndTheEfforts`, `TestTheModelsRoutesNeedAuthentication`. The automatic refresh and its value 0: a Go test with a fake clock, because a period is at least one hour. |
| The model check | `TestModelCheckLocalMode`, `TestModelCheckOnAWorker`, `TestModelCheckOnAnAgentWorker` (root). |
| Routes and audit | Go tests of the handlers. The audit store needs a license. |
| Configuration | `TestAIConfigIsClosedAndStrict`, `TestServerStopsForAnInvalidAIBlock`, `TestAIConfigHasNoEnvironmentBinding`, `TestConfigCommandPrintsTheEffectiveAIBlock`. The Go test `TestAIKeyDocsMatchTheSpec` checks the table of this spec against the table of the code. |
| The UI contract | Not covered here. The tests of the UI cover it. |

## Open items

Each item has a default. The default applies until the owner decides.

### Measured with Claude Code

The author ran the real Claude Code 2.1.311 on 2026-11-04. The model was `claude-haiku-4-5`. The
endpoint held an OAuth token of a subscription, with the form `anthropic-oauth`. The agent worker
held no model key, and the proxy had no model route. The demo `examples/agent-worker-demo` holds the
scripts and the evidence format. These facts are measured.

| Subject | Fact |
| --- | --- |
| The models list | `GET /v1/models` with the OAuth token and the beta `oauth-2025-04-20` gave 14 names. A Reload gave the same list. The Anthropic API accepted the token. |

### Decisions to confirm

| Item | Default |
| --- | --- |
| An endpoint for GitOps: an endpoint whose key is named by `api_key_file` or `api_key_env` in a file. | Not added. Endpoints and keys are managed data. The need is not shown. |
| A local model with no key. The auth forms have no value `none`. | The preset Local model accepts an empty key and sets no header. |
| A workspace endpoint hides a global endpoint of the same name. | As in this spec. |
| A limit of models for an endpoint (`allow.models`), so that an agent cannot use an expensive model. | Not added. The agent can use each model that the key allows. |
| The native `gemini` type of `chat.completion` and the type `openai-codex` as endpoints. | Not endpoint protocols. |
| No route reads one endpoint: the list gives all the fields. | Not added. The routes of the table are the routes of the spec. |
| A role other than admin needs a license. The suite cannot test the refusal of a role at the binary level. | A Go test of the handlers covers the roles. The suite checks the refusal with no token. |
| The audit log needs a license. The suite cannot read the audit lines at the binary level. | A Go test of the handlers covers the lines. |

### Values to verify

| Item | State |
| --- | --- |
| The models URL of DeepSeek: the page gives `GET /models` with no host. | To verify. |
| The models URL and the header of the Z.ai Anthropic endpoint. | To verify. |
| The coding plan endpoint of Z.ai. | The author did not read it. No row. |
| The Anthropic compatibility of xAI. A third-party page says it is deprecated. | No row. To verify on the page of xAI. |
| The Anthropic compatibility of Together AI. | None found. No row. |
| The default port and the key header of vLLM, and the header of LiteLLM for the OpenAI format. | To verify. |
| The models URL for the Anthropic rows of third parties (DeepSeek, Z.ai, vLLM, LiteLLM). | The rows use the OpenAI models URL. To verify that the names are the same. |
| The value of `anthropic-version` that the models call sends. The page of 2026-11-04 shows `2026-10-21` in its example and lists the header as optional. | The call sends `2023-06-01`, the value of the Anthropic provider of Dagu. To verify that the endpoint still accepts it. |
| The names of the OpenAI form of Google Gemini start with `models/`. | The list keeps the names as they are. To verify that `chat.completion` accepts the prefix. |
| The models URL of DeepSeek. The page of 2026-11-04 shows `GET /models` and the base URL `https://api.deepseek.com`, and no page gives the full URL. | The table keeps `https://api.deepseek.com/models`. A failed read shows the error. |
| Anthropic documents `authorization: Bearer` as the main form and `x-api-key` as a legacy form that stays. | The default stays `x-api-key`. To verify the long term. |

### Technical risks

| Risk | Note |
| --- | --- |
| An OAuth token of a subscription can lack the right to list models. | On 2026-11-04 the Anthropic API accepted such a token for the models list. The provider can refuse it in the future. The page then shows the error and a sentence that says why, and the model stays free text. |
| A list of more than 5000 models. | The server keeps the first 5000 names and shows no error. The field `model` stays free text. |
| Two servers on one data directory refresh the same list. | The period counts from the stored time of the last read, so the calls are rare. A short race can give two reads. The server has no lock for it. |
| A local model host. `localhost` is the host of the coordinator. | The preset says so. A deployment gives a name that the coordinator resolves. |
| A key that a person pasted can be wrong, and the save does not test it. | The first models read shows the error. |
