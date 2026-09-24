# Spec: Browser Actions

## Status

Partially implemented.

Conformance covers extraction into outputs, a multi-operation run with model
and fixed conditions, screenshots, downloads, the replay cache, the secret
check, allowed domains, dialogs, and validation. Waiting for input and resuming the same
browser need the REST API and are covered by integration tests. Model prompts,
page interaction internals, and profile locking belong to executor tests.

## Scope

This spec defines the `browser.extract` and `browser.run` action boundary: the
`with` contract, model configuration, outputs, artifacts, downloads, the replay
cache, allowed domains, and human input. It does not define how a model chooses
page elements or the browser runtime's prompts.

## Goal

Workflow authors can drive a website that has no API from a step: act on pages
in natural language, extract structured data into step outputs, check page
state, and pause for a person when the site asks for input.

## Behavior

### Actions

`browser.run` takes `with.do`, a nonempty list of operations run in order in one
browser session. `with.url`, when set, is opened before the first operation.

`browser.extract` takes `with.url`, `with.instruction`, and `with.schema`, and
behaves as `browser.run` with one `extract` operation.

Each operation sets exactly one of:

- `goto`: navigate to a URL.
- `act`: perform an action described in natural language. The value is an
  instruction string or an object with `instruction` and optional `cache`.
- `extract`: `{instruction, schema}`. The schema must be a JSON Schema with
  `type: object`.
- `expect`: a condition that must hold; otherwise the step fails with the
  reason.
- `wait`: `{selector}` waits until the element is visible; `{duration}` pauses.
- `screenshot`: a name; the page is saved as a PNG run artifact.
- `ask`: `{prompt, as, timeout}` waits for a person's answer (see Human input).

Any operation may set `when`, a condition checked once before the operation;
the operation is skipped unless it holds. Any operation may set `timeout`, a
duration such as `30s`; the default is two minutes. A browser that stops
responding fails the step: an operation is abandoned a few seconds after its
timeout, and a screenshot or page check after 30 seconds.

### Conditions

A condition is either:

