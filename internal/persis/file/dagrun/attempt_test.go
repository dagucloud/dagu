// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAttempt_Open(t *testing.T) {
	dir := createTempDir(t)
	file := filepath.Join(dir, "status.dat")

	att, err := NewAttempt(file, nil)
	require.NoError(t, err)

	// Test successful open
	err = att.Open(context.Background())
	assert.NoError(t, err)

	// Test open when already open
	err = att.Open(context.Background())
	assert.ErrorIs(t, err, ErrStatusFileOpen)

	// Cleanup
	err = att.Close(context.Background())
	assert.NoError(t, err)
}

func TestAttempt_OpenRejectsCorruptDAGDefinition(t *testing.T) {
	dir := createTempDir(t)
	file := filepath.Join(dir, "status.dat")
	ctx := context.Background()

	att, err := NewAttempt(file, nil)
	require.NoError(t, err)
	att.SetDAG(&ir.DAG{Name: "test"})
	require.NoError(t, att.Open(ctx))
	require.NoError(t, att.Close(ctx))
	require.NoError(t, os.WriteFile(filepath.Join(dir, DAGDefinition), []byte("{"), 0600))

	reopened, err := NewAttempt(file, nil)
	require.NoError(t, err)
	err = reopened.Open(ctx)
	require.ErrorContains(t, err, "failed to restore DAG definition")
}

func TestAttempt_Write(t *testing.T) {
	dir := createTempDir(t)
	file := filepath.Join(dir, "status.dat")

	att, err := NewAttempt(file, nil)
	require.NoError(t, err)

	// Test write without open
	testStatus := createTestStatus(ir.Running)
	err = att.Write(context.Background(), testStatus)
	assert.ErrorIs(t, err, ErrStatusFileNotOpen)

	// Open and write
	err = att.Open(context.Background())
	require.NoError(t, err)

	// Write test status
	err = att.Write(context.Background(), testStatus)
	assert.NoError(t, err)

	// Verify file content
	actual, err := att.ReadStatus(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "test", actual.DAGRunID)
	assert.Equal(t, ir.Running, actual.Status)

	// Close
	err = att.Close(context.Background())
	assert.NoError(t, err)
}

func TestAttempt_Read(t *testing.T) {
	dir := createTempDir(t)
	file := filepath.Join(dir, "status.dat")

	// Create test file with multiple status entries
	status1 := createTestStatus(ir.Running)
	status2 := createTestStatus(ir.Succeeded)

	// Create file directory if it doesn't exist
	err := os.MkdirAll(filepath.Dir(file), 0750)
	require.NoError(t, err)

	// Create test file with two status entries
	f, err := os.Create(file)
	require.NoError(t, err)

	data1, err := json.Marshal(status1)
	require.NoError(t, err)
	_, err = f.Write(append(data1, '\n'))
	require.NoError(t, err)

	data2, err := json.Marshal(status2)
	require.NoError(t, err)
	_, err = f.Write(append(data2, '\n'))
	require.NoError(t, err)

	err = f.Close()
	require.NoError(t, err)

	// Initialize attempt
	att, err := NewAttempt(file, nil)
	require.NoError(t, err)

	// Read status - should get the last entry (test2)
	dagRunStatus, err := att.ReadStatus(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, ir.Succeeded.String(), dagRunStatus.Status.String())

	// Read using ReadStatus
	latestStatus, err := att.ReadStatus(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, ir.Succeeded.String(), latestStatus.Status.String())
}

func TestAttempt_ReadStatusHonorsCanceledContext(t *testing.T) {
	dir := createTempDir(t)
	file := filepath.Join(dir, "status.dat")

	writeJSONToFile(t, file, createTestStatus(ir.Running))

	att, err := NewAttempt(file, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = att.ReadStatus(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestAttempt_ReadStatusUncachedDoesNotPopulateCache(t *testing.T) {
	t.Parallel()

	file := filepath.Join(createTempDir(t), "status.dat")
	writeJSONToFile(t, file, createTestStatus(ir.Queued))
	cache := fileutil.NewCache[*ir.DAGRunStatus]("dag_run_status", 10, time.Hour)
	att, err := NewAttempt(file, cache)
	require.NoError(t, err)

	status, err := att.ReadStatusUncached(t.Context())
	require.NoError(t, err)
	assert.Equal(t, ir.Queued, status.Status)
	assert.Zero(t, cache.Size())
}

func TestAttempt_Compact(t *testing.T) {
	dir := createTempDir(t)
	file := filepath.Join(dir, "status.dat")

	// Create test file with multiple status entries
	for i := range 10 {
		testStatus := createTestStatus(ir.Running)

		if i == 9 {
			// Make some status changes to create different attempts
			testStatus.Status = ir.Succeeded
		}

		if i == 0 {
			// Create new file for first write
			writeJSONToFile(t, file, testStatus)
		} else {
			// Append to existing file
			data, err := json.Marshal(testStatus)
			require.NoError(t, err)

			f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0600)
			require.NoError(t, err)

			_, err = f.Write(append(data, '\n'))
			require.NoError(t, err)
			_ = f.Close()
		}
	}

	// Get file size before compaction
	fileInfo, err := os.Stat(file)
	require.NoError(t, err)
	beforeSize := fileInfo.Size()

	// Initialize Attempt
	att, err := NewAttempt(file, nil)
	require.NoError(t, err)

	// Compact the file
	err = att.Compact(context.Background())
	assert.NoError(t, err)

	// Get file size after compaction
	fileInfo, err = os.Stat(file)
	require.NoError(t, err)
	afterSize := fileInfo.Size()

	// Verify file size reduced
	assert.Less(t, afterSize, beforeSize)

	// Verify content is still correct
	dagRunStatus, err := att.ReadStatus(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, ir.Succeeded, dagRunStatus.Status)
}

func TestAttempt_CompactReopensWriter(t *testing.T) {
	dir := createTempDir(t)
	file := filepath.Join(dir, "status.dat")

	att, err := NewAttempt(file, nil)
	require.NoError(t, err)
	require.NoError(t, att.Open(context.Background()))

	first := createTestStatus(ir.Running)
	require.NoError(t, att.Write(context.Background(), first))
	require.NotNil(t, att.writer)
	assert.True(t, att.writer.IsOpen())

	require.NoError(t, att.Compact(context.Background()))
	require.NotNil(t, att.writer)
	assert.True(t, att.writer.IsOpen())

	final := createTestStatus(ir.Succeeded)
	require.NoError(t, att.Write(context.Background(), final))

	status, err := att.ReadStatus(context.Background())
	require.NoError(t, err)
	assert.Equal(t, ir.Succeeded, status.Status)

	require.NoError(t, att.Close(context.Background()))
}

func TestAttempt_Close(t *testing.T) {
	dir := createTempDir(t)
	file := filepath.Join(dir, "status.dat")

	// Initialize and open Attempt
	att, err := NewAttempt(file, nil)
	require.NoError(t, err)

	err = att.Open(context.Background())
	require.NoError(t, err)

	// Write some data
	err = att.Write(context.Background(), createTestStatus(ir.Running))
	require.NoError(t, err)

	// Close
	err = att.Close(context.Background())
	assert.NoError(t, err)

	// Verify we can't write after close
	err = att.Write(context.Background(), createTestStatus(ir.Succeeded))
	assert.ErrorIs(t, err, ErrStatusFileNotOpen)

	// Test double close is safe
	err = att.Close(context.Background())
	assert.NoError(t, err)
}

// TestAttempt_WriteReopensUnusableFile checks that a write still persists
// when the open file can no longer be used, as an NFS handle goes stale when
// another host replaces the file. Closing the file under the writer stands
// in for that.
func TestAttempt_WriteReopensUnusableFile(t *testing.T) {
	ctx := context.Background()

	t.Run("WritePersists", func(t *testing.T) {
		att := openAttemptForTest(t)
		require.NoError(t, att.Write(ctx, statusWithPID(1)))
		require.NoError(t, att.writer.file.Close())

		require.NoError(t, att.Write(ctx, statusWithPID(2)))

		status, err := att.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, ir.PID(2), status.PID)
	})

	t.Run("FailedReopenRetries", func(t *testing.T) {
		att := openAttemptForTest(t)
		require.NoError(t, att.Write(ctx, statusWithPID(1)))
		require.NoError(t, att.writer.file.Close())
		blockStatusFile(t, att.file)
		require.ErrorIs(t, att.Write(ctx, statusWithPID(2)), ErrWriteFailed)

		require.NoError(t, fileutil.Remove(att.file))
		require.NoError(t, att.Write(ctx, statusWithPID(3)))

		status, err := att.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, ir.PID(3), status.PID)
	})
}

