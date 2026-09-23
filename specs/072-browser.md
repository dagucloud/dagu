# Spec: Browser Actions

## Status

Partially implemented.

Conformance covers extraction into outputs, a multi-operation run, the replay
cache, the secret check, allowed domains, and validation. Waiting for input and
resuming the same browser need the REST API and are covered by integration
tests. Model prompts, page interaction internals, and profile locking belong to
executor tests.

## Scope

This spec defines the `browser.extract` and `browser.run` action boundary: the
`with` contract, model configuration, outputs, artifacts, the replay cache, and
human input. It does not define how a model chooses page elements or the
browser runtime's prompts.

## Goal

Workflow authors can drive a website that has no API from a step: act on pages
in natural language, extract structured data into step outputs, and pause for a
person when the site asks for input.

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
- `expect`: a statement about the page. The step fails when the model judges it
  false, with the model's reason.
- `wait`: `{selector}` waits until the element is visible; `{duration}` pauses.
- `screenshot`: a name; the page is saved as a PNG run artifact.
- `ask`: `{prompt, as, timeout}` waits for a person's answer (see Human input).

Any operation may set `when`, a statement about the page; the operation is
skipped unless the model judges it true. Any operation may set `timeout`, a
duration such as `30s`; the default is two minutes.

### Model

A browser step uses the DAG-level `llm` block. `with.llm` replaces it entirely,
with the same shape and providers as the DAG-level block. A browser step with no
model configuration fails validation. When several models are listed, each
model request tries them in order.

### Variables and secrets

`with.variables` maps names to values. An `act` instruction references them as
`%name%`. The browser receives the values; requests to the model carry only the
names. Secret and variable values are masked in text sent to the model, in the
step log, and in the timeline.

The step fails before starting a browser when an `act`, `extract`, `expect`,
`when`, or `ask` text contains the resolved value of a declared secret of four
or more characters.

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

With `allowed_domains`, navigation to a host outside the listed domains fails
the step. A domain matches itself and its subdomains; `*.example.com` matches
subdomains of `example.com`.

### Artifacts

A DAG with a browser action enables artifact storage unless it sets
`artifacts.enabled: false`. Files are written under `browser/<step id>/` in the
run's artifacts directory. `screenshots: on_failure` (the default) saves a
screenshot when the step fails and when it succeeds; `each` also saves one after
every operation; `never` saves none automatically. Downloads started by the page
are saved under `browser/<step id>/downloads/`. With artifacts disabled, no
automatic screenshots or downloads are saved, and a `screenshot` operation fails.

### Replay cache

With `with.cache` true (the default), a successful `act` records the actions it
performed. A later run of the same step replays them without a model request
when the operation position, instruction, and page URL without query or
fragment match. When a replay fails, the step asks the model again and records
the new actions. `act.cache: false` disables the cache for one operation.

### Profiles

`browser.profile` names a persistent browser profile kept on the executing
host. Cookies and storage survive across runs. Runs that use the same profile
run one at a time; a run fails immediately when another run waiting for input
holds the profile.

### Human input

An `ask` operation puts the step in `Waiting` with a pending question, keeps the
browser open, and ends the step execution. Answering the question from the Web
UI or REST API resumes the step in the same browser at the operation after the
`ask`. The answer is available to later `act` instructions as `%<as>%`.
Rejecting the question fails the step. The browser stays open for
`ask.timeout` (default one hour); an answer after that fails the step.

Answers are stored in the run's history like other human input.

## Errors

A missing `with.do`, `with.url` for `browser.extract`, `with.instruction`, or
`with.schema` fails validation with a diagnostic naming the field. An operation
that sets zero or several operation keys fails validation. A step fails when an
`act` does not complete, an `expect` is false, a selector does not appear before
the timeout, or the browser cannot be started.

## Examples

```yaml
llm:
  provider: anthropic
  model: claude-sonnet-5

steps:
  - id: checkout
    action: browser.run
    with:
      url: https://shop.example.com/cart
      variables:
        coupon: SPRING
      do:
        - act: Enter %coupon% in the coupon field and apply it
        - act: Click the Place order button
        - expect: The page shows the order confirmation
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
