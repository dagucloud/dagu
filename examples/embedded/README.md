# Embedded Dagu Examples

These examples show how another Go application can import Dagu as a library:

```go
import "github.com/dagucloud/dagu"
```

The embedded API is experimental. It is intended for applications that want to
run Dagu DAGs in-process or dispatch them to existing Dagu coordinators while
keeping Dagu's CLI and server behavior unchanged.

## Local Execution

Run a DAG in the current process with file-backed state:

```sh
go run ./examples/embedded/local
```

The example creates a temporary Dagu home directory, loads
`examples/embedded/local/workflow.yaml`, passes parameters, waits for completion,
and prints the final status.

## Custom Executor

Register a process-local executor and use it from DAG YAML:

```sh
go run ./examples/embedded/custom-executor
```

This is useful when the embedding application already has domain-specific Go
code and wants DAG steps to call that code without shelling out.

## Distributed Worker Execution

Run against an existing Dagu coordinator:

```sh
DAGU_COORDINATORS=127.0.0.1:50055 go run ./examples/embedded/distributed
```

The example starts an embedded worker and dispatches a DAG through the
coordinator. Set `DAGU_COORDINATORS` to a comma-separated list when using more
than one coordinator. The sample opts into plaintext coordinator transport for
local development; production embedders should provide TLS files instead.
