---
id: "019"
title: "Team Secret Integration Layer"
status: draft
---

# RFC 019: Team Secret Integration Layer

## Summary

This RFC proposes a team-oriented secret integration layer for Dagu.

Dagu already lets a DAG declare secret references with providers such as
environment variables, files, Vault, and Kubernetes Secrets. Dagu resolves those
references when a run starts, injects the resulting values into the run
environment, and masks known secret values in several output paths.

That is useful for local automation, but it is not enough for teams. Teams need
a way to centrally define which secrets exist, which external provider backs
them, who may attach them to DAGs, which DAGs may use them, and which version
or provider reference was used by a run.

The proposed direction is to keep the existing `secrets:` behavior compatible
while adding a Dagu-managed integration layer:

- A registry of team-facing secret aliases and metadata.
- Centrally managed provider connections.
- Role-based controls for using secrets in DAGs.
- Audit and run provenance without plaintext values.
- Brokered run-start resolution.
- Consistent redaction across UI, API, logs, events, and run history.

Dagu should be the control plane for safe team usage of secrets in DAGs. It
should not pretend to be a full enterprise vault or a sandbox that can prevent a
trusted workload from intentionally exfiltrating a secret it was allowed to
receive.

## End-to-End Invariant

The feature is successful when a team can:

1. Connect Dagu to an approved secret provider once.
2. Register a stable Dagu secret reference for a workspace secret.
3. Allow DAG authors to attach that reference to approved DAGs without seeing
   the plaintext value through Dagu UI or API.
4. Enforce authorization before execution based on workspace, secret, provider,
   DAG, and run context.
5. Resolve approved secret values at DAG run start through the brokered
   resolution flow.
6. Persist only non-secret metadata, audit, usage, and provenance.

Plaintext secret values must never be stored in Dagu-controlled durable state:
run history, queue payloads, API responses, audit details, UI metadata, provider
connection metadata, persistent scheduler state, or persistent coordinator state.
This invariant applies to secret values resolved by the Dagu secret layer and to
Dagu-managed provider credentials. It does not claim that Dagu can detect every
arbitrary plaintext secret literal a user writes directly into DAG YAML,
parameters, outputs, artifacts, or external systems. It also does not claim to
stop a trusted workload from printing, storing, uploading, or transforming a
secret after Dagu intentionally provides that secret to the workload.

## Resolved Decisions

The following decisions are part of this RFC, not open questions:

- Dagu Cloud and distributed execution use brokered resolution for the first
  release. Delegated provider-native resolution is out of scope.
- Team-managed secrets are resolved at DAG run start, using the same timing
  model as the current `secrets:` mechanism: after the execution process has
  loaded the DAG, runtime parameters, and dotenv/base environment for a concrete
  run or retry attempt, but before the runner executes steps. They are not
  fetched lazily at step start.
- The first release uses Dagu's existing roles and workspace access for secret
  RBAC. It does not add a separate generic policy engine or customer-facing
  secret permission list.
- The first release must not treat self-reported runner ids or labels as a
  secret authorization boundary. Runtime authorization uses a `canUseSecret`
  control-plane check for the DAG, run, workspace, secret, secret backend,
  provider connection when present, and run context. Worker identity
  restrictions can be added only when a deployment has cryptographically bound
  worker identity.
- DAG authors need a passing `canAttachSecret` check to bind a secret reference
  to a DAG.
- If authorization controls, provider connection state, secret backend state,
  workspace access, or DAG access changes before run start, the run-start
  decision wins. A denied decision fails the run before plaintext is returned.
- Dagu-controlled durable state must never persist plaintext secret values.
- Direct provider references, direct `file` provider access, absolute file
  paths, and per-DAG provider options are controlled by administrator or
  workspace settings. Managed deployments fail closed by default.
- Provider credentials and sensitive routing material must live in provider
  connections, not DAG YAML, in managed mode.
- Provider checks must declare their capability, and default check paths must
  not read secret material.
- Provider reference fingerprints are opaque HMAC-SHA-256 values using a
  Dagu-controlled key.
- Secret references in DAG YAML are workspace-local refs. Dagu resolves them
  against the DAG's workspace. Cross-workspace secret use is out of scope for the
  first release.
- The default workspace is the existing unlabelled DAG scope. In user-facing API
  and UI selectors it is addressed as `workspace=default`; internally it uses the
  empty workspace name, not a real user-created workspace named `default`.
  `default` and `all` remain reserved workspace names.
- Deployment stage and tags are not part of the first-release secret registry.
- Dagu-managed secret values are write-only after creation. Plaintext reveal is
  not part of the first release.
- `name` is only the local environment variable alias. It must be a valid
  environment variable name, unique within the DAG's `secrets:` list, must not
  start with `DAGU_`, and must not collide with DAG-defined `env`, DAG
  parameters, or Dagu-managed runtime environment variables.

