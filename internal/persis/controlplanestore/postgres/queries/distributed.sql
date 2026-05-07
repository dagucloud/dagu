-- name: EnqueueDispatchTask :exec
INSERT INTO dagu_dispatch_tasks (
    id,
    queue_name,
    attempt_key,
    worker_selector,
    task_data,
    enqueued_at
) VALUES (
    sqlc.arg(id),
    sqlc.arg(queue_name),
    sqlc.arg(attempt_key),
    sqlc.arg(worker_selector),
    sqlc.arg(task_data),
    sqlc.arg(enqueued_at)
);

-- name: FindClaimableDispatchTask :one
SELECT *
FROM dagu_dispatch_tasks
WHERE (claim_token IS NULL OR claim_expires_at <= now())
  AND worker_selector <@ sqlc.arg(worker_labels)::jsonb
ORDER BY enqueued_at ASC, id ASC
FOR UPDATE SKIP LOCKED
LIMIT 1;

-- name: ClaimDispatchTaskByID :one
UPDATE dagu_dispatch_tasks
SET claim_token = sqlc.arg(claim_token),
    claimed_at = sqlc.arg(claimed_at),
    claim_expires_at = sqlc.arg(claim_expires_at),
    worker_id = sqlc.arg(worker_id),
    poller_id = sqlc.arg(poller_id),
    owner_id = NULLIF(sqlc.arg(owner_id), ''),
    owner_host = NULLIF(sqlc.arg(owner_host), ''),
    owner_port = sqlc.arg(owner_port),
    task_data = sqlc.arg(task_data),
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: GetDispatchTaskClaim :one
SELECT *
FROM dagu_dispatch_tasks
WHERE claim_token = sqlc.arg(claim_token);

-- name: DeleteDispatchTaskClaim :execrows
DELETE FROM dagu_dispatch_tasks
WHERE claim_token = sqlc.arg(claim_token);

-- name: CountOutstandingDispatchTasksByQueue :one
SELECT count(*)::integer
FROM dagu_dispatch_tasks
WHERE (sqlc.arg(queue_name)::text = '' OR queue_name = sqlc.arg(queue_name)::text)
  AND (claim_token IS NULL OR claim_expires_at > now());

-- name: HasOutstandingDispatchTaskAttempt :one
SELECT EXISTS (
    SELECT 1
    FROM dagu_dispatch_tasks
    WHERE attempt_key = sqlc.arg(attempt_key)
      AND (claim_token IS NULL OR claim_expires_at > now())
);

-- name: DeleteExpiredPendingDispatchTasks :execrows
DELETE FROM dagu_dispatch_tasks
WHERE claim_token IS NULL
  AND enqueued_at <= now() - sqlc.arg(stale_seconds)::bigint * interval '1 second';

-- name: UpsertWorkerHeartbeat :exec
INSERT INTO dagu_worker_heartbeats (
    worker_id,
    labels,
    stats,
    last_heartbeat_at,
    data
) VALUES (
    sqlc.arg(worker_id),
    sqlc.arg(labels),
    sqlc.arg(stats),
    sqlc.arg(last_heartbeat_at),
    sqlc.arg(data)
)
ON CONFLICT (worker_id) DO UPDATE
SET labels = EXCLUDED.labels,
    stats = EXCLUDED.stats,
    last_heartbeat_at = EXCLUDED.last_heartbeat_at,
    data = EXCLUDED.data,
    updated_at = now();

-- name: GetWorkerHeartbeat :one
SELECT *
FROM dagu_worker_heartbeats
WHERE worker_id = sqlc.arg(worker_id);

-- name: ListWorkerHeartbeats :many
SELECT *
FROM dagu_worker_heartbeats
ORDER BY worker_id ASC;

-- name: DeleteStaleWorkerHeartbeats :execrows
DELETE FROM dagu_worker_heartbeats
WHERE last_heartbeat_at < sqlc.arg(before);

