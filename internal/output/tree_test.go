// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package output

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestRenderer creates a renderer with colors disabled for consistent test output.
func newTestRenderer() *Renderer {
	config := DefaultConfig()
	config.ColorEnabled = false
	return NewRenderer(config)
}

// newTestRendererWithConfig creates a renderer with custom config options.
func newTestRendererWithConfig(opts ...func(*Config)) *Renderer {
	config := DefaultConfig()
	config.ColorEnabled = false
	for _, opt := range opts {
		opt(&config)
	}
	return NewRenderer(config)
}

// createTempLogFile creates a temporary log file with the given content.
// Returns the file path and a cleanup function.
func createTempLogFile(t *testing.T, pattern, content string) (string, func()) {
	t.Helper()
	file, err := os.CreateTemp("", pattern)
	require.NoError(t, err)
	if content != "" {
		_, _ = file.WriteString(content)
	}
	_ = file.Close()
	return file.Name(), func() { _ = os.Remove(file.Name()) }
}

func TestRenderDAGStatus_BasicSuccess(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "test-dag"}
	status := &ir.DAGRunStatus{
		Status:     ir.Succeeded,
		StartedAt:  "2024-01-15 10:00:00",
		FinishedAt: "2024-01-15 10:01:00",
		Nodes: []*ir.Node{
			{
				Step:       ir.Step{Name: "step1", Command: "echo", Args: []string{"hello"}},
				Status:     ir.NodeSucceeded,
				StartedAt:  "2024-01-15 10:00:00",
				FinishedAt: "2024-01-15 10:00:30",
			},
		},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "test-dag")
	require.Contains(t, output, "step1")
	require.Contains(t, output, "[succeeded]")
	require.Contains(t, output, "Result: Succeeded")
}

func TestRenderDAGStatus_BuildDecision(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "build-dag"}
	status := &ir.DAGRunStatus{
		Status: ir.Succeeded,
		Nodes: []*ir.Node{
			{
				Step:   ir.Step{Name: "build"},
				Status: ir.NodeSucceeded,
				Build: &ir.BuildExecution{
					Decision:    "reuse",
					Reason:      "matched",
					Detail:      "recipe, inputs, and output match the committed manifest",
					ProducerRun: ir.NewDAGRunRef("build-dag", "run-1"),
				},
			},
			{
				Step:   ir.Step{Name: "publish"},
				Status: ir.NodeSkipped,
				Build: &ir.BuildExecution{
					Decision: "none",
					Reason:   "precondition_not_met",
				},
			},
		},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "build: reuse (matched)")
	require.Contains(t, output, "producer: build-dag:run-1")
	require.Contains(t, output, "build: none (precondition_not_met)")
}

func TestRenderDAGStatus_FailedStep(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "failed-dag"}
	status := &ir.DAGRunStatus{
		Status: ir.Failed,
		Nodes: []*ir.Node{
			{
				Step:   ir.Step{Name: "failing-step", Command: "exit", Args: []string{"1"}},
				Status: ir.NodeFailed,
				Error:  "command exited with code 1",
			},
		},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "[failed]")
	require.Contains(t, output, "error:")
	require.Contains(t, output, "command exited with code 1")
	require.Contains(t, output, "Result: Failed")
}

func TestRenderDAGStatus_MultipleSteps(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "multi-step-dag"}
	status := &ir.DAGRunStatus{
		Status: ir.Succeeded,
		Nodes: []*ir.Node{
			{Step: ir.Step{Name: "step1"}, Status: ir.NodeSucceeded},
			{Step: ir.Step{Name: "step2"}, Status: ir.NodeSucceeded},
			{Step: ir.Step{Name: "step3"}, Status: ir.NodeSucceeded},
		},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "├─")
	require.Contains(t, output, "└─")
	require.Contains(t, output, "step1")
	require.Contains(t, output, "step2")
	require.Contains(t, output, "step3")
}