## Current State

Today, a DAG can declare secrets like this:

```yaml
secrets:
  - name: DB_PASSWORD
    provider: vault
    key: secret/data/prod/db/password
    options:
      mountPath: secret
```

Dagu parses these entries into DAG-level secret references. At runtime, Dagu
resolves each reference through a provider registry and exposes the resolved
values to the run environment.

The current implementation shape is:

- `internal/core/dag.go` defines `SecretRef`.
- `internal/cmn/secrets` contains provider resolution logic.
- Built-in providers include `env`, `file`, `vault`, and `kubernetes`.
- `internal/runtime/agent/agent.go` resolves all DAG secrets before execution.
- `internal/core/exec/context.go` stores resolved secret values in the
  execution environment scope.
- `internal/runtime/env.go` makes the secret layer available to the runtime
  environment.
- `internal/runtime/output.go` masks known secret values in local output
  writers.
- `internal/cmn/masking` masks exact resolved secret values.

Current behavior is intentionally simple. It treats secrets primarily as
run-scoped environment variables, not as governed team resources.

## Problem

Teams need answers to questions the current model cannot fully answer:

- Which secrets exist for this workspace?
- Which external provider and provider connection backs each secret?
- Who can attach a secret to a DAG?
- Who can rotate, disable, or delete a secret?
- Which DAGs reference a secret?
- Which run and execution context received a secret?
- Which provider version or reference was used by a particular run?
- Can provider credentials be configured centrally instead of copied into DAG
  YAML or process environment?
- Can direct provider references and host file access be restricted on
  multi-user servers and in Dagu Cloud?
- Can Dagu keep plaintext values out of durable control-plane state?

The current mechanism is a good secret reference feature. This RFC turns it into
a team integration layer for secure use in DAGs.

## Goals

- Preserve existing DAGs that use `provider`, `key`, and `options`.
- Add a stable registry reference syntax for team-managed secrets.
- Let teams manage secret aliases, provider routing, admin controls, usage,
  rotation metadata, and run provenance.
- Move provider connection configuration out of DAG files.
- Separate "can attach this secret to a DAG" from "can reveal this secret
  through Dagu UI or API".
- Make the trust boundary explicit: a workload that legitimately receives a
  secret can still leak it unless it runs inside stronger sandboxing and network
  controls.
- Enforce authorization controls at validation, DAG save, run start, and runtime
  resolution boundaries.
- Resolve approved run secrets into the run environment at DAG run start through
  a brokered flow.
- Avoid plaintext values in queue payloads, run metadata, audit logs, API
  responses, scheduler coordination data, and persistent history.
- Provide a Dagu-managed encrypted provider for teams that do not already have a
  vault, while treating external provider integration as the preferred
  enterprise path.
- Make log masking and API/UI redaction consistent across local, distributed,
  and remote execution paths.

## Non-Goals

- Do not replace HashiCorp Vault, AWS Secrets Manager, GCP Secret Manager, Azure
  Key Vault, Kubernetes Secrets, or similar systems.
- Do not guarantee containment against malicious DAG code after a secret is
  intentionally provided to the workload.
- Do not make plaintext reveal a normal workflow.
- Do not require Dagu Cloud customers to store secret values in Dagu.
- Do not break existing self-hosted workflows that use direct `env`, `file`,
  `vault`, or `kubernetes` provider references.
- Do not require every provider to support non-fetching existence checks. When a
  provider cannot check without reading secret material, Dagu must report that
  limitation clearly.

## Trust Model

This feature has two different security boundaries.

The first boundary is Dagu's control plane. Dagu can prevent users from seeing
plaintext through Dagu UI and API, prevent unauthorized users from attaching
secrets to DAGs, prevent unauthorized DAG runs from receiving brokered secret
material, and record audit and provenance.

The second boundary is the executed workload. Once a secret is passed to a
process environment variable, temporary file, container, remote shell, or
executor-specific field, that workload can read it. If the user can modify the
command that receives the secret, the user can intentionally print, upload, or
store the value.

Therefore, `canAttachSecret` means:

- The user may bind an approved secret reference to a DAG according to the
  current authorization controls.
- The user may not reveal the plaintext through Dagu UI, API, metadata, audit,
  logs, or run history.

`canUseSecret` means:

- Dagu may resolve and hand off an approved secret reference for a DAG run
  according to the current run-start authorization decision.
- The execution process may receive plaintext only through the brokered
  run-start resolution flow.

`canUseSecret` does not mean:

- The DAG author is cryptographically prevented from learning the value if they
  can control the workload that receives it.

Strong containment requires additional controls outside this RFC, such as
trusted DAG review, restricted executors, workload sandboxing, egress policy,
network isolation, container hardening, or provider-native identity binding.

## Design Principles

### Dagu Is the Integration Layer

