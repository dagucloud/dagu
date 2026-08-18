# Spec: Agent DAGs

## Status

Implemented.

## Scope

This spec defines:

- the `type: agent` DAG shape
- the `tasks` root field and its role as the termination condition
- how declared steps become a catalog of actions offered to a model
- the decision loop, one action per turn
- the `set_task_status` and `ask_user` tools
- failure handling and action repetition
- suspension for human input and resumption
- terminal status derivation

This spec does not define:

- provider-specific model behavior or prompt engineering beyond the framing
  the agent supplies
- `action: human.task` semantics, which are defined in `031-human-task.md`
- REST, Web UI, MCP, notification, authentication, or authorization behavior
- concurrent action execution

## Goal

A workflow author declares *what a run must achieve* rather than *the order in
which steps run*. Steps become capabilities; `tasks` become goals. A model
selects one action per turn, observes its outcome, and marks tasks complete
until every goal is satisfied, at which point the run concludes.

Because the agent drives the existing node machinery rather than an
in-process tool loop, an action may open a human task: the run releases its
process and worker slot, and the agent resumes with its conversation and
goal progress intact.

## Related Specs

- `002-yaml-schema.md` — root fields
- `031-human-task.md` — the waiting checkpoint an action may open

## DAG shape

```yaml
type: agent

llm:
  provider: anthropic
  model: claude-opus-5
  system: |
    Optional framing prepended to the agent's own instructions.

steps:
  - name: design
    action: dag.run
    with: { dag: design }

  - id: review
    name: review
    action: human.task
    with:
      prompt: Approve the design?

tasks:
  - name: designed
    description: Finished when the design workflow ran and a person approved it.
```

### Root fields

Agent DAGs were previously called controller DAGs. `type: controller` remains
accepted as a deprecated alias: it is canonicalized to `agent` before validation,
so every rule stated for `type: agent` applies to it unchanged. Loading a DAG
that uses it reports a deprecation warning naming the replacement.

`tasks` is an array of objects with `name` and `description`. It is valid only
when `type` is `agent`, and `type: agent` requires it.

- `name` MUST be non-empty and unique within the DAG.
- `description` MUST be non-empty. It states the completion criteria the
  agent decides against, so an empty description is a specification error
  rather than a stylistic one.

`llm` MUST be present. Its `system` value, when set, is prepended to the
agent's own framing rather than replacing it.

`llm.model` MAY be an ordered array of model entries. The first entry is the
primary model and each later entry is a fallback. Provider, model name, base
URL, API-key name, and sampling overrides are taken from the selected entry in
the same way as for a chat step.

`llm.system` and every task `description` are author-written prompt text and
MUST be resolved against the run's variables before the agent sees them, so
a workflow can be steered by its parameters without editing the DAG. The
resolved description is what gets persisted, since it is what the agent
judged against.

`llm.max_tool_iterations` bounds the number of decisions in a single run. When
unset the bound is 50.

`llm.observation_max_bytes` bounds each tool result added to the agent's
conversation. It defaults to 524288 bytes (512 KiB). The limit applies only to
the agent-facing copy: the step's output, logs, and human-task submission
stay complete in their owning records. A truncated result uses an explicit
marker when the configured limit can hold it and MUST remain valid UTF-8. A
value of zero disables this size limit.

`llm.max_context_tokens` is the prompt-token threshold for observation aging and
defaults to 200000. Dagu does not infer it from the model. An author SHOULD
override the default when needed to leave headroom below the provider's hard
context limit. Once aging starts, `llm.observation_keep_recent` controls how many
recent tool results remain complete; it defaults to 20. A zero
`max_context_tokens` disables proactive aging. A zero
`observation_keep_recent` disables aging entirely, including overflow recovery.
These context-management fields are valid only in an agent DAG's root
`llm` configuration and MUST NOT be set on an individual step.

### Step constraints

An agent DAG MUST declare at least one step. For every declared step:

- `depends` MUST NOT be set, and a step MUST NOT be explicitly marked as having
  no dependencies. Ordering belongs to the agent.
- `router` MUST NOT be set.
- The name `__agent__` is reserved and MUST NOT be used as a step name
  or ID.

Every declared step is implicitly failure-tolerant: a failed action never aborts
the run, because the failure is an observation the agent acts on.

### The agent step

