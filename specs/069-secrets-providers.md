# Spec: Secrets Providers

## Status

Partially implemented.

Conformance covers direct provider references declared on a DAG's `secrets:`
field, build-time validation of that field, live resolution for the
`env`, `file`, and `aws` providers, and a retry that fails a step reading an
output stored masked while it reuses outputs the step does not read. It does
not cover the workspace-managed secret registry (`ref:`-style references),
which requires a running Dagu server and its secrets management API.

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
  values). `vault` also falls back to the standard `VAULT_ADDR`,
  `VAULT_TOKEN`, and other `VAULT_*` variables when its Dagu settings are
  unset. `aws` is the exception: its client honors `AWS_ENDPOINT_URL` and
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
of the same run reads a published output with the secret intact.

A run can instead restore a step's stored result without executing the step
again: a retry of the run or of a step, a retry with an edited DAG, resume
after an approval or a human task, and a run that carries another run's
outputs. Stored outputs stay masked, and stored
status records which outputs masking changed. A step that reads a recorded
output after such a restore fails before it runs, with an error that names the
step and the output and says the producing step has to run again, together
with the steps after it. A step reads an output where value resolution inserts
it into one of the step's fields, or where a POSIX shell that runs the step's
command or script expands it from its environment:

- `${steps.<id>.outputs.<name>}` reads a published output.
- `${<id>.outputs}` and any path under it read the published outputs when the
  step published any, and the captured output document otherwise.
- `${<id>.output}` reads the captured output text, or the step's output
  variable when no captured text is stored. A path under it reads that field
  of a JSON object.
- `${NAME}`, `$NAME`, and `${env.NAME}` read an output variable when the value
  under that name in the step's scope is the output variable. `${NAME.path}`
  reads the part of it the path selects, and a path that no longer resolves
  because masking left the text no longer JSON counts as reading the variable.

Each reference fails only when masking changed the output it reads. A reference
that resolution leaves as written reads nothing: an escaped reference, a name a
shell leaves in a comment, in single quotes, or in a quoted heredoc, a name a
script expands after it sets or unsets its own value, and a `js` or `template`
script, which is passed on as written. A value a script sets in a subshell, in
code that may not run, or as a function's local holds only there, and a value
every branch of an `if` sets holds after it. A function body runs where the
function first runs, and the values it sets hold after that only when the call
surely runs it in the script's own shell; a function no call names, such as one
`trap` runs, runs at the end of the script. A `for` loop's variable holds after
the loop only when its list is plain words. `declare`, `export`, or `readonly`
without a value keeps the inherited value. Text a script hands to another shell,
as with `eval` or `sh -c`, and a script whose shebang names an interpreter other
than a POSIX shell are programs reading their environment.

A variable the step's own `env:` defines takes the place of the output variable
in its other fields and in the `env:` entries after the one that defines it, and
of two predecessors that set the same variable, the one later in scope order
does. A foreach body step reads outputs in its own scope, which holds the
foreach step's output variables under its own `env:`.

A program the step runs that reads an output variable from its environment, and
an executor that takes a variable by name, such as through `pass_env`, receive
`*******`; only the reads above are checked. A handler step and the prompt of a
human task report on the run rather than do its work, so they read an output
stored masked as it is, as `*******`. When masking changed the name of a
published output, or masked the published outputs as text, every published
output of that step counts as masked. Outputs that masking did not change are
reused unchanged, and a value supplied for an output of a carried step replaces
the recorded one, including the captured output when it names the step's
`output:` variable.

A step whose preconditions are not met is skipped as before, unless a
precondition reads a recorded output: a precondition decides on what it reads,
so the step then fails whatever the precondition decides. A field that an
executor resolves while the step runs, such as foreach items, fails the step at
the read, before the executor uses the value. A repeat condition runs after each
execution, so one that reads a recorded output fails the step after the
execution that evaluates it. The failure is the same on every attempt, so it is
not retried, repeated, or marked successful.

Resume after an approval or a human task cannot run a step before the gate
again without reopening it, so a step after the gate that reads a recorded
output fails on every resume. A step that needs a secret after an approval or
a human task reads the secret itself instead of through an output.

Provider-specific behavior covered here:

- `env`: resolves `key` from the DAG's own env scope and the run's runtime
  profile entries first (see [Spec 070](070-runtime-profiles.md)), then the
  real OS environment. An unset variable fails with
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
