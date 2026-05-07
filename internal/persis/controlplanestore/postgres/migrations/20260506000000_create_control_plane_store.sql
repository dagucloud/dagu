-- +goose Up
CREATE DOMAIN dagu_uuid_v7 AS uuid
    CHECK (VALUE::text ~* '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$');

CREATE DOMAIN dagu_dag_name AS text
    CHECK (
        VALUE <> ''
        AND VALUE <> '.'
        AND VALUE <> '..'
        AND char_length(VALUE) <= 40
        AND VALUE ~ '^[a-zA-Z0-9_.-]+$'
    );

CREATE DOMAIN dagu_dag_run_id AS text
    CHECK (
        VALUE <> ''
        AND char_length(VALUE) <= 64
        AND VALUE ~ '^[-a-zA-Z0-9_]+$'
    );

CREATE DOMAIN dagu_attempt_id AS text
    CHECK (
        VALUE <> ''
        AND char_length(VALUE) <= 64
        AND VALUE ~ '^[-a-zA-Z0-9_]+$'
    );

CREATE DOMAIN dagu_workspace_name AS text
    CHECK (
        VALUE <> ''
        AND lower(VALUE) NOT IN ('all', 'default')
        AND VALUE ~ '^[A-Za-z0-9_-]+$'
    );

CREATE DOMAIN dagu_status_code AS integer
    CHECK (VALUE BETWEEN 0 AND 8);

CREATE DOMAIN dagu_queue_priority AS integer
    CHECK (VALUE IN (0, 1));

CREATE DOMAIN dagu_service_name AS text
    CHECK (VALUE IN ('coordinator', 'scheduler'));

CREATE DOMAIN dagu_service_status AS integer
    CHECK (VALUE BETWEEN 0 AND 2);

CREATE DOMAIN dagu_auth_role AS text
    CHECK (VALUE IN ('admin', 'manager', 'developer', 'operator', 'viewer'));

CREATE DOMAIN dagu_audit_category AS text
    CHECK (VALUE IN ('terminal', 'user', 'dag', 'api_key', 'webhook', 'git_sync', 'agent', 'system', 'remote_node', 'workspace'));

CREATE DOMAIN dagu_event_kind AS text
    CHECK (VALUE IN ('dag_run', 'llm_usage'));

CREATE TABLE dagu_dag_runs (
    id dagu_uuid_v7 PRIMARY KEY,
    dag_name dagu_dag_name NOT NULL,
    dag_run_id dagu_dag_run_id NOT NULL,
    root_dag_name dagu_dag_name NOT NULL,
    root_dag_run_id dagu_dag_run_id NOT NULL,
    is_root boolean NOT NULL,
    run_created_at timestamptz NOT NULL,
    latest_attempt_id dagu_uuid_v7,
    latest_attempt_created_at timestamptz,
    workspace dagu_workspace_name,
    workspace_valid boolean NOT NULL DEFAULT true,
    status dagu_status_code,
    started_at timestamptz,
    finished_at timestamptz,
    data_version integer NOT NULL DEFAULT 1,
    data jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (workspace IS NULL OR workspace_valid),
    CHECK (data_version = 1),
    CHECK (jsonb_typeof(data) = 'object'),
    CHECK (NOT data ? 'status' OR jsonb_typeof(data -> 'status') = 'object')
);

CREATE TABLE dagu_dag_run_attempts (
    id dagu_uuid_v7 PRIMARY KEY,
    run_id dagu_uuid_v7 NOT NULL REFERENCES dagu_dag_runs(id) ON DELETE CASCADE,
    dag_name dagu_dag_name NOT NULL,
    dag_run_id dagu_dag_run_id NOT NULL,
    root_dag_name dagu_dag_name NOT NULL,
    root_dag_run_id dagu_dag_run_id NOT NULL,
    is_root boolean NOT NULL,
    attempt_id dagu_attempt_id NOT NULL,
    run_created_at timestamptz NOT NULL,
    attempt_created_at timestamptz NOT NULL,
    workspace dagu_workspace_name,
    workspace_valid boolean NOT NULL DEFAULT true,
    status dagu_status_code,
    started_at timestamptz,
    finished_at timestamptz,
    data_version integer NOT NULL DEFAULT 1,
    data jsonb NOT NULL DEFAULT '{}'::jsonb,
    cancel_requested boolean NOT NULL DEFAULT false,
    hidden boolean NOT NULL DEFAULT false,
    local_work_dir text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (workspace IS NULL OR workspace_valid),
    CHECK (data_version = 1),
    CHECK (jsonb_typeof(data) = 'object'),
    CHECK (NOT data ? 'status' OR jsonb_typeof(data -> 'status') = 'object'),
    CHECK (NOT data ? 'dag' OR jsonb_typeof(data -> 'dag') = 'object'),
    CHECK (NOT data ? 'outputs' OR jsonb_typeof(data -> 'outputs') = 'object'),
    CHECK (NOT data ? 'messages' OR jsonb_typeof(data -> 'messages') = 'object')
);

