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

## Performance

Dagu runs on small hosts and keeps state in files, so cost grows with the
number of DAGs, stored runs, running steps, open browser tabs, and workers.

Every pull request body has a `Performance` section. It states either that
the change has no performance risk, with a one-line reason, or the risk and
its cost as listed below. A bare "None" is not enough.

A change has performance risk when it:

- adds or shortens UI polling, or changes SSE topics, payloads, or refetch
  frequency
- adds a ticker, file watcher, or periodic scan
- reads run or DAG directories on a request or tick path
- adds data to persisted run status, logs, outputs, or indexes
- bumps an index version or adds a migration
- changes how often workers, the coordinator, or heartbeats send messages

For such a change, the `Performance` section states:

- how often it runs and what multiplies it: tabs, subscribers, runs,
  steps, or workers
- when it stops: hidden tab, terminal run status, or closed stream
- how its cost grows with total DAGs and stored runs
- bytes sent per update and bytes stored per run
- one-time cost on upgrade, such as an index rebuild or migration

Defaults:

- UI: subscribe to SSE and poll only as a fallback when the stream is
  unavailable. Stop polling when the tab is hidden or the run is terminal.
  Fetch a list once instead of polling per row.
- SSE: fetch once per topic regardless of subscriber count, rate-limit
  refetches, and send invalidations instead of full state when the client
  can refetch.
- Persistence: read through the DAG and run indexes and paginate. Do not
  walk run history on a request or tick path.
- Memory: bound caches and maps keyed by run or DAG. Tie each goroutine to
  a context that ends.
- Idle cost: a new periodic task must justify the CPU and disk I/O it
  spends when no DAG is running.
- Storage may be slow. NFS, EFS, shared Kubernetes volumes, and Windows
  make every file read, stat, and rename slower. Read fewer files.
- File watches see only changes made on the same host. Also re-read on a
  timer.
- On Windows, use `internal/cmn/fileutil` to read, rename, and delete
  files. It retries when another process briefly holds the file.

Assume several thousand DAGs and up to 100,000 runs per day, about one run
per second, with stored history growing by that much each day. Estimate
cost against that load. Run a benchmark only when the estimate is unclear
for a request, tick, or SSE path; most changes need no measurement.

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

## Pull request body

Write the body so a reviewer understands the change in a minute. Put the
core of the change in the body; leave plumbing, tests, and generated code
to the diff.

Every pull request body has these sections:

- `Summary`: the problem and why it matters, in two or three sentences.
- `Changes`: the list of changes, one per line, and user-visible behavior
  before and after, such as CLI output, API fields, YAML, or a UI
  screenshot.
- `Core change`: the mechanism in two to four sentences, and the few lines
  of code that decide the new behavior, about 30 lines at most, with file
  and line.
- `Testing`: what shows the change works. For a bug fix, name the test that
  failed before the fix.
- `Performance`: as described in the Performance section.

Add these only when they apply, and omit the heading otherwise:

- `Compatibility`: changes to persisted data, API, CLI, YAML, or config.
  State whether an older binary still reads the data, whether a downgrade
  is safe, and whether a coordinator and workers on different versions
  still work together.
- `Review guide`: when the diff is over about 300 lines, or the change
  alters state transitions, the flow between components, or the order of
  locks, leases, or events.
  - Which files are mechanical or generated, and which decisions need a
    reviewer's judgment.
  - A Mermaid diagram for state transitions, component flow, or ordering.
    Draw only the changed part, in about ten nodes, and mark what changed.
    Keep the prose too: `gh pr view` and email show the diagram as code.
- `Security`: changes to authentication, permissions, or exposure of
  secrets in logs or responses.
- `Out of scope`: what the change leaves out on purpose, and follow-up
  pull requests.
- `Agent`: optional. The agent harness, model, and reasoning effort that
  wrote the change, such as `Claude Code, claude-opus-5-5, xhigh`.

## Pull request CI

- Each workflow runs only for changes to the paths it covers. A pull request
  or push that changes only `rfcs/` runs no checks.
- Every push to a pull request runs the light checks its changes cover: Go
  lint, Linux unit and integration tests, the frontend build, and the chart
  and image scans.
- The heavy checks run only on a pull request with the `full-ci` label:
  Windows tests, the conformance suite, browser and desktop E2E, and the dev
  image browser test. Pushes to `main` run every check their changes cover.
- The Windows tests also run on a pull request that changes Go code built
  only for Windows, a `*_windows.go` file or one whose build constraint names
  windows, and on one with the `windows-ci` label, which runs them without
  the conformance and E2E checks. The Windows installer test runs on every
  pull request that changes the installer.
- Draft pull requests run no checks. Marking one ready for review runs them,
  and turning one back into a draft cancels the run in progress.
- Add `full-ci` to a pull request that changes what the heavy checks test:
  workflow semantics or CLI behavior, Windows behavior, or a user flow with
  browser coverage. Merge it only after every check, heavy ones included,
  passes. Another pull request can merge once the light checks pass, since
  `main` runs every check after the merge.
- Add the label once review is done: a label added earlier runs the heavy
  checks on every later push.
