# Spec: Parallel Fan-Out and Foreach Iteration

## Status

Partially implemented.

Implemented `parallel` behavior covers:

- `parallel` on `action: dag.run` and `action: dag.enqueue`
- string, direct-array, and object-form item sources
- default `max_concurrent: 10`
- `max_concurrent` limiting active child DAG runs for `action: dag.run`
- `${ITEM}` and top-level `${ITEM.field}` item references in child DAG target
  and params
- string item source compatibility parsing
- coalescing duplicate resolved child DAG target and params into one represented
  child run
- aggregate JSON output for `dag.run` and `dag.enqueue`

Target-only behavior in this spec includes:

- the `foreach` field
- strict `parallel` validation for unknown object fields, `max_concurrent`
  upper bound, and strict integer handling where current product behavior is
  looser
- `max_concurrent` enforcement for `dag.enqueue` request creation

Target-only behavior must not be treated as product behavior until
implementation and black-box conformance tests catch up.

## Scope

This spec defines two item-expansion step fields:

- `parallel`, which expands items into represented child DAG runs or enqueue
  requests.
- `foreach`, which runs an inline step body once for each item in the same DAG
  document.

This spec covers:

- accepted field shapes
- item source parsing
- item-scoped value references
- concurrency limits
- parent step status
- aggregate outputs
- validation and runtime errors

This spec does not define:

- graph DAG scheduling of independent top-level steps
- nested-array step shorthand for parallel branches
- the legacy `call` and `params` aliases as normative syntax
- scheduler, queue worker, coordinator, storage, API, or UI internals
- how a child DAG or body step executes after the item-specific inputs have
  been resolved
- action-specific behavior other than the `dag.run` and `dag.enqueue` fan-out
  surfaces named here

Compatibility aliases may continue to exist, but conformance for this spec is
measured against the canonical syntax shown in the examples.

## Goal

Workflow authors can express item-based fan-out without guessing whether an
item is passed to a child DAG, to an inline body, to a shell, or to the queue.

`parallel` keeps the existing child-DAG fan-out contract stable.

`foreach` gives workflow authors an inline iteration construct for cases where
creating a separate child DAG only to process one item would add avoidable
workflow structure.

## Related Specs

- YAML schema: [Spec 002: YAML Schema](002-yaml-schema.md)
- Value resolution: [Spec 003: Value Resolution and Field Evaluation](003-value-resolution.md)
- Environment values: [Spec 006: Value Resolution Env](006-value-resolution-env.md)
- Step output references: [Spec 007: Value Resolution Steps](007-value-resolution-steps.md)
- Step identity: [Spec 009: Step Reference](009-step-reference.md)
- Step outputs: [Spec 012: Step Outputs](012-step-outputs.md)
- Step run: [Spec 013: Step Run](013-step-run.md)

## Terms

An item source is a workflow value that expands to zero or more items.

An item is one expanded value from an item source.

An item slot is the zero-based position of an item in the expanded item list.

An item key is the stable string identity for one item slot inside one
`foreach` step.

An item body is the inline `steps` list under `foreach`.

An item run is one represented child DAG run or enqueue request created by
`parallel` after item-specific child DAG target and params are resolved.

## Behavior

### Common Behavior

#### Item Ordering

- Item sources are expanded after the owning step's dependencies have reached
  the status required for the owning step to start.

- Static YAML arrays are expanded in YAML array order.

- JSON arrays are expanded in JSON array order.

- A spec section below must explicitly say when consumers may rely on aggregate
  output array order.

- Starting and completion order are not guaranteed unless an owning action spec
  explicitly says otherwise.

#### Concurrency

- For target conformance, `max_concurrent` limits the number of represented
  child runs, enqueue requests, or item bodies that may be active at the same
  time for the owning expansion construct.

- `max_concurrent` must be an integer from `1` through `1000`.

- When `max_concurrent` is omitted, Dagu uses `10`.

- Current implemented `parallel` behavior enforces `max_concurrent` for active
  `dag.run` child DAG runs.

- `max_concurrent` enforcement for `foreach` and for `dag.enqueue` request
  creation is target-only behavior.

- `max_concurrent` does not limit normal top-level graph scheduling outside the
  owning expansion step.

#### Value Resolution

- Item source strings are value-resolved before expansion.

- Item-scoped references are available only in the fields explicitly named by
  the `parallel` or `foreach` sections below.

- Item-scoped references do not create dependencies.

- References to prior top-level step outputs still require normal dependency
  ordering under Spec 007.