// blockStatusFile replaces the status file with a directory, so that opening
// it for writing fails until the directory is removed.
func blockStatusFile(t *testing.T, file string) {
	t.Helper()
	require.NoError(t, fileutil.Remove(file))
	require.NoError(t, os.Mkdir(file, 0750))
}

func TestAttempt_HandleNonExistentFile(t *testing.T) {
	dir := createTempDir(t)
	file := filepath.Join(dir, "invalid.dat")

	att, err := NewAttempt(file, nil)
	require.NoError(t, err)

	// Should be able to open a non-existent file
	err = att.Open(context.Background())
	assert.NoError(t, err)

	// Write to create the file
	err = att.Write(context.Background(), createTestStatus(ir.Succeeded))
	assert.NoError(t, err)

	// Verify the file was created with correct data
	dagRunStatus, err := att.ReadStatus(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "test", dagRunStatus.DAGRunID)

	// Cleanup
	err = att.Close(context.Background())
	assert.NoError(t, err)
}

func TestAttempt_EmptyFile(t *testing.T) {
	dir := createTempDir(t)
	file := filepath.Join(dir, "empty.dat")

	// Create an empty file
	f, err := os.Create(file)
	require.NoError(t, err)
	_ = f.Close()

	att, err := NewAttempt(file, nil)
	require.NoError(t, err)

	// Reading an empty file returns ErrCorruptedStatusData.
	_, err = att.ReadStatus(context.Background())
	assert.ErrorIs(t, err, dagrun.ErrCorruptedStatusData)

	// Compacting an empty file should be safe
	err = att.Compact(context.Background())
	assert.NoError(t, err)
}

func TestAttempt_InvalidJSON(t *testing.T) {
	dir := createTempDir(t)
	file := filepath.Join(dir, "invalid.dat")

	// Create a file with valid JSOn
	validStatus := createTestStatus(ir.Running)
	writeJSONToFile(t, file, validStatus)

	// Append invalid JSON
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0600)
	require.NoError(t, err)
	_, err = f.Write([]byte("invalid json\n"))
	require.NoError(t, err)

	att, err := NewAttempt(file, nil)
	require.NoError(t, err)

	// Should be able to read and get the valid entry
	dagRunStatus, err := att.ReadStatus(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, ir.Running.String(), dagRunStatus.Status.String())
}

func TestAttempt_CorruptedStatusFile(t *testing.T) {
	t.Run("EmptyFile", func(t *testing.T) {
		dir := createTempDir(t)
		file := filepath.Join(dir, "empty.jsonl")

		// Create empty file
		f, err := os.Create(file)
		require.NoError(t, err)
		require.NoError(t, f.Close())

		att, err := NewAttempt(file, nil)
		require.NoError(t, err)

		// An empty status file returns ErrCorruptedStatusData.
		_, err = att.ReadStatus(context.Background())
		assert.ErrorIs(t, err, dagrun.ErrCorruptedStatusData)
	})

	t.Run("OnlyWhitespace", func(t *testing.T) {
		dir := createTempDir(t)
		file := filepath.Join(dir, "whitespace.jsonl")

		// Create file with only whitespace
		err := os.WriteFile(file, []byte("\n\n\n"), 0600)
		require.NoError(t, err)

		att, err := NewAttempt(file, nil)
		require.NoError(t, err)

		// Status data without a complete record returns ErrCorruptedStatusData.
		_, err = att.ReadStatus(context.Background())
		assert.ErrorIs(t, err, dagrun.ErrCorruptedStatusData)
	})

	t.Run("NoValidJSON", func(t *testing.T) {
		dir := createTempDir(t)
		file := filepath.Join(dir, "novalid.jsonl")

		// Create file with only invalid JSON
		err := os.WriteFile(file, []byte("not json\nstill not json\n"), 0600)
		require.NoError(t, err)

		att, err := NewAttempt(file, nil)
		require.NoError(t, err)

		// Status data without a valid record returns ErrCorruptedStatusData.
		_, err = att.ReadStatus(context.Background())
		assert.ErrorIs(t, err, dagrun.ErrCorruptedStatusData)
	})
}

func TestAttempt_ReadStatusFromTheEnd(t *testing.T) {
	// snapshots writes one status line per PID, the way the writer appends them.
	snapshots := func(t *testing.T, pids ...int) []byte {
		t.Helper()
		var data []byte
		for _, pid := range pids {
			st := createTestStatus(ir.Running)
			st.PID = ir.PID(pid)
			line, err := json.Marshal(st)
			require.NoError(t, err)
			data = append(append(data, line...), '\n')
		}
		return data
	}
	read := func(t *testing.T, data []byte) (*ir.DAGRunStatus, error) {
		t.Helper()
		file := filepath.Join(createTempDir(t), "status.jsonl")
		require.NoError(t, os.WriteFile(file, data, 0600))
		att, err := NewAttempt(file, nil)
		require.NoError(t, err)
		return att.ReadStatus(context.Background())
	}

	t.Run("LatestSnapshot", func(t *testing.T) {
		status, err := read(t, snapshots(t, 1, 2, 3))
		require.NoError(t, err)
		assert.Equal(t, ir.PID(3), status.PID)
	})

	t.Run("LineStillBeingWritten", func(t *testing.T) {
		data := snapshots(t, 1, 2)
		partial := snapshots(t, 3)
		status, err := read(t, append(data, partial[:len(partial)/2]...))
		require.NoError(t, err)
		assert.Equal(t, ir.PID(2), status.PID)
	})

	t.Run("InvalidLastLine", func(t *testing.T) {
		status, err := read(t, append(snapshots(t, 1, 2), []byte("{not json\n")...))
		require.NoError(t, err)
		assert.Equal(t, ir.PID(2), status.PID)
	})

	t.Run("SnapshotLargerThanAReadChunk", func(t *testing.T) {
		st := createTestStatus(ir.Running)
		st.PID = ir.PID(4)
		st.Log = strings.Repeat("x", 300*1024)
		line, err := json.Marshal(st)
		require.NoError(t, err)
		status, err := read(t, append(append(snapshots(t, 1, 2), line...), '\n'))
		require.NoError(t, err)
		assert.Equal(t, ir.PID(4), status.PID)
		assert.Len(t, status.Log, 300*1024)
	})
}

