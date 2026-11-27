# RFC 023: Publish Workflows as A2A Agents

- **Status:** Draft
- **Date:** 2026-11-23
- **Issue:** https://github.com/dagucloud/dagu/issues/4048
- **Affected areas:** `spec` (DAG-level `outputs`, publishing files),
  `ir` (run status fields), `intake`, `queue` (retry admission), `auth`
  (`a2a` key surface), `persis/file/dag` (discovery), `gitsync`,
  `service/frontend` (A2A endpoints, AI Catalog), `cmn/config`,
  `runtime/builtin/a2a` (`a2a.send` message IDs), `specs`, `conformance`,
  and `docs`

## Summary

Let an external A2A agent discover and call a Dagu workflow. An operator
publishes a DAG by adding one file to `<dags_dir>/a2a/`. Dagu then serves an
Agent Card and an A2A 1.0 JSON-RPC endpoint for it. `SendMessage` enqueues one
ordinary DAG run, and `GetTask` and `CancelTask` act on that run. The DAG run
is the only record: the task ID is the run ID, and nothing else is stored.

This is the inbound half of A2A support. The outbound half is `a2a.send` and
`a2a.get_task` (Spec 099), which lists hosting agents as out of scope. This
RFC also makes a retried `a2a.send` step resend the same message ID, so a call
from one Dagu to another does not start a second run. The design follows the
proposal in #4048.

```text
<dags_dir>/a2a/incident-diagnostics.yaml        A2A client
  dag: incident-diagnostics                     ----------
                                                    |
GET  /a2a/agents/incident-diagnostics/.well-known/agent-card.json
POST /a2a/agents/incident-diagnostics   SendMessage | GetTask | CancelTask
        |
        v
  auth (a2a surface, subject) -> intake enqueue -> queue -> run
        |                                              |
        +------------- task = read of the run <--------+
```

## Problem statement

- **An A2A agent cannot call a Dagu workflow.** It needs a wrapper that turns
  A2A messages into REST calls, polls the run, and maps its status back.
- **The REST API and MCP do not speak A2A.** A2A agents expect an Agent Card,
  JSON-RPC methods, and tasks with A2A states. MCP serves LLM tool clients.
- **Dagu-to-Dagu delegation has no target.** `a2a.send` can call a remote
  agent, but another Dagu instance cannot be that agent.

## Goals

1. An operator publishes an existing DAG with one file and no change to its
   steps.
2. An A2A 1.0 JSON-RPC client finds the agent through its Agent Card or the AI
   Catalog, sends a message, polls the task, and cancels it.
3. Each task is exactly one DAG run. The run is the single source of truth.
4. A retried `SendMessage` never starts a second run, including one that a
   retried `a2a.send` step resends.
5. A task in a terminal state never changes.
6. A caller sees only its own tasks, and still sees them after it rotates its
   API key.
7. Nothing changes for a server that does not turn A2A on.

## Non-goals

- Streaming, push notifications, and `SubscribeToTask`.
- Multi-turn tasks: continuing a task with `input-required` or
  `auth-required`.
- Step-level progress. Step states can be derived from the run status later
  without a migration. A2A's channels for progress are `TaskStatus.message`
  and `TaskStatusUpdateEvent`.
- `ListTasks` and the extended Agent Card.
- The gRPC and HTTP+JSON bindings, and A2A versions other than 1.0.
- An LLM that routes messages between workflows. Each agent is one workflow.
- Using DAG-level `outputs` in the REST API, MCP, or sub-DAG results. The
  field is general, and those surfaces can adopt it in later changes.
- Structured data parts in outbound `a2a.send`. That is a change to Spec 099.

## Guarantees

Promised:

- **One run per message.** The same caller sending the same `messageId` to
  the same agent gets the same task while the run is kept.
- **Terminal tasks stay terminal.** A run started through A2A cannot be
  retried. A client retries by sending a new message.
- **Rejected input starts nothing.** Input that fails validation returns an
  error, and no run is created.