- a string: a statement about the page, judged by the model; or
- an object with exactly one of `text` (the page's visible text contains it),
  `selector` (a CSS selector matches a visible element), or `url` (the current
  URL contains it). These fixed checks read the page and make no model request.

A fixed check may set `within`, a duration during which it keeps reading the
page until it holds. A fixed `when` reads the page once unless `within` is set;
a fixed `expect` keeps reading until `within`, or the operation timeout when
`within` is unset. A model-judged condition is asked once. A `selector` check
holds when any matching element is visible.

### Model

A browser step uses the DAG-level `llm` block. `with.llm` replaces it entirely,
with the same shape and providers as the DAG-level block. A browser step with no
model configuration fails validation. When several models are listed, each
model request tries them in order.

Every model request asks for a tool call whose parameters are the response
schema. A reply without a tool call is accepted when its text is valid JSON.
Models that follow tool-call schemas reliably are required for dependable
results.

### Variables and secrets

`with.variables` maps names to values. An `act` instruction references them as
`%name%`, and so does a later act for an `ask.as` name. A reference to any other
name fails validation. An act that references the answer of an `ask` that was
skipped fails the step. The browser receives variable values; requests to the
model carry only the names.

The step fails before starting a browser when an `act`, `extract`, `ask`, or
model-judged `expect` or `when` text contains the resolved value of a secret
declared under `secrets:` that has four or more characters. Values that only
come from `env:` are not secrets and are not checked.

Declared secret values and `ask` answers of four or more characters are masked
in text sent to the model, in the step log, and in the timeline. Other variable
values are not masked.

### Outputs

The top-level properties each `extract` schema lists become step outputs,
readable as `${steps.<id>.outputs.<name>}` and known when the DAG loads. Two
extract operations in one step that list the same property fail validation.
Only listed properties are published; extracted fields a schema does not list
are dropped. When the step succeeds with outputs, stdout is one JSON object of
those outputs. Operation progress is written to stderr.

### Browser options

`with.browser` accepts `headless` (default `true`), `executable` (otherwise
`CHROME_PATH` or an installed Chrome), `viewport` `{width, height}`, `proxy`
(unauthenticated), `allowed_domains`, `screenshots`, and `profile`.

### Allowed domains

The browser runtime applies `allowed_domains` to the page's HTTP(S) requests,
including scripts, images, and API calls, so a site's CDN and sign-in hosts
must be listed. WebSocket connections are not covered, and the runtime's check
can be bypassed. `example.com` matches only that host; `*.example.com` matches
its subdomains but not `example.com`. An entry with a scheme, port, or path, a
`*` other than a leading `*.`, or fewer than two labels fails validation.

Dagu itself checks only the page URL: a `goto` or `with.url` outside the list
fails before navigating, and after the start URL and after every operation the
step fails when a redirect or an action left the allowed domains. Pages without a network
host, such as `about:blank`, are not checked.

### Artifacts

A DAG with a browser action enables artifact storage unless it sets
`artifacts.enabled: false`. Files are written under `browser/<step id>/` in the
run's artifacts directory and are not masked.

| `screenshots` | Automatic screenshots |
| --- | --- |
| `on_failure` (default) | When the step fails. |
| `final` | When the step fails, and at the end of a successful step. |
| `each` | After every operation, plus the `final` ones. |
| `never` | None. |

With artifacts disabled, no automatic screenshots are saved, a `screenshot`
operation fails, and the browser refuses downloads.

### Downloads

Files the page downloads are saved under `browser/<step id>/downloads/` with
the name the site suggests, made unique within the directory. Only `act` and
`goto` operations start downloads. Once one has run, the step waits for running
downloads after every operation, and before it ends or pauses for an `ask` it
also waits a few seconds for a download to begin. A download may run for the
longest timeout of the acts and gotos run so far. A canceled download, or one
still running at that timeout, fails the step.

### Dialogs

The step accepts every JavaScript dialog a page opens, so a dialog never
blocks the page: `alert`, `confirm`, and `beforeunload` are accepted, and a
`prompt` is answered with its default text. Each accepted dialog appears in
the timeline and the step log after the operation that opened it.

### Replay cache

With `with.cache` true (the default), a successful `act` records the actions it
performed. A later run of the same step on the same host replays them without a
model request when the operation position, instruction, and page URL without
query or fragment match. When a replay fails, the step asks the model again and
records the new actions. `act.cache: false` disables the cache for one
operation.

The cache covers `act` only. `extract` and model-judged conditions make model
requests on every run. A replay that finds an element at the recorded location
succeeds even if the page layout changed and a different element is now there.

### Profiles

`browser.profile` names a persistent browser profile kept on the executing
host. Cookies and storage survive across runs on that host. Runs that use the
same profile run one at a time; a run fails immediately when another run
waiting for input holds the profile.

### Human input

An `ask` operation puts the step in `Waiting` with a pending question, keeps the
browser open, and ends the step execution. Answering the question from the Web
UI or REST API resumes the step in the same browser, on the host that holds it,
at the operation after the `ask`. The answer is available to later `act`
instructions as `%<as>%`. Rejecting the question fails the step. The browser
stays open for `ask.timeout` (default one hour); an answer after that fails the
step.

Answers are stored in the run's history like other human input.

On Windows, a step with an `ask` operation fails before starting a browser,
because the browser cannot outlive the step process there.

## Errors

A missing `with.do`, `with.url` for `browser.extract`, `with.instruction`, or
`with.schema` fails validation with a diagnostic naming the field. An operation
or condition that sets zero or several keys fails validation. A step fails when
an `act` does not complete, an `expect` does not hold, a selector does not
appear before the timeout, a download does not finish, the page leaves the
allowed domains, the browser stops responding, or the browser cannot be
started.

## Examples

```yaml
secrets:
  - name: SHOP_COUPON
    provider: env
    key: SHOP_COUPON

llm:
  provider: anthropic
  model: claude-sonnet-5

steps:
  - id: checkout
    action: browser.run
    with:
      url: https://shop.example.com/cart
      browser:
        # Every host the site loads from, including CDNs.
        allowed_domains: ["*.example.com"]
      variables:
        coupon: ${SHOP_COUPON}
      do:
        - act: Enter %coupon% in the coupon field and apply it
        - act: Click the Place order button
        - expect: {text: Order confirmed}
        - extract:
            instruction: The order number
            schema:
              type: object
              properties:
                order_number: { type: string }

  - id: record
    depends: checkout
    run: echo "${steps.checkout.outputs.order_number}"
```