// TestAttempt_WriteAfterPartialLine checks that a status appended to a file
// that ends in a partial line, as a write cut off by a crash or a full disk
// leaves it, is read back, and that complete lines get no blank line.
func TestAttempt_WriteAfterPartialLine(t *testing.T) {
	ctx := context.Background()
	line := func(t *testing.T, pid int) []byte {
		t.Helper()
		st := createTestStatus(ir.Running)
		st.PID = ir.PID(pid)
		data, err := json.Marshal(st)
		require.NoError(t, err)
		return append(data, '\n')
	}
	partial := func(t *testing.T, pid int) []byte {
		t.Helper()
		data := line(t, pid)
		return data[:len(data)/2]
	}
	open := func(t *testing.T, data []byte) *Attempt {
		t.Helper()
		file := filepath.Join(createTempDir(t), "status.jsonl")
		if data != nil {
			require.NoError(t, os.WriteFile(file, data, 0600))
		}
		att, err := NewAttempt(file, nil)
		require.NoError(t, err)
		require.NoError(t, att.Open(ctx))
		t.Cleanup(func() { _ = att.Close(ctx) })
		return att
	}
	write := func(t *testing.T, att *Attempt, pid int) {
		t.Helper()
		st := createTestStatus(ir.Running)
		st.PID = ir.PID(pid)
		require.NoError(t, att.Write(ctx, st))
	}
	readPID := func(t *testing.T, att *Attempt) ir.PID {
		t.Helper()
		status, err := att.ReadStatus(ctx)
		require.NoError(t, err)
		return status.PID
	}

	t.Run("Open", func(t *testing.T) {
		att := open(t, append(line(t, 1), partial(t, 2)...))
		write(t, att, 3)
		assert.Equal(t, ir.PID(3), readPID(t, att))

		require.NoError(t, att.Close(ctx))
		assert.Equal(t, ir.PID(3), readPID(t, att), "compaction keeps it")
	})

	t.Run("ReopenAfterReplace", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows cannot replace a file that another handle holds open")
		}
		att := open(t, line(t, 1))
		replacement := filepath.Join(filepath.Dir(att.file), "replacement")
		require.NoError(t, os.WriteFile(replacement, append(line(t, 2), partial(t, 3)...), 0600))
		require.NoError(t, os.Rename(replacement, att.file))

		write(t, att, 4)
		assert.Equal(t, ir.PID(4), readPID(t, att))
	})

	t.Run("CompleteLines", func(t *testing.T) {
		for name, data := range map[string][]byte{"NewFile": nil, "EndsWithNewline": line(t, 1)} {
			t.Run(name, func(t *testing.T) {
				att := open(t, data)
				write(t, att, 2)
				require.NoError(t, att.writer.close())
				require.NoError(t, att.writer.Open())
				write(t, att, 3)

				written, err := fileutil.ReadFile(att.file)
				require.NoError(t, err)
				assert.False(t, bytes.HasPrefix(written, []byte("\n")))
				assert.NotContains(t, string(written), "\n\n")
				assert.Equal(t, ir.PID(3), readPID(t, att))
			})
		}
	})
}

// A running attempt writes change records, and each status it writes reads
// back as written. Close leaves one full record.
func TestAttempt_WritesChangeRecords(t *testing.T) {
	ctx := context.Background()
	att := openAttemptForTest(t)
	st := wideStatus(20)
	for i := range 40 {
		node := st.Nodes[i%len(st.Nodes)]
		node.Status = ir.NodeStatus(1 + i%4)
		node.StartedAt = fmt.Sprintf("2026-11-15T00:00:%02dZ", i)
		require.NoError(t, att.Write(ctx, st))
		status, err := att.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, readBack(t, st), status)
	}
	data, err := fileutil.ReadFile(att.file)
	require.NoError(t, err)
	require.Contains(t, string(data), "\n~")

	require.NoError(t, att.Close(ctx))
	data, err = fileutil.ReadFile(att.file)
	require.NoError(t, err)
	assert.Equal(t, 1, bytes.Count(data, []byte("\n")))
	assert.Equal(t, byte('{'), data[0])
	status, err := ParseStatusFile(att.file)
	require.NoError(t, err)
	assert.Equal(t, readBack(t, st), status)
}

// A record that another writer appends between two of the runner's, such as
// an approval, does not break the runner's change records.
func TestAttempt_ChangeRecordAfterOtherWriter(t *testing.T) {
	ctx := context.Background()
	att := openAttemptForTest(t)
	st := wideStatus(20)
	require.NoError(t, att.Write(ctx, st))

	other := NewWriter(att.file)
	require.NoError(t, other.Open())
	approved := wideStatus(20)
	approved.Nodes[0].ApprovedBy = "other"
	require.NoError(t, other.Write(ctx, approved))
	require.NoError(t, other.close())

	st.Nodes[1].Status = ir.NodeRunning
	require.NoError(t, att.Write(ctx, st))
	require.Equal(t, byte('~'), lastLine(t, att.file)[0])
	status, err := att.ReadStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, readBack(t, st), status)
}

// Once another attempt replaces the file, as its compaction does, the line the
// runner's next change record would build on is gone.
func TestAttempt_WriteAfterReplacedFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot replace a file that another handle holds open")
	}
	ctx := context.Background()
	att := openAttemptForTest(t)
	st := wideStatus(20)
	require.NoError(t, att.Write(ctx, st))
	st.Nodes[0].Status = ir.NodeRunning
	require.NoError(t, att.Write(ctx, st))

	other, err := NewAttempt(att.file, nil)
	require.NoError(t, err)
	require.NoError(t, other.Compact(ctx))

	st.Nodes[1].Status = ir.NodeRunning
	require.NoError(t, att.Write(ctx, st))
	status, err := att.ReadStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, readBack(t, st), status)
}

func TestSafeRename(t *testing.T) {
	dir := createTempDir(t)
	sourceFile := filepath.Join(dir, "source.txt")
	targetFile := filepath.Join(dir, "target.txt")

	// Create source file
	err := os.WriteFile(sourceFile, []byte("test content"), 0600)
	require.NoError(t, err)

	// Test rename when target doesn't exist
	err = safeRename(sourceFile, targetFile)
	assert.NoError(t, err)
	assert.FileExists(t, targetFile)
	assert.NoFileExists(t, sourceFile)

	// Create source again
	err = os.WriteFile(sourceFile, []byte("new content"), 0600)
	require.NoError(t, err)

	// Test rename when target exists
	err = safeRename(sourceFile, targetFile)
	assert.NoError(t, err)
	assert.FileExists(t, targetFile)
	assert.NoFileExists(t, sourceFile)

	// Read target to verify content was updated
	content, err := os.ReadFile(targetFile)
	require.NoError(t, err)
	assert.Equal(t, "new content", string(content))
}

func TestAttempt_HideAndIsHidden(t *testing.T) {
	ctx := context.Background()

	// Create attempt
	dir := createTempDir(t)
	statusFile := filepath.Join(dir, "status.dat")
	att, err := NewAttempt(statusFile, nil)
	require.NoError(t, err)

	// Initially not hidden
	assert.False(t, att.Hidden())

	// Can't hide when open
	require.NoError(t, att.Open(ctx))
	assert.ErrorIs(t, att.Hide(ctx), ErrStatusFileOpen)

	// Can hide after close
	require.NoError(t, att.Close(ctx))
	require.NoError(t, att.Hide(ctx))
	assert.True(t, att.Hidden())

	// Idempotent
	require.NoError(t, att.Hide(ctx))
	assert.True(t, att.Hidden())
}

// createTempDir creates a temporary directory for testing
func createTempDir(t *testing.T) string {
	t.Helper()

	attemptID, err := genAttemptID()
	require.NoError(t, err)

	dir, err := os.MkdirTemp("", attemptDirName(persis.NewUTC(time.Now()), attemptID))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})

	return dir
}

// createTestDAG creates a sample DAG for testing
func createTestDAG() *ir.DAG {
	return &ir.DAG{
		Name: "TestDAG",
		Steps: []ir.Step{
			{
				Name:    "step1",
				Command: "echo 'step1'",
			},
			{
				Name:    "step2",
				Command: "echo 'step2'",
				Depends: []string{
					"step1",
				},
			},
		},
		HandlerOn: ir.HandlerOn{
			Success: &ir.Step{
				Name:    "on_success",
				Command: "echo 'success'",
			},
			Failure: &ir.Step{
				Name:    "on_failure",
				Command: "echo 'failure'",
			},
		},
		Params: []string{"--param1=value1", "--param2=value2"},
	}
}

