-- name: EmitEvent :exec
INSERT INTO dagu_events (
    id,
    event_id,
    schema_version,
    occurred_at,
    recorded_at,
    kind,
    event_type,
    source_service,
    source_instance,
    dag_name,
    dag_run_id,
    attempt_id,
    session_id,
    user_id,
    model,
    status,
    event_data,
    data
) VALUES (
    sqlc.arg(id),
    sqlc.arg(event_id),
    sqlc.arg(schema_version),
    sqlc.arg(occurred_at),
    sqlc.arg(recorded_at),
    sqlc.arg(kind),
    sqlc.arg(event_type),
    sqlc.arg(source_service),
    NULLIF(sqlc.arg(source_instance), ''),
    NULLIF(sqlc.arg(dag_name), ''),
    NULLIF(sqlc.arg(dag_run_id), ''),
    NULLIF(sqlc.arg(attempt_id), ''),
    NULLIF(sqlc.arg(session_id), ''),
    NULLIF(sqlc.arg(user_id), ''),
    NULLIF(sqlc.arg(model), ''),
    NULLIF(sqlc.arg(status), ''),
    sqlc.arg(event_data),
    sqlc.arg(data)
)
ON CONFLICT (event_id) DO NOTHING;

-- name: QueryEvents :many
SELECT *
FROM dagu_events
WHERE (NOT sqlc.arg(has_kind)::boolean OR kind = sqlc.arg(kind))
  AND (NOT sqlc.arg(has_type)::boolean OR event_type = sqlc.arg(event_type))
  AND (NOT sqlc.arg(has_dag_name)::boolean OR dag_name = sqlc.arg(dag_name))
  AND (NOT sqlc.arg(has_dag_run_id)::boolean OR dag_run_id = sqlc.arg(dag_run_id))
  AND (NOT sqlc.arg(has_attempt_id)::boolean OR attempt_id = sqlc.arg(attempt_id))
  AND (NOT sqlc.arg(has_session_id)::boolean OR session_id = sqlc.arg(session_id))
  AND (NOT sqlc.arg(has_user_id)::boolean OR user_id = sqlc.arg(user_id))
  AND (NOT sqlc.arg(has_model)::boolean OR model = sqlc.arg(model))
  AND (NOT sqlc.arg(has_status)::boolean OR status = sqlc.arg(status))
  AND (NOT sqlc.arg(has_start_time)::boolean OR occurred_at >= sqlc.arg(start_time))
  AND (NOT sqlc.arg(has_end_time)::boolean OR occurred_at <= sqlc.arg(end_time))
ORDER BY occurred_at DESC, recorded_at DESC, event_id DESC
LIMIT sqlc.arg(row_limit)::integer
OFFSET sqlc.arg(row_offset)::integer;

-- name: CountEvents :one
SELECT count(*)::integer
FROM dagu_events
WHERE (NOT sqlc.arg(has_kind)::boolean OR kind = sqlc.arg(kind))
  AND (NOT sqlc.arg(has_type)::boolean OR event_type = sqlc.arg(event_type))
  AND (NOT sqlc.arg(has_dag_name)::boolean OR dag_name = sqlc.arg(dag_name))
  AND (NOT sqlc.arg(has_dag_run_id)::boolean OR dag_run_id = sqlc.arg(dag_run_id))
  AND (NOT sqlc.arg(has_attempt_id)::boolean OR attempt_id = sqlc.arg(attempt_id))
  AND (NOT sqlc.arg(has_session_id)::boolean OR session_id = sqlc.arg(session_id))
  AND (NOT sqlc.arg(has_user_id)::boolean OR user_id = sqlc.arg(user_id))
  AND (NOT sqlc.arg(has_model)::boolean OR model = sqlc.arg(model))
  AND (NOT sqlc.arg(has_status)::boolean OR status = sqlc.arg(status))
  AND (NOT sqlc.arg(has_start_time)::boolean OR occurred_at >= sqlc.arg(start_time))
  AND (NOT sqlc.arg(has_end_time)::boolean OR occurred_at <= sqlc.arg(end_time));

-- name: QueryEventsCursor :many
SELECT *
FROM dagu_events
WHERE (NOT sqlc.arg(has_kind)::boolean OR kind = sqlc.arg(kind))
  AND (NOT sqlc.arg(has_type)::boolean OR event_type = sqlc.arg(event_type))
  AND (NOT sqlc.arg(has_dag_name)::boolean OR dag_name = sqlc.arg(dag_name))
  AND (NOT sqlc.arg(has_dag_run_id)::boolean OR dag_run_id = sqlc.arg(dag_run_id))
  AND (NOT sqlc.arg(has_attempt_id)::boolean OR attempt_id = sqlc.arg(attempt_id))
  AND (NOT sqlc.arg(has_session_id)::boolean OR session_id = sqlc.arg(session_id))
  AND (NOT sqlc.arg(has_user_id)::boolean OR user_id = sqlc.arg(user_id))
  AND (NOT sqlc.arg(has_model)::boolean OR model = sqlc.arg(model))
  AND (NOT sqlc.arg(has_status)::boolean OR status = sqlc.arg(status))
  AND (NOT sqlc.arg(has_start_time)::boolean OR occurred_at >= sqlc.arg(start_time))
  AND (NOT sqlc.arg(has_end_time)::boolean OR occurred_at <= sqlc.arg(end_time))
  AND (
    NOT sqlc.arg(has_cursor)::boolean
    OR (
      occurred_at,
      recorded_at,
      event_id
    ) < (
      sqlc.arg(after_occurred_at)::timestamptz,
      sqlc.arg(after_recorded_at)::timestamptz,
      sqlc.arg(after_event_id)::text
    )
  )
ORDER BY occurred_at DESC, recorded_at DESC, event_id DESC
LIMIT sqlc.arg(row_limit)::integer;
