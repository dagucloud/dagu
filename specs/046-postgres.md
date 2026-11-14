# Spec: PostgreSQL Actions

## Status

Partially implemented.

Conformance covers action configuration without a database server. Queries,
imports, transaction isolation, locking, and row serialization belong in SQL
executor unit and integration tests.

## Scope

This spec defines configuration for `postgres.query` and `postgres.import`.
It does not define PostgreSQL protocol or database semantics.

## Goal

Workflow authors can validate SQL action inputs before connecting to a database.

## Behavior

- `postgres.query` requires `with.query`.
- `postgres.import` requires `with.import`, which describes the source file and
  destination table.
- Both actions use `with.dsn` to identify the database connection.
- A relative `with.import.input_file` or `with.output_file` names a file under
  the step working directory.
- `dagu validate` accepts configured actions without a running database or a
  local import file.

## Errors

Missing query or import configuration fails validation with a diagnostic naming
the field. A missing DSN fails before a database connection is attempted.

A `with` field the action does not define fails `dagu validate`; the error
names the field and, when a defined field is a likely misspelling of it,
suggests that field.

## Examples

```yaml
steps:
  - action: postgres.query
    with:
      dsn: postgres://localhost/example
      query: SELECT 1
```