func TestRenderDAGStatus_LifecycleHandlers(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "handler-dag"}
	status := &ir.DAGRunStatus{
		Status: ir.Failed,
		OnInit: &ir.Node{
			Step:   ir.Step{Name: "onInit", Command: "exit", Args: []string{"1"}},
			Status: ir.NodeFailed,
			Error:  "init handler failed",
		},
		Nodes: []*ir.Node{
			{Step: ir.Step{Name: "step1"}, Status: ir.NodeNotStarted},
		},
		OnExit: &ir.Node{
			Step:   ir.Step{Name: "onExit"},
			Status: ir.NodeSucceeded,
		},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "onInit")
	require.Contains(t, output, "init handler failed")
	require.Contains(t, output, "onExit")
	assert.Less(t, strings.Index(output, "onInit"), strings.Index(output, "step1"),
		"init handler should render before the steps it precedes")
	assert.Less(t, strings.Index(output, "step1"), strings.Index(output, "onExit"),
		"exit handler should render after the steps")
}

func TestRenderDAGStatus_RunningStatus(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "running-dag"}
	status := &ir.DAGRunStatus{
		Status:    ir.Running,
		StartedAt: "2024-01-15 10:00:00",
		Nodes: []*ir.Node{
			{Step: ir.Step{Name: "running-step"}, Status: ir.NodeRunning, StartedAt: "2024-01-15 10:00:00"},
		},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "[running]")
	require.Contains(t, output, "Running")
}

func TestRenderDAGStatus_WaitingHumanTask(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{Name: "deploy"}
	status := &ir.DAGRunStatus{
		Name:   "deploy",
		Status: ir.Waiting,
		Nodes: []*ir.Node{{
			Step: ir.Step{
				ID:   "production_review",
				Name: "production_review",
				HumanTask: &ir.HumanTaskConfig{
					Prompt: "Review production",
					Form:   []byte(`{"type":"object","additionalProperties":false}`),
				},
			},
			Status: ir.NodeWaiting,
		}},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)
	assert.Contains(t, output, "step id: production_review")
	assert.Contains(t, output, "prompt: Review production")
	assert.Contains(t, output, `form: {"type":"object","additionalProperties":false}`)
}

func TestRenderDAGStatus_WaitingHumanTaskPreservesResolvedPromptAndUTF8(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{Name: "deploy"}
	status := &ir.DAGRunStatus{
		Status: ir.Waiting,
		Nodes: []*ir.Node{
			{
				Step: ir.Step{
					ID:        "empty_prompt",
					Name:      "empty_prompt",
					HumanTask: &ir.HumanTaskConfig{},
				},
				Status: ir.NodeWaiting,
			},
			{
				Step: ir.Step{
					ID:   "multiline",
					Name: "multiline",
					HumanTask: &ir.HumanTaskConfig{
						Prompt: "第一行\n第二行",
						Form:   []byte(`{"type":"object","description":"日本語の長い説明文を安全に折り返します"}`),
					},
				},
				Status: ir.NodeWaiting,
			},
		},
	}

	output := newTestRendererWithConfig(func(config *Config) { config.MaxWidth = 28 }).RenderDAGStatus(dag, status)
	assert.True(t, utf8.ValidString(output))
	assert.Contains(t, output, "prompt: \n")
	assert.Contains(t, output, "prompt: 第一行")
	assert.Contains(t, output, "第二行")
	assert.NotContains(t, output, "\n第二行")
}

func TestRenderDAGStatus_AbortedStatus(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "aborted-dag"}
	status := &ir.DAGRunStatus{
		Status:     ir.Aborted,
		StartedAt:  "2024-01-15 10:00:00",
		FinishedAt: "2024-01-15 10:00:30",
		Nodes: []*ir.Node{
			{Step: ir.Step{Name: "aborted-step"}, Status: ir.NodeAborted},
		},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "[aborted]")
	require.Contains(t, output, "Result: Aborted")
}

func TestRenderDAGStatus_PartiallySucceededStatus(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "partial-dag"}
	status := &ir.DAGRunStatus{
		Status:     ir.PartiallySucceeded,
		StartedAt:  "2024-01-15 10:00:00",
		FinishedAt: "2024-01-15 10:00:30",
		Nodes: []*ir.Node{
			{Step: ir.Step{Name: "partial-step"}, Status: ir.NodePartiallySucceeded},
		},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "[partially_succeeded]")
	require.Contains(t, output, "Result: Partially Succeeded")
}

func TestRenderDAGStatus_QueuedStatus(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "queued-dag"}
	status := &ir.DAGRunStatus{
		Status: ir.Queued,
		Nodes:  []*ir.Node{},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "Queued")
}