Dagu should manage team-facing secret references, provider connections,
authorization controls, usage, audit, and runtime authorization. External
providers should remain the preferred system of record for secret material.

### Stable Dagu References Hide Provider Details

DAGs should depend on stable Dagu references such as:

```yaml
ref: db-password
```

DAG authors should not need to know the Vault path, Kubernetes namespace, AWS
ARN, provider credentials, or workspace routing for normal use. The DAG's
workspace supplies the secret namespace.

### Plaintext Stays Out of Durable State

Dagu can pass plaintext at the runtime handoff to an authorized execution
process, but it must avoid persisting plaintext in control-plane data
structures.

### Resolution Timing Is Fixed

Team-managed secrets are resolved at DAG run start, not lazily at step start.
For this RFC, "DAG run start" means the same semantic point used by the current
`secrets:` mechanism: the execution process is initializing a concrete DAG run
or retry attempt after the DAG definition, runtime parameters, and dotenv/base
environment have been loaded, and before the runner executes any DAG steps.

DAG run start does not mean scheduler evaluation, queue enqueue time,
coordinator dispatch time, worker poll or claim time by itself, or individual
step start. In distributed mode, the queued task can carry non-secret
resolution metadata, but the plaintext resolution request is made only when the
scheduled execution process initializes the run.

Each retry, restart, and sub-DAG run has its own DAG run start for secret
resolution. Authorization controls, provider connection state, secret backend
state, workspace access, DAG access, and secret status are rechecked for that
execution attempt. Authorization changes after successful run-start resolution
do not retroactively remove plaintext already handed to that running execution
process; they affect later run starts, retries, restarts, and sub-DAG runs.

### Cloud and Distributed Use Brokered Resolution

Dagu Cloud and distributed execution use brokered resolution for the first
release. The control plane resolves the secret value and sends it to the
scheduled execution process over an authenticated coordinator or worker channel
without persisting it. Delegated provider-native resolution is out of scope for
the first release. The brokered request must be bound to the active scheduled
run attempt, not just to a worker-reported label or id.

### Compatibility Is a Contract

Existing `provider` and `key` references must keep working. Team and cloud
deployments can restrict direct references with administrator settings, but the
default self-hosted behavior should remain usable.

### Authorization Must Be Evaluated More Than Once

DAG validation and UI checks help users catch mistakes early, but run start must
enforce the final decision. A saved DAG becomes invalid if authorization
controls, secret backend state, provider connection state, workspace access, or
DAG access changes before the run starts.

## Proposed Model

### Secret Registry

Add a Dagu secret registry. A registry entry represents a team-facing secret
alias and its provider routing metadata.

Conceptual model:

```text
Secret
  id
  workspace
  ref
  description
  provider_type
  provider_connection_id
  provider_ref
  provider_ref_fingerprint
  current_version
  status
  rotation_policy
  created_by
  created_at
  updated_by
  updated_at
  last_checked_at
  last_resolved_at
  last_rotated_at
```

`workspace` plus `ref` forms the internal secret identity. The DAG-facing
reference is only the workspace-local `ref`. For example, a DAG in workspace
`payments` with `ref: db-password` resolves to the registry entry with
`workspace=payments` and `ref=db-password`. The pair must be unique in the
registry. The same `ref` may exist in different workspaces.

For the default workspace, Dagu uses the existing unlabelled DAG scope. A DAG
without a valid `workspace` label resolves `ref: db-password` to
`workspace=""` and `ref=db-password` internally. API and UI filters display and
select this scope as `workspace=default`. `default` must not become a normal
workspace name, because Dagu already reserves it as the user-facing selector for
unlabelled resources.

The exact external provider reference is visible only to admins and workspace
managers because provider paths can reveal sensitive infrastructure names.

Workspace is the Dagu namespace and access boundary. Deployment stage and tags
are intentionally not part of the first-release registry model; if teams need
that metadata later it can be added without changing the stable reference
grammar.

`provider_ref_fingerprint` must be an opaque HMAC-SHA-256 fingerprint over a
canonical provider reference using a Dagu-controlled key. It must not be a plain
hash of the provider reference. The HMAC input must be stable for the same
provider reference and provider connection, and it must exclude plaintext secret
values and provider credentials.

For externally managed secrets, Dagu stores metadata and provider references.
For Dagu-managed secrets, Dagu stores encrypted version records. Dagu-managed
secrets do not require an external provider connection.

### Provider Connections

Add centrally managed provider connections. A provider connection contains the
configuration and credentials needed to talk to an external secret provider.

Conceptual model:

```text
SecretProviderConnection
  id
  workspace_scope
  name
  provider_type
  auth_method
  config
  credential_material
  allowed_ref_prefixes
  allowed_namespaces
  status
  created_by
  created_at
  updated_by
  updated_at
  last_checked_at
```

