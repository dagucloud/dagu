# Dagu repository guide

## Project

Dagu is a local-first workflow orchestrator. Workflows are YAML DAGs. The root
Go module builds one binary containing the CLI, server, scheduler, coordinator,
and worker. Runtime state is file-backed; no control-plane database or broker is
required.

The root package, `github.com/dagucloud/dagu/v2`, also exposes an experimental
embedded engine API.

## Repository map

- `cmd/`: binary entry point. Command implementations live in `internal/cmd/`.
- `internal/spec/`: workflow YAML decoding, normalization, and validation.
- `internal/ir/`: canonical DAG definitions and persisted run state.
- `internal/intake/`: local and queued run admission.
- `internal/runtime/`: planning, execution, retries, and run lifecycle.
- `internal/runtime/builtin/`: built-in executor implementations.
- `internal/executor/registry/`: executor metadata, schemas, and validators.
- `internal/persis/`: persistence contracts and repositories.
- `internal/persis/file/`: file-backed persistence adapters.
- `internal/persis/store/`: collection-backed domain stores.
- `internal/service/frontend/`: HTTP API, SSE, MCP, and embedded UI assets.
- `internal/service/scheduler/`: schedules, queues, retries, and run dispatch.
- `internal/service/coordinator/`: distributed execution coordinator.
- `internal/service/worker/`: distributed worker.
- `internal/engine/`: implementation behind the root embedded API.
- `api/v1/`: OpenAPI source and generated Go server types.
- `proto/`: coordinator and index protobuf APIs.
- `ui/`: React and TypeScript frontend.
- `specs/`: normative behavior specifications.
- `conformance/`: binary-level tests for those specifications.
- `internal/intg/`: Go integration tests.
- `examples/`: example workflows and embedded API programs.

## Runtime flow

```text
CLI / REST API / UI
        |
        v
spec -> ir -> intake -> runtime plan -> runner -> node -> executor
                         |                              |
                         +---------- persis -----------+
```

Distributed runs continue through scheduler dispatch, coordinator gRPC, and a
worker before reporting state and logs back.

## Commands

### Go

```sh
make bin                         # build .local/bin/dagu
make build                       # build UI and binary
make test                        # Go tests with race detection
make test TEST_TARGET=./internal/runtime/...
make conformance                 # binary-level specification tests
make fmt                         # go fix, go fmt, and lint fixes
make check                       # formatting and lint checks, including Windows
```

For a fast package-level check:

```sh
go test ./internal/spec/...
```

### Frontend

```sh
cd ui
pnpm install --frozen-lockfile
pnpm test
pnpm typecheck
pnpm build
pnpm dev
```

The frontend development server listens on port 8081 and proxies the backend on
port 8080.
```

## Generated code

Do not hand-edit generated files.

- Edit `api/v1/api.yaml`, then run `make api`.
- Edit `proto/**/*.proto`, then run `make proto`.
- After OpenAPI changes, run `cd ui && pnpm gen:api`.
- Build and copy frontend assets with `make ui`.

Review generated diffs with the source change that caused them.

## Change discipline

- Work in the current worktree. Preserve unrelated changes and untracked files.
- Make the smallest change that solves the requested behavior.
- Respect package ownership and existing dependency direction.
- Reuse nearby patterns before adding abstractions or dependencies.
- Add short comments only for non-obvious invariants or constraints.
- Keep comments current and impersonal. Godoc describes caller-visible contracts.
- Keep Go test names short; explain complex intent in a comment above the test.

## Testing

- For bug fixes, add a focused regression test first and confirm it fails.
- Prefer behavior and public-contract assertions over implementation details.
- Extend an existing test file when it already owns the behavior.
- Run the narrowest relevant test during development.
- Run all affected package tests after the change.
- Run `make fmt` and inspect its diff before committing Go changes.
- Run `make check` for broad Go validation when practical.
- Run `make conformance` when workflow semantics or CLI behavior changes.
- Run frontend tests, type checking, and build checks for UI changes.
- Run `make test-e2e` for user-visible flows that need browser coverage.
