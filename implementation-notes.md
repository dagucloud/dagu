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
- Dequeue tradeoff: `Collection.Claim` removes the record before the adapter decodes it. That is the backend-neutral atomic-dequeue seam from the refactor plan, but it differs from the old filequeue behavior where a corrupt first file could fail before removal.
- Added a file-backed legacy compatibility test that writes the old raw queue item JSON format under `{queueDir}/{queueName}/item_*.json`. This guards the no-migration path when wiring the new adapter to `cfg.Paths.QueueDir`.
- Wiring decision: changed `internal/cmd/context.go` to construct `store.NewQueueStore(file.NewCollection(cfg.Paths.QueueDir))`. I did not switch the broader context to a single shared `file.New(cfg.Paths.DataDir)` backend yet, because `dagrun`, `proc`, and distributed stores are still on legacy packages.
- Test fixture wiring change: updated shared command/scheduler/API fixtures to construct the new queue store too. Keeping fixtures on the old queue store created mixed-backend tests where the command deleted records through the new store but assertions read stale legacy queue indexes.
- Scheduler package boundary cleanup: moved `WatermarkStore` out of `internal/persis/store` so package-local tests under `internal/service/scheduler` can instantiate `store.NewQueueStore` without an import cycle.
- Cleanup: `DequeueByName` and queue listing now use the `{queueName}/item_` prefix so legacy `.queue-index.json` files are never claimed as queue items.
- Cleanup: moved `WatermarkStore` to `internal/persis/schedulerstore` so the generic `internal/persis/store` package no longer imports scheduler. Scheduler package tests can now use `store.NewQueueStore`.
- Cleanup: split queue item parsing, cursor handling, and watcher polling into separate files.
- Removed the legacy `internal/persis/filequeue` package after verifying it had no non-test consumers. Legacy file layout coverage now lives in `internal/persis/store/queue_test.go`.
