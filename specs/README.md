# Specs

Specs describe data-plane behavior. They are written for implementers and for black-box conformance tests.

Specs are implemented incrementally. A change that implements part of a spec must say which behavior it covers and must add black-box coverage for that behavior. Documented behavior that is not implemented remains target conformance work, not implied product behavior.

## Implementation Status

This table describes conformance status.
`Not implemented` means the spec documents target conformance behavior.
It must not be treated as product behavior until implementation catches up.

| Spec | Status |
| --- | --- |
| [001: Project](001-project.md) | Not implemented |
| [002: YAML Schema](002-yaml-schema.md) | Implemented |
| [003: Value Resolution](003-value-resolution.md) | Partially implemented |
| [004: Value Resolution Consts](004-value-resolution-consts.md) | Implemented |
| [005: Value Resolution Params](005-value-resolution-params.md) | Partially implemented |
| [006: Value Resolution Env](006-value-resolution-env.md) | Not implemented |
| [007: Value Resolution Steps](007-value-resolution-steps.md) | Not implemented |
| [009: Step Reference](009-step-reference.md) | Not implemented |
| [010: Field Evaluation](010-field-evaluation.md) | Not implemented |
| [011: Dynamic Evaluation](011-dynamic-evaluation.md) | Not implemented |
| [012: Step Outputs](012-step-outputs.md) | Not implemented |
| [013: Step Run](013-step-run.md) | Partially implemented |
| [014: Step Run Command](014-step-run-command.md) | Partially implemented |
| [015: Step Run Script](015-step-run-script.md) | Partially implemented |

**Writing guidelines:**

- Describe observable behavior in a way that cannot be misinterpreted.
- Write specs as observable contracts, not explanations of implementation.
- Keep each file focused on one topic.
- Keep each spec scoped to one owner and explicitly say what it does not define.
- Every numbered spec must include an `Implementation Status` section.
- Every numbered spec must include a `Scope` section.
- Name numbered specs with numeric prefixes to show reading order, such as `001-language.md`.
- Define observable behavior, errors, side effects, and lifecycle effects.
- Include examples that can be used as test fixtures.
- Do not require control-plane behavior.
- Remove obsolete functionality or behavior unless an owning spec explicitly keeps it.
- Do not add tests that verify a functionality or behavior is removed.

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