// openAttemptForTest opens an attempt on a new status file and closes it when
// the test ends.
func openAttemptForTest(t *testing.T) *Attempt {
	t.Helper()
	att, err := NewAttempt(filepath.Join(createTempDir(t), "status.jsonl"), nil)
	require.NoError(t, err)
	require.NoError(t, att.Open(context.Background()))
	t.Cleanup(func() { _ = att.Close(context.Background()) })
	return att
}

// statusWithPID returns a running status that the PID tells apart.
func statusWithPID(pid int) ir.DAGRunStatus {
	status := createTestStatus(ir.Running)
	status.PID = ir.PID(pid)
	return status
}

// wideStatus returns a running status with enough steps that a change to one
// of them is written as a change record.
func wideStatus(steps int) ir.DAGRunStatus {
	st := createTestStatus(ir.Running)
	for i := range steps {
		st.Nodes = append(st.Nodes, ir.NewNodeFromStep(ir.Step{Name: fmt.Sprintf("wide%d", i)}))
	}
	return st
}

// readBack returns the status as reading its full record returns it.
func readBack(t *testing.T, st ir.DAGRunStatus) *ir.DAGRunStatus {
	t.Helper()
	data, err := json.Marshal(ir.StoredStatus(st))
	require.NoError(t, err)
	var read ir.DAGRunStatus
	require.NoError(t, json.Unmarshal(data, &read))
	return &read
}

// lastLine returns the last line of the file.
func lastLine(t *testing.T, file string) []byte {
	t.Helper()
	data, err := fileutil.ReadFile(file)
	require.NoError(t, err)
	lines := bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
	return lines[len(lines)-1]
}

// createTestStatus creates a sample status for testing using StatusFactory
func createTestStatus(st ir.Status) ir.DAGRunStatus {
	dag := createTestDAG()

	return ir.DAGRunStatus{
		Name:      dag.Name,
		DAGRunID:  "test",
		Status:    st,
		PID:       ir.PID(12345),
		StartedAt: stringutil.FormatTime(time.Now()),
		Nodes:     ir.NewNodesFromSteps(dag.Steps),
	}
}