func TestRenderDAGStatus_NotStartedStatus(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "not-started-dag"}
	status := &ir.DAGRunStatus{
		Status: ir.NotStarted,
		Nodes: []*ir.Node{
			{Step: ir.Step{Name: "not-started-step"}, Status: ir.NodeNotStarted},
		},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "Not Started")
}

func TestRenderDAGStatus_SkippedStep(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "skipped-dag"}
	status := &ir.DAGRunStatus{
		Status: ir.Succeeded,
		Nodes: []*ir.Node{
			{Step: ir.Step{Name: "skipped-step"}, Status: ir.NodeSkipped},
		},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "[skipped]")
}

func TestRenderDAGStatus_WithSubRuns(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "parent-dag"}
	status := &ir.DAGRunStatus{
		Status: ir.Succeeded,
		Nodes: []*ir.Node{
			{
				Step:   ir.Step{Name: "sub-step"},
				Status: ir.NodeSucceeded,
				SubRuns: []ir.SubDAGRun{
					{DAGRunID: "sub-run-123", Params: "param1=value1"},
				},
			},
		},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "subdag:")
	require.Contains(t, output, "sub-run-123")
	require.Contains(t, output, "param1=value1")
}

func TestRenderDAGStatus_WithSubRunsNoParams(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "parent-dag"}
	status := &ir.DAGRunStatus{
		Status: ir.Succeeded,
		Nodes: []*ir.Node{
			{
				Step:   ir.Step{Name: "sub-step"},
				Status: ir.NodeSucceeded,
				SubRuns: []ir.SubDAGRun{
					{DAGRunID: "sub-run-456"},
				},
			},
		},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "sub-run-456")
}

func TestRenderDAGStatus_DisabledOutputs(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "test-dag"}
	status := &ir.DAGRunStatus{
		Status: ir.Succeeded,
		Nodes: []*ir.Node{
			{
				Step:   ir.Step{Name: "step1", Command: "echo"},
				Status: ir.NodeSucceeded,
				Stdout: "/tmp/nonexistent-stdout.log",
				Stderr: "/tmp/nonexistent-stderr.log",
			},
		},
	}

	renderer := newTestRendererWithConfig(func(c *Config) {
		c.ShowStdout = false
		c.ShowStderr = false
	})
	output := renderer.RenderDAGStatus(dag, status)

	require.NotContains(t, output, "stdout:")
	require.NotContains(t, output, "stderr:")
}

func TestRenderDAGStatus_WithActualLogFiles(t *testing.T) {
	t.Parallel()

	stdoutPath, cleanupStdout := createTempLogFile(t, "stdout-*.log", "Hello from stdout\nLine 2\n")
	defer cleanupStdout()

	stderrPath, cleanupStderr := createTempLogFile(t, "stderr-*.log", "Warning: something happened\n")
	defer cleanupStderr()

	dag := &ir.DAG{Name: "test-dag"}
	status := &ir.DAGRunStatus{
		Status:     ir.Succeeded,
		StartedAt:  "2024-01-15 10:00:00",
		FinishedAt: "2024-01-15 10:00:30",
		Nodes: []*ir.Node{
			{
				Step:       ir.Step{Name: "step1", Command: "echo"},
				Status:     ir.NodeSucceeded,
				StartedAt:  "2024-01-15 10:00:00",
				FinishedAt: "2024-01-15 10:00:30",
				Stdout:     stdoutPath,
				Stderr:     stderrPath,
			},
		},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "Hello from stdout")
	require.Contains(t, output, "Warning: something happened")
}

func TestRenderDAGStatus_WithTruncatedOutput(t *testing.T) {
	t.Parallel()

	tmpfile, err := os.CreateTemp("", "stdout-*.log")
	require.NoError(t, err)
	defer func() { _ = os.Remove(tmpfile.Name()) }()
	for i := 1; i <= 100; i++ {
		_, _ = fmt.Fprintf(tmpfile, "Line %d\n", i)
	}
	_ = tmpfile.Close()

	dag := &ir.DAG{Name: "test-dag"}
	status := &ir.DAGRunStatus{
		Status: ir.Succeeded,
		Nodes: []*ir.Node{
			{
				Step:   ir.Step{Name: "step1"},
				Status: ir.NodeSucceeded,
				Stdout: tmpfile.Name(),
			},
		},
	}

	renderer := newTestRendererWithConfig(func(c *Config) {
		c.MaxOutputLines = 10
	})
	output := renderer.RenderDAGStatus(dag, status)

	require.Contains(t, output, "... (90 more lines)")
	require.Contains(t, output, "Line 91")
}

