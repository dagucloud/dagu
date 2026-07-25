# Spec: Controller DAGs

## Status

Implemented.

## Scope

This spec defines:

- the `type: controller` DAG shape
- the `tasks` root field and its role as the termination condition
- how declared steps become a catalog of actions offered to a model
- the decision loop, one action per turn
- the `complete_task` tool
- failure handling and action repetition
- suspension for human input and resumption
- terminal status derivation

This spec does not define:

- provider selection, model behavior, or prompt engineering beyond the framing
  the controller supplies
- `action: human.task` semantics, which are defined in `031-human-task.md`
- REST, Web UI, MCP, notification, authentication, or authorization behavior
- concurrent action execution

## Goal

A workflow author declares *what a run must achieve* rather than *the order in
which steps run*. Steps become capabilities; `tasks` become goals. A model
selects one action per turn, observes its outcome, and marks tasks complete
until every goal is satisfied, at which point the run concludes.

Because the controller drives the existing node machinery rather than an
in-process tool loop, an action may open a human task: the run releases its
process and worker slot, and the controller resumes with its conversation and
goal progress intact.

## Related Specs

- `002-yaml-schema.md` — root fields
- `031-human-task.md` — the waiting checkpoint an action may open

## DAG shape

```yaml
type: controller

llm:
  provider: anthropic
  model: claude-opus-5
  system: |
    Optional framing prepended to the controller's own instructions.

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

`tasks` is an array of objects with `name` and `description`. It is valid only
when `type` is `controller`, and `type: controller` requires it.

- `name` MUST be non-empty and unique within the DAG.
- `description` MUST be non-empty. It states the completion criteria the
  controller decides against, so an empty description is a specification error
  rather than a stylistic one.

`llm` MUST be present. Its `system` value, when set, is prepended to the
controller's own framing rather than replacing it.

`llm.max_tool_iterations` bounds the number of decisions in a single run. When
unset the bound is 50.

### Step constraints

A controller DAG MUST declare at least one step. For every declared step:

- `depends` MUST NOT be set, and a step MUST NOT be explicitly marked as having
  no dependencies. Ordering belongs to the controller.
- `router` MUST NOT be set.
- The name `__controller__` is reserved and MUST NOT be used as a step name
  or ID.

Every declared step is implicitly failure-tolerant: a failed action never aborts
the run, because the failure is an observation the controller acts on.

### The controller step

Building a controller DAG appends a synthesized step named `__controller__`
carrying the DAG's `llm` configuration. It is the node the runner drives the
loop from, and it holds the conversation transcript, the tool catalog that was
offered, and the persisted goal progress. It is not an action the controller may
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

One additional tool, `complete_task`, is always offered. It takes a `task` name
and a `reason`, and it is reserved: no step tool may take that name.

## The decision loop

Each turn:

1. The controller is sent a system message stating its role, the full task list
   with per-task completion status, and the operating rules, followed by the
   conversation so far.
2. The model replies. If it requests no tool call, see *Stalling* below.
3. Exactly one action is carried out per turn. When a reply contains several
   tool calls only the first is recorded and executed, so the conversation never
   references a result that was not produced.
4. The outcome is appended as the tool result: the resulting status, any error,
   any human task submission, declared outputs, and a bounded tail of the step's
   logs.

The loop ends when every task is complete, when an action opens a human task,
or when a limit is reached.

### Completing tasks

`complete_task` marks one task done and records the stated reason. Completing an
unknown task, or one already complete, is reported back as a tool error and the
loop continues; it does not fail the run. The same applies to a call naming a
tool that does not exist, or arguments that cannot be decoded.

### Failure

A failed action is reported to the controller, which may retry it, choose a
different action, or stop. The failure is an observation, not a run-level error:
it MUST NOT by itself cause the run to report an error to its caller.

Final status follows the steps' end state. A failed action that was re-run
successfully leaves the run `succeeded`; an action left failed while every task
completed leaves it `partially succeeded`.

### Repetition

An action may be selected again after it has already run, which resets the node
and marks it repeated so a child DAG run receives a fresh run ID. A single
action may run at most 5 times per DAG run; beyond that, the request is refused
as a tool error and the controller must choose differently.

### Stalling

If the model replies without calling a tool while tasks remain open, it is
reminded once which tasks are outstanding. A second consecutive reply without a
tool call fails the run. Any turn that does use a tool clears the count, so
occasional silence between real work is not fatal.

### Limits

Reaching the turn limit with tasks still open fails the run, and the error names
the outstanding tasks.

## Suspension and resumption

When a chosen action ends in the `waiting` status — an `action: human.task`
step, or a child DAG that is itself waiting — the controller records the
in-flight tool call, persists its state, and returns. The controller step itself
MUST NOT be left waiting, since an outstanding waiting step would prevent the
run from being released when the human task completes.

The run then reports `waiting`, the `onWait` handler runs, and the process
exits.

Completing the human task marks that step succeeded and re-queues the same DAG
run. On the next attempt the controller restores its transcript and goal
progress from the controller step, reports the outcome of the in-flight action
as that turn's tool result, and continues.

Restored state is reconciled against the current DAG: progress on a task that
still exists is preserved, a task that has been removed does not linger, and a
newly declared task starts open.

## Variable scope

A controller DAG has no dependency edges. Every action that has already
finished is treated as upstream of the action starting now, so its outputs are
in scope.

## Terminal status

- Every task complete, no action failed → `succeeded`.
- Every task complete, at least one action failed → `partially succeeded`.
- An action is waiting → `waiting`.
- Turn limit reached with open tasks, a second consecutive reply without a tool
  call, or an unrecoverable controller error → `failed`.

Steps the controller never selected are marked `skipped` when the run reaches a
terminal state.
