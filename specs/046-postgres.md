# Spec: PostgreSQL Actions

## Status

Implemented.

This spec defines conformance behavior for the built-in `postgres.query`
and `postgres.import` actions.

## Scope

This spec defines `postgres.query`, which runs a single SQL statement
against a PostgreSQL database, and `postgres.import`, which bulk-loads a
CSV/TSV/JSONL file into a table.

This spec covers:

- the required `with.dsn` and `with.query` (for `postgres.query`) or
  `with.import` (for `postgres.import`) fields
- `with.params`, as a positional array (`$1`, `$2`, ... placeholders) or a
  named map (`:name` placeholders), and that named-parameter syntax
  collides with PostgreSQL's `::type` cast syntax
- `with.output_format` (`jsonl` by default, or `csv` with `with.headers`
  and `with.null_string`)
- `with.max_rows`, and that it limits rows written to the result, not
  rows the query itself may process
- `with.advisory_lock` and `with.transaction`/`with.isolation_level`
  being accepted alongside a query
- `postgres.import`'s `input_file`, `table`, `on_conflict`, `dry_run`,
  and the JSON import metrics it writes to stderr
- validation and runtime errors

This spec does not define:

- `sqlite.query`/`sqlite.import`, which share this package's
  configuration shape but use a different driver and DSN format
- direct `type: postgres` authoring with a multi-statement SQL script
  (the `run:`/script form), which is a separate path from the single
  `with.query` statement the `action: postgres.query` shorthand this spec
  documents supports
- transaction isolation level semantics beyond that a recognized value is
  accepted
- `with.streaming`/`with.output_file` (writing query results to a file
  instead of stdout)
- `postgres.import`'s `columns`, `update_columns`, `skip_rows`, and
  `batch_size` fields
- connection pooling, retry, and timeout behavior for establishing the
  database connection itself

## Goal

Workflow authors query or bulk-load a PostgreSQL database as a DAG step,
without shelling out to `psql` through a command executor.

## Behavior

### Query execution

`with.query` (required) is a single SQL statement. `with.params` supplies
its parameters: a JSON/YAML array binds positionally to `$1`, `$2`, ...
placeholders already in the query; a map binds by name to `:name`
placeholders, which are rewritten to `$N` placeholders before the query
runs. Because both PostgreSQL's `::type` cast syntax and this named-param
syntax start with a colon, a query using named parameters must avoid `::`
casts (use `CAST(x AS type)` instead), or a cast target such as `::text`
is misread as an undefined parameter named `text`.

A statement whose result set is rows (a `SELECT`, or any statement with a
`RETURNING` clause) has each row written to stdout in `with.output_format`
(`jsonl` by default: one JSON object per row). `with.max_rows`, when set,
stops writing further rows once that many have been written -- it does
not limit how many rows the query itself matches or fetches from the
server. A statement without a result set (`INSERT`/`UPDATE`/`DELETE`
without `RETURNING`) produces no row output.

`with.transaction: true` wraps the statement in a transaction;
`with.isolation_level` sets its isolation level. `with.advisory_lock`
acquires a named PostgreSQL advisory lock before the statement runs and
releases it afterward.

Every execution (and, for `postgres.import`, the import as a whole) also
writes one JSON object of execution metrics to stderr.

### Import

`postgres.import` (required fields `with.import.input_file` and
`with.import.table`) reads rows from `input_file` (format auto-detected
from its extension, or set explicitly) and inserts them into `table`.
`with.import.on_conflict: ignore` (with `with.import.conflict_target` set
to the conflicting column) skips a row that conflicts with an existing
row instead of failing the import. `with.import.dry_run: true` reads and
counts every row without writing any of them to the table.

The import's metrics (written to stderr, as with `postgres.query`)
include `rows_read` and `rows_imported`; `dry_run: true` still counts
rows into `rows_imported`, since it reports what would have been
imported.

## Errors

### Validation

- `with.query` missing (for `postgres.query`): rejected at DAG-build-time
  validation, with an error containing `"with.query is required"`.
- `with.import` missing (for `postgres.import`): rejected at
  DAG-build-time validation, with an error containing `"with.import is
  required"`.

### Runtime

- `with.dsn` missing: a runtime error, surfaced only when the step
  actually runs, containing `"dsn is required"` -- this is not checked at
  DAG-build time, and fails before any connection is attempted.
- The query fails (a syntax error, a reference to a table or column that
  does not exist, a constraint violation, and so on): a runtime error
  wrapping the underlying driver's error.

## Related Specs

- Run context: [Spec 017: Built-In Run Context](017-built-in-run-context.md)

## Examples

Query with positional parameters:

```yaml
steps:
  - action: postgres.query
    with:
      dsn: postgres://user:pass@localhost:5432/mydb?sslmode=disable
      query: "SELECT * FROM users WHERE status = $1"
      params:
        - active
```

Bulk-load a CSV file, skipping rows that already exist:

```yaml
steps:
  - action: postgres.import
    with:
      dsn: postgres://user:pass@localhost:5432/mydb?sslmode=disable
      import:
        input_file: ./users.csv
        table: users
        on_conflict: ignore
        conflict_target: id
```