func TestRenderDAGStatus_StartTimeFallback(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		startedAt string
	}{
		{name: "empty", startedAt: ""},
		{name: "dash", startedAt: "-"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dag := &ir.DAG{Name: "test-dag"}
			status := &ir.DAGRunStatus{
				Status:    ir.NotStarted,
				StartedAt: tt.startedAt,
				Nodes:     []*ir.Node{},
			}

			output := newTestRenderer().RenderDAGStatus(dag, status)

			require.Contains(t, output, "dag: test-dag")
		})
	}
}

func TestRenderDAGStatus_NoDuration(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "test-dag"}
	status := &ir.DAGRunStatus{
		Status:    ir.NotStarted,
		StartedAt: "",
		Nodes:     []*ir.Node{},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.NotContains(t, output, "dag: test-dag (")
}

func TestRenderDAGStatus_InvalidTimeFormat(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "test-dag"}
	status := &ir.DAGRunStatus{
		Status:     ir.Succeeded,
		StartedAt:  "invalid-time",
		FinishedAt: "also-invalid",
		Nodes:      []*ir.Node{},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "dag: test-dag")
}

func TestReadLogFileTail_AllLines(t *testing.T) {
	t.Parallel()

	tmpfile, err := os.CreateTemp("", "test-log-*.txt")
	require.NoError(t, err)
	defer func() { _ = os.Remove(tmpfile.Name()) }()
	for i := 1; i <= 10; i++ {
		_, _ = fmt.Fprintf(tmpfile, "Line %d\n", i)
	}
	_ = tmpfile.Close()

	lines, truncated, err := ReadLogFileTail(tmpfile.Name(), 0)
	require.NoError(t, err)
	require.Len(t, lines, 10)
	require.Equal(t, 0, truncated)
}

func TestReadLogFileTail_WithLimit(t *testing.T) {
	t.Parallel()

	tmpfile, err := os.CreateTemp("", "test-log-*.txt")
	require.NoError(t, err)
	defer func() { _ = os.Remove(tmpfile.Name()) }()
	for i := 1; i <= 100; i++ {
		_, _ = fmt.Fprintf(tmpfile, "Line %d\n", i)
	}
	_ = tmpfile.Close()

	lines, truncated, err := ReadLogFileTail(tmpfile.Name(), 10)
	require.NoError(t, err)
	require.Len(t, lines, 10)
	require.Equal(t, 90, truncated)
	require.Equal(t, "Line 91", lines[0])
	require.Equal(t, "Line 100", lines[9])
}

func TestReadLogFileTail_NegativeLimit(t *testing.T) {
	t.Parallel()

	path, cleanup := createTempLogFile(t, "test-log-*.txt", "Line 1\nLine 2\n")
	defer cleanup()

	lines, truncated, err := ReadLogFileTail(path, -5)
	require.NoError(t, err)
	require.Len(t, lines, 2)
	require.Equal(t, 0, truncated)
}

func TestReadLogFileTail_EmptyFile(t *testing.T) {
	t.Parallel()

	path, cleanup := createTempLogFile(t, "test-log-*.txt", "")
	defer cleanup()

	lines, truncated, err := ReadLogFileTail(path, 10)
	require.NoError(t, err)
	require.Len(t, lines, 0)
	require.Equal(t, 0, truncated)
}

func TestReadLogFileTail_NonexistentFile(t *testing.T) {
	t.Parallel()

	lines, truncated, err := ReadLogFileTail("/nonexistent/path/file.log", 10)
	require.NoError(t, err)
	require.Nil(t, lines)
	require.Equal(t, 0, truncated)
}

func TestReadLogFileTail_EmptyPath(t *testing.T) {
	t.Parallel()

	lines, truncated, err := ReadLogFileTail("", 10)
	require.NoError(t, err)
	require.Nil(t, lines)
	require.Equal(t, 0, truncated)
}

