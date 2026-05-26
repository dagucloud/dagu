// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	"github.com/dagucloud/dagu/internal/runtime/transform"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWriter verifies writer persistence to new and existing status files.
func TestWriter(t *testing.T) {
	th := setupTestStore(t)

	t.Run("WriteStatusToNewFile", func(t *testing.T) {
		dag := th.DAG("test_write_status")
		dagRunID := uuid.Must(uuid.NewV7()).String()
		dagRunStatus := transform.NewStatusBuilder(dag.DAG).Create(dagRunID, core.Running, 1, time.Now())
		writer := dag.Writer(t, dagRunID, time.Now())
		writer.Write(t, dagRunStatus)

		writer.AssertContent(t, "test_write_status", dagRunID, core.Running)
	})

	t.Run("WriteStatusToExistingFile", func(t *testing.T) {
		dag := th.DAG("test_append_to_existing")
		dagRunID := uuid.Must(uuid.NewV7()).String()
		startedAt := time.Now()

		writer := dag.Writer(t, dagRunID, startedAt)

		dagRunStatus := transform.NewStatusBuilder(dag.DAG).Create(dagRunID, core.Aborted, 1, time.Now())

		// Write initial status
		writer.Write(t, dagRunStatus)
		writer.Close(t)
		writer.AssertContent(t, "test_append_to_existing", dagRunID, core.Aborted)

		// Append to existing file
		dataRoot := NewDataRoot(th.TmpDir, dag.Name)
		run, err := dataRoot.FindByDAGRunID(th.Context, dagRunID)
		require.NoError(t, err)

		latestRun, err := run.LatestAttempt(th.Context, nil)
		require.NoError(t, err)

		err = latestRun.Open(th.Context)
		require.NoError(t, err)
		defer func() {
			_ = latestRun.Close(th.Context)
		}()

		// Append new status
		dagRunStatus.Status = core.Succeeded
		err = latestRun.Write(th.Context, dagRunStatus)
		require.NoError(t, err)

		// Verify appended data
		writer.AssertContent(t, "test_append_to_existing", dagRunID, core.Succeeded)
	})
}

// TestWriterErrorHandling verifies writer lifecycle and error paths.
func TestWriterErrorHandling(t *testing.T) {
	th := setupTestStore(t)

	t.Run("OpenNonExistentDirectory", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "blocked")
		testutil.BlockPathWithFile(t, blocker)

		writer := NewWriter(filepath.Join(blocker, "file.dat"))
		err := writer.Open()
		assert.Error(t, err)
	})

	t.Run("WriteToClosedWriter", func(t *testing.T) {
		writer := NewWriter(filepath.Join(th.TmpDir, "test.dat"))
		require.NoError(t, writer.Open())
		require.NoError(t, writer.close())

		dag := th.DAG("test_write_to_closed_writer")
		dagRunID := uuid.Must(uuid.NewV7()).String()
		dagRunStatus := transform.NewStatusBuilder(dag.DAG).Create(dagRunID, core.Running, 1, time.Now())
		assert.Error(t, writer.write(dagRunStatus))
	})

	t.Run("CloseMultipleTimes", func(t *testing.T) {
		writer := NewWriter(filepath.Join(th.TmpDir, "test.dat"))
		require.NoError(t, writer.Open())
		require.NoError(t, writer.close())
		assert.NoError(t, writer.close()) // Second close should not return an error
	})

	t.Run("IsOpenTracksLifecycle", func(t *testing.T) {
		writer := NewWriter(filepath.Join(th.TmpDir, "lifecycle.dat"))
		assert.False(t, writer.IsOpen())
		require.NoError(t, writer.Open())
		assert.True(t, writer.IsOpen())
		require.NoError(t, writer.close())
		assert.False(t, writer.IsOpen())
	})

	t.Run("WritesNewlineDelimitedJSON", func(t *testing.T) {
		writerPath := filepath.Join(th.TmpDir, "ndjson.dat")
		writer := NewWriter(writerPath)
		require.NoError(t, writer.Open())

		dag := th.DAG("test_newline_delimited_json")
		dagRunID := uuid.Must(uuid.NewV7()).String()
		dagRunStatus := transform.NewStatusBuilder(dag.DAG).Create(dagRunID, core.Running, 1, time.Now())

		require.NoError(t, writer.write(dagRunStatus))
		require.NoError(t, writer.close())

		data, err := os.ReadFile(writerPath)
		require.NoError(t, err)
		require.NotEmpty(t, data)

		var decoded map[string]any
		require.NoError(t, json.Unmarshal(bytes.TrimRight(data, "\n"), &decoded))

		assert.Equal(t, byte('\n'), data[len(data)-1])
	})
}

// TestWriterRename verifies status files follow DAG rename operations.
func TestWriterRename(t *testing.T) {
	th := setupTestStore(t)

	// Create a status file with old path
	dag := th.DAG("test_rename_old")
	writer := dag.Writer(t, "dag-run-id-1", time.Now())
	dagRunID := uuid.Must(uuid.NewV7()).String()
	dagRunStatus := transform.NewStatusBuilder(dag.DAG).Create(dagRunID, core.Running, 1, time.Now())
	writer.Write(t, dagRunStatus)
	writer.Close(t)
	require.FileExists(t, writer.FilePath)

	// Rename and verify the file
	newDAG := th.DAG("test_rename_new")
	err := th.Store.RenameDAGRuns(context.Background(), dag.Location, newDAG.Location)
	require.NoError(t, err)
	newWriter := newDAG.Writer(t, "dag-run-id-2", time.Now())

	require.NoFileExists(t, writer.FilePath)
	require.FileExists(t, newWriter.FilePath)
}