- **Unauthorized callers learn nothing.** An agent, card, catalog entry, or
  task the caller cannot access answers as if it did not exist.
- **Retiring keeps tasks reachable.** A retired agent takes no new messages,
  and its callers can still read and cancel their tasks.
- **No idle cost.** No ticker, watcher, or index is added.

Not promised:

- **Deduplication after retention.** Once retention removes the run, the same
  `messageId` starts a new run. `GetTask` for a removed run returns
  `TaskNotFoundError`.
- **A final state from a blocking call.** A blocking `SendMessage` waits at
  most `a2a.max_wait` and may return a task that is still running.
- **Recovery of an admission cut by a crash.** A crash between writing the
  run and queuing it leaves the task submitted until an operator dequeues
  it.

## Design

### Server setting

| Key | Environment | Default | Meaning |
| --- | --- | --- | --- |
| `a2a.enabled` | `DAGU_A2A_ENABLED` | `false` | Serves the A2A endpoints and the AI Catalog. Off answers 404 on all of them. |
| `a2a.max_wait` | `DAGU_A2A_MAX_WAIT` | `30s` | Longest wait of a blocking `SendMessage`. The handler extends its write deadline to cover the wait. |
| `paths.a2a_dir` | `DAGU_A2A_DIR` | `<dags_dir>/a2a` | Directory of publishing files. |

The server refuses to start with `a2a.enabled: true` and no `public_url`,
because the Agent Card must name an absolute URL for its interface.

### Publishing files

A DAG is published by a file in `paths.a2a_dir`:

```yaml
# <dags_dir>/a2a/incident-diagnostics.yaml
dag: incident-diagnostics
name: Incident Diagnostics
description: Diagnose an operational incident
tags: [operations, diagnostics]
text_param: PROMPT
```

| Field | Required | Meaning |
| --- | --- | --- |
| `dag` | yes | Name of the DAG to publish. |
| `name` | no | Agent name on the card. Defaults to the DAG name. |
| `description` | no | Agent description. Defaults to the DAG description. |
| `tags` | no | Skill tags on the card and the catalog. |
| `text_param` | no | Parameter that receives a text part. Without it, only data parts are accepted. |
| `retired` | no | `true` stops new messages and keeps existing tasks reachable. See Retiring an agent. |

Rules:

- The agent ID is the file's base name. It matches `^[A-Za-z0-9_-]+$` and is
  at most 64 characters. It appears in URLs, so renaming the DAG does not
  change the agent's address.
- A file's presence publishes the DAG, unless the file sets `retired: true`.
  Removing or moving the file revokes the agent.
- Each DAG has at most one publishing file.
- Unknown fields are invalid.
- `dagu validate` checks a publishing file: the DAG exists, no other file
  publishes it, and `text_param` names one of its parameters.

The directory sits beside the DAGs because publishing is a definition people
write and review, like DAGs, the Wiki (`<dags_dir>/wiki`), and calendars
(`<dags_dir>/calendars`). It holds no secrets, since callers authenticate with
API keys in the data directory. Two changes follow:

- **Discovery skips the directory.** It is reserved as `workspaces` is
  (`internal/persis/file/dag/discovery.go`), so recursive discovery does not
  read publishing files as DAGs.
- **Git sync carries it.** Git sync handles each item kind on its own, so
  publishing files become a new kind beside calendars and Wiki pages.

### Retiring an agent

An agent stops in one of two ways:

| State | New `SendMessage` | Card and catalog | `GetTask` and `CancelTask` |
| --- | --- | --- | --- |
| Published | Accepted | Served and listed | Served |
| Retired (`retired: true`) | Refused | HTTP 404, not listed | Served |
| File deleted | HTTP 404 | HTTP 404, not listed | HTTP 404 |

- **Retiring is the normal way to stop an agent.** Callers can still read
  and cancel the tasks they started until retention removes the runs.
- A retired agent refuses a new message with `UnsupportedOperationError` and
  creates no run. A message whose `messageId` already has a task returns that
  task, so a client that lost a response still gets its answer.
