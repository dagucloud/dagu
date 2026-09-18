// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/runtime/durability"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWriter verifies writer persistence to new and existing status files.
func TestWriter(t *testing.T) {
	th := setupTestRepository(t)

	t.Run("WriteStatusToNewFile", func(t *testing.T) {
		dag := th.DAG("test_write_status")
		dagRunID := uuid.Must(uuid.NewV7()).String()
		dagRunStatus := ir.NewStatusBuilder(dag.DAG).Create(dagRunID, ir.Running, 1, time.Now())
		writer := dag.Writer(t, dagRunID, time.Now())
		writer.Write(t, dagRunStatus)

		writer.AssertContent(t, "test_write_status", dagRunID, ir.Running)
	})

	t.Run("WriteStatusToExistingFile", func(t *testing.T) {
		dag := th.DAG("test_append_to_existing")
		dagRunID := uuid.Must(uuid.NewV7()).String()
		startedAt := time.Now()

		writer := dag.Writer(t, dagRunID, startedAt)

		dagRunStatus := ir.NewStatusBuilder(dag.DAG).Create(dagRunID, ir.Aborted, 1, time.Now())

		// Write initial status
		writer.Write(t, dagRunStatus)
		writer.Close(t)
		writer.AssertContent(t, "test_append_to_existing", dagRunID, ir.Aborted)

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
		dagRunStatus.Status = ir.Succeeded
		err = latestRun.Write(th.Context, dagRunStatus)
		require.NoError(t, err)

		// Verify appended data
		writer.AssertContent(t, "test_append_to_existing", dagRunID, ir.Succeeded)
	})
}

// TestWriterErrorHandling verifies writer lifecycle and error paths.
func TestWriterErrorHandling(t *testing.T) {
	th := setupTestRepository(t)

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
		dagRunStatus := ir.NewStatusBuilder(dag.DAG).Create(dagRunID, ir.Running, 1, time.Now())
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
		dagRunStatus := ir.NewStatusBuilder(dag.DAG).Create(dagRunID, ir.Running, 1, time.Now())

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

// awaitErr waits for the Await result from ch, failing the test if Await does
// not resolve within a bounded time. The deadline is a deadlock safety net only.
func awaitErr(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("Await did not resolve within bounded time; possible deadlock")
		return nil
	}
}

// TestWriterSyncBeforeAcknowledgeVG4 proves VG-4 (with VG-7/VG-8 on the failure
// path): the writer's file.Sync() (performed inside Writer.Write) is the
// durability "sync" step. Acknowledge must be issued only AFTER Write returns
// nil (Sync completed), and Await must resolve nil only once Sync has done so.
// A Sync failure must be reported via tracker.Fail and surface to Await.
func TestWriterSyncBeforeAcknowledgeVG4(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.ndjson")

	writer := NewWriter(path)
	require.NoError(t, writer.Open())
	t.Cleanup(func() { _ = writer.Close(context.Background()) })

	tr := durability.New()
	const key = "write-req"
	id := tr.Register(key)

	dag := &ir.DAG{Name: "vg4"}
	mkStatus := func(s ir.Status) ir.DAGRunStatus {
		return ir.NewStatusBuilder(dag).Create(uuid.Must(uuid.NewV7()).String(), s, 1, time.Now())
	}

	// Producer awaits (mirrors Runner.sendProgress Await).
	awaitDone := make(chan error, 1)
	go func() {
		awaitDone <- tr.Await(context.Background(), id)
	}()

	// Deterministic negative proof: no Acknowledge yet, so Await cannot resolve.
	select {
	case err := <-awaitDone:
		t.Fatalf("Await resolved (%v) before Write+Ack", err)
	default:
	}

	// Persist the status: Writer.Write performs encode + flush + file.Sync().
	// Sync succeeds; only after it returns nil do we Acknowledge.
	require.NoError(t, writer.Write(context.Background(), mkStatus(ir.Running)))
	tr.Acknowledge(key) // durability: acknowledge only after Sync completed

	err := awaitErr(t, awaitDone)
	require.NoError(t, err, "Await should return nil after Sync+Ack (VG-4)")

	// --- Sync-failure injection (VG-7/VG-8) ---
	id2 := tr.Register("fail-req")
	awaitFailDone := make(chan error, 1)
	go func() {
		awaitFailDone <- tr.Await(context.Background(), id2)
	}()

	// Deterministic negative proof: Fail not yet called.
	select {
	case err := <-awaitFailDone:
		t.Fatalf("Await resolved (%v) before Fail", err)
	default:
	}

	// Inject a Sync failure: close the underlying OS file descriptor so the
	// next Writer.Write's flushAndSyncLocked hits a closed fd and errors.
	// writer.file stays non-nil, so isOpenLocked() still reports open.
	require.NoError(t, writer.file.Close())
	writeErr := writer.Write(context.Background(), mkStatus(ir.Failed))
	require.Error(t, writeErr, "Write should fail when Sync fails")

	// Consumer reports the sync error via Fail (NOT Acknowledge).
	tr.Fail(writeErr)
	fErr := awaitErr(t, awaitFailDone)
	require.Error(t, fErr, "Await should return error on sync failure (VG-7)")
	if !errors.Is(fErr, writeErr) {
		t.Fatalf("Await returned %v; want the preserved sync error %v (VG-8)", fErr, writeErr)
	}
}
