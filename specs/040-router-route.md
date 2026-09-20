# Spec: Router Route Action

## Status

Implemented.

This spec defines conformance behavior for the built-in `router.route`
action.

## Scope

This spec defines `router.route`, which evaluates a value against a set of
patterns and lets each pattern's target steps run only when their pattern
matches.

This spec covers:

- the `with.value` and `with.routes` fields
- matching targets run after the router step completes
- that route matching is independent per target, not first-match-wins
- fan-out: multiple targets under one pattern, and multiple patterns
  matching the same value at once
- behavior when no pattern matches
- the router step's own diagnostic output
- validation errors, and the one runtime error router.route defines of its
  own: a value that cannot be compared against a numeric route

This spec does not define:

- pattern matching syntax itself, which is the same condition/pattern
  matching used by step `precondition` (see
  [Spec 023: Preconditions](023-preconditions.md))
- signal delivery, timeout, and abort behavior beyond what already applies
  to any step (see [Spec 017: Built-In Run Context](017-built-in-run-context.md))
- authoring a router step directly with `type: router` plus top-level
  `value`/`routes` fields instead of `action: router.route`. That form
  exists and reaches the same routing behavior, but its own validation
  errors have different wording than the `action: router.route` errors
  this spec documents, because it goes through a different validation path.

## Goal

