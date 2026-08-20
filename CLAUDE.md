# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What is Dagu?

Dagu is a self-contained, single-binary workflow orchestration engine. Workflows are defined as DAGs (Directed Acyclic Graphs) in YAML. It requires no external databases or message brokers — all control-plane data is stored locally in files. It supports local, queue-based, and distributed (coordinator/worker) execution modes, and can also be embedded in Go programs via the root `dagu` package.

## Build & Development Commands

| Command | Description |
|---------|-------------|
| `make build` | Build frontend UI + Go binary |
| `make bin` | Build Go binary only (output: `.local/bin/dagu`) |
| `make ui` | Build frontend only (cleans node_modules, installs, webpack builds) |
| `make run` | Run frontend server + scheduler (requires built UI assets) |
| `make run-server` | Run backend server only |
| `make test` | Run Go tests except conformance (`gotestsum` with race detection) |
| `make test TEST_TARGET=./internal/spec/...` | Run tests for a specific package |
| `make conformance` | Binary-level conformance tests (builds binary, sets `DAGU_BIN`) |
| `make test-coverage` | Run tests with coverage, writes HTML report |
| `make test-e2e` | Browser E2E tests (Playwright, builds UI + E2E binary) |
| `make lint` | Run `golangci-lint` (with `--fix`, also under `GOOS=windows`) |
| `make fmt` | Auto-format: `go fix` + `go fmt` + `golangci-lint --fix` |
| `make check` | CI-style check: formatting + linting without modifications |
| `make api` | Generate server code from OpenAPI spec (`api/v1/api.yaml`) |
| `make proto` | Lint proto files + generate gRPC code |
| `make llms` | Regenerate `llms.txt` from `skills/dagu` sources |

Run a single test directly: `go test -race -run TestName ./internal/runtime/...`

**Frontend dev server**: `cd ui && pnpm install && pnpm dev` (port 8081, proxies API to backend on 8080).

## Architecture Overview

### Embeddable engine API (repo root)

The module root is package `dagu` — an experimental embedded API (`engine.go`, `executor.go`): `New(ctx, Options)` returns an `Engine` with `RunFile`/`RunYAML`/`Status`/`Stop`, supporting local file-backed and distributed (coordinator) modes. `RegisterExecutor` adds custom executors. It wraps `internal/engine`.

### Go Backend (`internal/`)

