// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package clickhouse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests in this file need a ClickHouse server and are skipped unless
// CLICKHOUSE_TEST_URL points to its HTTP endpoint, for example
//
//	CLICKHOUSE_TEST_URL=http://127.0.0.1:8123 \
//	CLICKHOUSE_TEST_USER=default CLICKHOUSE_TEST_PASSWORD=secret \
//	go test ./internal/runtime/builtin/clickhouse -run TestLive
//
// The user needs to create and drop tables in a scratch database and to see
// its own queries in system.processes.

type liveServer struct {
	url, user, password string
}

func requireLive(t *testing.T) liveServer {
	t.Helper()
	url := os.Getenv("CLICKHOUSE_TEST_URL")
	if url == "" {
		t.Skip("CLICKHOUSE_TEST_URL not set, skipping live ClickHouse test")
	}
	return liveServer{
		url:      url,
		user:     os.Getenv("CLICKHOUSE_TEST_USER"),
		password: os.Getenv("CLICKHOUSE_TEST_PASSWORD"),
	}
}

// config returns step options for the server merged with extra.
func (s liveServer) config(extra map[string]any) map[string]any {
	cfg := map[string]any{"url": s.url}
	if s.user != "" {
		cfg["user"] = s.user
	}
	if s.password != "" {
		cfg["password"] = s.password
	}
	maps.Copy(cfg, extra)
	return cfg
}

// run executes sql as a step and returns stdout, stderr, and the error.
func (s liveServer) run(t *testing.T, extra map[string]any, sql string) (string, string, error) {
	t.Helper()
	return runStep(context.Background(), t, s.config(extra), sql)
}

func runStep(ctx context.Context, t *testing.T, cfg map[string]any, sql string) (string, string, error) {
	t.Helper()
	exec, err := newTestExecutor(testContext(t, t.TempDir(), ir.Step{}), cfg, sql)
	require.NoError(t, err)
	var stdout, stderr bytes.Buffer
	exec.SetStdout(&stdout)
	exec.SetStderr(&stderr)
	defer func() { _ = exec.(*clickhouseExecutor).Close() }()
	err = exec.Run(ctx)
	return stdout.String(), stderr.String(), err
}

func (s liveServer) mustRun(t *testing.T, extra map[string]any, sql string) string {
	t.Helper()
	out, _, err := s.run(t, extra, sql)
	require.NoError(t, err)
	return out
}

// scratchDatabase creates a database for the test and drops it afterwards.
func (s liveServer) scratchDatabase(t *testing.T) string {
	t.Helper()
	db := fmt.Sprintf("dagu_test_%d", time.Now().UnixNano())
	s.mustRun(t, nil, "CREATE DATABASE "+db)
	t.Cleanup(func() {
		_, _, _ = s.run(t, nil, "DROP DATABASE IF EXISTS "+db+" SYNC")
	})
	return db
}

// running reports how many queries matching a SQL fragment the server runs.
func (s liveServer) running(t *testing.T, fragment string) int {
	t.Helper()
	out := s.mustRun(t, map[string]any{"output_format": "csv", "params": map[string]any{"f": fragment}},
		"SELECT count() FROM system.processes WHERE position(query, {f:String}) > 0 AND position(query, 'system.processes') = 0")
	var n int
	_, err := fmt.Sscanf(strings.TrimSpace(out), "%d", &n)
	require.NoError(t, err, out)
	return n
}

func (s liveServer) waitStopped(t *testing.T, fragment string) {
	t.Helper()
	require.Eventually(t, func() bool { return s.running(t, fragment) == 0 }, 10*time.Second, 200*time.Millisecond,
		"query containing %q still runs on the server", fragment)
}

