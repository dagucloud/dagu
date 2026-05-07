-- name: CreateWorkspace :exec
INSERT INTO dagu_workspaces (
    id,
    name,
    data,
    created_at,
    updated_at
) VALUES (
    sqlc.arg(id),
    sqlc.arg(name),
    sqlc.arg(data),
    sqlc.arg(created_at),
    sqlc.arg(updated_at)
);

-- name: GetWorkspaceByID :one
SELECT *
FROM dagu_workspaces
WHERE id = sqlc.arg(id);

-- name: GetWorkspaceByName :one
SELECT *
FROM dagu_workspaces
WHERE name = sqlc.arg(name);

-- name: ListWorkspaces :many
SELECT *
FROM dagu_workspaces
ORDER BY name ASC, id ASC;

-- name: UpdateWorkspace :execrows
UPDATE dagu_workspaces
SET name = sqlc.arg(name),
    data = sqlc.arg(data),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: DeleteWorkspace :execrows
DELETE FROM dagu_workspaces
WHERE id = sqlc.arg(id);
