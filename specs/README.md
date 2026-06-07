# Specs

Specs describe behavior.

- describe observable behavior in a way that can not be misinterpreted
- keep each file focused on one topic
- name spec files with numeric prefixes to show reading order, such as `001-language.md`
- define public inputs, outputs, errors, side effects, and lifecycle effects
- include examples that can be used as test fixtures
- do not require control-plane behavior

Each spec should document:

- scope: what behavior the spec covers
- inputs: commands, files, env, params, and config
- behavior: what must happen
- outputs: exit code, stdout/stderr, events, result files, logs, and artifacts
- errors: invalid input, runtime failure, timeout, abort, and cleanup behavior
- examples: minimal cases that can become black-box tests
- acceptance criteria: conditions that prove the behavior is implemented
