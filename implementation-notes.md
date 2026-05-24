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
