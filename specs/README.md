# Specs

Specs describe data-plane behavior. They are written for implementers and for black-box conformance tests.

Specs may be implemented incrementally. A change that implements part of a spec must say which acceptance criteria it covers and must add black-box coverage for that covered behavior. Acceptance criteria that are documented but not implemented remain target conformance work, not implied current behavior.

**Writing guidelines:**

- Describe observable behavior in a way that cannot be misinterpreted.
- Write specs as observable contracts, not explanations of implementation.
- Keep each file focused on one topic.
- Keep each spec scoped to one owner and explicitly say what it does not define.
- Every numbered spec must include a `Scope` section.
- Name numbered specs with numeric prefixes to show reading order, such as `001-language.md`.
- Define public inputs, outputs, errors, side effects, and lifecycle effects.
- Include examples that can be used as test fixtures.
- Make acceptance criteria black-box testable.
- Do not require control-plane behavior.
- Remove obsolete functionality or behavior instead of preserving compatibility.
- Do not add tests that verify a functionality or behavior is removed.

**Each spec should document:**

| Section | Purpose |
| --- | --- |
| Scope | Behavior covered by the spec. |
| Goal | The reason this behavior needs a spec. |
| Inputs | Commands, files, env, params, and config. |
| Behavior | Required behavior. |
| Outputs | Exit code, stdout/stderr, events, result files, logs, and artifacts. |
| Errors | Invalid input, runtime failure, timeout, abort, and cleanup behavior. |
| Examples | Minimal cases that can become black-box tests. |
| Acceptance Criteria | Conditions that prove the behavior is implemented. |