- Removing `retired` publishes the agent again.
- **Deleting the file revokes the agent at once,** for example after a leak.
  Its tasks answer 404 with the rest of its paths, because nothing else maps
  the agent ID to its DAG. The runs continue in Dagu, and operators handle
  them there.
- A retired file is still the DAG's one publishing file, so the agent ID
  keeps naming the same DAG while its tasks are served. `dagu validate`
  checks it as any other publishing file.

### DAG-level outputs

A DAG declares its result with a root `outputs` field. Each entry maps a
result name to one step output:

```yaml
name: incident-diagnostics
description: Diagnose an operational incident

params:
  schema:
    type: object
    properties:
      incident_id:
        type: string
    required: [incident_id]

outputs:
  summary: ${diagnose.outputs.summary}
  findings: ${diagnose.outputs.findings}

steps:
  - id: diagnose
    run: ./diagnose.sh "${params.incident_id}"
    outputs:
      - name: summary
      - name: findings
        type: json
```

Rules:

- Each value is exactly one `${<step id>.outputs.<name>}` reference.
  Literals and templates are invalid.
- `dagu validate` checks that the step exists and declares that output.
- Result names match `^[A-Za-z][A-Za-z0-9_]*$`, the rule for step output
  names (Spec 012).
- The result is built from the run when it is read: from the DAG definition
  stored with the run and the step outputs in its status. Nothing new is
  written, and editing the DAG later does not change an old run's result.
- A `json` output is embedded as its JSON value. A `string` output is a JSON
  string.
- An output of a step that published none, for example one that was skipped,
  is left out.
- The result is built only for a successful run.

The field is not specific to A2A. A2A is its first reader.

### Endpoints

All paths are under the server's base path.

| Method and path | Purpose |
| --- | --- |
| `GET /a2a/agents/{id}/.well-known/agent-card.json` | Agent Card. |
| `POST /a2a/agents/{id}` | A2A 1.0 JSON-RPC endpoint. |
| `GET /.well-known/ai-catalog.json` | AI Catalog of the caller's agents. |

Every endpoint requires an API key, as described under Identity and access.
The JSON-RPC handler is the SDK's `a2asrv.NewJSONRPCHandler` with a
Dagu-owned `a2asrv.RequestHandler`. The SDK's default handler is not used,
because its task store keeps task state of its own.

### Agent Card

```json
{
  "name": "Incident Diagnostics",
  "description": "Diagnose an operational incident",
  "version": "3f9c2a1b",
  "supportedInterfaces": [
    {
      "url": "https://dagu.example.com/a2a/agents/incident-diagnostics",
      "protocolBinding": "JSONRPC",
      "protocolVersion": "1.0"
    }
  ],
  "capabilities": {
    "streaming": false,
    "pushNotifications": false,
    "extensions": [
      {
        "uri": "urn:dagu:a2a:workflow:v1",
        "params": {
          "inputSchema": { "type": "object", "properties": { "incident_id": { "type": "string" } }, "required": ["incident_id"] },
          "outputs": { "summary": "string", "findings": "json" },
          "maxWaitSeconds": 30
        }
      }
    ]
  },
  "defaultInputModes": ["application/json", "text/plain"],
  "defaultOutputModes": ["application/json"],
  "skills": [
    {
      "id": "incident-diagnostics",
      "name": "Incident Diagnostics",
      "description": "Diagnose an operational incident",
      "tags": ["operations", "diagnostics"]
    }
  ],
  "securitySchemes": {
    "dagu": { "httpAuthSecurityScheme": { "scheme": "Bearer" } }
  },
  "securityRequirements": [
    { "schemes": { "dagu": { "list": [] } } }
  ]
}
```

- The interface URL is `public_url` joined with the base path and the
  agent's path.
- `version` is a short digest of the publishing file and the DAG definition,
  so a client can tell when the contract changed.
