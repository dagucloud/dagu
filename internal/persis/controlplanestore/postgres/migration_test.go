// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package postgres

import (
	"database/sql"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/persis/controlplanestore/postgres/migrations"
)

func TestMigrationUsesExistingIdentifierConstraints(t *testing.T) {
	data, err := migrations.FS.ReadFile("20260506000000_create_control_plane_store.sql")
	require.NoError(t, err)

	sqlText := string(data)
	assertSQLFragment(t, sqlText, "CREATE TABLE dagu_dag_runs")
	assertSQLFragment(t, sqlText, "CREATE TABLE dagu_dag_run_attempts")
	assertSQLFragment(t, sqlText, "run_id dagu_uuid_v7 NOT NULL REFERENCES dagu_dag_runs(id) ON DELETE CASCADE")
	assertSQLFragment(t, sqlText, "latest_attempt_id dagu_uuid_v7")
	assertSQLFragment(t, sqlText, "VALUE ~ '^[a-zA-Z0-9_.-]+$'")
	assertSQLFragment(t, sqlText, "char_length(VALUE) <= 40")
	assertSQLFragment(t, sqlText, "VALUE ~ '^[-a-zA-Z0-9_]+$'")
	assertSQLFragment(t, sqlText, "char_length(VALUE) <= 64")
	assertSQLFragment(t, sqlText, "VALUE ~ '^[A-Za-z0-9_-]+$'")
	assertSQLFragment(t, sqlText, "lower(VALUE) NOT IN ('all', 'default')")
	assertSQLFragment(t, sqlText, "VALUE::text ~* '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'")
	assertSQLFragment(t, sqlText, "dag_name dagu_dag_name")
	assertSQLFragment(t, sqlText, "dag_run_id dagu_dag_run_id")
	assertSQLFragment(t, sqlText, "attempt_id dagu_attempt_id")
}

func TestMigrationUsesCanonicalJSONDocuments(t *testing.T) {
	data, err := migrations.FS.ReadFile("20260506000000_create_control_plane_store.sql")
	require.NoError(t, err)

	sqlText := string(data)
	for _, tableName := range []string{
		"dagu_dag_runs",
		"dagu_dag_run_attempts",
		"dagu_queue_items",
		"dagu_dispatch_tasks",
		"dagu_worker_heartbeats",
		"dagu_dag_run_leases",
		"dagu_active_distributed_runs",
		"dagu_service_instances",
		"dagu_audit_entries",
		"dagu_users",
		"dagu_api_keys",
		"dagu_webhooks",
		"dagu_workspaces",
		"dagu_agent_sessions",
		"dagu_agent_session_messages",
		"dagu_events",
	} {
		body := createTableBody(t, sqlText, tableName)
		assertSQLFragment(t, body, "data_version integer NOT NULL DEFAULT 1")
		assertSQLFragment(t, body, "data jsonb NOT NULL")
		assertSQLFragment(t, body, "CHECK (data_version = 1)")
		assertSQLFragment(t, body, "CHECK (jsonb_typeof(data) = 'object')")
	}

	assert.NotContains(t, sqlText, "status_data")
	assert.NotContains(t, sqlText, "dag_data")
	assert.NotContains(t, sqlText, "outputs_data")
	assert.NotContains(t, sqlText, "messages_data")
	assert.NotContains(t, sqlText, "payload jsonb")
	assert.NotContains(t, sqlText, "task_data jsonb")
	assert.NotContains(t, sqlText, "details jsonb")
	assert.NotContains(t, sqlText, "event_data jsonb")
}

func createTableBody(t *testing.T, sqlText, tableName string) string {
	t.Helper()
	pattern := regexp.MustCompile(`(?s)CREATE TABLE ` + regexp.QuoteMeta(tableName) + ` \((.*?)\);`)
	matches := pattern.FindStringSubmatch(sqlText)
	require.Len(t, matches, 2, "CREATE TABLE %s not found", tableName)
	return matches[1]
}

func assertSQLFragment(t *testing.T, sqlText, fragment string) {
	t.Helper()
	quoted := regexp.QuoteMeta(fragment)
	pattern := strings.Join(strings.Fields(quoted), `\s+`)
	assert.Regexp(t, regexp.MustCompile(pattern), sqlText)
}

func TestWorkspaceFromLabels(t *testing.T) {
	t.Run("Missing", func(t *testing.T) {
		workspaceName, valid := workspaceFromLabels(core.NewLabels(nil))
		assert.Equal(t, sql.NullString{}, workspaceName)
		assert.True(t, valid)
	})

	t.Run("Valid", func(t *testing.T) {
		workspaceName, valid := workspaceFromLabels(core.NewLabels([]string{"workspace=ops"}))
		assert.Equal(t, sql.NullString{String: "ops", Valid: true}, workspaceName)
		assert.True(t, valid)
	})

	t.Run("Invalid", func(t *testing.T) {
		workspaceName, valid := workspaceFromLabels(core.NewLabels([]string{"workspace=default"}))
		assert.Equal(t, sql.NullString{}, workspaceName)
		assert.False(t, valid)
	})
}

func TestDAGLockKeyUsesTextSafeSeparator(t *testing.T) {
	key := dagLockKey("example", "run-1")
	assert.Equal(t, "example:run-1", key)
	assert.NotContains(t, key, "\x00")
}
