// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec046_postgres holds black-box conformance tests for
// Spec 046: PostgreSQL Actions.
package spec046_postgres_test

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// stdoutLogPattern matches the per-step captured-stdout log path dagu start
// prints in its tree render, e.g. "└─stdout: /path/to/step.<ts>.<run>.out".
var stdoutLogPattern = regexp.MustCompile(`stdout: (.+)`)

// stderrLogPattern matches the per-step captured-stderr log path the same
// way, e.g. "├─stderr: /path/to/step.<ts>.<run>.err".
var stderrLogPattern = regexp.MustCompile(`stderr: (.+)`)

// stepStdout reads the exact bytes a step wrote to stdout, by locating its
// captured-output log file from dagu start's own tree render and reading it
// directly. The tree render re-wraps long lines with its own indentation,
// which would corrupt a strict JSONL/CSV parse, so assertions on precise
// query output read this file instead of result.Stdout().
func stepStdout(t *testing.T, daguStartOutput string) string {
	t.Helper()
	return readLoggedOutput(t, stdoutLogPattern, daguStartOutput)
}

// stepStderr is stepStdout's counterpart for a step's captured stderr,
// where postgres.query/postgres.import write per-statement/import JSON
// execution metrics -- also re-wrapped by the tree render, so this reads
// the raw log file the same way.
func stepStderr(t *testing.T, daguStartOutput string) string {
	t.Helper()
	return readLoggedOutput(t, stderrLogPattern, daguStartOutput)
}

func readLoggedOutput(t *testing.T, pattern *regexp.Regexp, daguStartOutput string) string {
	t.Helper()

	match := pattern.FindStringSubmatch(daguStartOutput)
	require.Lenf(t, match, 2, "expected a %q log path in output:\n%s", pattern, daguStartOutput)
	path := strings.TrimSpace(match[1])
	data, err := os.ReadFile(path) // #nosec G304 -- path comes from the harness's own trusted output.
	require.NoError(t, err)
	return string(data)
}

func TestPostgresLive(t *testing.T) {
	dockerClient := requireDockerDaemon(t)
	dsn := startPostgresContainer(t, dockerClient)
	env := []string{"PG_DSN=" + dsn}

	t.Run("positional params substitute into the query", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env, "start", "positional_params.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, `{"sum":5}`+"\n", stepStdout(t, result.Stdout()))
	})

	t.Run("named params substitute into the query", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env, "start", "named_params.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, `{"combined":"foobar"}`+"\n", stepStdout(t, result.Stdout()))
	})

	t.Run("an INSERT with RETURNING produces row output", func(t *testing.T) {
		t.Parallel()

		execSQL(t, dsn, "CREATE TABLE insert_returning_test(id int primary key, name text)")
		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env, "start", "insert_returning.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, `{"id":1,"name":"widget"}`+"\n", stepStdout(t, result.Stdout()))
	})

	t.Run("csv output includes headers and the configured null string", func(t *testing.T) {
		t.Parallel()

		execSQL(t, dsn, "CREATE TABLE csv_output_test(id int, name text)")
		execSQL(t, dsn, "INSERT INTO csv_output_test(id, name) VALUES (1, 'alice'), (2, NULL)")

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env, "start", "csv_output.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "id,name\n1,alice\n2,N/A\n", stepStdout(t, result.Stdout()))
	})

	t.Run("max_rows truncates the rows written, not the underlying query", func(t *testing.T) {
		t.Parallel()

		execSQL(t, dsn, "CREATE TABLE max_rows_test(id int)")
		for i := 1; i <= 5; i++ {
			execSQL(t, dsn, "INSERT INTO max_rows_test(id) VALUES ($1)", i)
		}

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env, "start", "max_rows.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "{\"id\":1}\n{\"id\":2}\n", stepStdout(t, result.Stdout()))
	})

	t.Run("advisory_lock does not prevent the query from succeeding", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env, "start", "advisory_lock.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, `{"ok":1}`+"\n", stepStdout(t, result.Stdout()))
	})

	t.Run("transaction and isolation_level are accepted and the write persists", func(t *testing.T) {
		t.Parallel()

		execSQL(t, dsn, "CREATE TABLE transaction_smoke_test(id int)")
		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env, "start", "transaction_smoke.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, 1, queryInt(t, dsn, "SELECT count(*) FROM transaction_smoke_test WHERE id = 1"))
	})

	t.Run("a query against a nonexistent table is a runtime error", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env, "start", "query_error.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("this_table_does_not_exist")
	})

	t.Run("postgres.import loads every row from the file", func(t *testing.T) {
		t.Parallel()

		execSQL(t, dsn, "CREATE TABLE import_basic_test(id int primary key, name text)")
		dagu := harness.NewRunner(t)
		dagu.WriteFile("import_basic.csv", "id,name\n1,alpha\n2,beta\n3,gamma\n")

		result := dagu.RunWithEnv(env, "start", "import_basic.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, 3, queryInt(t, dsn, "SELECT count(*) FROM import_basic_test"))

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

	t.Run("postgres.import with dry_run counts rows without writing them", func(t *testing.T) {
		t.Parallel()

		execSQL(t, dsn, "CREATE TABLE import_dry_run_test(id int primary key, name text)")
		dagu := harness.NewRunner(t)
		dagu.WriteFile("import_dry_run.csv", "id,name\n1,alpha\n2,beta\n")

		result := dagu.RunWithEnv(env, "start", "import_dry_run.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, 0, queryInt(t, dsn, "SELECT count(*) FROM import_dry_run_test"))

		var metrics struct {
			RowsImported int64 `json:"rows_imported"`
		}
		require.NoError(t, json.Unmarshal([]byte(lastJSONLine(stepStderr(t, result.Stdout()))), &metrics))
		require.Equal(t, int64(2), metrics.RowsImported)
	})

	t.Run("postgres.import with on_conflict ignore keeps the existing row", func(t *testing.T) {
		t.Parallel()

		execSQL(t, dsn, "CREATE TABLE import_conflict_test(id int primary key, name text)")
		execSQL(t, dsn, "INSERT INTO import_conflict_test(id, name) VALUES (1, 'original')")

		dagu := harness.NewRunner(t)
		dagu.WriteFile("import_conflict.csv", "id,name\n1,should-be-ignored\n2,new-row\n")

		result := dagu.RunWithEnv(env, "start", "import_on_conflict_ignore.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, 2, queryInt(t, dsn, "SELECT count(*) FROM import_conflict_test"))
		require.Equal(t, 1, queryInt(t, dsn, "SELECT count(*) FROM import_conflict_test WHERE id = 1 AND name = 'original'"))
		require.Equal(t, 1, queryInt(t, dsn, "SELECT count(*) FROM import_conflict_test WHERE id = 2 AND name = 'new-row'"))
	})
}

// lastJSONLine returns the last non-empty line of s, since import metrics
// are written as one JSON object per line to stderr.
func lastJSONLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
}

// TestPostgresNoServer proves the failure modes that do not need a real
// PostgreSQL server: with.query and with.import are validated at DAG-build
// time, and a missing with.dsn is a runtime error that fails immediately,
// before any connection is attempted (see NewConnectionManager's generous
// connection retry loop in internal/runtime/builtin/sql/connection.go,
// which this avoids exercising).
func TestPostgresNoServer(t *testing.T) {
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

	t.Run("missing with.dsn fails before any connection is attempted", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "missing_dsn.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("dsn is required")
	})
}