Credential fields must be encrypted at rest or delegated to another credential
source. They must be redacted in list, detail, audit, logs, and UI views.

The first team-managed provider types are:

- `dagu-managed`
- `vault`
- `kubernetes`

Existing direct `env` and `file` providers remain available for compatibility
when administrator settings allow direct references. `vault` and `kubernetes`
use provider connections. `dagu-managed` uses Dagu's encrypted secret value
store and does not require or create an external provider connection.
`aws-secrets-manager`, `aws-ssm`, `gcp-secret-manager`, and `azure-key-vault`
are later provider additions. The registry and API shape must remain
provider-neutral so those providers can be added without changing DAG reference
syntax.

### Registry Reference Syntax

Keep the existing syntax valid:

```yaml
secrets:
  - name: DB_PASSWORD
    provider: vault
    key: secret/data/prod/db/password
```

Add workspace-local registry references:

```yaml
secrets:
  - name: DB_PASSWORD
    ref: db-password
```

The schema should require exactly one of:

- `ref`
- `provider` plus `key`

`name` remains the local environment variable alias exposed to the DAG run.
Dagu must validate it before saving a managed DAG and again at run start:

- It must match `[A-Za-z_][A-Za-z0-9_]*`.
- It must be unique within the DAG's `secrets:` list.
- It must not start with `DAGU_`.
- It must not collide with a DAG-defined `env` variable, DAG parameter name, or
  Dagu-managed runtime environment variable.

If a collision is detected, Dagu must fail validation or run start instead of
silently choosing an environment variable precedence rule. Inherited base
environment variables are lower precedence and may be shadowed by secrets, but
the provenance and masking layer must still mark the final alias source as
secret without logging the value.

Registry reference grammar:

```text
<ref>
```

- `<ref>` is a slash-separated registry reference inside the DAG's workspace.
- Ref segments should be lowercase slugs.
- References should be case-sensitive and normalized at creation time.

Dagu resolves `ref` against the DAG's workspace label. DAGs without a workspace
label use the existing default/unlabelled workspace scope, represented
internally as an empty workspace name and externally as `workspace=default`.
The workspace is not part of the DAG reference string.

A DAG may use only secrets from its own workspace. Cross-workspace secret
references are not supported in the first release.

### Run-Scoped Resolution

Team-managed secrets keep the current `secrets:` runtime model for the first
release. A top-level secret declaration creates a local alias for the run. At
DAG run start, Dagu authorizes the requested aliases, resolves approved values
through the broker, and makes them available to the run environment using the
same environment-variable mechanism as existing DAG secrets.

This is intentionally run-scoped. It reduces administrative leakage through
Dagu UI and API and centralizes authorization, audit, and provenance, but it
does not claim to isolate secrets between commands inside the same run.

### Role Mapping

The first release must use Dagu's existing roles and workspace access. It must
not expose a separate secret permission model to users.

Initial role mapping:

- `admin`: manage all secrets, provider connections, Dagu-managed values, audit,
  and provider references.
- workspace `manager`: manage secrets, provider connections, Dagu-managed
  values, audit, and provider references for that workspace.
- workspace `developer`: view non-sensitive secret metadata, attach existing
  enabled workspace secrets to DAGs they can edit, and run DAGs with attached
  approved secrets. Developers cannot view provider credentials, Dagu-managed
  values, or hidden provider references.
- workspace `operator`: run DAGs with already-attached approved secrets.
  Operators cannot attach or manage secrets.
- workspace `viewer`: view non-sensitive metadata only.

Dagu must implement this through two authorization checks rather than a broad
authorization surface:

- `canAttachSecret(user, dag, secret)`: evaluated when a DAG is saved or edited.
- `canUseSecret(run, dag, secret)`: evaluated at run start before plaintext is
  returned to the execution process.

Plaintext reveal is not a supported product or API concept in the first release.

### Admin Controls

The first release should expose concrete administrator and workspace-manager
settings, not a generic policy engine. These controls should be enforceable at
workspace, provider connection, secret, DAG, and run context boundaries.

Initial controls:

- Allowed provider types.
- Allowed provider connections.
- Allowed secret registry refs.
- Allowed external provider reference prefixes.
- Allowed Kubernetes namespaces.
- Whether direct `provider` and `key` references are allowed.
- Whether per-DAG provider `options` are allowed.
- Whether the `file` provider is allowed.
- Whether absolute file paths are allowed.

For Dagu Cloud and multi-user servers, direct provider references, direct `file`
provider access, absolute file paths, and per-DAG provider credentials are
denied by default unless an administrator explicitly enables them. Provider
`options` in DAG YAML are allowed only from a provider-specific allowlist of
non-sensitive fields. Credentials, tokens, kubeconfig paths, Vault addresses
that bypass a managed connection, and similar routing/authentication material
must come from provider connections, not from DAG YAML, in managed mode.

