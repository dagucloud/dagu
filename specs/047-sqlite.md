# Spec: SQLite Actions

## Status

Implemented.

This spec defines conformance behavior for the built-in `sqlite.query` and
`sqlite.import` actions.

## Scope

This spec defines `sqlite.query`, which runs a single SQL statement
against a SQLite database, and `sqlite.import`, which bulk-loads a
CSV/TSV/JSONL file into a table.

This spec covers:

- the required `with.dsn` and `with.query` (for `sqlite.query`) or
  `with.import` (for `sqlite.import`) fields
- `with.dsn` as `:memory:` or a file path, and that each step opens and
  fully closes its own connection
- that a `:memory:` database is therefore isolated per step by default,
  and that `with.shared_memory: true` does not change this in practice
- `with.file_lock`, accepted without failing an uncontended connection
- `with.params`, as a positional array (`?` placeholders) or a named map
  (`:name` placeholders), including that a name used more than once in
  one query supplies the same value to each of its `?` placeholders
- `with.output_format` (`jsonl` by default, or `csv` with `with.headers`
  and `with.null_string`) and `with.max_rows`
- `with.advisory_lock` being accepted but having no effect
- `sqlite.import`'s `input_file`, `table`, `on_conflict`, `dry_run`, and
  the JSON import metrics it writes to stderr
- validation and runtime errors

This spec does not define:

- `postgres.query`/`postgres.import` -- see [Spec 046: PostgreSQL
  Actions](046-postgres.md), which shares this package's configuration
  shape but uses a different driver, DSN format, and placeholder syntax
- direct `type: sqlite` authoring with a multi-statement SQL script (the
  `run:`/script form), a separate path from the single `with.query`
  statement the `action: sqlite.query` shorthand this spec documents
  supports
- transaction isolation level semantics beyond that a recognized value is
  accepted
- `with.streaming`/`with.output_file`
- `sqlite.import`'s `columns`, `update_columns`, `skip_rows`, and
  `batch_size` fields
- the exact runtime error text or timing for a contended `with.file_lock`
  (see "Errors")

## Goal

Workflow authors query or bulk-load a local SQLite database as a DAG
step, without shelling out to the `sqlite3` CLI through a command
executor.

## Behavior

### Connections and `:memory:`

Each step opens its own connection and fully closes it when the step
finishes. `with.dsn: ":memory:"` therefore gives each step a database
that exists only for that step: a table created in one step is not
visible to a later step using the same `:memory:` DSN, even one that
`depends:` on it.

`with.shared_memory: true` rewrites `:memory:` to SQLite's shared-cache
DSN (`file::memory:?cache=shared`), which lets multiple *simultaneously
open* connections in the same process share one named in-memory
database. It does not change the isolation described above in practice:
because a shared-cache in-memory database is destroyed once its last
open connection closes, and each step's connection is fully closed before
the next step starts, a table created with `shared_memory: true` in one
step is still not visible to a later step.

A file-path `with.dsn` does not have this limitation: since the data
lives in a file rather than only in a connection's memory, it persists
across every step and every separate `dagu` invocation that uses the same
path.

`with.file_lock: true` takes an exclusive lock on the database file (at
`<path>.lock`) for the life of the connection, to serialize access across
processes.

### Query execution

`with.query` (required) is a single SQL statement. `with.params` supplies
its parameters: a JSON/YAML array binds positionally to `?` placeholders
in the query, in order; a map binds by name to `:name` placeholders,
which are rewritten to `?` placeholders before the query runs. A name
used more than once in the query (for example, `WHERE a = :x OR b = :x`)
supplies that same value again for each additional `?` it expands to.

A statement whose result set is rows (a `SELECT`, or any statement with a
`RETURNING` clause) has each row written to stdout in `with.output_format`
(`jsonl` by default: one JSON object per row). `with.max_rows`, when set,
stops writing further rows once that many have been written -- it does
not limit how many rows the query itself matches. A statement without a
result set produces no row output.

`with.advisory_lock`, if set, is accepted without error but has no
effect: SQLite has no advisory-lock mechanism, so this field (meaningful
for `postgres.query`) is silently ignored here.

Every execution (and, for `sqlite.import`, the import as a whole) also
writes one JSON object of execution metrics to stderr.

### Import

`sqlite.import` (required fields `with.import.input_file` and
`with.import.table`) reads rows from `input_file` (format auto-detected
from its extension, or set explicitly) and inserts them into `table`,
which must already exist. `with.import.on_conflict: ignore` (or
`replace`) skips (or replaces) a row that conflicts with an existing row
instead of failing the import. `with.import.dry_run: true` reads and
counts every row without writing any of them to the table.

The import's metrics (written to stderr, as with `sqlite.query`) include
`rows_read` and `rows_imported`; `dry_run: true` still counts rows into
`rows_imported`, since it reports what would have been imported.

## Errors

### Validation

- `with.query` missing (for `sqlite.query`): rejected at DAG-build-time
  validation, with an error containing `"with.query is required"`.
- `with.import` missing (for `sqlite.import`): rejected at DAG-build-time
  validation, with an error containing `"with.import is required"`.

### Runtime

- `with.dsn` missing: a runtime error, surfaced only when the step
  actually runs, containing `"dsn is required"` -- this is not checked at
  DAG-build time.
- The query fails (a syntax error, a reference to a table or column that
  does not exist, a constraint violation, and so on): a runtime error
  wrapping the underlying driver's error.
- A contended `with.file_lock` is a runtime error, but its retry timing
  and exact wording are not covered by this spec (see "Scope").

## Related Specs

- PostgreSQL counterpart: [Spec 046: PostgreSQL Actions](046-postgres.md)
- Run context: [Spec 017: Built-In Run Context](017-built-in-run-context.md)

## Examples

Query a file-backed database with positional parameters:

```yaml
steps:
  - action: sqlite.query
    with:
      dsn: "file:./app.db"
      query: "SELECT * FROM users WHERE status = ?"
      params:
        - active
```

Bulk-load a CSV file, skipping rows that already exist:

```yaml
steps:
  - action: sqlite.import
    with:
      dsn: "file:./app.db"
      import:
        input_file: ./users.csv
        table: users
        on_conflict: ignore
```
