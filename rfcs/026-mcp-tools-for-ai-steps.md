# RFC 026: MCP Tools for AI Steps

- **Status:** Proposed
- **Date:** 2026-11-25
- **Affected areas:** `spec` and `ir` (`mcp_servers`), `runtime` and
  `runtime/agentloop` (MCP tools, approval tasks, confirmation delivery),
  `runtime/agent` (run socket), `runtime/builtin` (`harness`, `chat`), the
  `connection` package (MCP gateway), `service/coordinator` and
  `service/worker` (confirmation answers in the run heartbeat),
  `service/frontend` (editor diagnostics), `proto`, `ui`, `specs`,
  `conformance`, and `docs`

## Summary

Give harness agents, agent DAGs, and chat steps the MCP tools of the
connections that RFC 025 defines. A step lists servers in `mcp_servers`, and
a per-step MCP gateway serves their tools, takes tokens from RFC 025's token
broker, and enforces a tool allow-list. Agents never hold a provider token.
Write tools wait for a person by default, and Google's confirmation
requests reach a person in agent DAGs.

From 2027-02-01, Google requires AI agents to reach Workspace data through
its MCP servers. RFC 025 lets `mcp.call` reach them; this RFC lets AI steps
do so. Harness steps ship in RFC 025's first milestone, M1. Agent DAGs and
chat steps follow in M3, once confirmation requests are tested against
Google's servers.

```text
 harness agent ---- loopback MCP ----+
 agent DAG loop --- in process ------+--> MCP gateway, per step --> MCP server
 chat step tools -- in process ------+    allow-list, pinned tools,
                                          approvals, confirmations
                                                    |
                                    Token(conn, resource), RFC 025 broker
```

## Problem statement

- **AI steps cannot use MCP tools with arguments.** Agent DAGs offer steps as
  tools, and "only a step that launches a child DAG accepts arguments"
  (`specs/032-agent-dag.md:153`). Chat steps accept only child DAGs as tools.
  The ACP harness sends an empty MCP server list
  (`internal/runtime/builtin/harness/acp.go:243`).
- **Google requires MCP for agents.** From 2027-02-01, AI agents must reach
  Workspace data through Google's MCP servers, with an OAuth client marked
  for AI agents.
- **An agent that reads email can be steered by it.** Search results and
  documents carry the content that can turn an agent against its user, so a
  write tool needs a check that does not depend on what the agent read.

## Goals

1. Harness, agent DAG, and chat steps use a connection's MCP tools by
   naming it in `mcp_servers`.
2. AI steps meet Google's MCP requirement without extra configuration.
3. No agent holds a provider token, and no provider token or gateway token
   appears in an agent process's arguments.
4. A write tool waits for a person unless the DAG author exempts it by name.
5. DAGs without `mcp_servers` behave as before.

## Non-goals

- **MCP sampling and URL-mode elicitation.** As in RFC 025, Dagu advertises
  form elicitation only.