### Runtime Resolution

Runtime resolution must separate control-plane authorization from secret value
materialization. For team-managed secrets, resolution happens at the DAG run
start point defined above.

The queue or coordinator must carry only:

- DAG identity.
- Run identity.
- Attempt identity.
- Secret alias.
- Registry secret id or reference.
- Non-secret authorization and provenance metadata.

It must not carry plaintext values.

Existing distributed task payloads can include user-authored execution material
such as DAG definitions, run parameters, base configuration, retry status, and
agent snapshots. Those fields are not a safe place to transport broker-resolved
secret material. In managed mode, Dagu must enforce these rules:

- Broker-resolved values are never copied into task payload fields, queue
  records, dispatch records, retry snapshots, previous-status payloads, base
  config payloads, or agent snapshots.
- Task payloads may carry only secret references, local aliases, and non-secret
  authorization/provenance metadata.
- Structured DAG and task fields that configure secret or provider access must
  not accept provider credentials, Dagu-managed secret values, or sensitive
  provider routing material. Those values must live in provider connection or
  secret value stores.
- User-authored payload fields are treated as untrusted input. They may contain
  arbitrary user-provided text, so API, UI, logging, audit, and persistence paths
  must not display them as proof that Dagu has stored a broker-resolved secret.

At DAG run start, the execution process requests brokered resolution for the set
of secret aliases required by the run. The control plane authorizes the request
using the current authorization controls and run context before any plaintext is
returned. The decision must verify:

- The request is bound to the active scheduled attempt: workspace, DAG identity,
  run id, attempt id, and requested aliases match the current run claim or
  lease.
- The DAG/run is allowed to use the secret.
- `canUseSecret` passes for the secret, DAG, workspace, run context, and
  provider connection when the secret uses one.
- The secret is enabled.
- The secret backend is enabled.
- The provider connection is enabled when the secret uses an external provider.
- Workspace controls allow the resolution.
- Direct provider references and per-DAG provider options are allowed by
  administrator or workspace settings when the DAG uses legacy `provider` plus
  `key` syntax.

If any of these checks fails, the run must fail before plaintext is returned to
the execution process. If an authorization control is changed after the DAG is
saved but before run start, the run-start authorization decision wins.

For self-hosted local mode, the broker may run in-process and resolve from the
local registry and provider connection store. It must still use the same
authorization, provenance, masking, and no-persistence rules.

For Dagu Cloud and distributed mode, the first release uses only brokered
resolution. The control plane resolves the value and returns it to the scheduled
execution process over an authenticated coordinator or worker channel without
writing the value to durable coordinator, queue, or run-history state. Delegated
resolution is out of scope for the first release.

The execution process may hold resolved plaintext in memory for the lifetime of
the run. It must not write resolved plaintext to run history, queue payloads,
coordinator state, API responses, audit details, UI metadata, or scheduler
state. Dagu injects approved values into the run environment under the aliases
declared in the DAG.

### Provider Checks

Separate structural validation, provider reachability, provider authorization,
and value resolution.

Recommended commands and semantics:

- `dagu validate`: schema, DAG structure, and reference shape only.
- `dagu check secrets`: authorization and provider access checks without fetching
  values when the provider supports it.
- `dagu dry`: preserve existing behavior initially. A future no-fetch dry-run
  mode can be added explicitly rather than silently changing behavior.

For registry references, `dagu check secrets` must resolve workspace the same
way a real run would. A DAG with a valid `workspace` label checks secrets in
that workspace. A DAG without a workspace label checks secrets in the default
unlabelled scope, exposed as `workspace=default`. A CLI or API check that is not
attached to a DAG must require an explicit workspace selector rather than
guessing.

Every provider must report a check capability before Dagu performs a provider
check:

- `no_fetch`: can check reachability, auth, and existence without reading value.
- `metadata_only`: can check provider metadata but not all requested fields.
- `requires_value_read`: cannot prove access or key existence without reading
  secret material.
- `unsupported`: no provider-side check is available.

`dagu check secrets` must not read secret material by default. If a provider
reports `requires_value_read`, the command must return a clear capability result
instead of resolving the secret. A separate explicit value-read check mode can be
added later, but it must be visibly named, audited, and disabled in Dagu Cloud
unless product configuration explicitly allows it.

### Dagu-Managed Encrypted Provider

Dagu must provide a managed encrypted provider for simple team setups and Dagu
Cloud onboarding in the first release.

For self-hosted deployments, Dagu-managed values are encrypted version records in
a file-backed value store under the Dagu data directory. The store requires
explicit root-key configuration or an explicit administrator-approved generated
key file. Dagu must show backup guidance before enabling generated file-key mode.

Generated file-key mode must be explicit and fail closed:

- Dagu must never silently generate a key inside the encrypted value store.
- The generated key file path must be administrator-approved and stored outside
  the encrypted value store directory.
