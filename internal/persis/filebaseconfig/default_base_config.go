// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package filebaseconfig

// defaultBaseConfig is the content written to base.yaml on first run.
// Settings with sensible defaults are enabled; others are commented out.
// This serves as a discoverable reference for all inheritable DAG settings.
const defaultBaseConfig = `# Base DAG Configuration
# =====================
# Values defined here are inherited by ALL DAGs.
# Individual DAGs can override any setting.
# Environment variables (env:) are additive — DAG env vars append to these.
#
# Settings with sensible defaults are enabled below. Uncomment others as needed.

# -- Active Defaults --

# Execution model. Keep the default explicit so new base.yaml files are self-describing.
# "graph" runs steps in parallel based on dependency resolution.
# "chain" runs steps sequentially in definition order.
type: graph

# Behavior when a new run is triggered while one is already active.
# "skip" = discard the new run. "all" = queue all runs. "latest" = keep only the most recent.
overlap_policy: skip

# Log file format. "separate" writes stdout to .out and stderr to .err files.
# "merged" combines both into a single .log file.
log_output: separate

# Number of days to retain DAG run history before automatic purge.
# Use either hist_retention_days or hist_retention_runs, not both.
hist_retention_days: 30

# Number of recent DAG runs to retain before automatic purge.
# hist_retention_runs: 100

# Maximum seconds to wait for step cleanup when a DAG is stopped.
max_clean_up_time_sec: 5

# Maximum number of steps that can run concurrently within a single DAG execution.
# 0 = unlimited. Only applies when type is "graph".
max_active_steps: 10

# Maximum bytes of stdout/stderr output captured per step. Excess output is truncated.
max_output_size: 1048576  # 1MB (bytes)

# Lookback window for replaying missed scheduled runs after downtime.
# On scheduler restart, all missed cron intervals within this window are executed (max 1000).
# Duration string: e.g. "6h", "24h", "2d12h". Empty = no catchup (missed runs discarded).
catchup_window: "6h"

# Retry the entire DAG after a terminal failure.
# This absorbs transient infrastructure or dependency failures by default.
# Override or disable per DAG if the workflow is intentionally non-idempotent.
retry_policy:
  limit: 3
  interval_sec: 5

# -- Shell --
# Shell interpreter for command steps.
# Default resolution order: $DAGU_DEFAULT_SHELL -> $SHELL -> sh
# Can be a string (split on spaces) or an explicit array.
# shell: "bash -e"
# shell: ["/bin/bash", "-e"]

# -- Working Directory --
# Default working directory for all steps. Supports ~ (home), $ENV_VAR, and
# relative paths (resolved against DAG file location). Default: DAG file's parent directory.
# working_dir: /home/user/workflows

# -- Dotenv --
# Load environment variables from files before execution.
# Accepts a single path or an array. Later files override earlier ones.
# Paths are resolved relative to working_dir.
# dotenv: .env
# dotenv:
#   - .env
#   - .env.local

# -- Timeout & Delays --
# timeout_sec: Max execution time for the entire DAG in seconds. 0 = no timeout.
# delay_sec: Seconds to sleep before starting the first step. 0 = start immediately.
# restart_wait_sec: Seconds to wait before restart (dagu restart command). 0 = no wait.
# timeout_sec: 3600
# delay_sec: 0
# restart_wait_sec: 0

# -- Logging --
# Directory for log files. Default: $XDG_DATA_HOME/dagu/logs or $DAGU_HOME/logs
# log_dir: /var/log/dagu

# -- Queue --
# Assign DAGs to a named queue for concurrency control across DAGs.
# When unset, each DAG uses its own name as the process group (no cross-DAG coordination).
# queue: default

# -- Environment Variables --
# Global env vars inherited by all DAGs. DAG-level env vars are appended (not replaced).
# env:
#   - LOG_LEVEL: "info"
#   - ENVIRONMENT: "production"

# -- Step Defaults --
# Applied to every step unless the step overrides a field explicitly.
# defaults:
#
#   # Retry on failure. limit = max retries. interval_sec = wait between retries.
#   # backoff = exponential multiplier (next_interval = interval_sec * backoff^attempt).
#   # max_interval_sec = cap on computed interval. exit_code = retry only on these codes.
#   retry_policy:
#     limit: 2
#     interval_sec: 5
#     backoff: true
#     max_interval_sec: 60
#     exit_code: [1, 2]
#
#   # Continue to next step instead of failing the DAG.
#   # failure = on any failure. skipped = when preconditions not met.
#   # exit_code = continue only on these exit codes. output = continue on matching stdout patterns.
#   # mark_success = treat the step as succeeded when continue_on triggers.
#   continue_on:
#     failure: false
#     skipped: false
#     exit_code: []
#     output: []
#     mark_success: false
#
#   # Repeat a step in a loop.
#   # repeat: "while" (repeat while condition matches) or "until" (repeat until it matches).
#   # condition = command to evaluate. expected = stdout value to compare against.
#   repeat_policy:
#     repeat: "while"
#     interval_sec: 5
#     limit: 10
#     condition: "curl -s http://localhost:8080/health"
#     expected: "ok"
#
#   # Per-step timeout in seconds. Overrides DAG-level timeout_sec for this step.
#   timeout_sec: 300
#
#   # Signal sent to the step process when the DAG is stopped. e.g. SIGTERM, SIGINT, SIGKILL.
#   signal_on_stop: "SIGTERM"
#
#   # Send error_mail when this step fails.
#   mail_on_error: true

# -- Custom Actions --
# Define reusable actions that expand into builtin-backed steps at load time.
# The call-site config becomes typed input validated by input_schema.
# actions:
#   greet:
#     input_schema:
#       type: object
#       additionalProperties: false
#       required: [message]
#       properties:
#         message:
#           type: string
#     template:
#       run: echo "{$input: message}"

# -- Lifecycle Handlers --
# Hooks that run on DAG events. Each handler is a full step definition (supports run,
# action, env, timeout_sec, etc. — not limited to a simple command string).
#
# init:    Runs before DAG steps begin (after preconditions pass).
# success: Runs when DAG completes successfully.
# failure: Runs when DAG fails.
# abort:   Runs when DAG is cancelled.
# exit:    Runs on any exit (success, failure, or abort).
# wait:    Runs when DAG enters wait status (human-in-the-loop approval).
#
# handler_on:
#   init:
#     run: echo "DAG starting"
#   success:
#     run: echo "DAG succeeded"
#   failure:
#     run: echo "DAG failed"
#     env:
#       - ALERT_LEVEL: critical
#   exit:
#     run: echo "DAG finished"

# -- Email Notifications --
# smtp:
#   host: smtp.example.com
#   port: 587
#   username: ""
#   password: ""
#
# # Sent on DAG failure (when mail_on.failure is true).
# error_mail:
#   from: dagu@example.com
#   to: alerts@example.com        # Can also be an array: [a@x.com, b@x.com]
#   prefix: "[DAGU]"              # Subject line prefix.
#   attach_logs: true
#
# # Sent on DAG success (when mail_on.success is true).
# info_mail:
#   from: dagu@example.com
#   to: team@example.com
#   prefix: "[DAGU]"
#   attach_logs: false
#
# # Sent when DAG enters wait status (when mail_on.wait is true).
# wait_mail:
#   from: dagu@example.com
#   to: approvers@example.com
#   prefix: "[DAGU APPROVAL]"
#   attach_logs: false
#
# # Controls which events trigger emails.
# mail_on:
#   failure: true
#   success: false
#   wait: false

# -- SSH Defaults --
# Inherited by all ssh executor steps. Steps can override per-field.
# ssh:
#   user: deploy
#   host: server.example.com
#   port: 22
#   key: ~/.ssh/id_rsa
#   password: ""
#   strict_host_key: true         # Verify host key against known_hosts.
#   known_host_file: ~/.ssh/known_hosts
#   shell: "bash"
#   timeout: "30s"
#   bastion:                      # Jump host for tunneled connections.
#     host: bastion.example.com
#     port: 22
#     user: jump
#     key: ~/.ssh/bastion_key

# -- S3 Defaults --
# Inherited by all s3 executor steps. Steps can override per-field.
# s3:
#   region: us-east-1
#   bucket: my-bucket
#   endpoint: ""                  # Custom endpoint for S3-compatible services (e.g. MinIO).
#   access_key_id: ${AWS_ACCESS_KEY_ID}
#   secret_access_key: ${AWS_SECRET_ACCESS_KEY}
#   session_token: ""
#   profile: ""                   # AWS credentials profile name.
#   force_path_style: false       # Required for some S3-compatible services.
#   disable_ssl: false

# -- Redis Defaults --
# Inherited by all redis executor steps. Steps can override per-field.
# redis:
#   host: localhost
#   port: 6379
#   password: ""
#   username: ""                  # ACL username (Redis 6+).
#   db: 0                         # Database number (0-15).
#   tls: false
#   tls_skip_verify: false
#   mode: standalone              # "standalone", "sentinel", or "cluster".
#   max_retries: 0

# -- LLM Defaults --
# Inherited by all chat and agent executor steps.
# llm:
#   provider: openai              # "openai", "anthropic", "gemini", "openrouter", "local".
#   model: gpt-4o-mini
#   base_url: ""                  # Custom API endpoint.
#   api_key_name: ""              # Name of env var containing the API key.
#   system: ""                    # Default system prompt.
#   temperature: 0.7
#   max_tokens: 4096
#   top_p: 1.0
#   stream: true                  # Stream response output.

# -- OpenTelemetry --
# otel:
#   enabled: true
#   endpoint: http://localhost:4318   # OTel collector endpoint.
#   insecure: true                    # Skip TLS verification.
#   timeout: 10s
#   headers:
#     Authorization: "Bearer token"
#   resource:                         # Resource attributes attached to spans.
#     service.name: dagu

# -- Docker Registry Auth --
# Credentials for pulling container images. Map of registry hostname to auth.
# registry_auths:
#   "registry.example.com":
#     username: user
#     password: ${REGISTRY_PASSWORD}

# -- External Secrets --
# Resolve secrets from external providers at runtime. Each entry sets an env var.
# Providers: "env", "file", "vault".
# secrets:
#   - name: DB_PASSWORD
#     provider: env
#     key: DB_PASSWORD_ENV

# -- Run Config --
# Controls user interactions when starting DAG runs from the UI.
# run_config:
#   disable_param_edit: false     # Prevent editing parameters before run.
#   disable_run_id_edit: false    # Prevent specifying custom run IDs.
`
