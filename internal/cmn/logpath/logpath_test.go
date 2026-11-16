// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package logpath

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// DAGs whose names differ only in characters a file name cannot hold, such as
// etl.daily and etl_daily, keep the logs of runs with the same ID apart.
func TestRunDirKeepsSimilarDAGNamesApart(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	dotted, err := Config{BaseDir: baseDir, Name: "etl.daily", DAGRunID: "run-1"}.RunDir()
	require.NoError(t, err)
	underscored, err := Config{BaseDir: baseDir, Name: "etl_daily", DAGRunID: "run-1"}.RunDir()
	require.NoError(t, err)
	assert.NotEqual(t, filepath.Dir(dotted), filepath.Dir(underscored))
}