- Dynamic evaluation is not run for item-scoped references unless another spec
  explicitly opts in.

### Parallel

#### Field Shape

`parallel` is an optional step field.

Rules:

- A step with `parallel` must use `action: dag.run` or `action: dag.enqueue`.

- A step with `parallel` must not define `foreach`.

- `parallel` accepts a string item source:

```yaml
steps:
  - id: fanout
    action: dag.run
    with:
      dag: process-item
    parallel: ${params.items}
```

- `parallel` accepts a direct array item source:

```yaml
steps:
  - id: fanout
    action: dag.run
    with:
      dag: process-item
    parallel:
      - item-a
      - item-b
```

- `parallel` accepts an object form with required `items` and optional
  `max_concurrent`:

```yaml
steps:
  - id: fanout
    action: dag.run
    with:
      dag: process-item
    parallel:
      items:
        - item-a
        - item-b
      max_concurrent: 2
```

- Object-form `parallel` must not contain fields other than `items` and
  `max_concurrent`.

- `parallel.items` may be a string item source or a direct array item source.

- Direct array items may be strings, numbers, or flat mappings.

- A flat mapping item represents item fields.

- Flat mapping item values may be strings, numbers, or booleans.

- Nested mapping items and nested array items are invalid for static
  `parallel` arrays.

- A `parallel` expansion must produce at least one item.

- A `parallel` expansion must not produce more than `1000` items.

#### String Item Sources

For `parallel`, a string item source supports existing compatibility forms.

Rules:

- If the resolved string is a valid JSON array, each JSON array entry is one
  item.

- If the resolved string is a valid JSON object, the whole object is one item.

- If the resolved string is not JSON, non-empty tokens become items.

- Token separators are line breaks, commas, semicolons, pipes, tabs, or runs of
  unquoted whitespace.

- Double-quoted token groups preserve whitespace inside the group.

- Empty tokens are ignored.

- An empty or whitespace-only resolved string produces zero items and therefore
  fails the `parallel` step.

#### Item Scope

For each expanded item, Dagu evaluates the child DAG target and child DAG
params with an item scope.

Rules:

- `${ITEM}` resolves to the current item.

- For a scalar item, `${ITEM}` resolves to that scalar as text.

- For an object item, `${ITEM}` resolves to a compact JSON object.

- `${ITEM.field}` resolves to a top-level field from an object item.

- `${ITEM.field}` is not defined for scalar items.

- Nested item paths such as `${ITEM.a.b}` are outside this spec.

- Item scope is available to `with.dag`.

- Item scope is available to `with.params`.

- Item scope is not available to `with.queue`.

- Item scope is not available to unrelated step fields unless another spec
  explicitly opts in.

- If `with.params` is omitted, the child DAG receives the current item as its
  whole parameter payload.

- If `with.params` is present, the resolved `with.params` value is the child
  DAG parameter payload. Workflow authors must include `${ITEM}` or
  `${ITEM.field}` there when the child needs item data.

#### Represented Child Runs

`parallel` first expands item slots, then resolves the child DAG target and
child DAG params for each item slot.

Rules:

- A represented child run is identified by the resolved child DAG target and
  resolved child DAG params.

- If more than one item slot resolves to the same child DAG target and the same
  child DAG params, current implemented behavior represents those item slots as
  one child run.

- Parent step child-run lists and `parallel` aggregate counts count represented
  child runs, not raw expanded item slots.

- Workflow authors that need one child run per item slot must make the resolved
  child DAG params distinct for each item slot.

#### `dag.run` Semantics

When `parallel` is used with `action: dag.run`, the parent step starts child
DAG runs and waits for their terminal status.

Rules:

- If every represented child DAG run is `succeeded`, the parent step is
  `succeeded`.

- If one or more represented child DAG runs fail, the parent step fails after
  the fan-out reaches a terminal state.

- If no represented child DAG run fails, aborts, is rejected, or ends in
  another non-success terminal status, and at least one represented child DAG run
  is `partially_succeeded`, the parent step is `partially_succeeded`.

- If one or more represented child DAG runs wait for external approval or input,
  the parent step waits.

- Aborting the parent step stops launching pending item runs and asks active
  child DAG runs to abort.

- A parent step timeout is handled as a parent step failure or abort according
  to the step timeout contract.

- Parent step retry retries the fan-out step. This spec does not define
  per-item retry of only failed items.

#### `dag.enqueue` Semantics

When `parallel` is used with `action: dag.enqueue`, the parent step creates or
finds queued child DAG runs and does not wait for those child runs to execute.