- **`ir/`** — Canonical serialized workflow representation shared by storage and execution. It owns normalized DAG definitions, persisted run snapshots, lifecycle enums, compatibility decoding, cloning, defaults, and intrinsic queries, but not mutable runtime state or environment loading. Three DAG execution types: `graph` (default), `chain`, `agent` (LLM-driven step ordering).
- **`spec/`** — YAML decoding, building, normalization, and build-time validation. Authored YAML structs remain private to this package; loaders return `*ir.DAG`. It also normalizes the step-level `action:` shorthand (~58 built-in action names like `file.*`, `state.*`, `git.worktree.*`, `human.task`) into executor configs (`step_v2.go`).
- **Domain contracts** — Feature-specific APIs live with their owning concepts. Domain packages include `audit`, `eventstore`, `docs`, `dagsettings`, `dagrun`, `queue`, `proc`, `dispatch`, `serviceregistry`, `build`, and `workspace`; DAG definition, DAG-run, and process persistence are centralized in `persis`. Persisted run status and condition results live in `ir`; each condition result embeds a value snapshot of its definition.
- **`intake/`** — Orchestrates local and queued DAG-run admission before execution reaches the runtime layer.
- **`executor/registry/`** — Executor capabilities, step validators, and configuration-schema registries used by spec and runtime.
- **`runtime/`** — Execution engine. `plan.go` builds the step graph (cycle validation), `runner.go` runs a plan (concurrency, lifecycle handlers, metrics), `node.go` is the per-step state machine (retry, repeat, output capture), `manager.go` starts/stops/inspects DAG runs. `runtime/agent/` is the DAG-run process agent (unix-socket control, signal propagation, status persistence) — not an LLM agent. `runtime/agentloop/` implements Agent DAG decisions.
- **`runctx/`, `cmn/runenv/`, and `runtimeenv/`** — `runctx` owns per-run execution context and shared runtime dependencies. `cmn/runenv` owns the import-free environment key constants. `runtimeenv` resolves dotenv files without mutating DAG definitions, while `runtimeenv/transport` prepares source-aware snapshots for subprocess launchers.
- **`runtime/builtin/`** — 28 executor packages registering ~37 executor type names: `command`/`shell`, `docker`, `container`, `kubernetes`, `ssh`/`sftp`, `http`, `jq`, `mail`, `postgres`/`sqlite`, `redis`, `s3`, `dag`/`subworkflow`/`parallel`/`foreach`, `router`, `chat` (LLM), `agent`, `action` (reusable Dagu Actions from `owner/repo@version`), `harness` (external coding-agent CLIs: aider, amp, claude, cline, codex, copilot, cursor, deepseek, droid, gemini, goose, kiro, opencode, pi, qwen), plus `archive`, `artifact`, `data`, `file`, `git`, `state`, `template`, `wait`, etc.
- **`runtime/executor/`** — Executor factories and runtime `Executor` interface. Factories are registered globally and instantiated by type name.
- **`persis/`** — Persistence contracts and shared repository behavior. DAG definitions use `DAGDefinitionStore` behind `DAGRepository`; DAG runs and their attempts use `DAGRunStore` behind `DAGRunRepository` as one consistency boundary; process liveness uses `ProcStore` behind `ProcRepository`. The production process file adapter lives under `persis/file/proc`. Collection-backed data uses the generic `Backend` → `Collection` → `Record` abstraction; `persis/store` contains its adapters.
- **`service/frontend/`** — HTTP server (chi router). REST API v1 handlers in `api/v1/` (~150 paths), SSE, terminal, static assets; also mounts the MCP server at `/mcp`.
- **`service/scheduler/`** — Cron scheduling with timezones, catchup, zombie detection, queue processing, file watching.
- **`service/coordinator/`** — gRPC server for distributed execution (`proto/coordinator/v1`: dispatch, heartbeats, log/artifact streaming, workspace bundles, shared state). Its `subflow/` package owns local and distributed child-workflow routing.
- **`service/worker/`** — Polls coordinator for tasks, executes DAGs locally, reports status.
- **`service/mcp/`** — Model Context Protocol server (read/change/execute tools, run-inspector MCP App).
- **`auth/`** — RBAC roles (admin, manager, developer, operator, viewer), users, API keys, webhook auth. Basic, OIDC, and built-in JWT auth.
- **`llm/`** — Provider-agnostic LLM abstraction (anthropic, openai, openrouter, gemini, zai, local) used by `chat` executor and agent DAGs.
- **`engine/`** — Internal implementation behind the root `dagu` package.
- **`cmd/`** — Cobra CLI implementations; entry point `cmd/main.go` at repo root registers 27 commands: `start`, `exec`, `enqueue`, `stop`, `restart`, `retry`, `dry`, `validate`, `status`, `server`, `scheduler`, `coordinator`, `worker`, `start-all` (server+scheduler+coordinator in one process), `sync`, `context`, `profile`, `license`, `human-task`, etc.
- Domain feature packages, one concern each: `gitsync` (git-backed DAG sync), `humantask`, `incident`, `notification`, `dagsettings`, `profile`, `secret` (managed secrets and provider backends), `telemetry` (metrics and tracing), `workspace`, `remotenode`, `view`, `tunnel` (Tailscale), `license`, `upgrade`, and `dispatch` (local-vs-coordinator policy).
- **`cmn/`** — Low-level shared utilities for configuration, logging, files, backoff, values, and similar cross-domain primitives. Domain imports are prohibited except for the documented configuration exception.

### Frontend (`ui/`)

React 19 + TypeScript with Webpack 5. Tailwind CSS 4, Radix UI/shadcn components, Monaco editor for YAML, xterm.js terminal, SWR + `openapi-fetch` for typed API calls. Feature modules under `ui/src/features/` (dags, dag-runs, cockpit, dashboard, incidents, queues, workers, views...). Unit tests: Vitest (`pnpm test`). Browser E2E: Playwright (`ui/e2e/`).

### Tests & specs

- `specs/` — numbered behavior specs (001–032); `conformance/` contains matching black-box tests that shell out to the real binary (requires `DAGU_BIN`, handled by `make conformance`).
- `internal/intg/` — Go integration tests (distributed, embed, queue).

### Related but separate

- **`cloud/`** — separate Go module (`github.com/dagu-org/cloud`): the SaaS backend (Postgres/sqlc, Stripe, licensing). Has its own `cloud/CLAUDE.md`; nothing in `internal/` depends on it.
- **`skills/dagu/`** — source of truth for the bundled Dagu authoring skill; `make llms` flattens it into `llms.txt`.

### Key Data Flow

```
CLI/API/UI → cmd handler → DAG load & validate (spec) → intake
  → runtime agent → Plan (runtime/plan.go) → Runner → Node → Executor (runtime/builtin/*)
  → persis/file storage → SSE → Web UI
```

Distributed mode: Scheduler → Queue → dispatch policy → Coordinator (gRPC) → Worker → report back.

## Code Generation

