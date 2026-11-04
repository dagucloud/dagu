// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sql

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveSQLiteDSN(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	dbPath := filepath.Join(workDir, "data.db")
	absolute := filepath.Join(t.TempDir(), "other.db")
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{"plain relative", "data.db", dbPath},
		{"file URI relative", "file:./data.db", "file:" + dbPath},
		{"file URI with query", "file:data.db?mode=ro", "file:" + dbPath + "?mode=ro"},
		{"absolute", absolute, absolute},
		{"memory", ":memory:", ":memory:"},
		{"shared memory", "file::memory:?cache=shared", "file::memory:?cache=shared"},
		{"memory mode", "file:mem?mode=memory", "file:mem?mode=memory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, resolveSQLiteDSN(workDir, tt.dsn))
		})
	}
}
