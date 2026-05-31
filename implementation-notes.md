# Implementation Notes

## 2026-05-24

- Branch target: `refactor/queue-persis-store`.
- Initial staged documentation change was committed first as `doc: update persistence refactor plan`, keeping it separate from product-code changes.
- Implementation target: port the queue store from `internal/persis/filequeue` to a `persis.Collection`-backed adapter in `internal/persis/store`.
- Literal queue collection/root decision: use `cfg.Paths.QueueDir` through `file.NewCollection(cfg.Paths.QueueDir)` for the first wiring step so the file backend preserves the existing `data/queue` layout. This avoids silently moving queue files to a new `queue_items` directory.
- Literal queue record ID decision: store records as `{queueName}/{itemID}` while keeping the public `QueuedItemData.ID()` value as `{itemID}`. This preserves the existing `QueueStore` contract for deletion and cursor callers.
- Test coverage decision: start with memory-backed store tests for the queue adapter because the refactor goal is backend-neutral behavior. File-backend compatibility is covered through the existing `persis.Collection` contract; if queue file layout issues appear while wiring, add a file-backed queue test before committing.
- Queue item ID decision: generated item IDs keep the old `item_{high|low}_{timestamp}Z_{dagRunID}` prefix shape, but add a UUID suffix to avoid accidental overwrite when the same run is enqueued more than once at the same timestamp.
- Queue watcher tradeoff: the new adapter uses a backend-neutral polling watcher rather than the old filesystem watcher. This keeps `internal/persis/store` independent from `internal/persis/filequeue`, and the scheduler already has interval-based processing as a fallback.
- Dequeue tradeoff: `Collection.Claim` removes the record before the adapter decodes it. The queue store now restores the claimed record on decode/validation failure so a malformed first item is not silently dropped.
- Added a file-backed legacy compatibility test that writes the old raw queue item JSON format under `{queueDir}/{queueName}/item_*.json`. This guards the no-migration path when wiring the new adapter to `cfg.Paths.QueueDir`.
- Wiring decision: changed `internal/cmd/context.go` to construct `store.NewQueueStore(file.NewCollection(cfg.Paths.QueueDir))`. I did not switch the broader context to a single shared `file.New(cfg.Paths.DataDir)` backend yet, because `dagrun`, `proc`, and distributed stores are still on legacy packages.
- Test fixture wiring change: updated shared command/scheduler/API fixtures to construct the new queue store too. Keeping fixtures on the old queue store created mixed-backend tests where the command deleted records through the new store but assertions read stale legacy queue indexes.
- Scheduler package boundary cleanup: moved `WatermarkStore` out of `internal/persis/store` so package-local tests under `internal/service/scheduler` can instantiate `store.NewQueueStore` without an import cycle.
- Cleanup: `DequeueByName` and queue listing now use the `{queueName}/item_` prefix so legacy `.queue-index.json` files are never claimed as queue items.
- Cleanup: moved `WatermarkStore` to `internal/persis/schedulerstore` so the generic `internal/persis/store` package no longer imports scheduler. Scheduler package tests can now use `store.NewQueueStore`.
- Cleanup: split queue item parsing, cursor handling, and watcher polling into separate files.
- Removed the legacy `internal/persis/filequeue` package after verifying it had no non-test consumers. Legacy file layout coverage now lives in `internal/persis/store/queue_test.go`.
- Documentation cleanup: updated `refactor_persis_layer.html` so the queue slice is marked complete, `filequeue` is described as removed, and the current wiring shows `store.NewQueueStore(file.NewCollection(cfg.Paths.QueueDir))`.
- Coverage cleanup: added focused queue-store tests for invalid enqueue input, normalized item deletion, cursor edge cases, invalid record parsing, queue metadata parsing, and the polling watcher.
- Review cleanup: `DeleteByItemIDs` now uses an optional exact-delete collection extension (`DeleteIfExists`) so corrupt queue item records can still be deleted and missing records are counted accurately without a pre-read race.
- Review cleanup: `DequeueByName` restores a claimed record if payload validation fails. This preserves malformed or legacy-incompatible records for inspection instead of dropping them during decode.
- Review cleanup: queue list APIs now surface malformed queue item records instead of silently skipping them; `QueueList` derives names from record IDs so broken payloads still keep the queue visible.
- Review cleanup: `ListCursor` uses a collection-backed queue read index plus an in-process cache. This avoids decoding every queued item on every cursor page while keeping a rebuild path for legacy directories and stale indexes. File and memory collections expose `RecordVersion` so the cache can be invalidated when the persisted index changes.
- Review cleanup: the polling queue watcher now snapshots queue item IDs and emits only when the queue changes, instead of notifying on every tick.
- Documentation drift cleanup: escaped Mermaid arrows in `refactor_persis_layer.html` as HTML entities and updated stale PR #2194 wording to PR #2197 status text dated May 24, 2026.
- Follow-up review cleanup: file-backed queue listing now discovers item IDs without decoding payloads, so corrupt/truncated queue files remain visible to `List`, `Len`, `All`, `QueueList`, and watcher fingerprints. Invalid items are returned with `Data()` errors instead of making the whole queue listing fail.
- Follow-up review cleanup: scheduler dispatch selection now skips invalid queued items and continues to healthy items behind them, avoiding a head-of-line block from one malformed payload.
- Follow-up review cleanup: file-backed queue mutations and cursor-index rebuilds run under a per-queue `dirlock` via an optional collection lock hook, so persisted queue index updates are serialized across Dagu processes.

