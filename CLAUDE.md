# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What is Dagu?

Dagu is a self-contained, single-binary workflow orchestration engine. Workflows are defined as DAGs (Directed Acyclic Graphs) in YAML. It requires no external databases or message brokers — all data is stored locally in files. It supports local, queue-based, and distributed (coordinator/worker) execution modes.

## Build & Development Commands

| Command | Description |
|---------|-------------|
| `make build` | Build frontend UI + Go binary |
| `make bin` | Build Go binary only (output: `.local/bin/dagu`) |
| `make ui` | Build frontend only (cleans node_modules, installs, webpack builds) |
| `make run` | Run frontend server + scheduler (requires built UI assets) |
| `make run-server` | Run backend server only |
| `make test` | Run all tests (`gotestsum` with race detection) |
| `make test TEST_TARGET=./internal/core/...` | Run tests for a specific package |
| `make test-coverage` | Run tests with coverage, opens HTML report |
| `make lint` | Run `golangci-lint` |
| `make fmt` | Auto-format: `go fix` + `go fmt` + `golangci-lint --fix` |
| `make check` | CI-style check: formatting + linting without modifications |
| `make api` | Generate server code from OpenAPI spec (`api/v1/api.yaml`) |
| `make api-validate` | Validate OpenAPI spec |
| `make protoc` | Generate gRPC code from proto files |

**Frontend dev server**: `cd ui && pnpm install && pnpm dev` (runs on port 8081, backend on 8080).

## Architecture Overview

### Go Backend (`internal/`)

- **`core/`** — DAG and step definitions, validation, status types. The DAG spec is rich (~100 fields) supporting schedules, lifecycle hooks, container specs, parameters, and three execution types: `graph`, `chain`, `agent`.
- **`core/exec/`** — Interfaces for DAG run tracking, queue, and process stores (`DAGRunStore`, `QueueStore`, `ProcStore`, `Dispatcher`).
- **`runtime/`** — Execution engine. `runner.go` orchestrates parallel execution with dependency resolution. `node.go` manages individual step execution with retry logic. `manager.go` coordinates the overall lifecycle.
- **`runtime/builtin/`** — 18+ built-in executor implementations: `command`, `docker`, `http`, `ssh`, `jq`, `mail`, `sql`, `redis`, `s3`, `dag` (sub-DAG), `chat` (LLM), `router`, `agentstep`, etc.
- **`runtime/executor/`** — Executor factory pattern with global registry. Executors implement `Run(ctx) error` with stdout/stderr/kill support.
- **`persis/`** — File-based persistence layer. Store implementations include legacy file-backed packages (`filedag`, `filedagrun`, `fileproc`, etc.) and collection-backed adapters in `persis/store` such as the queue, user, session, and API-key stores.
- **`service/frontend/`** — HTTP server using Chi router. REST API v1 with 43+ endpoint handlers, SSE for real-time updates, static asset serving. API handlers are in `api/v1/`.
- **`service/scheduler/`** — Cron scheduling with timezone support, zombie detection, queue processing, distributed coordination.
- **`service/coordinator/`** — gRPC server for distributed execution and service registry.
- **`service/worker/`** — Polls coordinator for work, executes DAGs locally, reports status.
- **`auth/`** — Authentication with RBAC roles (admin, manager, operator, viewer, developer). Supports Basic, OIDC, and built-in JWT auth.
- **`cmn/`** — Shared utilities: config loading, expression evaluation, file operations, structured logging, secret management, OpenTelemetry, backoff strategies.
- **`agent/`** — LLM-powered agent for workflow generation with session/skill/memory management.
- **`cmd/`** — CLI command implementations (Cobra). ~20 commands: `start`, `stop`, `server`, `scheduler`, `coord`, `worker`, `validate`, `dry`, etc.

### Frontend (`ui/`)

React 19 + TypeScript with Webpack 5. Uses Tailwind CSS 4, Radix UI/shadcn components, Monaco editor for YAML, xterm.js for terminal, SWR for data fetching, and `openapi-fetch` for typed API calls. API types generated from OpenAPI spec via `pnpm gen:api`.

### Key Data Flow

```
CLI/API/UI → Command Handler (cmd/) → DAG Loader & Validator (core/)
  → Runtime Engine (runtime/runner.go) → Node Execution (runtime/node.go)
  → Executor (runtime/builtin/*) → File Storage (persis/)
  → SSE → Web UI
```

For distributed mode: Scheduler → Queue → Coordinator (gRPC) → Worker → Report back.

## Code Generation

- **REST API**: OpenAPI spec at `api/v1/api.yaml` → generated Go server code via `oapi-codegen`. Run `make api`.
- **gRPC**: Proto files at `proto/coordinator/v1/` → generated Go code via `protoc`. Run `make protoc`.
- **Frontend API types**: `cd ui && pnpm gen:api` generates TypeScript types from the OpenAPI spec.

## Key Conventions

- All storage is behind interfaces (in `core/exec/`) with file-based implementations (in `persis/`).
- Executors follow the factory pattern — registered globally, instantiated dynamically by type name.
- DAGs can compose hierarchically — a step can invoke another DAG via the `dag` executor.
- Configuration uses `DAGU_*` environment variables, with fallback to `~/.config/dagu/config.yaml`.
- Go commit message guidelines apply. Run `make fmt` before committing.
- License: GPL v3. License headers on source files managed via `make addlicense`.

## Tech Stack Summary

- **Go 1.26**, Chi router, Cobra CLI, gRPC, SQLite (modernc), pgx, go-redis
- **Frontend**: React 19, TypeScript, pnpm, Webpack 5, Tailwind CSS 4, Vitest
- **Linting**: golangci-lint v2 (errcheck, govet, staticcheck, gosec, revive, etc.)
- **Testing**: gotestsum with race detection, stretchr/testify assertions