// BenchmarkReadLatestStatus reads the latest status from a running DAG's file,
// which holds many snapshots with a large step output, and from a finished
// run's file, which compaction leaves with one small snapshot.
func BenchmarkReadLatestStatus(b *testing.B) {
	cases := []struct {
		name      string
		status    ir.Status
		snapshots int
		logSize   int
	}{
		{name: "Running", status: ir.Running, snapshots: 12, logSize: 512 * 1024},
		{name: "Compacted", status: ir.Succeeded, snapshots: 1},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			file := filepath.Join(b.TempDir(), "status.jsonl")
			var data []byte
			for i := range tc.snapshots {
				st := createTestStatus(tc.status)
				st.PID = ir.PID(i)
				st.Log = strings.Repeat("x", tc.logSize)
				line, err := json.Marshal(st)
				require.NoError(b, err)
				data = append(append(data, line...), '\n')
			}
			require.NoError(b, os.WriteFile(file, data, 0600))

			b.ReportAllocs()
			for b.Loop() {
				if _, err := ParseStatusFile(file); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkDAGRunJSON(b *testing.B) {
	fixture := createDAGRunJSONBenchmarkData()
	cases := []struct {
		name       string
		value      any
		newEncoder func() benchmarkJSONEncoder
		newTarget  func() any
	}{
		{
			name:       "Status",
			value:      fixture.status,
			newEncoder: newStatusJSONBenchmarkEncoder,
			newTarget:  func() any { return new(ir.DAGRunStatus) },
		},
		{
			name:      "DAG",
			value:     fixture.dag,
			newTarget: func() any { return new(ir.DAG) },
		},
		{
			name:       "Outputs",
			value:      fixture.outputs,
			newEncoder: newIndentedJSONBenchmarkEncoder,
			newTarget:  func() any { return new(ir.DAGRunOutputs) },
		},
		{
			name:       "StepMessages",
			value:      fixture.messages,
			newEncoder: newIndentedJSONBenchmarkEncoder,
			newTarget:  func() any { return new([]ir.LLMMessage) },
		},
		{
			name: "RetryCandidate",
			value: retryCandidateFile{
				RunTimestampUnix: 1784505600,
				Status:           retryCandidateStatus(fixture.status),
			},
			newTarget: func() any { return new(retryCandidateFile) },
		},
		{
			name: "LatestAttemptPointer",
			value: latestAttemptPointer{
				StatusFile: "2026/07/20/dag-run_20260720_000000Z_run-benchmark/attempt_20260720_000000_000Z_000001/status.jsonl",
			},
			newTarget: func() any { return new(latestAttemptPointer) },
		},
		{
			name: "QueryCursor",
			value: queryCursorPayload{
				Version:    queryCursorVersion,
				FilterHash: "267ea072a7c8443529ba567a1c54282bf75c77c10f35f255fd6cb2f63eb323f3",
				Timestamp:  "2026-07-20T00:00:00.123456789Z",
				Name:       "benchmark-dag",
				DAGRunID:   "run-benchmark",
			},
			newTarget: func() any { return new(queryCursorPayload) },
		},
	}

	for _, tc := range cases {
		newEncoder := tc.newEncoder
		if newEncoder == nil {
			newEncoder = func() benchmarkJSONEncoder { return json.Marshal }
		}
		encoder := newEncoder()
		data, err := encoder(tc.value)
		require.NoError(b, err)
		data = bytes.Clone(data)

		b.Run(tc.name+"/Encode", func(b *testing.B) {
			encoder := newEncoder()
			b.ResetTimer()
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			for range b.N {
				if _, err := encoder(tc.value); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run(tc.name+"/Unmarshal", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			for range b.N {
				if err := json.Unmarshal(data, tc.newTarget()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

type benchmarkJSONEncoder func(any) ([]byte, error)

func newStatusJSONBenchmarkEncoder() benchmarkJSONEncoder {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	return func(value any) ([]byte, error) {
		buffer.Reset()
		if err := encoder.Encode(value); err != nil {
			return nil, err
		}
		return buffer.Bytes(), nil
	}
}

func newIndentedJSONBenchmarkEncoder() benchmarkJSONEncoder {
	return func(value any) ([]byte, error) {
		return json.MarshalIndent(value, "", "  ")
	}
}

type dagRunJSONBenchmarkData struct {
	dag      *ir.DAG
	status   ir.DAGRunStatus
	outputs  *ir.DAGRunOutputs
	messages []ir.LLMMessage
}

func createDAGRunJSONBenchmarkData() dagRunJSONBenchmarkData {
	const stepCount = 50

	dag := createTestDAG()
	dag.Name = "benchmark-dag"
	dag.Description = "Representative workflow used to measure persisted DAG-run JSON encoding."
	dag.DefaultParams = `{"environment":"production","region":"ap-northeast-1"}`
	dag.PresolvedBuildEnv = map[string]string{
		"DAGU_ENV":    "production",
		"DAGU_REGION": "ap-northeast-1",
	}
	dag.Steps = make([]ir.Step, stepCount)
	for i := range stepCount {
		name := fmt.Sprintf("step-%02d", i+1)
		step := ir.Step{
			ID:          name,
			Name:        name,
			Description: "Process one stage of the benchmark workflow.",
			Command:     fmt.Sprintf("process --stage=%d --input=${INPUT_FILE}", i+1),
			Env: []string{
				fmt.Sprintf("STAGE=%d", i+1),
				"INPUT_FILE=/var/lib/dagu/input.json",
			},
			Output:  "RESULT",
			Timeout: 5 * time.Minute,
		}
		if i > 0 {
			step.Depends = []string{dag.Steps[i-1].Name}
		}
		dag.Steps[i] = step
	}

	messages := make([]ir.LLMMessage, 0, 16)
	for i := range 8 {
		messages = append(messages,
			ir.LLMMessage{
				Role:    ir.LLMRoleUser,
				Content: fmt.Sprintf("Inspect workflow stage %d and summarize its output.", i+1),
			},
			ir.LLMMessage{
				Role:    ir.LLMRoleAssistant,
				Content: fmt.Sprintf("Stage %d completed successfully with validated output.", i+1),
				Metadata: &ir.LLMMessageMetadata{
					Provider:         "openai",
					Model:            "benchmark-model",
					PromptTokens:     1200 + i,
					CompletionTokens: 180 + i,
					TotalTokens:      1380 + 2*i,
					Cost:             0.0123,
				},
			},
		)
	}

	status := ir.InitialStatus(dag)
	status.DAGRunID = "run-benchmark"
	status.AttemptID = "attempt-benchmark"
	status.AttemptKey = "benchmark-dag:run-benchmark:attempt-benchmark"
	status.Status = ir.Succeeded
	status.TriggerType = ir.TriggerTypeScheduler
	status.TriggerActor = "scheduler"
	status.WorkerID = "worker-01"
	status.PID = ir.PID(12345)
	status.PIDStartedAt = 1784505600000
	status.CreatedAt = 1784505600000
	status.QueuedAt = "2026-07-20T00:00:00Z"
	status.StartedAt = "2026-07-20T00:00:01Z"
	status.FinishedAt = "2026-07-20T00:02:31Z"
	status.WorkingDir = "/var/lib/dagu/benchmark-dag"
	status.Params = `environment=production region=ap-northeast-1`
	status.ParamsList = []string{"environment=production", "region=ap-northeast-1"}
	status.Labels = []string{"environment=production", "team=platform"}
	for i, node := range status.Nodes {
		node.Status = ir.NodeSucceeded
		node.Stdout = fmt.Sprintf("/var/log/dagu/benchmark-dag/step-%02d.out", i+1)
		node.Stderr = fmt.Sprintf("/var/log/dagu/benchmark-dag/step-%02d.err", i+1)
		node.StartedAt = "2026-07-20T00:00:01Z"
		node.FinishedAt = "2026-07-20T00:00:04Z"
		output := fmt.Sprintf(`{"stage":%d,"status":"succeeded"}`, i+1)
		node.OutputValue = &output
		if i == len(status.Nodes)-1 {
			node.ChatMessages = messages
		}
	}

	outputValues := make(map[string]string, stepCount)
	for i := range stepCount {
		outputValues[fmt.Sprintf("step_%02d", i+1)] = fmt.Sprintf("result-%02d", i+1)
	}
	outputs := &ir.DAGRunOutputs{
		Metadata: ir.OutputsMetadata{
			DAGName:     dag.Name,
			DAGRunID:    status.DAGRunID,
			AttemptID:   status.AttemptID,
			Status:      status.Status.String(),
			CompletedAt: status.FinishedAt,
			Params:      status.Params,
		},
		Outputs: outputValues,
	}

	return dagRunJSONBenchmarkData{
		dag:      dag,
		status:   status,
		outputs:  outputs,
		messages: messages,
	}
}

// writeJSONToFile writes a JSON object to a file for testing
func writeJSONToFile(t *testing.T, file string, obj any) {
	t.Helper()
	data, err := json.Marshal(obj)
	require.NoError(t, err)

	err = os.WriteFile(file, append(data, '\n'), 0600)
	require.NoError(t, err)
}

func TestAttempt_WriteOutputs(t *testing.T) {
	ctx := context.Background()

	t.Run("WriteValidOutputs", func(t *testing.T) {
		dir := createTempDir(t)
		statusFile := filepath.Join(dir, "status.dat")
		att, err := NewAttempt(statusFile, nil)
		require.NoError(t, err)

		outputs := &ir.DAGRunOutputs{
			Metadata: ir.OutputsMetadata{
				DAGName:     "test-dag",
				DAGRunID:    "run-123",
				AttemptID:   "attempt-1",
				Status:      "succeeded",
				CompletedAt: "2024-12-28T15:30:45Z",
				Params:      `["key=value"]`,
			},
			Outputs: map[string]string{
				"totalCount": "42",
				"resultFile": "/path/to/result.txt",
			},
		}

		err = att.WriteOutputs(ctx, outputs)
		require.NoError(t, err)

		// Verify file was created
		outputsFile := filepath.Join(dir, OutputsFile)
		assert.FileExists(t, outputsFile)

		// Verify content
		data, err := os.ReadFile(outputsFile)
		require.NoError(t, err)

		var readOutputs ir.DAGRunOutputs
		err = json.Unmarshal(data, &readOutputs)
		require.NoError(t, err)

		assert.Equal(t, outputs.Metadata, readOutputs.Metadata)
		assert.Equal(t, outputs.Outputs, readOutputs.Outputs)
	})

	t.Run("WriteEmptyOutputs_NoFileCreated", func(t *testing.T) {
		dir := createTempDir(t)
		statusFile := filepath.Join(dir, "status.dat")
		att, err := NewAttempt(statusFile, nil)
		require.NoError(t, err)

		outputs := &ir.DAGRunOutputs{
			Metadata: ir.OutputsMetadata{},
			Outputs:  map[string]string{},
		}

		err = att.WriteOutputs(ctx, outputs)
		require.NoError(t, err)

		// Verify file was NOT created (per implementation spec)
		outputsFile := filepath.Join(dir, OutputsFile)
		assert.NoFileExists(t, outputsFile)
	})

	t.Run("WriteNilOutputs_NoFileCreated", func(t *testing.T) {
		dir := createTempDir(t)
		statusFile := filepath.Join(dir, "status.dat")
		att, err := NewAttempt(statusFile, nil)
		require.NoError(t, err)

		err = att.WriteOutputs(ctx, nil)
		require.NoError(t, err)

		// Verify file was NOT created (per implementation spec)
		outputsFile := filepath.Join(dir, OutputsFile)
		assert.NoFileExists(t, outputsFile)
	})

	t.Run("OverwriteExistingOutputs", func(t *testing.T) {
		dir := createTempDir(t)
		statusFile := filepath.Join(dir, "status.dat")
		att, err := NewAttempt(statusFile, nil)
		require.NoError(t, err)

		// Write first outputs
		outputs1 := &ir.DAGRunOutputs{
			Metadata: ir.OutputsMetadata{DAGName: "dag1", DAGRunID: "run-1"},
			Outputs:  map[string]string{"key1": "value1"},
		}
		err = att.WriteOutputs(ctx, outputs1)
		require.NoError(t, err)

		// Write second outputs (overwrites first)
		outputs2 := &ir.DAGRunOutputs{
			Metadata: ir.OutputsMetadata{DAGName: "dag2", DAGRunID: "run-2"},
			Outputs:  map[string]string{"key2": "value2"},
		}
		err = att.WriteOutputs(ctx, outputs2)
		require.NoError(t, err)

		// Verify second version overwrites first
		readOutputs, err := att.ReadOutputs(ctx)
		require.NoError(t, err)
		require.NotNil(t, readOutputs)
		assert.Equal(t, outputs2.Outputs, readOutputs.Outputs)
		assert.Equal(t, outputs2.Metadata.DAGName, readOutputs.Metadata.DAGName)
	})
}

func TestAttempt_ReadOutputs(t *testing.T) {
	ctx := context.Background()

	t.Run("ReadExistingOutputs", func(t *testing.T) {
		dir := createTempDir(t)
		statusFile := filepath.Join(dir, "status.dat")
		att, err := NewAttempt(statusFile, nil)
		require.NoError(t, err)

		// Create outputs file with metadata
		outputs := &ir.DAGRunOutputs{
			Metadata: ir.OutputsMetadata{
				DAGName:     "test-dag",
				DAGRunID:    "run-123",
				AttemptID:   "attempt-1",
				Status:      "succeeded",
				CompletedAt: "2024-12-28T15:30:45Z",
			},
			Outputs: map[string]string{
				"totalCount": "42",
				"resultFile": "/path/to/result.txt",
			},
		}
		err = att.WriteOutputs(ctx, outputs)
		require.NoError(t, err)

		// Read outputs
		readOutputs, err := att.ReadOutputs(ctx)
		require.NoError(t, err)
		require.NotNil(t, readOutputs)
		assert.Equal(t, outputs.Metadata, readOutputs.Metadata)
		assert.Equal(t, outputs.Outputs, readOutputs.Outputs)
	})

	t.Run("ReadNonExistentOutputs", func(t *testing.T) {
		dir := createTempDir(t)
		statusFile := filepath.Join(dir, "status.dat")
		att, err := NewAttempt(statusFile, nil)
		require.NoError(t, err)

		// Read outputs without creating file
		readOutputs, err := att.ReadOutputs(ctx)
		require.NoError(t, err)
		assert.Nil(t, readOutputs)
	})

	t.Run("ReadCorruptedOutputs", func(t *testing.T) {
		dir := createTempDir(t)
		statusFile := filepath.Join(dir, "status.dat")
		att, err := NewAttempt(statusFile, nil)
		require.NoError(t, err)

		// Create corrupted outputs file
		outputsFile := filepath.Join(dir, OutputsFile)
		err = os.WriteFile(outputsFile, []byte("not valid json"), 0600)
		require.NoError(t, err)

		// Read should return error
		_, err = att.ReadOutputs(ctx)
		assert.Error(t, err)
	})

	t.Run("ReadOutputsWithSpecialCharacters", func(t *testing.T) {
		dir := createTempDir(t)
		statusFile := filepath.Join(dir, "status.dat")
		att, err := NewAttempt(statusFile, nil)
		require.NoError(t, err)

		outputs := &ir.DAGRunOutputs{
			Metadata: ir.OutputsMetadata{DAGName: "test", DAGRunID: "run-123"},
			Outputs: map[string]string{
				"path":     "/path/with/slashes",
				"message":  "hello \"world\"",
				"unicode":  "日本語",
				"newlines": "line1\nline2",
			},
		}
		err = att.WriteOutputs(ctx, outputs)
		require.NoError(t, err)

		readOutputs, err := att.ReadOutputs(ctx)
		require.NoError(t, err)
		require.NotNil(t, readOutputs)
		assert.Equal(t, outputs.Outputs, readOutputs.Outputs)
	})
}

func TestAttempt_WriteStepMessages(t *testing.T) {
	ctx := context.Background()

	t.Run("WriteAndReadStepMessages", func(t *testing.T) {
		th := setupTestRepository(t)
		dag := th.DAG("test-messages")

		att, err := th.Repository.CreateAttempt(ctx, dag.DAG, time.Now(), "run-1", persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)

		messages := []ir.LLMMessage{
			{Role: ir.LLMRoleSystem, Content: "be helpful"},
			{Role: ir.LLMRoleUser, Content: "hello"},
			{Role: ir.LLMRoleAssistant, Content: "hi there"},
		}

		err = att.WriteStepMessages(ctx, "step1", messages)
		require.NoError(t, err)

		readMsgs, err := att.ReadStepMessages(ctx, "step1")
		require.NoError(t, err)
		require.NotNil(t, readMsgs)
		require.Len(t, readMsgs, 3)
		assert.Equal(t, ir.LLMRoleSystem, readMsgs[0].Role)
		assert.Equal(t, "be helpful", readMsgs[0].Content)
	})

	t.Run("WriteEmptyMessages", func(t *testing.T) {
		th := setupTestRepository(t)
		dag := th.DAG("test-empty-messages")

		att, err := th.Repository.CreateAttempt(ctx, dag.DAG, time.Now(), "run-1", persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)

		err = att.WriteStepMessages(ctx, "step1", []ir.LLMMessage{})
		require.NoError(t, err)

		// File should not exist for empty messages
		readMsgs, err := att.ReadStepMessages(ctx, "step1")
		require.NoError(t, err)
		assert.Nil(t, readMsgs)
	})

	t.Run("ReadNonExistentStepMessages", func(t *testing.T) {
		th := setupTestRepository(t)
		dag := th.DAG("test-nonexistent-messages")

		att, err := th.Repository.CreateAttempt(ctx, dag.DAG, time.Now(), "run-1", persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)

		readMsgs, err := att.ReadStepMessages(ctx, "nonexistent-step")
		require.NoError(t, err)
		assert.Nil(t, readMsgs)
	})

	t.Run("UpdateStepMessages", func(t *testing.T) {
		th := setupTestRepository(t)
		dag := th.DAG("test-update-messages")

		att, err := th.Repository.CreateAttempt(ctx, dag.DAG, time.Now(), "run-1", persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)

		// Write initial messages
		messages1 := []ir.LLMMessage{
			{Role: ir.LLMRoleUser, Content: "first"},
		}
		err = att.WriteStepMessages(ctx, "step1", messages1)
		require.NoError(t, err)

		// Update with more messages (overwrites)
		messages2 := []ir.LLMMessage{
			{Role: ir.LLMRoleUser, Content: "first"},
			{Role: ir.LLMRoleAssistant, Content: "response"},
		}
		err = att.WriteStepMessages(ctx, "step1", messages2)
		require.NoError(t, err)

		readMsgs, err := att.ReadStepMessages(ctx, "step1")
		require.NoError(t, err)
		require.NotNil(t, readMsgs)
		assert.Len(t, readMsgs, 2)
	})

	t.Run("MultipleSteps", func(t *testing.T) {
		th := setupTestRepository(t)
		dag := th.DAG("test-multiple-steps")

		att, err := th.Repository.CreateAttempt(ctx, dag.DAG, time.Now(), "run-1", persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)

		// Write messages for step1
		step1Msgs := []ir.LLMMessage{
			{Role: ir.LLMRoleUser, Content: "question 1"},
			{Role: ir.LLMRoleAssistant, Content: "answer 1"},
		}
		err = att.WriteStepMessages(ctx, "step1", step1Msgs)
		require.NoError(t, err)

		// Write messages for step2
		step2Msgs := []ir.LLMMessage{
			{Role: ir.LLMRoleUser, Content: "question 2"},
			{Role: ir.LLMRoleAssistant, Content: "answer 2"},
		}
		err = att.WriteStepMessages(ctx, "step2", step2Msgs)
		require.NoError(t, err)

		// Verify each step's messages are independent
		read1, err := att.ReadStepMessages(ctx, "step1")
		require.NoError(t, err)
		require.Len(t, read1, 2)
		assert.Equal(t, "question 1", read1[0].Content)

		read2, err := att.ReadStepMessages(ctx, "step2")
		require.NoError(t, err)
		require.Len(t, read2, 2)
		assert.Equal(t, "question 2", read2[0].Content)
	})

	// Step names are free text, so each step keeps its own messages even when
	// its name holds a character that a file name cannot.
	t.Run("StepNamesFileNamesCannotHold", func(t *testing.T) {
		th := setupTestRepository(t)
		dag := th.DAG("test-unsafe-step-names")

		att, err := th.Repository.CreateAttempt(ctx, dag.DAG, time.Now(), "run-1", persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)

		names := []string{"fetch/parse", "fetch", `fetch\parse`, "db:query", "why?", "nul"}
		for _, name := range names {
			require.NoError(t, att.WriteStepMessages(ctx, name, []ir.LLMMessage{{Role: ir.LLMRoleUser, Content: name}}))
		}
		for _, name := range names {
			msgs, err := att.ReadStepMessages(ctx, name)
			require.NoError(t, err)
			require.Len(t, msgs, 1, name)
			assert.Equal(t, name, msgs[0].Content)
		}
	})

	// Earlier versions wrote a step's messages under its raw name: one with
	// "%", a device name that Windows 11 accepts, or a "/" that still resolved
	// inside the messages directory.
	t.Run("ReadsMessagesUnderRawStepName", func(t *testing.T) {
		for _, tc := range []struct{ step, file string }{
			{step: "./fetch", file: "fetch.json"},
			{step: "50%", file: "50%.json"},
			{step: "con", file: "con.json"},
		} {
			th := setupTestRepository(t)
			dag := th.DAG("test-raw-step-name")
			att := th.CreateAttemptWithDAG(t, time.Now(), "run-1", ir.Running, dag.DAG)

			data, err := json.Marshal([]ir.LLMMessage{{Role: ir.LLMRoleUser, Content: "stored before"}})
			require.NoError(t, err)
			dir := filepath.Join(att.dagRunDir(), MessagesDir)
			require.NoError(t, os.MkdirAll(dir, 0o750))
			require.NoError(t, os.WriteFile(filepath.Join(dir, tc.file), data, 0o600))

			msgs, err := att.ReadStepMessages(ctx, tc.step)
			require.NoError(t, err)
			require.Len(t, msgs, 1, tc.step)
			assert.Equal(t, "stored before", msgs[0].Content)
		}
	})

	// A step never reads the messages of another step whose file its raw
	// name resolves to, such as fetch/parse for ./fetch%2Fparse, or b for
	// x/../b.
	t.Run("DoesNotReadAnotherStepsMessages", func(t *testing.T) {
		th := setupTestRepository(t)
		dag := th.DAG("test-alias-step-name")
		att, err := th.Repository.CreateAttempt(ctx, dag.DAG, time.Now(), "run-1", persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)

		require.NoError(t, att.WriteStepMessages(ctx, "fetch/parse", []ir.LLMMessage{{Role: ir.LLMRoleUser, Content: "parse"}}))
		require.NoError(t, att.WriteStepMessages(ctx, "b", []ir.LLMMessage{{Role: ir.LLMRoleUser, Content: "b"}}))
		for _, name := range []string{"./fetch%2Fparse", "x/../b"} {
			msgs, err := att.ReadStepMessages(ctx, name)
			require.NoError(t, err)
			assert.Nil(t, msgs, name)
		}
	})

	// Escaping can make a step name longer than a file name may be, so such a
	// step still keeps its messages, and messages that earlier versions stored
	// under its raw name are still read.
	t.Run("LongEscapedStepName", func(t *testing.T) {
		th := setupTestRepository(t)
		dag := th.DAG("test-long-step-name")
		att := th.CreateAttemptWithDAG(t, time.Now(), "run-1", ir.Running, dag.DAG)
		written := strings.Repeat("%", 200)
		stored := strings.Repeat("%", 100)

		require.NoError(t, att.WriteStepMessages(ctx, written, []ir.LLMMessage{{Role: ir.LLMRoleUser, Content: "written"}}))
		msgs, err := att.ReadStepMessages(ctx, written)
		require.NoError(t, err)
		require.Len(t, msgs, 1)
		assert.Equal(t, "written", msgs[0].Content)

		data, err := json.Marshal([]ir.LLMMessage{{Role: ir.LLMRoleUser, Content: "stored before"}})
		require.NoError(t, err)
		dir := filepath.Join(att.dagRunDir(), MessagesDir)
		require.NoError(t, os.WriteFile(filepath.Join(dir, stored+".json"), data, 0o600))
		msgs, err = att.ReadStepMessages(ctx, stored)
		require.NoError(t, err)
		require.Len(t, msgs, 1)
		assert.Equal(t, "stored before", msgs[0].Content)
	})

	// A step whose name is the shortened file name of another, longer step
	// keeps its own messages.
	t.Run("ShortenedNameKeepsStepsApart", func(t *testing.T) {
		th := setupTestRepository(t)
		dag := th.DAG("test-shortened-step-name")
		att, err := th.Repository.CreateAttempt(ctx, dag.DAG, time.Now(), "run-1", persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)

		long := strings.Repeat("a", 241)
		short := strings.TrimSuffix(stepMessagesName(long), ".json")
		require.NoError(t, att.WriteStepMessages(ctx, long, []ir.LLMMessage{{Role: ir.LLMRoleUser, Content: long}}))
		// A file system that ignores case finds the file under either case.
		for _, name := range []string{short, strings.ToUpper(short)} {
			msgs, err := att.ReadStepMessages(ctx, name)
			require.NoError(t, err)
			assert.Nil(t, msgs, "a step without messages reads none")
		}

		for _, name := range []string{long, short} {
			require.NoError(t, att.WriteStepMessages(ctx, name, []ir.LLMMessage{{Role: ir.LLMRoleUser, Content: name}}))
		}
		for _, name := range []string{long, short} {
			msgs, err := att.ReadStepMessages(ctx, name)
			require.NoError(t, err)
			require.Len(t, msgs, 1)
			assert.Equal(t, name, msgs[0].Content)
		}
	})

	t.Run("MessagesSharedAcrossRetryAttempts", func(t *testing.T) {
		th := setupTestRepository(t)
		dag := th.DAG("test-retry-messages")
		dagRunID := "retry-run-1"

		// First attempt writes messages
		att1, err := th.Repository.CreateAttempt(ctx, dag.DAG, time.Now(), dagRunID, persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)

		step1Msgs := []ir.LLMMessage{
			{Role: ir.LLMRoleUser, Content: "hello"},
			{Role: ir.LLMRoleAssistant, Content: "hi there"},
		}
		err = att1.WriteStepMessages(ctx, "step1", step1Msgs)
		require.NoError(t, err)

		// Second attempt (retry) should be able to read the same messages
		att2, err := th.Repository.CreateAttempt(ctx, dag.DAG, time.Now().Add(time.Second), dagRunID, persis.DAGRunCreateAttemptOptions{Retry: true})
		require.NoError(t, err)

		readMsgs, err := att2.ReadStepMessages(ctx, "step1")
		require.NoError(t, err)
		require.NotNil(t, readMsgs, "retry attempt should be able to read messages from first attempt")
		require.Len(t, readMsgs, 2)
		assert.Equal(t, "hello", readMsgs[0].Content)
		assert.Equal(t, "hi there", readMsgs[1].Content)

		// Retry attempt can also write new step messages
		step2Msgs := []ir.LLMMessage{
			{Role: ir.LLMRoleUser, Content: "follow up"},
			{Role: ir.LLMRoleAssistant, Content: "response"},
		}
		err = att2.WriteStepMessages(ctx, "step2", step2Msgs)
		require.NoError(t, err)

		// Both attempts should see step2 messages (they share the dag-run level storage)
		finalMsgs, err := att1.ReadStepMessages(ctx, "step2")
		require.NoError(t, err)
		require.NotNil(t, finalMsgs)
		assert.Len(t, finalMsgs, 2)
	})
}

func TestRepositoryAttempt_WriteEmitsLifecycleTransitionsAndStatusUpdates(t *testing.T) {
	t.Parallel()

	store := &captureEventStore{}
	fixture := setupEventTest(t, store)
	fixture.dag.Labels = ir.NewLabels([]string{"workspace=ops"})
	require.NoError(t, fixture.attempt.Open(fixture.ctx))
	t.Cleanup(func() { _ = fixture.attempt.Close(fixture.ctx) })

	queued := createTestStatus(ir.Queued)
	queued.AttemptID = "attempt-1"
	queued.QueuedAt = time.Now().UTC().Format(time.RFC3339)
	queued.Labels = fixture.dag.Labels.Strings()
	require.NoError(t, fixture.attempt.Write(fixture.ctx, queued))
	require.NoError(t, fixture.attempt.Write(fixture.ctx, queued))

	running := queued
	running.Status = ir.Running
	running.StartedAt = time.Now().UTC().Format(time.RFC3339)
	require.NoError(t, fixture.attempt.Write(fixture.ctx, running))
	require.NoError(t, fixture.attempt.Write(fixture.ctx, running))

	succeeded := running
	succeeded.Status = ir.Succeeded
	succeeded.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	require.NoError(t, fixture.attempt.Write(fixture.ctx, succeeded))
	require.NoError(t, fixture.attempt.Write(fixture.ctx, succeeded))

	require.Len(t, store.events, 6)
	assert.Equal(t, []eventstore.EventType{
		eventstore.TypeDAGRunQueued,
		eventstore.TypeDAGRunUpdated,
		eventstore.TypeDAGRunRunning,
		eventstore.TypeDAGRunUpdated,
		eventstore.TypeDAGRunSucceeded,
		eventstore.TypeDAGRunUpdated,
	}, captureEventTypes(store.events))

	snapshot, err := eventstore.DAGRunSnapshotFromEvent(store.events[0])
	require.NoError(t, err)
	assert.Equal(t, "test-dag", snapshot.DAGFile)
	assert.Equal(t, []string{"workspace=ops"}, snapshot.Labels)
	assert.Equal(t, ir.Queued, snapshot.Status)
}

func TestRepositoryAttempt_OpenRestoresLastEmittedLifecycleState(t *testing.T) {
	t.Parallel()

	store := &captureEventStore{}
	fixture := setupEventTest(t, store)
	require.NoError(t, fixture.attempt.Open(fixture.ctx))

	queued := createTestStatus(ir.Queued)
	queued.AttemptID = "attempt-1"
	queued.QueuedAt = time.Now().UTC().Format(time.RFC3339)
	require.NoError(t, fixture.attempt.Write(fixture.ctx, queued))
	require.NoError(t, fixture.attempt.Close(fixture.ctx))
	require.Len(t, store.events, 1)

	reopened, err := fixture.repository.FindAttempt(fixture.ctx, ir.NewDAGRunRef(fixture.dag.Name, "test"))
	require.NoError(t, err)
	require.NoError(t, reopened.Open(fixture.ctx))
	t.Cleanup(func() { _ = reopened.Close(fixture.ctx) })
	require.NoError(t, reopened.Write(fixture.ctx, queued))

	running := queued
	running.Status = ir.Running
	running.StartedAt = time.Now().UTC().Format(time.RFC3339)
	require.NoError(t, reopened.Write(fixture.ctx, running))

	require.Len(t, store.events, 3)
	assert.Equal(t, []eventstore.EventType{
		eventstore.TypeDAGRunQueued,
		eventstore.TypeDAGRunUpdated,
		eventstore.TypeDAGRunRunning,
	}, captureEventTypes(store.events))
	snapshot, err := eventstore.DAGRunSnapshotFromEvent(store.events[2])
	require.NoError(t, err)
	assert.Equal(t, "test-dag", snapshot.DAGFile)
}

func TestRepositoryAttempt_EmitsOnlyAfterSuccessfulPersistence(t *testing.T) {
	t.Parallel()

	store := &captureEventStore{}
	fixture := setupEventTest(t, store)
	require.NoError(t, fixture.attempt.Open(fixture.ctx))
	require.NoError(t, fixture.attempt.Close(fixture.ctx))

	status := createTestStatus(ir.Running)
	status.AttemptID = fixture.attempt.ID()
	require.Error(t, fixture.attempt.Write(fixture.ctx, status))
	assert.Empty(t, store.events)
}

func TestRepositoryAttempt_EventFailureDoesNotFailPersistedWrite(t *testing.T) {
	t.Parallel()

	store := &captureEventStore{emitErr: errors.New("event unavailable")}
	fixture := setupEventTest(t, store)
	require.NoError(t, fixture.attempt.Open(fixture.ctx))
	t.Cleanup(func() { _ = fixture.attempt.Close(fixture.ctx) })

	status := createTestStatus(ir.Running)
	status.AttemptID = fixture.attempt.ID()
	require.NoError(t, fixture.attempt.Write(fixture.ctx, status))
	persisted, err := fixture.attempt.ReadStatus(fixture.ctx)
	require.NoError(t, err)
	assert.Equal(t, ir.Running, persisted.Status)
}

func TestRepositoryCompareAndSwapEmitsPersistedTransition(t *testing.T) {
	t.Parallel()

	store := &captureEventStore{}
	fixture := setupEventTest(t, store)
	require.NoError(t, fixture.attempt.Open(context.Background()))

	queued := createTestStatus(ir.Queued)
	queued.AttemptID = fixture.attempt.ID()
	require.NoError(t, fixture.attempt.Write(context.Background(), queued))
	require.NoError(t, fixture.attempt.Close(context.Background()))

	ref := ir.NewDAGRunRef(fixture.dag.Name, queued.DAGRunID)
	updated, swapped, err := fixture.repository.CompareAndSwapLatestAttemptStatus(
		fixture.ctx,
		ref,
		fixture.attempt.ID(),
		ir.Queued,
		func(status *ir.DAGRunStatus) error {
			status.Status = ir.Running
			return nil
		},
		persis.DAGRunCompareAndSwapOptions{},
	)
	require.NoError(t, err)
	require.True(t, swapped)
	require.NotNil(t, updated)
	require.Len(t, store.events, 1)
	assert.Equal(t, eventstore.TypeDAGRunRunning, store.events[0].Type)

	snapshot, err := eventstore.DAGRunSnapshotFromEvent(store.events[0])
	require.NoError(t, err)
	assert.Equal(t, "test-dag", snapshot.DAGFile)
	assert.Equal(t, ir.Running, snapshot.Status)

	_, swapped, err = fixture.repository.CompareAndSwapLatestAttemptStatus(
		fixture.ctx,
		ref,
		fixture.attempt.ID(),
		ir.Queued,
		func(status *ir.DAGRunStatus) error {
			status.Status = ir.Failed
			return nil
		},
		persis.DAGRunCompareAndSwapOptions{},
	)
	require.NoError(t, err)
	assert.False(t, swapped)
	assert.Len(t, store.events, 1)
}

type eventTest struct {
	ctx        context.Context
	repository *persis.DAGRunRepository
	dag        *ir.DAG
	attempt    dagrun.Attempt
}

func setupEventTest(t *testing.T, store *captureEventStore) eventTest {
	t.Helper()

	dir := t.TempDir()
	ctx := eventstore.WithContext(
		context.Background(),
		eventstore.New(store),
		eventstore.Source{Service: eventstore.SourceServiceServer},
	)
	dag := &ir.DAG{Name: "TestDAG", Location: filepath.Join(dir, "test-dag.yaml")}
	repository := persis.NewDAGRunRepository(NewStore(dir), nil, persis.DAGRunRepositoryOptions{})
	att, err := repository.CreateAttempt(ctx, dag, time.Now(), "test", persis.DAGRunCreateAttemptOptions{
		AttemptID: "attempt-1",
	})
	require.NoError(t, err)
	return eventTest{ctx: ctx, repository: repository, dag: dag, attempt: att}
}

type captureEventStore struct {
	events  []*eventstore.Event
	emitErr error
}

func (c *captureEventStore) Emit(_ context.Context, event *eventstore.Event) error {
	c.events = append(c.events, event)
	return c.emitErr
}

func (*captureEventStore) Query(context.Context, eventstore.QueryFilter) (*eventstore.QueryResult, error) {
	return nil, nil
}

func captureEventTypes(events []*eventstore.Event) []eventstore.EventType {
	types := make([]eventstore.EventType, 0, len(events))
	for _, event := range events {
		if event == nil {
			continue
		}
		types = append(types, event.Type)
	}
	return types
}
