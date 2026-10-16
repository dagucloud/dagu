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

## Errors

Missing method or URL fields fail validation with a diagnostic naming the field.
A non-success HTTP response fails the step. When `with.output` names an existing
file, an error response preserves that file and writes the error body to stdout.

## Examples

```yaml
steps:
  - action: http.request
    with:
      method: GET
      url: https://example.com/status
      format: json
```
