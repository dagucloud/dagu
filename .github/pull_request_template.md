## Summary

Brief description of the changes in this PR.
Include the problem and why it matters.

## Changes

- List the changes made
- One item per line

Show user-visible behavior before and after: CLI output, API fields, YAML,
or a UI screenshot.

## Core change

The mechanism in two to four sentences, and the few lines of code that
decide the new behavior (about 30 lines at most), with file and line.

## Testing

What shows the change works. For a bug fix, the test that failed before the
fix.

## Related Issues

Closes #issue_number (if applicable)

## Performance

No risk: one-line reason. Or describe the risk: frequency and what
multiplies it, stop condition, growth with DAG and run counts, bytes sent
and stored, and upgrade cost. See the Performance section in AGENTS.md.

<!--
Add only when it applies (see "Pull request body" in AGENTS.md):

## Compatibility
Persisted data, API, CLI, YAML, or config changes; downgrade safety;
coordinator and workers on different versions.

## Review guide
For diffs over about 300 lines, or changes to state transitions, component
flow, or lock/lease/event order: mechanical or generated files, decisions
to judge, and a small Mermaid diagram of the changed part.

## Security
Authentication, permissions, or secrets in logs or responses.

## Out of scope
What is left out on purpose, and follow-up pull requests.

## Agent
Optional. Agent harness, model, and reasoning effort that wrote the change,
such as `Claude Code, claude-opus-5-5, xhigh`.
-->

## Checklist

- [ ] Code follows the project style guidelines
- [ ] Self-review of the code has been performed
- [ ] Tests have been added or updated as needed
- [ ] Documentation has been updated as needed
- [ ] Changes have been tested locally

<!--
First-PR validation

- For a focused code change, run the smallest package-scoped check for the package you touched (for example, `make test TEST_TARGET=./path/to/package`). A full `make test` run is not required before opening a first PR; CI and maintainers run the full suite.
- For docs-only or example-only changes, Go setup and Go tests are not required. Note any relevant documentation or example validation in the PR description.
-->
