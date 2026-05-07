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
	"github.com/dagucloud/dagu/internal/persis/dagrunstore/postgres/migrations"
)

func TestMigrationUsesExistingIdentifierConstraints(t *testing.T) {
	data, err := migrations.FS.ReadFile("20260506000000_create_dag_run_attempts.sql")
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