func TestReadLogFileTail_BinaryContent(t *testing.T) {
	t.Parallel()

	tmpfile, err := os.CreateTemp("", "test-log-*.bin")
	require.NoError(t, err)
	defer func() { _ = os.Remove(tmpfile.Name()) }()
	_, _ = tmpfile.Write([]byte{0x00, 0x01, 0x02, 0xFF, 0xFE})
	_ = tmpfile.Close()

	lines, _, err := ReadLogFileTail(tmpfile.Name(), 10)
	require.NoError(t, err)
	require.Len(t, lines, 1)
	require.Equal(t, "(binary data)", lines[0])
}

func TestReadLogFileTail_LargeFile(t *testing.T) {
	t.Parallel()

	tmpfile, err := os.CreateTemp("", "test-large-*.txt")
	require.NoError(t, err)
	defer func() { _ = os.Remove(tmpfile.Name()) }()
	data := make([]byte, 11*1024*1024)
	for i := range data {
		data[i] = 'x'
	}
	_, _ = tmpfile.Write(data)
	_ = tmpfile.Close()

	lines, truncated, err := ReadLogFileTail(tmpfile.Name(), 10)
	require.NoError(t, err)
	require.Equal(t, 0, truncated)
	require.Len(t, lines, 1)
	require.Equal(t, "(file too large, >10MB)", lines[0])
}

func TestStatusText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status ir.Status
		text   string
	}{
		{ir.Running, "Running"},
		{ir.Succeeded, "Succeeded"},
		{ir.Failed, "Failed"},
		{ir.Aborted, "Aborted"},
		{ir.PartiallySucceeded, "Partially Succeeded"},
		{ir.Queued, "Queued"},
		{ir.NotStarted, "Not Started"},
	}

	for _, tt := range tests {
		t.Run(tt.status.String(), func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.text, StatusText(tt.status))
		})
	}
}

func TestDefaultConfig(t *testing.T) {
	t.Parallel()
	config := DefaultConfig()

	require.True(t, config.ColorEnabled)
	require.True(t, config.ShowStdout)
	require.True(t, config.ShowStderr)
	require.Equal(t, DefaultMaxOutputLines, config.MaxOutputLines)
}

func TestRenderDAGStatus_TreeStructure(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "tree-test"}
	status := &ir.DAGRunStatus{
		Status: ir.Succeeded,
		Nodes: []*ir.Node{
			{Step: ir.Step{Name: "first_step"}, Status: ir.NodeSucceeded},
			{Step: ir.Step{Name: "last_step"}, Status: ir.NodeSucceeded},
		},
	}

	renderer := newTestRendererWithConfig(func(c *Config) {
		c.ShowStdout = false
		c.ShowStderr = false
	})
	output := renderer.RenderDAGStatus(dag, status)

	require.Contains(t, output, "├─first_step")
	require.Contains(t, output, "└─last_step")
}

func TestIsBinaryContent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		data     []byte
		expected bool
	}{
		{"empty", []byte{}, false},
		{"text", []byte("hello world"), false},
		{"with null", []byte{0x00, 'a', 'b'}, true},
		{"high non-printable", []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a}, true},
		{"mostly text", []byte("hello\nworld\ttab"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, isBinaryContent(tt.data))
		})
	}
}

func TestRenderDAGStatus_WithMultipleCommands(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "multi-cmd-dag"}
	status := &ir.DAGRunStatus{
		Status: ir.Succeeded,
		Nodes: []*ir.Node{
			{
				Step: ir.Step{
					Name: "multi-step",
					Commands: []ir.CommandEntry{
						{Command: "echo", Args: []string{"first"}, CmdWithArgs: "echo first"},
						{Command: "echo", Args: []string{"second"}, CmdWithArgs: "echo second"},
					},
				},
				Status: ir.NodeSucceeded,
			},
		},
	}

	renderer := newTestRendererWithConfig(func(c *Config) {
		c.ShowStdout = false
		c.ShowStderr = false
	})
	output := renderer.RenderDAGStatus(dag, status)

	require.Contains(t, output, "echo first")
	require.Contains(t, output, "echo second")
}

