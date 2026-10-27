# Spec: Computer Actions

## Status

Partially implemented.

Conformance covers validation, the secret check, the unsupported-platform
error, and, on an interactive Windows desktop, a run that finds a test window
in screenshots, clicks it and types into it through a scripted model, and
the elements command's outline and selector matching on a window with named
controls. Model loops, replay, exact conditions, human input, and the
desktop lease are covered by executor tests.

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
  string or an object with `instruction` and optional `ai`, `cache`,
  `max_actions`, `find_within`, `input`, and `recorded` (pinned steps, see
  Pinned steps).
- `extract`: `{instruction, schema}`, with optional `ai`, `max_rows`, and
  `fields` (pinned bindings, see Pinned steps). The schema must be a JSON
  Schema with `type: object`. A successful extract binds each scalar field
  to the element its value came from, so a later run reads it without the
  model (see Extract binding).
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
in session 0, with the screen unlocked, and, in a Remote Desktop or child
session, with that session connected so its display draws; on macOS with
Screen Recording and Accessibility granted to the application that starts
Dagu, or to the `dagu` binary itself. Otherwise the step fails before any
action, naming the missing condition. A screen that locks, another user's
session taking the display, or a Remote Desktop session whose client
disconnects while a step runs fails the operation that next reads the screen.

A step that starts while only a person can make the desktop usable, because
the screen is locked, nobody is logged in, or another user's session has
the display, waits before it takes the desktop instead of failing, as when
a run caught up after a laptop wakes starts on the lock screen. It checks
every two seconds, logs that it is waiting in an event named `screen`, and
marks the end of the wait with a `running` lifecycle event. The step's
timeout bounds the wait; a step without one fails after 24 hours, naming
the condition. Any other missing condition fails the step at once.

`dagu computer check` reports the same conditions for the current session,
prints the display size, and exits nonzero when the desktop cannot be
automated. On macOS it also asks the system to show the Screen Recording and
Accessibility prompts for the permissions that are missing. With
`--format json` it prints one object with `os`, `width`, `height`, `ready`, and
`problems`, each problem with a `message` and one of these `code`s:
`unsupported`, `load_failed`, `no_session`, `other_session`, `no_display`,
`screen_locked`, `screen_recording`, `accessibility`, `service_session`, or
`session_disconnected`. `width` and `height` are 0 when the display size is
unknown.

Computer DAGs are routed to such hosts with a DAG-level `worker_selector`.

One computer step at a time operates a user's desktop, across every Dagu
process that user runs on the host, whatever their data directories. A step
that finds the desktop in use waits for it and logs that it is waiting; the
waiting event on the timeline is named `desktop`. A step
paused by `ask` does not hold the desktop. While a step holds the desktop, the
display and the system stay awake, as far as the operating system allows.

### Conditions

`expect` and `when` take a statement string, or an object with exactly one
of `statement`, `text`, `element`, or `window`, and optionally `within`.

A statement is judged by the model against a screenshot. With `within`, a
false statement is checked again every few seconds until it holds or
`within` passes.

The other three are exact checks: they read the elements of the window in
front (see Elements), take no screenshot, and call no model, so they give
the same answer on every run. `text` holds when the name or value of a
visible element contains it. `element` holds when the selector matches
exactly one visible element; several matches are a miss, never a guess.
`window` holds when the title of the window in front contains it, with `*`
for any run of characters. A window that exposes no elements is a miss
while it is in front. An exact `expect` keeps looking every quarter second
until it holds, `within` passes, or, without `within`, the operation
timeout passes; an exact `when` keeps looking for two seconds unless
`within` says how long. The event of an exact check records `via: exact`.

`%name%` placeholders in `text`, `window`, and a selector's `name`,
`window`, `near.label`, and container are replaced from `with.variables`
and earlier `ask` answers, which validation checks as it does for an
`act`; a selector's `id` and `app` are identifiers and stay as written. An
exact check never reaches the model, so the secret check does not apply to
it, and its values are masked in the timeline as typed values are. Exact
checks need macOS or 64-bit Windows; elsewhere the step fails at the first
one.

