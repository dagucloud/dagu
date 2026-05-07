-- name: UpsertServiceInstance :exec
INSERT INTO dagu_service_instances (
    service_name,
    instance_id,
    host,
    port,
    status,
    started_at,
    last_heartbeat_at,
    data
) VALUES (
    sqlc.arg(service_name),
    sqlc.arg(instance_id),
    sqlc.arg(host),
    sqlc.arg(port),
    sqlc.arg(status),
    sqlc.arg(started_at),
    sqlc.arg(last_heartbeat_at),
    sqlc.arg(data)
)
ON CONFLICT (service_name, instance_id) DO UPDATE
SET host = EXCLUDED.host,
    port = EXCLUDED.port,
    status = EXCLUDED.status,
    started_at = EXCLUDED.started_at,
    last_heartbeat_at = EXCLUDED.last_heartbeat_at,
    data = EXCLUDED.data,
    updated_at = now();

-- name: TouchServiceInstance :execrows
UPDATE dagu_service_instances
SET last_heartbeat_at = sqlc.arg(last_heartbeat_at),
    updated_at = now()
WHERE service_name = sqlc.arg(service_name)
  AND instance_id = sqlc.arg(instance_id);

-- name: UpdateServiceInstanceStatus :execrows
UPDATE dagu_service_instances
SET status = sqlc.arg(status),
    last_heartbeat_at = now(),
    updated_at = now()
WHERE service_name = sqlc.arg(service_name)
  AND instance_id = sqlc.arg(instance_id);

-- name: DeleteServiceInstance :execrows
DELETE FROM dagu_service_instances
WHERE service_name = sqlc.arg(service_name)
  AND instance_id = sqlc.arg(instance_id);

-- name: ListActiveServiceInstances :many
SELECT *
FROM dagu_service_instances
WHERE service_name = sqlc.arg(service_name)
  AND last_heartbeat_at >= sqlc.arg(active_after)
ORDER BY last_heartbeat_at DESC, instance_id ASC;

-- name: DeleteStaleServiceInstances :execrows
DELETE FROM dagu_service_instances
WHERE service_name = sqlc.arg(service_name)
  AND last_heartbeat_at < sqlc.arg(active_after);