-- name: UpsertDAGRunLease :exec
INSERT INTO dagu_dag_run_leases (
    attempt_key,
    dag_name,
    dag_run_id,
    root_dag_name,
    root_dag_run_id,
    attempt_id,
    queue_name,
    worker_id,
    owner_id,
    owner_host,
    owner_port,
    claimed_at,
    last_heartbeat_at,
    data
) VALUES (
    sqlc.arg(attempt_key),
    sqlc.arg(dag_name),
    sqlc.arg(dag_run_id),
    sqlc.arg(root_dag_name),
    sqlc.arg(root_dag_run_id),
    sqlc.arg(attempt_id),
    sqlc.arg(queue_name),
    sqlc.arg(worker_id),
    NULLIF(sqlc.arg(owner_id), ''),
    NULLIF(sqlc.arg(owner_host), ''),
    sqlc.arg(owner_port),
    sqlc.arg(claimed_at),
    sqlc.arg(last_heartbeat_at),
    sqlc.arg(data)
)
ON CONFLICT (attempt_key) DO UPDATE
SET dag_name = EXCLUDED.dag_name,
    dag_run_id = EXCLUDED.dag_run_id,
    root_dag_name = EXCLUDED.root_dag_name,
    root_dag_run_id = EXCLUDED.root_dag_run_id,
    attempt_id = EXCLUDED.attempt_id,
    queue_name = EXCLUDED.queue_name,
    worker_id = EXCLUDED.worker_id,
    owner_id = EXCLUDED.owner_id,
    owner_host = EXCLUDED.owner_host,
    owner_port = EXCLUDED.owner_port,
    claimed_at = EXCLUDED.claimed_at,
    last_heartbeat_at = EXCLUDED.last_heartbeat_at,
    data = EXCLUDED.data,
    updated_at = now();

-- name: TouchDAGRunLease :execrows
UPDATE dagu_dag_run_leases
SET last_heartbeat_at = sqlc.arg(last_heartbeat_at),
    data = jsonb_set(data, '{lastHeartbeatAt}', to_jsonb(sqlc.arg(last_heartbeat_at_millis)::bigint), true),
    updated_at = now()
WHERE attempt_key = sqlc.arg(attempt_key);

-- name: DeleteDAGRunLease :execrows
DELETE FROM dagu_dag_run_leases
WHERE attempt_key = sqlc.arg(attempt_key);

-- name: GetDAGRunLease :one
SELECT *
FROM dagu_dag_run_leases
WHERE attempt_key = sqlc.arg(attempt_key);

-- name: ListDAGRunLeasesByQueue :many
SELECT *
FROM dagu_dag_run_leases
WHERE queue_name = sqlc.arg(queue_name)
ORDER BY last_heartbeat_at DESC, attempt_key ASC;

-- name: ListAllDAGRunLeases :many
SELECT *
FROM dagu_dag_run_leases
ORDER BY last_heartbeat_at DESC, attempt_key ASC;

-- name: UpsertActiveDistributedRun :exec
INSERT INTO dagu_active_distributed_runs (
    attempt_key,
    dag_name,
    dag_run_id,
    root_dag_name,
    root_dag_run_id,
    attempt_id,
    worker_id,
    status,
    observed_at,
    data
) VALUES (
    sqlc.arg(attempt_key),
    sqlc.arg(dag_name),
    sqlc.arg(dag_run_id),
    sqlc.arg(root_dag_name),
    sqlc.arg(root_dag_run_id),
    sqlc.arg(attempt_id),
    sqlc.arg(worker_id),
    sqlc.arg(status),
    sqlc.arg(observed_at),
    sqlc.arg(data)
)
ON CONFLICT (attempt_key) DO UPDATE
SET dag_name = EXCLUDED.dag_name,
    dag_run_id = EXCLUDED.dag_run_id,
    root_dag_name = EXCLUDED.root_dag_name,
    root_dag_run_id = EXCLUDED.root_dag_run_id,
    attempt_id = EXCLUDED.attempt_id,
    worker_id = EXCLUDED.worker_id,
    status = EXCLUDED.status,
    observed_at = EXCLUDED.observed_at,
    data = EXCLUDED.data,
    updated_at = now();

-- name: DeleteActiveDistributedRun :execrows
DELETE FROM dagu_active_distributed_runs
WHERE attempt_key = sqlc.arg(attempt_key);

-- name: GetActiveDistributedRun :one
SELECT *
FROM dagu_active_distributed_runs
WHERE attempt_key = sqlc.arg(attempt_key);

-- name: ListActiveDistributedRuns :many
SELECT *
FROM dagu_active_distributed_runs
ORDER BY observed_at DESC, attempt_key ASC;
