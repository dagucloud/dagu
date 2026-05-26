// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/eventstore"
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

func TestAttempt_Write(t *testing.T) {
	dir := createTempDir(t)
	file := filepath.Join(dir, "status.dat")

	att, err := NewAttempt(file, nil)
	require.NoError(t, err)

	// Test write without open
	testStatus := createTestStatus(core.Running)
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
	assert.Equal(t, core.Running, actual.Status)

	// Close
	err = att.Close(context.Background())
	assert.NoError(t, err)
}

func TestAttempt_Read(t *testing.T) {
	dir := createTempDir(t)
	file := filepath.Join(dir, "status.dat")

	// Create test file with multiple status entries
	status1 := createTestStatus(core.Running)
	status2 := createTestStatus(core.Succeeded)

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
	assert.Equal(t, core.Succeeded.String(), dagRunStatus.Status.String())

	// Read using ReadStatus
	latestStatus, err := att.ReadStatus(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, core.Succeeded.String(), latestStatus.Status.String())
}

func TestAttempt_ReadStatusHonorsCanceledContext(t *testing.T) {
	dir := createTempDir(t)
	file := filepath.Join(dir, "status.dat")

	writeJSONToFile(t, file, createTestStatus(core.Running))

	att, err := NewAttempt(file, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = att.ReadStatus(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestAttempt_Compact(t *testing.T) {
	dir := createTempDir(t)
	file := filepath.Join(dir, "status.dat")

	// Create test file with multiple status entries
	for i := range 10 {
		testStatus := createTestStatus(core.Running)

		if i == 9 {
			// Make some status changes to create different attempts
			testStatus.Status = core.Succeeded
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
	assert.Equal(t, core.Succeeded, dagRunStatus.Status)
}

func TestAttempt_CompactReopensWriter(t *testing.T) {
	dir := createTempDir(t)
	file := filepath.Join(dir, "status.dat")

	att, err := NewAttempt(file, nil)
	require.NoError(t, err)
	require.NoError(t, att.Open(context.Background()))

	first := createTestStatus(core.Running)
	require.NoError(t, att.Write(context.Background(), first))
	require.NotNil(t, att.writer)
	assert.True(t, att.writer.IsOpen())

	require.NoError(t, att.Compact(context.Background()))
	require.NotNil(t, att.writer)
	assert.True(t, att.writer.IsOpen())

	final := createTestStatus(core.Succeeded)
	require.NoError(t, att.Write(context.Background(), final))

	status, err := att.ReadStatus(context.Background())
	require.NoError(t, err)
	assert.Equal(t, core.Succeeded, status.Status)

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
	err = att.Write(context.Background(), createTestStatus(core.Running))
	require.NoError(t, err)

	// Close
	err = att.Close(context.Background())
	assert.NoError(t, err)

	// Verify we can't write after close
	err = att.Write(context.Background(), createTestStatus(core.Succeeded))
	assert.ErrorIs(t, err, ErrStatusFileNotOpen)

	// Test double close is safe
	err = att.Close(context.Background())
	assert.NoError(t, err)
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
	err = att.Write(context.Background(), createTestStatus(core.Succeeded))
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

	// Reading an empty file should return ErrCorruptedStatusFile
	_, err = att.ReadStatus(context.Background())
	assert.ErrorIs(t, err, exec.ErrCorruptedStatusFile)

	// Compacting an empty file should be safe
	err = att.Compact(context.Background())
	assert.NoError(t, err)
}

func TestAttempt_InvalidJSON(t *testing.T) {
	dir := createTempDir(t)
	file := filepath.Join(dir, "invalid.dat")

	// Create a file with valid JSOn
	validStatus := createTestStatus(core.Running)
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
	assert.Equal(t, core.Running.String(), dagRunStatus.Status.String())
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

		// Should return ErrCorruptedStatusFile
		_, err = att.ReadStatus(context.Background())
		assert.ErrorIs(t, err, exec.ErrCorruptedStatusFile)
	})

	t.Run("OnlyWhitespace", func(t *testing.T) {
		dir := createTempDir(t)
		file := filepath.Join(dir, "whitespace.jsonl")

		// Create file with only whitespace
		err := os.WriteFile(file, []byte("\n\n\n"), 0600)
		require.NoError(t, err)

		att, err := NewAttempt(file, nil)
		require.NoError(t, err)

		// Should return ErrCorruptedStatusFile
		_, err = att.ReadStatus(context.Background())
		assert.ErrorIs(t, err, exec.ErrCorruptedStatusFile)
	})

	t.Run("NoValidJSON", func(t *testing.T) {
		dir := createTempDir(t)
		file := filepath.Join(dir, "novalid.jsonl")

		// Create file with only invalid JSON
		err := os.WriteFile(file, []byte("not json\nstill not json\n"), 0600)
		require.NoError(t, err)

		att, err := NewAttempt(file, nil)
		require.NoError(t, err)

		// Should return ErrCorruptedStatusFile
		_, err = att.ReadStatus(context.Background())
		assert.ErrorIs(t, err, exec.ErrCorruptedStatusFile)
	})
}

func TestReadLineFrom(t *testing.T) {
	dir := createTempDir(t)
	file := filepath.Join(dir, "lines.txt")

	// Create a test file with multiple lines
	content := "line1\nline2\nline3\n"
	err := os.WriteFile(file, []byte(content), 0600)
	require.NoError(t, err)

	f, err := os.Open(file)
	require.NoError(t, err)
	defer func() {
		_ = f.Close()
	}()

	// Read first line
	line1, offset, err := readLineFrom(f, 0)
	assert.NoError(t, err)
	assert.Equal(t, "line1", string(line1))
	assert.Equal(t, int64(6), offset) // "line1\n" = 6 bytes

	// Read second line
	line2, offset, err := readLineFrom(f, offset)
	assert.NoError(t, err)
	assert.Equal(t, "line2", string(line2))
	assert.Equal(t, int64(12), offset) // offset 6 + "line2\n" = 12 bytes

	// Read third line
	line3, offset, err := readLineFrom(f, offset)
	assert.NoError(t, err)
	assert.Equal(t, "line3", string(line3))
	assert.Equal(t, int64(18), offset) // offset 12 + "line3\n" = 18 bytes

	// Try to read beyond EOF
	_, _, err = readLineFrom(f, offset)
	assert.ErrorIs(t, err, io.EOF)
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

	dir, err := os.MkdirTemp("", "attempt_"+formatAttemptTimestamp(exec.NewUTC(time.Now()))+"_"+attemptID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})

	return dir
}

// createTestDAG creates a sample DAG for testing
func createTestDAG() *core.DAG {
	return &core.DAG{
		Name: "TestDAG",
		Steps: []core.Step{
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
		HandlerOn: core.HandlerOn{
			Success: &core.Step{
				Name:    "on_success",
				Command: "echo 'success'",
			},
			Failure: &core.Step{
				Name:    "on_failure",
				Command: "echo 'failure'",
			},
		},
		Params: []string{"--param1=value1", "--param2=value2"},
	}
}

// createTestStatus creates a sample status for testing using StatusFactory
func createTestStatus(st core.Status) exec.DAGRunStatus {
	dag := createTestDAG()

	return exec.DAGRunStatus{
		Name:      dag.Name,
		DAGRunID:  "test",
		Status:    st,
		PID:       exec.PID(12345),
		StartedAt: stringutil.FormatTime(time.Now()),
		Nodes:     exec.NewNodesFromSteps(dag.Steps),
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

		outputs := &exec.DAGRunOutputs{
			Metadata: exec.OutputsMetadata{
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

		var readOutputs exec.DAGRunOutputs
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

		outputs := &exec.DAGRunOutputs{
			Metadata: exec.OutputsMetadata{},
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
		outputs1 := &exec.DAGRunOutputs{
			Metadata: exec.OutputsMetadata{DAGName: "dag1", DAGRunID: "run-1"},
			Outputs:  map[string]string{"key1": "value1"},
		}
		err = att.WriteOutputs(ctx, outputs1)
		require.NoError(t, err)

		// Write second outputs (overwrites first)
		outputs2 := &exec.DAGRunOutputs{
			Metadata: exec.OutputsMetadata{DAGName: "dag2", DAGRunID: "run-2"},
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
		outputs := &exec.DAGRunOutputs{
			Metadata: exec.OutputsMetadata{
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

		outputs := &exec.DAGRunOutputs{
			Metadata: exec.OutputsMetadata{DAGName: "test", DAGRunID: "run-123"},
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
		th := setupTestStore(t)
		dag := th.DAG("test-messages")

		att, err := th.Store.CreateAttempt(ctx, dag.DAG, time.Now(), "run-1", exec.NewDAGRunAttemptOptions{})
		require.NoError(t, err)

		messages := []exec.LLMMessage{
			{Role: exec.RoleSystem, Content: "be helpful"},
			{Role: exec.RoleUser, Content: "hello"},
			{Role: exec.RoleAssistant, Content: "hi there"},
		}

		err = att.WriteStepMessages(ctx, "step1", messages)
		require.NoError(t, err)

		readMsgs, err := att.ReadStepMessages(ctx, "step1")
		require.NoError(t, err)
		require.NotNil(t, readMsgs)
		require.Len(t, readMsgs, 3)
		assert.Equal(t, exec.RoleSystem, readMsgs[0].Role)
		assert.Equal(t, "be helpful", readMsgs[0].Content)
	})

	t.Run("WriteEmptyMessages", func(t *testing.T) {
		th := setupTestStore(t)
		dag := th.DAG("test-empty-messages")

		att, err := th.Store.CreateAttempt(ctx, dag.DAG, time.Now(), "run-1", exec.NewDAGRunAttemptOptions{})
		require.NoError(t, err)

		err = att.WriteStepMessages(ctx, "step1", []exec.LLMMessage{})
		require.NoError(t, err)

		// File should not exist for empty messages
		readMsgs, err := att.ReadStepMessages(ctx, "step1")
		require.NoError(t, err)
		assert.Nil(t, readMsgs)
	})

	t.Run("ReadNonExistentStepMessages", func(t *testing.T) {
		th := setupTestStore(t)
		dag := th.DAG("test-nonexistent-messages")

		att, err := th.Store.CreateAttempt(ctx, dag.DAG, time.Now(), "run-1", exec.NewDAGRunAttemptOptions{})
		require.NoError(t, err)

		readMsgs, err := att.ReadStepMessages(ctx, "nonexistent-step")
		require.NoError(t, err)
		assert.Nil(t, readMsgs)
	})

	t.Run("UpdateStepMessages", func(t *testing.T) {
		th := setupTestStore(t)
		dag := th.DAG("test-update-messages")

		att, err := th.Store.CreateAttempt(ctx, dag.DAG, time.Now(), "run-1", exec.NewDAGRunAttemptOptions{})
		require.NoError(t, err)

		// Write initial messages
		messages1 := []exec.LLMMessage{
			{Role: exec.RoleUser, Content: "first"},
		}
		err = att.WriteStepMessages(ctx, "step1", messages1)
		require.NoError(t, err)

		// Update with more messages (overwrites)
		messages2 := []exec.LLMMessage{
			{Role: exec.RoleUser, Content: "first"},
			{Role: exec.RoleAssistant, Content: "response"},
		}
		err = att.WriteStepMessages(ctx, "step1", messages2)
		require.NoError(t, err)

		readMsgs, err := att.ReadStepMessages(ctx, "step1")
		require.NoError(t, err)
		require.NotNil(t, readMsgs)
		assert.Len(t, readMsgs, 2)
	})

	t.Run("MultipleSteps", func(t *testing.T) {
		th := setupTestStore(t)
		dag := th.DAG("test-multiple-steps")

		att, err := th.Store.CreateAttempt(ctx, dag.DAG, time.Now(), "run-1", exec.NewDAGRunAttemptOptions{})
		require.NoError(t, err)

		// Write messages for step1
		step1Msgs := []exec.LLMMessage{
			{Role: exec.RoleUser, Content: "question 1"},
			{Role: exec.RoleAssistant, Content: "answer 1"},
		}
		err = att.WriteStepMessages(ctx, "step1", step1Msgs)
		require.NoError(t, err)

		// Write messages for step2
		step2Msgs := []exec.LLMMessage{
			{Role: exec.RoleUser, Content: "question 2"},
			{Role: exec.RoleAssistant, Content: "answer 2"},
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

	t.Run("MessagesSharedAcrossRetryAttempts", func(t *testing.T) {
		th := setupTestStore(t)
		dag := th.DAG("test-retry-messages")
		dagRunID := "retry-run-1"

		// First attempt writes messages
		att1, err := th.Store.CreateAttempt(ctx, dag.DAG, time.Now(), dagRunID, exec.NewDAGRunAttemptOptions{})
		require.NoError(t, err)

		step1Msgs := []exec.LLMMessage{
			{Role: exec.RoleUser, Content: "hello"},
			{Role: exec.RoleAssistant, Content: "hi there"},
		}
		err = att1.WriteStepMessages(ctx, "step1", step1Msgs)
		require.NoError(t, err)

		// Second attempt (retry) should be able to read the same messages
		att2, err := th.Store.CreateAttempt(ctx, dag.DAG, time.Now().Add(time.Second), dagRunID, exec.NewDAGRunAttemptOptions{Retry: true})
		require.NoError(t, err)

		readMsgs, err := att2.ReadStepMessages(ctx, "step1")
		require.NoError(t, err)
		require.NotNil(t, readMsgs, "retry attempt should be able to read messages from first attempt")
		require.Len(t, readMsgs, 2)
		assert.Equal(t, "hello", readMsgs[0].Content)
		assert.Equal(t, "hi there", readMsgs[1].Content)

		// Retry attempt can also write new step messages
		step2Msgs := []exec.LLMMessage{
			{Role: exec.RoleUser, Content: "follow up"},
			{Role: exec.RoleAssistant, Content: "response"},
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

func TestAttempt_WorkDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dagRunDir := filepath.Join(dir, "dag-run_20250101_000000Z_abc")
	attemptDir := filepath.Join(dagRunDir, "attempt_20250101_000000_000Z_xyz")
	require.NoError(t, os.MkdirAll(attemptDir, 0750))
	statusFile := filepath.Join(attemptDir, "status.jsonl")
	require.NoError(t, os.WriteFile(statusFile, nil, 0600))
	att, err := NewAttempt(statusFile, nil)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dagRunDir, "work"), att.WorkDir())
}

func TestAttempt_WriteEmitsLifecycleTransitionsAndStatusUpdates(t *testing.T) {
	t.Parallel()

	dir := createTempDir(t)
	file := filepath.Join(dir, "status.dat")
	store := &captureEventStore{}
	service := eventstore.New(store)
	ctx := eventstore.WithContext(context.Background(), service, eventstore.Source{Service: eventstore.SourceServiceServer})

	dag := &core.DAG{Name: "TestDAG", Location: filepath.Join(dir, "test-dag.yaml")}
	att, err := NewAttempt(file, nil, WithDAG(dag))
	require.NoError(t, err)
	require.NoError(t, att.Open(ctx))

	queued := createTestStatus(core.Queued)
	queued.AttemptID = "attempt-1"
	queued.QueuedAt = time.Now().UTC().Format(time.RFC3339)
	require.NoError(t, att.Write(ctx, queued))
	require.NoError(t, att.Write(ctx, queued))

	running := queued
	running.Status = core.Running
	running.StartedAt = time.Now().UTC().Format(time.RFC3339)
	require.NoError(t, att.Write(ctx, running))
	require.NoError(t, att.Write(ctx, running))

	succeeded := running
	succeeded.Status = core.Succeeded
	succeeded.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	require.NoError(t, att.Write(ctx, succeeded))
	require.NoError(t, att.Write(ctx, succeeded))

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
	assert.Equal(t, core.Queued, snapshot.Status)
}

func TestAttempt_OpenRestoresLastEmittedLifecycleState(t *testing.T) {
	t.Parallel()

	dir := createTempDir(t)
	file := filepath.Join(dir, "status.dat")
	store := &captureEventStore{}
	service := eventstore.New(store)
	ctx := eventstore.WithContext(context.Background(), service, eventstore.Source{Service: eventstore.SourceServiceServer})

	dag := &core.DAG{Name: "TestDAG", Location: filepath.Join(dir, "test-dag.yaml")}
	att, err := NewAttempt(file, nil, WithDAG(dag))
	require.NoError(t, err)
	require.NoError(t, att.Open(ctx))

	queued := createTestStatus(core.Queued)
	queued.AttemptID = "attempt-1"
	queued.QueuedAt = time.Now().UTC().Format(time.RFC3339)
	require.NoError(t, att.Write(ctx, queued))
	require.NoError(t, att.Close(ctx))
	require.Len(t, store.events, 1)

	reopened, err := NewAttempt(file, nil, WithDAG(dag))
	require.NoError(t, err)
	require.NoError(t, reopened.Open(ctx))
	require.NoError(t, reopened.Write(ctx, queued))

	running := queued
	running.Status = core.Running
	running.StartedAt = time.Now().UTC().Format(time.RFC3339)
	require.NoError(t, reopened.Write(ctx, running))

	require.Len(t, store.events, 3)
	assert.Equal(t, []eventstore.EventType{
		eventstore.TypeDAGRunQueued,
		eventstore.TypeDAGRunUpdated,
		eventstore.TypeDAGRunRunning,
	}, captureEventTypes(store.events))
}

type captureEventStore struct {
	events []*eventstore.Event
}

func (c *captureEventStore) Emit(_ context.Context, event *eventstore.Event) error {
	c.events = append(c.events, event)
	return nil
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
