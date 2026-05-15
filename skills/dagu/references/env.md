# Environment Variables

## Execution Variables

Set automatically during DAG execution. Defined in `internal/core/exec/env.go`.

### Always Available (set for every step)

| Variable | Description |
| --------- | ----------- |
| `DAG_NAME` | Name of the executing DAG |
| `DAG_RUN_ID` | Unique run identifier |
| `DAG_RUN_LOG_FILE` | Path to the main log file for the DAG run |
| `DAG_RUN_STEP_NAME` | Name of the currently executing step |
| `DAG_RUN_STEP_STDOUT_FILE` | Path to the step's stdout log file |
| `DAG_RUN_STEP_STDERR_FILE` | Path to the step's stderr log file |

### Conditionally Set

| Variable | Condition | Description |
| -------- | --------- | ----------- |
| `DAG_RUN_WORK_DIR` | Only if a per-run working directory is configured | Path to the per-DAG-run working directory |
| `DAG_DOCS_DIR` | Only if `paths.docs_dir` is configured | Per-DAG docs directory (`{docs_dir}/{dag_name}`) |
| `DAGU_PARAMS_JSON` | Only if the DAG has parameters | Resolved parameters encoded as JSON |

### Handler-Only Variables

These are only available inside lifecycle handler steps, not during normal step execution.

| Variable            | Handler Scope                                        | Description                                                                |
| ------------------- | ---------------------------------------------------- | -------------------------------------------------------------------------- |
| `DAG_RUN_STATUS`    | `onSuccess`, `onFailure`, `onAbort`, `onExit`, `onWait` | Current DAG run status (e.g., `success`, `failed`)                         |
| `DAG_WAITING_STEPS` | `onWait` only                                        | Comma-separated list of step names that are waiting for approval           |

## Param and Env Resolution

- `params:` values are exposed as strings. Pass structured data as JSON strings if a downstream step needs objects or arrays.
- `env:` values can reference `params:` values because parameter resolution happens first.
- Use list-of-maps for `env:` when one env var depends on another. Go maps do not preserve evaluation order.

```yaml
params:
  base: /tmp
env:
  - ROOT: "${base}"
  - OUTPUT_DIR: "${ROOT}/out"
```

## Configuration Variables

All configuration environment variables use the `DAGU_` prefix. They map to config keys via viper bindings in `internal/cmn/config/loader.go`.

### Paths

| Variable | Default | Description |
| -------- | ------- | ----------- |
| `DAGU_HOME` | XDG dirs | Base directory for all Dagu data. When set, all paths use unified structure under this directory |
| `DAGU_DAGS_DIR` | `$DAGU_HOME/dags` | DAG YAML files directory |
| `DAGU_LOG_DIR` | `$DAGU_HOME/logs` | Log files directory |
| `DAGU_DATA_DIR` | `$DAGU_HOME/data` | Data storage directory |
| `DAGU_TOOLS_DIR` | `$DAGU_DATA_DIR/tools` | Managed DAG tool cache directory |
| `DAGU_DOCS_DIR` | — | Documentation directory |

### Server

| Variable | Default | Description |
| -------- | ------- | ----------- |
| `DAGU_HOST` | `127.0.0.1` | Server bind address |
| `DAGU_PORT` | `8080` | Server port |
| `DAGU_BASE_PATH` | `""` (empty) | URL base path for reverse proxy setups |
| `DAGU_TZ` | system | Timezone for schedules |

### Core

| Variable | Default | Description |
| -------- | ------- | ----------- |
| `DAGU_DEFAULT_SHELL` | — | Default shell for commands |
| `DAGU_SKIP_EXAMPLES` | `false` | Skip creating example DAGs |
| `DAGU_DEFAULT_EXECUTION_MODE` | `local` | Execution mode: `local` or `distributed` |

### Features

| Variable                | Default | Description                 |
| ----------------------- | ------- | --------------------------- |
| `DAGU_TERMINAL_ENABLED` | `false` | Enable web terminal feature |
| `DAGU_QUEUE_ENABLED`    | `true`  | Enable queue system         |

### Authentication

| Variable | Default | Description |
| -------- | ------- | ----------- |
| `DAGU_AUTH_MODE` | `builtin` | Auth mode: `none`, `basic`, `builtin` |
| `DAGU_AUTH_BASIC_USERNAME` | — | Basic auth username (requires `auth.mode=basic`) |
| `DAGU_AUTH_BASIC_PASSWORD` | — | Basic auth password (requires `auth.mode=basic`) |

OIDC settings are available under the `DAGU_AUTH_OIDC_*` prefix (client ID, secret, issuer, scopes, role mappings, etc.).

### TLS

| Variable | Description |
| -------- | ----------- |
| `DAGU_CERT_FILE` | TLS certificate file path |
| `DAGU_KEY_FILE` | TLS key file path |

### Distributed Mode

Coordinator settings use the `DAGU_COORDINATOR_*` prefix (host, port, advertise address).

Worker settings use the `DAGU_WORKER_*` prefix (worker ID, max active runs, labels, coordinator addresses, PostgreSQL pool settings).

### Other Configuration Prefixes

- **Git Sync**: `DAGU_GITSYNC_*` — repository sync settings (repo URL, branch, auth, auto-sync interval)
- **Tunnel**: `DAGU_TUNNEL_*` — Tailscale tunnel settings
- **Peer TLS**: `DAGU_PEER_*` — gRPC peer TLS settings

## Path Resolution

When `DAGU_HOME` is set, all paths use a **unified structure** under that directory:

```sh
$DAGU_HOME/
├── dags/          # DAG definitions
├── data/          # Application data
├── logs/          # Logs
│   └── admin/     # Admin logs
├── suspend/       # Suspend flags
└── base.yaml      # Base configuration
```

When `DAGU_HOME` is not set, XDG-compliant paths are used (`$XDG_CONFIG_HOME/dagu/`, `$XDG_DATA_HOME/dagu/`).

Individual path variables (e.g., `DAGU_DAGS_DIR`) override the defaults regardless of which resolution mode is active.