### Model

A computer step uses the DAG-level `llm` block. `with.llm` replaces it
entirely. A step with no model configuration fails validation when some
operation can call the model under its choice of how much AI decides: an
`act` or an `extract` whose choice is not `never`, or an `expect` or `when`
that is a statement. A step none of whose operations can call the model
runs without one.

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

The act ends when the model reports the task done. A report made in the
same turn as actions is not taken yet: the actions run, the model is shown
the screen they produced with a note, and the act ends only on a report
without actions. A round that repeats the previous one and leaves the
screen as it was is answered with a note saying so. When a round would take
the act past `max_actions` (`with.max_actions`, default 50), its actions are
not run and the model is shown the screen with a note asking for its report.
The act fails when:

- the model reports that the task cannot be done, with its summary;
- the model twice answers without an action or a report;
- the model acts again after being told the task has no actions left;
- the model provider asks a person to confirm the next actions and
  `with.on_confirmation` is `fail` (the default); with `allow` the actions run
  and the approval is sent with the next screenshot; or
- the operation timeout passes.

A step never operates the application that runs it: an action aimed at one
of its windows, or keys and typing while one has the focus, fails with a
result naming the window, and the model is told. Those are the windows of
the Dagu process and the processes that started it, and on any system the
windows of the applications named, by executable name and separated by
commas, in the `DAGU_DESKTOP_PROTECTED_APPS` environment variable, and the
windows whose title matches one named, separated by commas and matched as
a window title is anywhere else, in `DAGU_DESKTOP_PROTECTED_TITLES`. The
first protects an application a person starts automation from when a
service manager, rather than that application, starts Dagu, as on macOS;
the second protects that application's pages in a browser a person also
uses, where a click could start the automation again.

### Window size

Unless `with.maximize` is `false`, a step maximizes the window in front
before an operation looks at the screen: before an `act` and each model turn
of it, each replayed turn and while a pixel turn waits for its screen, and
before an `extract`, an `expect`, a `when`, and a `screenshot`. An `act` or
an `extract` may set its own `maximize`. The application is then operated at
one size every run, so recordings replay, and the model reads larger text.
On Windows the window is maximized as its maximize button would; on macOS it
fills the main display between the menu bar and the Dock, without entering
full screen, which would move it to a Space of its own. A window on another
display moves to the main display, the only one captured. A dialog, a sheet,
a panel, a window of fixed size, a window already maximized, and a window of
a protected application are left as they are. Each window is maximized at
most once per step attempt, so a window a person or its application resizes
afterwards keeps its size. The step log names each window it maximized.

The step log lists each action; the timeline records one event per operation.

### A person using the desktop

Before it launches an application, replays a recorded turn, or asks the model
for its first actions, a step waits until nobody has used the desktop's
pointer or keyboard for `with.idle` (default `5s`), and logs that it is
waiting in an event named `person`. Input the step itself sent does not count, including input sent by
the step that held the desktop before it. When a person uses the desktop
after the screenshot the model answered, the step waits for the idle period
again. If the window the actions land on still looks as the model saw it,
overall and where each action lands, and the keyboard focus is still on the
window it was on, the actions run; otherwise they are not run, and the new
screenshot is sent with a note saying why. Before that screenshot, an act
brings back the application it was working in: the one in front when the
act started, or the one its last action that ran operated (where a pointer
action landed, or the window in front as keys were sent; a screenshot or a
wait operates none). The window the person
left in front is theirs, so the step never maximizes it. A change elsewhere on the screen
does not stop the actions. Where the system does not report the focus, the
screen check alone guards a turn that types or presses keys. The end of
a wait is a lifecycle event of its own, `running`, so a view knows the step
is at work again. Skipped actions do
not count toward `max_actions`. The waiting does not count toward the
operation timeout, which bounds what the step does, not what it waits for;
a person who keeps using the desktop for an hour fails the operation.
`idle: 0` turns the waiting and the skipping off.