- A2A skills carry no input schema, so the parameter schema and the result
  names travel in a Dagu extension.
- `maxWaitSeconds` is `a2a.max_wait`. It tells a client how long a blocking
  `SendMessage` waits before it answers with a task that is still running.
- `text/plain` is listed only when the file sets `text_param`.
- `securityRequirements` requires the `dagu` scheme on every call.

### Identity and access

A2A defines no credentials. It names the accepted schemes in the card and
leaves issuing credentials to the server (A2A §7). Dagu's scheme is its
existing API key, sent as `Authorization: Bearer`.

- **Surface.** A new API-key surface, `a2a`, beside `rest_api` and `mcp`.
  Existing keys do not get it, including keys whose surfaces are empty.
- **Subject.** The owner of a task is the key's subject, not the key: the
  owner user for a `user_owned` key, or the service account for a
  `service_account` key. Rotating a key keeps the owner and its tasks.
- **Service accounts.** Today a service-account key's account ID is derived
  from the key (`api_key:<key id>`, `internal/auth/apikey.go`), so a new key
  is a new account. This RFC depends on service accounts that own several
  keys, designed in their own RFC. Migrating each existing key to an account
  whose ID is its current derived ID keeps today's audit subjects valid.
- **Run attribute.** The run status records the subject as
  `triggerSubject: {type, id}`, and every later attempt keeps it.
  `triggerActor` stays for display. The field is general, not an A2A one:
  this RFC sets it for A2A runs, and other surfaces can set it later.
- **Authorization.** Each request is checked against the role and workspace
  access of the key it carries, as they are when the request arrives.
  - Cards, catalog entries, and `SendMessage` require permission to run the
    DAG, as for REST enqueue.
  - `GetTask` requires access to the run's workspace, as a REST read of the
    run does. `CancelTask` requires permission to run the DAG, as REST stop
    does.
  - `GetTask` and `CancelTask` also require the run's `triggerSubject` to
    match the caller. Owning a task does not bypass the other checks: a new
    key of the same subject with a narrower role or fewer workspaces cannot
    reach a task outside its scope.
  - A failed check answers as if the agent or task did not exist: HTTP 404
    for cards and endpoints, and `TaskNotFoundError` for tasks (A2A §3.3.2).

### SendMessage

1. **Validate the message.**
   - It has exactly one part.
   - A data part must be a JSON object and becomes the run's params,
     canonicalized to compact JSON as MCP does (Spec 036).
   - A text part becomes `{<text_param>: <text>}` when `text_param` is set,
     and is invalid otherwise.
   - A message that sets `taskId` is refused with
     `UnsupportedOperationError`, because tasks are single-turn.
   - `referenceTaskIds` is accepted and ignored.
   - Params are validated as for any enqueue, including the DAG's parameter
     schema. Any failure returns `InvalidParamsError` and creates no run.
2. **Derive the run ID.** The run ID is the base62 form of a UUIDv5 over
   (subject type, subject ID, `messageId`). It has 22 characters, within the
   run ID rules (`internal/ir/run_id.go`). Run IDs are scoped to the DAG, so
   the agent needs no place in the hash.
3. **Enqueue.** The run is admitted through the same intake path as REST
   enqueue, so queues and admission checks apply. The admission records:
   - the derived run ID and the params
   - `triggerType: a2a`, for display
   - `triggerSubject`
   - `retryDisabled: true`
   - `a2aContextId` when the message carries a `contextId`
4. **Handle a duplicate.** The run store refuses a run ID that exists, under
   its per-DAG lock (`internal/persis/file/dagrun/store.go`). On that refusal
   the handler compares the stored params with the new ones:
   - Equal params return the existing task.
   - Different params return `InvalidParamsError` saying the `messageId` was
     reused.
