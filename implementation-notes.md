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

## Plane separation refactor

### Invariant

- Preserve existing Dagu CLI/server/worker behavior and storage formats while allowing local execution to use a runtime `runstate.Store` directly.

### Target Strings

- Notes file: `implementation-notes.md`
- New package: `internal/runtime/runstate/memstore`
- No branch, image name, package release name, tag, workflow trigger, or release output is changed.

### Decisions

- Treat the next deep module as execution state, not generic persistence. The runtime-facing seam should expose run attempt behavior, while Dagu history listing, retention, rename, and UI queries stay outside the engine store.
- Added `OpenAttempt`, `OpenChildAttempt`, `ReadStatus`, and `ReadOutputs` to the runtime run-state port. These are still execution-state operations needed by embedded local execution and retry/cancel flows; broad history operations remain on `DAGRunStore`.
- Added `internal/runtime/runstate/memstore` as a concrete runtime store for embedded/no-file dag-run state. It keeps root and child attempt state in memory and intentionally omits listing, retention, rename, and UI history behavior.
- `Agent` now prefers an explicitly supplied `runstate.Store`. The existing prepared-attempt path remains the file-backed local execution path so current Dagu storage formats and proc attempt IDs are preserved.
- `subflow.Local` now accepts a direct `runstate.Store` and passes it to child workflow agents. When no direct store is provided, it keeps using the current `DAGRunStore`-backed history adapter.
- Embedded local execution can be configured without `DAGRunStore`; process tracking uses the run ID until the runtime opens its attempt. This keeps duplicate-run protection lightweight but means no-file embedded mode does not provide Dagu history/listing APIs.
- Added embedded run output readback so no-file local execution can verify collected outputs through runtime state instead of relying on file-backed dag-run history.
- Added `internal/node` as the adapter for child workflow runner construction. The adapter owns the distributed/local router composition, while command, worker, engine, and test code still own their concrete stores and remote transport dependencies.
- When both `RunStateStore` and `DAGRunStore` are configured for embedded execution, `RunStateStore` is now authoritative for status, outputs, and cancel requests because the agent writes to that store.
- A missing run-state attempt now falls back to DAG-run history in hybrid configurations. Other run-state errors still remain authoritative. This preserves compatibility for runs created before the direct run-state store saw them without hiding real runtime-state failures.
- Direct run-state embedded execution now preflights duplicate run IDs before returning a run handle. This matches the file-backed path more closely and keeps duplicate errors synchronous.
- `memstore` validates run IDs at the adapter boundary instead of relying only on engine-level validation.
- CodeRabbit review fixes kept the missing-store errors explicit when neither runtime nor history storage is configured, and preserved `ProfileStore` when test-helper subworkflows are routed through the centralized factory.

### Verification

- `go test ./internal/node ./internal/runtime/runstate/... ./internal/runtime/agent ./internal/subflow ./internal/engine ./internal/cmd ./internal/service/worker ./internal/test`
- `go test .`
- `git diff --check`