- **MCP servers for harness steps in a container or on a remote ACP agent.**
  Such a step with `mcp_servers` is refused. [Future
  enhancements](#future-enhancements) describes a socket bridge.

## Guarantees

- An AI step never calls a write tool without a person's approval, unless
  the DAG lists that tool in `unattended`.
- Dagu never answers a server's confirmation request on its own, even for a
  tool in `unattended`.
- An agent never holds a provider token. The gateway token lasts only as
  long as its step, and never appears in process arguments.

## Design

### `mcp_servers`

`mcp_servers` gives an AI step tools from connections:

```yaml
mcp_servers:
  - work-google/calendar            # preset of a google connection
  - notion                          # an mcp connection
  - connection: work-google
    server: gmail
    tools: [search_threads, get_thread, create_draft, label_thread]
    unattended: [create_draft]      # label_thread still waits for a person
```

- A name resolves to the project's own connection, as RFC 025's Ownership
  describes.
- `tools` is an allow-list. When it is left out, every tool listed when the
  run starts is offered.
- **Tool names are checked against the catalog.** Tools are listed when a
  run starts, not when a DAG is loaded, so validation stays offline. The
  editor checks the names in `tools` and `unattended` against RFC 025's
  tool catalog, which needs no network access. When the step starts, a name
  the catalog lacks fails the step, naming the tool, so a misspelled tool is
  never dropped silently.
- **Write tools wait for a person by default.** Every tool that is not
  read-only needs a person's approval before each call. `unattended` names
  write tools that run without one. Read-only tools always run without one.
- **Read-only, by server.** On the built-in presets, a tool is read-only when
  its MCP annotation says `readOnlyHint: true`. Those servers are run by the
  provider itself, so Dagu trusts their annotations. The MCP spec calls
  annotations from other servers untrusted, so on an `mcp` connection every
  tool counts as a write tool unless it is listed in `unattended`.
- **When a preset has no annotation.** MCP annotations are optional. For a
  Google tool without `readOnlyHint`, Dagu uses a list of read-only tools
  for each preset, compiled into the binary. A tool on neither counts as a
  write tool, so a tool Google adds later waits for a person until the list
  names it.
- **Why tool kind, not content.** The rule depends on what a tool does, not on
  what the agent read. Dagu cannot tell which tool output is untrusted:
  `search_threads` returns email bodies, which is exactly the content that can
  steer an agent. A write that stays inside the account, such as
  `create_draft`, is a good candidate for `unattended`, since a person still
  sends the draft.
- **Google agent flag.** A harness, agent DAG, or chat step that uses a
  Google connection needs an app with `agent` set. Without it the run fails
  at admission. A service account connection needs no flag.

### Confirmation requests

RFC 025 has `mcp.call` decline Google's confirmation requests, and Try it
show them to the person present. Dagu advertises form elicitation, so
Google always sends the request and never skips its check, as RFC 025
describes. AI steps handle them by the kind of step:

- **Agent DAGs.** The call stays open while a confirmation task shows the
  server's message and form. The run keeps its process and worker slot while
  it waits, because the request is part of a call in flight. The person's
  answer, accept with the form's values, decline, or cancel, goes back to
  the server. By default the call waits as long as Google keeps the request
  open, which RFC 025's tests measure. `llm.confirmation_timeout` shortens
  the wait, and `0` declines at once, for a scheduled agent that nobody
  watches. This needs Google to keep the request open while a person
  answers. If RFC 025's tests against Google show it does not, agent DAGs
  decline confirmation requests, as harness steps do.
- **Delivering the answer.** A human task today is answered while its run
  has no process. A confirmation needs a path to a live process, and reuses
  the ones that stop a run:
  - a local run receives the answer on its run socket, beside `/stop`
    (`internal/runtime/agent/agent.go:2314`);
  - a run on a worker receives it in the `RunHeartbeat` response, as it
    receives cancellations (`proto/coordinator/v1/coordinator.proto:318`).

  Where the run socket is unavailable, Dagu declines at once.
- **Harness and chat steps.** Dagu declines at once. The agent receives a
  tool error that names the rule and the ways to let the action through
  that RFC 025 lists for `mcp.call`. Holding the call open for a person is
  a future enhancement.

The answer's path:

```text
 MCP server --form--> gateway, in the run --> confirmation task in the UI
                                                         |
                                        person answers --+--> server
 local run:    server --> run socket, beside /stop --> run process
 on a worker:  server --> coordinator --> RunHeartbeat response --> worker run
 run process --answer--> MCP server, and the call in flight goes on
```

Dagu never accepts a request on its own, even for a tool in `unattended`.
The request is the provider's own check that a person is present, and
`unattended` waives only Dagu's.

### MCP gateway

Each AI step that has `mcp_servers` gets its own MCP gateway: the tool
allow-list, pinned tools, approvals, and confirmation handling, in front of
an MCP client. Agent DAGs and chat steps run inside Dagu and call the
gateway in process. A harness step's agent is a separate process, so for it
the gateway also serves MCP on a loopback listener, built on go-sdk's server
as `internal/service/mcp/` is. It is not a byte-level proxy, so sessions,
server-sent events, and protocol versions work as each side expects.

```text
 run process, local or on a worker
 +---------------------------------------------------------------------+
 |  harness step                      agent DAG or chat step           |
 |  agent process: Claude Code,       agent loop or tool loop          |
 |  Codex, or an ACP agent                  |                          |
 |      | loopback MCP, step's bearer       | in process               |
 |      v                                   v                          |
 |  +---------------------------------------------------------------+  |
 |  | MCP gateway, one per step                                     |  |
 |  | allow-list, pinned hashes, approvals, confirmations,          |  |
 |  | Retry-After hold                                              |  |
 |  +---------------------------------------------------------------+  |
 |      |                     |                          |             |
 |  tool catalog          Token(conn, resource)      MCP calls         |
 |  file, or coordinator  broker, or coordinator         |             |
 |  RPC on a worker       RPC on a worker                |             |
 +-------------------------------------------------------+-------------+
                                                         v
                                        MCP server: Gmail, Calendar, ...
```

- The listener binds `127.0.0.1` on a random port. It accepts only a random
  bearer token created for the step, and rejects a request whose `Host` or
  `Origin` is not a loopback address, which stops DNS rebinding.
- Upstream calls take tokens from the broker. Long agent turns therefore
  outlive any single access token, and the agent never holds a provider
  token.
- When the step starts, it reads RFC 025's tool catalog, not the upstream,
  and pins each allowed tool's name and the hash of its description, input
  schema, and annotations. A tool the upstream adds later is not offered.
  The gateway checks for a newer catalog at most once a minute, and a tool
  whose hash differs there fails its calls until the next run. This limits
  tool poisoning by a changed upstream, and keeps a tool from becoming
  read-only mid-run. An agent DAG saves the hashes with its catalog, so a
  run that resumes after a human task keeps them.
- When the upstream answers HTTP 429, the call fails with a tool error that
  carries `Retry-After`, so the agent can wait. The gateway also holds
  further calls to that server until `Retry-After` passes, so an agent that
  retries at once does not reach the provider again.
- Each tool call is written to the step log with its arguments masked.
- The gateway closes when the step ends.

### Harness steps

```yaml
- action: harness.run
  with:
    provider: claude
    prompt: Prepare notes for each of today's meetings.
    mcp_servers: [work-google/calendar, work-google/drive]
```

Three adapters point an agent at the gateway:

- **Claude Code:** a temporary `--mcp-config` file with mode `0600`, deleted
  after the step.
- **Codex:** `mcp_servers` config overrides, with the bearer token read from
  an environment variable through `bearer_token_env_var`. The overrides are
  process arguments (`codex exec -c`), so the token stays out of them.
- **ACP agents:** `McpServerHttpInline` entries in `session/new`, sent when
  the agent advertises HTTP MCP support. These replace the empty list at
  `acp.go:243`.

Other built-in providers, and custom ones, refuse `mcp_servers` until they
gain an adapter, as do container and remote ACP steps. Today a harness
option given inline lands in the process arguments (`configToFlags`,
`harness.go:965`), so a token passed that way would be visible in `ps`. No
adapter puts the gateway token in process arguments, because any local user
could read it there and call the gateway while the step runs. The gateway
token is the only credential the agent sees, and it lasts only as long as
the step.

Each adapter also pre-approves the gateway's tools, and only those, since
the gateway already enforces the allow-list and approvals. Without this the
agent's own permission check would block every gateway call:

- `claude -p` runs with no permission flags today
  (`internal/runtime/builtin/harness/provider.go:70`), and print mode denies
  an MCP tool that no rule allows. The adapter passes `--allowedTools` for
  the gateway server's tools.
- An ACP agent's permission request is denied unless `acp_permission:
  allow`, which accepts every tool, shell included (`acp.go:358`). A request
  for a gateway tool is accepted whatever `acp_permission` says. Other
  requests keep the policy. The adapter tells a gateway tool by the MCP
  server name in the request, so it is enabled only for ACP agents
  verified to send that name. Other ACP agents refuse `mcp_servers`, naming
  the reason, as providers without an adapter do.
- Codex gets the same pre-approval through its config overrides, where it
  asks for one.

The agent's other tools keep the step's permission settings.

A harness step does not wait for a person in this RFC, as [Future
enhancements](#future-enhancements) notes. So in a harness step, the
gateway refuses a call to a write tool that is not in `unattended`. The
agent receives a tool error that names the rule, and the step log records
the refused call. Such a step can still succeed, so the refusals are made
visible:

- The editor warns when a harness or chat step's `mcp_servers` offers a
  write tool that is not in `unattended`, since the step can never call it.
  The warning reads the tool catalog and its annotations.
- The step records how many calls the gateway refused, and the run page
  shows the count beside the step's status.

### Agent DAGs and chat steps

```yaml
type: agent
mcp_servers:
  - connection: work-google
    server: gmail
    tools: [search_threads, get_thread, create_draft, label_thread]
    unattended: [create_draft]
steps:
  - name: escalate
    action: dag.run
    with: { dag: open-incident }
tasks:
  - name: triaged
    description: Every unread support email has a draft reply or an incident.
```

- When the run starts, the agent loop lists each server's tools through the
  gateway. Each MCP tool becomes one function tool named
  `<connection>__<server>__<tool>`, or `<connection>__<tool>` for an `mcp`
  connection, so two accounts with the same service never collide. The name
  is sanitized and kept within the 64-character limit like step tool names,
  and the tool's input schema becomes its parameters. The catalog is saved
  with the `__agent__` node, as Spec 032 already requires, so a run's tools
  stay fixed while it runs.
- A call runs through the gateway, inside the agent loop. The result is
  added to the transcript under the observation limits that apply to step
  results.
- A write tool that is not in `unattended` opens an approval task before
  the call. It is a synthesized human task like `ask_user`, but `ask_user`'s
  rules do not apply to it (`specs/032-agent-dag.md:169`). It does not count
  toward the five-question limit.
  - The task shows the tool, its arguments, and any text the agent wrote
    with the call, so a person can judge why the agent wants it.
  - The person approves once, approves the tool for the rest of the run,
    approves it for the DAG for up to 24 hours, or rejects. Each wider
    approval covers that tool only. Otherwise an earlier decision never
    answers a later call, even an identical one. Without these, an agent
    that labels 30 emails, or 100 runs that start together, would wait for
    one approval per call, and authors would list every tool in
    `unattended` to avoid them.
  - Approving for the rest of the run is recorded on the run, and ends with
    it.
  - Approving for the DAG is offered only to a person who may edit the DAG,
    since that person could list the tool in `unattended` anyway. It covers
    every run of the DAG until it expires, and answers the tasks already
    waiting for that tool. It is audited, and shown on the DAG page, where
    it can be revoked.
  - The run waits without a process, and the existing `dag.run.waiting`
    event tells someone.
  - The run continues once a person decides. A rejection goes back to the
    model as the tool result.
- A child run cannot open a human task, as Spec 032 rules for `ask_user`. In
  a child agent DAG, a write tool that is not in `unattended` is refused, as
  in harness steps.
- Step `approval` cannot serve here. It waits after the step runs
  (`internal/ir/step.go:132`), and it keeps a DAG off workers
  (`internal/ir/dag.go:489`).
- Chat steps take the same field under `llm.mcp_servers`. The tool registry
  in `internal/runtime/builtin/chat/tools.go` gains MCP tools beside child
  DAGs. A chat step does not wait for a person either, so it refuses write
  tools that are not in `unattended`, as harness steps do.
- The integration points are `NewCatalog`
  (`internal/runtime/agentloop/catalog.go:43`), the planner's decision
  mapping, and `prepareAgentActionNode`
  (`internal/runtime/agent_batch.go:123`).

### Google's agent policy

- An agent DAG gets a lint warning when its catalog includes a step that
  calls Google REST APIs through a connection, such as `http.request` or a
  mail action. The warning cites Google's 2027-02-01 requirement that AI
  agents use MCP.
- The editor completes `mcp_servers` with agent connections, as RFC 025's
  completion does for `mcp.call` with a Google preset.

### Runs

- `mcp_servers` adds to the connections a DAG declares (RFC 025, Runs), so
  a worker gets their tokens from its coordinator.
- A worker has no tool catalog of its own. A new coordinator RPC,
  `ResolveConnectionTools`, returns the catalog for a connection and
  service, with the authorization `ResolveConnectionToken` uses. The
  coordinator reads or refreshes the catalog as any reader does.
- A token the gateway mints during a step is masked, as RFC 025 masks any
  token minted mid-step.
- RFC 025's starter templates gain an agent that prepares notes for today's
  Calendar meetings from Drive.

## Security

- **Agents.** Agents never hold provider tokens. The gateway accepts only
  its step's bearer token, offers only allowed tools, and refuses calls to
  tools whose description, schema, or annotations changed.
- **Prompt injection.** An agent that reads email or documents can be steered
  by their content. So write tools wait for a person by default, and only
  the DAG author can exempt a tool, by name, in `unattended`. Annotations from
  servers other than the built-in presets are not trusted. Dagu never accepts
  a provider's confirmation request on its own.

## Failure behavior

| Event | Behavior |
| --- | --- |
| A server sends a confirmation request | An agent DAG shows it to a person and waits up to the timeout. Harness and chat steps decline it, and the agent receives a tool error |
| An upstream MCP tool's description changes during a run | Calls to that tool fail until the next run, which records the new description |
| A harness step in a container, on a remote ACP agent, or on a provider or ACP agent without an adapter sets `mcp_servers` | Validation fails, naming the limit |
| A name in `tools` or `unattended` is not in the tool catalog | The editor reports it. The step fails when it starts, naming the tool |
| A DAG-wide approval expires or is revoked | Later calls to the tool wait for a person again |
| A harness or chat step calls a write tool that is not in `unattended` | The gateway refuses the call. The model receives a tool error that names the rule, and the step continues. The run page shows the refused count |
| A harness agent's own permission policy would deny a gateway tool | The adapter's pre-approval lets the call through. The gateway's rules still apply |
| Nobody answers a write tool's approval in an agent DAG | The run stays waiting, as for any human task |
| The provider answers HTTP 429 to a gateway call | The agent receives a tool error with `Retry-After`, and the gateway holds that server's calls until it passes |
| Many agent DAG runs wait on confirmation requests at once | Each holds its worker slot up to the timeout, and runs queued behind them wait. A queue's `max_concurrency` caps how many do |

## Performance

- **Idle cost.** None. A gateway lives only as long as its step, and
  nothing runs between steps.
- **Tool lists.** AI steps read RFC 025's tool catalog, so a service is
  listed at most once per 10 minutes per host, however many runs start. At
  one run per second, listing per run would send about 170,000 `tools/list`
  calls a day for two services, against a quota Google has not published.
  Each running gateway checks the catalog file for a newer version once a
  minute, a single file stat. On a worker, the catalog costs one
  coordinator RPC per service when the step starts.
- **Gateway.** One loopback listener per harness step with `mcp_servers`,
  tied to the step's context. It adds one local hop per tool call. Agent
  DAGs and chat steps add no listener.
- **Confirmation waits.** An agent DAG run that waits on a confirmation
  request holds its process and worker slot, for at most Google's hold
  time, or `llm.confirmation_timeout` when it is shorter. Approval tasks
  before a call release both.
  The answer arrives on the run socket or in the existing run heartbeat, so
  the wait adds no polling. Many such waits at once can fill every worker
  slot for up to the timeout, and runs queued behind them wait. A queue's
  `max_concurrency` bounds how many runs of a DAG can hold slots this way,
  and `llm.confirmation_timeout: 0` stops a scheduled agent from waiting.
- **DAG-wide approvals.** One small record per approved tool in the DAG's
  state, read when a write tool that needs approval is called.
- **Storage.** An agent DAG run with `mcp_servers` saves its MCP tools in the
  `__agent__` catalog: names, descriptions, input schemas, and hashes,
  roughly 1 to 2 KB per tool, so 10 to 40 KB for two Google servers. A
  `tools` allow-list shrinks it. A harness or chat step adds its refused
  count to its node status, a few bytes. Nothing is added to indexes.
- **Upgrade.** No migration and no index rebuild.

## Compatibility and rollout

- **Additive.** A DAG without `mcp_servers` behaves as before.
- **Older binaries.** A DAG with `mcp_servers` fails validation on an older
  binary, which rejects unknown keys there.
- **Version skew in distributed mode.** The coordinator dispatches a DAG
  with `mcp_servers` only to workers new enough for this RFC, as RFC 025
  gates DAGs that declare connections.
- **Specs.** 032 (agent DAGs), 059 (chat), and 067 (harness) gain
  `mcp_servers`.

Rollout follows RFC 025's milestones, in pull requests that each work on
their own.

1. **Gateway and harness steps, in M1.** Claude Code first, then Codex and
   ACP adapters, with pre-approved gateway tools, `unattended`, pinned
   tools, the compiled read-only lists, and declining confirmation requests.
   Write tools not listed in `unattended` are refused, with the editor
   warning and the refused count. Declining confirmation requests needs no
   Google test to be safe.
2. **Agent DAGs and chat steps, in M3,** after RFC 025's tests against
   Google. MCP tools, approval tasks before write tools with approval for
   the rest of a run or for a DAG, confirmation tasks with their delivery
   over the run socket and run heartbeat, and the Google policy lint.

## Verification and acceptance criteria

1. A harness step with `mcp_servers` completes a turn longer than the
   access token's lifetime. The agent process's arguments contain neither
   a provider token nor the gateway token, and its environment contains no
   provider token.
2. A harness step with the step's default permission settings calls a
   gateway tool through Claude Code, Codex, and an ACP agent. Each ACP
   agent the adapter supports names the gateway server in its permission
   requests, and its other tools keep `acp_permission`.
3. An agent DAG calls an MCP tool with arguments. A write tool not in
   `unattended` waits for a person before each call, including after five
   such calls, and runs only after approval. Once a person approves it for
   the rest of the run, later calls to it run without a task, and other
   write tools still wait. Once a person who may edit the DAG approves it
   for the DAG, every run of the DAG calls it without a task until the
   approval expires or is revoked, and tasks already waiting for it are
   answered. A harness step's or a child agent DAG's call to such a tool is
   refused.
4. A Gmail read-only tool runs unattended. Every tool of a generic `mcp`
   connection waits for a person unless `unattended` lists it.
5. A Google tool without `readOnlyHint` runs unattended only when the
   compiled list names it read-only. Any other tool waits for a person.
6. 100 agent DAG runs that start together cause one `tools/list` per
   service, locally and on workers.
7. An agent that retries a call at once after HTTP 429 does not reach the
   provider again before `Retry-After` passes.
8. A Google tool that sends a confirmation request waits for a person in
   an agent DAG, and the answer reaches the waiting call both in a local run
   and on a worker. Harness and chat steps decline it. No path accepts a
   request without a person.
9. A name in `tools` or `unattended` that the tool catalog lacks fails the
   step when it starts, naming the tool.
10. Every case in [Failure behavior](#failure-behavior) ends as stated.

## Alternatives considered

- **Giving agents the provider token in their MCP config.** Simpler than a
  gateway. But the token sits in the agent's files or arguments, expires
  during long turns, and allows every tool.
- **A byte-level reverse proxy as the gateway.** Less code, but it cannot
  enforce a tool allow-list, pin tool descriptions, or audit calls without
  parsing MCP anyway.
- **Opt-in confirmation (`confirm: [tool]`).** The safe setting would depend
  on the author remembering it, and a scheduled agent that reads email would
  run every write unattended by default.
- **Requiring confirmation only when the agent reads untrusted content.**
  Dagu cannot tell which tool output is untrusted. Read tools return the
  email bodies and documents that carry injected instructions, so the rule
  could not be enforced.
- **Reusing `ask_user` for approvals.** No new task. But its five-question
  limit stalls an agent that writes more than five times, and its repeat
  rule would approve an identical second write without asking.
- **Approving each call only.** Simpler. But an agent that writes 30 times
  waits for 30 approvals in turn, which pushes authors to list every tool in
  `unattended` and lose the default.
- **Accepting confirmation requests for tools in `unattended`, or when the
  DAG author opts in.** Harness and chat steps could then use Google's
  sensitive tools. But the request is Google's own check that a person is
  present, under rules the Workspace admin sets. Answering it without one
  defeats that check, and leaves a person's approval in Google's audit log
  that nobody gave.
- **Google's `/mcp/v1self` endpoint for steps without a person.** It offers
  fewer tools, may leave content out of results, and gives a person no way
  to approve or save an approval.

## Future enhancements

- **Waiting for a person in harness, chat, and `mcp.call` steps.** The step
  would hold the call open, notify on the channels of `dag.run.waiting`,
  and wait as agent DAGs do, for Google's confirmation requests and for
  Dagu's approval of write tools. The process and worker slot stay held
  while it waits.
- **Harness steps in containers.** The step would bind-mount a Unix socket
  for the gateway and run a stdio bridge inside the container. It would also
  work with `network: none`.
- **A concurrency cap per connection.** The gateway would limit calls to
  one account across all runs on a host. It needs a semaphore shared by
  processes, so this RFC relies on queues instead.
- **Approval timeouts.** A write tool's approval would be rejected after a
  set time. Human tasks have no timeout today, and a run that waits without
  a process needs a scheduler check to enforce one.

## References

- RFC 025: Connections (`rfcs/025-connections.md`)
- Spec 032: Agent DAGs (`specs/032-agent-dag.md`)
- Configure the Google Workspace MCP servers:
  https://developers.google.com/workspace/guides/configure-mcp-servers
- MCP specification: https://modelcontextprotocol.io/specification/latest