CREATE UNIQUE INDEX dagu_dag_runs_root_identity_uidx
    ON dagu_dag_runs (dag_name, dag_run_id)
    WHERE is_root;

CREATE UNIQUE INDEX dagu_dag_runs_sub_identity_uidx
    ON dagu_dag_runs (root_dag_name, root_dag_run_id, dag_run_id)
    WHERE NOT is_root;

CREATE INDEX dagu_dag_runs_list_order_idx
    ON dagu_dag_runs (run_created_at DESC, dag_name ASC, dag_run_id ASC)
    WHERE is_root AND data ? 'status';

CREATE INDEX dagu_dag_runs_workspace_idx
    ON dagu_dag_runs (workspace, run_created_at DESC)
    WHERE is_root AND data ? 'status';

CREATE INDEX dagu_dag_runs_status_idx
    ON dagu_dag_runs (status, run_created_at DESC)
    WHERE is_root AND data ? 'status';

CREATE INDEX dagu_dag_runs_started_at_idx
    ON dagu_dag_runs (started_at DESC)
    WHERE is_root AND data ? 'status';

CREATE INDEX dagu_dag_runs_finished_at_idx
    ON dagu_dag_runs (finished_at DESC)
    WHERE is_root AND data ? 'status';

CREATE INDEX dagu_dag_runs_cleanup_idx
    ON dagu_dag_runs (dag_name, run_created_at DESC, dag_run_id ASC)
    WHERE is_root;

CREATE INDEX dagu_dag_runs_data_gin_idx
    ON dagu_dag_runs USING gin (data jsonb_path_ops)
    WHERE data ? 'status';