A step that runs in a remote desktop session rather than the one attached
to the physical console does not wait for a person unless `with.idle` is
set: nobody at the keyboard shares that desktop, and the remote connection
feeds it pointer and keyboard updates of its own, so it never looks idle.
The step says so once, in a `running` lifecycle event. On Windows the
console session is the one `WTSGetActiveConsoleSessionId` names; elsewhere
every session counts as the console.

### Variables and secrets

`with.variables` maps names to values. An `act` instruction references them as
`%name%`, and so does a later act for an `ask.as` name. A reference to any
other name fails validation. The model sees only the placeholder; when it types
text containing `%name%`, the value is typed instead. Values typed into fields
that show them can appear in later screenshots, which are sent to the model
and saved as artifacts.

`with.visible` lists variables whose values the model may see, for values
that are not secret: the model is told what each one stands for, so it can
find or choose it on the screen, such as an item in a list a row chooses
from. It still types the placeholder, and an element it chose by such a
value is recorded by the placeholder (see Recordings), so the next run
chooses its own value. A name in `with.visible` must be in
`with.variables`, and the step fails before operating the desktop when a
visible value holds a declared secret's value, however short.

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
or `never`. A step in a `foreach` body writes every item's screenshots there,
and each item's timeline names the screenshots it took. With artifacts
disabled, a `screenshot` operation fails.

### How much AI decides

`with.ai` chooses how much the model decides for the step's `act` and
`extract` operations, and `ai` on an `act` or an `extract` replaces it for
that operation:

- `every_run`: the model decides every run. A successful `act` still
  records what it did.
- `on_miss` (the default): a recorded `act` replays without a model request
  while every screen still matches, and the model takes the task over from
  the first screen that differs and repairs the recording.
- `never`: a recorded `act` replays, and a miss fails the step. No
  screenshot leaves the host and no model is called. The failure names the
  reason: no recording of the act on this host, the turn at which the screen
  differed from the recording, the action that failed, or the screen after
  the last turn that differed. The turns replayed before the miss have
  already run on the desktop, which is left as it is. The recording is kept,
  so a later run under `on_miss` repairs it.

`with.cache: false` and `act.cache: false`, from before the choice existed,
mean `every_run`. Setting both `ai` and `cache` on the step, or on an act,
fails validation. A statement is judged by the model, so a step whose `ai`
is `never` fails validation with an `expect` or `when` that is a statement.
An exact check is allowed, and so is an `extract` whose fields all read from
an element; an `extract` under `never` that lists an object, array, or
boolean field, which need the model, fails validation.

Each operation's timeline event records `position`, its place in `do` from
1, and `via`, how it ran: `element` for a replay by the window's elements,
`screen` for a replay by the pixels of the screen, `model` for a model
request, `exact` for an exact check, and nothing for an operation that
decides nothing, such as `launch`; with `durationMs` and `tokens`. An
operation that fails records its event the same way, with the failure as
its detail, before the step's failure event that carries the screenshot.

### Replay cache

A model-driven `act` records each screen the model saw and the actions it
chose on it, and the recordings are kept when the step succeeds. Beside the
pixels of each action, a recording keeps the element the action landed on,
or typed into, when the host can read elements (see Elements): its role,
its name or id, its path in the window, and where in it the pointer went.
An element with neither a name nor an id is recorded by its pixels alone. A
name or a typed text equal to a variable's value or an `ask` answer of two
or more characters is recorded as the `%name%` placeholder, and so is a
name equal to a `with.visible` value however short, since the model chose
the element by it; an element recorded by a placeholder is recorded without
its id, which would find the recorded element again. In a window title,
each such value of two or more characters is replaced by its placeholder
likewise, so a recording made on one record replays on the next; a shorter
value stays as it is there, since one character would match parts of the
title that are not the value. The recording keeps the application of the
window the act ran on — the process image name — which a replay matches on,
and each window's title only to tell apart several windows of one
application and as a fallback when the application cannot be read. The
recording ends with its landmarks: the window in front and up to three of
the elements the act touched last that were still there.