- **REST API**: `api/v1/api.yaml` → Go server code via `oapi-codegen`. Run `make api`.
- **gRPC**: `proto/coordinator/v1/` + `proto/index/v1/` → Go code. Run `make proto`.
- **Frontend API types**: `cd ui && pnpm gen:api` generates TypeScript types from the OpenAPI spec.

## Key Conventions

- DAG definition, DAG-run, and process persistence contracts live in `persis`: `persis.DAGDefinitionStore` is the backend seam behind `persis.DAGRepository`, `persis.DAGRunStore` is the backend seam behind `persis.DAGRunRepository`, and `persis.ProcStore` is the backend seam behind `persis.ProcRepository`. Collection-backed records use `persis.Backend`; other feature stores remain behind their domain-owned interfaces. Production implementations live in `persis/file` and `persis/store`.
- Do not recreate a `core` umbrella below `internal/`; domain contracts belong to their owning packages, while authored DAG parsing belongs to `internal/spec`.
- Packages under `persis/` must not directly import packages under `service/`, including from tests. The following check must produce no output and exit successfully:

  ```sh
  go list -f '{{range .Imports}}{{$.ImportPath}}{{"\t"}}{{.}}{{"\n"}}{{end}}{{range .TestImports}}{{$.ImportPath}}{{"\t"}}{{.}}{{"\n"}}{{end}}{{range .XTestImports}}{{$.ImportPath}}{{"\t"}}{{.}}{{"\n"}}{{end}}' ./internal/persis/... | awk '$2 ~ /\/internal\/service\// { key=$1 "\t" $2; if (!seen[key]++) print key; bad=1 } END { exit bad }'
  ```

- Packages under `cmn/` must not directly import other internal domain packages. `cmn/config` is the sole deferred exception and may import only `auth` and `workspace`. The following check covers production, test, and external-test imports and must produce no output and exit successfully:

  ```sh
  go list -f '{{range .Imports}}{{$.ImportPath}}{{"\t"}}{{.}}{{"\n"}}{{end}}{{range .TestImports}}{{$.ImportPath}}{{"\t"}}{{.}}{{"\n"}}{{end}}{{range .XTestImports}}{{$.ImportPath}}{{"\t"}}{{.}}{{"\n"}}{{end}}' ./internal/cmn/... | awk '$2 ~ /^github\.com\/dagucloud\/dagu\/v2\/internal\// && $2 !~ /^github\.com\/dagucloud\/dagu\/v2\/internal\/cmn(\/|$)/ { if ($1 == "github.com/dagucloud/dagu/v2/internal/cmn/config" && ($2 == "github.com/dagucloud/dagu/v2/internal/auth" || $2 == "github.com/dagucloud/dagu/v2/internal/workspace")) next; key=$1 "\t" $2; if (!seen[key]++) print key; bad=1 } END { exit bad }'
  ```

- Executors follow the factory pattern — registered globally, instantiated dynamically by type name.
- DAGs compose hierarchically — steps invoke other DAGs via the `dag` executor; `action:` is shorthand normalized at spec-build time.
- Configuration uses `DAGU_*` environment variables, with fallback to `~/.config/dagu/config.yaml`.
- Run `make fmt` before committing (lint also runs under `GOOS=windows` — keep Windows builds green).
- License: GPL v3 (root module). License headers managed via `make addlicense`.
- `AGENTS.md` is a symlink to this file.

## Tech Stack Summary

- **Go 1.27**, chi router, Cobra CLI, gRPC, goccy/go-yaml
- SQLite (modernc) and pgx appear only as `sql` step-executor drivers — the OSS control plane is file-based, no SQL database
- **Frontend**: React 19, TypeScript, pnpm, Webpack 5, Tailwind CSS 4, Vitest, Playwright
- **Linting**: golangci-lint v2 (errcheck, govet, staticcheck, gosec, revive, etc.)
- **Testing**: gotestsum with race detection, stretchr/testify assertions

## Skill routing

When the user's request matches an available skill, invoke it via the Skill tool. When in doubt, invoke the skill.

Key routing rules:
- Product ideas/brainstorming → invoke /office-hours
- Strategy/scope → invoke /plan-ceo-review
- Architecture → invoke /plan-eng-review
- Design system/plan review → invoke /design-consultation or /plan-design-review
- Full review pipeline → invoke /autoplan
- Bugs/errors → invoke /investigate
- QA/testing site behavior → invoke /qa or /qa-only
- Code review/diff check → invoke /review
- Visual polish → invoke /design-review
- Ship/deploy/PR → invoke /ship or /land-and-deploy
- Save progress → invoke /context-save
- Resume context → invoke /context-restore
- Author a backlog-ready spec/issue → invoke /spec
