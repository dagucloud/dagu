// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	executorpkg "github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func outputCommandTimeout() time.Duration {
	return 5 * time.Second
}

func outputDeadlockTimeout() time.Duration {
	return 5 * time.Second
}

func outputCommandCleanupWait() time.Duration {
	return 2 * time.Second
}

func startNodeExecuteAsync(t *testing.T, node *Node, ctx context.Context) <-chan error {
	t.Helper()

	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		done <- node.Execute(ctx)
	}()

	t.Cleanup(func() {
		node.Cancel()
		select {
		case <-finished:
		case <-time.After(outputCommandCleanupWait()):
		}
		_ = node.Teardown()
	})

	return done
}

type outputTestExecutor struct {
	stdout io.Writer
	stderr io.Writer
	run    func(context.Context, *outputTestExecutor) error
}

func (e *outputTestExecutor) SetStdout(out io.Writer) {
	e.stdout = out
}

func (e *outputTestExecutor) SetStderr(out io.Writer) {
	e.stderr = out
}

func (e *outputTestExecutor) Kill(_ os.Signal) error {
	return nil
}

func (e *outputTestExecutor) Run(ctx context.Context) error {
	if e.run != nil {
		return e.run(ctx, e)
	}
	return nil
}

var outputTestExecutorSeq atomic.Int64

func registerOutputTestExecutor(t *testing.T, run func(context.Context, *outputTestExecutor) error) string {
	t.Helper()

	executorType := fmt.Sprintf("__output_test_%d", outputTestExecutorSeq.Add(1))
	executorpkg.RegisterExecutor(executorType, func(context.Context, core.Step) (executorpkg.Executor, error) {
		return &outputTestExecutor{run: run}, nil
	}, nil, core.ExecutorCapabilities{})
	t.Cleanup(func() {
		executorpkg.UnregisterExecutor(executorType)
	})
	return executorType
}

func writeRepeatedX(ctx context.Context, w io.Writer, size int) error {
	if size <= 0 {
		return nil
	}

	chunk := strings.Repeat("x", 8*1024)
	remaining := size
	for remaining > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n := min(remaining, len(chunk))
		if _, err := io.WriteString(w, chunk[:n]); err != nil {
			return err
		}
		remaining -= n
	}
	return nil
}

func writeProcessLikeOutput(ctx context.Context, w io.Writer, processes, bytesPerProcess int) error {
	for i := 1; i <= processes; i++ {
		if _, err := fmt.Fprintf(w, "Process %d: ", i); err != nil {
			return err
		}
		if err := writeRepeatedX(ctx, w, bytesPerProcess); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
	}
	return nil
}

func TestNode_LargeOutput(t *testing.T) {
	tests := []struct {
		name       string
		outputSize int
		expectHang bool
	}{
		{
			name:       "SmallOutput",
			outputSize: 1024, // 1KB
			expectHang: false,
		},
		{
			name:       "MediumOutput",
			outputSize: 32 * 1024, // 32KB
			expectHang: false,
		},
		{
			name:       "LargeOutputJustBelow64KB",
			outputSize: 63 * 1024, // 63KB
			expectHang: false,
		},
		{
			name:       "LargeOutputAt64KB",
			outputSize: 64 * 1024, // 64KB
			expectHang: false,     // Fixed - no longer hangs
		},
		{
			name:       "LargeOutputAbove64KB",
			outputSize: 128 * 1024, // 128KB
			expectHang: false,      // Fixed - no longer hangs
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executorType := registerOutputTestExecutor(t, func(ctx context.Context, exec *outputTestExecutor) error {
				return writeRepeatedX(ctx, exec.stdout, tt.outputSize)
			})
			step := core.Step{
				Name:           "test",
				Output:         "RESULT",
				ExecutorConfig: core.ExecutorConfig{Type: executorType},
			}

			node := NewNode(step, NodeState{})
			ctx := context.Background()
			// Set up environment context with proper DAG
			dag := &core.DAG{Name: "test"}
			ctx = NewContext(ctx, dag, "test-run", "test.log")

			// Setup node with a temporary directory
			tmpDir := t.TempDir()
			err := node.Prepare(ctx, tmpDir, "test-run")
			require.NoError(t, err)

			// Execute with timeout to detect hanging
			done := startNodeExecuteAsync(t, node, ctx)

			select {
			case err := <-done:
				if tt.expectHang {
					t.Errorf("Expected hanging but command completed: %v", err)
				}
				// Verify output was captured correctly
				if !tt.expectHang && err == nil {
					// Access the output variable through NodeData
					nodeData := node.NodeData()
					if nodeData.State.OutputVariables != nil {
						if v, ok := nodeData.State.OutputVariables.Load("RESULT"); ok {
							output := v.(string)
							// Extract the value part after the = sign
							if idx := strings.Index(output, "="); idx != -1 {
								output = output[idx+1:]
							}
							assert.NotEmpty(t, output, "output should be captured")
							assert.True(t, strings.HasPrefix(output, "xxx"), "output should start with x's")
						} else {
							t.Error("RESULT variable not found in OutputVariables")
						}
					} else {
						t.Error("OutputVariables is nil")
					}
				}
			case <-time.After(outputCommandTimeout()):
				if !tt.expectHang {
					t.Errorf("Command hung unexpectedly for output size %d bytes", tt.outputSize)
				}
			}
		})
	}
}