5. **Answer.**
   - With `returnImmediately: true`, it answers with the task at once.
   - Otherwise it waits until the task is terminal or `a2a.max_wait` passes,
     reading the run status as the synchronous REST start does (100 ms
     growing to 2 s). Then it answers with the task as it is. A2A §3.2.2
     requires a blocking call to wait for a terminal or interrupted state, so
     returning a running task is a documented deviation.
   - The frontend's 60s write timeout does not force the limit. The handler
     extends its write deadline, as the MCP endpoint and the synchronous
     REST start already do. The limit exists because reverse proxies and
     load balancers in front of Dagu commonly close a response after 60s,
     and the client then gets an error instead of the task. Each wait also
     holds a connection and a goroutine.
   - A client that sends a blocking message must poll `GetTask` when the
     answer is not terminal. The card states the limit as `maxWaitSeconds`.
     Dagu's `a2a.send` already reports a non-terminal task with its state
     (Spec 099). The spec's own §6.6 example also returns a submitted task
     without `returnImmediately`.

The answer is always the `task` variant, never a direct message.

### Task

`GetTask` reads the run's latest attempt and builds the task. Nothing about
the task is stored except the run.

- `id` is the run ID.
- `contextId` is `a2aContextId` when set, and the run ID otherwise.
- History is omitted.

| Run status | Task state |
| --- | --- |
| `not_started`, `queued` | `TASK_STATE_SUBMITTED` |
| `running` | `TASK_STATE_WORKING` |
| `waiting` | `TASK_STATE_WORKING`, with a status message saying the run waits for a person in Dagu |
| `succeeded`, `partially_succeeded` | `TASK_STATE_COMPLETED` |
| `failed` | `TASK_STATE_FAILED` |
| `aborted` | `TASK_STATE_CANCELED` |
| `rejected` | `TASK_STATE_REJECTED` |

A Dagu run that waits for a person is not `input-required`, because the A2A
client cannot answer it. The person answers in Dagu.

A completed task carries one artifact when the DAG declares `outputs`:

```json
{
  "artifactId": "outputs",
  "name": "outputs",
  "parts": [
    {
      "data": { "summary": "No active incident detected", "findings": [{ "service": "api", "status": "ok" }] },
      "mediaType": "application/json"
    }
  ]
}
```

- A result larger than the DAG's `max_output_size` is left out, and the
  status message says so.
- Failed, canceled, and rejected tasks carry no artifact. Their status
  message says the run failed and names no step, path, or error text.
  Operators read the details in Dagu.

### CancelTask

- A queued run is dequeued as the REST dequeue does, and a running or
  waiting run is stopped as the REST stop does.
- **A dequeued run stays visible as `aborted`.** Today a dequeue hides the
  attempt and removes a run left with no visible attempt
  (`internal/queue/queue_abort.go`). For an A2A run that would turn a
  canceled task into `TaskNotFoundError`, and the same `messageId` would
  start a new run. The dequeue already keeps a restart run visible. It
  keeps a `retryDisabled` run visible the same way, because both rules keep
  a terminal task final. The rule lives in that shared function, so the
  REST, UI, and CLI dequeue keep such a run too.
- The answer is the task. It may still be `WORKING` until the stop finishes.
- A terminal task returns `TaskNotCancelableError`.

### Other methods

Every other method returns `UnsupportedOperationError`:
`SendStreamingMessage`, `SubscribeToTask`, `ListTasks`,
`GetExtendedAgentCard`, and the push notification methods. `ListTasks`
would need a lookup of runs by subject, which no index provides.

### Retries

A Dagu retry makes a new attempt of the same run, so a `failed` task would
become `working` again. A2A forbids that.

- **`retryDisabled` is a run attribute,** separate from `triggerType`. It is
  set at admission, and every later attempt keeps it, as `BackfillID` is
  kept.
- **Admission enforces it in one place.** Retry admission
  (`internal/queue/enqueue_retry.go`: `PrepareRetry` and `EnqueueRetry`)
  refuses to retry a terminal run that has `retryDisabled`. This covers the
  REST and UI retry, step retry, the DAG `retry_policy` scanner, queued
  `dagu retry`, and human-task resume. A direct `dagu retry` applies the same
  rule. The scanner records a skip, not an error.