Building an agent DAG appends a synthesized step named `__agent__`
carrying the DAG's `llm` configuration. It is the node the runner drives the
loop from, and it holds the conversation transcript, the tool catalog that was
offered, and the persisted goal progress. It is not an action the agent may
select.

## The action catalog

Each declared step is advertised to the model as one function-calling tool.

- The tool name derives from the step `id`, or the step `name` when no `id` is
  set. Characters outside `[A-Za-z0-9_-]` are replaced with `_`, the result is
  truncated to 64 characters, and collisions are disambiguated with a numeric
  suffix.
- The tool description is the step `description`; failing that, the human task
  prompt for a human task, the target workflow's description for `dag.run`, and
  otherwise a generated sentence naming the step.
- Only a step that launches a child DAG accepts arguments. Its schema is derived
  from the target's parameter definitions, falling back to its default-params
  string. Every other step is a nullary action.
- A parameter the step supplies a value for MUST NOT appear in that schema. A
  value written in the workflow is the author's decision, not one the agent
  restates, and a step that supplies every parameter is a nullary action.
- Parameters supplied by a child-DAG step MUST use named form. Positional
  parameters are rejected because tool arguments are identified by name.

The parameters a child DAG run receives are the ones the step supplies, plus an
argument for each parameter the step left open. An argument naming a parameter
the step supplies MUST be discarded rather than override it: the model was never
offered that choice.

Two additional tools are always offered.

`ask_user` puts a question to a person. An agent DAG is built with a
synthesized human task, named and identified `ask_user`, which the tool opens
with the question the agent wrote. Answering it is an ordinary human task
completion, and the reply returns as the next observation.

That task MUST NOT count as a declared human task when deciding whether the DAG
may run as somebody's child, or every agent would be barred from
composition. Instead the agent declines to ask when it is not the root run.

A question already answered MUST NOT be put to a person again: the answers so
far are restated to the agent each turn, an exact repeat is refused with the
prior answer, and a run may ask at most 5 questions.

`set_task_status` is always offered. It takes a `task`
name, a `status`, and a `reason`. It is reserved: no step tool may take that
name.

## The decision loop

Each turn:

1. The agent is sent a system message stating its role, the full task list
   with per-task completion status, and the operating rules, followed by the
   conversation so far.
2. The model replies. If it requests no tool call, see *Stalling* below.
3. Exactly one action is carried out per turn. When a reply contains several
   tool calls only the first is recorded and executed, so the conversation never
   references a result that was not produced.
4. The outcome is appended as the tool result: the resulting status, any error,
   and any human task submission.

   For a step that launched a child DAG, the rest of the observation is read
   from the child run itself: its status, the output variables its steps
   declared, and the name and error of any step that failed. The parent step's
   log MUST NOT be used as the source, because it only mirrors the child's
   status document, repeated once per internal retry, and is empty on a repeated
   run.

   For every other step, a bounded tail of stdout and stderr is reported.

   The complete tool result is then limited by `llm.observation_max_bytes`.
   Human answers repeated in the agent's system message use the same
   agent-facing limit.

The loop ends when no task is open, when an action opens a human task, or when a
limit is reached.

### Model fallback

When `llm.model` is an array, every decision starts with the currently selected
model. If its request still fails after the provider and logical retry budgets
are exhausted, the agent advances through the remaining entries in order.
A fallback that succeeds remains selected for later turns in the same agent
process, so a sustained primary outage is not retried on every decision. A new
process after suspension starts from the configured primary again.

A failed model request MUST NOT consume an agent turn or append an assistant
message. The next model receives the existing conversation unchanged, including
assistant tool calls and tool results produced through earlier models. Every
successful assistant message records the provider and model that produced it.

Context-length recovery runs against the current model before advancing to a
fallback. If all configured models fail, the run fails with an error identifying
the exhausted models and preserving their underlying errors.

### Observation aging and context recovery

Every successful decision records the provider-reported prompt token count. If
that count reaches `llm.max_context_tokens`, observation aging starts before the
next decision and remains active for the rest of the run.

While aging is active, the newest `llm.observation_keep_recent` tool results
remain complete. Every older tool result is replaced by a deterministic
one-line summary derived from the matching decision-timeline event. The
assistant tool call, tool result role, and tool-call ID MUST remain in the
conversation so provider tool-call protocols remain valid. Compacted results
are persisted and MUST NOT expand again after suspension or retry.