A later run of the same step on the same host replays a recording without
a model request when the operation position and instruction match. Each
turn replays by its elements when every pointer action of the turn has one
and the application in front is the recorded one: each element is found again,
with the recorded path breaking a tie between equal matches, and the action
runs where it is now, typing into it after focusing it. Otherwise the turn
replays by its pixels within the application window it was recorded on: the
window its first pointer action landed on, or the window with the keyboard
focus for a turn that only types or presses keys, or the front window of
that window's application when the action landed on a menu. The replay
finds the application's front window of the recorded size, needs that
window, and the area around each pointer action inside it, to look as
recorded, and acts at the recorded positions relative to the window's
corner. A window that moved and a change elsewhere on the screen do not
stop it; a window of another size does. A turn recorded where the system
named no window compares the whole display, which then needs the recorded
display size. A
replayed action waits for its element, and a pixel turn for its screen,
looking again every quarter second until `find_within` passes: 10 seconds
unless `with.find_within` or `act.find_within` says otherwise, 0 to look
once. The wait counts toward the operation timeout. After the last turn, a
replay whose turns all ran by elements needs its landmarks in front, when
the recording has some; any other replay needs the window the act ended on,
or the whole display where the system named none, to look as recorded.

`with.input`, or `act.input` for one act, says how a replayed action
reaches the application. `screen`, the default, sends the pointer and keys
as the model did; a click the model carried out through the element itself,
as it does on a button, replays the same way under either input, since a
click at the element's centre may not reach what the pattern did. A click
the model aims at a combo box lands on it as a person's click does, opening
one that only offers a list and putting the caret in one that takes typing.
`direct` has the element act itself through its
automation pattern, which moves no pointer and needs no window in front: a
plain click on a `button`, `menu_item`, `link`, or a control of role
`other` invokes it, on a `checkbox` toggles it, on a `radio`, `list_item`,
`tab`, or `cell` selects it, on a `combo_box` opens it, and on a
`text_field` focuses it; a typing
sets the field's value at once when the recording proved it could, which is
when the field showed the typed text once the model had typed it, so an
append to existing text, a text with a line break, or a field that
reformats what was typed is typed again. Any other action keeps the pointer
and keys. An element that offers no pattern for the action replays it on
the screen, and the turn is still a replay by element. An element that is
disabled or gone is looked at again until `find_within` passes; any other
refusal is a miss naming the element and why: `button "保存" could not be
invoked at turn 2 of 5: the field is read-only`. An application that reacts
to each key press or to the pointer alone sees nothing of a direct action,
so `screen` stays the default and `direct` is chosen per step or act. The
model's own turns always use the screen; a recording keeps whether a typing
proved it sets the value as `sets_value`.

When an element or a screen is not found, or an action fails, the model
continues the task from the current screen and the new actions are
recorded; the timeline marks the operation `healed`. The model is told
which recorded actions the replay already carried out and why it stopped,
so it does only what is still needed, and nothing when the task is already
done, such as a form the replay saved before its last screen differed. When
the step succeeds, the turns that replayed and the new actions replace the
recording; when there are none, the recording is removed unless another run
of the step replaced it first. A full replay is marked `cache-hit`, with
`via: element` when every turn replayed by elements and `via: screen`
otherwise. Under `never`, a miss names the element, the wait, the turn, and
why, such as `button "保存" was not found within 10s at turn 2 of 5: 3
elements match`.

Recordings carry a format version. A recording of another version is
ignored once: the run asks the model, or fails under `never` saying so,
and what it records replaces the old one.

Runs of a step share its recordings, and each `act` reads them when it runs. A
recording that another run of the step replaced or removed meanwhile is not
replayed.

