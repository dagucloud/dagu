# Spec: Computer Actions

## Status

Partially implemented.

Conformance covers validation, the secret check, the unsupported-platform
error, and, on an interactive Windows desktop, a run that finds a test window
in screenshots, clicks it and types into it through a scripted model. Model loops, replay, human input,
and the desktop lease are covered by executor tests.

## Scope

This spec defines the `computer.extract` and `computer.run` action boundary:
the `with` contract, where steps run, model configuration, the act loop,
outputs, artifacts, the replay cache, and human input. It does not define the
prompts sent to models or how a model chooses actions.

## Goal

Workflow authors can automate desktop applications that have no API, such as
ERP clients or legacy Windows programs, from a step: start applications,
complete tasks described in natural language, read values from the screen into
step outputs, check the screen, and pause for a person.

## Behavior

### Actions

`computer.run` takes `with.do`, a nonempty list of operations run in order on
one desktop.

`computer.extract` takes `with.instruction` and `with.schema`, with optional
`timeout`, `mode`, `screenshots`, and `llm`, and behaves as `computer.run` with
one `extract` operation.

Each operation sets exactly one of:

- `launch`: a command string, or `{command, args}`. The application starts
  without a shell, the step does not wait for it, and it keeps running after
  the step ends.
- `act`: a task described in natural language. The value is an instruction
  string or an object with `instruction`, optional `cache`, and optional
  `max_actions`.
- `extract`: `{instruction, schema}`. The schema must be a JSON Schema with
  `type: object`.
- `expect`: a statement about the screen that must hold; otherwise the step
  fails with the model's reason.
- `wait`: a duration such as `2s`.
- `screenshot`: a name; the screen is saved as a PNG run artifact.
- `ask`: `{prompt, as, timeout}` waits for a person's answer (see Human input).

Any operation may set `when`, a statement checked before the operation; the
operation is skipped unless it holds. Any operation may set `timeout`, a
duration such as `30s`; the default is five minutes.

### Where steps run

A computer step operates the primary display of the desktop session the Dagu
process runs in, on macOS or Windows. On other systems the step fails with
`desktop automation is supported on macOS and Windows only`.

The process must run in a logged-in user session: on Windows not as a service
in session 0, and with the screen unlocked; on macOS with Screen Recording and
Accessibility granted to the application that starts Dagu, or to the `dagu`
binary itself. Otherwise the step fails before any action, naming the missing
condition. A screen that locks, or another user's session taking the display,
while a step runs fails the operation that next reads the screen.

`dagu computer check` reports the same conditions for the current session,
prints the display size, and exits nonzero when the desktop cannot be
automated. On macOS it also asks the system to show the Screen Recording and
Accessibility prompts for the permissions that are missing. With
`--format json` it prints one object with `os`, `width`, `height`, `ready`, and
`problems`, each problem with a `message` and one of these `code`s:
`unsupported`, `load_failed`, `no_session`, `other_session`, `no_display`,
`screen_locked`, `screen_recording`, `accessibility`, or `service_session`.
`width` and `height` are 0 when the display size is unknown.

Computer DAGs are routed to such hosts with a DAG-level `worker_selector`.

One computer step at a time operates a user's desktop, across every Dagu
process that user runs on the host, whatever their data directories. A step
that finds the desktop in use waits for it and logs that it is waiting; the
waiting event on the timeline is named `desktop`. A step
paused by `ask` does not hold the desktop. While a step holds the desktop, the
display and the system stay awake, as far as the operating system allows.

### Conditions

`expect` and `when` take a statement string, or `{statement, within}`. The
model judges the statement against a screenshot. With `within`, a false
statement is checked again every few seconds until it holds or `within`
passes.

### Model

A computer step uses the DAG-level `llm` block. `with.llm` replaces it
entirely. A step with no model configuration fails validation.

`with.mode` chooses how `act` talks to the model:

- `auto` (default): the provider's native computer-use tool for `anthropic`,
  `openai`, and `gemini` models that support it; plain function tools for
  other providers and for models released before their provider's tool, such
  as Claude Opus 4.7 or Gemini 2.5.
- `native`: the native tool; a provider without one fails validation, and a
  model without one fails the step.
- `generic`: plain function tools, which work with any tool-calling model that
  accepts images.

When several models are listed, an `act` moves to the next model only when a
model fails before any action ran. `extract` and conditions try the models in
order for every request.

### Act

An `act` shows the model a screenshot scaled to what the model accepts and
performs the pointer and keyboard actions it answers with, in order, mapping
positions back to display pixels. After each round of actions the step waits
for the screen to stop changing and sends the new screenshot with the results.
An action that fails skips the rest of the round and is reported to the model.

The act ends when the model reports the task done. It fails when:

- the model reports that the task cannot be done, with its summary;
- the model twice answers without an action or a report;
- the actions would exceed `max_actions` (`with.max_actions`, default 50);
- the model provider asks a person to confirm the next actions and
  `with.on_confirmation` is `fail` (the default); with `allow` the actions run
  and the approval is sent with the next screenshot; or
- the operation timeout passes.

