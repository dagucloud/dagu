# Spec 093: Model Connections

## Status

Not implemented.

This spec describes planned behavior. No version of Dagu accepts a
connection of the provider `model`, or the step key `connection` of
`chat.completion`, `agent.run`, or `harness.run`, yet. One rule is already
enforced: Dagu reserves the secret ref prefix `ai/`, as Spec 069 and
Spec 103 state. This spec stores no key in the secret store, and the
prefix stays reserved.

**Open items.** These points are not decided:

- Where the gateway that adds a static key runs for a step on an agent
  worker: in the coordinator, as [Spec 094](094-ai-gateway.md) plans, or
  next to the worker, such as in the egress proxy of
  [Spec 105](105-egress-proxy.md). See [Model Traffic](#model-traffic).
- Sign-in for a subscription, such as a Claude or ChatGPT plan, through
  the connect flow of Spec 107. The terms of each provider for a token
  used through a gateway are not checked. A long-lived token that a CLI
  prints for automation is a key, as [Credentials](#credentials) states.
- Whether `agent.run` and `harness.run` become one action. Both run a CLI
  agent, and this spec gives both the same keys.
- Whether a page sums the token counts of [Runs](#runs) per connection,
  and through which index.
- The protocols that each preset serves. A value marked "To verify" is
  not checked.

This spec extends [Spec 107: Connections](107-connections.md) with the
provider `model`. It uses [Spec 102: Projects](102-projects.md) for
ownership and base config. [Spec 059](059-chat-completion.md),
[Spec 067](067-harness.md), [Spec 091](091-agent-node.md), and
[Spec 092](092-agent-workers.md) define the steps that use a model
connection. [Spec 095](095-mcp-servers.md) and
[RFC 026](../rfcs/026-mcp-tools-for-ai-steps.md) give the same steps
their MCP tools.

## Scope

This spec defines:

- the provider `model` of a connection: its fields, credentials, status,
  and presets
- shared keys: one key that connections in several projects use
- the models list of a connection
- the step keys `connection`, `model`, and `effort` of `chat.completion`,
  `agent.run`, and `harness.run`, and the keys `connection` and `model` of
  the DAG-level `llm` block
- CLI adapters: what an adapter does, and which CLIs have one
- the policy that makes steps use model connections
- the model controls of the UI, and the routes they use

This spec does not define:

- connection records, names, ownership, the broker, binding, events,
  permissions, and audit, beyond what this spec adds. See Spec 107.
- the gateway that adds the key to each request. See Spec 094.
- MCP servers. See Spec 095 and RFC 026.
- the other keys of `chat.completion` (Spec 059), `harness.run`
  (Spec 067), and `agent.run` (Spec 091).
- untrusted steps and the restricted user. See
  [Spec 103](103-untrusted-steps.md).
- the egress proxy and its key injection. See Spec 105.

## Goal

- A workflow author names a connection and a model. The author never
  writes a key in a DAG, in a file, or in an environment variable.
- The same keys work for `chat.completion` and for every CLI that has an
  adapter: Claude Code, Codex, OpenCode, and pi.
- An admin adds a key once, shares it with projects, and rotates it in
  one place.
- On a personal machine, an agent step runs with the CLI's own sign-in
  and no setup.
- An author sees a combination that cannot work while editing, not when
  a run fails.

## Terms

A model connection is a connection of Spec 107 with the provider `model`.

A protocol is the form of a model API: its paths, headers, and bodies.

| Protocol | API |
| --- | --- |
| `anthropic-messages` | Anthropic Messages: `POST /v1/messages` |
| `openai-chat` | OpenAI Chat Completions: `POST /chat/completions` |
| `openai-responses` | OpenAI Responses: `POST /responses` |

A preset is a row group of [Presets](#presets). It gives one provider's
defaults.

A shared key is an app of Spec 107 with the provider `model`. It holds one
key, and connections in several projects use it.

An adapter is the part of Dagu that points one CLI at the gateway of its
step. A CLI is a harness provider of Spec 067, such as `claude` or
`codex`.

A CLI's own sign-in is the credential the CLI finds by itself: its login
state, its config files, or its environment.

The gateway, the relay, and the relay secret are as in Spec 094.

## Model Connections

### Fields

A model connection has the Spec 107 metadata fields `name`, `project`,
`provider`, `app`, `grant`, and `holder`, and these fields:

| Field | Values and rules |
| --- | --- |
| `preset` | A preset ID, or `custom`. Fixed at creation |
| `protocols` | A map from a protocol to its `base_url` and `auth`. At least one entry. A preset fills it, and a person may remove an entry. `custom` sets each entry |
| `models_url` | `custom` only. See [Models List](#models-list) |
| `allowed_models` | Optional. Model IDs or CLI aliases, matched exactly. Empty means every model |

- `base_url` is an `https` URL with no user information, query, or
  fragment. A local-model preset and `custom` also accept `http`, and the
  UI warns that the key crosses the network unencrypted.
- `auth` is `x-api-key`, `bearer`, or `anthropic-oauth`, the forms of
  [Key injection](105-egress-proxy.md#key-injection).
  `anthropic-oauth` is valid with `anthropic-messages` only.
- `grant` is `api_key`, `authorization_code`, `device_code`, or `none`.
  `app` names a shared key or the OAuth app of a sign-in, and is empty
  otherwise. See [Credentials](#credentials). Spec 107 lists the provider
  `model` and these grants.
- `services`, `scopes`, `subject`, `bound_hosts`, and `resource` do not
  apply.
- `allow_token_export` is always off. No step, `secrets` entry, or route
  reads the key of a model connection. A `secrets` entry with
  `provider: connection` that names one fails admission.

### Credentials

| Credential | `grant` and `app` | Where the key is | Who adds it |
| --- | --- | --- | --- |
| Own key | `api_key`, no `app` | In the connection's token state, encrypted | `manager` or above in the project |
| Shared key | `api_key`, `app` names a [shared key](#shared-keys) | In the app | An admin, through Share |
| Sign-in | `authorization_code` or `device_code`, `app` names an OAuth app | In the token state, from the connect flow of Spec 107. The broker refreshes it | `manager` or above in the project |
| No key | `none`, no `app` | Nowhere. Valid for a local-model preset only | `manager` or above in the project |

- A key is write-only. After a save, no route and no page shows it. Pages
  show a prefix and a fingerprint. The prefix is the first characters of
  the key: the smaller of 6 and a tenth of its length, rounded down. A key
  shorter than 10 characters has no prefix. The fingerprint is the first
  12 hexadecimal digits of the key's SHA-256.
- A key is not empty, is valid UTF-8 with no control character, and has
  at most 8192 bytes. A save does not call the provider.
- A long-lived token that a CLI prints for automation, such as the token
  of `claude setup-token`, is an own key with the auth `anthropic-oauth`.
- A rotation replaces the key in one place: the connection for an own key,
  the app for a shared key. Every connection on a shared key sends the new key
  from its next request.
- The key is stored as Spec 107 [Records](107-connections.md#records)
  state: encrypted with the server's key, and never in the secret store.

### Connection Status

A sign-in connection follows the Spec 107 status transitions. This spec
adds these for the grants `api_key` and `none`:

| From | To | When |
| --- | --- | --- |
| none | `connected` | The connection is created |
| `connected` | `needs_reconnect` | The provider answers 401 to a request that carried the stored key |
| `needs_reconnect` | `connected` | The key is rotated |

- A 401 causes no refresh and no retry, since a key has no refresh.
- A 403 changes no status. It can deny one model and allow others.
- For a connection on a shared key, the 401 sets the app's status to
  `invalid` in place of the connection's, and a rotation of the app sets
  it back to `ok`. Every connection on the app shows the status.
- The change records the event `connection.needs_attention` of Spec 107,
  once, however many runs see the 401. The UI calls this status "Key
  rejected".

### Shared Keys

- A shared key is an app with the provider `model`. It holds `preset`,
  `protocols`, `models_url`, and the key, encrypted as Spec 107 keeps a
  service account key. It has no `kind`, `client_id`, `client_type`, or
  `endpoints`. Apps are server-wide, as Spec 107 states.
- Only an admin creates, changes, rotates, shares, or deletes a shared
  key.
- Share picks projects, or all projects. Dagu creates, in each picked
  project, a connection of the app's name with the grant `api_key` and
  that app. With all projects, a project created later gets one too.
- A connection on a shared key holds no key. It takes `preset`,
  `protocols`, and `models_url` from the app. A manager of its project
  may set its `allowed_models`, or remove it. Removing it leaves the
  connections of other projects working.
- An app that a connection uses cannot be deleted, as Spec 107 states.
  The share list shows where each connection is used.

### Binding

- The bound hosts of a model connection are the hosts of its `base_url`
  entries and of its models URL. A person adds no other host.
- Spec 107 [Binding](107-connections.md#binding) applies to the gateway
  and to the models reader, with one exception: a local-model preset or
  `custom` sends the key over `http` to a base URL that a person entered.
- A redirect from a request that carries the key fails the request.

### Presets

The presets are data of the server. A new row needs no change to a DAG.
Each preset lists the protocols its provider serves. One connection
serves all of them with one key.

| Preset | Protocol | Base URL | Auth | Models URL |
| --- | --- | --- | --- | --- |
| `anthropic` | `anthropic-messages` | `https://api.anthropic.com` | `x-api-key` | `https://api.anthropic.com/v1/models` |
| `openai` | `openai-responses` | `https://api.openai.com/v1` | `bearer` | `https://api.openai.com/v1/models` |
| | `openai-chat` | `https://api.openai.com/v1` | `bearer` | |
| `google-gemini` | `openai-chat` | `https://generativelanguage.googleapis.com/v1beta/openai` | `bearer` | `https://generativelanguage.googleapis.com/v1beta/openai/models` |
| `mistral` | `openai-chat` | `https://api.mistral.ai/v1` | `bearer` | `https://api.mistral.ai/v1/models` |
| `deepseek` | `openai-chat` | `https://api.deepseek.com` | `bearer` | To verify: `https://api.deepseek.com/models` |
| | `anthropic-messages` | `https://api.deepseek.com/anthropic` | `x-api-key` | |
| `xai` | `openai-chat` | `https://api.x.ai/v1` | `bearer` | `https://api.x.ai/v1/models` |
| | `openai-responses` | To verify: `https://api.x.ai/v1` | `bearer` | |
| `groq` | `openai-chat` | `https://api.groq.com/openai/v1` | `bearer` | `https://api.groq.com/openai/v1/models` |
| `openrouter` | `openai-chat` | `https://openrouter.ai/api/v1` | `bearer` | `https://openrouter.ai/api/v1/models` |
| | `anthropic-messages` | `https://openrouter.ai/api` | `bearer` | |
| | `openai-responses` | To verify: `https://openrouter.ai/api/v1` | `bearer` | |
| `zai` | `openai-chat` | `https://api.z.ai/api/paas/v4` | `bearer` | To verify |
| | `anthropic-messages` | `https://api.z.ai/api/anthropic` | To verify: `bearer` | |
| `together` | `openai-chat` | `https://api.together.ai/v1` | `bearer` | `https://api.together.ai/v1/models` |
| `ollama` | `openai-chat` | `http://localhost:11434/v1` | `bearer`, no key | `http://localhost:11434/v1/models` |
| | `anthropic-messages` | `http://localhost:11434` | `bearer`, no key | |
| | `openai-responses` | To verify: `http://localhost:11434/v1` | `bearer`, no key | |
| `vllm` | `openai-chat` | None. Hint: `http://<host>:8000/v1` | `bearer` | `<base_url>/models` of `openai-chat` |
| | `anthropic-messages` | None. Hint: `http://<host>:8000` | To verify | |
| `litellm` | `openai-chat` | None. Hint: `http://<host>:4000/v1` | `bearer` | `<base_url>/models` of `openai-chat` |
| | `anthropic-messages` | None. Hint: `http://<host>:4000` | `x-api-key` | |
| `custom` | Chosen by a person | Set by a person | Set by a person | `models_url`, or none |

- A preset gives defaults. A person may change a base URL or an auth form,
  and may remove a protocol. A person cannot add a protocol that the
  preset lacks; `custom` serves any provider.
- The local-model presets are `ollama`, `vllm`, and `litellm`. They accept
  `http` and the grant `none`. A row with no base URL needs one from
  the person.
- The models URL follows the base URLs. When the table's URL starts with
  the default base URL of one of the preset's protocols, Dagu puts the
  connection's base URL of that protocol in its place. Another URL holds
  only while the connection keeps every default base URL. After a change,
  such a connection has no models URL, so the key never goes to a host
  that a person did not set.
- The contributor of #3730 checked each value not marked "To verify"
  against the provider's documentation on 2026-11-04. The native Gemini
  API is not a protocol of this spec.

## Models List

### Catalog

- Dagu keeps a model catalog for each connection with a models URL, and
  one for each shared key, which its connections read. A catalog holds
  each model's ID, display name, and effort levels, the time of the last
  good read, `truncated`, and the last error with its time. It holds no
  secret.
- The catalog sits beside the tool catalogs of Spec 107, in
  `connection-catalogs/`.
- The catalog serves suggestions, display names, effort levels, the
  editor's warnings, and the suggestion of
  [`model_unknown`](#errors). It never blocks a run or a save.

### Read

- A read is a `GET` of the models URL, with the key in its auth form,
  through the binding rules. It follows no redirect.
- For `anthropic-messages`, the read sends `anthropic-version: 2023-06-01`
  and `limit=1000`. While `has_more` is true, it reads the next page with
  `after_id` set to the field `last_id`, up to 10 pages. A page whose
  `has_more` is true and whose `last_id` is not new is an error.
- For the OpenAI protocols, the read takes the field `data` of one
  response, and each item's `id`. A response with no `data` is an error.
- A response is at most 4 MiB. The catalog keeps the first 5000 IDs. It
  drops an empty ID, an ID over 256 characters, an ID that is not valid
  UTF-8, and a repeated ID. Dagu does not change an ID.
- `truncated` is true when the read stopped at 5000 IDs or 10 pages.
- For `anthropic-messages`, the catalog also keeps `display_name` and the
  effort levels whose field `capabilities.effort.<level>.supported` is
  true, from `low`, `medium`, `high`, `xhigh`, and `max`. A bad detail
  drops that detail and keeps the model.
- A failed read keeps the last good list and records the error: the
  provider's status, or the cause of the failure. The read never reads
  the body of an error response, since a provider can echo the key there.
- `ai.models_timeout_sec` limits one read, all its pages together.

### When Dagu Reads

- A create, a rotation, a change of a base URL or of `models_url`, and a
  person's Reload start a read. The create, the rotation, and the change
  answer without waiting for it. Reload waits for it.
- A reader that finds the catalog older than `ai.models_refresh_hours`
  starts one read in the background and uses the catalog it has. Dagu
  runs no periodic task for catalogs.
- On one host, at most one read of a catalog runs at a time. A failed read
  answers the readers of its process for 60 seconds without a new read.

## Steps

### Keys

`chat.completion`, `agent.run`, and `harness.run` take these keys under
`with`. The DAG-level `llm` block takes `connection` and `model`.

| Key | Rule |
| --- | --- |
| `connection` | The name of a model connection of the run's project. A literal: a reference fails validation, as Spec 107 [Declared Connections](107-connections.md#declared-connections) states |
| `model` | A model ID of the connection, or an alias that the CLI's adapter lists. May be a `${...}` reference |
| `effort` | `low`, `medium`, `high`, `xhigh`, or `max`. `chat.completion` keeps `thinking` of Spec 059 instead |

- A step uses its own `connection` and `model` when it has `connection`.
  Otherwise it uses `llm.connection` and `llm.model` of its DAG, when
  `llm` has `connection`. Otherwise it uses no model connection.
- A project's base config sets `llm` for every DAG of the project, as
  Spec 102 [Base Config](102-projects.md#base-config) states. So
  `llm.connection` in the base config is the project's default model.
- A CLI step takes the DAG's `llm.connection` only when its adapter
  accepts one of the connection's protocols. Otherwise it uses no model
  connection, and the editor says why.
- A step with `connection` and no `model` takes `llm.model` when
  `llm.connection` names the same connection. Otherwise it fails
  validation.
- `chat.completion` with `connection` rejects `provider`, `base_url`, and
  `api_key_name`. The connection's protocol decides the request. For
  `agent.run` and `harness.run`, `provider` names the CLI and stays.
- A model list entry of `chat.completion` takes `connection` and `name`,
  beside the entries with `provider` of Spec 059. An entry with
  `connection` rejects `provider`, `base_url`, and `api_key_name`.
- In `harness.run`, `connection` is a reserved key, never passed as a CLI
  flag. With `connection`, the adapter handles `model` and `effort`, and
  passes neither as a flag of its own name. Without it, both keep their
  Spec 067 behavior. Each `fallback` entry takes the same keys.

### Choosing the Protocol

A step uses the first protocol, in this order, that both the connection
and its consumer accept:

| Consumer | Protocols, in order |
| --- | --- |
| `chat.completion` | `anthropic-messages`, `openai-chat` |
| `claude` | `anthropic-messages` |
| `codex` | `openai-responses` |
| `opencode` | `anthropic-messages`, `openai-chat` |
| `pi` | `anthropic-messages`, `openai-responses`, `openai-chat` |

A step whose connection serves none of its consumer's protocols fails
admission with `protocol_mismatch`.

### Policy

- An untrusted step (Spec 103), which includes every `agent.run` step with
  a CLI provider, never uses a CLI's own sign-in. A CLI with an adapter
  MUST use a model connection there. A CLI with no adapter gets its key
  from the key injection of the egress proxy, as Spec 092 states.
- A trusted step with no model connection uses the CLI's own sign-in, or
  the old keys of `chat.completion`.
- With `ai.require_connections: true`, a trusted step that would use a
  CLI's own sign-in, and a `chat.completion` step on the old keys, fail
  admission with `connection_required`.
- On a server that runs DAGs locally, a trusted agent runs as the Dagu
  user and can read `<data_dir>`, which holds `auth/encryption_key` by
  default. There, a connection does not hide its key from DAG authors or
  from a trusted agent. Creating one says so, as Spec 107 states.

## Adapters

### Contract

For a step with a model connection, an adapter:

1. Points the CLI at the gateway of its step for the chosen protocol: the
   relay address as the base URL, and the relay secret as the credential.
   The relay secret never appears in process arguments. It goes in an
   environment variable or in a file of mode `0600`.
2. Removes every other credential source of the CLI: its provider
   variables, its stored login, and its project config files that can
   change the provider. The CLI runs with a home or config directory
   that holds no login.
3. Passes the model ID, and maps `effort` to the CLI's levels.
4. Writes the step's `mcp_servers` (RFC 026) into the CLI's MCP config,
   when the CLI supports MCP. See Spec 095.
5. Reads the token counts from the CLI's JSON output, when the CLI
   reports them.
6. Names the CLI versions that its conformance tests ran with. Another
   version runs, and the step log says that the version is untested.

### CLIs

The values come from the help and documentation of Claude Code 2.1.332,
codex-cli 0.172.0, opencode 1.18.32, and pi 0.80.3. Each adapter's
conformance test confirms them.

| CLI | Gateway | Removed | Model | Effort |
| --- | --- | --- | --- | --- |
| `claude` | `ANTHROPIC_BASE_URL`, `ANTHROPIC_API_KEY` | Every other `ANTHROPIC_` variable, every `CLAUDE_CODE_USE_` variable, `CLAUDE_CODE_OAUTH_TOKEN` | `--model` | `--effort`: `low` to `max` |
| `codex` | `-c model_provider=dagu`, and `model_providers.dagu` with the relay `base_url`, `wire_api = "responses"`, and `env_key` | `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `CODEX_API_KEY`. A new `CODEX_HOME` | `-m` | `model_reasoning_effort`: `low` to `xhigh` |
| `opencode` | A `dagu` provider in `OPENCODE_CONFIG_CONTENT`, with the relay `baseURL` and the secret as `{env:<variable>}` | `OPENCODE_AUTH_CONTENT`, `OPENCODE_API_KEY`, the project config. A new data directory | `-m dagu/<model>` | `--variant` |
| `pi` | A `dagu` provider in a generated `models.json`, with the relay `baseUrl`, its `api`, and the secret as `$<variable>` | Project-local files, with `--no-approve`. A new `PI_CODING_AGENT_DIR` | `--provider dagu --model <model>` | `--thinking`: `low` to `xhigh` |

- The effort select offers the levels that both the CLI and the model
  accept. A level that the CLI does not accept fails validation with
  `effort_unsupported`. A level that the model's catalog lacks gets an
  editor warning, since validation reads no catalog.
- An adapter lists the aliases of its CLI, such as `sonnet` and `opus` for
  `claude`. The editor offers them, and they pass the editor's model
  warning. Dagu passes an alias to the CLI unchanged.
- Another harness provider, such as `copilot`, `cursor`, `gemini`, or
  `aider`, has no adapter. A step that gives it `connection` fails
  validation with `adapter_missing`.

## Model Traffic

- The gateway of Spec 094 adds the key to each request of a step. The step
  holds only the relay secret of its attempt.
- The gateway removes the client headers `x-api-key`, `authorization`, and
  `proxy-authorization`, and sets the header of the protocol's `auth`.
- It sends a request only to a bound host, and only for the step's
  connection.
- It refuses a request whose model is not in a non-empty
  `allowed_models`, with `model_not_allowed`. A CLI that sends some
  requests with a second model, such as a small model for titles, needs
  that model in the list too. The error names the model.
- It maps the provider's answer to an unknown model to `model_unknown`,
  and a 401 to `key_rejected`.
- For a step on an agent worker, where the gateway runs is an open item.
  See [Status](#status).

## Runs

- The status of a step records its connection, model, effort, and CLI
  version, and its input and output token counts when the CLI or the
  provider reports them. That adds a few fields to each step, and no
  file.
- The run page shows them beside the step. A step that failed with
  `key_rejected` or `reconnect_required` shows the connection's fix: Rotate
  for a key, Reconnect for a sign-in.

## Configuration

| Key | Type | Default | Range |
| --- | --- | --- | --- |
| `ai.models_refresh_hours` | integer | 24 | 0 to 720. 0 reads a catalog only on a create, a change, or a Reload |
| `ai.models_timeout_sec` | integer | 30 | 1 to 300 |
| `ai.require_connections` | boolean | false | |

- A wrong type or a value out of range stops the process at start. The
  error names the key, the expected type or range, and the value.
- An unknown key of the block `ai` is logged as a warning and ignored, so
  a newer configuration file does not stop an older binary.
- No key of the block holds a secret. Spec 094 adds the keys of the
  gateway under `ai.relay`.

## User Interface

### Connections Page

- Add offers the kind Model API beside the providers of Spec 107. The
  preset fills the protocols, base URLs, and auth forms. The choices are
  Own key, Sign in, and No key for a local-model preset. An admin also
  sees Shared key, with Share.
- The key field is a password field. Dagu clears it after each request,
  and keeps its value in no state, URL, storage, or error text.
- A row shows the key's prefix and fingerprint, the shared key's name, or
  the signed-in account, never the key. It also shows the status, the
  catalog's count, date, and last error, the DAGs that name the
  connection, and Works with: `chat.completion` and each CLI whose
  adapter accepts one of the connection's protocols.
- Rotate key, Reload, Test, and Remove are actions of a row. Test reads
  the models. Remove shows where the connection is used, as Spec 107
  states.

### Step Forms

The Model section of `chat.completion`, `agent.run`, and `harness.run`:

```text
Agent CLI   [codex           v]   agent.run and harness.run only
Connection  [openai-team     v]   connections the step can use
Model       [<display name>  v]   free text and ${...} accepted
Effort      [high            v]   levels both the CLI and the model accept
```

- The Connection select reads `GET /model-choices`. A connection that the
  step cannot use is listed disabled, with the reason. The first items
  are "Project default (`<name>`)" when the base config sets
  `llm.connection`, and, on a trusted CLI step without
  `ai.require_connections`, "The CLI's own sign-in".
- The Model field suggests the catalog's models by display name, and the
  adapter's aliases. A pick writes the model ID.
- A pick of a connection on a `chat.completion` step on the old keys
  removes `provider`, `base_url`, and `api_key_name`, and keeps `model`
  and `thinking`. The form never removes a model by itself.
- The editor shows each error of [Errors](#errors) that validation or
  admission finds. It also warns, and blocks neither a save nor a run, on
  a literal model that the catalog lacks, with the closest catalog ID; on
  an effort level that the model lacks; on a model that `allowed_models`
  lacks; and on a connection that is `needs_reconnect`. A reference, an
  adapter's alias, and a catalog with `truncated` true get no model
  warning.

## REST API

This spec adds to the routes of Spec 107:

| Method and path | Purpose |
| --- | --- |
| `GET /connection-providers` | Also lists the provider `model`, its presets, and their protocols |
| `POST /connections`, `PATCH /connections/{name}` | Create or change a model connection. A `key` field sets or rotates the key. A blank `key` keeps it |
| `POST /oauth-apps`, `PATCH /oauth-apps/{id}` | Create or change a shared key, with the provider `model` |
| `POST /oauth-apps/{id}/shares` | Share a shared key with projects, or with all projects |
| `GET /connections/{name}/models` | The catalog. It calls no provider |
| `POST /connections/{name}/models/reload` | Read the models now, and answer with the catalog |
| `GET /model-choices?dag=<name>&provider=<cli>` | The connections that a step of the DAG can use, with models, display names, effort levels, aliases, and the reason a connection is disabled |

- No response holds a key. A response holds `keyPrefix`,
  `keyFingerprint`, and `keyUpdatedAt`.
- A request body has a closed schema. An unknown field gives the status
  400. A body over 1 MiB gives the status 413.
- A failed read is not an error of the reload route. The catalog in the
  answer holds the error.
- `GET /model-choices` gives no key data and no base URL.

## Permissions

| Action | Role |
| --- | --- |
| Create, change, rotate, share, or delete a shared key | `admin` |
| Create, change, rotate, or remove a model connection, or set its `allowed_models` | `manager` or above in its project |
| Reload or test a model connection | `developer` or above in its project |
| Read the model choices of a DAG | Whoever may edit the DAG |

## Audit

The audit log records the changes of Spec 107 for model connections and
shared keys, and also:

- the fingerprint at a create and at a rotation
- the old and the new URL at a change of a base URL or of `models_url`,
  since the key goes to the new host from then on
- each Reload, and whether its read was good

No entry holds a key or a prefix.

## Errors

`dagu validate` rejects:

- a `connection` that is not a literal
- `connection` with `provider`, `base_url`, or `api_key_name` on a
  `chat.completion` step or model entry
- `connection` with no model, when `llm` gives none for that connection
- `connection` on a CLI with no adapter: `adapter_missing`
- an effort level that the CLI does not accept: `effort_unsupported`

Admission, at the start of a run and in the editor, fails with:

- a connection that the run's project lacks, as Spec 107 states
- `protocol_mismatch`: the connection serves none of the consumer's
  protocols
- `connection_required`: the policy needs a connection and the step has
  none
- a `secrets` entry that names a model connection

A step fails at run time with:

- `model_not_allowed`: a request's model is not in `allowed_models`
- `model_unknown`: the provider does not know the model
- `key_rejected`: the provider answered 401 to the stored key
- `reconnect_required`: a sign-in connection needs a reconnect

```text
codex cannot use connection "anthropic-main": it serves anthropic-messages, and codex needs openai-responses
connection "anthropic-main": the provider rejected the key (401); a manager can rotate it on the Connections page
model "claude-sonet-4-6" is not a model of connection "anthropic-main"; did you mean "claude-sonnet-4-6"? (models read at 2026-11-05T09:14:02Z)
```

## Examples

### A Model Connection

A create request. The key is only in the request.

```json
{
  "name": "anthropic-main",
  "provider": "model",
  "preset": "anthropic",
  "grant": "api_key",
  "key": "<the API key>"
}
```

A read of it:

```json
{
  "name": "anthropic-main",
  "provider": "model",
  "preset": "anthropic",
  "protocols": {
    "anthropic-messages": { "baseUrl": "https://api.anthropic.com", "auth": "x-api-key" }
  },
  "grant": "api_key",
  "keyPrefix": "sk-ant",
  "keyFingerprint": "0123456789ab",
  "keyUpdatedAt": "2026-11-04T10:00:00Z",
  "status": "connected",
  "models": { "count": 14, "truncated": false, "refreshedAt": "2026-11-04T10:00:05Z" }
}
```

### Steps

The project's base config sets the default model:

```yaml
llm:
  connection: anthropic-main
  model: claude-sonnet-4-6
```

A chat step and a Claude Code step use it. A Codex step names its own
connection:

```yaml
steps:
  - id: summarize
    action: chat.completion
    with:
      prompt: Summarize the release notes.

  - id: fix
    action: agent.run
    with:
      provider: claude
      effort: high
      prompt: Fix the failing test.

  - id: review
    action: harness.run
    with:
      provider: codex
      connection: openai-team
      model: ${REVIEW_MODEL}
      prompt: Review the change.
```

A chat step falls back to a second connection:

```yaml
steps:
  - action: chat.completion
    with:
      model:
        - connection: anthropic-main
          name: claude-sonnet-4-6
        - connection: openrouter
          name: anthropic/claude-sonnet-4.6
      prompt: Classify the ticket.
```

## Conformance

No test exists yet. Each change that implements a part of this spec adds
its tests here and updates [Status](#status).
