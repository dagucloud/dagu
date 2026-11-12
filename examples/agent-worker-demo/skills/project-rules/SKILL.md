---
name: project-rules
description: Gives the code rules and the review checklist of this project. Use before you change code of the project and before you report the result of a change.
---

# Project rules

## Rules for a change

- Keep each function small. One function does one thing.
- Do not change a file that the task does not name.
- Never edit the files below `.github/`. The CI belongs to the maintainers.
- Run `./scripts/test.sh` before you report a result.

## Review checklist

1. Read the diff of the change. Each changed line must serve the task.
2. Check that the tests pass.
3. Check that the change adds no new file that the task does not need.
4. Write a summary of one sentence. Name each changed file.
