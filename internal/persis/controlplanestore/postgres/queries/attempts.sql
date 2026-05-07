-- name: LockDAGRunKey :exec
SELECT pg_advisory_xact_lock(
    hashtext(sqlc.arg(lock_key)::text),
    hashtext('dagu-dag-run:' || sqlc.arg(lock_key)::text)
);

-- name: CreateRun :one
INSERT INTO dagu_dag_runs (
    id,
    dag_name,
    dag_run_id,
    root_dag_name,
    root_dag_run_id,
    is_root,
    run_created_at,
    workspace,
    workspace_valid
) VALUES (
    sqlc.arg(id),
    sqlc.arg(dag_name),
    sqlc.arg(dag_run_id),
    sqlc.arg(root_dag_name),
    sqlc.arg(root_dag_run_id),
    sqlc.arg(is_root),
    sqlc.arg(run_created_at),
    sqlc.narg(workspace),
    sqlc.arg(workspace_valid)
)
RETURNING *;

-- name: FindRootRun :one
SELECT *
FROM dagu_dag_runs
WHERE is_root
  AND dag_name = sqlc.arg(dag_name)
  AND dag_run_id = sqlc.arg(dag_run_id)
LIMIT 1;

-- name: FindSubRun :one
SELECT *
FROM dagu_dag_runs
WHERE NOT is_root
  AND root_dag_name = sqlc.arg(root_dag_name)
  AND root_dag_run_id = sqlc.arg(root_dag_run_id)
  AND dag_run_id = sqlc.arg(dag_run_id)
LIMIT 1;

-- name: CreateAttempt :one
INSERT INTO dagu_dag_run_attempts (
    id,
    run_id,
    dag_name,
    dag_run_id,
    root_dag_name,
    root_dag_run_id,
    is_root,
    attempt_id,
    run_created_at,
    attempt_created_at,
    workspace,
    workspace_valid,
    data,
    local_work_dir
) VALUES (
    sqlc.arg(id),
    sqlc.arg(run_id),
    sqlc.arg(dag_name),
    sqlc.arg(dag_run_id),
    sqlc.arg(root_dag_name),
    sqlc.arg(root_dag_run_id),
    sqlc.arg(is_root),
    sqlc.arg(attempt_id),
    sqlc.arg(run_created_at),
    sqlc.arg(attempt_created_at),
    sqlc.narg(workspace),
    sqlc.arg(workspace_valid),
    sqlc.arg(data),
    sqlc.arg(local_work_dir)
)
RETURNING *;

-- name: LatestRootAttempt :one
SELECT a.*
FROM dagu_dag_runs r
JOIN dagu_dag_run_attempts a ON a.id = r.latest_attempt_id
WHERE r.is_root
  AND r.dag_name = sqlc.arg(dag_name)
  AND r.dag_run_id = sqlc.arg(dag_run_id)
  AND r.data ? 'status'
  AND NOT a.hidden
LIMIT 1;

-- name: LatestRootAttemptForUpdate :one
SELECT a.*
FROM dagu_dag_runs r
JOIN dagu_dag_run_attempts a ON a.id = r.latest_attempt_id
WHERE r.is_root
  AND r.dag_name = sqlc.arg(dag_name)
  AND r.dag_run_id = sqlc.arg(dag_run_id)
  AND r.data ? 'status'
  AND NOT a.hidden
LIMIT 1
FOR UPDATE OF r, a;

-- name: LatestSubAttempt :one
SELECT a.*
FROM dagu_dag_runs r
JOIN dagu_dag_run_attempts a ON a.id = r.latest_attempt_id
WHERE NOT r.is_root
  AND r.root_dag_name = sqlc.arg(root_dag_name)
  AND r.root_dag_run_id = sqlc.arg(root_dag_run_id)
  AND r.dag_run_id = sqlc.arg(dag_run_id)
  AND r.data ? 'status'
  AND NOT a.hidden
LIMIT 1;

