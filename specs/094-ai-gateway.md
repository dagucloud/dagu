# Spec 094: AI Gateway

## Status

Implemented.

This spec is the companion of [Spec 059: Chat Completion](059-chat-completion.md), [Spec 090: Agent
Tools](090-agent-tools.md), [Spec 091: Agent Node](091-agent-node.md), [Spec 092: Agent
Workers](092-agent-workers.md), [Spec 093: AI Endpoints](093-ai-endpoints.md), [Spec 095: MCP
Servers](095-mcp-servers.md) and [Spec 096: Skills](096-skills.md). It also uses [Spec 067: Harness
Executor (CLI and ACP)](067-harness.md).

Spec 093 defines the endpoints that the gateway uses. Spec 095 defines the MCP servers that the same
relay serves.

## Scope

This spec defines:

- the threat model of the gateway
- the gateway: the relay in the worker and the stream to the coordinator
- the start checks, the version check and the local mode of the gateway
- the block `endpoint` of `agent.run` and of `chat.completion`, and the key `endpoint` of a model entry
- the use of an endpoint by `agent.run` and by `harness.run`

This spec does not define:

- the endpoint, its key, its scope, the provider presets, the models list and the block `ai`. See
  [Spec 093](093-ai-endpoints.md).
- the MCP server entries with credentials, the MCP relay and the key `allow` of a named entry. See
  [Spec 095](095-mcp-servers.md).
- the keys of `agent.run`, the typed result and the failure kinds of an attempt. See Spec 091.
- the key `dags`, the tool map, the skills and the key `allow`. See Spec 090.
- the agent worker, the pools, the restricted user and the egress proxy. See Spec 092.
- the secret providers and the runtime profiles. See Spec 069 and Spec 070.
- the keys of the `harness.run` action. See [Spec 067](067-harness.md).

## Goal

A workflow author names a model API in one key of a step. The author never writes the API key in a
DAG, in a file of the worker or in the environment of an agent. [Spec 093](093-ai-endpoints.md)
defines the endpoint that holds the key.

The key stays in the coordinator. A CLI agent in an untrusted step calls the model API through a gateway
that adds the key. A `chat.completion` step on a worker does the same.

## Terms

An endpoint, a protocol, a preset and a scope are as in Spec 093. A MCP server entry is as in Spec
095.

The gateway is the relay of the worker and the coordinator code that serves it.

The relay is a small HTTP server in the worker process. It listens on `127.0.0.1` and exists for one
attempt of one step.

An upstream is the HTTP server of the provider.

The relay secret is a random value that the relay needs in each request. The client sends it in the
place of the API key.

A target is an endpoint or a MCP server entry that a relay request names.

The restricted user, an untrusted step, an agent worker and the coordinator are as in Spec 092. A
local run is a run that one process does without a coordinator.

## Threat model

### Assets

- the API key of a provider and the credentials of a MCP server
- the money and the quota of the owner of the key
- the data in the requests and in the responses
- the client certificate of the worker

### Trust

| Party | Trust |
| --- | --- |
| An administrator of Dagu | Trusted. Only an administrator adds, rotates and deletes a key. |
| The coordinator and the server | Trusted. They hold the key and use it. |
| The Dagu worker process | Trusted for the runs that it holds. It never holds a key. |
| The CLI agent, its processes and an untrusted step | Not trusted. See Spec 092. |
| A worker with a stolen certificate | Trusted as much as its pool allows. See Spec 092. |
| The provider | Trusted with the key and with the data of each request. |

### Threats and controls

| Threat | Control | What remains |
| --- | --- | --- |
| The agent reads the API key. | The key stays in the coordinator. The agent has the relay secret only. | None. |
| The agent sends the key or data to another host. | The coordinator calls the host of the base URL only. The relay refuses each other host and path. | Data goes to the provider. |
| The agent uses the relay of another step or run. | Each relay has its own secret and its own file. The coordinator checks the run, the step and the target. | None. |
| The agent spends the quota of the key. | A limit of streams, of bytes and of time applies to each run. | The agent can call each allowed path while the run is alive, at the cost of the owner. |
| A process of the same host calls the relay. | The relay needs the secret in each request. It compares in constant time. | A process that reads the secret. See Spec 091 for the file rules. |
| A compromised worker asks for a key. | No message of the gateway carries a key. | The worker can use the targets that the runs on it may use. It cannot read the key. |
| A worker uses a target that its run does not name. | The coordinator takes the target of the step from the DAG of the attempt. | None. |
| A worker with an old lease uses a target. | The coordinator checks the lease for each stream. It cancels the streams when the lease ends. | None. |
| The upstream redirects to another host. | The coordinator does not follow a redirect. It reports `upstream_error`. | None. |
| The agent changes the protocol with a header or a path. | The coordinator builds the upstream request from its own list of paths, methods and headers. | None. |
| The coordinator is compromised. | Out of scope. It holds the keys. | The keys. |

An untrusted agent reaches its own relay and no other part of the gateway. A compromised worker uses
the targets that the runs on it may use. It never gets the key.

Spec 093 has the threat of the endpoint pages. Spec 095 has the threats of the MCP servers with
credentials.

## The gateway

### Path of a request

```text
client --HTTP--> relay (worker, 127.0.0.1) --gRPC stream, mTLS--> coordinator --HTTPS--> provider
```

1. The client sends an HTTP request to the relay. It sends the relay secret where it would send the
   API key.
2. The relay checks the secret, the path and the method. It opens a gRPC stream to the coordinator.
3. The coordinator checks the identity and the target. It adds the real key and calls the upstream.
4. The coordinator streams the response back. The relay writes it to the client.

The gateway adds no port and no component. The stream uses the gRPC channel that the worker has to
the coordinator. A deployment that runs agent workers already allows it.

### The relay

The relay is part of the worker process. A step that names a target starts one relay for each
attempt, as the tool bridge does (Spec 091). The relay:

- listens on `127.0.0.1` and a random free port
- needs the relay secret: 32 random bytes from the system random generator, in the header that the
  client uses for its key
- compares the secret in constant time, and answers 401 for a wrong secret
- is bound to the run, the step and the target of the attempt
- ends when the attempt ends. A request that is open then gets an error and the stream ends.

The relay accepts the secret in the header `x-api-key`, or in the header `authorization` with the scheme
`Bearer`. A request with the secret in two headers is refused.

Dagu writes the secret to no file of its own. The CLI gets the secret in its variables (see [Client
settings](#client-settings)), and the MCP config of the attempt holds it for a named MCP entry. Dagu
masks the secret in the logs and the records, as for the bridge.

The relay serves the origin form only. It refuses a request in absolute form, a `CONNECT` request and
a `Host` header other than `127.0.0.1:<port>` or `localhost:<port>`. This blocks a client that tries to
reach another host through the relay. It also blocks an attack that changes the address of a host name.

The relay does not cache and does not read the body. It copies it.

### Client settings

Dagu sets these variables in the process of the CLI. They replace the same variables of
`env_passthrough` for this step.

| Variable | Value |
| --- | --- |
| `ANTHROPIC_BASE_URL` | `http://127.0.0.1:<port>`, for a protocol `anthropic`. |
| `ANTHROPIC_API_KEY` | The relay secret. It is the placeholder key of the client. |
| `NO_PROXY`, `no_proxy` | The list of the deployment, with `127.0.0.1` and `localhost` added. |

The CLI of this step gets no other variable of the environment that starts with `ANTHROPIC_` or
`CLAUDE_CODE_USE_`, and no `CLAUDE_CODE_OAUTH_TOKEN`. Such a variable, such as `ANTHROPIC_AUTH_TOKEN`, can hold a credential or change the
credential that the CLI sends. It can also send the CLI to another cloud around the relay.
A model route of the egress proxy in the environment of the deployment does not apply: the value of
`ANTHROPIC_BASE_URL` is the relay.

A provider client of `chat.completion` gets the base URL `http://127.0.0.1:<port>/v1` for the
protocol `openai`, and `http://127.0.0.1:<port>` for the protocol `anthropic`. It gets the relay
secret as its key.

### Allowed requests

The relay and the coordinator refuse each request that is not in this list. The path is the path
that the client sends to the relay.

| Protocol | Method and path | Query |
| --- | --- | --- |
| `anthropic` | `POST /v1/messages` | `beta` only |
| `anthropic` | `POST /v1/messages/count_tokens` | `beta` only |
| `anthropic` | `GET /v1/models`, `GET /v1/models/{id}` | `limit`, `after_id`, `before_id` |
| `openai` | `POST /v1/chat/completions` | None |
| `openai` | `POST /v1/responses` | None |
| `openai` | `GET /v1/models`, `GET /v1/models/{id}` | None |

An `{id}` has at most 200 characters from `A-Za-z0-9._:/-` and no `..`. The query has at most 2048
bytes.

For the protocol `anthropic`, the coordinator sends the path to the base URL of the endpoint as it is.
For the protocol `openai`, it removes the first segment `/v1` and adds the rest to the base URL. For
example, the path `/v1/chat/completions` and the base URL `https://api.groq.com/openai/v1` give
`https://api.groq.com/openai/v1/chat/completions`.

The coordinator sends these request headers, and no other: `content-type`, `accept`, `user-agent`,
`anthropic-version`, `anthropic-beta` and `openai-beta`. It sets `accept-encoding: identity`. A
request with `content-encoding` is refused with the reason `host_not_allowed`, because the form of the
request is not allowed.

The coordinator returns the status and the response headers of the upstream, except the hop-by-hop
headers, `set-cookie` and the headers that start with `proxy-`. So `retry-after` and the rate limit
headers reach the client.

A status of the upstream is never changed. A 401 of the upstream gives the failure kind
`auth_required` of Spec 091, and a 429 gives `rate_limited`. The administrator then checks or
rotates the key.

The coordinator does not follow a redirect. A response with a status 3xx is an `upstream_error`.

### The RPC

The service `CoordinatorService` has two RPCs for the gateway. This sketch shows the shape. The file
`proto/coordinator/v1/coordinator.proto` holds the field numbers.

```proto
// CheckAIEndpoint checks, with no value, that a step may use a target.
rpc CheckAIEndpoint (CheckAIEndpointRequest) returns (CheckAIEndpointResponse);

// RelayAIRequest carries one HTTP request and its response.
rpc RelayAIRequest (stream RelayClientMessage) returns (stream RelayServerMessage);

message RelayTarget {
  string kind = 1; // "endpoint" or "mcp"
  string name = 2;
}

message CheckAIEndpointRequest {
  string worker_id = 1;
  string attempt_key = 2;
  string attempt_id = 3;
  string dag_name = 4;
  string step_name = 5;
  RelayTarget target = 6;
  string provider = 7; // The CLI provider or the chat type that will use it
  string model = 8;    // The model of the step after resolution. Empty: no model check
}

message CheckAIEndpointResponse {
  string protocol = 1;
  string chat_type = 2; // The provider type of chat.completion
}

message RelayClientMessage {
  oneof kind {
    RelayOpen open = 1;     // The first message
    RelayBody body = 2;     // Chunks of the request body
    RelayCancel cancel = 3; // The client went away
  }
}

message RelayOpen {
  string worker_id = 1;
  string attempt_key = 2;
  string attempt_id = 3;
  string dag_name = 4;
  string step_name = 5;
  RelayTarget target = 6;
  string method = 7;
  string path = 8;
  string query = 9;
  repeated RelayHeader headers = 10;
  int64 content_length = 11; // -1 when unknown
}

message RelayHeader {
  string name = 1;
  string value = 2;
}

message RelayBody {
  bytes data = 1; // At most 65536 bytes
  bool end = 2;
}

message RelayCancel {}

message RelayServerMessage {
  oneof kind {
    RelayAccepted accepted = 1; // The checks passed. It holds the limits.
    RelayResponseHead head = 2;
    RelayBody body = 3;
    RelayError error = 4;       // The coordinator refused or the call failed
    RelayEnd end = 5;           // The last message of a good response
  }
}

message RelayAccepted {
  int64 max_request_bytes = 1;
  int64 max_response_bytes = 2;
  int32 idle_timeout_sec = 3;
}

message RelayResponseHead {
  int32 status = 1;
  repeated RelayHeader headers = 2;
}

message RelayError {
  string reason = 1; // A name of the table of failure kinds
  string message = 2; // No key, no header value
}

message RelayEnd {
  int64 response_bytes = 1;
}
```

The worker announces the capability `ai_relay.v1` in `PollRequest` and `HeartbeatRequest`
(`repeated string capabilities`, with the numbers 4 and 5). The task has `repeated string
required_capabilities`, with the number 42. See [Version check](#version-check). A worker or a
coordinator of a release before these fields ignores them.

The coordinator reports a refusal of the identity checks 1, 2 and 4 as an error of gRPC with the code
`PermissionDenied`. It reports a refusal of the checks 3 and 5 to 9 as a `RelayError` of the stream. For
`CheckAIEndpoint`, which has no stream, it reports each refusal as an error of gRPC. The message of a
refusal of the checks 3 and 5 to 9 starts with the reason and a colon, as in `endpoint_forbidden: the
DAG is not in the allow list`. The worker reads the reason from this text.

### Identity checks

The coordinator checks these items in `RelayOpen` and in `CheckAIEndpoint`, in this order. The first
failure ends the call.

1. The connection has a verified client certificate. The coordinator refuses a gateway call on a
   connection with no mutual TLS.
2. The lease of the attempt exists, and the worker ID and the attempt ID match it. With
   `coordinator.worker_pools`, the pool of the certificate is the pool of the lease. The lease is
   fresh. These rules are the rules of a registry secret in Spec 092.
3. The step is a step of the DAG of the attempt. A refusal gives the reason `endpoint_forbidden`.
4. The attempt has no final status, and the step has no final status in the reports of the worker.
5. The step names the target. The coordinator reads the name from the DAG that it holds. The name is
   the key `with.endpoint.name`, the key `endpoint` of a model entry, or an entry of `tools.mcp` with no
   `url`. The relay request cannot name another target. A MCP target that the step does not name gives
   the reason `mcp_server_forbidden`. The coordinator finds the step by its name in the steps, in the
   handler steps and in the bodies of the `foreach` steps. When several steps have the name, each one
   MUST name the target. The `allow` of a MCP target is then the set of the tools that each such step
   allows.
6. The target exists in the scope rules of [Scope and name](093-ai-endpoints.md#scope-and-name).
7. The DAG may use the target. See [Use rule](093-ai-endpoints.md#use-rule).
8. The protocol of the endpoint matches the client. The provider `claude` needs `anthropic`. The type
   of a chat step comes from the endpoint, so it always matches. Only `CheckAIEndpoint` makes this
   check. On `RelayOpen`, the protocol of the endpoint selects the [allowed
   requests](#allowed-requests).
9. The target has a key, unless the preset allows no key.

A failed check gives a reason of [Failure kinds](#failure-kinds). The worker maps a refusal of the checks 1,
2 and 4 to the reason `endpoint_forbidden`. The coordinator checks items 1 to 4 and the lease again while a
stream is open, once every 10 seconds. A refusal of such a check cancels each stream of the attempt at
once. An error of the storage cancels them after 3 checks in a row that fail. The
client then gets the reason `relay_unavailable` with the message "the lease of the attempt ended".

The check 4 reads the status of the attempt and of the step from the store of the run. A step that is
`NotStarted`, `Running` or `Retrying` can use the relay. Another status of the step, and a final status of
the attempt, refuse the call.

### Limits and timeouts

The coordinator sets the limits. It sends them to the relay in `RelayAccepted`. The worker has no
key for them. See [Configuration](093-ai-endpoints.md#configuration) for the keys and the defaults.

- The relay sends the body in chunks of at most 65536 bytes. A receiver accepts a message of at most
  1 MiB. A larger first message gets the code `ResourceExhausted`. A larger later message ends the
  stream. A response message over the limit gives `response_too_large` to the client. The limit of the
  gRPC server stays at 16 MiB for the other calls. So the receiver checks the size of each message of
  the relay.
- The coordinator reads the whole request body before it calls the upstream. It does this so that it
  can send a `content-length`. A body over `max_request_bytes` gives `request_too_large`. The idle
  timeout and the total timeout apply to the read of the body too. A body that comes too slowly gives
  `upstream_timeout`.
- The request headers that the relay forwards have at most `max_header_bytes` bytes together. A
  larger set gives `request_too_large`.
- A response over `max_response_bytes` ends the stream with `response_too_large`.
- The coordinator connects to the upstream in `connect_timeout_sec`. It waits for the response head
  at most `response_header_timeout_sec`. A stream that moves no byte for `idle_timeout_sec` ends. A
  request lives at most `total_timeout_sec`. A timeout gives `upstream_timeout`.
- One run has at most `max_streams_per_run` open streams. The coordinator has at most `max_streams`.
  A request over a limit gets `too_many_streams`.

### Stream, cancel and backpressure

A response of the upstream can be a stream of server-sent events (SSE). The coordinator reads the
upstream body in chunks and sends each chunk at once. It does not wait for a full buffer. The relay
writes each chunk to the client and flushes it.

The gateway has no unbounded buffer. A sender waits when the flow control of gRPC or of TCP tells it
to wait. A client that reads slowly makes the coordinator read slowly from the upstream. The idle
timeout stops a stream that no one reads.

A client that closes its connection makes the relay send `RelayCancel` and close the stream. The
coordinator then cancels the call to the upstream. The end of the attempt, the stop of the step and a
lost lease have the same effect. The coordinator never lets an upstream call run after its stream
ends.

### Failure kinds

A refusal of the relay or of the coordinator has a reason. The client gets an HTTP status and a body
in the error format of its protocol. The message holds the reason name. The step log of the worker
gets one line, such as `ai relay: refuse POST /v1/messages: endpoint_forbidden`.

| Protocol | Body of a refusal |
| --- | --- |
| `anthropic` | `{"type":"error","error":{"type":"dagu_relay_error","message":"<reason>: <text>"}}` |
| `openai` | `{"error":{"message":"<reason>: <text>","type":"dagu_relay_error","code":"<reason>"}}` |
| A MCP path | A JSON-RPC error with the `id` `null`. The code is -32600 for `mcp_request_invalid`, and -32000 for each other reason. A refused `tools/call` has the code -32001 and the `id` of the request. See [The key allow](095-mcp-servers.md#the-key-allow). |

A refusal has the header `cache-control: no-store`. A refusal with the status 401 also has the header
`www-authenticate: Bearer realm="dagu"`.

| Reason | Status | Case |
| --- | --- | --- |
| `relay_unauthorized` | 401 | The request has no relay secret, or a wrong one. |
| `host_not_allowed` | 403 | The `Host` header or the form of the request is not allowed. |
| `path_not_allowed` | 404 | The path or the query is not in the list of the protocol. |
| `method_not_allowed` | 405 | The method is not in the list of the path. |
| `endpoint_unknown` | 404 | The name is not in a scope that the DAG can see. |
| `endpoint_forbidden` | 403 | The DAG is not in the `allow` lists, or the step does not name the target. |
| `endpoint_protocol_mismatch` | 400 | The protocol of the endpoint does not match the client. |
| `endpoint_key_missing` | 503 | The endpoint has no key. |
| `mcp_server_unknown` | 404 | The MCP server entry does not exist. |
| `mcp_server_forbidden` | 403 | The DAG may not use the MCP server entry. |
| `mcp_tool_not_allowed` | 200 | A `tools/call` names a tool that is not in `allow`. The body is a JSON-RPC error. |
| `mcp_request_invalid` | 400 | The body of a MCP request is not a JSON-RPC message that the coordinator can read as the server reads it. See [The key allow](095-mcp-servers.md#the-key-allow). |
| `request_too_large` | 413 | The body or the headers are over the limit. |
| `response_too_large` | 502 | The response is over the limit. |
| `too_many_streams` | 503 | A limit of open streams is reached. |
| `model_unknown` | 404 | The model of the step is not in the models list of its endpoint. Only the start check gives this reason, so no HTTP response has it. |
| `endpoint_unavailable` | 503 | The local process cannot open the store or decrypt the key. |
| `relay_unavailable` | 503 | The relay cannot reach the coordinator, or cannot start. |
| `upstream_error` | 502 | The connection, the TLS or the protocol to the upstream failed, or the upstream redirected. |
| `upstream_timeout` | 504 | A timeout of the coordinator ended the call. |
| `worker_too_old` | None | The worker cannot run the relay. See [Version check](#version-check). |

The relay adds the header `x-should-retry: false` to a refusal that no later request can change. These
are the reasons of the table with a status of 400 or more, except `too_many_streams`,
`relay_unavailable`, `endpoint_unavailable`, `upstream_error` and `upstream_timeout`. Claude Code repeats
a request that gets a status 401 with no such header, and gives no result. See [Measured with Claude
Code](#measured-with-claude-code).

The status 401 and 403 of the relay can appear as an `auth_required` attempt in Spec 091. Dagu
checks the target before the CLI starts, so these statuses are rare. See [Start checks](#start-checks).

### Start checks

Before a step that names a target starts its CLI or its client, the worker calls `CheckAIEndpoint`
with the step, the target and the provider. A failed check fails the step at once. The error has the
reason and the name of the target. No request goes to the provider.

```text
step "code_1": endpoint "cle1": endpoint_forbidden: the DAG "fix-bug" is not in the allow list
```

The failed step has the exit code 1. The kind of the attempt is `error`, with the reason of the table.

A step that names an endpoint and a model sends the model in the field `model` of the request. The
coordinator makes the checks in this order:

1. The endpoint exists, and the DAG may use it.
2. The protocol matches.
3. The endpoint has a key.
4. The model is in the list.

The first failure ends the check. An
unknown endpoint therefore gives `endpoint_unknown`, also when the model is wrong. The rule of the model
check is in [Use in a step](093-ai-endpoints.md#use-in-a-step).

The check of an unknown provider is not part of this check. A step on the old method with an unknown
provider fails. `dagu validate` rejects a literal provider that Dagu does not know. A step with a
provider reference fails when it starts. A provider that is not a CLI provider is a validation
error of `agent.run`.

### Version check

A worker announces the capability `ai_relay.v1` when it can run the relay. It runs a self-test at
start: it binds a listener on `127.0.0.1`. A worker that fails the test does not announce the
capability and logs the error.

A DAG needs the capability when it has one of these items:

- a step with the block `with.endpoint`
- a model entry with `endpoint`
- an entry of `tools.mcp` with no `url`

Dagu adds it to `required_capabilities` of each task of
the run. The coordinator matches a task only with a worker that announces each required capability.
This rule adds to the rules of Spec 092.

A run with no worker that has the capability waits, as a run with no worker that matches. After
`coordinator.worker_wait_timeout` the run fails. The error names the reason and the step, and ends with
the wait:

```text
no worker supports the AI relay: the step "code_1" names the endpoint "cle1" (worker_too_old); no worker took the run within 30s
```

With `coordinator.worker_wait_timeout` at 0, the coordinator refuses the dispatch at once, with the
message of a selector that no worker matches. A run in the queue of the scheduler stays queued, with the
conditions of an unmatched selector.

A step with no endpoint and no named MCP server needs no capability. It runs on each worker. A worker
of a release before the relay ignores the field `required_capabilities`. Only the match of the
coordinator keeps such a task away from it.

### Local mode

A local run has no coordinator and no worker. It is a run of `dagu start`, of `dagu start-all` with no
worker, or of a process with `coordinator.enabled: false`. The relay then calls the code of the
gateway in the same process. No gRPC stream is open, and no certificate is checked.

The process reads the endpoint store in its data directory. It makes the checks 5 to 9 of
[Identity checks](#identity-checks), with the run that it holds. The checks 1 to 4 do not apply,
because no certificate and no lease exist. It uses the same limits, the same allowed requests and the same audit line. A
process that cannot open the store or decrypt the key fails the step with `endpoint_unavailable`. A
process with no secret store has no gateway, and the step fails with `relay_unavailable`.

The commands `dagu start`, `dagu retry` and `dagu restart` set the local gateway for a run with the
worker ID `local`. The scheduler of `dagu start-all` starts a run with no worker through `dagu start`.

In this mode, the key is in the memory of the Dagu process. An untrusted step is safe from it only with
the restricted user of Spec 092. Without that user, the step fails. With `untrusted.isolation: off`, the
agent runs as the process user. See Spec 092, "Without isolation".

### Audit

The coordinator, or the local process, writes one audit line for each request. A request that a check
before the call refuses has a line too: it has the reason and the status of the refusal. A refusal of
an identity check gives a line with no run ID. The action is `ai_relay_request`. The line has:

- the run ID, the DAG name, the step name and the worker ID
- the target kind, the name and the scope
- the method and the name of the route (`messages`, `count_tokens`, `chat_completions`, `responses`,
  `models` or `mcp`), not the raw path
- the HTTP status, or the reason of a refusal
- the number of bytes of the request and of the response
- the time to the first byte of the response (`first_byte_ms`), and the total time (`duration_ms`), in
  milliseconds. A response that streams has a first byte long before its end.
- the fingerprint of the key that the request used

The line never has the key, a prefix, a header value, a body or a model name. A field with no value is
absent.

The line is a line of the log of the process, with the message "AI relay request" and the attribute
`action=ai_relay_request`. The coordinator is often a process with no audit store, and the audit store of
the server needs a license. The line does not go to the audit store.

### The egress proxy

The egress proxy of Spec 092 stays. It serves non-model traffic: the clone and the push of git
(`github.com`), and each other destination that a trusted step needs. A deployment that uses an
endpoint needs no model route in the proxy.

The options `--upstream`, `--upstream-auth` and `--upstream-key-*` stay valid for a deployment that
does not use endpoints. The two ways can exist together. The step decides: a step with `endpoint`
uses the gateway, and a step with no `endpoint` uses the variables of the deployment.

## The consumers

### agent.run

`agent.run` gets one key. The key is a block with the three keys of the table.

| Key | Type | Rule |
| --- | --- | --- |
| `endpoint` | map | The step MUST give it with the provider `claude`. A block with the keys `name`, `model` and `effort`. |
| `endpoint.name` | string | The step MUST give it. The name of an endpoint. Matches `^[a-z][a-z0-9-]{0,63}$`. A value reference is not valid. |
| `endpoint.model` | string | The step MUST give it. The model name for the endpoint. A value reference is valid. |
| `endpoint.effort` | string | Optional. `low`, `medium`, `high`, `xhigh` or `max`. A value reference is valid. |

With `endpoint`, these rules apply:

- `with.provider` is the CLI provider. The check of [Identity checks](#identity-checks) compares it with
  the protocol of the endpoint. `claude` needs `anthropic`.
- The loader accepts `endpoint` only with the provider `claude`. The other CLI providers have no client
  settings in this spec.
- Dagu passes `endpoint.model` to the CLI as `--model`.
- Dagu passes `endpoint.effort` to the CLI as `--effort`.
- With the provider `claude`, `model` and `effort` are not keys of `with`. Each one is an unknown key.
- The block is closed. An unknown key is a validation error.
- Dagu sets the variables of [Client settings](#client-settings) in the process of the CLI.
- The CLI never sees the key. The deployment needs no `ANTHROPIC_API_KEY` for this step.

A step with the provider `claude` and no `endpoint` is a validation error when Dagu loads the DAG, so the
run never starts. The model key lives in the server, and the agent worker never holds it. A step with no
endpoint cannot call a model safely. The error has this form:

```text
field 'with.endpoint': agent.run needs an endpoint; add one on the AI page and name it here
```

An `endpoint` that is not a block is also a validation error. A block with no `name` is a validation error too:

```text
field 'with.endpoint': endpoint must be a block with name and model
field 'with.endpoint.name': agent.run needs the name of an endpoint
```

A step with the provider `claude` and no `endpoint.model` is also a validation error when Dagu loads the
DAG. The model must be one that the endpoint lists. A model that is empty, blank or not a string is an
error too. The error has this form. A load that lacks both the name and the model reports both errors.

```text
field 'with.endpoint.model': agent.run needs a model; choose one that the endpoint lists
```

The key `endpoint.effort` stays optional. A step with no `endpoint.effort` uses the default of the CLI.

`dagu validate` checks that the name and the model are present and their form. It does not read the
endpoint store, so it cannot tell if the endpoint exists or if the endpoint lists the model. The step start
checks both. A model name that the endpoint lacks fails the step at start with the reason `model_unknown`.

### chat.completion

A `chat.completion` step names an AI endpoint in one of two forms. The block `endpoint` of `with` is the
form for one model. The key `endpoint` in each entry of a model list is the form for a fallback.

The block `with.endpoint` holds the endpoint, the model and the effort of the step in one place. It has
the same shape as the block of `agent.run`:

```yaml
with:
  endpoint:
    name: claude-sub
    model: claude-sonnet-5-5
    effort: high
```

| Key | Where | Rule |
| --- | --- | --- |
| `endpoint.name` | The block `with.endpoint` | Required. The literal name of an AI endpoint. |
| `endpoint.model` | The block `with.endpoint` | Required. A model name. A value reference such as `${MODEL}` is valid. |
| `endpoint.effort` | The block `with.endpoint` | Optional. One of `low`, `medium`, `high`, `xhigh` and `max`. |
| `endpoint` | An entry of the model list | Optional. The name of the endpoint of this entry. |

With an endpoint, the provider type comes from the endpoint, from the column Chat type. So `with.provider`
is not needed.

The rules of validation:

- The block is closed. An unknown key is an error that names the path and lists `name`, `model` and
  `effort`.
- A value that is not a block is an error: `with.endpoint: endpoint must be a block with name and model`.
- A missing `name` and a missing `model` are errors on the paths `with.endpoint.name` and
  `with.endpoint.model`. One load reports both.
- The name follows the rules of the name of an endpoint. It is a literal, and a value reference is an
  error.
- The value of `effort` is a literal level. A value reference is an error, because the step does not
  resolve a reference in the thinking configuration.
- The keys `model` and `thinking` of `with` contradict the block. Each one with the block is an error.
- The block with `api_key_name`, `base_url` or `provider` in the same `with` is an error.
- The key `endpoint` of an entry of the model list with `api_key_name`, `base_url` or `provider` in the same
  entry is an error.
- A contradiction names the two keys and the path of the key `endpoint`. The text after the semicolon says
  why:

  | Key | Text |
  | --- | --- |
  | `api_key_name` | an endpoint holds its own key |
  | `base_url` | an endpoint holds its own base URL |
  | `model` | the block endpoint gives the model |
  | `thinking` | the block endpoint gives the effort |
  | `provider` | an endpoint gives the provider type |

The effort of the block sets the thinking of the step. A block with an `effort` has the same effect as the
key `thinking: { enabled: true, effort: <effort> }` of the old method. A block with no `effort` sets no
thinking.

A step that needs a fallback keeps the list form of `with.model`. Each entry names its own endpoint, or it
uses the old method with `provider`. There is no default endpoint for the entries, and the block has no
list:

```yaml
with:
  model:
    - name: claude-sonnet-4-6
      endpoint: cle1
    - name: gpt-4o
      provider: openai
      api_key_name: OPENAI_API_KEY
```

An entry with no `provider` and no `endpoint` is an error.

The old method is deprecated. It stays valid and does not change. The step reads the key from the
environment variable that `api_key_name` names, and the call goes from the step process to the provider.
`dagu validate` prints a warning for a step that uses `provider`, `base_url` or `api_key_name`. See [Spec
059](059-chat-completion.md#keys-and-endpoints).

A step with an endpoint sends its requests to its relay. The relay serves `chat.completion` as it
serves a CLI.

These rules apply to a step with an endpoint:

- The loader compiles the block `endpoint` to the endpoint name and the model of the step, and to the
  thinking of the effort. The entries of a model list keep their own `endpoint`. An entry needs a
  `provider`, or an `endpoint` of its own.
- The start check runs for each endpoint of the models, in the order of the models, before the first
  request. The first failed check fails the step. The check sends no provider, because the type comes
  from the endpoint.
- Each model that names an endpoint starts a relay for its attempt, and the relay ends with the
  attempt. A model that uses the old method starts no relay.
- The provider client has the type of the column Chat type, the base URL of its relay, and the relay
  secret as its key. The step has no other key and no other address.
- Streaming, tool calling, structured output and the fallback to the next model work as they do with
  the old method. A fallback entry with another endpoint has its own relay.
- When the relay refuses a request, the error of the step names the endpoint and the reason of
  [Failure kinds](#failure-kinds).
- The step runs in the Dagu process, and no other process needs the secret of the relay.
- A DAG with such a step needs the capability `ai_relay.v1`. See [Version check](#version-check).

The key `endpoint` is not valid in the key `llm` of a DAG. So an agent DAG never uses an endpoint, and
nor do the steps that inherit that block. It is not valid in the `llm` block of `browser.run`,
`browser.extract`, `computer.run` or `computer.extract`. The step key `llm` of a `chat.completion` step
refuses it too, with the error `the key endpoint is valid only with the action chat.completion`. An
entry of the model list of that key can name an endpoint, with the rules of an entry of `with.model`.

### harness.run

`harness.run` does not change. It has no key `endpoint`, because each other key of its `with` is a flag
of the CLI.

A step of the CLI `claude` that must use an endpoint uses `agent.run`. This table gives the key of
`agent.run` for each common flag of the CLI:

| `harness.run` | `agent.run` |
| --- | --- |
| `with.provider: claude` | `with.provider: claude` and `with.endpoint.name: <name>` |
| `with.model` or the flag `--model` | `with.endpoint.model` |
| The flag `--effort` | `with.endpoint.effort` |
| `with.prompt` | `with.prompt` |
| The flag `--append-system-prompt` | `with.system` |
| The flag `--max-turns` | `with.max_tool_iterations` |
| The flags `--allowedTools` and `--disallowedTools` | `with.tools.builtin` |
| The flag `--mcp-config` | `with.tools.mcp` |
| The flag `--json-schema` | The step key `output_schema` |
| The key in `env`, such as `ANTHROPIC_API_KEY` | None. The endpoint holds the key. |

The CLI `codex` is not an endpoint client of `agent.run`. A `harness.run` step with `codex` keeps the
key in its environment.

A DAG that has an `agent.run` step makes its other process steps untrusted by default (Spec 092). This
includes `harness.run`. An untrusted step gets no secret, and the process can read its own
environment. A `harness.run` step that reads an API key from `env` or from `env_passthrough` gives the
key to an agent that a prompt controls. Use an endpoint through `agent.run`, or mark the step
`untrusted: false` and list the key in `secrets`. In the second case, the CLI is a trusted process, and
the agent in it can read the key.

## Errors

`dagu validate` rejects these cases with a nonzero exit code:

- no `endpoint` in an `agent.run` step whose provider is `claude`. The error is `agent.run needs an
  endpoint; add one on the AI page and name it here`.
- an `endpoint` that is not a block, in `agent.run` or in `chat.completion`. The error is `endpoint must
  be a block with name and model`.
- a block `endpoint` with no `name`. The error is `agent.run needs the name of an endpoint`, or
  `chat.completion needs the name of an endpoint`.
- a block `endpoint` with no `model`, or a `model` that is empty, blank or not a string. The error is
  `agent.run needs a model; choose one that the endpoint lists`, or `chat.completion needs a model;
  choose one that the endpoint lists`.
- an endpoint name that is not a string, is empty, does not match the name rule, or is a value
  reference. This applies to `endpoint.name` and to the key `endpoint` of a model entry.
- an unknown key in the block `endpoint`, or an `effort` that is not one of the five levels
- the block `endpoint` of `chat.completion` with `model`, `thinking`, `api_key_name`, `base_url` or
  `provider` in the same `with`
- the key `endpoint` of a model entry with `api_key_name`, `base_url` or `provider` in the same entry
- the key `endpoint` in the `llm` block of a DAG. The same applies to the `llm` block of a step of
  another action. The error is `the key endpoint is valid only with the action chat.completion`.
- an `endpoint` in an `agent.run` step whose provider is not `claude`

A `harness.run` step does not change. Its `with` passes each unknown key to the CLI as a flag. So
`dagu validate` accepts a key `endpoint` there, and the CLI gets a flag with this name.

These cases fail a step at run time, with the reasons of [Failure kinds](#failure-kinds):

- a process that has no gateway: the reason is `relay_unavailable`
- an unknown or forbidden target, a protocol mismatch or no key, at the start check
- no worker with the capability `ai_relay.v1`
- each refusal of the relay and each failure of the upstream

The errors of the endpoint routes, of the secret refs and of the block `ai` are in [Spec
093](093-ai-endpoints.md#errors). The reason `model_unknown` is there too. The errors of the named MCP
entries are in [Spec 095](095-mcp-servers.md#errors).

## Examples

### An agent step with an endpoint

The step checks the endpoint when it starts, then runs the CLI through the relay.

```yaml
steps:
  - id: code
    action: agent.run
    with:
      endpoint:
        name: cle1
        model: claude-sonnet-4-6
      provider: claude
      prompt: "Fix the failing test."
      tools:
        builtin: { read: true, write: true, edit: true, search: true }
        mcp:
          - name: docs
            allow: [search]
```

The step needs no `ANTHROPIC_API_KEY` in the worker. The DAG `fix-bug` must be in `allow.dags` of the
endpoint `cle1` and of the MCP server `docs`.

### A chat step with an endpoint

```yaml
steps:
  - id: classify
    action: chat.completion
    with:
      endpoint:
        name: cle1
        model: claude-haiku-4-5
        effort: high
      prompt: "Classify this note."
```

A list of models with a fallback. The first entry names its own endpoint. The second entry uses
the old method:

```yaml
    with:
      model:
        - name: claude-sonnet-4-6
          endpoint: cle1
        - name: gpt-4o
          provider: openai
          api_key_name: OPENAI_API_KEY
```

### A contradiction

```yaml
    with:
      endpoint:
        name: cle1
        model: claude-sonnet-4-6
      base_url: https://api.anthropic.com
      prompt: "Hello"
```

```text
field 'with.endpoint': key "endpoint" and key "base_url" contradict each other; an endpoint holds its own base URL
```

## Conformance

The tests run the Dagu binary on static fixtures in `conformance/spec094_ai_gateway/`. They use
three fakes. The fake upstream is a local HTTP server that records each request and serves SSE. The
fake `claude` is an executable that calls `ANTHROPIC_BASE_URL` with `ANTHROPIC_API_KEY`, and the MCP
servers of the MCP config of its attempt. The fake MCP server records the method, the path, the
headers and the body of each request, and streams events on a `GET`. No test needs an external
service. The fixtures name the endpoint `test-endpoint` and the model `claude-test`, except where the
name of a model is the subject of the test.

The tests of a worker run a coordinator with mutual TLS and the pools of Spec 092. A test of an agent
worker changes to the restricted user. It needs Linux, the user ID 0 and the flag
no-new-privileges. It skips on another host. The table marks these tests "root". A test of a
general worker runs with each user ID.

Each test that has a key checks that the key is in no response, no output, no log and no file of the
worker.

| Section | Tests |
| --- | --- |
| The relay, client settings, allowed requests | `TestRelayOnAnAgentWorker` (root), `TestRelayLocalMode`. |
| Limits, stream, cancel | `TestRelayLargeAndManyChunks` (root), `TestRelayLimits` (root), and the subtest `StepStopCancelsTheProviderCall` of `TestRelayOnAnAgentWorker` (root). |
| Failure kinds, start checks | `TestRelayRefusalsAndKeyChanges` (root), `TestStepWithAnUnknownEndpointFailsBeforeTheCLIStarts`, `TestRelayLocalMode`. |
| Identity checks | The checks 3 and 5 to 9: the tests of the start checks. Go tests of the coordinator, with a real gRPC server and mutual TLS, check the rest. They check the checks 1, 2 and 4, a lost lease that cancels the open streams, and the worker pools. |
| Version check | Go tests. The binary has no option that removes the capability `ai_relay.v1`, and this spec adds no key for it. |
| Local mode | `TestRelayLocalMode`, `TestChatEndpointLocalMode`. The tests of the other specs for the local mode are `TestModelCheckLocalMode` (Spec 093), `TestMCPRelayLocalMode` and `TestChatNamedMCPLocalMode` (Spec 095). |
| Audit | Go tests of the handlers and of the gateway. The audit store needs a license. |
| agent.run | `TestValidateChecksTheFormOfTheEndpoint`, and Spec 091, `TestValidateRejectsWhatTheProviderCannotRun`. |
| chat.completion | `TestChatEndpointLocalMode`, `TestValidateChatEndpointContradictions`, `TestChatEndpointOnAWorker`, `TestChatEndpointOnAWorkerForbiddenDAG`. |
| harness.run | Not covered: `harness.run` does not change. |

## Open items

Each item has a default. The default applies until the owner decides.

### Measured with Claude Code

The author ran the real Claude Code 2.1.311 on 2026-11-04. The model was `claude-haiku-4-5`. The
endpoint held an OAuth token of a subscription, with the form `anthropic-oauth`. The agent worker
held no model key, and the proxy had no model route. The demo `examples/agent-worker-demo` holds the
scripts and the evidence format. These facts are measured.

| Subject | Fact |
| --- | --- |
| The run | A run with a skill, a DAG tool, a named MCP server and a fix of a test succeeded. It took 12 turns and 8 model requests. All 8 requests had the status 200. The typed result was valid. |
| Streaming | The first byte of a response reached the relay at 435 to 569 ms. The response ended at 1.4 to 2.0 s. The audit field `first_byte_ms` shows this. The CLI got the events as they came. |
| The key | The worker never held the token. A scan of its environment, files, processes, arguments and logs found no hit in 25 samples during the run. The environment of the CLI had a relay secret in `ANTHROPIC_API_KEY`, and no other credential. |
| Skills | The CLI finds a skill in `<agent directory>/.claude/skills/<name>/`, when Dagu passes the agent directory with `--add-dir`. The event `system/init` lists the skill in `skills` and the directory in `additional_directories`. The tool `Skill` gives the text of `SKILL.md` to the model. The agent named the skill in its result. |
| The Dagu tool bridge | It works with an endpoint. The CLI called the DAG tool `lookup-ticket` as a child run, with the `CUSTOMER_ID` of the DAG. |
| A start check refusal | The step failed before the CLI started, with the reason `endpoint_forbidden`. No model request left. |
| A 401 of the provider | The relay gave the status and the headers of the provider to the CLI. The CLI sent one request and stopped. Its result was "Failed to authenticate. API Error: 401". Dagu ended the step with the kind `auth_required`. |
| A 401 of the relay | Without the header `x-should-retry`, the CLI sent at least 8 requests in 100 s and gave no result. With `x-should-retry: false`, the CLI sent one request and stopped. The relay sets this header. See [Failure kinds](#failure-kinds). |
| A 403 of the relay | The CLI reports it as an authentication failure: the assistant error `authentication_failed`, with the text "Failed to authenticate. API Error: 403" and the reason of the relay. |
| A 404 of the relay | The CLI reports it as a model error: "There's an issue with the selected model". The text does not name the cause. Dagu ended the step with the kind `error` and the reason of the relay, `endpoint_unknown`. The relay refusal that Dagu noted gives the reason. |
| A 5xx of the relay | The CLI repeats the request with a wait. It gave no result in 120 s. |
| The order of steps | The worker runs a trusted step and an untrusted step one after the other. An `agent.run` step is untrusted. A step of the same DAG cannot change an endpoint during the agent step. A script outside the run does this. |

### Decisions to confirm

| Item | Default |
| --- | --- |
| The gateway needs mutual TLS. A coordinator with no mutual TLS refuses it. | As in this spec. Use the local mode for a development host. |
| A trusted step of an agent DAG, such as `chat.completion`, calls its relay on the loopback. A direct call in the process would skip the hop. | The relay serves both, to keep one path. |
| The relay secret goes to the CLI as the variable `ANTHROPIC_API_KEY`. A settings file of the CLI with an `env` block is not verified. | The variables. |
| The audit log gets one line for each request. The volume can be high. | One line for each request. No sample and no count. |
| The CLI `codex` as an endpoint client of `agent.run`, and the path `GET /v1/responses/{id}` that it can need. | Not added. |
| `dagu validate` cannot check that an endpoint exists, because it does not read the store. | The step start checks it. The editor shows an issue. |

### Values to verify

| Item | State |
| --- | --- |
| The way that Claude Code reports a 401, 403 or 404 of the relay. | Measured. See [Measured with Claude Code](#measured-with-claude-code). |

### Technical risks

| Risk | Note |
| --- | --- |
| SSE over a gRPC stream. A proxy of the deployment between the worker and the coordinator can buffer or cut a stream. | The idle timeout and the cancel are the controls. In the tests, the first event reaches the client within 25 ms of the first byte of the provider. 64 MiB cross the gateway in less than 1 s on one host. The real CLI streams through the gateway. See [Measured with Claude Code](#measured-with-claude-code). A deployment with a proxy between the worker and the coordinator needs its own test. |
| The load on the coordinator. It holds each stream and each request body, up to `max_streams` times `max_request_bytes`. | The defaults give at most 2 GiB. A deployment sets lower values. |
| The coordinator needs outbound access to the providers, and the worker does not. | A deployment opens it for the coordinator. `ai.upstream.proxy_url` serves a proxy. |
| The limits of the size of a gRPC message. | The body goes in chunks of 64 KiB. A message over 1 MiB is refused. The global limit of 16 MiB is not the limit of a request. |
| The coordinator holds a request body in memory to set `content-length`. | No chunked upload to the upstream. |
| The coordinator is a single point of failure for the model calls of all the agent workers. | A deployment runs the coordinator with a restart policy. High availability is out of scope. |
