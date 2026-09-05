# Spec: HTTP Request Action

## Status

Implemented.

This spec defines conformance behavior for the built-in `http.request`
action.

## Scope

This spec defines `http.request`, which makes an HTTP request and reports
its result.

This spec covers:

- the required fields `with.method` and `with.url`
- `with.headers` and `with.query`
- `with.body`, and that it cannot be combined with `with.form` or
  `with.files`
- `with.form` and `with.files`, sent together as one multipart request
- relative `with.files` paths resolving against the step's working
  directory
- `with.silent`, and that it only suppresses status/header output on a
  successful (2xx) response
- `with.format: json` (equivalently `with.json: true`), producing a
  structured result instead of the default text output
- `with.output`, writing the response body to a file instead of stdout,
  with the same relative-path resolution as `with.files`
- `with.skip_tls_verify`
- that a non-2xx response still writes its result (to stdout or to
  `with.output`) before the step is reported as failed
- validation and runtime errors

This spec does not define:

- direct `type: http` authoring with a command-shaped `command`/`args`
  invocation, which exists as a separate path from the `action:
  http.request` shorthand this spec documents
- `with.debug`'s output
- exact MIME structure, header ordering, or other SMTP-adjacent wire
  details beyond what a step author observes in stdout, a file, or the
  step's error

## Goal

Workflow authors call an HTTP API as a DAG step, without shelling out to
a command executor running `curl` or an equivalent.

## Behavior

### Request

`with.method` and `with.url` are required. `with.headers` and
`with.query` add request headers and query-string parameters.

`with.body` sets the raw request body. It cannot be combined with
`with.form` or `with.files` (see "Errors"). `with.form` and `with.files`
send a `multipart/form-data` request: `with.form` supplies plain field
values, `with.files` supplies field values whose content comes from a
local file (keyed by field name, valued by file path); both can be set
together in one request. A `with.files` path is resolved relative to the
step's working directory when it is not already absolute -- the same
resolution `with.output` uses.

### Result output

By default, the response status line and headers are printed to stdout,
followed by the response body. `with.silent: true` suppresses the status
line and headers when the response is successful (2xx); a non-2xx
response always prints them, regardless of `with.silent`.

`with.format: json` (or `with.json: true`) prints a single JSON object to
stdout instead: `status_code`, `headers`, and `body` (the response body,
parsed as JSON when possible). `headers` and `status_code` are included
only when the response is not successful, or `with.silent` is not set.

`with.output` writes the response body to the named file instead of
stdout (still subject to the same relative-path resolution as
`with.files`), replacing any existing file at that path. This happens
whether or not the response was successful: a non-2xx response still
writes the file (or the stdout result), and only then does the step
report failure.

### TLS

`with.skip_tls_verify: true` disables TLS certificate verification for
the request. It is `false` by default, so a self-signed or otherwise
untrusted server certificate fails the request.

## Errors

### Validation

- `with.method` or `with.url` missing entirely: rejected at
  DAG-build-time validation, with an error containing `"with.method is
  required"` or `"with.url is required"`.
- `with.method` or `with.url` present but not a non-empty string:
  rejected at DAG-build-time validation, with an error containing
  `"with.method must be a non-empty string"` or `"with.url must be a
  non-empty string"`.

### Runtime

- `with.body` combined with `with.form` or `with.files`: a runtime
  error, surfaced only when the step actually runs, containing `"body
  cannot be combined with form or files"` -- this is not checked at
  DAG-build time.
- The response status is not 2xx: the step fails, with an error
  containing `"http status code not 2xx: <code>"`, after the response
  has already been written (see "Result output").
- The request cannot be sent at all (connection refused, TLS
  verification failure, and so on): a runtime error wrapping the
  underlying transport's error.

## Related Specs

- Run context: [Spec 017: Built-In Run Context](017-built-in-run-context.md)

## Examples

Call a JSON API and use its structured result:

```yaml
steps:
  - action: http.request
    with:
      method: GET
      url: https://api.example.com/status
      format: json
```

Upload a file as multipart form data:

```yaml
steps:
  - action: http.request
    with:
      method: POST
      url: https://api.example.com/upload
      form:
        description: nightly build
      files:
        file: ./dist/build.tar.gz
```

Save a response body to a file:

```yaml
steps:
  - action: http.request
    with:
      method: GET
      url: https://example.com/report.csv
      output: ./report.csv
```