func TestRenderDAGStatus_WithLegacyCommand(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "legacy-cmd-dag"}
	status := &ir.DAGRunStatus{
		Status: ir.Succeeded,
		Nodes: []*ir.Node{
			{
				Step: ir.Step{
					Name:        "legacy-step",
					CmdWithArgs: "echo hello world",
				},
				Status: ir.NodeSucceeded,
			},
		},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "echo hello world")
}

func TestRenderDAGStatus_WithLegacyCommandAndArgs(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "legacy-cmd-dag"}
	status := &ir.DAGRunStatus{
		Status: ir.Succeeded,
		Nodes: []*ir.Node{
			{
				Step: ir.Step{
					Name:    "legacy-step",
					Command: "echo",
					Args:    []string{"hello", "world"},
				},
				Status: ir.NodeSucceeded,
			},
		},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "echo hello world")
}

func TestRenderDAGStatus_NoCommand(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "no-cmd-dag"}
	status := &ir.DAGRunStatus{
		Status: ir.Succeeded,
		Nodes: []*ir.Node{
			{
				Step:   ir.Step{Name: "no-cmd-step"},
				Status: ir.NodeSucceeded,
			},
		},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "no-cmd-step")
}

func TestTrimTrailingEmptyLines(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{"no trailing", []string{"a", "b"}, []string{"a", "b"}},
		{"one trailing", []string{"a", "b", ""}, []string{"a", "b"}},
		{"multiple trailing", []string{"a", "", ""}, []string{"a"}},
		{"all empty", []string{"", "", ""}, []string{}},
		{"empty input", []string{}, []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, trimTrailingEmptyLines(tt.input))
		})
	}
}

func TestNodeStatusToStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		nodeStatus ir.NodeStatus
		expected   ir.Status
	}{
		{ir.NodeRunning, ir.Running},
		{ir.NodeRetrying, ir.Running},
		{ir.NodeSucceeded, ir.Succeeded},
		{ir.NodeFailed, ir.Failed},
		{ir.NodeAborted, ir.Aborted},
		{ir.NodePartiallySucceeded, ir.PartiallySucceeded},
		{ir.NodeWaiting, ir.Waiting},
		{ir.NodeRejected, ir.Rejected},
		{ir.NodeSkipped, ir.NotStarted},
		{ir.NodeNotStarted, ir.NotStarted},
		{ir.NodeStatus(999), ir.NotStarted},
	}

	for _, tt := range tests {
		t.Run(tt.nodeStatus.String(), func(t *testing.T) {
			require.Equal(t, tt.expected, nodeStatusToStatus(tt.nodeStatus))
		})
	}
}

func TestShouldShowDuration(t *testing.T) {
	t.Parallel()

	assert.True(t, shouldShowDuration(ir.NodeRunning))
	assert.True(t, shouldShowDuration(ir.NodeRetrying))
	assert.True(t, shouldShowDuration(ir.NodeSucceeded))
	assert.False(t, shouldShowDuration(ir.NodeWaiting))
	assert.False(t, shouldShowDuration(ir.NodeSkipped))
}

func TestRenderDAGStatus_UnknownStatus(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "unknown-dag"}
	status := &ir.DAGRunStatus{
		Status: ir.Status(999),
		Nodes:  []*ir.Node{},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "Result:")
}

func TestRenderDAGStatus_OnlyStdout(t *testing.T) {
	t.Parallel()

	stdoutPath, cleanup := createTempLogFile(t, "stdout-*.log", "Hello stdout\n")
	defer cleanup()

	dag := &ir.DAG{Name: "test-dag"}
	status := &ir.DAGRunStatus{
		Status: ir.Succeeded,
		Nodes: []*ir.Node{
			{
				Step:   ir.Step{Name: "step1", Command: "echo"},
				Status: ir.NodeSucceeded,
				Stdout: stdoutPath,
			},
		},
	}

	renderer := newTestRendererWithConfig(func(c *Config) {
		c.ShowStdout = true
		c.ShowStderr = false
	})
	output := renderer.RenderDAGStatus(dag, status)

	require.Contains(t, output, "stdout:")
	require.NotContains(t, output, "stderr:")
}