Rules:

- The parent step succeeds when every represented child enqueue request is
  accepted or resolves to an existing child run.

- The parent step fails when any represented child enqueue request fails.

- `with.queue` selects the queue for all item runs in that step.

- `max_concurrent` does not control later queue processing. Queue processing is
  owned by the queue configuration.

#### Aggregate Outputs

When a `parallel` step writes an aggregate output, the payload is JSON.

For `action: dag.run`, the payload object has these required fields:

| Field | Meaning |
| --- | --- |
| `summary.total` | Number of represented child DAG runs in the aggregate. |
| `summary.succeeded` | Number of represented child DAG runs counted as successful. |
| `summary.failed` | Number of represented child DAG runs not counted as successful. |
| `results` | Child run result objects. |
| `outputs` | Output maps from successful child DAG runs. |

Rules:

- `summary.succeeded` includes child DAG runs that are `succeeded` or
  `partially_succeeded`.

- `outputs` contains only child DAG runs counted as successful.

- A failed child DAG run contributes to `summary.failed` and must not contribute
  an output map to `outputs`.

- Consumers must not infer item slot identity from `results` or `outputs` array
  position unless a later spec adds an explicit ordering guarantee.

For a `parallel` step using `action: dag.enqueue`, the payload object has these
required fields, including when expansion or duplicate coalescing leaves exactly
one represented enqueue request:

| Field | Meaning |
| --- | --- |
| `summary.total` | Number of represented enqueue requests. |
| `summary.queued` | Number of child runs newly queued by this step. |
| `runs` | Per-child enqueue result objects. |

Each `runs` entry must include:

| Field | Meaning |
| --- | --- |
| `name` | Child DAG name. |
| `dagRunId` | Child DAG-run id. |
| `queue` | Queue selected for that child run. |
| `status` | Status observed after the enqueue request. |

Each `runs` entry may include:

| Field | Meaning |
| --- | --- |
| `params` | Child parameter payload. |
| `alreadyExists` | True when the child run already existed. |

### Foreach

#### Field Shape

`foreach` is an optional step field.

Rules:

- A step with `foreach` must not define another execution selector, such as
  `run`, `action`, `command`, `script`, `call`, or `parallel`.

- `foreach` must be an object.

- `foreach.items` is required.

- `foreach.steps` is required.

- `foreach.steps` must be a non-empty sequence of step objects.

- `foreach.as` is optional and defaults to `item`.

- `foreach.as` must match `^[A-Za-z][A-Za-z0-9_]*$`.

- `foreach.as` must not be `index` or `key`.

- `foreach.key` is optional.

- `foreach.max_concurrent` is optional and follows the common concurrency
  rules above.

- `foreach.collect` is optional.

- `foreach` must not contain fields other than `items`, `as`, `key`,
  `max_concurrent`, `steps`, and `collect`.

Example shape:

```yaml
steps:
  - id: summarize_episodes
    foreach:
      items: ${steps.list_episodes.outputs.episodes}
      as: episode
      key: ${foreach.episode.slug}
      max_concurrent: 3
      steps:
        - id: summarize
          run: ./summarize.sh ${foreach.episode.url}
          outputs:
            - name: markdown
      collect:
        markdown: ${steps.summarize.outputs.markdown}
    output: EPISODE_SUMMARIES
```

#### Item Sources

Rules:

- `foreach.items` may be a YAML array.

- `foreach.items` may be a string that resolves to a JSON array.

- A non-array JSON value is invalid for `foreach.items`.

- A non-JSON resolved string is invalid for `foreach.items`.

- An empty array is valid and produces zero item body executions.

- A `foreach` expansion must not produce more than `1000` items.

- Static YAML array items may be any JSON-compatible value.

#### Item Keys

Rules:

- If `foreach.key` is omitted, the item key is the decimal item slot index.

- If `foreach.key` is present, Dagu resolves it once per item with item scope
  available.

- The resolved item key must be a non-empty string.

- Item keys must be unique inside one `foreach` step.

- Duplicate item keys fail the `foreach` step before any item body starts.

#### Item Scope

For each item body execution, Dagu exposes structured item references.

Rules:

- `${foreach.index}` resolves to the zero-based item slot index as decimal text.

- `${foreach.key}` resolves to the item key.

- `${foreach.<as>}` resolves to the current item.

- When `as` is omitted, `${foreach.item}` resolves to the current item.

- For a scalar item, `${foreach.<as>}` resolves to that scalar as text.

