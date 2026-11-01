# Spec: ClickHouse Action

## Status

Partially implemented.

Conformance covers action configuration without a ClickHouse server. Request
construction, streaming, error reporting, and cancellation belong in executor
unit tests and in the live integration tests that run against a server.

## Scope

This spec defines `clickhouse.query`, which runs SQL against the ClickHouse
HTTP interface. It does not define ClickHouse SQL, the native TCP protocol, or
bulk data import.

## Goal

Workflow authors can run queries, DDL, and data-modifying statements on
ClickHouse, with inline or file SQL and server-side parameters, and receive
results on step output.

## Behavior

- `with.url` is the `http://` or `https://` endpoint of the server and is
  required. It may have a path but no credentials, query string, or fragment.
- `with.query` holds inline SQL. `with.file` names a `.sql` file instead; a
  relative path is resolved against the step working directory. Exactly one is
  required.
- `with.user` and `with.password` authenticate the request. They are sent only
  in the `X-ClickHouse-User` and `X-ClickHouse-Key` request headers, never in
  the URL, and the password is replaced with `***` in errors.
- `with.database` is the default database. A script of several statements
  selects it once with `USE` at the start of its session, so a `USE` in the
  script takes effect for later statements.
- The SQL may hold several statements separated by semicolons. A semicolon
  inside a string, a quoted identifier, a heredoc, or a comment does not end a
  statement; statements that are empty or hold only comments are skipped. The
  statements run one after another and share one session, so `SET`, `USE`, and
  temporary tables apply to the statements that follow. The step stops at the
  first failing statement. A single statement runs without a session. The
  executor does not close a session; the server expires it after its session
  timeout.
- `with.params` maps names to values. Each is sent as a server-side query
  parameter and bound to the `{name:Type}` placeholder of the same name; it is
  never interpolated into the SQL. A string is passed as written, a number or
  boolean as its literal, a list as an `Array` literal, and a map as a `Map`
  literal.
- `with.settings` maps ClickHouse setting names to string, number, or boolean
  values applied to every statement, for example `readonly: 1` or
  `max_execution_time: 30`. Besides the listed settings the executor sets
  only what it needs: `default_format` and, for `json`,
  `output_format_json_array_of_rows` from `output_format`, a query id, a
  session for scripts, and `http_write_exception_in_output_format=0`. The names
  `query`, `database`, `user`, `password`, `default_format`, `query_id`,
  `session_id`, `session_check`, `session_timeout`, `compress`, `decompress`,
  and `http_write_exception_in_output_format`, and names starting with
  `param_`, are rejected. Settings apply to every statement through the URL, so
  a `SET x` in a script is overridden when `x` is also listed in
  `with.settings`.
- `with.output_format` selects the result format of statements without their
  own `FORMAT` clause; a `FORMAT` clause in the SQL takes precedence.
  `jsonl` (default) writes one JSON object per row (`JSONEachRow`), `json`
  writes a JSON array of row objects, and `csv` writes `CSV`, or
  `CSVWithNames` when `with.headers` is true.
- The response of every statement is written to step stdout as it arrives,
  whether or not the statement returns rows.
- `with.timeout` is the number of seconds the step may run, 60 by default and
  0 for no limit. For an `https://` endpoint `with.tls_ca` names a PEM bundle of
  additional root CAs, and `with.tls_skip_verify` disables certificate
  verification. `with.tls_ca` is read when the step starts, not by
  `dagu validate`. Redirects are not followed.
- Each statement writes one JSON line to stderr with its number (`statement`,
  0 for the `USE` that selects `with.database`), `query_id`, `started_at`,
  `finished_at`, `duration_ms`, `bytes_returned`, `status`, and `error` on
  failure.
- Each statement has a unique query id. When the step is stopped, cancelled, or
  times out, the request is closed and the executor asks the server to stop the
  statement with `KILL QUERY` on a short separate deadline.

## Errors

- A missing `with.url`, a missing or conflicting `with.query` and `with.file`,
  a `with.url` that holds credentials, an unknown option, an invalid
  `with.output_format`, or a rejected setting or parameter name fails
  validation with a diagnostic naming the problem. They are reported without
  contacting a server.
- A response with a status other than 200 fails the step with the status and
  the error text of the server. In a multi-statement script the diagnostic
  names the failing statement. Error text kept from a response is limited to
  about 8 KiB. The server may echo parameter values in its error text.
- An error that the server reports after it began streaming a 200 response
  fails the step. When the server announces an error report with the
  `X-ClickHouse-Exception-Tag` response header, the report is removed from the
  output and its text becomes the step error. A response that ends before it is
  complete fails the step as an interrupted response.
- When the timeout expires or the step is cancelled, the step fails. Stopping
  the statement on the server is best effort; a refused or failed `KILL QUERY`
  does not change the step outcome.

## Examples

```yaml
steps:
  - action: clickhouse.query
    with:
      url: https://clickhouse.example.com:8443
      user: etl
      password: ${CLICKHOUSE_PASSWORD}
      database: analytics
      query: |
        INSERT INTO daily SELECT * FROM raw WHERE day = {day:Date};
        SELECT count() AS rows FROM daily WHERE day = {day:Date}
      params:
        day: "2024-01-02"
      settings:
        max_execution_time: 300
```

```yaml
steps:
  - action: clickhouse.query
    with:
      url: http://localhost:8123
      file: sql/rollup.sql
      output_format: csv
      headers: true
```
