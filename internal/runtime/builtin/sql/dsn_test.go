// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sql

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveSQLiteDSN(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	dbPath := filepath.Join(workDir, "data.db")
	uriDir := filepath.ToSlash(workDir)
	absolute := filepath.Join(t.TempDir(), "other.db")
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{"plain relative", "data.db", dbPath},
		{"plain with driver query", "data.db?_pragma=busy_timeout(5000)", dbPath + "?_pragma=busy_timeout(5000)"},
		{"file URI relative", "file:./data.db", "file:" + uriDir + "/data.db"},
		{"file URI with query", "file:data.db?mode=ro", "file:" + uriDir + "/data.db?mode=ro"},
		{"file URI keeps its escapes", "file:my%20data.db", "file:" + uriDir + "/my%20data.db"},
		{"absolute", absolute, absolute},
		{"absolute file URI", "file:///var/lib/app.db", "file:///var/lib/app.db"},
		{"memory", ":memory:", ":memory:"},
		{"shared memory", "file::memory:?cache=shared", "file::memory:?cache=shared"},
		{"memory mode", "file:mem?mode=memory", "file:mem?mode=memory"},
		{"memory in a file name", "reports:memory:.db", filepath.Join(workDir, "reports:memory:.db")},
		{"mode in a file name", "file:mode=memory.db", "file:" + uriDir + "/mode=memory.db"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveSQLiteDSN(workDir, tt.dsn)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// A working directory with URI or query characters must not change which file
// the DSN opens.
func TestResolveSQLiteDSN_SpecialWorkingDir(t *testing.T) {
	t.Parallel()

	const workDir = "/srv/a?b#c%d"
	got, err := resolveSQLiteDSN(workDir, "file:data.db?mode=ro")
	require.NoError(t, err)
	assert.Equal(t, "file:/srv/a%3Fb%23c%25d/data.db?mode=ro", got)

	_, err = resolveSQLiteDSN(workDir, "data.db")
	require.ErrorContains(t, err, "contains '?'")
}