- For an object or array item, `${foreach.<as>}` resolves to compact JSON.

- `${foreach.<as>.field}` resolves to a top-level object field.

- Nested item field paths such as `${foreach.item.a.b}` are outside this spec.

- Item scope is available to `foreach.key`.

- Item scope is available to every value-resolved string field inside
  `foreach.steps`.

- Item scope is available to every `foreach.collect` value.

- The uppercase `${ITEM}` item reference is owned by `parallel`. It is not a
  `foreach` item reference.

#### Body Step Scope

`foreach.steps` defines steps scoped to one item body.

Rules:

- Body step ids must be unique inside the body.

- Body step names must be unique inside the body after loading.

- Body step ids and names must not collide with top-level step ids or names
  visible to the `foreach` step.

- Body step dependencies stay inside the body.

- Body steps must not depend on top-level steps directly.

- Top-level steps must not depend on body steps directly.

- A body step can reference top-level step outputs when the top-level producer
  is ordered before the owning `foreach` step under Spec 007.

- A body step can reference an earlier body step output from the same item body
  using normal step-output reference syntax.

- Body step output references never cross item bodies.

- Top-level steps after the `foreach` step cannot reference body step outputs
  directly. They must consume the `foreach` aggregate output.

#### Collect

`foreach.collect` defines output values copied from each successful item body
into the parent aggregate output.

Rules:

- `foreach.collect` must be a mapping from output names to string expressions.

- Collect output names must match `^[A-Za-z][A-Za-z0-9_]*$`.

- Collect output names must be unique inside one `foreach` step.

- Collect expressions are value-resolved after an item body succeeds.

- Collect expressions are resolved with both item scope and that item body's
  step-output scope available.

- A collect expression that still contains a supported unresolved reference
  after item body success fails that item.

- Unsupported braced text follows Spec 003 preservation behavior.

#### Execution Semantics

Rules:

- A `foreach` step with zero items succeeds without running body steps.

- A `foreach` step with one or more items starts at most `max_concurrent` item
  bodies at a time.

- Each item body runs the same `foreach.steps` graph with that item's scope.

- Item body execution order is not guaranteed.

- The parent `foreach` step does not finish until every item body has reached a
  terminal status, unless the parent step is aborted or times out.

- If every item body succeeds, the parent `foreach` step succeeds.

- If one or more item bodies fail, the parent `foreach` step fails after all
  item bodies that can run have reached a terminal status.

- A failed item body does not prevent later item bodies from starting unless
  the parent step is aborted or times out.

- Aborting the parent `foreach` step stops launching pending item bodies and
  asks active item bodies to abort.

- Parent step retry retries the entire `foreach` step. This spec does not
  define per-item retry of only failed item bodies.

#### Aggregate Outputs

When a `foreach` step defines `output`, the captured value is a JSON object with
these required fields:

| Field | Meaning |
| --- | --- |
| `summary.total` | Number of expanded items. |
| `summary.succeeded` | Number of item bodies that succeeded. |
| `summary.failed` | Number of item bodies that failed. |
| `items` | Per-item status records. |
| `outputs` | Collect maps from successful item bodies. |

Each `items` entry must include:

| Field | Meaning |
| --- | --- |
| `index` | Zero-based item slot index. |
| `key` | Item key. |
| `status` | Item body status. |

Each successful `items` entry must include `outputs`.

Each failed `items` entry should include an error message when an error message
is available.

Rules:

- `items` is ordered by item slot index.

- `outputs` is ordered by item slot index and includes only successful item
  bodies.

- If `foreach.collect` is omitted, successful item output maps are empty.

- Dagu must not include the raw item value in the aggregate output unless a
  future spec adds an explicit field for that.

## Errors

The errors below define target conformance. The Status section identifies
implemented `parallel` behavior and target-only behavior where current product
behavior is looser.

### Parallel Errors

Validation must fail when:

- `parallel` appears on a step whose action is not `dag.run` or `dag.enqueue`.
- `parallel` and `foreach` appear on the same step.
- `parallel` has an invalid shape.
- object-form `parallel` omits `items`.
- object-form `parallel` contains an unknown field.
- `max_concurrent` is not an integer from `1` through `1000`.
- a static array item is a nested array.
- a static array item is a nested mapping.
- a static mapping item contains a value that is not a string, number, or
  boolean.

Runtime execution must fail when:

- value resolution for an item source fails.
- value resolution for a child DAG target or child params fails.
- item expansion produces zero items.
- item expansion produces more than `1000` items.
- an expanded string item source cannot be parsed according to the `parallel`
  string item source rules.