func TestRenderDAGStatus_OnlyStderr(t *testing.T) {
	t.Parallel()

	stderrPath, cleanup := createTempLogFile(t, "stderr-*.log", "Hello stderr\n")
	defer cleanup()

	dag := &ir.DAG{Name: "test-dag"}
	status := &ir.DAGRunStatus{
		Status: ir.Succeeded,
		Nodes: []*ir.Node{
			{
				Step:   ir.Step{Name: "step1", Command: "echo"},
				Status: ir.NodeSucceeded,
				Stderr: stderrPath,
			},
		},
	}

	renderer := newTestRendererWithConfig(func(c *Config) {
		c.ShowStdout = false
		c.ShowStderr = true
	})
	output := renderer.RenderDAGStatus(dag, status)

	require.NotContains(t, output, "stdout:")
	require.Contains(t, output, "stderr:")
}

func TestReadLogFileTail_PermissionDenied(t *testing.T) {
	t.Parallel()

	path, cleanup := createTempLogFile(t, "test-perm-*.txt", "test content\n")
	defer cleanup()

	if err := os.Chmod(path, 0o000); err != nil {
		t.Skip("Cannot change permissions on this system")
	}
	defer func() { _ = os.Chmod(path, 0o644) }()

	lines, truncated, err := ReadLogFileTail(path, 10)
	if err == nil {
		t.Skip("System allows reading file without read permission")
	}
	require.Nil(t, lines)
	require.Equal(t, 0, truncated)
}

func TestCalculateDuration_InvalidFinishedAt(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "test-dag"}
	status := &ir.DAGRunStatus{
		Status:     ir.Succeeded,
		StartedAt:  "2024-01-15 10:00:00",
		FinishedAt: "invalid-time-format",
		Nodes:      []*ir.Node{},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "dag: test-dag")
}

func TestCalculateDuration_NotRunningWithDashFinishedAt(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "test-dag"}
	status := &ir.DAGRunStatus{
		Status:     ir.Succeeded,
		StartedAt:  "2024-01-15 10:00:00",
		FinishedAt: "-",
		Nodes:      []*ir.Node{},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "dag: test-dag")
}

func TestCalculateDuration_NotRunningWithEmptyFinishedAt(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "test-dag"}
	status := &ir.DAGRunStatus{
		Status:     ir.Succeeded,
		StartedAt:  "2024-01-15 10:00:00",
		FinishedAt: "",
		Nodes:      []*ir.Node{},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "dag: test-dag")
}

func TestRenderDAGStatus_NodeDurationWithInvalidTime(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "test-dag"}
	status := &ir.DAGRunStatus{
		Status: ir.Succeeded,
		Nodes: []*ir.Node{
			{
				Step:       ir.Step{Name: "step1"},
				Status:     ir.NodeSucceeded,
				StartedAt:  "2024-01-15 10:00:00",
				FinishedAt: "invalid-time",
			},
		},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "step1")
}

func TestRenderDAGStatus_RunningNodeCalculatesDuration(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "test-dag"}
	status := &ir.DAGRunStatus{
		Status:    ir.Running,
		StartedAt: "2024-01-15 10:00:00",
		Nodes: []*ir.Node{
			{
				Step:      ir.Step{Name: "running-step"},
				Status:    ir.NodeRunning,
				StartedAt: "2024-01-15 10:00:00",
			},
		},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "running-step")
	require.Contains(t, output, "(")
}

func TestCleanLogLine_CarriageReturn(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"no carriage return", "hello world", []string{"hello world"}},
		{"single carriage return", "first\rsecond", []string{"second"}},
		{"multiple carriage returns", "a\rb\rc\rd", []string{"d"}},
		{"curl progress bar", "  0     0    0     0    0     0      0      0 --:--:-- --:--:-- --:--:--     0\r100   123  100   123    0     0   1000      0 --:--:-- --:--:-- --:--:--  1000", []string{"100   123  100   123    0     0   1000      0 --:--:-- --:--:-- --:--:--  1000"}},
		{"empty after carriage return", "something\r", []string{"something"}},
		{"all empty segments", "\r\r\r", nil},
		{"empty line", "", nil},
		{"whitespace only", "   ", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, cleanLogLine(tt.input))
		})
	}
}