CREATE INDEX dagu_dag_runs_labels_gin_idx
    ON dagu_dag_runs USING gin ((data #> '{status,labels}'))
    WHERE data ? 'status';

CREATE INDEX dagu_dag_run_attempts_latest_by_run_idx
    ON dagu_dag_run_attempts (run_id, attempt_created_at DESC, id DESC)
    WHERE NOT hidden AND data ? 'status';

CREATE INDEX dagu_dag_run_attempts_run_idx
    ON dagu_dag_run_attempts (run_id, attempt_created_at DESC, id DESC);

CREATE TABLE dagu_queue_items (
    id dagu_uuid_v7 PRIMARY KEY,
    queue_name text NOT NULL CHECK (queue_name <> ''),
    priority dagu_queue_priority NOT NULL,
    dag_name dagu_dag_name NOT NULL,
    dag_run_id dagu_dag_run_id NOT NULL,
    data_version integer NOT NULL DEFAULT 1,
    data jsonb NOT NULL,
    enqueued_at timestamptz NOT NULL DEFAULT now(),
    lease_token dagu_uuid_v7,
    lease_owner text,
    leased_at timestamptz,
    lease_expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (data_version = 1),
    CHECK (jsonb_typeof(data) = 'object'),
    CHECK (
        (lease_token IS NULL AND lease_owner IS NULL AND leased_at IS NULL AND lease_expires_at IS NULL)
        OR (lease_token IS NOT NULL AND lease_owner IS NOT NULL AND leased_at IS NOT NULL AND lease_expires_at IS NOT NULL)
    )
);

CREATE INDEX dagu_queue_items_ready_idx
    ON dagu_queue_items (queue_name, priority ASC, enqueued_at ASC, id ASC);

CREATE INDEX dagu_queue_items_run_idx
    ON dagu_queue_items (queue_name, dag_name, dag_run_id);

CREATE UNIQUE INDEX dagu_queue_items_lease_token_uidx
    ON dagu_queue_items (lease_token)
    WHERE lease_token IS NOT NULL;

CREATE TABLE dagu_dispatch_tasks (
    id dagu_uuid_v7 PRIMARY KEY,
    queue_name text NOT NULL CHECK (queue_name <> ''),
    attempt_key text NOT NULL CHECK (attempt_key <> ''),
    worker_selector jsonb NOT NULL DEFAULT '{}'::jsonb,
    data_version integer NOT NULL DEFAULT 1,
    data jsonb NOT NULL,
    enqueued_at timestamptz NOT NULL DEFAULT now(),
    claim_token dagu_uuid_v7,
    claimed_at timestamptz,
    claim_expires_at timestamptz,
    worker_id text,
    poller_id text,
    owner_id text,
    owner_host text,
    owner_port integer,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (jsonb_typeof(worker_selector) = 'object'),
    CHECK (data_version = 1),
    CHECK (jsonb_typeof(data) = 'object'),
    CHECK (
        (claim_token IS NULL AND claimed_at IS NULL AND claim_expires_at IS NULL)
        OR (claim_token IS NOT NULL AND claimed_at IS NOT NULL AND claim_expires_at IS NOT NULL)
    )
);

CREATE INDEX dagu_dispatch_tasks_ready_idx
    ON dagu_dispatch_tasks (enqueued_at ASC, id ASC);

CREATE INDEX dagu_dispatch_tasks_queue_idx
    ON dagu_dispatch_tasks (queue_name, enqueued_at ASC);

CREATE INDEX dagu_dispatch_tasks_attempt_idx
    ON dagu_dispatch_tasks (attempt_key);

CREATE UNIQUE INDEX dagu_dispatch_tasks_claim_token_uidx
    ON dagu_dispatch_tasks (claim_token)
    WHERE claim_token IS NOT NULL;

CREATE TABLE dagu_worker_heartbeats (
    worker_id text PRIMARY KEY CHECK (worker_id <> ''),
    labels jsonb NOT NULL DEFAULT '{}'::jsonb,
    stats jsonb,
    last_heartbeat_at timestamptz NOT NULL,
    data_version integer NOT NULL DEFAULT 1,
    data jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (jsonb_typeof(labels) = 'object'),
    CHECK (stats IS NULL OR jsonb_typeof(stats) = 'object'),
    CHECK (data_version = 1),
    CHECK (jsonb_typeof(data) = 'object')
);

CREATE INDEX dagu_worker_heartbeats_last_seen_idx
    ON dagu_worker_heartbeats (last_heartbeat_at);

CREATE TABLE dagu_dag_run_leases (
    attempt_key text PRIMARY KEY CHECK (attempt_key <> ''),
    dag_name dagu_dag_name NOT NULL,
    dag_run_id dagu_dag_run_id NOT NULL,
    root_dag_name dagu_dag_name NOT NULL,
    root_dag_run_id dagu_dag_run_id NOT NULL,
    attempt_id dagu_attempt_id NOT NULL,
    queue_name text NOT NULL CHECK (queue_name <> ''),
    worker_id text NOT NULL CHECK (worker_id <> ''),
    owner_id text,
    owner_host text,
    owner_port integer,
    claimed_at timestamptz NOT NULL,
    last_heartbeat_at timestamptz NOT NULL,
    data_version integer NOT NULL DEFAULT 1,
    data jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (data_version = 1),
    CHECK (jsonb_typeof(data) = 'object')
);

CREATE INDEX dagu_dag_run_leases_queue_idx
    ON dagu_dag_run_leases (queue_name, last_heartbeat_at DESC);

CREATE INDEX dagu_dag_run_leases_last_seen_idx
    ON dagu_dag_run_leases (last_heartbeat_at);

CREATE TABLE dagu_active_distributed_runs (
    attempt_key text PRIMARY KEY CHECK (attempt_key <> ''),
    dag_name dagu_dag_name NOT NULL,
    dag_run_id dagu_dag_run_id NOT NULL,
    root_dag_name dagu_dag_name NOT NULL,
    root_dag_run_id dagu_dag_run_id NOT NULL,
    attempt_id dagu_attempt_id NOT NULL,
    worker_id text NOT NULL CHECK (worker_id <> ''),
    status dagu_status_code NOT NULL,
    observed_at timestamptz NOT NULL,
    data_version integer NOT NULL DEFAULT 1,
    data jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (data_version = 1),
    CHECK (jsonb_typeof(data) = 'object')
);

CREATE INDEX dagu_active_distributed_runs_observed_idx
    ON dagu_active_distributed_runs (observed_at DESC);

CREATE TABLE dagu_service_instances (
    service_name dagu_service_name NOT NULL,
    instance_id text NOT NULL CHECK (instance_id <> ''),
    host text NOT NULL CHECK (host <> ''),
    port integer NOT NULL CHECK (port >= 0 AND port <= 65535),
    status dagu_service_status NOT NULL,
    started_at timestamptz NOT NULL,
    last_heartbeat_at timestamptz NOT NULL,
    data_version integer NOT NULL DEFAULT 1,
    data jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (service_name, instance_id),
    CHECK (data_version = 1),
    CHECK (jsonb_typeof(data) = 'object')
);

CREATE INDEX dagu_service_instances_lookup_idx
    ON dagu_service_instances (service_name, last_heartbeat_at DESC);

CREATE TABLE dagu_audit_entries (
    id dagu_uuid_v7 PRIMARY KEY,
    occurred_at timestamptz NOT NULL,
    category dagu_audit_category NOT NULL,
    action text NOT NULL CHECK (action <> ''),
    user_id text NOT NULL,
    username text NOT NULL,
    ip_address text,
    data_version integer NOT NULL DEFAULT 1,
    data jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (data_version = 1),
    CHECK (jsonb_typeof(data) = 'object')
);

CREATE INDEX dagu_audit_entries_query_idx
    ON dagu_audit_entries (occurred_at DESC, id DESC);

CREATE INDEX dagu_audit_entries_category_idx
    ON dagu_audit_entries (category, occurred_at DESC);

CREATE INDEX dagu_audit_entries_user_idx
    ON dagu_audit_entries (user_id, occurred_at DESC);

CREATE TABLE dagu_users (
    id dagu_uuid_v7 PRIMARY KEY,
    username text NOT NULL UNIQUE CHECK (username <> ''),
    password_hash text NOT NULL,
    role dagu_auth_role NOT NULL,
    auth_provider text,
    oidc_issuer text,
    oidc_subject text,
    is_disabled boolean NOT NULL DEFAULT false,
    workspace_access jsonb,
    data_version integer NOT NULL DEFAULT 1,
    data jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (workspace_access IS NULL OR jsonb_typeof(workspace_access) = 'object'),
    CHECK (data_version = 1),
    CHECK (jsonb_typeof(data) = 'object'),
    CHECK ((oidc_issuer IS NULL AND oidc_subject IS NULL) OR (oidc_issuer IS NOT NULL AND oidc_subject IS NOT NULL))
);

CREATE UNIQUE INDEX dagu_users_oidc_identity_uidx
    ON dagu_users (oidc_issuer, oidc_subject)
    WHERE oidc_issuer IS NOT NULL AND oidc_subject IS NOT NULL;

CREATE TABLE dagu_api_keys (
    id dagu_uuid_v7 PRIMARY KEY,
    name text NOT NULL UNIQUE CHECK (name <> ''),
    role dagu_auth_role NOT NULL,
    key_hash text NOT NULL CHECK (key_hash <> ''),
    key_prefix text NOT NULL,
    created_by text NOT NULL,
    workspace_access jsonb,
    last_used_at timestamptz,
    data_version integer NOT NULL DEFAULT 1,
    data jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (workspace_access IS NULL OR jsonb_typeof(workspace_access) = 'object'),
    CHECK (data_version = 1),
    CHECK (jsonb_typeof(data) = 'object')
);

CREATE TABLE dagu_webhooks (
    id dagu_uuid_v7 PRIMARY KEY,
    dag_name dagu_dag_name NOT NULL UNIQUE,
    token_hash text NOT NULL CHECK (token_hash <> ''),
    token_prefix text NOT NULL,
    enabled boolean NOT NULL,
    auth_mode text,
    hmac_enforcement_mode text,
    created_by text NOT NULL,
    last_used_at timestamptz,
    data_version integer NOT NULL DEFAULT 1,
    data jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (auth_mode IS NULL OR auth_mode IN ('token_only', 'token_and_hmac', 'hmac_only')),
    CHECK (hmac_enforcement_mode IS NULL OR hmac_enforcement_mode IN ('strict', 'observe')),
    CHECK (data_version = 1),
    CHECK (jsonb_typeof(data) = 'object')
);

CREATE TABLE dagu_workspaces (
    id dagu_uuid_v7 PRIMARY KEY,
    name dagu_workspace_name NOT NULL UNIQUE,
    data_version integer NOT NULL DEFAULT 1,
    data jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (data_version = 1),
    CHECK (jsonb_typeof(data) = 'object')
);

CREATE TABLE dagu_agent_sessions (
    id dagu_uuid_v7 PRIMARY KEY,
    user_id text NOT NULL CHECK (user_id <> ''),
    dag_name dagu_dag_name,
    title text,
    model text,
    parent_session_id dagu_uuid_v7 REFERENCES dagu_agent_sessions(id) ON DELETE CASCADE,
    delegate_task text,
    data_version integer NOT NULL DEFAULT 1,
    data jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (data_version = 1),
    CHECK (jsonb_typeof(data) = 'object')
);

CREATE INDEX dagu_agent_sessions_user_updated_idx
    ON dagu_agent_sessions (user_id, updated_at DESC, id DESC);

CREATE INDEX dagu_agent_sessions_parent_idx
    ON dagu_agent_sessions (parent_session_id, updated_at DESC)
    WHERE parent_session_id IS NOT NULL;

CREATE TABLE dagu_agent_session_messages (
    id dagu_uuid_v7 PRIMARY KEY,
    session_id dagu_uuid_v7 NOT NULL REFERENCES dagu_agent_sessions(id) ON DELETE CASCADE,
    message_type text NOT NULL CHECK (message_type IN ('user', 'assistant', 'error', 'ui_action', 'user_prompt')),
    sequence_id bigint NOT NULL,
    created_at timestamptz NOT NULL,
    data_version integer NOT NULL DEFAULT 1,
    data jsonb NOT NULL,
    CHECK (data_version = 1),
    CHECK (jsonb_typeof(data) = 'object'),
    UNIQUE (session_id, sequence_id)
);

CREATE INDEX dagu_agent_session_messages_session_idx
    ON dagu_agent_session_messages (session_id, sequence_id ASC);

CREATE TABLE dagu_events (
    id dagu_uuid_v7 PRIMARY KEY,
    event_id text NOT NULL UNIQUE CHECK (event_id <> ''),
    schema_version integer NOT NULL CHECK (schema_version > 0),
    occurred_at timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL,
    kind dagu_event_kind NOT NULL,
    event_type text NOT NULL CHECK (event_type <> ''),
    source_service text NOT NULL CHECK (source_service <> ''),
    source_instance text,
    dag_name dagu_dag_name,
    dag_run_id dagu_dag_run_id,
    attempt_id dagu_attempt_id,
    session_id text,
    user_id text,
    model text,
    status text,
    data_version integer NOT NULL DEFAULT 1,
    data jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (data_version = 1),
    CHECK (jsonb_typeof(data) = 'object')
);

CREATE INDEX dagu_events_query_idx
    ON dagu_events (occurred_at DESC, recorded_at DESC, event_id DESC);

CREATE INDEX dagu_events_kind_type_idx
    ON dagu_events (kind, event_type, occurred_at DESC);

CREATE INDEX dagu_events_dag_run_idx
    ON dagu_events (dag_name, dag_run_id, occurred_at DESC);

-- +goose Down
DROP TABLE IF EXISTS dagu_events;
DROP TABLE IF EXISTS dagu_agent_session_messages;
DROP TABLE IF EXISTS dagu_agent_sessions;
DROP TABLE IF EXISTS dagu_workspaces;
DROP TABLE IF EXISTS dagu_webhooks;
DROP TABLE IF EXISTS dagu_api_keys;
DROP TABLE IF EXISTS dagu_users;
DROP TABLE IF EXISTS dagu_audit_entries;
DROP TABLE IF EXISTS dagu_service_instances;
DROP TABLE IF EXISTS dagu_active_distributed_runs;
DROP TABLE IF EXISTS dagu_dag_run_leases;
DROP TABLE IF EXISTS dagu_worker_heartbeats;
DROP TABLE IF EXISTS dagu_dispatch_tasks;
DROP TABLE IF EXISTS dagu_queue_items;
DROP TABLE IF EXISTS dagu_dag_run_attempts;
DROP TABLE IF EXISTS dagu_dag_runs;
DROP DOMAIN IF EXISTS dagu_event_kind;
DROP DOMAIN IF EXISTS dagu_audit_category;
DROP DOMAIN IF EXISTS dagu_auth_role;
DROP DOMAIN IF EXISTS dagu_service_status;
DROP DOMAIN IF EXISTS dagu_service_name;
DROP DOMAIN IF EXISTS dagu_queue_priority;
DROP DOMAIN IF EXISTS dagu_status_code;
DROP DOMAIN IF EXISTS dagu_workspace_name;
DROP DOMAIN IF EXISTS dagu_attempt_id;
DROP DOMAIN IF EXISTS dagu_dag_run_id;
DROP DOMAIN IF EXISTS dagu_dag_name;
DROP DOMAIN IF EXISTS dagu_uuid_v7;
