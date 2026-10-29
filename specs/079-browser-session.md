# Spec 079: Browser Sessions

## Status

Implemented for local commands.

## Scope

This spec covers the `dagu browser session` commands: opening a session,
running operations in it, describing its page, exporting it as a
`browser.run` step, and its lifetime. The operations themselves behave as in
Spec 072 and are not restated here. Sessions are local to the host that runs
the commands; there is no REST or MCP surface for them.

## Goal

A person or an agent can build a browser step by working the real site one
operation at a time, with the operations the step will run, seeing the page
after each, and keep exactly what worked as a step whose first run replays
it.

## Behavior

### Commands

Every command prints one JSON object to stdout. A command that fails prints
`{"error": {"code", "message"}}` and exits 1.

| Command | Purpose |
|---|---|
| `open [URL]` | Launch a browser, go to URL, and leave it waiting for commands |
| `do <ID>` | Run one operation, read from stdin, in the session |
| `describe <ID>` | Report what the session's page shows |
| `export <ID>` | Build a `browser.run` step from the session's history |
| `close <ID>` | Close the browser and remove the session, or end it with `--keep` |
| `list` | List the host's sessions |

`open` accepts `--profile`, `--headed`, `--viewport WxH`, `--executable`,
`--proxy`, `--allowed-domain` (repeatable), `--idle-timeout` (default `30m`,
at most `24h`), and the model as `--llm` (a step's `llm` field, in YAML or
JSON) or `--provider`, `--model`, `--base-url`, and `--api-key-name`. Its
result carries the session ID, the page's URL, title, and outline, and the
browser's DevTools address (`cdp_url`), where an application can show the
page and let a person act on it.

`open` also returns `tls_verified`: true confirms that the certificate proxy
verified the final HTTPS page's host. False means no such evidence was
available; it does not by itself mean the certificate failed. A host the
guard carried counts only while the guard runs, since the browser reaches it
directly once the guard stops. This confirms transport trust, not login or
whether a configured authority was needed.
`list` includes `capabilities: ["trusted_cas"]` so applications can detect
support without assuming an engine version.

A session's browser presents the client certificates of the host
configuration's `browser.client_certificates`, and guards its other HTTPS
requests as the host configuration says, as a browser step does, for as long
as the browser runs, including between commands. What the certificate
proxy reports while a command runs, such as a site rejecting the certificate,
appears in the command's warnings or error. Between commands the browser and
its proxy keep running, so local processes can use the certificates as long
as the session lasts, as they can during a browser step.

The host's `browser.trusted_cas` also applies, with the same scoped trust and
browser-lifetime snapshot as a workflow browser.

### Lifetime

A session waits between commands until its idle deadline, which every
command renews. Opening a session starts a watchdog process that closes the
browser once the session is idle past its deadline. A sweep by a later
session command, a browser step, or a server also closes it, as it does
when the process running a command died.

A session whose browser closed this way ends: `do` and `describe` fail with
`session_ended`, and its history stays readable, so `export` and `list`
still work, for 24 hours. `close --keep` ends a session the same way at
once, freeing its profile for a step. `close` removes the session at once,
with its history.

Commands on one session run one at a time; a command that finds the
session in use fails at once with `session_busy`.

### Operations

`do` reads one operation from stdin in the form of an item of a browser
step's `with.do`: `goto`, `act`, `upload`, `extract`, `expect`, `wait`, or
`screenshot`, with `when` and `timeout`. `ask` is refused. Each operation
behaves as the same operation in a step, except that an `act` or an `upload`
into words never replays a recording; it asks the model and records what it
did. An upload's relative paths are under the command's working directory.

`do` also acts on an element the outline shows with its ID, without the
model: `{"click": ID}`, `{"type": {"into": ID, "text": TEXT}}`,
`{"select": {"in": ID, "option": OPTION}}`, or `{"upload": {"into": ID,
"files": FILES}}`, each with `when` and `timeout` as an act takes them. The
element must still be on the page, have a name, and be one the operation
fits: a field to type into, a select to pick from. The session keeps a
click, typing, or pick as the act a step writes, such as `Click the "Sign
in" button`, which the result reports as `act`, and an upload as the upload
into the element in words, such as `the "Receipt" file input`, which the
result reports as `into`, with the action it took as its recording, so an
exported step replays exactly that action and heals by the operation's
words when the page changes.

The result reports the operation's index in the session's history, its
status (`done`, `failed`, or `skipped` when its `when` does not hold), the
actions an act or upload performed and whether an exported step will replay
them,
what an extract read, the files a screenshot or download saved, the dialogs
the browser accepted, the requests `allowed_domains` blocked, the tokens
used, and the page the browser is on next with its outline. A failed
operation is kept in the history and the session stays open. An extract
warns about the same link fields loading a step does.