## 2026-05-31

- Success invariant: `harness.run` remains compatible with existing CLI harness providers while adding an explicit built-in agent selector.
- Literal provider string chosen for the new selector: `builtin`.
- Provider omission remains invalid for now. This keeps missing or misspelled provider configuration from silently running the built-in agent and preserves the existing harness validation contract.
- Added failing contract tests before implementation:
  - `provider: builtin` is accepted in runtime harness config.
  - top-level `harness.provider: builtin` is inherited by `action: harness.run`.
  - `builtin` is reserved and cannot be redefined under `harnesses:`.
  - builtin harness config rejects CLI-only pass-through flags instead of treating typos as agent settings.
- Implementation direction: `provider: builtin` delegates to the existing `agent.run` executor in-process. It does not register a fake CLI binary provider.
- `with.stdin` on `harness.run` is preserved for `builtin` by appending it to the user prompt with a blank line, matching custom harness `prompt_mode: stdin` behavior.
- JSON schema/docs now expose `builtin` as a reserved provider. The schema allows agent-shaped fields such as `tools` and `memory` in harness config because step-level harness config can inherit `provider: builtin` from the DAG-level `harness:` block.
- Chat-message persistence and push-back history are enabled only for harness steps whose configured attempt list includes `builtin`. Marking the whole `harness` executor type as agent-capable would make ordinary CLI harness steps fail agent-message validation.
- Fallbacks can mix `builtin` and CLI providers in either order. A failed `builtin` attempt discards its stdout spool like failed CLI attempts; successful `builtin` attempts persist the agent conversation for downstream agent steps and push-back.
- Cleanup: split built-in harness provider taxonomy into all built-ins and CLI-only built-ins. Runtime CLI provider resolution, flag normalization, and sync tests now use the CLI-only helper so `builtin` is not treated like a registered binary provider.

## 2026-06-01

- Review cleanup: builtin harness validation now accepts top-level `max-iterations`, `safe-mode`, and `web-search` aliases by canonicalizing them to the agent config field names before parsing. The documented field names remain snake_case.
- Review cleanup: builtin harness execution snapshots parent/run context errors before calling the local cleanup cancel function, so a parent cancellation is treated as cancellation without turning every successful run into a canceled run.
- Review cleanup: schema wording now distinguishes CLI pass-through keys from validated `provider: builtin` agent fields.