func TestCleanControlChars(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"normal text", "hello world", "hello world"},
		{"with tab", "hello\tworld", "hello\tworld"},
		{"with bell char", "hello\x07world", "helloworld"},
		{"with escape", "hello\x1bworld", "helloworld"},
		{"with backspace", "hello\x08world", "helloworld"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, cleanControlChars(tt.input))
		})
	}
}

func TestCleanErrorMessage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"no stderr tail", "command failed: exit status 1", "command failed: exit status 1"},
		{"with stderr tail newline prefix", "command failed: exit status 1\nrecent stderr (tail):\nsome error output", "command failed: exit status 1"},
		{"with stderr tail no newline prefix", "failed to execute: exit status 56recent stderr (tail):\nerror details", "failed to execute: exit status 56"},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, cleanErrorMessage(tt.input))
		})
	}
}

func TestReadLogFileTail_WithCarriageReturns(t *testing.T) {
	t.Parallel()

	content := "  0     0    0     0    0     0      0      0 --:--:-- --:--:-- --:--:--     0\r100   123  100   123    0     0   1000      0 --:--:-- --:--:-- --:--:--  1000\nDownloaded file.txt\n"
	path, cleanup := createTempLogFile(t, "test-curl-*.txt", content)
	defer cleanup()

	lines, truncated, err := ReadLogFileTail(path, 0)
	require.NoError(t, err)
	require.Equal(t, 0, truncated)
	require.Len(t, lines, 2)
	require.Contains(t, lines[0], "100")
}

func TestRenderDAGStatus_ShowsLogFilePaths(t *testing.T) {
	t.Parallel()

	stdoutPath, cleanupStdout := createTempLogFile(t, "stdout-*.log", "Hello from stdout\n")
	defer cleanupStdout()

	stderrPath, cleanupStderr := createTempLogFile(t, "stderr-*.log", "Warning message\n")
	defer cleanupStderr()

	dag := &ir.DAG{Name: "test-dag"}
	status := &ir.DAGRunStatus{
		Status:     ir.Succeeded,
		StartedAt:  "2024-01-15 10:00:00",
		FinishedAt: "2024-01-15 10:00:30",
		Nodes: []*ir.Node{
			{
				Step:       ir.Step{Name: "step1", Command: "echo"},
				Status:     ir.NodeSucceeded,
				StartedAt:  "2024-01-15 10:00:00",
				FinishedAt: "2024-01-15 10:00:30",
				Stdout:     stdoutPath,
				Stderr:     stderrPath,
			},
		},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "stdout: "+stdoutPath)
	require.Contains(t, output, "stderr: "+stderrPath)
	require.Contains(t, output, "Hello from stdout")
	require.Contains(t, output, "Warning message")
}

func TestRenderDAGStatus_ShowsSchedulerLog(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "test-dag"}
	status := &ir.DAGRunStatus{
		Status:     ir.Succeeded,
		StartedAt:  "2024-01-15 10:00:00",
		FinishedAt: "2024-01-15 10:00:30",
		Log:        "/path/to/scheduler.log",
		Nodes: []*ir.Node{
			{
				Step:   ir.Step{Name: "step1"},
				Status: ir.NodeSucceeded,
			},
		},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "log: /path/to/scheduler.log")
}

func TestRenderDAGStatus_NoSchedulerLogWhenEmpty(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "test-dag"}
	status := &ir.DAGRunStatus{
		Status:     ir.Succeeded,
		StartedAt:  "2024-01-15 10:00:00",
		FinishedAt: "2024-01-15 10:00:30",
		Log:        "",
		Nodes: []*ir.Node{
			{
				Step:   ir.Step{Name: "step1"},
				Status: ir.NodeSucceeded,
			},
		},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.NotContains(t, output, "log:")
}

func TestRenderDAGStatus_SchedulerLogLastBranchWhenNoSteps(t *testing.T) {
	t.Parallel()
	dag := &ir.DAG{Name: "test-dag"}
	status := &ir.DAGRunStatus{
		Status:     ir.Succeeded,
		StartedAt:  "2024-01-15 10:00:00",
		FinishedAt: "2024-01-15 10:00:30",
		Log:        "/path/to/scheduler.log",
		Nodes:      []*ir.Node{},
	}

	output := newTestRenderer().RenderDAGStatus(dag, status)

	require.Contains(t, output, "└─log:")
}