- **Resume still works.** A run waiting for a person is not terminal, so
  approve, reject, and push back continue it.
- **Step-level `retry_policy` is unaffected.** It retries inside the run and
  never changes the run's terminal state.
- **The UI disables Retry** for such a run and says that the caller retries
  by sending a new message.
- **The DAG-level `retry_policy` does not apply** to runs started through
  A2A.

### AI Catalog

`GET /.well-known/ai-catalog.json` answers an AI Catalog v0.9 document with
the agents the caller may run:

```json
{
  "specVersion": "0.9",
  "host": { "displayName": "Dagu" },
  "entries": [
    {
      "identifier": "https://dagu.example.com/a2a/agents/incident-diagnostics/.well-known/agent-card.json",
      "type": "application/a2a-agent-card+json",
      "url": "https://dagu.example.com/a2a/agents/incident-diagnostics/.well-known/agent-card.json",
      "displayName": "Incident Diagnostics",
      "description": "Diagnose an operational incident",
      "tags": ["operations", "diagnostics"]
    }
  ]
}
```

- AI Catalog v0.9 allows a catalog at any URL ("Location Independence"), so
  the path is under the base path. An origin-level `/.well-known/` path with
  a base path set needs a reverse proxy.
- The catalog defines no access control. Dagu requires an `a2a` key and
  lists only agents whose DAG the caller may run.
- A retired agent is left out.
- A publishing file whose DAG is missing or invalid is left out, and the
  server logs a warning once per change of the file.

### Outbound message IDs

`a2a.send` gives each execution a random `messageId` (Spec 099). Toward a
Dagu agent this defeats the deduplication above: a step retry after a lost
response sends a new message, and the target starts a second run. This RFC
changes Spec 099 so that a retry resends the same ID.

- **The default ID is derived.** It is a UUIDv5 over the attempt ID, the
  step name, the repeat index, the message text, and, inside a `foreach`
  body, the item, so iterations of one body stay apart.
- **`message_id` sets it explicitly.** It is a new optional `with` field of
  `a2a.send`. A workflow that wants one ID across attempts can set, for
  example, `${DAG_RUN_ID}-delegate`.

| Execution | `messageId` | Agent that deduplicates |
| --- | --- | --- |
| Step `retry_policy` | Same | Returns the existing task |
| Next `repeat_policy` iteration | New | Starts a task |
| Dagu retry of the run, which is a new attempt | New | Starts a task |
| Another step with the same message | New | Starts a task |

- A `retry_policy` fires only when the RPC fails. A remote state is data, and
  the step succeeds whatever the state (Spec 099), so a retry never runs
  because the remote task failed. If the agent accepted the first message,
  the response was lost, and the task then failed, the retry resends the
  same ID and a deduplicating agent returns that failed task.
- Running a failed remote task again takes a Dagu retry, which is a new
  attempt and so a new message.
- An agent that does not deduplicate can still run a retried message twice,
  so Spec 099's `Outcome unknown` note stays for such agents.

## Failure behavior

| Case | Answer |
| --- | --- |
| No key, an invalid key, or a key without the `a2a` surface | HTTP 401 |
| Unknown agent, or one the caller may not run | HTTP 404 |
| Card of a retired agent | HTTP 404 |
| New message to a retired agent | `UnsupportedOperationError`, no run created |
| Message shape or params invalid | `InvalidParamsError`, no run created |
| `messageId` reused with other params | `InvalidParamsError` |
| Message continues a task (`taskId`) | `UnsupportedOperationError` |
| Task unknown, removed, or owned by another subject | `TaskNotFoundError` |
| Cancel of a terminal task | `TaskNotCancelableError` |
| Run store or queue unavailable | `InternalError`. A retry with the same `messageId` is safe. |
| Result over `max_output_size` | Completed task without the artifact, with a status message |

## Performance

