# Specs

Specs describe data-plane behavior. They are written for implementers and for black-box conformance tests.

Specs are implemented incrementally. A partial implementation must state the behavior it covers and must add black-box coverage for that behavior. Documented behavior remains normative even when product implementation has not caught up.

## Status

This table describes conformance status.
`Not implemented` means the spec documents target conformance behavior.
It must not be treated as product behavior until implementation catches up.

| Spec | Status |
| --- | --- |
| [001: Project](001-project.md) | Not implemented |
| [002: YAML Schema](002-yaml-schema.md) | Implemented |
| [003: Value Resolution and Field Evaluation](003-value-resolution.md) | Implemented |
| [004: Value Resolution Consts](004-value-resolution-consts.md) | Implemented |
| [005: Value Resolution Params](005-value-resolution-params.md) | Implemented |
| [006: Value Resolution Env](006-value-resolution-env.md) | Implemented |
| [007: Value Resolution Steps](007-value-resolution-steps.md) | Implemented |
| [009: Step Reference](009-step-reference.md) | Implemented |
| [011: Dynamic Evaluation](011-dynamic-evaluation.md) | Implemented |
| [012: Step Outputs](012-step-outputs.md) | Implemented |
| [013: Step Run](013-step-run.md) | Partially implemented |
| [014: Step Run Command](014-step-run-command.md) | Implemented |
| [015: Step Run Script](015-step-run-script.md) | Implemented |
| [017: Built-In Run Context](017-built-in-run-context.md) | Implemented |
| [018: Parallel Fan-Out and Foreach Iteration](018-parallel-and-foreach.md) | Implemented |

**Writing guidelines:**

- Describe observable behavior in a way that cannot be misinterpreted.
- Write specs as observable contracts, not explanations of implementation.
- Keep each file focused on one topic.
- Keep each spec scoped to one owner and explicitly say what it does not define.
- Every numbered spec must include a `Status` section.
- Every numbered spec must include a `Scope` section.
- Name numbered specs with numeric prefixes to show reading order, such as `001-language.md`.
- Define observable behavior, errors, side effects, and lifecycle effects.
- Include examples that can be used as test fixtures.
- Do not require control-plane behavior.
- Remove obsolete functionality or behavior unless an owning spec explicitly keeps it.
- Do not add tests that verify a functionality or behavior is removed.
- Spec is not implementation note. Only document normative behavior.

**Conformance test guidelines:**

- Put each spec's black-box tests in `conformance/<spec_slug>`.
- Put workflow examples in static YAML fixtures under `conformance/<spec_slug>/testdata`.
- Keep Go conformance tests as small tables over fixture filenames and expected outcomes.
- Do not generate DAG YAML dynamically in Go test code.
- Add a new fixture when a behavior needs a new workflow shape.
- Keep setup helpers limited to runtime files or directories that the static fixture needs.

**Each spec should document:**

| Section | Purpose |
| --- | --- |
| Scope | Behavior covered by the spec. |
| Goal | The reason this behavior needs a spec. |
| Behavior | Required behavior. |
| Errors | Invalid input, runtime failure, timeout, abort, and cleanup behavior. |
| Examples | Minimal cases that can become black-box tests. |