The step log lists each action; the timeline records one event per operation.

### A person using the desktop

Before it launches an application, replays a recorded turn, or asks the model
for its first actions, a step waits until nobody has used the desktop's
pointer or keyboard for `with.idle` (default `15s`), and logs that it is
waiting in an event named `person`. Input the step itself sent does not count, including input sent by
the step that held the desktop before it. When a person uses the desktop
after the screenshot the model answered, the model's actions are not run: the
step waits for the idle period again and sends the new screenshot with a note
saying why. Skipped actions do not count toward `max_actions`. The waiting
counts toward the operation timeout; an operation whose timeout passes while
a person keeps using the desktop fails. `idle: 0` turns the waiting and the
skipping off.

### Variables and secrets

`with.variables` maps names to values. An `act` instruction references them as
`%name%`, and so does a later act for an `ask.as` name. A reference to any
other name fails validation. The model sees only the placeholder; when it types
text containing `%name%`, the value is typed instead. Values typed into fields
that show them can appear in later screenshots, which are sent to the model
and saved as artifacts.

The step fails before operating the desktop when an `act`, `extract`, `ask`,
`expect`, or `when` text contains the resolved value of a secret declared
under `secrets:` that has four or more characters. Declared secret values and
`ask` answers of four or more characters are masked in the step log, the
timeline, and errors.

### Outputs

The top-level properties each `extract` schema lists become step outputs,
readable as `${steps.<id>.outputs.<name>}` and known when the DAG loads. Two
extract operations in one step that list the same property fail validation.
Only listed properties are published. When the step succeeds with outputs,
stdout is one JSON object of those outputs.

### Artifacts

A DAG with a computer action enables artifact storage unless it sets
`artifacts.enabled: false`. Screenshots are written under
`computer/<step id>/` in the run's artifacts directory, scaled to at most
1920 pixels on the long edge, and are not masked. `with.screenshots` takes the
same values as for browser actions: `on_failure` (default), `final`, `each`,
or `never`. With artifacts disabled, a `screenshot` operation fails.

### Replay cache

With `with.cache` true (the default), a model-driven `act` records each
screen the model saw and the actions it chose on it, and the recordings are
kept when the step succeeds. A later run of the same step on the same host
replays them without a model request when the operation position,
instruction, and display size match and each screen, and the area around each
pointer action, still looks as recorded. The screen after the last action must
also match. When a screen differs or an action fails, the model continues the
task from the current screen and the new actions are recorded; the timeline
marks the operation `healed`. When the step succeeds, the turns that replayed
and the new actions replace the recording; when there are none, the recording
is removed unless another run of the step replaced it first. A full replay is
marked `cache-hit`. `act.cache: false` disables the cache for one operation.

Runs of a step share its recordings, and each `act` reads them when it runs. A
recording that another run of the step replaced or removed meanwhile is not
replayed.

A replay follows the step's current settings: it hands the task to the model
before a recorded turn that would take the act past `max_actions`, or that
the model provider asked a person to confirm while `on_confirmation` is not
`allow`. Replayed actions count toward `max_actions`.

When an operation fails after a replay, the step drops the recordings it
replayed, so the next run asks the model again. A failure of a model request,
the screen capture, a launch, or an `ask`, or a canceled run, leaves them.

Typed text is recorded with its `%name%` placeholders, never the values.

`dagu computer cache clear <dag>` removes the recordings of every step of the
DAG, or of one step with `--step <id>`. Removing all of a DAG's history with
`dagu rm --history` also clears them. Each clears the cache on its own host.

### Human input

An `ask` operation puts the step in `Waiting` with a pending question and ends
the step execution, leaving the desktop as it is. Answering the question from
the Web UI or REST API resumes the step on the same host at the operation
after the `ask`, with the answer available as `%<as>%` and the outputs
extracted before the pause. Rejecting the question fails the step. An answer
after `ask.timeout` (default one hour) fails the step. The pending question
carries that deadline as `expiresAt`. Restarting the session runs the step
from its first operation.

## Errors

A missing `with.do`, `with.instruction` for `computer.extract`, or
`with.schema` fails validation with a diagnostic naming the field. An operation
that sets zero or several keys fails validation. A step fails when the desktop
cannot be opened, an application cannot be launched, an `act` fails as
described above, or an `expect` does not hold.

## Examples

```yaml
secrets:
  - name: ERP_PASSWORD
    provider: env
    key: ERP_PASSWORD

params:
  INVOICE_ID: INV-0001

llm:
  provider: anthropic
  model: claude-opus-5

worker_selector:
  desktop: finance

steps:
  - id: post
    action: computer.run
    with:
      variables:
        password: ${ERP_PASSWORD}
      do:
        - launch: C:\Program Files\ERP\client.exe
        - act: Log in as clerk with password %password%
        - act: Open the invoice entry form and post invoice ${params.INVOICE_ID}
        - expect: {statement: A document number is shown, within: 30s}
        - extract:
            instruction: The document number in the status bar
            schema:
              type: object
              properties:
                document_number: { type: string }

  - id: record
    depends: post
    run: echo "${steps.post.outputs.document_number}"
```
