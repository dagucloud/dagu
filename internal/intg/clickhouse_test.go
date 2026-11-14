// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
)

// TestClickHouseExecutor_Live runs clickhouse.query steps against a server.
// It is skipped unless CLICKHOUSE_TEST_URL is set; CLICKHOUSE_TEST_USER and
// CLICKHOUSE_TEST_PASSWORD are optional. The user must be able to create and
// drop a database.
func TestClickHouseExecutor_Live(t *testing.T) {
	url := os.Getenv("CLICKHOUSE_TEST_URL")
	if url == "" {
		t.Skip("CLICKHOUSE_TEST_URL not set, skipping live ClickHouse test")
	}
	t.Parallel()

	user := os.Getenv("CLICKHOUSE_TEST_USER")
	if user == "" {
		user = "default"
	}
	password := os.Getenv("CLICKHOUSE_TEST_PASSWORD")
	db := fmt.Sprintf("dagu_intg_%d", time.Now().UnixNano())

	th := test.Setup(t)
	dag := th.DAG(t, fmt.Sprintf(`
type: graph
steps:
  - name: load
    action: clickhouse.query
    with:
      url: "%[1]s"
      user: "%[2]s"
      password: "%[3]s"
      query: |
        CREATE DATABASE %[4]s;
        CREATE TABLE %[4]s.users (id UInt32, name String) ENGINE = MergeTree ORDER BY id;
        INSERT INTO %[4]s.users VALUES (1, 'Alice'), (2, 'Bob; Jr.'), (3, 'Carol');
  - name: query
    depends: [load]
    action: clickhouse.query
    with:
      url: "%[1]s"
      user: "%[2]s"
      password: "%[3]s"
      database: %[4]s
      query: SELECT id, name FROM users WHERE id >= {min:UInt32} ORDER BY id
      params:
        min: 2
    output: USERS
  - name: report
    depends: [query]
    action: clickhouse.query
    with:
      url: "%[1]s"
      user: "%[2]s"
      password: "%[3]s"
      database: %[4]s
      query: SELECT count() AS n FROM users
      output_format: csv
    output: COUNT
  - name: cleanup
    depends: [report]
    action: clickhouse.query
    with:
      url: "%[1]s"
      user: "%[2]s"
      password: "%[3]s"
      query: DROP DATABASE %[4]s SYNC
`, url, user, password, db))

	dag.Agent().RunSuccess(t)
	dag.AssertLatestStatus(t, ir.Succeeded)
	dag.AssertOutputs(t, map[string]any{
		"USERS": []test.Contains{
			`"id":2`,
			`"name":"Bob; Jr."`,
			`"id":3`,
			`"name":"Carol"`,
		},
		"COUNT": "3",
	})
}

// TestClickHouseExecutor_Live_ServerError checks that a server error fails
// the step with the server's message.
func TestClickHouseExecutor_Live_ServerError(t *testing.T) {
	url := os.Getenv("CLICKHOUSE_TEST_URL")
	if url == "" {
		t.Skip("CLICKHOUSE_TEST_URL not set, skipping live ClickHouse test")
	}
	t.Parallel()

	user := os.Getenv("CLICKHOUSE_TEST_USER")
	if user == "" {
		user = "default"
	}
	password := os.Getenv("CLICKHOUSE_TEST_PASSWORD")

	th := test.Setup(t)
	dag := th.DAG(t, fmt.Sprintf(`
steps:
  - name: bad
    action: clickhouse.query
    with:
      url: "%s"
      user: "%s"
      password: "%s"
      query: SELECT * FROM dagu_no_such_table
`, url, user, password))

	dag.Agent().RunCheckErr(t, "UNKNOWN_TABLE")
}