- **Admission.** `SendMessage` costs what a REST enqueue costs: one DAG read,
  params validation, one run directory, one status write, and one queue item.
  At one run per second this is the REST enqueue load.
- **Reads.** `GetTask` reads the latest attempt's status by run ID. A
  completed task also reads the run's stored DAG for its result. It walks no
  history. Cost scales with how often clients poll, as REST polling does.
- **Blocking calls.** A blocking `SendMessage` holds one connection and one
  goroutine for at most `a2a.max_wait`, reading the status at most a few
  dozen times. It stops at a terminal state, at the limit, or when the
  client disconnects.
- **Cards and catalog.** Each request reads the publishing directory and
  stats its files. Files are parsed only when their mtime changes, and the
  cache holds one entry per file. Cost grows with the number of published
  agents, not the number of DAGs.
- **Storage.** A run started through A2A stores about 150 bytes more:
  `triggerSubject`, `retryDisabled`, and `a2aContextId`. Other runs are
  unchanged.
- **Idle cost.** Nothing runs when no request arrives.
- **Upgrade.** No index version changes and no migration runs, apart from the
  service-account migration in its own RFC.

## Compatibility and rollout

- **Off by default.** With `a2a.enabled: false` and no publishing files,
  behavior is unchanged.
- **DAG `outputs`.** A DAG that sets root `outputs` fails to load on an older
  binary, which rejects unknown root fields (Spec 002).
- **Run status.** An older binary reads runs with the new status fields,
  since they are optional. A downgraded scheduler or frontend ignores
  `retryDisabled` and may retry an A2A run, so a mixed-version scheduler and
  frontend is not supported for A2A.
- **Workers and the coordinator** run A2A runs as ordinary runs and need no
  change. Retry admission happens in the frontend, scheduler, and CLI.
- **Reserved directory.** A user with recursive discovery and a DAG folder
  named `a2a` must move it or set `paths.a2a_dir`. The release notes say so.
- **Keys.** An older binary may not accept a key whose surfaces include
  `a2a`. Downgrading requires removing that surface first.
- **`a2a.send` message IDs.** A step retry resends the same `messageId`
  instead of a new one, so an agent that deduplicates returns the earlier
  task. Repeat iterations and Dagu retries still send new IDs.

Order:

1. Service accounts that own several API keys (separate RFC).
2. DAG-level `outputs` and its validation.
3. `triggerSubject`, `retryDisabled`, and the retry admission rule.
4. The A2A server: settings, publishing files with discovery, git sync, and
   validation, Agent Cards, the JSON-RPC methods, and the `a2a` surface.
5. The AI Catalog.
6. Docs, including a Dagu-to-Dagu example with `a2a.send`.
7. `a2a.send` message IDs, as a change to Spec 099. It depends on none of the
   steps above.

The behavior goes into a new spec. Spec 099's out-of-scope line for hosting
agents then points to it.

## Security

- Every endpoint requires an API key with the `a2a` surface. Production use
  needs HTTPS, which A2A requires (§7.1) and `a2a.send` enforces.
- Tasks are scoped to the caller's subject. Another subject's task answers as
  a missing one.
- Deleting a publishing file revokes the agent and access to its tasks at
  once. Retiring keeps the tasks reachable for their callers.
- Results contain only declared outputs, masked as step outputs already are.
  Failure messages carry no error text.
- `SendMessage` and `CancelTask` write audit entries with the subject, the
  agent, and the run ID.
- Request bodies are limited to 1 MiB.

## Verification and acceptance criteria

- With `a2a.enabled: false`, every A2A path and the catalog answer 404.
- A publishing file makes the card available. The card's interface URL uses
  `public_url`, and its extension carries the parameter schema and the result
  names.
- `dagu validate` rejects a publishing file whose DAG is missing, a second
  file for the same DAG, an unknown field, and a `text_param` the DAG does not
  declare. It rejects a DAG-level `outputs` entry that is not a single step
  output reference, or that names an undeclared output.
- With recursive discovery on, files in `paths.a2a_dir` do not appear as
  DAGs. Git sync pushes and pulls them.