func TestNode_OutputCaptureDeadlock(t *testing.T) {
	executorType := registerOutputTestExecutor(t, func(ctx context.Context, exec *outputTestExecutor) error {
		return writeRepeatedX(ctx, exec.stdout, 64*1024+1)
	})

	// Test specifically for the pipe deadlock issue
	step := core.Step{
		Name:           "deadlock-test",
		Output:         "RESULT",
		ExecutorConfig: core.ExecutorConfig{Type: executorType},
	}

	node := NewNode(step, NodeState{})
	ctx := context.Background()
	// Set up environment context with proper DAG
	dag := &core.DAG{Name: "test"}
	ctx = NewContext(ctx, dag, "deadlock-test", "test.log")

	tmpDir := t.TempDir()
	err := node.Prepare(ctx, tmpDir, "deadlock-test")
	require.NoError(t, err)

	// This should complete without hanging
	done := startNodeExecuteAsync(t, node, ctx)

	select {
	case err := <-done:
		assert.NoError(t, err, "command should complete successfully")
		// Access the output variable through NodeData
		nodeData := node.NodeData()
		require.NotNil(t, nodeData.State.OutputVariables, "OutputVariables should not be nil")
		v, ok := nodeData.State.OutputVariables.Load("RESULT")
		require.True(t, ok, "RESULT variable should be present")
		output := v.(string)
		// Extract the value part after the = sign
		if idx := strings.Index(output, "="); idx != -1 {
			output = output[idx+1:]
		}
		assert.Len(t, output, 64*1024+1, "output should be exactly 64KB + 1 byte")
	case <-time.After(outputDeadlockTimeout()):
		t.Fatal("Command execution hung - possible deadlock detected")
	}
}

func TestNode_OutputExceedsLimit(t *testing.T) {
	executorType := registerOutputTestExecutor(t, func(ctx context.Context, exec *outputTestExecutor) error {
		return writeRepeatedX(ctx, exec.stdout, 2*1024*1024)
	})

	// Test that output exceeding the limit returns an error
	step := core.Step{
		Name:           "exceed-limit-test",
		Output:         "RESULT",
		ExecutorConfig: core.ExecutorConfig{Type: executorType},
	}

	node := NewNode(step, NodeState{})
	ctx := context.Background()
	// Set up environment context with proper DAG
	dag := &core.DAG{Name: "test"}
	ctx = NewContext(ctx, dag, "exceed-limit-test", "test.log")

	tmpDir := t.TempDir()
	err := node.Prepare(ctx, tmpDir, "exceed-limit-test")
	require.NoError(t, err)

	// Execute should fail with output limit error
	err = node.Execute(ctx)
	if err != nil {
		t.Logf("Error: %v", err)
	}
	assert.Error(t, err, "should return error when output exceeds limit")
	assert.Contains(t, err.Error(), "output exceeded maximum size limit", "error should mention output size limit")

	_ = node.Teardown()
}

func TestNode_CustomOutputLimit(t *testing.T) {
	executorType := registerOutputTestExecutor(t, func(ctx context.Context, exec *outputTestExecutor) error {
		return writeRepeatedX(ctx, exec.stdout, 100*1024)
	})

	// Test with custom output limit
	step := core.Step{
		Name:           "custom-limit-test",
		Output:         "RESULT",
		ExecutorConfig: core.ExecutorConfig{Type: executorType},
	}

	node := NewNode(step, NodeState{})
	ctx := context.Background()
	// Set up environment context with custom limit of 50KB
	dag := &core.DAG{
		Name:          "test",
		MaxOutputSize: 50 * 1024, // 50KB limit
	}
	ctx = NewContext(ctx, dag, "custom-limit-test", "test.log")

	tmpDir := t.TempDir()
	err := node.Prepare(ctx, tmpDir, "custom-limit-test")
	require.NoError(t, err)

	// Execute should fail with output limit error
	err = node.Execute(ctx)
	if err != nil {
		t.Logf("Error with custom limit: %v", err)
	}
	assert.Error(t, err, "should return error when output exceeds custom limit")
	assert.Contains(t, err.Error(), "output exceeded maximum size limit", "error should mention output size limit")

	_ = node.Teardown()
}