Workflow authors send execution down one or more of several named steps
based on a runtime value (an upstream step's output, a param, and similar),
without hand-writing a matching `precondition` on every candidate step.

## Behavior

### Fields

`with.value` (required) is the expression to evaluate, using the same value
resolution as any other step field.

`with.routes` (required) is a map from pattern to a list of target step
names: `{pattern: [step1, step2, ...]}`. Each pattern is matched against the
resolved value the same way a step `precondition`'s `expected` matches its
`condition` -- an exact string, a `re:`-prefixed regular expression, or a
`num:`-prefixed numeric comparison. A numeric route's threshold may be a single
whole value reference, such as `num:>=${threshold}`, resolved when the router
runs; see [Spec 023: Preconditions](023-preconditions.md).

### Target execution

Each target runs after the router when its pattern matches the resolved value
and its own preconditions pass. Matching is independent for every target.
Steps depending on a skipped target may still run; authors do not need to add
`continue_on: skipped` to routed targets.

### Fan-out and "no match" are both normal outcomes

Because each target's match is independent:

- A single pattern may list more than one target; all of them run when that
  pattern matches.
- More than one pattern may match the same value at once; every matching
  pattern's targets run. Router route is not first-match-wins.
- If no pattern matches the value, every target is skipped and the DAG-run
  still succeeds; a router with no matching route is not itself an error.

### Diagnostic output

The router step's own run writes exactly this to its stdout, which is the
step output the DAG-run's own tree render inlines:

```text
Router evaluating: <resolved value>
  <pattern 1> -> [<targets 1>]
  <pattern 2> -> [<targets 2>]
...
```

The first line is the literal text `Router evaluating:` followed by a
space and the resolved value verbatim (no quoting), then a newline. Each
route prints two leading spaces, its literal pattern text, `->` surrounded by spaces, then its
targets as a Go string-slice literal (`[target1 target2]`, brackets,
space-separated, no quotes or commas), each on its own line. Exact-pattern
routes are printed before
`re:`-pattern routes, and a `re:.*` catch-all route, if present, is always
printed last. This output does not affect which targets run; it only
reports the routing decision that the injected preconditions already make.

## Errors

The following are rejected by DAG-build-time validation, before the DAG
starts running. Each condition's error text is normative and must contain
the quoted wording below (exact surrounding phrasing may vary):

- `with.value` is missing: `"with.value is required"`.
- `with.routes` is missing: `"with.routes is required"`.
- `with.routes` is empty: `"router step requires at least one route"`.
- A route's pattern is empty: `"route pattern cannot be empty"`.
- A route's pattern starts with `num:` and the rest is not an ordering operator
  followed by a finite number: `"numeric comparison is invalid"`.
- A route's pattern starts with `re:` and the rest is empty, whitespace only, or
  not a valid Go regexp pattern: `"regexp is empty"` or `"regexp is invalid"`.
- A route lists no targets: `"has no targets"`.
- A route lists an empty target name: `"has empty target"`.
- The same step name appears as a target of more than one route:
  `"is targeted by multiple routes"`.
- A route names a target step that does not exist in the DAG:
  `"references non-existent step"`.
- A `router.route` step is used in a DAG with `type: chain`; router steps
  require `type: graph`: `"router steps require type 'graph'"`.

### Runtime

`with.value` is resolved using the same value resolution as any other step
field: a reference that cannot be resolved is left as unresolved literal text
(see
[Spec 003: Value Resolution and Field Evaluation](003-value-resolution.md)),
not a runtime error. That literal text is then matched against every route
exactly like any other resolved value -- it is not treated as inherently
unmatchable. A `re:.*` catch-all route, or any route whose pattern happens
to equal the unresolved literal text, still matches it. Beyond the numeric
rule below, a router step can only fail at runtime the way any step can
(signal delivery, cancellation; see
[Spec 017: Built-In Run Context](017-built-in-run-context.md)).

A `num:` route is the one exception to that leniency, because a numeric gate
that silently stops gating is worse than a loud failure. Rules:

- If the DAG declares any `num:` route and the resolved value is not a finite
  number, the router step itself reaches terminal status `failed` and the DAG
  run fails.
- The router writes its diagnostic output before failing, so the resolved value
  and the route table are still reported.
- No target runs. This includes targets of non-`num:` routes whose pattern
  matches the value, and a `re:.*` catch-all. A routing decision that cannot be
  evaluated is not partially carried out.
- A `re:.*` catch-all therefore does not act as a fallback for non-numeric
  input. There is also no single route pattern for a middle band such as
  `0.1 < x < 0.9`, because a route carries one pattern. A step covering either
  case states its own bounds as preconditions, which are combined with AND (see
  [Spec 023: Preconditions](023-preconditions.md)).
- Routing is unaffected when the value is a number: every route, numeric or
  not, matches independently as usual.

## Related Specs

- Precondition and pattern matching: [Spec 023: Preconditions](023-preconditions.md)
- Run context: [Spec 017: Built-In Run Context](017-built-in-run-context.md)

## Examples

Route to exactly one of two steps by an exact value match:

```yaml
steps:
  - name: pick
    action: router.route
    with:
      value: "${STATUS}"
      routes:
        ok: [handle_ok]
        error: [handle_error]
  - name: handle_ok
    run: echo ok
  - name: handle_error
    run: echo error
```

Route by pattern, with a regex catch-all:

```yaml
steps:
  - name: pick
    action: router.route
    with:
      value: "${STATUS_CODE}"
      routes:
        "re:^5\\d\\d$": [server_error]
        "re:.*": [catch_all]
  - name: server_error
    run: echo "5xx"
  - name: catch_all
    run: echo "other"
```

Route by numeric comparison. The two routes are mutually exclusive, so exactly
one target runs; adding a `re:.*` catch-all here would run its targets as well,
because routing is not first-match-wins:

```yaml
steps:
  - name: pick
    action: router.route
    with:
      value: "${CONFIDENCE}"
      routes:
        "num:>=0.9": [auto_approve]
        "num:<0.9": [human_review]
  - name: auto_approve
    run: echo approve
  - name: human_review
    run: echo review
```

A three-way split needs a middle band, which no single route pattern expresses.
The band is stated on the step that covers it, as preconditions combined with
AND:

```yaml
type: graph
steps:
  - id: auto_approve
    preconditions:
      - condition: "${CONFIDENCE}"
        expected: "num:>=0.9"
    run: echo approve
  - id: auto_reject
    preconditions:
      - condition: "${CONFIDENCE}"
        expected: "num:<=0.1"
    run: echo reject
  - id: human_review
    preconditions:
      - condition: "${CONFIDENCE}"
        expected: "num:<0.9"
      - condition: "${CONFIDENCE}"
        expected: "num:>0.1"
    run: echo review
```