- The key file must use restrictive permissions.
- Normal encrypted value-store backups must not include the key file unless the
  administrator intentionally backs up key material as part of a documented
  recovery procedure.
- If the configured key is missing or unreadable, Dagu must refuse to start the
  Dagu-managed value store rather than creating a new empty key.

For Dagu Cloud, managed values must use envelope encryption with a cloud
KMS-backed hierarchy.

Dagu-managed values must be:

- Write-only through normal UI/API flows after creation.
- Versioned.
- Encrypted at rest.
- Excluded from list and detail responses.
- Excluded from audit and run history.
- Rotatable without changing DAG references.

Dagu-managed storage is a convenience provider for teams without an external
vault. External provider integration remains the default enterprise
recommendation.

Plaintext reveal for Dagu-managed values is out of scope for the first release.
The first release supports write, rotate, use, check, audit, and provenance
workflows, not reveal.

### Audit Events

Add a `secret` audit category or equivalent structured audit domain.

Recommended actions:

- `secret_create`
- `secret_update`
- `secret_disable`
- `secret_enable`
- `secret_delete`
- `secret_value_write`
- `secret_rotate`
- `secret_use_authorized`
- `secret_resolve_success`
- `secret_resolve_denied`
- `secret_provider_check`
- `secret_reference_added`
- `secret_reference_removed`

Audit details must never include plaintext values or provider credentials.
Resolution audit events are emitted at DAG run start for every requested secret
alias, including denied requests.

Audit details may include:

- Actor.
- Workspace.
- DAG name.
- Run id.
- Secret id.
- Secret registry reference.
- Provider type.
- Provider connection id.
- Provider reference fingerprint.
- Execution context and reported worker metadata, when available.
- Result.
- Error category.
- Timestamp.

### Run Provenance

Persist non-secret provenance for each run.

Conceptual model:

```text
RunSecretUsage
  run_id
  local_name
  secret_id
  secret_ref
  provider_type
  provider_connection_id
  provider_ref_fingerprint
  version
  execution_context
  worker_metadata
  resolved_at
  result
```

If a provider exposes a stable version, Dagu should record it. If not, Dagu
should record `version_unavailable`.

Provenance is for investigation and rotation planning. It must not contain the
value.

Because team-managed resolution happens at DAG run start, Dagu may record one
provenance row per secret alias. `resolved_at` is the brokered run-start
resolution time.

### Usage Graph

Add usage discovery from two sources:

- Static references in DAG definitions.
- Dynamic run provenance.

The graph should answer:

- Which DAGs reference this secret?
- Which recent runs resolved this secret?
- Which secrets appear unused?
- Which DAGs will be affected by disabling or rotating this secret?

Static usage can be indexed when DAGs are saved or scanned. Dynamic usage should
come from run provenance.

### Masking and Redaction

Masking is a last line of defense, not a primary security boundary.

Required improvements:

- Apply the same masking behavior to local logs, remote logs, streaming logs,
  redirected step output, and collected string outputs.
- Redact secret values and provider credentials in API responses, UI views,
  structured events, and audit details.
- Avoid masking empty or very short values.
- Consider common derived forms such as JSON-escaped strings where practical.
- Redact provider `options` that may contain credentials.
- Clearly document that artifacts, databases, remote APIs, and arbitrary step
  side effects are outside masking guarantees.

## API Sketch

The exact OpenAPI shape should follow Dagu's existing conventions. Conceptually,
the API should include these resources.

Secrets:

```text
GET    /api/v1/secrets
POST   /api/v1/secrets
GET    /api/v1/secrets/{id}
PATCH  /api/v1/secrets/{id}
DELETE /api/v1/secrets/{id}

POST   /api/v1/secrets/{id}/disable
POST   /api/v1/secrets/{id}/enable
POST   /api/v1/secrets/{id}/versions
POST   /api/v1/secrets/{id}/rotate
POST   /api/v1/secrets/{id}/check
GET    /api/v1/secrets/{id}/usage
GET    /api/v1/secrets/{id}/audit
```

Provider connections:

```text
GET    /api/v1/secret-provider-connections
POST   /api/v1/secret-provider-connections
GET    /api/v1/secret-provider-connections/{id}
PATCH  /api/v1/secret-provider-connections/{id}
DELETE /api/v1/secret-provider-connections/{id}

POST   /api/v1/secret-provider-connections/{id}/disable
POST   /api/v1/secret-provider-connections/{id}/enable
POST   /api/v1/secret-provider-connections/{id}/check
GET    /api/v1/secret-provider-connections/{id}/usage
```

Important API rules:

- List endpoints must be paginated.
- All request bodies must validate at the API boundary.
- Secret management APIs must carry workspace metadata because workspace is the
  internal access boundary.
- API selectors use `workspace=default` for the default unlabelled scope; durable
  secret identity and provenance records use the empty workspace name internally
  for that scope.
