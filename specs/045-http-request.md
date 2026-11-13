# Spec: HTTP Request Action

## Status

Partially implemented.

Conformance covers one request/response exchange, silent JSON output, failed
response file handling, TLS verification, and required fields. Multipart encoding, output variants, and
transport internals belong in executor unit and integration tests.

## Scope

This spec defines the `http.request` action boundary. It does not define the
HTTP protocol, connection pooling, or every response formatting option.

## Goal

Workflow authors can make an HTTP request and consume its result.

## Behavior

- `with.method` and `with.url` are required.
- `with.headers` and `with.query` supply request headers and query parameters.
- With `with.format: json`, a successful JSON response produces `status_code`
  (integer), `headers` (header-name to string-array object), and `body` (the parsed
  JSON value) on stdout. With `with.silent: true`, a successful response omits
  `status_code` and `headers`; a non-success response keeps them.
- An explicit `with.skip_tls_verify: false` rejects an untrusted server
  certificate. Setting it to `true` permits that certificate.
- `with.status_exit_codes` maps an HTTP status code (`"404"`) or a class
  (`"5xx"`) to the step exit code (0–255). An exact code wins over a class. The
  status of a 2xx response is unaffected. For a non-2xx response covered by the
  map, the step exits with the mapped code instead of the default `1`; a mapped
  value of `0` makes the step succeed. A status not present in the map keeps
  today's behaviour. Validation rejects keys outside `100`–`599` or `1xx`–`5xx`
  and values outside `0`–`255`.

## Errors

Missing method or URL fields fail validation with a diagnostic naming the field.
A `with.status_exit_codes` key or value outside its allowed range fails
validation. A `with` field the action does not define also fails validation;
the error names the field and, when a defined field is a likely misspelling of
it, suggests that field. A non-success HTTP response fails the step unless
`status_exit_codes` maps its status to `0`. When `with.output` names an
existing file, an error response preserves that file and writes the error body
to stdout.

## Examples

```yaml
steps:
  - action: http.request
    with:
      method: GET
      url: https://example.com/status
      format: json
```