-- name: LatestAttemptByName :one
SELECT a.*
FROM dagu_dag_runs r
JOIN dagu_dag_run_attempts a ON a.id = r.latest_attempt_id
WHERE r.is_root
  AND r.dag_name = sqlc.arg(dag_name)
  AND r.data ? 'status'
  AND NOT a.hidden
  AND (NOT sqlc.arg(has_from)::boolean OR r.run_created_at >= sqlc.arg(from_at)::timestamptz)
ORDER BY r.run_created_at DESC, r.dag_run_id ASC
LIMIT 1;

-- name: RecentAttemptsByName :many
SELECT a.*
FROM dagu_dag_runs r
JOIN dagu_dag_run_attempts a ON a.id = r.latest_attempt_id
WHERE r.is_root
  AND r.dag_name = sqlc.arg(dag_name)
  AND r.data ? 'status'
  AND NOT a.hidden
ORDER BY r.run_created_at DESC, r.dag_run_id ASC
LIMIT sqlc.arg(item_limit)::integer;

-- name: GetAttempt :one
SELECT *
FROM dagu_dag_run_attempts
WHERE id = sqlc.arg(id);

-- name: UpdateAttemptDAG :exec
UPDATE dagu_dag_run_attempts
SET data = jsonb_set(data, '{dag}', sqlc.arg(dag_json)::jsonb, true),
    updated_at = now()
WHERE id = sqlc.arg(id);

-- name: UpdateAttemptStatus :exec
WITH updated_attempt AS (
    UPDATE dagu_dag_run_attempts AS a
    SET data = jsonb_set(data, '{status}', sqlc.arg(status_json)::jsonb, true),
        status = sqlc.arg(status)::dagu_status_code,
        workspace = sqlc.narg(workspace),
        workspace_valid = sqlc.arg(workspace_valid),
        started_at = sqlc.narg(started_at),
        finished_at = sqlc.narg(finished_at),
        updated_at = now()
    WHERE a.id = sqlc.arg(id)
    RETURNING a.id, a.run_id, a.attempt_created_at, a.workspace, a.workspace_valid, a.status, a.started_at, a.finished_at, a.data, a.hidden
)
UPDATE dagu_dag_runs r
SET latest_attempt_id = ua.id,
    latest_attempt_created_at = ua.attempt_created_at,
    workspace = ua.workspace,
    workspace_valid = ua.workspace_valid,
    status = ua.status,
    started_at = ua.started_at,
    finished_at = ua.finished_at,
    data = jsonb_build_object('status', ua.data -> 'status'),
    updated_at = now()
FROM updated_attempt ua
WHERE r.id = ua.run_id
  AND NOT ua.hidden
  AND (
      r.latest_attempt_id IS NULL
      OR r.latest_attempt_created_at IS NULL
      OR ua.attempt_created_at > r.latest_attempt_created_at
      OR (ua.attempt_created_at = r.latest_attempt_created_at AND ua.id >= r.latest_attempt_id)
  );

-- name: UpdateAttemptOutputs :exec
UPDATE dagu_dag_run_attempts
SET data = CASE
        WHEN sqlc.narg(outputs_json)::jsonb IS NULL THEN data - 'outputs'
        ELSE jsonb_set(data, '{outputs}', sqlc.narg(outputs_json)::jsonb, true)
    END,
    updated_at = now()
WHERE id = sqlc.arg(id);

-- name: UpdateAttemptMessages :exec
UPDATE dagu_dag_run_attempts
SET data = jsonb_set(data, '{messages}', sqlc.arg(messages_json)::jsonb, true),
    updated_at = now()
WHERE id = sqlc.arg(id);

-- name: MergeAttemptStepMessages :exec
UPDATE dagu_dag_run_attempts
SET data = jsonb_set(
        jsonb_set(data, '{messages}', coalesce(data -> 'messages', '{}'::jsonb), true),
        ARRAY['messages', sqlc.arg(step_name)::text],
        sqlc.arg(step_messages_json)::jsonb,
        true
    ),
    updated_at = now()