- DAG-facing `ref` values must remain workspace-local refs and must not include
  the workspace name.
- Responses must not include plaintext secret values.
- Provider credentials must be write-only or redacted.
- Provider reference visibility must follow the role mapping above.
- APIs must not expose any plaintext reveal operation in the first release.
- Error responses should distinguish validation failure, authorization denial,
  provider failure, and unsupported check capability.

## Web UI Requirements

The UI is part of the product surface, not a convenience layer. Team secret
management should not require editing YAML for common administration workflows.

### Secret Registry

Add a workspace-level secret management page, for example
`Settings > Secrets`.

The list view should show:

- Secret reference.
- Provider type.
- Status.
- Last checked time.
- Last resolved time.
- Last rotated time.
- Usage count.

The detail view should show metadata, controls, usage, audit history, rotation
state, and provider routing according to the current user's workspace role. It
must not show plaintext values by default.

### Create and Edit Flow

The create flow should let an administrator:

- Choose provider type.
- Choose or create a provider connection.
- Enter a provider reference or Dagu-managed value.
- Assign workspace.
- Configure access and provider restrictions.
- Test provider access without revealing the value when supported.
- Copy or insert the generated workspace-local DAG reference.

For Dagu-managed values, the value entry is write-only after creation. The UI
can confirm that a value exists and show version metadata, but it should not
reveal plaintext in normal workflows.

### Provider Connections

Add a provider connection management page.

Administrators should be able to:

- Create, edit, disable, and delete provider connections.
- Test a provider connection.
- Configure allowed provider reference prefixes or namespaces.
- Configure workspace access.
- View dependent secrets.

Provider connection secrets, tokens, and credentials must be redacted.

### DAG Editor Integration

The DAG editor should make registry references discoverable and hard to misuse:

- Autocomplete registry references from the current DAG workspace.
- Show whether the current user can attach the selected secret.
- Warn when direct `provider` and `key` references violate admin controls.
- Validate that referenced registry secrets exist in the current DAG workspace.
- Surface authorization errors before a run starts.
- Avoid displaying plaintext values in previews, diffs, or validation output.

### Run and History Views

Run detail pages should show secret provenance without values:

- Which secret references were requested.
- Which provider and version were used when available.
- Which execution context received the secret.
- Whether a resolution was denied by authorization controls.
- Whether a provider check failed.

### Usage, Rotation, and Audit

The UI should expose:

- Usage graph by DAG and recent run.
- Unused secret detection.
- Affected DAGs before disable, delete, or rotation.
- Rotation metadata and stale secret warnings.
- Secret audit events with actor, action, result, DAG, run id, and timestamp.

## Migration and Compatibility

Existing DAGs using direct provider references continue to run unchanged:

```yaml
secrets:
  - name: API_TOKEN
    provider: env
    key: PROD_API_TOKEN
```

New team-managed DAGs can use registry references:

```yaml
secrets:
  - name: API_TOKEN
    ref: api-token
```

Admin controls can restrict direct references in managed deployments:

- Self-hosted default: direct references allowed for compatibility.
- Multi-user server default: direct references denied unless explicitly enabled
  by administrators.
- Dagu Cloud default: direct references denied unless product configuration
  explicitly enables a safe subset.

The first implementation should avoid changing `dagu dry` semantics silently.
Add an explicit no-fetch secret check path before changing dry-run behavior.

## Implementation Plan

### Phase 0: Harden Current Behavior

- Verify and fix masking across local, remote, streaming, redirect, and output
  collection paths.
- Redact provider options in logs, API responses, UI views, and structured
  events.
- Add control hooks for direct provider references, direct `file` provider
  access, per-DAG provider options, and absolute paths.
- Add provider check capability reporting.
- Stop check paths from silently resolving values when a provider cannot perform
  non-fetching checks.
- Add tests for masking, redaction, file-provider restrictions, and provider
  check semantics.

### Phase 1: Registry, Provider Connections, Dagu-Managed Values, and References

- Add secret registry data model and store.
- Add provider connection data model and store.
- Add encrypted Dagu-managed value data model and file-backed store.
- Add self-hosted root-key configuration and generated file-key setup with backup
  guidance.
- Add workspace-local registry reference syntax: `ref: <ref>`.
- Preserve `provider` and `key` syntax.
- Add API endpoints for metadata CRUD, provider connection CRUD, and write-only
  Dagu-managed value write/rotate.
- Add basic UI for secret and provider connection administration.
- Add static usage indexing for DAG references.
- Add secret audit events for metadata and reference changes.

### Phase 2: Admin Controls and Runtime Resolution

- Add role-based `canAttachSecret` and `canUseSecret` checks.
- Enforce `canAttachSecret` when saving DAGs and `canUseSecret` when starting
  DAG runs.