func TestNode_ConcurrentOutputCapture(t *testing.T) {
	executorType := registerOutputTestExecutor(t, func(ctx context.Context, exec *outputTestExecutor) error {
		return writeProcessLikeOutput(ctx, exec.stdout, 10, 10000)
	})

	// Test that output capture doesn't interfere with concurrent writes
	step := core.Step{
		Name:           "concurrent-test",
		ExecutorConfig: core.ExecutorConfig{Type: executorType},
		Output:         "RESULT",
	}

	node := NewNode(step, NodeState{})
	ctx := context.Background()
	// Set up environment context with proper DAG
	dag := &core.DAG{Name: "test"}
	ctx = NewContext(ctx, dag, "concurrent-test", "test.log")

	tmpDir := t.TempDir()
	err := node.Prepare(ctx, tmpDir, "concurrent-test")
	require.NoError(t, err)

	err = node.Execute(ctx)
	assert.NoError(t, err, "concurrent output should be handled correctly")

	// Access the output variable through NodeData
	nodeData := node.NodeData()
	require.NotNil(t, nodeData.State.OutputVariables, "OutputVariables should not be nil")
	v, ok := nodeData.State.OutputVariables.Load("RESULT")
	require.True(t, ok, "RESULT variable should be present")
	output := v.(string)
	// Extract the value part after the = sign
	if idx := strings.Index(output, "="); idx != -1 {
		output = output[idx+1:]
	}
	assert.NotEmpty(t, output, "output should be captured")
	assert.Contains(t, output, "Process", "output should contain process output")

	_ = node.Teardown()
}

func TestOutputCapture_BasicCapture(t *testing.T) {
	t.Parallel()

	t.Run("CapturesSmallOutput", func(t *testing.T) {
		t.Parallel()

		oc := newOutputCapture(1024 * 1024) // 1MB limit

		// Create a pipe to simulate output
		reader, writer, err := os.Pipe()
		require.NoError(t, err)

		ctx := context.Background()
		oc.start(ctx, reader)

		// Write test data
		testData := "Hello, World!"
		_, err = writer.WriteString(testData)
		require.NoError(t, err)
		_ = writer.Close()

		// Wait for capture
		output, err := oc.wait()
		assert.NoError(t, err)
		assert.Equal(t, testData, output)
	})

	t.Run("CapturesLargeOutput", func(t *testing.T) {
		t.Parallel()

		oc := newOutputCapture(1024 * 1024) // 1MB limit

		reader, writer, err := os.Pipe()
		require.NoError(t, err)

		ctx := context.Background()
		oc.start(ctx, reader)

		// Write 100KB of data
		testData := strings.Repeat("x", 100*1024)
		_, err = writer.WriteString(testData)
		require.NoError(t, err)
		_ = writer.Close()

		output, err := oc.wait()
		assert.NoError(t, err)
		assert.Equal(t, testData, output)
	})

	t.Run("TruncatesExcessOutput", func(t *testing.T) {
		t.Parallel()

		maxSize := int64(1024) // 1KB limit
		oc := newOutputCapture(maxSize)

		reader, writer, err := os.Pipe()
		require.NoError(t, err)

		ctx := context.Background()
		oc.start(ctx, reader)

		// Write data larger than limit
		testData := strings.Repeat("x", 2048) // 2KB
		_, err = writer.WriteString(testData)
		require.NoError(t, err)
		_ = writer.Close()

		output, err := oc.wait()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "exceeded maximum size limit")
		assert.Equal(t, int(maxSize), len(output))
	})

	t.Run("HandlesEmptyOutput", func(t *testing.T) {
		t.Parallel()

		oc := newOutputCapture(1024)

		reader, writer, err := os.Pipe()
		require.NoError(t, err)

		ctx := context.Background()
		oc.start(ctx, reader)

		// Close writer immediately with no data
		_ = writer.Close()

		output, err := oc.wait()
		assert.NoError(t, err)
		assert.Empty(t, output)
	})
}

func TestOutputCoordinator_StdoutFile(t *testing.T) {
	t.Parallel()

	t.Run("ReturnsFileName", func(t *testing.T) {
		t.Parallel()

		oc := &OutputCoordinator{
			stdoutFileName: "/var/log/test.stdout.log",
		}

		result := oc.StdoutFile()
		assert.Equal(t, "/var/log/test.stdout.log", result)
	})

	t.Run("ReturnsEmptyWhenNotSet", func(t *testing.T) {
		t.Parallel()

		oc := &OutputCoordinator{}

		result := oc.StdoutFile()
		assert.Empty(t, result)
	})
}