- A data part that matches the schema enqueues one run with `triggerType`
  `a2a`, the caller's `triggerSubject`, and `retryDisabled`. A data part that
  fails the schema returns `InvalidParamsError`, and no run exists.
- A text part reaches `text_param`, and is refused without it.
- The same `messageId` and params, sent twice, including concurrently,
  produce one run and the same task. The same `messageId` with other params
  returns `InvalidParamsError`.
- Task states follow the mapping table for each run status, including
  `waiting` with its status message.
- A completed task returns the declared outputs with JSON values. A skipped
  step's output is absent. A result over `max_output_size` is absent, and the
  status message says so. Editing the DAG after the run does not change the
  result.
- A blocking `SendMessage` of a run longer than `a2a.max_wait` returns a
  non-terminal task within the limit. `GetTask` later returns the final
  state.
- `CancelTask` dequeues a queued run, stops a running one, and refuses a
  terminal one. After a dequeue, `GetTask` returns `TASK_STATE_CANCELED`,
  and the same `messageId` returns that task instead of starting a run.
- Retry is refused for a terminal A2A run from the REST API, the UI, `dagu
  retry`, step retry, and the DAG `retry_policy`. A waiting A2A run can still
  be approved, rejected, and pushed back.
- Another subject's `GetTask` and `CancelTask` return `TaskNotFoundError`. A
  new key of the same service account reads the task. A new key of the same
  subject without access to the run's workspace gets `TaskNotFoundError`.
- The catalog lists only agents the caller may run, and omits a file whose
  DAG is missing.
- A retired agent's card answers 404, and the catalog omits it. A new
  message returns `UnsupportedOperationError` and creates no run. A replayed
  `messageId` returns its task, and `GetTask` and `CancelTask` still work.
  After the file is deleted, every path of the agent answers 404.
- Dagu to Dagu: a DAG with `a2a.send` and `a2a.get_task` calls an agent on a
  second Dagu over HTTPS and reads its artifact.
- A step retry of `a2a.send` after a lost response resends the same
  `messageId`, and the target has one run. A Dagu retry and each repeat
  iteration send a new `messageId`. An explicit `message_id` is sent as
  given.

## Alternatives considered

- **An `a2a:` block in the DAG YAML,** as #4048 proposed. It mixes what a
  workflow does with whether it is exposed. A DAG edit could publish it, and
  the catalog would have to read every DAG.
- **Publishing settings in the data directory,** as JSON managed through the
  API, like webhooks. They could not be reviewed and synced with the DAGs.
  Webhooks live there because they hold tokens, and publishing files hold no
  secrets.
- **A task store mapped to runs.** It is a second record of the same state,
  with its own restart recovery, retention, and drift from the run.
- **The SDK's default request handler.** Its task store must persist every
  update it is given, which needs that second record.
- **Ownership by API key.** A rotated key would lose its tasks.
- **Deciding retries by `triggerType: a2a`.** It ties two separate facts
  together, and a retry overwrites the trigger type of the newest attempt.
- **Starting instead of enqueuing.** A start skips queues, and external
  agents call in bursts and retry.
- **Blocking until the run ends.** Proxies in front of Dagu cut long waits,
  and the client then has no task. Each wait also holds a connection and a
  goroutine for the whole run.
- **An A2A-only output list in the publishing file.** It would reference step
  IDs from outside the DAG, which `dagu validate` cannot check, and it gives a
  run a second definition of its result.

## Open questions

1. **Service accounts.** Their design, the migration of existing keys, and
   whether A2A ships before them.
2. **Public cards.** Whether an operator may serve a public card with only
   the name and security scheme, with details in the extended card.
3. **Catalog identifiers.** The card URL changes when `public_url` changes.
   An `urn:air:` identifier would survive that but needs a configured
   publisher name.
4. **The extension URI** and the shape of its `params`.
5. **`ListTasks`.** Whether a lookup of runs by subject is worth an index.
