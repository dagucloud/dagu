# Specs

Specs describe v3 data-plane behavior. They are written for implementers and for black-box conformance tests.

Specs may be implemented incrementally. A change that implements part of a spec must say which acceptance criteria it covers and must add black-box coverage for that covered behavior. Acceptance criteria that are documented but not implemented remain target conformance work, not implied current behavior.

**Writing guidelines:**

- Describe observable behavior in a way that cannot be misinterpreted.
- Keep each file focused on one topic.
- Name numbered specs with numeric prefixes to show reading order, such as `001-language.md`.
- Define public inputs, outputs, errors, side effects, and lifecycle effects.
- Include examples that can be used as test fixtures.
- Do not require control-plane behavior.
- Remove v3-obsolete public behavior instead of preserving compatibility aliases. If a legacy command, flag, field, or side effect is not part of the v3 data-plane spec, delete it from the v3 command surface and cover the removal with black-box tests.

**Each spec should document:**

| Section | Purpose |
| --- | --- |
| Scope | Behavior covered by the spec. |
| Inputs | Commands, files, env, params, and config. |
| Behavior | Required behavior. |
| Outputs | Exit code, stdout/stderr, events, result files, logs, and artifacts. |
| Errors | Invalid input, runtime failure, timeout, abort, and cleanup behavior. |
| Examples | Minimal cases that can become black-box tests. |
| Acceptance Criteria | Conditions that prove the behavior is implemented. |
