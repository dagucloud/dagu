-- name: AppendAuditEntry :exec
INSERT INTO dagu_audit_entries (
    id,
    occurred_at,
    category,
    action,
    user_id,
    username,
    ip_address,
    data
) VALUES (
    sqlc.arg(id),
    sqlc.arg(occurred_at),
    sqlc.arg(category),
    sqlc.arg(action),
    sqlc.arg(user_id),
    sqlc.arg(username),
    NULLIF(sqlc.arg(ip_address), ''),
    sqlc.arg(data)
);

-- name: QueryAuditEntries :many
SELECT *
FROM dagu_audit_entries
WHERE (NOT sqlc.arg(has_category)::boolean OR category = sqlc.arg(category))
  AND (NOT sqlc.arg(has_user_id)::boolean OR user_id = sqlc.arg(user_id))
  AND (NOT sqlc.arg(has_start_time)::boolean OR occurred_at >= sqlc.arg(start_time))
  AND (NOT sqlc.arg(has_end_time)::boolean OR occurred_at < sqlc.arg(end_time))
ORDER BY occurred_at DESC, id DESC
LIMIT sqlc.arg(row_limit)::integer
OFFSET sqlc.arg(row_offset)::integer;

-- name: CountAuditEntries :one
SELECT count(*)::integer
FROM dagu_audit_entries
WHERE (NOT sqlc.arg(has_category)::boolean OR category = sqlc.arg(category))
  AND (NOT sqlc.arg(has_user_id)::boolean OR user_id = sqlc.arg(user_id))
  AND (NOT sqlc.arg(has_start_time)::boolean OR occurred_at >= sqlc.arg(start_time))
  AND (NOT sqlc.arg(has_end_time)::boolean OR occurred_at < sqlc.arg(end_time));
