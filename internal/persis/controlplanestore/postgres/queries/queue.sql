-- name: EnqueueQueueItem :exec
INSERT INTO dagu_queue_items (
    id,
    queue_name,
    priority,
    dag_name,
    dag_run_id,
    data,
    enqueued_at
) VALUES (
    sqlc.arg(id),
    sqlc.arg(queue_name),
    sqlc.arg(priority),
    sqlc.arg(dag_name),
    sqlc.arg(dag_run_id),
    sqlc.arg(data),
    sqlc.arg(enqueued_at)
);

-- name: DequeueQueueItemByName :one
WITH picked AS (
    SELECT id
    FROM dagu_queue_items AS qi
    WHERE qi.queue_name = sqlc.arg(queue_name)
      AND (lease_token IS NULL OR lease_expires_at <= now())
    ORDER BY priority ASC, enqueued_at ASC, id ASC
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
DELETE FROM dagu_queue_items
WHERE id IN (SELECT id FROM picked)
RETURNING *;

-- name: ClaimQueueItemByID :one
WITH picked AS (
    SELECT id
    FROM dagu_queue_items AS qi
    WHERE qi.queue_name = sqlc.arg(queue_name)
      AND qi.id = sqlc.arg(id)
      AND (lease_token IS NULL OR lease_expires_at <= now())
    FOR UPDATE SKIP LOCKED
)
UPDATE dagu_queue_items
SET lease_token = sqlc.arg(lease_token),
    lease_owner = sqlc.arg(lease_owner),
    leased_at = sqlc.arg(leased_at),
    lease_expires_at = sqlc.arg(lease_expires_at),
    updated_at = now()
WHERE id IN (SELECT id FROM picked)
RETURNING *;

-- name: AckQueueItemLease :execrows
DELETE FROM dagu_queue_items
WHERE queue_name = sqlc.arg(queue_name)
  AND lease_token = sqlc.arg(lease_token);

-- name: ReleaseQueueItemLease :execrows
UPDATE dagu_queue_items
SET lease_token = NULL,
    lease_owner = NULL,
    leased_at = NULL,
    lease_expires_at = NULL,
    updated_at = now()
WHERE queue_name = sqlc.arg(queue_name)
  AND lease_token = sqlc.arg(lease_token);

-- name: DeleteQueueItemsByDAGRun :many
DELETE FROM dagu_queue_items
WHERE queue_name = sqlc.arg(queue_name)
  AND dag_name = sqlc.arg(dag_name)
  AND dag_run_id = sqlc.arg(dag_run_id)
RETURNING *;

-- name: DeleteQueueItemsByIDs :many
DELETE FROM dagu_queue_items
WHERE queue_name = sqlc.arg(queue_name)
  AND id = ANY(sqlc.arg(ids)::uuid[])
RETURNING *;

-- name: CountQueueItemsByName :one
SELECT count(*)::integer
FROM dagu_queue_items
WHERE queue_name = sqlc.arg(queue_name);

-- name: ListQueueItemsByName :many
SELECT *
FROM dagu_queue_items
WHERE queue_name = sqlc.arg(queue_name)
ORDER BY priority ASC, enqueued_at ASC, id ASC;

-- name: ListQueueItemsByNameCursor :many
SELECT *
FROM dagu_queue_items
WHERE queue_name = sqlc.arg(queue_name)
  AND (
    NOT sqlc.arg(has_cursor)::boolean
    OR (
      priority,
      enqueued_at,
      id
    ) > (
      sqlc.arg(after_priority)::integer,
      sqlc.arg(after_enqueued_at)::timestamptz,
      sqlc.arg(after_id)::uuid
    )
  )
ORDER BY priority ASC, enqueued_at ASC, id ASC
LIMIT sqlc.arg(row_limit)::integer;

-- name: ListAllQueueItems :many
SELECT *
FROM dagu_queue_items
ORDER BY queue_name ASC, priority ASC, enqueued_at ASC, id ASC;

-- name: ListQueueItemsByDAGName :many
SELECT *
FROM dagu_queue_items
WHERE queue_name = sqlc.arg(queue_name)
  AND dag_name = sqlc.arg(dag_name)
ORDER BY priority ASC, enqueued_at ASC, id ASC;

-- name: ListQueueNames :many
SELECT DISTINCT queue_name
FROM dagu_queue_items
ORDER BY queue_name ASC;
