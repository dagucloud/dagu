# Spec: Wiki Page File Format

## Status

Implemented.

## Scope

This spec defines:

- the on-disk shape of a Wiki page
- Wiki page identifier rules
- recognized frontmatter fields
- wiki-link syntax and its exclusion rules
- the colon-scheme reservation for wiki-link targets
- the `dagu-*` fenced-code-language reservation

This spec does not define:

- REST, Web UI, MCP, notification, authentication, or authorization behavior
- Wiki storage layout, revision history, or attachments
- search, listing, or backlink query semantics
- rendering behavior of any client

## Goal

A Wiki page is a plain Markdown file that remains valid and portable outside
Dagu. Structured metadata and cross-references use conventions layered onto
standard Markdown, so external editors and git tooling keep working.

## Related Specs

020-mcp-server.md
021-mcp-read-tool.md
022-mcp-change-tool.md

## Terms

- **Wiki page**: a Markdown file managed under the configured Wiki directory.
- **Wiki page ID**: the slash-separated, extension-less path identifying a
  Wiki page.
- **frontmatter**: an optional YAML block delimited by `---` lines at the top
  of a Wiki page.
- **wiki link**: a `[[target]]` reference inside Wiki page content.
- **scheme target**: a wiki-link target containing a colon, addressing a
  non-Wiki resource.

## Behavior

### Wiki page identity

- A Wiki page must be stored as `<id>.md` under the Wiki directory.
- Each ID segment must start with an alphanumeric character or underscore and
  may contain alphanumeric characters, underscores, dots, hyphens, and spaces.
- A segment must not end with a space or a dot, and must not use a Windows
  reserved device name.
- An ID must not exceed 252 characters and must not contain a colon.

### Frontmatter

- Frontmatter is optional. When present it must start on the first line with
  `---` and end with a `---` line.
- Recognized fields are `title` (scalar), `description` (scalar), and `tags`.
- `tags` must accept either a YAML sequence of scalars or a single
  comma-separated scalar.
- Tag values are trimmed; empty values and case-insensitive duplicates are
  dropped, preserving authored order and casing.
- Unrecognized frontmatter fields must be preserved: Wiki page content
  round-trips byte-for-byte through read and write, including frontmatter.
- A malformed frontmatter block, or a malformed single field such as `tags`,
  must not invalidate the Wiki page; unaffected fields keep their values and
  the title falls back to the last ID segment.

### Wiki links

- The forms are `[[target]]`, `[[target#anchor]]`, `[[target|label]]`, and
  `[[target#anchor|label]]`.
- A target without a colon is a Wiki page reference, resolved relative to the
  containing page's workspace scope; a target equal to a full stored ID
  also resolves.
- A target containing a colon is a scheme target. The `dag:` scheme addresses
  a DAG by name: `[[dag:name]]`. Other schemes are reserved.
- Wiki links inside fenced code blocks and inline code spans must be inert.
- A wiki link whose target is empty after trimming is not a link.
- A leading exclamation mark marks an embed: `![[name.png]]` references an
  attachment of the containing Wiki page and renders it inline. The label
  position supplies alternate text. Embeds are not page links and must
  not appear in the link graph. An embed whose target contains a slash or a
  colon is not supported and degrades to a plain wiki link.

### Reserved fenced languages

- Fenced code blocks whose info string starts with `dagu-` are reserved for
  Dagu-rendered blocks. `dagu-info` renders DAG definition details and
  `dagu-run` renders an executable run action; clients without support must
  fall back to showing the block as plain code.

## Errors

| Failure class | Required diagnostic |
| --- | --- |
| ID with invalid segment, length, or reserved name | operation rejected with an invalid-ID error naming the rule |
| malformed frontmatter block | none; Wiki page loads with fallback title |
| malformed `tags` value | none; tags are empty, other fields keep values |
| wiki link inside code | none; text stays inert |

## Examples

````markdown
---
title: ETL Runbook
description: Recovery steps for the nightly ETL.
tags: [ops, runbook]
---

# ETL Runbook

Status: [[dag:daily-etl|nightly ETL]]

If the load fails, follow [[guides/reload#manual-steps|the reload guide]].

```dagu-run
dag: daily-etl
label: Retry today's load
```
````