WHERE id = sqlc.arg(id);

-- name: SetAttemptCancelRequested :exec
UPDATE dagu_dag_run_attempts
SET cancel_requested = true,
    updated_at = now()
WHERE id = sqlc.arg(id);

-- name: SetAttemptHidden :exec
WITH hidden_attempt AS (
    UPDATE dagu_dag_run_attempts AS a
    SET hidden = true,
        updated_at = now()
    WHERE a.id = sqlc.arg(id)
    RETURNING a.id, a.run_id
),
latest AS (
    SELECT a.*
    FROM dagu_dag_run_attempts a
    JOIN hidden_attempt h ON a.run_id = h.run_id
    WHERE a.id <> h.id
      AND NOT a.hidden
      AND a.data ? 'status'
    ORDER BY a.attempt_created_at DESC, a.id DESC
    LIMIT 1
),
summary_updated AS (
    UPDATE dagu_dag_runs r
    SET latest_attempt_id = l.id,
        latest_attempt_created_at = l.attempt_created_at,
        workspace = l.workspace,
        workspace_valid = l.workspace_valid,
        status = l.status,
        started_at = l.started_at,
        finished_at = l.finished_at,
        data = jsonb_build_object('status', l.data -> 'status'),
        updated_at = now()
    FROM latest l
    WHERE r.id = l.run_id
    RETURNING r.id
)
UPDATE dagu_dag_runs r
SET latest_attempt_id = NULL,
    latest_attempt_created_at = NULL,
    workspace = NULL,
    workspace_valid = true,
    status = NULL,
    started_at = NULL,
    finished_at = NULL,
    data = '{}'::jsonb,
    updated_at = now()
WHERE r.id IN (SELECT run_id FROM hidden_attempt)
  AND NOT EXISTS (SELECT 1 FROM summary_updated);

