-- name: CreateUser :exec
INSERT INTO dagu_users (
    id,
    username,
    password_hash,
    role,
    auth_provider,
    oidc_issuer,
    oidc_subject,
    is_disabled,
    workspace_access,
    data,
    created_at,
    updated_at
) VALUES (
    sqlc.arg(id),
    sqlc.arg(username),
    sqlc.arg(password_hash),
    sqlc.arg(role),
    NULLIF(sqlc.arg(auth_provider), ''),
    NULLIF(sqlc.arg(oidc_issuer), ''),
    NULLIF(sqlc.arg(oidc_subject), ''),
    sqlc.arg(is_disabled),
    sqlc.arg(workspace_access),
    sqlc.arg(data),
    sqlc.arg(created_at),
    sqlc.arg(updated_at)
);

-- name: GetUserByID :one
SELECT *
FROM dagu_users
WHERE id = sqlc.arg(id);

-- name: GetUserByUsername :one
SELECT *
FROM dagu_users
WHERE username = sqlc.arg(username);

-- name: GetUserByOIDCIdentity :one
SELECT *
FROM dagu_users
WHERE oidc_issuer = sqlc.arg(oidc_issuer)
  AND oidc_subject = sqlc.arg(oidc_subject);

-- name: ListUsers :many
SELECT *
FROM dagu_users
ORDER BY username ASC, id ASC;

-- name: UpdateUser :execrows
UPDATE dagu_users
SET username = sqlc.arg(username),
    password_hash = sqlc.arg(password_hash),
    role = sqlc.arg(role),
    auth_provider = NULLIF(sqlc.arg(auth_provider), ''),
    oidc_issuer = NULLIF(sqlc.arg(oidc_issuer), ''),
    oidc_subject = NULLIF(sqlc.arg(oidc_subject), ''),
    is_disabled = sqlc.arg(is_disabled),
    workspace_access = sqlc.arg(workspace_access),
    data = sqlc.arg(data),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: DeleteUser :execrows
DELETE FROM dagu_users
WHERE id = sqlc.arg(id);

-- name: CountUsers :one
SELECT count(*)::bigint
FROM dagu_users;

-- name: CreateAPIKey :exec
INSERT INTO dagu_api_keys (
    id,
    name,
    role,
    key_hash,
    key_prefix,
    created_by,
    workspace_access,
    last_used_at,
    data,
    created_at,
    updated_at
) VALUES (
    sqlc.arg(id),
    sqlc.arg(name),
    sqlc.arg(role),
    sqlc.arg(key_hash),
    sqlc.arg(key_prefix),
    sqlc.arg(created_by),
    sqlc.arg(workspace_access),
    sqlc.arg(last_used_at),
    sqlc.arg(data),
    sqlc.arg(created_at),
    sqlc.arg(updated_at)
);

-- name: GetAPIKeyByID :one
SELECT *
FROM dagu_api_keys
WHERE id = sqlc.arg(id);

-- name: ListAPIKeys :many
SELECT *
FROM dagu_api_keys
ORDER BY created_at DESC, id DESC;

-- name: UpdateAPIKey :execrows
UPDATE dagu_api_keys
SET name = sqlc.arg(name),
    role = sqlc.arg(role),
    key_hash = sqlc.arg(key_hash),
    key_prefix = sqlc.arg(key_prefix),
    created_by = sqlc.arg(created_by),
    workspace_access = sqlc.arg(workspace_access),
    last_used_at = sqlc.arg(last_used_at),
    data = sqlc.arg(data),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: DeleteAPIKey :execrows
DELETE FROM dagu_api_keys
WHERE id = sqlc.arg(id);

-- name: UpdateAPIKeyLastUsed :execrows
UPDATE dagu_api_keys
SET last_used_at = sqlc.arg(last_used_at),
    data = jsonb_set(data, '{last_used_at}', to_jsonb(sqlc.arg(last_used_at)::timestamptz), true),
    updated_at = now()
WHERE id = sqlc.arg(id);

-- name: CreateWebhook :exec
INSERT INTO dagu_webhooks (
    id,
    dag_name,
    token_hash,
    token_prefix,
    enabled,
    auth_mode,
    hmac_enforcement_mode,
    created_by,
    last_used_at,
    data,
    created_at,
    updated_at
) VALUES (
    sqlc.arg(id),
    sqlc.arg(dag_name),
    sqlc.arg(token_hash),
    sqlc.arg(token_prefix),
    sqlc.arg(enabled),
    NULLIF(sqlc.arg(auth_mode), ''),
    NULLIF(sqlc.arg(hmac_enforcement_mode), ''),
    sqlc.arg(created_by),
    sqlc.arg(last_used_at),
    sqlc.arg(data),
    sqlc.arg(created_at),
    sqlc.arg(updated_at)
);

-- name: GetWebhookByID :one
SELECT *
FROM dagu_webhooks
WHERE id = sqlc.arg(id);

-- name: GetWebhookByDAGName :one
SELECT *
FROM dagu_webhooks
WHERE dag_name = sqlc.arg(dag_name);

-- name: ListWebhooks :many
SELECT *
FROM dagu_webhooks
ORDER BY dag_name ASC, id ASC;

-- name: UpdateWebhook :execrows
UPDATE dagu_webhooks
SET dag_name = sqlc.arg(dag_name),
    token_hash = sqlc.arg(token_hash),
    token_prefix = sqlc.arg(token_prefix),
    enabled = sqlc.arg(enabled),
    auth_mode = NULLIF(sqlc.arg(auth_mode), ''),
    hmac_enforcement_mode = NULLIF(sqlc.arg(hmac_enforcement_mode), ''),
    created_by = sqlc.arg(created_by),
    last_used_at = sqlc.arg(last_used_at),
    data = sqlc.arg(data),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: DeleteWebhook :execrows
DELETE FROM dagu_webhooks
WHERE id = sqlc.arg(id);

-- name: DeleteWebhookByDAGName :execrows
DELETE FROM dagu_webhooks
WHERE dag_name = sqlc.arg(dag_name);

-- name: UpdateWebhookLastUsed :execrows
UPDATE dagu_webhooks
SET last_used_at = sqlc.arg(last_used_at),
    data = jsonb_set(data, '{lastUsedAt}', to_jsonb(sqlc.arg(last_used_at)::timestamptz), true),
    updated_at = now()
WHERE id = sqlc.arg(id);
