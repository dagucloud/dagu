# Spec: SQLite Actions

## Status

Partially implemented.

Conformance covers a parameterized query, CSV import followed by a query,
relative DSN, import, and output paths resolved from the step working
directory, and required fields. Locking, connection lifetime, transaction behavior, import
formats, and output permutations belong in SQL executor tests.

## Scope

This spec defines a small execution boundary for `sqlite.query` and
`sqlite.import`. It does not define SQLite's SQL dialect or database internals.

## Goal

Workflow authors can query local data and import a small file without a server.

## Behavior

- `sqlite.query` executes `with.query` against `with.dsn` and binds
  `with.params` values. A query row is available as a JSON object on stdout.
- `sqlite.import` reads the CSV file in `with.import.input_file` into the table
  in `with.import.table`. A later action using the same file DSN sees imported
  rows.
- A relative database file in `with.dsn`, such as `file:./data.db`, a relative
  `with.import.input_file`, and a relative `with.output_file` name files under
  the step working directory.

## Errors

Missing query or import configuration fails validation. A missing DSN fails
before execution accesses a database. Diagnostics identify the missing field.

A relative plain DSN whose step working directory contains `?` fails step
setup with `failed to resolve sqlite dsn`, before any database is opened.

## Examples

```yaml
steps:
  - action: sqlite.query
    with:
      dsn: ":memory:"
      query: SELECT ? + ? AS sum
      params: [2, 3]
```