func TestLive_OutputFormats(t *testing.T) {
	s := requireLive(t)

	const q = "SELECT number AS n, toString(number) AS s FROM numbers(3)"

	out := s.mustRun(t, nil, q)
	assert.Equal(t, "{\"n\":0,\"s\":\"0\"}\n{\"n\":1,\"s\":\"1\"}\n{\"n\":2,\"s\":\"2\"}\n", out)

	out = s.mustRun(t, map[string]any{"output_format": "json"}, q)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &rows), out)
	require.Len(t, rows, 3)
	assert.Equal(t, "2", rows[2]["s"])

	out = s.mustRun(t, map[string]any{"output_format": "csv"}, q)
	assert.Equal(t, "0,\"0\"\n1,\"1\"\n2,\"2\"\n", out)

	out = s.mustRun(t, map[string]any{"output_format": "csv", "headers": true}, q)
	assert.Equal(t, "\"n\",\"s\"\n0,\"0\"\n1,\"1\"\n2,\"2\"\n", out)

	// An explicit FORMAT clause wins over output_format.
	out = s.mustRun(t, map[string]any{"output_format": "json"}, q+" FORMAT TSV")
	assert.Equal(t, "0\t0\n1\t1\n2\t2\n", out)

	// An empty result is still valid JSON.
	out = s.mustRun(t, map[string]any{"output_format": "json"}, "SELECT 1 WHERE 0")
	require.NoError(t, json.Unmarshal([]byte(out), &rows), out)
	assert.Empty(t, rows)
}

func TestLive_LargeResult(t *testing.T) {
	s := requireLive(t)

	out := s.mustRun(t, map[string]any{"output_format": "csv"}, "SELECT number, toString(number) FROM numbers(2000000)")
	assert.Equal(t, 2000000, strings.Count(out, "\n"))
	assert.True(t, strings.HasSuffix(out, "1999999,\"1999999\"\n"))
}

func TestLive_DDLAndDML(t *testing.T) {
	s := requireLive(t)
	db := s.scratchDatabase(t)

	out := s.mustRun(t, map[string]any{"database": db}, `
CREATE TABLE events (id UInt32, name String, at DateTime) ENGINE = MergeTree ORDER BY id;
INSERT INTO events VALUES (1, 'a', '2024-01-02 03:04:05'), (2, 'b;c', '2024-01-03 00:00:00');
ALTER TABLE events ADD COLUMN extra UInt8 DEFAULT 7;
SELECT id, name, extra FROM events ORDER BY id`)
	assert.Equal(t, "{\"id\":1,\"name\":\"a\",\"extra\":7}\n{\"id\":2,\"name\":\"b;c\",\"extra\":7}\n", out)

	s.mustRun(t, map[string]any{"database": db}, "INSERT INTO events (id, name, at) SELECT number + 10, 'gen', now() FROM numbers(5)")
	out = s.mustRun(t, map[string]any{"database": db, "output_format": "csv"}, "SELECT count() FROM events")
	assert.Equal(t, "7\n", out)

	s.mustRun(t, map[string]any{"database": db, "settings": map[string]any{"mutations_sync": 1}},
		"ALTER TABLE events DELETE WHERE id >= 10")
	out = s.mustRun(t, map[string]any{"database": db, "output_format": "csv"}, "SELECT count() FROM events")
	assert.Equal(t, "2\n", out)

	s.mustRun(t, map[string]any{"database": db}, "TRUNCATE TABLE events; DROP TABLE events")
	_, _, err := s.run(t, map[string]any{"database": db}, "SELECT * FROM events")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "UNKNOWN_TABLE")
}

func TestLive_Script(t *testing.T) {
	s := requireLive(t)

	// Statements share a session: SET and temporary tables carry over.
	out := s.mustRun(t, nil, `
-- leading comment; with a semicolon
SET max_threads = 3;
CREATE TEMPORARY TABLE tmp (v String);
INSERT INTO tmp VALUES ('it''s; fine'), ('back\\slash'); /* block; comment */
SELECT getSetting('max_threads') AS threads;
SELECT v FROM tmp ORDER BY v; # trailing comment
`)
	assert.Equal(t, "{\"threads\":3}\n{\"v\":\"back\\\\slash\"}\n{\"v\":\"it's; fine\"}\n", out)

	// A failing statement stops the script and is identified.
	_, stderr, err := s.run(t, nil, "SELECT 1 AS first; SELECT * FROM no_such_table_xyz; SELECT 3 AS third")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "statement 2 failed")
	assert.Contains(t, err.Error(), "UNKNOWN_TABLE")
	assert.NotContains(t, stderr, `"statement":3`)
}