A replay follows the step's current settings: it hands the task to the model
before a recorded turn that would take the act past `max_actions`, or that
the model provider asked a person to confirm while `on_confirmation` is not
`allow`. Replayed actions count toward `max_actions`.

When an operation fails after a replay, the step drops the recordings it
replayed, so the next run asks the model again. A failure of a model request,
the screen capture, a launch, or an `ask`, a canceled run, or a miss under
`never` leaves them.

Typed text is recorded with its `%name%` placeholders, never the values.

`dagu computer cache clear <dag>` removes the recordings of every step of the
DAG, or of one step with `--step <id>`, or of one task of a step with
`--step <id> --op <position>`, the position in `do` counted from 1 as the
run log does; the next run asks the model for that task and replays the
rest. Removing all of a DAG's history with `dagu rm --history` also clears
them. Each clears the cache on its own host.

### Human input

An `ask` operation puts the step in `Waiting` with a pending question and ends
the step execution, leaving the desktop as it is. Answering the question from
the Web UI or REST API resumes the step on the same host at the operation
after the `ask`, with the answer available as `%<as>%` and the outputs
extracted before the pause. Rejecting the question fails the step. An answer
after `ask.timeout` (default one hour) fails the step. The pending question
carries that deadline as `expiresAt`. Restarting the session runs the step
from its first operation.

### Extract binding

An `extract` reads structured data from the screen into the step's outputs.
Under `every_run` and `on_miss` the model receives the screenshot and, when
the host can read elements, an outline of the window in front: up to 400
elements, each an id, role, name, and value. The model returns each field's
value and the id of the element it read it from, or no id when it read from
the screen.

A scalar field, one whose schema type is `string`, `number`, or `integer`,
binds to an element when the model named one, the element's value converted
to the field's type equals the model's value converted the same way, and
exactly one element in the window gives that value. A value that matches
two elements, or that the model read from the screen, does not bind, so a
coincidental match is never guessed. An object or an array field does not
bind (an array of objects binds to a table, described below); a boolean
field binds with tables.

A later run reads each bound element and converts its value to the field's
type: a string is trimmed and compared folding full-width characters and
whitespace; a number or integer drops grouping and currency symbols and
folds full-width digits. The window in front must be the recorded
application, as a replay by elements requires; a field whose element is
missing, ambiguous, or whose value does not convert is a miss. Each read
waits up to `with.find_within`. `every_run` always reads with the model and
rebinds. `on_miss` reads the bindings and, on any miss, reads the whole
extract with the model again and rebinds. `never` reads the bindings alone
and fails, naming the field, when one is missing. The timeline event's
`via` is `element` when every field was read from its binding, `model` when
the model read it.

### Pinned steps

A recording lives on the device that learned it. To run a step on every
device of a system with no first-time learning, an act takes `recorded` and
an extract takes `fields`, written into the step and keyed by system
(`windows` or `macos`). A device whose system matches runs them ahead of its
own recording; a block for another system is ignored. The system is the
host's: Windows runs the `windows` block, macOS the `macos` block. A host
that cannot read elements, such as a Windows host that cannot open UI
Automation, misses a pin rather than failing, and `on_miss` hands the act to
the model while `never` reports the miss.

`recorded` is a list of steps, each exactly one of: `click`, `double_click`,
`right_click` (a selector); `type` (`{into, text}`, where `text` keeps its
`%name%` placeholders); `key` (a chord such as `ctrl+s`, with an optional
`focus` selector naming the element to focus first, which a replay finds and
focuses as it does a typed field's); `scroll` (`{on, dy}`); or `wait` (a
duration). Each pointer step clicks the centre of the element its selector
names, found the same way a replay finds a recorded
element, so the window is raised by the selector's `app` and `window` and the
action waits up to `find_within`. Under `input: direct` a step drives the
element's pattern. When every step runs, the act is a cache hit by element
with no model; when a step misses, `on_miss` lets the model finish and
record a fresh device recording, which begins with the pinned steps that ran
when any of them acted on an element, so a person can review it to update
the pin; `never` fails naming the step. A pinned act has no screen check of its
own; the following `expect` is its gate.

