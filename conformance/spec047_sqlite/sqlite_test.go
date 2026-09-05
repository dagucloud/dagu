// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec047_sqlite holds black-box conformance tests for
// Spec 047: SQLite Actions.
package spec047_sqlite_test

import (
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestSqliteLive(t *testing.T) {
	t.Run("positional params substitute into the query", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "positional_params.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, `{"sum":5}`+"\n", stepStdout(t, result.Stdout()))
	})

	t.Run("named params substitute into the query", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "named_params.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, `{"combined":"foobar"}`+"\n", stepStdout(t, result.Stdout()))
	})

	t.Run("a named param used twice supplies the same value to each ? placeholder", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "repeated_named_param.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, `{"a":5,"b":5}`+"\n", stepStdout(t, result.Stdout()))
	})

	t.Run(":memory: is isolated per connection, so a later step can't see an earlier step's table", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "memory_isolation.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("no such table: t")
	})

	// shared_memory converts :memory: to SQLite's shared-cache mode, but
	// each step's connection is opened and fully closed within that step,
	// and a shared-cache in-memory database is destroyed once its last
	// connection closes -- so, in practice, it does not make one step's
	// :memory: writes visible to a later, separate step. Verified stable
	// across repeated runs, not a one-off timing fluke.
	t.Run("shared_memory does not make :memory: state outlive the step that wrote it", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "shared_memory_cross_step.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("no such table: t")
	})

	t.Run("a file-based DSN persists across separate dagu invocations", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		write := dagu.Run("start", "file_persist_write.yaml")
		write.ExpectExitCode(0)

		read := dagu.Run("start", "file_persist_read.yaml")
		read.ExpectExitCode(0)
		require.Equal(t, `{"id":7}`+"\n", stepStdout(t, read.Stdout()))
	})

	// A contended file_lock is deliberately not exercised here: like a bad
	// DSN (see TestSqliteNoServer's doc comment), a lock held by another
	// process falls into NewConnectionManager's generous connect-retry
	// loop (internal/runtime/builtin/sql/connection.go), so proving it
	// live would make this suite slow rather than adding real coverage.
	t.Run("file_lock is accepted and does not fail an uncontended connection", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "file_lock.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, `{"1":1}`+"\n", stepStdout(t, result.Stdout()))
	})

	t.Run("csv output includes headers and the configured null string", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "csv_output.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "id,name\n1,alice\n2,N/A\n", stepStdout(t, result.Stdout()))
	})

	t.Run("max_rows truncates the rows written, not the underlying query", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "max_rows.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "{\"id\":1}\n{\"id\":2}\n", stepStdout(t, result.Stdout()))
	})

	t.Run("advisory_lock is accepted but has no effect for sqlite", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "advisory_lock_ignored.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, `{"ok":1}`+"\n", stepStdout(t, result.Stdout()))
	})

	t.Run("sqlite.import loads every row from the file", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.WriteFile("import_basic.csv", "id,name\n1,alpha\n2,beta\n3,gamma\n")

		result := dagu.Run("start", "import_basic.yaml")
		result.ExpectExitCode(0)

		verify := dagu.Run("start", "import_basic_verify.yaml")
		verify.ExpectExitCode(0)
		require.Equal(t, `{"n":3}`+"\n", stepStdout(t, verify.Stdout()))

		var metrics struct {
			RowsRead     int64  `json:"rows_read"`
			RowsImported int64  `json:"rows_imported"`
			Status       string `json:"status"`
		}
		require.NoError(t, json.Unmarshal([]byte(lastJSONLine(stepStderr(t, result.Stdout()))), &metrics))
		require.Equal(t, int64(3), metrics.RowsRead)
		require.Equal(t, int64(3), metrics.RowsImported)
		require.Equal(t, "completed", metrics.Status)
	})

	t.Run("sqlite.import with dry_run counts rows without writing them", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.WriteFile("import_dry_run.csv", "id,name\n1,alpha\n2,beta\n")

		result := dagu.Run("start", "import_dry_run.yaml")
		result.ExpectExitCode(0)

		verify := dagu.Run("start", "import_dry_run_verify.yaml")
		verify.ExpectExitCode(0)
		require.Equal(t, `{"n":0}`+"\n", stepStdout(t, verify.Stdout()))

		var metrics struct {
			RowsImported int64 `json:"rows_imported"`
		}
		require.NoError(t, json.Unmarshal([]byte(lastJSONLine(stepStderr(t, result.Stdout()))), &metrics))
		require.Equal(t, int64(2), metrics.RowsImported)
	})

	t.Run("sqlite.import with on_conflict ignore keeps the existing row", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.WriteFile("import_conflict.csv", "id,name\n1,should-be-ignored\n2,new-row\n")

		result := dagu.Run("start", "import_on_conflict_ignore.yaml")
		result.ExpectExitCode(0)

		verify := dagu.Run("start", "import_on_conflict_ignore_verify.yaml")
		verify.ExpectExitCode(0)
		require.Equal(t, "{\"id\":1,\"name\":\"original\"}\n{\"id\":2,\"name\":\"new-row\"}\n", stepStdout(t, verify.Stdout()))
	})
}

// TestSqliteNoServer proves the failure modes that need no setup at all:
// with.query and with.import are validated at DAG-build time, a missing
// with.dsn is a runtime error, and a query against a nonexistent table is
// a runtime error -- all fast, since SQLite is embedded and needs no
// connection handshake.
func TestSqliteNoServer(t *testing.T) {
	t.Parallel()

	t.Run("missing with.query fails validate", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "missing_query.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("with.query is required")
	})

	t.Run("missing with.import fails validate", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "missing_import.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("with.import is required")
	})

	t.Run("missing with.dsn is a runtime error", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "missing_dsn.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("dsn is required")
	})

	t.Run("a query against a nonexistent table is a runtime error", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "query_error.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("no such table: this_table_does_not_exist")
	})
}
