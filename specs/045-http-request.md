# Spec: HTTP Request Action

## Status

Partially implemented.

Conformance covers one request/response exchange, silent JSON output, failed
response file handling, TLS verification, status exit codes, and required fields. Multipart encoding, output variants, and
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
  JSON value) on stdout. A failed response whose body is not JSON reports the
  body as a string. With `with.silent: true`, a successful response omits
  `status_code` and `headers`; a non-success response keeps them.
- An explicit `with.skip_tls_verify: false` rejects an untrusted server
  certificate. Setting it to `true` permits that certificate.
- `with.status_exit_codes` maps a non-2xx status code (`"404"`) or class
  (`"5xx"`) to the step exit code (0–255). An exact code wins over a class. A
  status mapped to `0` is a success, and its response is written like a 2xx
  response. Any other mapped status fails the step with the mapped code, which
  `retry_policy.exit_code` and `continue_on.exit_code` match. A non-2xx status
  not in the map exits `1`. Keys must be in `300`–`599` or `3xx`–`5xx`.

## Errors

Missing method or URL fields fail validation with a diagnostic naming the field.
A `with.status_exit_codes` key or value outside its allowed range fails
validation. A `with` field the action does not define also fails validation;
the error names the field and, when a defined field is a likely misspelling of
it, suggests that field. A non-success HTTP response fails the step unless
`status_exit_codes` maps its status to `0`. When `with.output` names an
existing file, a failed response preserves that file and writes the error body
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