`fields` maps each output name to a selector for a scalar, or to
`{container, columns}` for a table, where `columns` maps each item property
to its column header. A device reads the pinned field with no model, taking
each field's type from the extract schema, exactly as it reads a learned
binding, and a field the device has learned itself is overridden by its pin.
A field with no pin falls back to the device's own binding.

Pins are kept per system because UI Automation and the macOS accessibility
API expose an application differently, so a pin made on Windows serves
Windows devices. Pins travel with the step; the device cache does not.

A recording keeps the system it was made on and when its run ran, and
`dagu computer cache show <dag>` prints a host's recordings in their pinned
form for a person to review and write into the step: every step, or one
with `--step <id>`, and one task with `--op <position>`. A task whose
instruction changed, or that ran with other parameters, has a recording for
each, and the newest comes first. Each recorded element becomes a selector
by its role, name, and id in its application, or in its window's title when
the application could not be read. An element without an id is looked for
`in` its closest named `group`, `list`, `tab`, or `other` ancestor, since a
name alone often repeats in a window; a row or a cell is never the
container, since its name is the data shown. A single or double left click,
a single right click, typing into a field that was read, a key chord
(once per press), a plain vertical scroll on an element, and a wait each
become one step, and a pointer move onto the element the next action uses
is dropped. A key chord keeps the element that had the focus as its
`focus`, on its first press only, since each press after it starts from
where the one before left the focus. Any other action has no pinned form: a
drag, a click holding keys or where no element could be read, a click or
scroll more than 30% of the element's width or height from its centre
(except on a text field), since a pin acts at the centre, a move alone, a
held key, or a button pressed or released alone. An act with one lists each such action
with its turn and why, instead of steps, since a pin that skipped an action
would report success without doing the task. An extract's bindings become
its fields. `--format json` prints `{"operations": [...]}`, each with
`step`, `op`, `kind`, `system`, `saved_at`, and `recorded`, `fields`, or
`unpinnable`, whose items carry `turn`, `action`, `reason`, and a `code` for
a program: `no_element`, `edge_click`, `click_with_keys`, `click`,
`no_field`, `keys`, `edge_scroll`, `scroll`, `move`, `drag`, `hold_key`,
`button`, or `unsupported`.

### Elements

A step checks elements through exact conditions (see Conditions), and a
replay acts on the elements the model's actions landed on and waits for
them (see Replay cache). A replay identifies the recorded window by its
**application** — the process image name — not by its title, since a title
often shows the document's contents and changes every run; it falls back to
the title only when the application cannot be read. When the recorded
application is not in front, a replay brings a window of that application
forward before it looks again, preferring one whose title still matches,
never the step's own application, so a window that lost the focus is raised
rather than counted a miss. Names, labels, window titles, and the text a
check looks for are compared after folding full-width characters to
half-width, collapsing whitespace, and dropping a label's trailing colon,
so a selector a person types matches what a form shows without reproducing
its spelling; ids are compared as written.

In generic mode the model is also offered the front window's actionable
elements by id beside the screenshot and may click one with `click_element`,
which the step resolves to the element's centre, more reliable than aiming
at a pixel for a small control. The click records its element like any other,
so the next run replays it by element.

An element is an accessible element of a window: its `role`, `name`, the
`id` the application gives it, its `value`, the `label` it is linked to,
its `bounds` in display pixels, its `window`'s title, and its `path` from
the window down, each step a role, a name, and the element's position among
its parent's children of that role. Roles are `button`, `text_field`,
`text`, `checkbox`, `radio`, `combo_box`, `list_item`, `menu_item`, `tab`,
`link`, `cell`, `list` (a list, grid, table, or tree), `group`, `window`,
and `other`. A password field has no value.

