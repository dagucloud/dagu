# Spec 093: Model Connections

## Status

Not implemented.

This spec describes planned behavior. No version of Dagu accepts a
connection of the provider `model`, or a model connection in the step key
`connection`, yet. One rule is already
enforced: Dagu reserves the secret ref prefix `ai/`, as Spec 069 and
Spec 103 state. This spec stores no key in the secret store, and the
prefix stays reserved.

**Open items.** These points are not decided:

- Whether the gateway of a step on an agent worker moves out of the
  coordinator, where Specs 092 and [094](094-ai-gateway.md) place it,
  to sit next to the worker, such as in the egress proxy of
  [Spec 105](105-egress-proxy.md). See [Model Traffic](#model-traffic).
- Subscriptions, such as a Claude or ChatGPT plan. This draft stores no
  subscription credential: Spec 107 defines no OAuth app for the
  provider `model`, and Anthropic's terms restrict storing or relaying a
  subscription token in another product. A trusted step uses the CLI's
  own sign-in for a subscription. An untrusted step has no path for one
  yet.
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
| `models_url` | `custom` only. A URL and the one protocol of the connection whose auth and response form a read uses. See [Models List](#models-list) |
| `allowed_models` | Optional. Model IDs as the provider receives them, matched exactly. Empty means every model |

- `base_url` is an `https` URL with no user information, query, or
  fragment. A local-model preset and `custom` also accept `http`, and the
  UI warns that the key crosses the network unencrypted.
- `auth` is `x-api-key` or `bearer`, forms of
  [Key injection](105-egress-proxy.md#key-injection). A model connection
  has no `anthropic-oauth`, since it holds no subscription token.
- `grant` is `api_key` or `none`. `app` names a shared key, and is empty
  otherwise. See [Credentials](#credentials). Spec 107 lists the provider
  `model` and these grants.
- `services`, `scopes`, `subject`, `bound_hosts`, and `resource` do not
  apply.
- `allow_token_export` is always off. No step, `secrets` entry, or route
  reads the key of a model connection. Only the steps of [Keys](#keys)
  use one. Any other consumer that names one, such as `http.request` or a
  `secrets` entry with `provider: connection`, fails admission.

### Credentials

| Credential | `grant` and `app` | Where the key is | Who adds it |
| --- | --- | --- | --- |
| Own key | `api_key`, no `app` | In the connection's token state, encrypted | `manager` or above in the project |
| Shared key | `api_key`, `app` names a [shared key](#shared-keys) | In the app | An admin, through Share |
| No key | `none`, no `app` | Nowhere. Valid for a local-model preset only | `manager` or above in the project |

- A key is write-only. After a save, no route and no page shows it, or
  any value derived from it. Pages show only that a key is set, and when.
- A key is not empty, is valid UTF-8 with no control character, and has
  at most 8192 bytes. A save does not call the provider.
- A key is a provider's API key. A subscription token, such as the token
  of `claude setup-token`, is not a key of this spec. See
  [Status](#status).
- A rotation replaces the key in one place: the connection for an own key,
  the app for a shared key. Every connection on a shared key sends the new key
  from its next request. A key, its URLs, and its auth change together:
  each request uses the URLs and auth saved with the key it sends.
- The key is stored as Spec 107 [Records](107-connections.md#records)
  state: encrypted with the server's key, and never in the secret store.

### Connection Status

This spec adds these transitions to Spec 107, for the grants `api_key` and
`none`:

| From | To | When |
| --- | --- | --- |
| none | `connected` | The connection is created |
| `connected` | `needs_reconnect` | The provider answers 401 to a model request of a step that carried the stored key. A catalog read's 401 is only a catalog error |
| `needs_reconnect` | `connected` | The key is rotated |

- A 401 causes no refresh and no retry, since a key has no refresh. It
  changes the status only when the rejected key is still the stored key,
  so a late 401 for a rotated key changes nothing.
- A 403 changes no status. It can deny one model and allow others.
- For a connection on a shared key, the 401 sets the app's status to
  `invalid` in place of the connection's, and a rotation of the app sets
  it back to `ok`. Every connection on the app shows the status.
- The change records the event `connection.needs_attention` of Spec 107,
  once, however many runs see the 401. The UI calls this status "Key
  rejected". The event points to Rotate on the Connections page, not to
  a reconnect.

### Shared Keys

- A shared key is an app with the provider `model`. It holds `preset`,
  `protocols`, `models_url`, and the key, encrypted as Spec 107 keeps a
  service account key. It has no `kind`, `client_id`, `client_type`, or
  `endpoints`. Apps are server-wide, as Spec 107 states.
- Only an admin creates, changes, rotates, shares, or deletes a shared
  key. A rotation works while connections use the key. Only a delete
  needs the key unused.
- Only Share attaches a shared key to a project. A connection request
  that names a `model` app is refused.
- Share picks projects, or all projects. Dagu creates, in each picked
  project, a connection of the app's name with the grant `api_key` and
  that app. With all projects, a project created later gets one too.
  Share refuses a project that already has a connection of that name, and
  the error names the project.
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
- A loopback base URL names the Dagu host. A run on a worker fails
  admission with such a connection, since its gateway is not there.
- On a connection or a shared key that holds a key, a change that adds
  or changes a base URL or a models URL needs the key again in the same
  request, so no one can send a stored key to a URL of their choosing.
- Each base URL and models URL is a credential past its origin, since a
  path or query can hold a secret. Dagu stores it encrypted, as the key.
  Reads, errors, and audit entries show only the origin.
- Dagu removes the key, and each URL past its origin or any part of it
  that can hold a secret, from everything a provider returns before Dagu
  stores it, shows it, or passes it to a
  step: responses, errors, and catalogs. A model ID that holds one is
  dropped.

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
- The models URL belongs to the protocol of its row, and a read uses that
  protocol's base URL and auth. When the URL starts with that protocol's
  default base URL, Dagu puts the connection's base URL in its place.
  Another URL holds only while that base URL keeps its default. A
  connection that changed it that way, or removed the protocol, has no
  models URL, so the key never goes to a host that a person did not set.
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
| `connection` | The name of a model connection of the run's project. A literal: a reference fails validation. `with.connection` and each `fallback` entry's `connection` on a provider with an adapter, the `connection` of each chat model entry, and `llm.connection` when the DAG has a step that can use it, are sources of Spec 107 [Declared Connections](107-connections.md#declared-connections) |
| `model` | A model ID of the connection, or an alias that the CLI's adapter lists. May be a `${...}` reference |
| `effort` | `low`, `medium`, `high`, `xhigh`, or `max`. `chat.completion` keeps `thinking` of Spec 059 instead |

A `chat.completion` step takes the first of these that applies:

1. A `model` list: each entry names its own `connection` or `provider`.
2. `connection`: that connection.
3. `provider`: the old keys of Spec 059, with no connection.
4. `llm.connection` of the DAG.
5. Spec 059 as it stands.

A CLI step takes the first of these that applies:

1. `connection`: that connection.
2. When [Policy](#policy) requires a connection: `llm.connection` of the
   DAG, when its adapter accepts one of the connection's protocols.
3. The CLI's own sign-in. `model` and `effort` keep Spec 067 behavior.

- A step that takes a connection and sets no `model` takes `llm.model`
  when it is one model and `llm.connection` names the same connection.
  Otherwise it fails
  validation.
- A project's base config sets `llm` for every DAG of the project, as
  Spec 102 [Base Config](102-projects.md#base-config) states. So
  `llm.connection` in the base config is the project's default model.
- `chat.completion` with `connection` rejects `provider`, `base_url`, and
  `api_key_name`. The connection's protocol decides the request. For
  `agent.run` and `harness.run`, `provider` names the CLI and stays.
- A model list entry of `chat.completion` takes `connection` and `name`,
  beside the entries with `provider` of Spec 059. An entry with
  `connection` rejects `provider`, `base_url`, and `api_key_name`, and
  always uses its connection's URLs and auth. A step-level `base_url` or
  `api_key_name` applies only to the entries with `provider`.
- In `harness.run`, `connection` is a reserved key for a provider with an
  adapter, never passed as a CLI flag. A custom harness keeps it as a
  flag, as Spec 067 states. With `connection`, the adapter handles
  `model` and `effort`, and passes neither as a flag of its own name.
  Without it, both keep their Spec 067 behavior. Each `fallback` entry
  takes the same keys. A step whose `provider` is a reference cannot
  have `connection`, since its meaning depends on the provider. When it
  would inherit `llm.connection`, the policy and protocol checks run
  after the reference resolves, before the CLI starts.
- A step that uses a model connection, by any rule above, cannot have a
  CLI option that changes the CLI's provider, base URL, credential, or
  server, such as `attach` or `managed: true` for `opencode`. Such a step
  fails with `option_conflict`. Each adapter lists such options.

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
  admission with `connection_required`. The rule applies to each model
  entry of a chat step and to each `fallback` attempt of `harness.run`.
  It also fails an attempt of a custom harness, which no adapter can
  point at the gateway.
- A step that runs as the Dagu user can read `<data_dir>`, which holds
  `auth/encryption_key` by default. That is every trusted step, and an
  untrusted step under `untrusted.isolation: off`, which Spec 092 needs
  when the coordinator is off. For such a step, a connection does not
  hide its key from the agent or from DAG authors. Creating a connection
  on such a server says so, as Spec 107 states.

## Adapters

### Contract

For a step with a model connection, an adapter:

1. Points the CLI at the gateway of its step for the chosen protocol: the
   relay address as the base URL, and the relay secret as the credential.
   The relay secret never appears in process arguments. It goes in an
   environment variable or in a file of mode `0600`, and step logs mask
   it, as RFC 026 masks the gateway token.
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
- A harness step that runs in a container, by its own `container` or
  the DAG's (Spec 067), cannot use a model connection yet, as RFC 026
  refuses `mcp_servers` there. Such a step fails admission with
  `option_conflict`.
- The `opencode` adapter covers the CLI only. An `opencode` step that
  uses a model connection runs the CLI, never the managed server.
- Another built-in harness provider, such as `copilot`, `cursor`, `gemini`, or
  `aider`, has no adapter. A step that gives it `connection` fails
  validation with `adapter_missing`.

## Model Traffic

- The gateway of Spec 094 adds the key to each request of a step. The step
  holds only the relay secret of its attempt.
- It forwards only the API operations of the step's protocol. No other
  method or path carries the key, so no upstream can echo it back.
- The gateway removes the client headers `x-api-key`, `authorization`, and
  `proxy-authorization`, and sets the header of the protocol's `auth`.
- It sends a request only to a bound host, and only for the step's
  connection.
- It refuses a request whose model is not in a non-empty
  `allowed_models`, with `model_not_allowed`. The list holds the IDs that
  reach the provider, so a CLI alias such as `sonnet` passes only when
  the ID it resolves to is listed. A CLI that sends some requests with a
  second model, such as a small model for titles, needs that ID too. The
  error names the model. With a list set, the gateway also refuses a
  request that can select a model it cannot check, such as through the
  `models` or `fallbacks` fields of OpenRouter.
- It maps the provider's answer to an unknown model to `model_unknown`,
  and a 401 to `key_rejected`.
- For a step on an agent worker, it runs in the coordinator, as Specs 092
  and 094 state. See [Status](#status).
- Every worker that serves the relay announces `ai_relay.v1` of Spec 092,
  general workers too. A run with a model connection goes only to such a
  worker.

## Runs

- The status of a step records its connection, model, effort, and CLI
  version, and its input and output token counts when the CLI or the
  provider reports them. That adds a few fields to each step, and no
  file.
- The run page shows them beside the step. A step that failed with
  `key_rejected` shows Rotate for its connection.

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
  Own key, and No key for a local-model preset. An admin also sees Shared
  key, with Share.
- The key field is a password field. Dagu clears it after each request,
  and keeps its value in no state, URL, storage, or error text.
- A row shows when the key was set, or the shared key's name, never the
  key. It also shows the status, the
  catalog's count, date, and last error, the DAGs that name the
  connection, and Works with: `chat.completion` and each CLI whose
  adapter accepts one of the connection's protocols.
- Rotate key, Reload, Test, and Remove are actions of a row. Test reads
  the models, so a connection with no models URL has neither Reload nor
  Test. Remove shows where the connection is used, as Spec 107 states.

### Step Forms

The Model section of `chat.completion`, `agent.run`, and `harness.run`:

```text
Agent CLI   [codex           v]   agent.run and harness.run only
Connection  [openai-team     v]   connections the step can use
Model       [<display name>  v]   free text and ${...} accepted
Effort      [high            v]   levels both the CLI and the model accept
```

- The Connection select reads `GET /model-choices`. A connection that the
  step cannot use is listed disabled, with the reason. Its first item
  names what the step takes with no `connection`, by
  [Keys](#keys): "Project default (`<name>`)" or "The CLI's own
  sign-in".
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
| `POST /connections`, `PATCH /connections/{name}` | Create or change a model connection. A `key` field sets or rotates the key. A blank `key` keeps it. A request that names a `model` app is refused |
| `POST /oauth-apps`, `PATCH /oauth-apps/{id}` | Create or change a shared key, with the provider `model`. `PATCH` rotates its key while connections use it |
| `POST /oauth-apps/{id}/shares` | Share a shared key with projects, or with all projects |
| `GET /connections/{name}/models` | The catalog. It calls no provider |
| `POST /connections/{name}/models/reload` | Read the models now, and answer with the catalog |
| `GET /model-choices?project=<name>&dag=<name>&provider=<cli>` | The connections that a step of the DAG can use, with models, display names, effort levels, aliases, and the reason a connection is disabled. It resolves and authorizes the DAG in `project`, as Spec 102 states |

- No response holds a key or a value derived from it. A response holds
  `keySet` and `keyUpdatedAt`.
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

- the old and the new origin at a change of a base URL or of
  `models_url`, since the key goes there from then on
- each Reload, and whether its read was good

No entry holds a key or a value derived from it.

## Errors

`dagu validate` rejects:

- a `connection` that is not a literal
- `connection` with `provider`, `base_url`, or `api_key_name` on a
  `chat.completion` step or model entry
- `connection` with no model, when `llm` gives none for that connection
- `connection` on a built-in CLI with no adapter: `adapter_missing`. A
  custom harness keeps `connection` as a flag, and only
  [Policy](#policy) applies to it
- an effort level that the CLI does not accept: `effort_unsupported`

Admission, at the start of a run and in the editor, fails with:

- `option_conflict`: a step that uses a model connection has a CLI option
  that changes the CLI's provider, base URL, credential, or server

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
  "keySet": true,
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