func TestOutputCoordinator_FlushWriters(t *testing.T) {
	t.Parallel()

	t.Run("NoErrorWhenClosed", func(t *testing.T) {
		t.Parallel()

		oc := &OutputCoordinator{
			closed: true,
		}

		err := oc.flushWriters()
		assert.NoError(t, err)
	})

	t.Run("NoErrorWithNilWriters", func(t *testing.T) {
		t.Parallel()

		oc := &OutputCoordinator{}

		err := oc.flushWriters()
		assert.NoError(t, err)
	})
}

func TestOutputCoordinator_CloseResources(t *testing.T) {
	t.Parallel()

	t.Run("NoErrorWhenAlreadyClosed", func(t *testing.T) {
		t.Parallel()

		oc := &OutputCoordinator{
			closed: true,
		}

		err := oc.closeResources()
		assert.NoError(t, err)
	})

	t.Run("MarksAsClosed", func(t *testing.T) {
		t.Parallel()

		oc := &OutputCoordinator{}

		_ = oc.closeResources()
		assert.True(t, oc.closed)
	})
}

// mockWriteCloser is a test implementation of io.WriteCloser
type mockWriteCloser struct {
	buf    *bytes.Buffer
	closed bool
}

func newMockWriteCloser() *mockWriteCloser {
	return &mockWriteCloser{buf: &bytes.Buffer{}}
}

func (m *mockWriteCloser) Write(p []byte) (int, error) {
	return m.buf.Write(p)
}

func (m *mockWriteCloser) Close() error {
	m.closed = true
	return nil
}

// mockLogWriterFactory is a test implementation of LogWriterFactory
type mockLogWriterFactory struct {
	stdoutWriter *mockWriteCloser
	stderrWriter *mockWriteCloser
}

func newMockLogWriterFactory() *mockLogWriterFactory {
	return &mockLogWriterFactory{
		stdoutWriter: newMockWriteCloser(),
		stderrWriter: newMockWriteCloser(),
	}
}

func (m *mockLogWriterFactory) NewStepWriter(_ context.Context, _ string, streamType int) io.WriteCloser {
	if streamType == exec.StreamTypeStdout {
		return m.stdoutWriter
	}
	return m.stderrWriter
}

func TestOutputCoordinator_SetupRemoteWriters(t *testing.T) {
	t.Parallel()

	t.Run("CreatesSeparateWritersForStdoutAndStderr", func(t *testing.T) {
		t.Parallel()

		oc := &OutputCoordinator{}
		factory := newMockLogWriterFactory()
		ctx := context.Background()

		data := NodeData{
			Step: core.Step{Name: "test-step"},
			State: NodeState{
				Stdout: "/path/to/stdout.log",
				Stderr: "/path/to/stderr.log",
			},
		}

		err := oc.setupRemoteWriters(ctx, data, factory)
		require.NoError(t, err)

		// Verify writers were created
		assert.NotNil(t, oc.stdoutWriter)
		assert.NotNil(t, oc.stderrWriter)
		assert.Equal(t, "/path/to/stdout.log", oc.stdoutFileName)
		assert.Equal(t, "/path/to/stderr.log", oc.stderrFileName)

		// Verify stdout and stderr writers are different
		assert.NotSame(t, oc.stdoutWriter, oc.stderrWriter)
	})

	t.Run("MergesWritersWhenPathsMatch", func(t *testing.T) {
		t.Parallel()

		oc := &OutputCoordinator{}
		factory := newMockLogWriterFactory()
		ctx := context.Background()

		// Same path for both stdout and stderr
		data := NodeData{
			Step: core.Step{Name: "test-step"},
			State: NodeState{
				Stdout: "/path/to/combined.log",
				Stderr: "/path/to/combined.log",
			},
		}

		err := oc.setupRemoteWriters(ctx, data, factory)
		require.NoError(t, err)

		// When paths are the same, stderr should use the same writer as stdout
		assert.Same(t, oc.stdoutWriter, oc.stderrWriter)
	})
}