If observation aging is enabled and a provider rejects a decision because its
context is too long, Dagu enables aging immediately and replaces every tool
result whose deterministic summary is smaller, including results normally
protected by `llm.observation_keep_recent`. It retries the decision once only
when that compaction changed the transcript. The rejected request does not
consume an agent turn or add an assistant message. If nothing can be made
smaller, or if the rebuilt request also fails, that model attempt fails and
ordinary model fallback applies. No further overflow retries are made for that
decision. When aging is disabled, a context-too-long response advances to the
next configured model without a recovery retry, or fails the run when no
fallback remains.

### Task status

Every task starts `open`. The agent settles it with `set_task_status`:

| Status | Meaning | Effect on the run |
|---|---|---|
| `completed` | The task's criteria are satisfied. | — |
| `skipped` | The task turned out to be unnecessary. | None: the run still succeeds. |
| `failed` | The task cannot be achieved. | The run fails. |
| `open` | Undo an earlier decision that later work invalidated. | Returns the task to the loop. |

`skipped` and `failed` MUST remain distinct: waiving a goal that never needed
doing is not the same outcome as failing to reach one.

Naming an unknown task, restating the status a task already holds, or passing a
status outside this set is reported back as a tool error and the loop continues;
none of these fail the run. The same applies to a call naming a tool that does
not exist, or arguments that cannot be decoded.

### Failure

A failed action is reported to the agent, which may retry it, choose a
different action, or stop. The failure is an observation, not a run-level error:
it MUST NOT by itself cause the run to report an error to its caller.

Final status follows the steps' end state. A failed action that was re-run
successfully leaves the run `succeeded`; an action left failed while every task
completed leaves it `partially succeeded`.

### Repetition

An action may be selected again after it has already run, which resets the node
and marks it repeated so a child DAG run receives a fresh run ID. A single
action may run at most 5 times per DAG run; beyond that, the request is refused
as a tool error and the agent must choose differently.

### Stalling

If the model replies without calling a tool while tasks remain open, it is
reminded once which tasks are outstanding. A second consecutive reply without a
tool call fails the run. Any turn that does use a tool clears the count, so
occasional silence between real work is not fatal.

### Limits

Reaching the turn limit with tasks still open fails the run, and the error names
the outstanding tasks. A task the agent cannot achieve should be settled as
`failed` rather than left open to exhaust the limit.

## Suspension and resumption

When a chosen action ends in the `waiting` status — an `action: human.task`
step, or a child DAG that is itself waiting — the agent records the
in-flight tool call, persists its state, and returns. The agent step itself
MUST NOT be left waiting, since an outstanding waiting step would prevent the
run from being released when the human task completes.

The run then reports `waiting`, the `onWait` handler runs, and the process
exits.

Completing the human task marks that step succeeded and re-queues the same DAG
run. On the next attempt the agent restores its transcript and goal
progress from the agent step, reports the outcome of the in-flight action
as that turn's tool result, and continues.

Restored state is reconciled against the current DAG: progress on a task that
still exists is preserved, a task that has been removed does not linger, and a
newly declared task starts open.

## Decision timeline

An agent run records an ordered timeline of its decisions, persisted
alongside goal progress and restored on resume. Each entry carries the turn it
belongs to and one of these kinds: `action`, `task_status`, `ask_user`,
`rejected`, `stalled`. An `action` entry additionally carries the resulting
status, which attempt of that step it was, and the start and finish times.

The timeline exists because an agent has no dependency edges: execution
order is a property of the run, not of the DAG, and cannot be recovered from the
step list.

## Variable scope

An agent DAG has no dependency edges. Every action that has already
finished is treated as upstream of the action starting now, so its outputs are
in scope.

## Terminal status

- No task open and none failed, no action failed → `succeeded`. Skipped tasks do
  not change this.
- No task open and none failed, at least one action left failed →
  `partially succeeded`.
- Any task settled as `failed` → `failed`, and the error names those tasks with
  the reasons given.
- An action is waiting → `waiting`.
- Turn limit reached with open tasks, a second consecutive reply without a tool
  call, or an unrecoverable agent error → `failed`.

Steps the agent never selected are marked `skipped` when the run reaches a
terminal state.