`dagu computer elements` lists the visible elements of the window in front
in tree order, with the window itself, up to `--limit` of them (default
400). With `--at-pointer`, after `--after`, it reports the element under
the pointer. With `--watch`, it reports the element under the pointer
whenever it changes, as one JSON object per line, until stdin closes or
the command is interrupted. With `--match`, it reports the elements a
selector matches and succeeds only when exactly one does; `-` reads the
selector from stdin. With `--format json` the result is one object; a
failing command prints `{"error": {"code", "message"}}` and exits nonzero,
where the code is `invalid_input`, `unsupported`, `load_failed`,
`no_elements`, `not_found`, `ambiguous`, `failed`, or a problem code of
`dagu computer check`. The matches are still listed when a selector fails,
so a person can see what to narrow. The command takes no desktop lease.

A selector is JSON with `role` and at least one of `name`, `id`, or
`near`, and optionally `app` (the process image name without its
extension, compared ignoring case), `window` (text the title contains),
`in` (a selector for a container), `near` (`{label, side}`, with `side`
one of `right`, `below`, `left`, `above`), and `nth` (which of several
equal matches, in reading order, from 0). `name` and `window` take `*` for
any run of characters; a `%name%` placeholder is matched as written. An
unknown key is refused.

Matching runs in a fixed order: `in` narrows the search to the container's
descendants, and the container itself must match exactly one element; an
`id` wins when it is unique there, and an id that matches nothing is a miss
whatever the name; otherwise `role` and `name` select the candidates;
`near` keeps the candidates the application links to the label, or, when
it links none, for each text element with the label's name, the nearest
candidate on that side of it that overlaps it in the other direction; then
`nth` picks one, in reading order. Reading order places elements in rows,
top to bottom, and left to right within a row; an element starts a new row
when its top is below the middle of the row's first element. More than one
candidate left is a miss, never a guess.

Elements are read on 64-bit Windows through UI Automation, so Win32,
WinForms, WPF, UWP, Office, and Chromium windows expose them; a Chromium
window builds its tree when first asked. A window of a process run as
administrator, or one that draws its own controls, gives `no_elements`.
Direct input uses the control patterns the same elements offer: Invoke,
Toggle, SelectionItem, ExpandCollapse, and Value, with ScrollItem to bring
an item into view first. A pattern call runs on a thread of its own, so an
application that opens a dialog inside the call holds only that call, which
comes back as a miss after five seconds while the elements stay readable.

On macOS elements are read through Accessibility, with the Accessibility
permission a step already needs, so AppKit, SwiftUI, Catalyst, and Chromium
and Electron windows expose them. The step turns on a Chromium or Electron
application's tree, which it builds a moment after a client asks, through
its `AXManualAccessibility` attribute. Roles map from Accessibility roles:
`AXButton` is a `button`, `AXTextField` and `AXTextArea` a `text_field`,
`AXStaticText` a `text` named by what it shows, `AXCheckBox` a `checkbox`,
`AXRadioButton` a `radio`, or a `tab` in a tab group, `AXPopUpButton` and
`AXComboBox` a `combo_box`, `AXRow` a `list_item`, `AXCell` a `cell`,
`AXTable`, `AXOutline`, and `AXList` a `list`, and `AXGroup` a `group`,
named by the element that titles it when it has no title of its own. An
element's id is its accessibility identifier, or a web page's element id.
Direct input presses an element, sets its selected state, shows its menu,
or sets its value. Every request to an application is bounded by a two
second timeout, so an application that opens a dialog inside a press holds
only that call, which comes back as a miss while the elements stay
readable. Other systems give `unsupported`.

## Errors

A missing `with.do`, `with.instruction` for `computer.extract`, or
`with.schema` fails validation with a diagnostic naming the field. An operation
that sets zero or several keys fails validation. A step fails when the desktop
cannot be opened, an application cannot be launched, an `act` fails as
described above, an `act` under `never` has no recording or misses, or an
`expect` does not hold. `dagu computer elements --match` exits nonzero when
the selector matches no element or several.

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