// USE in a script changes the database of the statements that follow, even
// when a default database is configured.
func TestLive_UseDatabase(t *testing.T) {
	s := requireLive(t)
	db := s.scratchDatabase(t)

	out := s.mustRun(t, map[string]any{"database": "default"}, fmt.Sprintf("SELECT currentDatabase() AS db; USE %s; SELECT currentDatabase() AS db", db))
	assert.Equal(t, fmt.Sprintf("{\"db\":\"default\"}\n{\"db\":\"%s\"}\n", db), out)
}

func TestLive_Params(t *testing.T) {
	s := requireLive(t)

	params := map[string]any{
		"i":     42,
		"f":     1.5,
		"s":     "it's a \\ test\twith\nnewline",
		"b":     true,
		"d":     "2024-01-02",
		"dt":    "2024-01-02 03:04:05",
		"dec":   "12.50",
		"ints":  []any{1, 2, 3},
		"words": []any{"x'y", "a\\b", "t\tn\n"},
		"m":     map[string]any{"a": 1, "b": 2},
		"nul":   nil,
		"uuid":  "61f0c404-5cb3-11e7-907b-a6006ad3dba0",
	}
	out := s.mustRun(t, map[string]any{"params": params}, `
SELECT
  {i:UInt32} AS i,
  {f:Float64} AS f,
  {s:String} AS s,
  {b:Bool} AS b,
  {d:Date} AS d,
  {dt:DateTime} AS dt,
  {dec:Decimal(10, 2)} AS dec,
  {ints:Array(UInt8)} AS ints,
  {words:Array(String)} AS words,
  {m:Map(String, UInt8)} AS m,
  {nul:Nullable(String)} AS nul,
  {uuid:UUID} AS uuid`)

	var row map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &row), out)
	assert.EqualValues(t, 42, row["i"])
	assert.EqualValues(t, 1.5, row["f"])
	assert.Equal(t, "it's a \\ test\twith\nnewline", row["s"])
	assert.Equal(t, true, row["b"])
	assert.Equal(t, "2024-01-02", row["d"])
	assert.Equal(t, "2024-01-02 03:04:05", row["dt"])
	assert.EqualValues(t, 12.5, row["dec"])
	assert.Equal(t, []any{float64(1), float64(2), float64(3)}, row["ints"])
	assert.Equal(t, []any{"x'y", "a\\b", "t\tn\n"}, row["words"])
	assert.Equal(t, map[string]any{"a": float64(1), "b": float64(2)}, row["m"])
	assert.Nil(t, row["nul"])
	assert.Equal(t, "61f0c404-5cb3-11e7-907b-a6006ad3dba0", row["uuid"])

	// A value that looks like SQL stays data.
	out = s.mustRun(t, map[string]any{"params": map[string]any{"x": "1' OR '1'='1"}}, "SELECT {x:String} AS x")
	assert.Equal(t, "{\"x\":\"1' OR '1'='1\"}\n", out)

	// An unbound placeholder is a server error.
	_, _, err := s.run(t, nil, "SELECT {missing:UInt8}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "UNKNOWN_QUERY_PARAMETER")
}

func TestLive_Errors(t *testing.T) {
	s := requireLive(t)

	t.Run("SyntaxError", func(t *testing.T) {
		_, _, err := s.run(t, nil, "SELEC 1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SYNTAX_ERROR")
		assert.Contains(t, err.Error(), "HTTP 400")
	})

	t.Run("AuthFailureDoesNotLeakPassword", func(t *testing.T) {
		if s.user == "" {
			t.Skip("CLICKHOUSE_TEST_USER not set")
		}
		out, _, err := runStep(context.Background(), t, s.config(map[string]any{"password": "wrong-pw-123"}), "SELECT 1")
		require.Error(t, err)
		assert.Empty(t, out)
		assert.NotContains(t, err.Error(), "wrong-pw-123")
		assert.Contains(t, err.Error(), "403")
	})

	// The server reports an error after it started streaming in the body of a
	// 200 response.
	t.Run("MidStreamException", func(t *testing.T) {
		out, stderr, err := s.run(t, map[string]any{"output_format": "csv"},
			"SELECT number, throwIf(number = 1000000) FROM numbers(2000000)")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "FUNCTION_THROW_IF_VALUE_IS_NON_ZERO")
		assert.NotContains(t, out, "__exception__")
		assert.NotContains(t, out, "DB::Exception")
		assert.True(t, strings.HasPrefix(out, "0,0\n1,0\n"), "data before the error is streamed")
		assert.Contains(t, stderr, `"status":"error"`)
	})

	// The failure must not be written into the data as a JSON object, which
	// would end the body normally.
	t.Run("MidStreamExceptionInJSON", func(t *testing.T) {
		out, _, err := s.run(t, nil,
			"SELECT throwIf(number = 50000, 'boom'), number FROM numbers(100000) SETTINGS max_block_size = 1000")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "boom")
		assert.NotContains(t, out, `"exception"`)
	})

	t.Run("ReadOnly", func(t *testing.T) {
		db := s.scratchDatabase(t)
		_, _, err := s.run(t, map[string]any{"database": db, "settings": map[string]any{"readonly": 1}},
			"CREATE TABLE t (a UInt8) ENGINE = Memory")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "READONLY")
		out := s.mustRun(t, map[string]any{"settings": map[string]any{"readonly": 1}, "output_format": "csv"}, "SELECT 1")
		assert.Equal(t, "1\n", out)
	})
}