- a child DAG target cannot be loaded.
- a `dag.run` child DAG run fails.
- a `dag.enqueue` child enqueue request fails.

### Foreach Errors

Validation must fail when:

- `foreach` appears with another execution selector.
- `foreach` has an invalid shape.
- `foreach.items` is missing.
- `foreach.steps` is missing or empty.
- `foreach.as` is invalid.
- `foreach.max_concurrent` is not an integer from `1` through `1000`.
- `foreach.collect` is not a mapping from valid output names to strings.
- a body step identity collides with another body step identity.
- a body step identity collides with a visible top-level step identity.
- a body step dependency names a top-level step.
- a top-level step dependency names a body step.

Runtime execution must fail when:

- value resolution for `foreach.items` fails.
- a string `foreach.items` value does not resolve to a JSON array.
- item expansion produces more than `1000` items.
- `foreach.key` resolves to an empty string.
- two item slots resolve to the same item key.
- an item body fails.
- a collect expression fails to resolve after item body success.

### Timeout and Abort

Rules:

- Timeout and abort must not start new pending item runs or item bodies.

- Active child DAG runs or item bodies must receive the same stop signal that a
  normal running step would receive in the same lifecycle state.

- A timed-out or aborted expansion step must not be reported as succeeded.

- Partial aggregate output on timeout or abort is allowed only when the output
  clearly reports that not all items succeeded.

## Examples

### Parallel Child DAG Run

```yaml
steps:
  - id: fanout_accounts
    action: dag.run
    with:
      dag: workflows/process-account
      params:
        account_id: ${ITEM.account_id}
        region: ${ITEM.region}
    parallel:
      max_concurrent: 2
      items:
        - account_id: acct_1
          region: us-east-1
        - account_id: acct_2
          region: eu-west-1
    output: ACCOUNT_RESULTS
```

Expected behavior:

- Two child DAG runs are represented.
- Each child receives item-specific params.
- The parent waits for both child DAG runs.
- `ACCOUNT_RESULTS` is JSON with `summary`, `results`, and `outputs`.

### Parallel Child Enqueue

```yaml
steps:
  - id: enqueue_accounts
    action: dag.enqueue
    with:
      dag: workflows/process-account
      queue: background
      params:
        account_id: ${ITEM.account_id}
    parallel:
      items:
        - account_id: acct_1
        - account_id: acct_2
    output: ENQUEUE_RESULTS
```

Expected behavior:

- Two child enqueue requests are represented.
- The parent succeeds after both enqueue requests are accepted or found.
- The parent does not wait for queued child DAG execution.
- `ENQUEUE_RESULTS` is JSON with `summary` and `runs`.

### Foreach Inline Body

```yaml
steps:
  - id: list_episodes
    run: ./list-episodes.sh
    outputs:
      - name: episodes
        type: json

  - id: summarize_episodes
    depends: list_episodes
    foreach:
      items: ${steps.list_episodes.outputs.episodes}
      as: episode
      key: ${foreach.episode.slug}
      max_concurrent: 3
      steps:
        - id: fetch_transcript
          run: ./fetch-transcript.sh ${foreach.episode.url}
          outputs:
            - name: transcript_path
        - id: summarize
          depends: fetch_transcript
          run: ./summarize-ja.sh ${steps.fetch_transcript.outputs.transcript_path}
          outputs:
            - name: markdown
      collect:
        markdown: ${steps.summarize.outputs.markdown}
    output: EPISODE_SUMMARIES
```

Expected behavior:

- The `episodes` JSON array controls the number of item bodies.
- Each item body sees `${foreach.episode.*}` for its own item only.
- Each item body can pass data between body steps through normal step-output
  references.
- The top-level workflow consumes body results through `EPISODE_SUMMARIES`.

### Foreach Empty Input

```yaml
steps:
  - id: summarize_episodes
    foreach:
      items: []
      steps:
        - id: summarize
          run: ./summarize.sh ${foreach.item}
    output: EPISODE_SUMMARIES
```

Expected behavior:

- No body step runs.
- The `summarize_episodes` step succeeds.
- `EPISODE_SUMMARIES.summary.total` is `0`.

### Invalid Parallel Action

```yaml
steps:
  - id: invalid
    action: http.request
    with:
      url: https://example.com
    parallel:
      items: [a, b]
```

Expected behavior:

- Validation fails because `parallel` is only valid with `dag.run` or
  `dag.enqueue`.