- Add brokered run-start secret resolution.
- Fail a run at start if current authorization controls, provider connection
  state, secret backend state, workspace access, or DAG access denies a
  requested secret.
- Enforce provider connection, secret backend, workspace, run context, and DAG
  authorization at runtime resolution.
- Avoid plaintext in queue and coordinator payloads.
- Record run secret provenance without values.

### Phase 3: Advanced Secret Workflows

- Add richer rotation workflows and reminders.
- Add key rotation and re-encryption workflows for Dagu-managed values.
- Add stale secret reports.

### Phase 4: Enterprise Workflows

- Add richer usage graph UI.
- Add additional external providers based on customer demand.

## Acceptance Criteria

Minimum viable release:

- Existing `provider` plus `key` DAG secrets continue to run unchanged.
- Registry references using `ref: <ref>` are accepted and resolved against the
  DAG's workspace.
- DAGs without a workspace label resolve registry references in the default
  unlabelled scope, exposed as `workspace=default`.
- Secret `name` aliases are validated for env-var shape, per-DAG uniqueness,
  `DAGU_` reservation, and collisions with DAG env, DAG params, and
  Dagu-managed runtime envs.
- A secret registry entry can point to an external provider connection.
- Dagu-managed secret values can be created, rotated, resolved, checked, audited,
  and recorded in provenance without plaintext reveal.
- Self-hosted generated file-key mode requires an administrator-approved key path
  outside the encrypted value store and fails closed if the key is missing.
- Provider credentials are centrally configured and redacted.
- Users can attach approved secrets to DAGs without plaintext reveal through
  Dagu UI or API.
- Brokered run-start resolution is implemented for team-managed secrets.
- Brokered resolution requests are bound to the active scheduled attempt by
  workspace, DAG identity, run id, attempt id, and requested aliases.
- Runtime authorization can deny unauthorized secret use before plaintext is
  returned to the execution process.
- Revoked authorization controls, disabled secrets, disabled secret backends,
  disabled provider connections, and disallowed DAG/run contexts cause the run
  to fail at start.
- Direct provider references, direct `file` provider access, absolute file
  paths, and per-DAG provider options can be restricted by administrator
  settings.
- Plaintext values are not persisted in run history, audit logs, API responses,
  queue payloads, or coordinator state.
- Secret usage can be queried by secret, DAG, and recent run.
- Run provenance records secret id, registry reference, provider type, provider
  connection id, version when available, execution context, reported worker
  metadata when available, resolution timestamp, and result.
- Secret audit events are emitted for create, update, disable, delete, use
  authorization, resolve success, resolve denial, and provider check.
- The UI supports secret list, detail, create, edit, disable, usage, audit, and
  provider connection management without exposing plaintext.
- No API or UI path exposes plaintext reveal in the first release, including
  Dagu-managed values.
- Provider checks declare `no_fetch`, `metadata_only`, `requires_value_read`, or
  `unsupported`, and default check paths never read secret material.
- Provider reference fingerprints are opaque HMAC-SHA-256 values, not plain
  hashes.
- Tests cover compatibility, authorization denial, check capability reporting,
  masking, audit redaction, provider credential redaction, brokered run-start
  resolution, authorization revocation at run start, no-value-read check
  behavior, and no-plaintext persistence.

## First Release Scope Decisions

- The first team-managed provider types are `dagu-managed`, `vault`, and
  `kubernetes`.
- `vault` and `kubernetes` use provider connections. `dagu-managed` uses Dagu's
  encrypted secret value store.
- Provider references are visible only to admins and workspace managers.
  Metadata readers cannot see provider references.
- Self-hosted registry and provider connection data use file-backed stores under
  the Dagu data directory for the first release, matching Dagu's existing
  persistence model.
- Provider credential material in self-hosted file-backed stores is encrypted at
  rest. If encryption is not configured, Dagu must refuse to store provider
  credentials.
- Dagu-managed secret values are included in the first release. They use a
  key-ring model: one active encryption key for new writes, one or more accepted
  decryption keys for old versions, and explicit re-encryption before an old key
  can be removed.
- Self-hosted Dagu-managed values require explicit root-key configuration or an
  explicit administrator-approved generated key file. Dagu must show backup
  guidance before enabling generated file-key mode.

## Recommendation

Proceed with this RFC as an incremental integration layer, not as a replacement
for the existing secret mechanism.

The first release should prioritize team-managed registry references,
Dagu-managed encrypted values, provider connections, redaction, authorization
denial, audit, and provenance. Richer rotation workflows and additional
external providers can follow once the core integration path is solid.

This gives teams a secure way to manage and use secrets in DAGs while keeping
Dagu honest about the runtime trust boundary: Dagu can govern secret access and
avoid accidental leaks, but code that legitimately receives a secret must still
be treated as trusted or isolated by stronger runtime controls.
