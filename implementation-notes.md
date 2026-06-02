# Implementation Notes

## DAG default profile settings

- Treat profiles as a control-plane concern. The DAG YAML/runtime data plane must not gain a `profile` field for this feature.
- Add a per-DAG settings record with `profile` as the user-facing field name. The endpoint context makes it clear this is the DAG default; `defaultProfileName` is unnecessarily verbose.
- Store DAG settings separately from DAG YAML so Git-managed workflow definitions remain portable and do not depend on Dagu-managed profile catalog semantics.
- Scheduled runs should use the DAG settings profile when no explicit run profile is present. Manual/API runs may still explicitly choose a profile or no profile.
- Retry must keep using the original run profile from persisted status and must not consult the current DAG settings profile.
- Key DAG settings by the DAG file identifier used by existing `/dags/{fileName}` routes. Scheduler resolves the same key from `dag.FileName()` and falls back to `dag.Name` for inline or fileless DAGs.
- Protected profiles require admin permission when configured as a DAG default. Once configured, scheduled runs and manual runs that use the default are allowed to use it because authorization happened at configuration time.
- Start/enqueue request bodies need three states: omit the profile override to use the DAG default, set `profile` to an empty string to run without a profile, or set `profile` to a profile name for an explicit override.
- Sub-DAG runs inherit the already-selected parent run profile. Runtime-managed sub-DAG paths must not read control-plane DAG settings; queued sub-DAG runs remain top-level queue records because queue items address the child DAG/run directly and status `Root` is also used as the later storage lookup root.
- Migrate the DAG settings record on DAG rename as a best-effort follow-up after the file rename succeeds. Failing to migrate settings should not roll back or block the DAG rename, but the server logs a warning.

## Verification notes

- The full Go suite must run outside the filesystem sandbox because several existing tests create localhost `httptest` servers. In-sandbox `make test` fails with `bind: operation not permitted`; escalated `make test` passed.
