# Spec: Secrets Providers

## Status

Partially implemented.

Conformance covers direct provider references declared on a DAG's `secrets:`
field, build-time validation of that field, live resolution for the
`env`, `file`, and `aws` providers, and masking of stored step outputs as a
retried run reads them. It does not cover the workspace-managed
secret registry (`ref:`-style references), which requires a running Dagu
server and its secrets management API.

## Scope

This spec covers `secrets:` entries that resolve directly through a named
provider (`provider:` plus `key:`), independent of any registry. It covers:

- Build-time validation of the `secrets:` field shared by every provider.
- Live resolution, failure, and masking behavior for the `env`, `file`, and
  `aws` providers.
- Provider-specific reference parsing for `gcp`, `azure`, `alibaba`, and
  `kubernetes`, to the extent it fails before any network client is created.

It does not define:

- The workspace secret registry, its API, or `ref:`-style entries.
- Live network resolution for `gcp`, `azure`, `alibaba`, `kubernetes`, or
  `vault`. Each of these providers only accepts a server address or
  credential file through `options.*` or workspace configuration, neither of
  which a DAG's `secrets:` field can set dynamically (`key` and `options`
  values are used literally and are not expanded like `with:` or `env:`
  values). `aws` is the exception: its client honors `AWS_ENDPOINT_URL` and
  the AWS credential environment variables directly, which lets a process
  environment override it without any DAG-level indirection.
- Secret value formatting beyond returning it as a single string (JSON field
  extraction via `options.field` is provider plumbing, not normative here).

## Goal

Workflow authors can inject a value from an external secret store into a
DAG's environment without writing that value into the DAG file, and without
that value leaking into logs or run history.

## Behavior

A `secrets:` entry has a `name` (the environment variable step processes
see) and either a registry `ref` or a `provider` plus `key`. `options` is a
map of provider-specific strings.

Build-time validation, independent of any provider:

- `name` is required, must be a valid environment variable name
  (`[A-Za-z_][A-Za-z0-9_]*`), must not start with `DAGU_`, and must not
  collide with a Dagu-managed runtime environment variable (for example
  `DAG_NAME`).
- Names must be unique within a DAG.
- Exactly one of `ref` or `provider` plus `key` must be set.
- `options` is rejected alongside `ref`.
- `ref` must match `[a-z0-9][a-z0-9-]*(/[a-z0-9][a-z0-9-]*)*` in full.
  Each nonempty segment starts with an ASCII lowercase letter or digit,
  followed by ASCII lowercase letters, digits, or hyphens. Internal and
  trailing hyphens are allowed. Empty segments, leading or trailing slashes,
  uppercase letters, underscores, and Unicode characters are rejected.

All secrets are resolved once, before any step runs. A DAG run fails
immediately if any secret fails to resolve; no steps execute.

An unregistered `provider` fails with `unknown secret provider: <name>`.

Every resolved secret value is masked wherever Dagu writes run output: the
step's stdout/stderr log files on disk, the rendered status tree, and stored
run status (error messages, output variables, outputs a step publishes, step
configuration). Published outputs include values written to
`DAGU_OUTPUT_FILE`. In stored run status, a secret that appears in escaped
form inside JSON text, such as a value containing `"`, `\`, `<`, `>`, `&`, or
a newline, is masked too. The mask replaces the literal value with `*******`.

Masking does not change the values steps exchange within a run. A later step
of the same run reads a published output with the secret intact. A run that
reuses a step's stored result instead of executing the step again reads the
stored value, so each secret in a reused output reads as `*******`. This
covers retry, resume after an approval or a human task, and runs that reuse
another run's outputs.

Provider-specific behavior covered here:

- `env`: resolves `key` from the DAG's own env scope first, then the real OS
  environment. An unset variable fails with
  `environment variable "<key>" is not set`.
- `file`: resolves `key` as a file path. A relative path is resolved against
  the DAG file's own directory. A missing file fails with
  `secret file not found: <path>`.
- `aws`: resolves `key` (a secret name or ARN) through AWS Secrets Manager
  using the standard AWS SDK, which honors `AWS_ENDPOINT_URL` and the AWS
  credential environment variables. A secret Secrets Manager reports as
  missing fails with `AWS Secrets Manager secret "<key>" was not found`.
- `gcp`, `azure`, `alibaba`, `kubernetes`: reference parsing runs before any
  client is created and reports a provider-specific diagnostic for a
  malformed reference (for example, `kubernetes` requires `key` to be
  `secret-name/data-key` or for `options.secret_name` to be set).

## Errors

Build-time validation failures exit `dagu validate` (and `dagu start`)
nonzero with a diagnostic naming the offending secret and field. Runtime
resolution failures (unknown provider, provider-specific parse errors,
provider fetch errors) fail the DAG run before any step starts, with an
error identifying the secret name and provider.

## Examples

```yaml
secrets:
  - name: API_TOKEN
    provider: env
    key: SOURCE_API_TOKEN
steps:
  - command: curl -H "Authorization: Bearer $API_TOKEN" https://example.invalid
```

```yaml
secrets:
  - name: DB_PASSWORD
    provider: aws
    key: prod/db/password
    options:
      region: us-east-1
      field: password
steps:
  - command: echo "$DB_PASSWORD" | psql-login
```
