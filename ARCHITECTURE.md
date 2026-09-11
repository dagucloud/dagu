# Dagu architecture

This is a short map for contributors. Start with the package that owns the
change; you do not need to understand the whole repository before opening a
focused PR.

```text
YAML / spec  ->  IR  ->  intake  ->  runtime  ->  executor
                                                   |
                                                   v
                                             files (persis)
                                                   |
                                                   v
                                             API / UI

internal/spec -> internal/ir -> internal/intake -> internal/runtime
                                      -> internal/runtime/builtin
                                      -> internal/persis
                                      -> internal/service/frontend -> ui/
```

## Day-one packages

- `internal/spec/` decodes authored YAML and validates it.
- `internal/ir/` holds canonical DAG definitions and persisted run state.
- `internal/intake/` admits local and queued runs.
- `internal/runtime/` plans and runs workflows, including retries and lifecycle.
- `internal/persis/` defines persistence contracts and file-backed adapters.
- `internal/service/frontend/` serves the HTTP API, SSE, MCP, and embedded UI assets.
- `ui/` is the React and TypeScript frontend.

Executors live under `internal/runtime/builtin/`; their metadata, schemas, and
validators are registered through `internal/executor/registry/`.

## Ignore until needed

For a first contribution, leave the distributed and optional subsystems alone:
`internal/service/coordinator/`, `internal/service/worker/`, `llm/`, `tunnel/`,
`license/`, `cloud/`, `proto/`, and most of `internal/cmn/`.

## Import rules

`internal/persis/` must not import `internal/service/*`; service handlers stay
above persistence and domain layers. Keep `internal/cmn/*` domain-independent:
`internal/cmn/config` may import `internal/auth` and `internal/workspace`, but
other domain imports do not belong in `internal/cmn/`.