func TestOutputCoordinator_CapturedOutput(t *testing.T) {
	t.Parallel()

	t.Run("ReturnsCachedResult", func(t *testing.T) {
		t.Parallel()

		oc := &OutputCoordinator{
			outputCaptured: true,
			outputData:     "cached output",
		}
		ctx := context.Background()

		output, err := oc.capturedOutput(ctx)
		assert.NoError(t, err)
		assert.Equal(t, "cached output", output)
	})

	t.Run("ReturnsEmptyWhenNoCapture", func(t *testing.T) {
		t.Parallel()

		oc := &OutputCoordinator{}
		ctx := context.Background()

		output, err := oc.capturedOutput(ctx)
		assert.NoError(t, err)
		assert.Empty(t, output)
	})

	t.Run("CapturesFromOutputCapture", func(t *testing.T) {
		t.Parallel()

		// Create a pipe for output
		reader, writer, err := os.Pipe()
		require.NoError(t, err)

		oc := &OutputCoordinator{
			outputCapture: newOutputCapture(1024 * 1024),
			outputReader:  reader,
			outputWriter:  writer,
		}

		ctx := context.Background()

		// Start capturing
		oc.outputCapture.start(ctx, reader)

		// Write test data
		testData := "captured test output"
		_, err = writer.WriteString(testData)
		require.NoError(t, err)

		// Get captured output (this will close the writer)
		output, err := oc.capturedOutput(ctx)
		assert.NoError(t, err)
		assert.Equal(t, testData, output)

		// Verify caching works
		assert.True(t, oc.outputCaptured)
	})

	t.Run("AccumulatesOutputOnRetry", func(t *testing.T) {
		t.Parallel()

		// Create a pipe for output
		reader, writer, err := os.Pipe()
		require.NoError(t, err)

		oc := &OutputCoordinator{
			outputCapture: newOutputCapture(1024 * 1024),
			outputReader:  reader,
			outputWriter:  writer,
			outputData:    "previous output", // Simulating previous attempt
		}

		ctx := context.Background()

		// Start capturing
		oc.outputCapture.start(ctx, reader)

		// Write test data
		testData := "new output"
		_, err = writer.WriteString(testData)
		require.NoError(t, err)

		// Get captured output
		output, err := oc.capturedOutput(ctx)
		assert.NoError(t, err)

		// Should contain both previous and new output
		assert.Contains(t, output, "previous output")
		assert.Contains(t, output, "new output")
	})
}

func TestOutputCoordinator_CapturedStderr(t *testing.T) {
	t.Parallel()

	t.Run("ReturnsCachedResult", func(t *testing.T) {
		t.Parallel()

		oc := &OutputCoordinator{
			stderrOutputCaptured: true,
			stderrOutputData:     "cached stderr",
		}
		ctx := context.Background()

		output, err := oc.capturedStderr(ctx)
		assert.NoError(t, err)
		assert.Equal(t, "cached stderr", output)
	})

	t.Run("ReturnsEmptyWhenNoCapture", func(t *testing.T) {
		t.Parallel()

		oc := &OutputCoordinator{}
		ctx := context.Background()

		output, err := oc.capturedStderr(ctx)
		assert.NoError(t, err)
		assert.Empty(t, output)
	})

	t.Run("CapturesFromStderrCapture", func(t *testing.T) {
		t.Parallel()

		reader, writer, err := os.Pipe()
		require.NoError(t, err)

		oc := &OutputCoordinator{
			stderrCapture:      newOutputCapture(1024 * 1024),
			stderrOutputReader: reader,
			stderrOutputWriter: writer,
		}

		ctx := context.Background()
		oc.stderrCapture.start(ctx, reader)

		_, err = writer.WriteString("captured stderr")
		require.NoError(t, err)

		output, err := oc.capturedStderr(ctx)
		assert.NoError(t, err)
		assert.Equal(t, "captured stderr", output)
		assert.True(t, oc.stderrOutputCaptured)
	})

	t.Run("PreservesPreviousAttemptsWhenRetryPipesAreRecreated", func(t *testing.T) {
		t.Parallel()

		oc := &OutputCoordinator{
			stderrWriter:         io.Discard,
			stderrOutputData:     "first attempt",
			stderrOutputCaptured: true,
		}
		t.Cleanup(func() {
			_ = oc.closeResources()
		})

		cmd := &outputTestExecutor{}
		data := NodeData{
			Step: core.Step{
				Name: "stderr-structured-output",
				StructuredOutput: map[string]core.StepOutputEntry{
					"warning": {From: core.StepOutputSourceStderr},
				},
			},
		}
		ctx := context.Background()

		err := oc.setupExecutorIO(ctx, cmd, data)
		require.NoError(t, err)
		require.NotNil(t, cmd.stderr)

		_, err = io.WriteString(cmd.stderr, "second attempt")
		require.NoError(t, err)

		output, err := oc.capturedStderr(ctx)
		require.NoError(t, err)
		assert.Equal(t, "first attempt\nsecond attempt", output)
	})
}
