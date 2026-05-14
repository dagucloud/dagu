// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/test"
)

// TestSQLExecutor_SQLite_BasicQuery tests basic SQLite query execution.
func TestSQLExecutor_SQLite_BasicQuery(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	dbPathForYAML := filepath.ToSlash(dbPath)

	dag := th.DAG(t, fmt.Sprintf(`
type: graph
steps:
  - name: init-db
    action: sqlite.query
    with:
      query: |
        CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
        INSERT INTO users (name) VALUES ('Alice'), ('Bob');

      dsn: "%s"
      transaction: true
  - name: query-users
    action: sqlite.query
    with:
      query: "SELECT id, name FROM users ORDER BY id"
      dsn: "%s"
      output_format: jsonl
    output: USERS
    depends: [init-db]
`, dbPathForYAML, dbPathForYAML))

	dag.Agent().RunSuccess(t)
	dag.AssertLatestStatus(t, core.Succeeded)

	// Verify query output contains expected rows
	dag.AssertOutputs(t, map[string]any{
		"USERS": []test.Contains{
			`"id":1`,
			`"name":"Alice"`,
			`"id":2`,
			`"name":"Bob"`,
		},
	})
}

// TestSQLExecutor_SQLite_Transaction tests transaction commit behavior.
func TestSQLExecutor_SQLite_Transaction(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	dbPathForYAML := filepath.ToSlash(dbPath)

	dag := th.DAG(t, fmt.Sprintf(`
type: graph
steps:
  - name: setup
    action: sqlite.query
    with:
      query: |
        CREATE TABLE accounts (id INTEGER PRIMARY KEY, balance INTEGER NOT NULL);
        INSERT INTO accounts VALUES (1, 100), (2, 200);

      dsn: "%s"
  - name: transfer
    action: sqlite.query
    with:
      query: |
        UPDATE accounts SET balance = balance - 50 WHERE id = 1;
        UPDATE accounts SET balance = balance + 50 WHERE id = 2;
      dsn: "%s"
      transaction: true
    depends: [setup]

  - name: verify
    action: sqlite.query
    with:
      query: "SELECT id, balance FROM accounts ORDER BY id"
      dsn: "%s"
      output_format: jsonl
    output: BALANCES
    depends: [transfer]
`, dbPathForYAML, dbPathForYAML, dbPathForYAML))

	dag.Agent().RunSuccess(t)
	dag.AssertLatestStatus(t, core.Succeeded)

	// Verify balances after transfer: account 1 = 50, account 2 = 250
	dag.AssertOutputs(t, map[string]any{
		"BALANCES": []test.Contains{
			`"id":1`,
			`"balance":50`,
			`"id":2`,
			`"balance":250`,
		},
	})
}

// TestSQLExecutor_SQLite_TransactionRollback tests that failed transactions
// properly rollback changes.
func TestSQLExecutor_SQLite_TransactionRollback(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	dbPathForYAML := filepath.ToSlash(dbPath)

	dag := th.DAG(t, fmt.Sprintf(`
type: graph
steps:
  - name: setup
    action: sqlite.query
    with:
      query: |
        CREATE TABLE rollback_test (id INTEGER PRIMARY KEY, value INTEGER NOT NULL);
        INSERT INTO rollback_test VALUES (1, 100);

      dsn: "%s"
  - name: failed-transaction
    action: sqlite.query
    with:
      query: |
        UPDATE rollback_test SET value = 999 WHERE id = 1;
        SELECT * FROM nonexistent_table_for_error;
      dsn: "%s"
      transaction: true
    depends: [setup]
    continue_on:
      failure: true

  - name: verify-rollback
    action: sqlite.query
    with:
      query: "SELECT value FROM rollback_test WHERE id = 1"
      dsn: "%s"
      output_format: jsonl
    output: VALUE_AFTER_ROLLBACK
    depends: [failed-transaction]
`, dbPathForYAML, dbPathForYAML, dbPathForYAML))

	// Run the DAG - it will have an error because one step fails
	ag := dag.Agent()
	_ = ag.Run(ag.Context)
	// The DAG is partially_succeeded because one step failed (even with continue_on: failure: true)
	// The value should still be 100 because the transaction was rolled back
	dag.AssertLatestStatus(t, core.PartiallySucceeded)

	// Verify rollback: value should still be 100, NOT 999
	dag.AssertOutputs(t, map[string]any{
		"VALUE_AFTER_ROLLBACK": test.Contains(`"value":100`),
	})
}