func TestLive_File(t *testing.T) {
	s := requireLive(t)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "q.sql"), []byte("-- file\nSELECT {n:UInt8} AS n;\nSELECT 'two' AS two;\n"), 0o600))

	step := ir.Step{
		Name:           "file-step",
		ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: s.config(map[string]any{"file": "q.sql", "params": map[string]any{"n": 5}})},
	}
	exec, err := newClickHouseExecutor(testContext(t, dir, step), step)
	require.NoError(t, err)
	var out bytes.Buffer
	exec.SetStdout(&out)
	exec.SetStderr(&bytes.Buffer{})
	require.NoError(t, exec.Run(context.Background()))
	assert.Equal(t, "{\"n\":5}\n{\"two\":\"two\"}\n", out.String())
}

func TestLive_Timeout(t *testing.T) {
	s := requireLive(t)
	const marker = "dagu_live_timeout_marker"

	// A query that produces no output for a long time, so the server has no
	// reason to notice that the client is gone.
	start := time.Now()
	_, stderr, err := s.run(t, map[string]any{"timeout": 1},
		fmt.Sprintf("SELECT count() FROM numbers(1000) WHERE sleepEachRow(0.05) = 0 AND '%s' != '' SETTINGS max_block_size = 1", marker))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out after 1s")
	assert.Less(t, time.Since(start), 8*time.Second)
	assert.Contains(t, stderr, `"status":"error"`)

	// The server stopped the query, which would otherwise run for 50 seconds.
	s.waitStopped(t, marker)
}

func TestLive_CancelStopsInsertSelect(t *testing.T) {
	s := requireLive(t)
	db := s.scratchDatabase(t)
	s.mustRun(t, map[string]any{"database": db}, "CREATE TABLE sink (n UInt64) ENGINE = MergeTree ORDER BY n")

	// A slow INSERT ... SELECT. Closing the connection does not stop it on
	// the server; the executor has to.
	const marker = "dagu_live_cancel_marker"
	insert := fmt.Sprintf("INSERT INTO sink SELECT number FROM numbers(100000) WHERE sleepEachRow(0.01) = 0 AND '%s' != '' SETTINGS max_block_size = 1, max_insert_block_size = 1", marker)

	step := ir.Step{
		Name:           "cancel",
		Commands:       []ir.CommandEntry{{Command: "INSERT", CmdWithArgs: insert}},
		ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: s.config(map[string]any{"database": db, "timeout": 0})},
	}
	exec, err := newClickHouseExecutor(testContext(t, t.TempDir(), step), step)
	require.NoError(t, err)
	exec.SetStdout(&bytes.Buffer{})
	exec.SetStderr(&bytes.Buffer{})

	done := make(chan error, 1)
	go func() { done <- exec.Run(context.Background()) }()

	require.Eventually(t, func() bool { return s.running(t, marker) > 0 }, 10*time.Second, 100*time.Millisecond, "insert did not start")
	require.NoError(t, exec.Kill(nil))

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after Kill")
	}
	s.waitStopped(t, marker)

	// No further rows arrive after the query is gone.
	count := func() string {
		return strings.TrimSpace(s.mustRun(t, map[string]any{"database": db, "output_format": "csv"}, "SELECT count() FROM sink"))
	}
	before := count()
	time.Sleep(time.Second)
	assert.Equal(t, before, count())
}
