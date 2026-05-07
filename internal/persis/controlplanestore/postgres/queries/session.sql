-- name: CreateAgentSession :exec
INSERT INTO dagu_agent_sessions (
    id,
    user_id,
    dag_name,
    title,
    model,
    parent_session_id,
    delegate_task,
    data,
    created_at,
    updated_at
) VALUES (
    sqlc.arg(id),
    sqlc.arg(user_id),
    NULLIF(sqlc.arg(dag_name), ''),
    NULLIF(sqlc.arg(title), ''),
    NULLIF(sqlc.arg(model), ''),
    sqlc.arg(parent_session_id),
    NULLIF(sqlc.arg(delegate_task), ''),
    sqlc.arg(data),
    sqlc.arg(created_at),
    sqlc.arg(updated_at)
);

-- name: GetAgentSession :one
SELECT *
FROM dagu_agent_sessions
WHERE id = sqlc.arg(id);

-- name: ListAgentSessionsByUser :many
SELECT *
FROM dagu_agent_sessions
WHERE user_id = sqlc.arg(user_id)
ORDER BY updated_at DESC, id DESC;

-- name: ListAgentSubSessions :many
SELECT *
FROM dagu_agent_sessions
WHERE parent_session_id = sqlc.arg(parent_session_id)
ORDER BY updated_at DESC, id DESC;

-- name: UpdateAgentSession :execrows
UPDATE dagu_agent_sessions
SET user_id = sqlc.arg(user_id),
    dag_name = NULLIF(sqlc.arg(dag_name), ''),
    title = NULLIF(sqlc.arg(title), ''),
    model = NULLIF(sqlc.arg(model), ''),
    parent_session_id = sqlc.arg(parent_session_id),
    delegate_task = NULLIF(sqlc.arg(delegate_task), ''),
    data = sqlc.arg(data),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: TouchAgentSessionUpdatedAt :execrows
UPDATE dagu_agent_sessions
SET updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: DeleteAgentSession :execrows
DELETE FROM dagu_agent_sessions
WHERE id = sqlc.arg(id);

-- name: AddAgentSessionMessage :exec
INSERT INTO dagu_agent_session_messages (
    id,
    session_id,
    message_type,
    sequence_id,
    created_at,
    data
) VALUES (
    sqlc.arg(id),
    sqlc.arg(session_id),
    sqlc.arg(message_type),
    sqlc.arg(sequence_id),
    sqlc.arg(created_at),
    sqlc.arg(data)
);

-- name: ListAgentSessionMessages :many
SELECT *
FROM dagu_agent_session_messages
WHERE session_id = sqlc.arg(session_id)
ORDER BY sequence_id ASC, id ASC;

-- name: GetLatestAgentSessionSequenceID :one
SELECT COALESCE(max(sequence_id), 0)::bigint
FROM dagu_agent_session_messages
WHERE session_id = sqlc.arg(session_id);