// TestSQLExecutor_SQLite_NullValues tests NULL value handling in output.
func TestSQLExecutor_SQLite_NullValues(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)

	dag := th.DAG(t, `
steps:
  - name: test-nulls
    action: sqlite.query
    with:
      query: "SELECT NULL as null_text, NULL as null_int, NULL as null_bool, 'not_null' as regular_text, 42 as regular_int"
      dsn: ":memory:"
      output_format: jsonl
    output: NULL_VALUES
`)

	dag.Agent().RunSuccess(t)
	dag.AssertLatestStatus(t, core.Succeeded)

	// Verify NULL values are represented as null in JSON and non-null values are correct
	dag.AssertOutputs(t, map[string]any{
		"NULL_VALUES": []test.Contains{
			`"null_text":null`,
			`"null_int":null`,
			`"null_bool":null`,
			`"regular_text":"not_null"`,
			`"regular_int":42`,
		},
	})
}

// TestSQLExecutor_SQLite_OutputFormats tests different output formats.
func TestSQLExecutor_SQLite_OutputFormats(t *testing.T) {
	tests := []struct {
		name     string
		format   string
		expected []test.Contains
	}{
		{
			name:   "JSONL",
			format: "jsonl",
			expected: []test.Contains{
				`"id":1`,
				`"name":"test"`,
			},
		},
		{
			name:   "JSON",
			format: "json",
			expected: []test.Contains{
				`"id": 1`,        // JSON format is pretty-printed with spaces
				`"name": "test"`, // JSON format is pretty-printed with spaces
			},
		},
		{
			name:   "CSV",
			format: "csv",
			expected: []test.Contains{
				`id,name`, // header
				`1,test`,  // data row
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			th := test.Setup(t)

			dag := th.DAG(t, fmt.Sprintf(`
steps:
  - name: query
    action: sqlite.query
    with:
      query: |
        CREATE TABLE data (id INTEGER, name TEXT);
        INSERT INTO data VALUES (1, 'test');
        SELECT * FROM data;
      dsn: ":memory:"
      output_format: %s
      headers: true
    output: RESULT
`, tt.format))

			dag.Agent().RunSuccess(t)
			dag.AssertLatestStatus(t, core.Succeeded)

			// Verify format-specific output
			dag.AssertOutputs(t, map[string]any{
				"RESULT": tt.expected,
			})
		})
	}
}

// TestSQLExecutor_SQLite_MaxRows tests row limiting functionality.
func TestSQLExecutor_SQLite_MaxRows(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)

	dag := th.DAG(t, `
steps:
  - name: query-limited
    action: sqlite.query
    with:
      query: |
        CREATE TABLE many_rows (id INTEGER PRIMARY KEY, value TEXT);
        INSERT INTO many_rows (value) VALUES ('row_1'), ('row_2'), ('row_3'), ('row_4'), ('row_5'), ('row_6'), ('row_7'), ('row_8'), ('row_9'), ('row_10');
        SELECT * FROM many_rows ORDER BY id;
      dsn: ":memory:"
      output_format: jsonl
      max_rows: 5
    output: LIMITED_ROWS
`)

	dag.Agent().RunSuccess(t)
	dag.AssertLatestStatus(t, core.Succeeded)

	// Verify output contains rows 1-5
	dag.AssertOutputs(t, map[string]any{
		"LIMITED_ROWS": []test.Contains{
			`"id":1`,
			`"id":2`,
			`"id":3`,
			`"id":4`,
			`"id":5`,
		},
	})

	// Verify rows 6-10 are NOT in output (maxRows=5 should limit)
	outputs := dag.ReadOutputs(t)
	limitedRows := outputs["LIMITED_ROWS"]
	if strings.Contains(limitedRows, `"id":6`) {
		t.Errorf("maxRows=5 but row 6 was returned")
	}
	if strings.Contains(limitedRows, `"id":10`) {
		t.Errorf("maxRows=5 but row 10 was returned")
	}
}