-- name: ListRootStatusRows :many
SELECT *
FROM dagu_dag_runs
WHERE is_root
  AND data ? 'status'
  AND (sqlc.arg(exact_name)::text = '' OR dag_name::text = sqlc.arg(exact_name)::text)
  AND (sqlc.arg(name_contains)::text = '' OR data #>> '{status,name}' ILIKE '%' || sqlc.arg(name_contains)::text || '%')
  AND (sqlc.arg(dag_run_id_contains)::text = '' OR dag_run_id::text LIKE '%' || sqlc.arg(dag_run_id_contains)::text || '%')
  AND (NOT sqlc.arg(has_from)::boolean OR run_created_at >= sqlc.arg(from_at)::timestamptz)
  AND (NOT sqlc.arg(has_to)::boolean OR run_created_at <= sqlc.arg(to_at)::timestamptz)
  AND (cardinality(sqlc.arg(statuses)::integer[]) = 0 OR status = ANY(sqlc.arg(statuses)::integer[]))
  AND (
      NOT sqlc.arg(workspace_filter_enabled)::boolean
      OR (
          workspace_valid
          AND (
              (workspace IS NULL AND sqlc.arg(include_unlabelled)::boolean)
              OR workspace::text = ANY(sqlc.arg(workspaces)::text[])
          )
      )
  )
  AND (
      NOT sqlc.arg(cursor_set)::boolean
      OR run_created_at < sqlc.arg(cursor_timestamp)::timestamptz
      OR (
          run_created_at = sqlc.arg(cursor_timestamp)::timestamptz
          AND dag_name::text > sqlc.arg(cursor_name)::text
      )
      OR (
          run_created_at = sqlc.arg(cursor_timestamp)::timestamptz
          AND dag_name::text = sqlc.arg(cursor_name)::text
          AND dag_run_id::text > sqlc.arg(cursor_dag_run_id)::text
      )
  )
ORDER BY run_created_at DESC, dag_name ASC, dag_run_id ASC
LIMIT sqlc.arg(page_limit)::integer;

-- name: ListRemovableRunsByDays :many
SELECT dag_run_id
FROM dagu_dag_runs
WHERE is_root
  AND dag_name = sqlc.arg(dag_name)
  AND run_created_at < sqlc.arg(cutoff)::timestamptz
  AND updated_at < sqlc.arg(cutoff)::timestamptz
  AND data ? 'status'
  AND status <> ALL(sqlc.arg(active_statuses)::integer[])
ORDER BY run_created_at ASC, dag_run_id ASC;

-- name: ListRemovableRunsByCount :many
WITH terminal AS (
    SELECT r.dag_run_id, r.run_created_at
    FROM dagu_dag_runs AS r
    WHERE r.is_root
      AND r.dag_name = sqlc.arg(dag_name)
      AND r.data ? 'status'
      AND r.status <> ALL(sqlc.arg(active_statuses)::integer[])
),
ranked AS (
    SELECT r.dag_run_id, r.run_created_at
    FROM dagu_dag_runs AS r
    WHERE r.is_root
      AND r.dag_name = sqlc.arg(dag_name)
    ORDER BY r.run_created_at DESC, r.dag_run_id ASC
    OFFSET sqlc.arg(retention_runs)::integer
),
removable AS (
    SELECT ranked.dag_run_id, ranked.run_created_at
    FROM ranked
    JOIN terminal USING (dag_run_id)
)
SELECT dag_run_id
FROM removable
ORDER BY run_created_at DESC, dag_run_id ASC;

-- name: DeleteDAGRunRows :many
WITH root_run AS (
    SELECT r.id
    FROM dagu_dag_runs AS r
    WHERE r.is_root
      AND r.dag_name = sqlc.arg(root_dag_name)
      AND r.dag_run_id = sqlc.arg(root_dag_run_id)
),
doomed AS (
    SELECT r.id, r.dag_run_id
    FROM dagu_dag_runs AS r
    WHERE r.id IN (SELECT id FROM root_run)
       OR (
           r.root_dag_name = sqlc.arg(root_dag_name)
           AND r.root_dag_run_id = sqlc.arg(root_dag_run_id)
       )
),
deleted AS (
    DELETE FROM dagu_dag_runs AS r
    WHERE r.id IN (SELECT id FROM doomed)
)
SELECT DISTINCT dag_run_id
FROM doomed
ORDER BY dag_run_id;

-- name: RenameDAGRuns :exec
WITH renamed_runs AS (
    UPDATE dagu_dag_runs
    SET dag_name = CASE WHEN is_root AND dag_name::text = sqlc.arg(old_name)::text THEN sqlc.arg(new_name) ELSE dag_name END,
        root_dag_name = CASE WHEN root_dag_name::text = sqlc.arg(old_name)::text THEN sqlc.arg(new_name) ELSE root_dag_name END,
        data = CASE
            WHEN is_root
             AND dag_name::text = sqlc.arg(old_name)::text
             AND data ? 'status'
            THEN jsonb_set(data, '{status,name}', to_jsonb(sqlc.arg(new_name)::text), true)
            ELSE data
        END,
        updated_at = now()
    WHERE root_dag_name::text = sqlc.arg(old_name)::text
       OR (is_root AND dag_name::text = sqlc.arg(old_name)::text)
    RETURNING id
)
UPDATE dagu_dag_run_attempts
SET dag_name = CASE WHEN is_root AND dag_name::text = sqlc.arg(old_name)::text THEN sqlc.arg(new_name) ELSE dag_name END,
    root_dag_name = CASE WHEN root_dag_name::text = sqlc.arg(old_name)::text THEN sqlc.arg(new_name) ELSE root_dag_name END,
    data = CASE
        WHEN is_root
         AND dag_name::text = sqlc.arg(old_name)::text
         AND data ? 'status'
        THEN jsonb_set(data, '{status,name}', to_jsonb(sqlc.arg(new_name)::text), true)
        ELSE data
    END,
    updated_at = now()
WHERE run_id IN (SELECT id FROM renamed_runs);