`open`, `do`, and `describe` report a page its site refused, as a browser
step counts one, as `refused`: the `host`, the HTTP `status`, `bot_check`
naming the check, such as `cloudflare`, when the site answered with one, and
a `message` saying so in a sentence. The error of an operation that failed on
it ends with that sentence. A browser reattached by a later command learns a
bot check only from pages it loads, so a command that finds the browser on
the same page, with the same host and status, as the command before keeps the
bot check that command reported, as after a person reloaded the page between
commands.

### Variables

Variable values come with the operation on stdin, under `variables`, never
on the command line. A value given as `{"env": NAME}` is read from the
environment on that command and every later one, which then require it,
and is masked like a secret in everything the session reports. A literal
value serves its own command only and is not masked. No value is ever kept
in the session's state, only the salted digest of one an act's actions
never used. An instruction that holds a secret value is
refused, and an act whose recorded actions hold a variable's value is not
exported for replay.

### Model

A session asks the model given to `open` for acts, uploads into words,
extracts, and judged conditions. The model's settings read the environment
variables they name, its API key's and those its base URL references, on
each command; no other variable is visible to them. A session opened
without a model refuses those operations with `model_required` and runs the
rest.

### Outline

The outline describes the page without a model request, from the same
accessibility tree an act shows the model: headings, fields with their
labels, selects with their choice and options, file inputs, buttons,
checkboxes, links with their addresses, messages, and tables and lists by
their columns and rows. Each element an operation can act on shows its ID in
brackets, and a link to the page's own site shows its path; a list item that
is one link shows only the link. The page's own content comes first, and the
site's header, menus, sidebars, and footer after it, so a limit on the
outline leaves out menus rather than content. A run of more than five alike
rows shows the first three and counts the rest. Text typed into fields is
never shown. `describe --find` shows only the entries containing the text,
with the entries they sit in; `--tree` reports the raw tree instead;
`--screenshot` also saves a screenshot.

### Export

`export <ID> --dag <name|file> --step <id>` builds a `browser.run` step from
the operations that succeeded and those skipped because their `when` did
not hold, in the order they ran. Failed operations and screenshots are left
out, and `--skip <index>` leaves out others. The page `open` went to becomes
`with.url`, and the session's browser options other than `headless` become
`with.browser`. A variable read from the environment becomes `${NAME}` of
that variable, which the DAG declares under its secrets; a literal one
becomes a parameter of its own name.

Unless `--dry-run` is given, export writes the recordings of the acts and
uploads to the replay cache of the named DAG and step on this host, keyed by
their places in the step's `with.do`, so the step's first run replays them
without a model request when it meets the pages the session met, and heals
as usual when it does not. An act whose actions never used a variable its
instruction names keeps a salted digest of the value the session gave it,
so the step replays it only while that variable has that value (Spec 072). Export runs the checks the step gets when its DAG
loads and warns about an act or upload with no recording, about a page
reached outside the exported operations, and about each upload, whose files
are those the session was given until the step names what each run
uploads. `--format yaml` prints the step alone.

### Profiles

A session holds its profile from `open` until it ends or is closed. A
browser step naming that profile fails at once, naming the session and how
to close it, and a session cannot open on a profile a running step uses.

### Platforms

Sessions work on Linux, macOS, and Windows. On Windows, a session opened
from a job object that ends its processes when it closes warns that its
browser closes with that job.

## Errors

| Code | Meaning |
|---|---|
| `invalid_input` | A flag, the operation, or a variable is invalid |
| `session_not_found` | No session with the ID exists on this host |
| `session_busy` | Another command holds the session |
| `session_ended` | The session's browser has closed |
| `model_required` | The operation needs a model the session lacks |
| `profile_in_use` | A step or another session holds the profile |
| `launch_failed` | The browser could not start |
| `operation_failed` | The operation failed; the session stays open |
| `export_invalid` | The exported step would not pass its DAG's checks |
| `failed` | Any other failure |

## Examples

```sh
dagu browser session open https://portal.example.com/login --provider openai --model gpt-5-mini
echo '{"act": "Type %user% into the Username field", "variables": {"user": "alice"}}' | dagu browser session do ab2cd3ef4g
echo '{"act": "Type %password% into the Password field", "variables": {"password": {"env": "PORTAL_PASSWORD"}}}' | dagu browser session do ab2cd3ef4g
echo '{"act": "Click the Sign in button"}' | dagu browser session do ab2cd3ef4g
dagu browser session describe ab2cd3ef4g --find Orders
dagu browser session export ab2cd3ef4g --dag orders --step fetch --format yaml
dagu browser session close ab2cd3ef4g
```

## Conformance

`conformance/spec079_browser_session/` runs each session command as its own
process against a real Chrome, a local portal, and a scripted model. It
works the portal through a failed operation, exports the step, and checks it
against a static fixture, whose first run then makes no act model request;
the password never reaches the model or the output. It uploads a file into a
file input by its ID without the model and checks that the exported step's
first run replays it. It checks the profile a session holds, a session
ending once idle with its history still exported, a session without a
model, and input refused before a browser starts.