// TestSQLExecutor_SQLite_NamedParams tests named parameter substitution.
func TestSQLExecutor_SQLite_NamedParams(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	dbPathForYAML := filepath.ToSlash(dbPath)

	dag := th.DAG(t, fmt.Sprintf(`
type: graph
steps:
  - name: setup
    action: sqlite.query
    with:
      query: |
        CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT, price REAL);
        INSERT INTO products (name, price) VALUES ('Apple', 1.50), ('Banana', 0.75), ('Orange', 2.00);

      dsn: "%s"
  - name: query-with-params
    action: sqlite.query
    with:
      query: "SELECT name, price FROM products WHERE price >= :min_price ORDER BY name"
      dsn: "%s"
      output_format: jsonl
      params:
        min_price: 1.00
    output: FILTERED_PRODUCTS
    depends: [setup]
`, dbPathForYAML, dbPathForYAML))

	dag.Agent().RunSuccess(t)
	dag.AssertLatestStatus(t, core.Succeeded)

	// Verify only products with price >= 1.00 are returned (Apple, Orange but NOT Banana)
	dag.AssertOutputs(t, map[string]any{
		"FILTERED_PRODUCTS": []test.Contains{
			`"name":"Apple"`,
			`"name":"Orange"`,
		},
	})

	// Verify Banana (price 0.75) is NOT in results
	outputs := dag.ReadOutputs(t)
	if strings.Contains(outputs["FILTERED_PRODUCTS"], `"name":"Banana"`) {
		t.Errorf("Banana should be filtered out (price 0.75 < min_price 1.00)")
	}
}

// TestSQLExecutor_SQLite_MultiStatement tests multi-statement scripts.
func TestSQLExecutor_SQLite_MultiStatement(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	dbPathForYAML := filepath.ToSlash(dbPath)

	dag := th.DAG(t, fmt.Sprintf(`
type: graph
steps:
  - name: multi-statement
    action: sqlite.query
    with:
      query: |
        CREATE TABLE orders (id INTEGER PRIMARY KEY, status TEXT);
        INSERT INTO orders (status) VALUES ('pending');
        UPDATE orders SET status = 'completed' WHERE status = 'pending';

      dsn: "%s"
      transaction: true
  - name: verify
    action: sqlite.query
    with:
      query: "SELECT status FROM orders"
      dsn: "%s"
      output_format: jsonl
    output: ORDER_STATUS
    depends: [multi-statement]
`, dbPathForYAML, dbPathForYAML))

	dag.Agent().RunSuccess(t)
	dag.AssertLatestStatus(t, core.Succeeded)

	// Verify status was updated from 'pending' to 'completed'
	dag.AssertOutputs(t, map[string]any{
		"ORDER_STATUS": test.Contains(`"status":"completed"`),
	})
}

// TestSQLExecutor_SQLite_InMemory tests SQLite in-memory database (single step).
func TestSQLExecutor_SQLite_InMemory(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)

	dag := th.DAG(t, `
steps:
  - name: sqlite-query
    action: sqlite.query
    with:
      query: |
        CREATE TABLE test (id INTEGER PRIMARY KEY, name TEXT);
        INSERT INTO test (name) VALUES ('Alice'), ('Bob');
        SELECT * FROM test ORDER BY id;
      dsn: ":memory:"
      output_format: jsonl
    output: SQLITE_RESULT
`)

	dag.Agent().RunSuccess(t)
	dag.AssertLatestStatus(t, core.Succeeded)

	// Verify query returns Alice and Bob
	dag.AssertOutputs(t, map[string]any{
		"SQLITE_RESULT": []test.Contains{
			`"id":1`,
			`"name":"Alice"`,
			`"id":2`,
			`"name":"Bob"`,
		},
	})
}

// TestSQLExecutor_SQLite_TransactionSingleStep tests SQLite transaction handling in a single step.
func TestSQLExecutor_SQLite_TransactionSingleStep(t *testing.T) {
	t.Parallel()
	th := test.Setup(t)

	dag := th.DAG(t, `
steps:
  - name: sqlite-transaction
    action: sqlite.query
    with:
      query: |
        CREATE TABLE counter (id INTEGER PRIMARY KEY, value INTEGER);
        INSERT INTO counter VALUES (1, 0);
        UPDATE counter SET value = value + 1 WHERE id = 1;
        UPDATE counter SET value = value + 1 WHERE id = 1;
        SELECT value FROM counter WHERE id = 1;
      dsn: ":memory:"
      transaction: true
      output_format: jsonl
    output: COUNTER_VALUE
`)

	dag.Agent().RunSuccess(t)
	dag.AssertLatestStatus(t, core.Succeeded)

	// Verify counter was incremented twice: 0 + 1 + 1 = 2
	dag.AssertOutputs(t, map[string]any{
		"COUNTER_VALUE": test.Contains(`"value":2`),
	})
}
