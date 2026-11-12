---
name: commit-message
description: Writes the commit message of a change in the format of this project. Use when you must name, describe or commit a change.
---

# Commit message

1. Read `references/format.md` for the format and the list of types.
2. Write the subject line as `<type>(<scope>): <summary>`.
3. Check the line with `scripts/check-message.sh "<subject line>"`. The script only reads its argument.

The format is in a reference file and not in this body. A task that makes no commit never loads it.
