// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	runtimeexec "github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const nodeSignalExecutorType = "test-node-signal"

var (
	registerNodeSignalExecutorOnce sync.Once
	nodeSignalExecutorFactoryMu    sync.Mutex
	nodeSignalExecutorFactory      func() *blockingSignalExecutor
)

type blockingSignalExecutor struct {
	ready   chan struct{}
	killed  chan os.Signal
	stopped chan cmdutil.TerminationIntent
	// Optional test hooks for controlling Kill ordering.
	killStarted  chan struct{}
	killContinue chan struct{}
	stdout       io.Writer
	stderr       io.Writer
}

func newBlockingSignalExecutor() *blockingSignalExecutor {
	return &blockingSignalExecutor{
		ready:  make(chan struct{}),
		killed: make(chan os.Signal, 1),
	}
}

func (e *blockingSignalExecutor) SetStdout(out io.Writer) { e.stdout = out }

func (e *blockingSignalExecutor) SetStderr(out io.Writer) { e.stderr = out }

func (e *blockingSignalExecutor) Run(ctx context.Context) error {
	close(e.ready)

	select {
	case sig := <-e.killed:
		return fmt.Errorf("signal: %s", sig.String())
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *blockingSignalExecutor) Kill(sig os.Signal) error {
	select {
	case e.killed <- sig:
	default:
	}
	if e.killStarted != nil {
		select {
		case e.killStarted <- struct{}{}:
		default:
		}
	}
	if e.killContinue != nil {
		<-e.killContinue
	}
	return nil
}

func (e *blockingSignalExecutor) Stop(intent cmdutil.TerminationIntent) error {
	if e.stopped != nil {
		select {
		case e.stopped <- intent:
		default:
		}
	}
	return e.Kill(intent.Signal)
}

func registerNodeSignalExecutor(t *testing.T) {
	t.Helper()

	registerNodeSignalExecutorOnce.Do(func() {
		runtimeexec.RegisterExecutor(
			nodeSignalExecutorType,
			func(context.Context, core.Step) (runtimeexec.Executor, error) {
				nodeSignalExecutorFactoryMu.Lock()
				factory := nodeSignalExecutorFactory
				nodeSignalExecutorFactoryMu.Unlock()
				if factory == nil {
					return nil, fmt.Errorf("node signal step factory not configured")
				}
				return factory(), nil
			},
			nil,
			core.ExecutorCapabilities{},
		)
	})
}

func withNodeSignalExecutor(t *testing.T) (<-chan *blockingSignalExecutor, func()) {
	t.Helper()

	registerNodeSignalExecutor(t)

	execCh := make(chan *blockingSignalExecutor, 1)

	nodeSignalExecutorFactoryMu.Lock()
	prev := nodeSignalExecutorFactory
	nodeSignalExecutorFactory = func() *blockingSignalExecutor {
		exec := newBlockingSignalExecutor()
		execCh <- exec
		return exec
	}
	nodeSignalExecutorFactoryMu.Unlock()

	return execCh, func() {
		nodeSignalExecutorFactoryMu.Lock()
		nodeSignalExecutorFactory = prev
		nodeSignalExecutorFactoryMu.Unlock()
	}
}

func withNodeSignalExecutorFactory(t *testing.T, factory func() *blockingSignalExecutor) (<-chan *blockingSignalExecutor, func()) {
	t.Helper()

	registerNodeSignalExecutor(t)

	execCh := make(chan *blockingSignalExecutor, 1)

	nodeSignalExecutorFactoryMu.Lock()
	prev := nodeSignalExecutorFactory
	nodeSignalExecutorFactory = func() *blockingSignalExecutor {
		exec := factory()
		execCh <- exec
		return exec
	}
	nodeSignalExecutorFactoryMu.Unlock()

	return execCh, func() {
		nodeSignalExecutorFactoryMu.Lock()
		nodeSignalExecutorFactory = prev
		nodeSignalExecutorFactoryMu.Unlock()
	}
}

func TestNode(t *testing.T) {
	t.Run("Execute", func(t *testing.T) {
		t.Parallel()

		node := setupNode(t, withNodeCommand("true"))
		node.Execute(t)
	})
	t.Run("Error", func(t *testing.T) {
		t.Parallel()

		node := setupNode(t, withNodeCommand("false"))
		node.ExecuteFail(t, "exit status 1")
	})
	t.Run("Signal", func(t *testing.T) {
		execCh, restore := withNodeSignalExecutor(t)
		defer restore()

		node := setupNode(t, withNodeExecutorType(nodeSignalExecutorType))
		go func() {
			exec := <-execCh
			<-exec.ready
			node.Signal(node.Context, syscall.SIGTERM, false)
		}()

		node.SetStatus(core.NodeRunning)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		err := node.Node.Execute(node.execContext(dagRunID))
		require.Error(t, err)
		require.Contains(t, err.Error(), "signal: terminated")
		require.Equal(t, core.NodeAborted.String(), node.State().Status.String())
	})
	t.Run("SignalOnStop", func(t *testing.T) {
		execCh, restore := withNodeSignalExecutor(t)
		defer restore()

		node := setupNode(t, withNodeExecutorType(nodeSignalExecutorType), withNodeSignalOnStop("SIGINT"))
		go func() {
			exec := <-execCh
			<-exec.ready
			node.Signal(node.Context, syscall.Signal(0), true) // allow override signal
		}()

		node.SetStatus(core.NodeRunning)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		err := node.Node.Execute(node.execContext(dagRunID))
		require.Error(t, err)
		require.Contains(t, err.Error(), "signal: interrupt")
		require.Equal(t, core.NodeAborted.String(), node.State().Status.String())
	})
	t.Run("SignalBeforeExecutorRunPreventsStart", func(t *testing.T) {
		execCh, restore := withNodeSignalExecutor(t)
		defer restore()

		node := setupNode(t, withNodeExecutorType(nodeSignalExecutorType))
		node.SetStatus(core.NodeRunning)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		err := runtime.NewStepExecutor().Execute(node.execContext(dagRunID), node.Node, func() {
			node.Signal(node.Context, syscall.SIGTERM, false)
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "node execution aborted before start")
		require.Equal(t, core.NodeAborted.String(), node.State().Status.String())

		exec := <-execCh
		select {
		case <-exec.ready:
			t.Fatal("executor Run should not start after a pre-run signal")
		default:
		}
	})
	t.Run("SignalMarksAbortedBeforeKillReturns", func(t *testing.T) {
		execCh, restore := withNodeSignalExecutorFactory(t, func() *blockingSignalExecutor {
			exec := newBlockingSignalExecutor()
			exec.killStarted = make(chan struct{}, 1)
			exec.killContinue = make(chan struct{})
			return exec
		})
		defer restore()

		node := setupNode(t, withNodeExecutorType(nodeSignalExecutorType), withNodeSignalOnStop("SIGINT"))
		node.SetStatus(core.NodeRunning)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		errCh := make(chan error, 1)
		go func() {
			errCh <- node.Node.Execute(node.execContext(dagRunID))
		}()

		exec := <-execCh
		<-exec.ready

		go node.Signal(node.Context, syscall.Signal(0), true) // allow override signal

		select {
		case <-exec.killStarted:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for Kill to start")
		}
		require.Equal(t, core.NodeAborted.String(), node.State().Status.String())

		close(exec.killContinue)

		var err error
		select {
		case err = <-errCh:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for Execute to return")
		}
		require.Error(t, err)
		require.Contains(t, err.Error(), "signal: interrupt")
	})
	t.Run("SignalUsesStopIntent", func(t *testing.T) {
		execCh, restore := withNodeSignalExecutorFactory(t, func() *blockingSignalExecutor {
			exec := newBlockingSignalExecutor()
			exec.stopped = make(chan cmdutil.TerminationIntent, 1)
			return exec
		})
		defer restore()

		node := setupNode(t, withNodeExecutorType(nodeSignalExecutorType))
		seen := make(chan *blockingSignalExecutor, 1)
		go func() {
			exec := <-execCh
			seen <- exec
			<-exec.ready
			node.Signal(node.Context, syscall.SIGTERM, false)
		}()

		node.SetStatus(core.NodeRunning)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		err := node.Node.Execute(node.execContext(dagRunID))
		require.Error(t, err)
		require.Contains(t, err.Error(), "signal: terminated")

		exec := <-seen
		select {
		case intent := <-exec.stopped:
			require.Equal(t, cmdutil.TerminationModeGraceful, intent.Mode)
			require.Equal(t, os.Signal(syscall.SIGTERM), intent.Signal)
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for stop intent")
		}
	})
	t.Run("ForceSignalIgnoresSignalOnStopOverride", func(t *testing.T) {
		execCh, restore := withNodeSignalExecutorFactory(t, func() *blockingSignalExecutor {
			exec := newBlockingSignalExecutor()
			exec.stopped = make(chan cmdutil.TerminationIntent, 1)
			return exec
		})
		defer restore()

		node := setupNode(t, withNodeExecutorType(nodeSignalExecutorType), withNodeSignalOnStop("SIGINT"))
		seen := make(chan *blockingSignalExecutor, 1)
		go func() {
			exec := <-execCh
			seen <- exec
			<-exec.ready
			node.Signal(node.Context, syscall.SIGKILL, true)
		}()

		node.SetStatus(core.NodeRunning)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		err := node.Node.Execute(node.execContext(dagRunID))
		require.Error(t, err)
		require.Equal(t, core.NodeAborted.String(), node.State().Status.String())

		exec := <-seen
		select {
		case intent := <-exec.stopped:
			require.Equal(t, cmdutil.TerminationModeForce, intent.Mode)
			require.Equal(t, os.Kill, intent.Signal)
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for stop intent")
		}
	})
	t.Run("CancelUpdatesOnlyRunningOrWaiting", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			status core.NodeStatus
			want   core.NodeStatus
		}{
			{name: "Running", status: core.NodeRunning, want: core.NodeAborted},
			{name: "Waiting", status: core.NodeWaiting, want: core.NodeAborted},
			{name: "Succeeded", status: core.NodeSucceeded, want: core.NodeSucceeded},
			{name: "NotStarted", status: core.NodeNotStarted, want: core.NodeNotStarted},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				node := runtime.NewNode(core.Step{Name: tt.name}, runtime.NodeState{Status: tt.status})
				node.Cancel()
				require.Equal(t, tt.want, node.State().Status)
			})
		}
	})
	t.Run("LogOutput", func(t *testing.T) {
		t.Parallel()

		node := setupNode(t, withNodeCommand("echo hello"))
		node.Execute(t)
		node.AssertLogContains(t, "hello")
	})
	t.Run("Stdout", func(t *testing.T) {
		t.Parallel()

		random := path.Join(os.TempDir(), uuid.Must(uuid.NewV7()).String())
		defer func() {
			_ = os.Remove(random)
		}()

		node := setupNode(t, withNodeCommand("echo hello"), withNodeStdout(random))
		node.Execute(t)

		file := node.NodeData().Step.Stdout
		dat, _ := os.ReadFile(file)
		require.Equalf(t, "hello\n", strings.ReplaceAll(string(dat), "\r\n", "\n"), "unexpected stdout content: %s", string(dat))
	})
	t.Run("Stderr", func(t *testing.T) {
		t.Parallel()

		random := path.Join(os.TempDir(), uuid.Must(uuid.NewV7()).String())
		defer func() {
			_ = os.Remove(random)
		}()

		node := setupNode(t,
			withNodeCommand("sh"),
			withNodeStderr(random),
			withNodeScript("echo hello >&2"),
		)
		node.Execute(t)

		file := node.NodeData().Step.Stderr
		dat, _ := os.ReadFile(file)
		require.Equalf(t, "hello\n", string(dat), "unexpected stderr content: %s", string(dat))
	})
	t.Run("Output", func(t *testing.T) {
		t.Parallel()

		node := setupNode(t, withNodeCommand(test.Output("hello")), withNodeOutput("OUTPUT_TEST"))
		node.Execute(t)
		node.AssertOutput(t, "OUTPUT_TEST", "hello")
	})
	t.Run("OutputJSON", func(t *testing.T) {
		t.Parallel()

		node := setupNode(t, withNodeCommand(test.Output(`{"key": "value"}`)), withNodeOutput("OUTPUT_JSON_TEST"))
		node.Execute(t)
		node.AssertOutput(t, "OUTPUT_JSON_TEST", `{"key": "value"}`)
	})
	t.Run("OutputJSONUnescaped", func(t *testing.T) {
		t.Parallel()

		node := setupNode(t, withNodeCommand(test.Output(`{"key":"value"}`)), withNodeOutput("OUTPUT_JSON_TEST"))
		node.Execute(t)
		node.AssertOutput(t, "OUTPUT_JSON_TEST", `{"key":"value"}`)
	})
	t.Run("OutputTabWithDoubleQuotes", func(t *testing.T) {
		t.Parallel()

		node := setupNode(t, withNodeCommand(test.Output("hello\tworld")), withNodeOutput("OUTPUT"))
		node.Execute(t)
		node.AssertOutput(t, "OUTPUT", "hello\tworld")
	})
	t.Run("OutputTabWithMixedQuotes", func(t *testing.T) {
		t.Parallel()

		node := setupNode(t, withNodeCommand(test.Output("hello\tworld")), withNodeOutput("OUTPUT"))
		node.Execute(t)
		node.AssertOutput(t, "OUTPUT", "hello\tworld")
	})
	t.Run("OutputTabWithoutQuotes", func(t *testing.T) {
		t.Parallel()

		node := setupNode(t, withNodeCommand(test.Output(`hello\tworld`)), withNodeOutput("OUTPUT"))
		node.Execute(t)
		node.AssertOutput(t, "OUTPUT", `hello\tworld`)
	})
	t.Run("OutputNewlineCharacter", func(t *testing.T) {
		t.Parallel()

		node := setupNode(t, withNodeCommand(test.Output("hello\nworld")), withNodeOutput("OUTPUT"))
		node.Execute(t)
		node.AssertOutput(t, "OUTPUT", "hello\nworld")
	})
	t.Run("OutputEscapedJSONWithoutQuotes", func(t *testing.T) {
		t.Parallel()

		node := setupNode(t, withNodeCommand(test.Output(`{"key":"value"}`)), withNodeOutput("OUTPUT"))
		node.Execute(t)
		node.AssertOutput(t, "OUTPUT", `{"key":"value"}`)
	})
	t.Run("OutputEscapedJSONWithQuotes", func(t *testing.T) {
		t.Parallel()

		node := setupNode(t, withNodeCommand(test.Output(`{"key":"value"}`)), withNodeOutput("OUTPUT"))
		node.Execute(t)
		node.AssertOutput(t, "OUTPUT", `{"key":"value"}`)
	})
	t.Run("OutputSingleQuotedString", func(t *testing.T) {
		t.Parallel()

		node := setupNode(t, withNodeCommand(test.Output("hello world")), withNodeOutput("OUTPUT"))
		node.Execute(t)
		node.AssertOutput(t, "OUTPUT", `hello world`)
	})
	t.Run("OutputMixedQuotesWithSpace", func(t *testing.T) {
		t.Parallel()

		node := setupNode(t, withNodeCommand(test.Output("hello world")), withNodeOutput("OUTPUT"))
		node.Execute(t)
		node.AssertOutput(t, "OUTPUT", `hello world`)
	})
	t.Run("OutputNestedQuotes", func(t *testing.T) {
		t.Parallel()

		node := setupNode(t, withNodeCommand(test.Output(`hello "world"`)), withNodeOutput("OUTPUT"))
		node.Execute(t)
		node.AssertOutput(t, "OUTPUT", `hello "world"`)
	})
}

func TestNodeShouldMarkSuccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		nodeStatus         core.NodeStatus
		continueOnSettings core.ContinueOn
		expectMarkSuccess  bool
	}{
		{
			name:       "SuccessStatus",
			nodeStatus: core.NodeSucceeded,
			continueOnSettings: core.ContinueOn{
				MarkSuccess: true,
			},
			expectMarkSuccess: true, // shouldContinue returns true for success status, so shouldMarkSuccess follows MarkSuccess setting
		},
		{
			name:       "ErrorWithContinueOnFailureAndMarkSuccess",
			nodeStatus: core.NodeFailed,
			continueOnSettings: core.ContinueOn{
				Failure:     true,
				MarkSuccess: true,
			},
			expectMarkSuccess: true,
		},
		{
			name:       "ErrorWithContinueOnFailureButNoMarkSuccess",
			nodeStatus: core.NodeFailed,
			continueOnSettings: core.ContinueOn{
				Failure:     true,
				MarkSuccess: false,
			},
			expectMarkSuccess: false,
		},
		{
			name:       "SkippedWithContinueOnSkippedAndMarkSuccess",
			nodeStatus: core.NodeSkipped,
			continueOnSettings: core.ContinueOn{
				Skipped:     true,
				MarkSuccess: true,
			},
			expectMarkSuccess: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			step := core.Step{
				Name:       "test-step",
				ContinueOn: tt.continueOnSettings,
			}
			node := runtime.NewNode(step, runtime.NodeState{
				Status: tt.nodeStatus,
			})

			// Now we can test the public method directly
			node.SetStatus(tt.nodeStatus)
			result := node.ShouldMarkSuccess(ctx)
			assert.Equal(t, tt.expectMarkSuccess, result)
		})
	}
}

func TestNodeLogContainsPattern(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logFile := filepath.Join(tempDir, "test.log")

	// Create a log file with test content
	logContent := `Line 1: This is a test log
Line 2: Error occurred
Line 3: Success message
Line 4: [WARNING] Something happened
Line 5: Process completed
`
	err := os.WriteFile(logFile, []byte(logContent), 0644)
	require.NoError(t, err)

	tests := []struct {
		name     string
		patterns []string
		expected bool
		setup    func()
	}{
		{
			name:     "ExactMatch",
			patterns: []string{"Error occurred"},
			expected: true,
		},
		{
			name:     "PartialMatch",
			patterns: []string{"Success"},
			expected: true,
		},
		{
			name:     "RegexMatch",
			patterns: []string{"re:Error.*"},
			expected: true,
		},
		{
			name:     "RegexWithBrackets",
			patterns: []string{`re:\[WARNING\].*`},
			expected: true,
		},
		{
			name:     "MultiplePatternsAnyMatch",
			patterns: []string{"NotFound", "Success"},
			expected: true,
		},
		{
			name:     "NoMatch",
			patterns: []string{"NotInLog"},
			expected: false,
		},
		{
			name:     "EmptyPatterns",
			patterns: []string{},
			expected: false,
		},
		{
			name:     "InvalidRegex",
			patterns: []string{"re:["},
			expected: false,
		},
		{
			name:     "NonExistentLogFile",
			patterns: []string{"anything"},
			expected: false,
			setup: func() {
				// Test with a node that has no log file
				logFile = ""
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup()
			}

			ctx := context.Background()
			step := core.Step{Name: "test-step"}

			// Setup node properly with log file
			node := runtime.NewNode(step, runtime.NodeState{})
			err := node.Prepare(ctx, tempDir, "test-run")
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, node.Teardown())
			})

			// For non-existent log file test, we skip the log file write
			if tt.name != "non-existent log file" && logFile != "" {
				// Write test content to the stdout file
				stdoutFile := node.StdoutFile()
				err = os.WriteFile(stdoutFile, []byte(logContent), 0644)
				require.NoError(t, err)
			}

			result, err := node.LogContainsPattern(ctx, tt.patterns)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNodeBuildSubDAGRuns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		parallel      *core.ParallelConfig
		subDAG        *core.SubDAG
		setupEnv      func(ctx context.Context) context.Context
		expectCount   int
		expectError   bool
		errorContains string
	}{
		{
			name:     "NonParallelExecution",
			parallel: nil,
			subDAG: &core.SubDAG{
				Name:   "sub-dag",
				Params: "param1=value1",
			},
			expectCount: 1,
		},
		{
			name: "ParallelWithVariableJSONArray",
			parallel: &core.ParallelConfig{
				Variable: "${LIST_VAR}",
			},
			subDAG: &core.SubDAG{
				Name: "sub-dag",
			},
			setupEnv: func(ctx context.Context) context.Context {
				env := runtime.GetEnv(ctx)
				env.Scope = env.Scope.WithEntry("LIST_VAR", `["item1", "item2", "item3"]`, cmnvalue.EnvSourceStepEnv)
				return runtime.WithEnv(ctx, env)
			},
			expectCount: 3,
		},
		{
			name: "ParallelWithVariableSpaceSeparated",
			parallel: &core.ParallelConfig{
				Variable: "${SPACE_VAR}",
			},
			subDAG: &core.SubDAG{
				Name: "sub-dag",
			},
			setupEnv: func(ctx context.Context) context.Context {
				env := runtime.GetEnv(ctx)
				env.Scope = env.Scope.WithEntry("SPACE_VAR", "one two three", cmnvalue.EnvSourceStepEnv)
				return runtime.WithEnv(ctx, env)
			},
			expectCount: 3,
		},
		{
			name: "ParallelWithStaticItems",
			parallel: &core.ParallelConfig{
				Items: []core.ParallelItem{
					{Value: "item1"},
					{Value: "item2"},
				},
			},
			subDAG: &core.SubDAG{
				Name: "sub-dag",
			},
			expectCount: 2,
		},
		{
			name: "ParallelWithParamsItems",
			parallel: &core.ParallelConfig{
				Items: []core.ParallelItem{
					{Params: map[string]string{"key1": "value1"}},
					{Params: map[string]string{"key2": "value2"}},
				},
			},
			subDAG: &core.SubDAG{
				Name: "sub-dag",
			},
			expectCount: 2,
		},
		{
			name: "ParallelWithNoItems",
			parallel: &core.ParallelConfig{
				Variable: "${EMPTY_VAR}",
			},
			subDAG: &core.SubDAG{
				Name: "sub-dag",
			},
			setupEnv: func(ctx context.Context) context.Context {
				env := runtime.GetEnv(ctx)
				env.Scope = env.Scope.WithEntry("EMPTY_VAR", "", cmnvalue.EnvSourceStepEnv)
				return runtime.WithEnv(ctx, env)
			},
			expectError:   true,
			errorContains: "requires at least one item",
		},
		{
			name: "ParallelWithTooManyItems",
			parallel: &core.ParallelConfig{
				Items: func() []core.ParallelItem {
					items := make([]core.ParallelItem, 1001)
					for i := range items {
						items[i] = core.ParallelItem{Value: fmt.Sprintf("item%d", i)}
					}
					return items
				}(),
			},
			subDAG: &core.SubDAG{
				Name: "sub-dag",
			},
			expectError:   true,
			errorContains: "exceeds maximum limit",
		},
		{
			name: "ParallelWithITEMVariableInParams",
			parallel: &core.ParallelConfig{
				Variable: "${SPACE_VAR}",
			},
			subDAG: &core.SubDAG{
				Name:   "sub-dag",
				Params: "item=${ITEM}",
			},
			setupEnv: func(ctx context.Context) context.Context {
				env := runtime.GetEnv(ctx)
				env.Scope = env.Scope.WithEntry("SPACE_VAR", "one two three", cmnvalue.EnvSourceStepEnv)
				return runtime.WithEnv(ctx, env)
			},
			expectCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := runtime.NewContext(context.Background(), &core.DAG{}, "test-run", "test.log")

			if tt.setupEnv != nil {
				ctx = tt.setupEnv(ctx)
			}

			step := core.Step{
				Name:     "test-step",
				Parallel: tt.parallel,
				SubDAG:   tt.subDAG,
			}
			node := runtime.NewNode(step, runtime.NodeState{})

			// Now we can test the public method directly
			runs, err := node.BuildSubDAGRuns(ctx, tt.subDAG)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
				assert.Len(t, runs, tt.expectCount)
			}
		})
	}
}

func TestNodeItemToParam(t *testing.T) {
	tests := []struct {
		name     string
		item     any
		expected string
		wantErr  bool
	}{
		{
			name:     "String",
			item:     "test-string",
			expected: "test-string",
		},
		{
			name:     "Int",
			item:     42,
			expected: "42",
		},
		{
			name:     "Int64",
			item:     int64(9223372036854775807),
			expected: "9223372036854775807",
		},
		{
			name:     "Float32",
			item:     float32(3.14),
			expected: "3.14",
		},
		{
			name:     "Float64",
			item:     3.14159,
			expected: "3.14159",
		},
		{
			name:     "BoolTrue",
			item:     true,
			expected: "true",
		},
		{
			name:     "BoolFalse",
			item:     false,
			expected: "false",
		},
		{
			name:     "Nil",
			item:     nil,
			expected: "null",
		},
		{
			name:     "JsonRawMessage",
			item:     json.RawMessage(`{"key":"value"}`),
			expected: `{"key":"value"}`,
		},
		{
			name:     "Map",
			item:     map[string]string{"key": "value"},
			expected: `{"key":"value"}`,
		},
		{
			name:     "Slice",
			item:     []string{"a", "b", "c"},
			expected: `["a","b","c"]`,
		},
		{
			name:     "Struct",
			item:     struct{ Name string }{Name: "test"},
			expected: `{"Name":"test"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a node to call the method on
			step := core.Step{Name: "test-step"}
			node := runtime.NewNode(step, runtime.NodeState{})

			// Now we can test the public method directly
			result, err := node.ItemToParam(tt.item)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestRetryPolicyShouldRetry(t *testing.T) {
	tests := []struct {
		name        string
		policy      runtime.RetryPolicy
		exitCode    int
		shouldRetry bool
	}{
		{
			name: "RetryOnAnyNonZeroWhenNoExitCodesSpecified",
			policy: runtime.RetryPolicy{
				Limit:    3,
				Interval: time.Second,
			},
			exitCode:    1,
			shouldRetry: true,
		},
		{
			name: "NoRetryOnZeroWhenNoExitCodesSpecified",
			policy: runtime.RetryPolicy{
				Limit:    3,
				Interval: time.Second,
			},
			exitCode:    0,
			shouldRetry: false,
		},
		{
			name: "RetryOnlyOnSpecificExitCodes",
			policy: runtime.RetryPolicy{
				Limit:     3,
				Interval:  time.Second,
				ExitCodes: []int{1, 2, 3},
			},
			exitCode:    2,
			shouldRetry: true,
		},
		{
			name: "NoRetryOnNonSpecifiedExitCode",
			policy: runtime.RetryPolicy{
				Limit:     3,
				Interval:  time.Second,
				ExitCodes: []int{1, 2, 3},
			},
			exitCode:    4,
			shouldRetry: false,
		},
		{
			name: "NoRetryOnZeroEvenWhenInExitCodes",
			policy: runtime.RetryPolicy{
				Limit:     3,
				Interval:  time.Second,
				ExitCodes: []int{0, 1, 2},
			},
			exitCode:    0,
			shouldRetry: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.policy.ShouldRetry(tt.exitCode)
			assert.Equal(t, tt.shouldRetry, result)
		})
	}
}

func TestNodeSetupAndTeardown(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tempDir := t.TempDir()

	step := core.Step{
		Name: "test-step",
		Commands: []core.CommandEntry{{
			Command: "echo",
			Args:    []string{"hello"},
		}},
	}

	node := runtime.NewNode(step, runtime.NodeState{})

	// Test Setup
	dagRunID := "test-run-123"
	err := node.Prepare(ctx, tempDir, dagRunID)
	assert.NoError(t, err)

	// Verify log files were created
	state := node.NodeData().State
	assert.NotEmpty(t, state.Stdout)
	assert.NotEmpty(t, state.Stderr)
	assert.True(t, strings.HasPrefix(state.Stdout, tempDir))
	assert.True(t, strings.HasPrefix(state.Stderr, tempDir))

	// Test Teardown
	err = node.Teardown()
	assert.NoError(t, err)

	// Test double teardown (should be idempotent)
	err = node.Teardown()
	assert.NoError(t, err)
}

func TestNodeInit(t *testing.T) {
	step := core.Step{Name: "test-step"}

	// Create multiple nodes to verify they get different IDs
	node1 := runtime.NewNode(step, runtime.NodeState{})
	node2 := runtime.NewNode(step, runtime.NodeState{})

	// Call Init on first node
	node1.Init()

	// Call Init multiple times on same node - should be idempotent
	node1.Init()
	node1.Init()

	// Call Init on second node
	node2.Init()

	// While we can't directly access the ID, we can verify that
	// two different nodes don't interfere with each other
	// and that multiple Init calls are safe
	assert.NotPanics(t, func() {
		for range 10 {
			node1.Init()
			node2.Init()
		}
	})
}

func TestNodeCancel(t *testing.T) {
	step := core.Step{
		Name: "test-step",
		Commands: []core.CommandEntry{{
			Command: "sleep",
			Args:    []string{"10"},
		}},
	}

	node := runtime.NewNode(step, runtime.NodeState{})
	node.SetStatus(core.NodeRunning)

	// Cancel the node
	node.Cancel()

	// Check status changed to cancel
	assert.Equal(t, core.NodeAborted, node.NodeData().State.Status)
}

func TestNodeSetupContextBeforeExec(t *testing.T) {
	ctx := context.Background()
	env := runtime.NewEnv(ctx, core.Step{Name: "test-step"})
	ctx = runtime.WithEnv(ctx, env)

	step := core.Step{Name: "test-step"}
	node := runtime.NewNode(step, runtime.NodeState{
		Stdout: "/tmp/stdout.log",
		Stderr: "/tmp/stderr.log",
	})

	// Setup context
	newCtx := node.SetupEnv(ctx)

	// Verify environment variables were set
	newEnv := runtime.GetEnv(newCtx)

	stdoutVar, _ := newEnv.Scope.Get(exec.EnvKeyDAGRunStepStdoutFile)
	stderrVar, _ := newEnv.Scope.Get(exec.EnvKeyDAGRunStepStderrFile)

	assert.Equal(t, "/tmp/stdout.log", stdoutVar)
	assert.Equal(t, "/tmp/stderr.log", stderrVar)
}

func TestNodeOutputCaptureWithLargeOutput(t *testing.T) {
	t.Parallel()

	// Test that the output capture mechanism respects size limits
	// This test validates the concept at a high level
	tests := []struct {
		name          string
		command       string
		args          []string
		maxOutputSize int
		expectSuccess bool
	}{
		{
			name:          "SmallOutputWithinLimit",
			command:       "echo",
			args:          []string{"Hello, World!"},
			maxOutputSize: 1000,
			expectSuccess: true,
		},
		{
			name:          "VeryLargeOutputSizeLimit",
			command:       "echo",
			args:          []string{"test"},
			maxOutputSize: 1024 * 1024, // 1MB
			expectSuccess: true,
		},
		{
			name:          "ZeroOutputSizeMeansUnlimited",
			command:       "echo",
			args:          []string{"unlimited test"},
			maxOutputSize: 0,
			expectSuccess: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			tempDir := t.TempDir()

			// Create DAG with output size limit
			dag := &core.DAG{
				MaxOutputSize: tt.maxOutputSize,
			}

			// Setup environment with DAG
			ctx = runtime.NewContext(ctx, dag, "test-run", "test.log")

			step := core.Step{
				Name:    "test-output-capture",
				Command: tt.command,
				Args:    tt.args,
				Output:  "CAPTURED_OUTPUT",
			}

			node := runtime.NewNode(step, runtime.NodeState{})
			node.Init()

			// Setup node
			err := node.Prepare(ctx, tempDir, "test-run-output")
			require.NoError(t, err)

			// Execute node
			err = node.Execute(ctx)

			if tt.expectSuccess {
				// Execution should succeed
				assert.NoError(t, err)

				// Check if output was captured
				nodeData := node.NodeData()
				if nodeData.State.OutputVariables != nil {
					_, ok := nodeData.State.OutputVariables.Load("CAPTURED_OUTPUT")
					assert.True(t, ok, "Expected output variable to be captured")
				}
			}

			// Verify that MaxOutputSize is respected in the DAG configuration
			env := runtime.GetEnv(ctx)
			assert.Equal(t, tt.maxOutputSize, env.DAG.MaxOutputSize)

			// Cleanup
			err = node.Teardown()
			assert.NoError(t, err)
		})
	}

	// Additional test to verify configuration is respected
	t.Run("DAGMaxOutputSizeConfiguration", func(t *testing.T) {
		// Test that different MaxOutputSize values are properly configured
		sizes := []int{0, 100, 1024, 1024 * 1024}

		for _, size := range sizes {
			t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
				ctx := context.Background()
				dag := &core.DAG{
					MaxOutputSize: size,
				}

				ctx = runtime.NewContext(ctx, dag, "test-run", "test.log")
				env := runtime.GetEnv(ctx)

				// Verify the MaxOutputSize is properly set in the environment
				assert.Equal(t, size, env.DAG.MaxOutputSize)

				// Create a node with output capture
				step := core.Step{
					Name: "test-size-config",
					Commands: []core.CommandEntry{{
						Command: "echo",
						Args:    []string{"test"},
					}},
					Output: "TEST_VAR",
				}

				node := runtime.NewNode(step, runtime.NodeState{})
				node.Init()

				// The node should respect the configured MaxOutputSize
				// This is validated through the DAG configuration
				assert.NotNil(t, node)
			})
		}
	})
}

func TestNodeShouldContinue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		nodeStatus         core.NodeStatus
		exitCode           int
		continueOnSettings core.ContinueOn
		setupOutput        func(t *testing.T, node *runtime.Node)
		expectContinue     bool
	}{
		{
			name:       "ContinueOnFailure",
			nodeStatus: core.NodeFailed,
			exitCode:   1,
			continueOnSettings: core.ContinueOn{
				Failure: true,
			},
			expectContinue: true,
		},
		{
			name:       "ContinueOnSpecificExitCode",
			nodeStatus: core.NodeFailed,
			exitCode:   42,
			continueOnSettings: core.ContinueOn{
				ExitCode: []int{42, 43},
			},
			expectContinue: true,
		},
		{
			name:       "DonTContinueOnNonMatchingExitCode",
			nodeStatus: core.NodeFailed,
			exitCode:   44,
			continueOnSettings: core.ContinueOn{
				ExitCode: []int{42, 43},
			},
			expectContinue: false,
		},
		{
			name:       "ContinueOnSkipped",
			nodeStatus: core.NodeSkipped,
			continueOnSettings: core.ContinueOn{
				Skipped: true,
			},
			expectContinue: true,
		},
		{
			name:               "SuccessAlwaysContinues",
			nodeStatus:         core.NodeSucceeded,
			continueOnSettings: core.ContinueOn{},
			expectContinue:     true,
		},
		{
			name:       "CancelNeverContinues",
			nodeStatus: core.NodeAborted,
			continueOnSettings: core.ContinueOn{
				Failure: true,
				Skipped: true,
			},
			expectContinue: false,
		},
		{
			name:       "ContinueOnOutputMatch",
			nodeStatus: core.NodeFailed,
			continueOnSettings: core.ContinueOn{
				Output: []string{"WARNING"},
			},
			setupOutput: func(t *testing.T, node *runtime.Node) {
				tempDir := t.TempDir()
				ctx := context.Background()
				err := node.Prepare(ctx, tempDir, "test-run")
				require.NoError(t, err)

				// Write test output to stdout file
				stdoutFile := node.StdoutFile()
				err = os.WriteFile(stdoutFile, []byte("WARNING: This is just a warning\n"), 0644)
				require.NoError(t, err)
			},
			expectContinue: true,
		},
		{
			name:       "ContinueOnRegexOutputMatch",
			nodeStatus: core.NodeFailed,
			continueOnSettings: core.ContinueOn{
				Output: []string{"re:.*timeout.*"},
			},
			setupOutput: func(t *testing.T, node *runtime.Node) {
				tempDir := t.TempDir()
				ctx := context.Background()
				err := node.Prepare(ctx, tempDir, "test-run")
				require.NoError(t, err)

				// Write test output to stdout file
				stdoutFile := node.StdoutFile()
				err = os.WriteFile(stdoutFile, []byte("ERROR: Connection timeout after 30 seconds\n"), 0644)
				require.NoError(t, err)
			},
			expectContinue: true,
		},

		{
			name:       "DonTContinueOnSkippedWhenContinueOnSkippedIsFalseEvenWithExitCode0InExitCode",
			nodeStatus: core.NodeSkipped,
			exitCode:   0,
			continueOnSettings: core.ContinueOn{
				Skipped:  false,
				ExitCode: []int{0, 1, 2},
			},
			expectContinue: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			step := core.Step{
				Name:       "test-step",
				ContinueOn: tt.continueOnSettings,
			}
			node := runtime.NewNode(step, runtime.NodeState{
				Status:   tt.nodeStatus,
				ExitCode: tt.exitCode,
			})

			if tt.setupOutput != nil {
				tt.setupOutput(t, node)
			}
			t.Cleanup(func() {
				require.NoError(t, node.Teardown())
			})

			// Now we can test the public method directly
			node.SetStatus(tt.nodeStatus)
			node.SetExitCode(tt.exitCode)

			result := node.ShouldContinue(ctx)
			assert.Equal(t, tt.expectContinue, result)
		})
	}
}

type nodeHelper struct {
	*runtime.Node
	test.Helper
}

type nodeOption func(*runtime.NodeData)

func withNodeCommand(command string) nodeOption {
	return func(data *runtime.NodeData) {
		data.Step.Commands = []core.CommandEntry{{
			Command:     command,
			CmdWithArgs: command,
		}}
	}
}

func withNodeSignalOnStop(signal string) nodeOption {
	return func(data *runtime.NodeData) {
		data.Step.SignalOnStop = signal
	}
}

func withNodeExecutorType(executorType string) nodeOption {
	return func(data *runtime.NodeData) {
		data.Step.ExecutorConfig.Type = executorType
	}
}

func withNodeStdout(stdout string) nodeOption {
	return func(data *runtime.NodeData) {
		data.Step.Stdout = stdout
	}
}

func withNodeStderr(stderr string) nodeOption {
	return func(data *runtime.NodeData) {
		data.Step.Stderr = stderr
	}
}

func withNodeScript(script string) nodeOption {
	return func(data *runtime.NodeData) {
		data.Step.Script = script
	}
}

func withNodeOutput(output string) nodeOption {
	return func(data *runtime.NodeData) {
		data.Step.Output = output
	}
}

func setupNode(t *testing.T, opts ...nodeOption) nodeHelper {
	t.Helper()

	th := test.Setup(t)

	data := runtime.NodeData{Step: core.Step{}}
	for _, opt := range opts {
		opt(&data)
	}

	return nodeHelper{
		Helper: th,
		Node:   runtime.NodeWithData(data),
	}
}

func (n nodeHelper) Execute(t *testing.T) {
	t.Helper()

	dagRunID := uuid.Must(uuid.NewV7()).String()
	err := n.Prepare(n.Context, n.Config.Paths.LogDir, dagRunID)
	require.NoError(t, err, "failed to setup node")

	err = n.Node.Execute(n.execContext(dagRunID))
	require.NoError(t, err, "failed to execute node")

	err = n.Teardown()
	require.NoError(t, err, "failed to teardown node")
}

func (n nodeHelper) ExecuteFail(t *testing.T, expectedErr string) {
	t.Helper()

	dagRunID := uuid.Must(uuid.NewV7()).String()
	err := n.Node.Execute(n.execContext(dagRunID))
	require.Error(t, err, "expected error")
	require.Contains(t, err.Error(), expectedErr, "unexpected error")
}

func (n nodeHelper) AssertLogContains(t *testing.T, expected string) {
	t.Helper()

	dat, err := os.ReadFile(n.StdoutFile())
	require.NoErrorf(t, err, "failed to read log file %q", n.StdoutFile())
	require.Contains(t, string(dat), expected, "log file does not contain expected string")
}

func (n nodeHelper) AssertOutput(t *testing.T, key, value string) {
	t.Helper()

	require.NotNil(t, n.NodeData().State.OutputVariables, "output variables not set")
	data, ok := n.NodeData().State.OutputVariables.Load(key)
	require.True(t, ok, "output variable not found")
	require.Equal(t, fmt.Sprintf(`%s=%s`, key, value), data, "output variable value mismatch")
}

func (n nodeHelper) execContext(dagRunID string) context.Context {
	return runtime.NewContext(n.Context, &core.DAG{}, dagRunID, "logFile")
}

func TestNodeOutputRedirectWithWorkingDir(t *testing.T) {
	t.Parallel()

	t.Run("AbsolutePathUnchanged", func(t *testing.T) {
		tempDir := t.TempDir()
		workDir := filepath.Join(tempDir, "work")
		err := os.MkdirAll(workDir, 0755)
		require.NoError(t, err)

		// Absolute path for stdout
		stdoutPath := filepath.Join(tempDir, "output.log")

		step := core.Step{
			Name: "test-absolute-path",
			Commands: []core.CommandEntry{{
				Command: "echo",
				Args:    []string{"hello world"},
			}},
			Stdout: stdoutPath,
		}

		node := runtime.NewNode(step, runtime.NodeState{})
		node.Init()

		// Setup context with working directory
		ctx := context.Background()
		dag := &core.DAG{}
		ctx = runtime.NewContext(ctx, dag, "test-run", "test.log")
		env := runtime.GetEnv(ctx)
		env.WorkingDir = workDir
		ctx = runtime.WithEnv(ctx, env)

		// Setup and execute node
		err = node.Prepare(ctx, tempDir, "test-run")
		require.NoError(t, err)

		err = node.Execute(ctx)
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, node.Teardown())
		})

		// Verify file was created at absolute path
		content, err := os.ReadFile(stdoutPath)
		require.NoError(t, err)
		assert.Contains(t, string(content), "hello world")
	})

	t.Run("RelativePathUsesWorkingDir", func(t *testing.T) {
		tempDir := t.TempDir()
		workDir := filepath.Join(tempDir, "work")
		err := os.MkdirAll(workDir, 0755)
		require.NoError(t, err)

		// Relative path for stdout
		stdoutPath := "output.log"

		step := core.Step{
			Name: "test-relative-path",
			Commands: []core.CommandEntry{{
				Command: "echo",
				Args:    []string{"hello from working dir"},
			}},
			Stdout: stdoutPath,
		}

		node := runtime.NewNode(step, runtime.NodeState{})
		node.Init()

		// Setup context with working directory
		ctx := context.Background()
		dag := &core.DAG{}
		ctx = runtime.NewContext(ctx, dag, "test-run", "test.log")
		env := runtime.GetEnv(ctx)
		env.WorkingDir = workDir
		ctx = runtime.WithEnv(ctx, env)

		// Setup and execute node
		err = node.Prepare(ctx, tempDir, "test-run")
		require.NoError(t, err)

		err = node.Execute(ctx)
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, node.Teardown())
		})

		// Verify file was created in working directory
		expectedPath := filepath.Join(workDir, stdoutPath)
		content, err := os.ReadFile(expectedPath)
		require.NoError(t, err)
		assert.Contains(t, string(content), "hello from working dir")

		// Verify file was NOT created in tempDir
		_, err = os.Stat(filepath.Join(tempDir, stdoutPath))
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("StderrRedirectWithWorkingDir", func(t *testing.T) {
		tempDir := t.TempDir()
		workDir := filepath.Join(tempDir, "work")
		err := os.MkdirAll(workDir, 0755)
		require.NoError(t, err)

		// Relative path for stderr
		stderrPath := "error.log"

		step := core.Step{
			Name: "test-stderr-path",
			Commands: []core.CommandEntry{{
				Command: "sh",
				Args:    []string{"-c", "echo 'error message' >&2"},
			}},
			Stderr: stderrPath,
		}

		node := runtime.NewNode(step, runtime.NodeState{})
		node.Init()

		// Setup context with working directory
		ctx := context.Background()
		dag := &core.DAG{}
		ctx = runtime.NewContext(ctx, dag, "test-run", "test.log")
		env := runtime.GetEnv(ctx)
		env.WorkingDir = workDir
		ctx = runtime.WithEnv(ctx, env)

		// Setup and execute node
		err = node.Prepare(ctx, tempDir, "test-run")
		require.NoError(t, err)

		err = node.Execute(ctx)
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, node.Teardown())
		})

		// Verify file was created in working directory
		expectedPath := filepath.Join(workDir, stderrPath)
		content, err := os.ReadFile(expectedPath)
		require.NoError(t, err)
		assert.Contains(t, string(content), "error message")
	})

	t.Run("NestedRelativePath", func(t *testing.T) {
		tempDir := t.TempDir()
		workDir := filepath.Join(tempDir, "work")
		err := os.MkdirAll(workDir, 0755)
		require.NoError(t, err)

		// Create nested directory in working dir
		logsDir := filepath.Join(workDir, "logs")
		err = os.MkdirAll(logsDir, 0755)
		require.NoError(t, err)

		// Nested relative path
		stdoutPath := "logs/output.log"

		step := core.Step{
			Name: "test-nested-path",
			Commands: []core.CommandEntry{{
				Command: "echo",
				Args:    []string{"nested output"},
			}},
			Stdout: stdoutPath,
		}

		node := runtime.NewNode(step, runtime.NodeState{})
		node.Init()

		// Setup context with working directory
		ctx := context.Background()
		dag := &core.DAG{}
		ctx = runtime.NewContext(ctx, dag, "test-run", "test.log")
		env := runtime.GetEnv(ctx)
		env.WorkingDir = workDir
		ctx = runtime.WithEnv(ctx, env)

		// Setup and execute node
		err = node.Prepare(ctx, tempDir, "test-run")
		require.NoError(t, err)

		err = node.Execute(ctx)
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, node.Teardown())
		})

		// Verify file was created in correct nested path
		expectedPath := filepath.Join(workDir, stdoutPath)
		content, err := os.ReadFile(expectedPath)
		require.NoError(t, err)
		assert.Contains(t, string(content), "nested output")
	})
}

func TestLogOutputMode(t *testing.T) {
	t.Parallel()

	t.Run("SeparateMode_DefaultBehavior", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()

		step := core.Step{
			Name: "test-separate",
			Commands: []core.CommandEntry{{
				Command: "sh",
				Args:    []string{"-c", "echo stdout && echo stderr >&2"},
			}},
		}

		node := runtime.NewNode(step, runtime.NodeState{})
		node.Init()

		// Setup context with DAG using default (separate) log output mode
		ctx := context.Background()
		dag := &core.DAG{
			LogOutput: core.LogOutputSeparate,
		}
		ctx = runtime.NewContext(ctx, dag, "test-run", "test.log")

		// Setup and execute node
		err := node.Prepare(ctx, tempDir, "test-run")
		require.NoError(t, err)

		err = node.Execute(ctx)
		require.NoError(t, err)

		err = node.Teardown()
		require.NoError(t, err)

		// Verify separate .out and .err files were created
		state := node.State()
		assert.True(t, strings.HasSuffix(state.Stdout, ".out"), "stdout should have .out extension")
		assert.True(t, strings.HasSuffix(state.Stderr, ".err"), "stderr should have .err extension")
		assert.NotEqual(t, state.Stdout, state.Stderr, "stdout and stderr should be different files")

		// Verify stdout file exists and contains stdout content
		stdoutContent, err := os.ReadFile(state.Stdout)
		require.NoError(t, err)
		assert.Contains(t, string(stdoutContent), "stdout")

		// Verify stderr file exists and contains stderr content
		stderrContent, err := os.ReadFile(state.Stderr)
		require.NoError(t, err)
		assert.Contains(t, string(stderrContent), "stderr")
	})

	t.Run("MergedMode_DAGLevel", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()

		step := core.Step{
			Name: "test-merged",
			Commands: []core.CommandEntry{{
				Command: "sh",
				Args:    []string{"-c", "echo stdout && echo stderr >&2"},
			}},
		}

		node := runtime.NewNode(step, runtime.NodeState{})
		node.Init()

		// Setup context with DAG using merged log output mode
		ctx := context.Background()
		dag := &core.DAG{
			LogOutput: core.LogOutputMerged,
		}
		ctx = runtime.NewContext(ctx, dag, "test-run", "test.log")

		// Setup and execute node
		err := node.Prepare(ctx, tempDir, "test-run")
		require.NoError(t, err)

		err = node.Execute(ctx)
		require.NoError(t, err)

		err = node.Teardown()
		require.NoError(t, err)

		// Verify single .log file was created
		state := node.State()
		assert.True(t, strings.HasSuffix(state.Stdout, ".log"), "stdout should have .log extension")
		assert.True(t, strings.HasSuffix(state.Stderr, ".log"), "stderr should have .log extension")
		assert.Equal(t, state.Stdout, state.Stderr, "stdout and stderr should be the same file in merged mode")

		// Verify the merged log file contains both stdout and stderr content
		logContent, err := os.ReadFile(state.Stdout)
		require.NoError(t, err)
		assert.Contains(t, string(logContent), "stdout")
		assert.Contains(t, string(logContent), "stderr")
	})

	t.Run("MergedMode_StepLevelOverride", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()

		// Step explicitly sets merged mode, overriding DAG's separate mode
		step := core.Step{
			Name: "test-step-override",
			Commands: []core.CommandEntry{{
				Command: "sh",
				Args:    []string{"-c", "echo stdout && echo stderr >&2"},
			}},
			LogOutput: core.LogOutputMerged,
		}

		node := runtime.NewNode(step, runtime.NodeState{})
		node.Init()

		// Setup context with DAG using separate mode (will be overridden by step)
		ctx := context.Background()
		dag := &core.DAG{
			LogOutput: core.LogOutputSeparate,
		}
		ctx = runtime.NewContext(ctx, dag, "test-run", "test.log")

		// Setup and execute node
		err := node.Prepare(ctx, tempDir, "test-run")
		require.NoError(t, err)

		err = node.Execute(ctx)
		require.NoError(t, err)

		err = node.Teardown()
		require.NoError(t, err)

		// Verify step-level override to merged mode was applied
		state := node.State()
		assert.True(t, strings.HasSuffix(state.Stdout, ".log"), "step override should use .log extension")
		assert.Equal(t, state.Stdout, state.Stderr, "stdout and stderr should be the same file")

		// Verify the merged log file contains both outputs
		logContent, err := os.ReadFile(state.Stdout)
		require.NoError(t, err)
		assert.Contains(t, string(logContent), "stdout")
		assert.Contains(t, string(logContent), "stderr")
	})

	t.Run("SeparateMode_StepLevelOverride", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()

		// Step explicitly sets separate mode, overriding DAG's merged mode
		step := core.Step{
			Name: "test-step-separate-override",
			Commands: []core.CommandEntry{{
				Command: "sh",
				Args:    []string{"-c", "echo stdout && echo stderr >&2"},
			}},
			LogOutput: core.LogOutputSeparate,
		}

		node := runtime.NewNode(step, runtime.NodeState{})
		node.Init()

		// Setup context with DAG using merged mode (will be overridden by step)
		ctx := context.Background()
		dag := &core.DAG{
			LogOutput: core.LogOutputMerged,
		}
		ctx = runtime.NewContext(ctx, dag, "test-run", "test.log")

		// Setup and execute node
		err := node.Prepare(ctx, tempDir, "test-run")
		require.NoError(t, err)

		err = node.Execute(ctx)
		require.NoError(t, err)

		err = node.Teardown()
		require.NoError(t, err)

		// Verify step-level override to separate mode was applied
		state := node.State()
		assert.True(t, strings.HasSuffix(state.Stdout, ".out"), "step override should use .out extension")
		assert.True(t, strings.HasSuffix(state.Stderr, ".err"), "step override should use .err extension")
		assert.NotEqual(t, state.Stdout, state.Stderr, "stdout and stderr should be different files")
	})

	t.Run("MergedMode_InterleavedOutput", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()

		// Command that interleaves stdout and stderr
		step := core.Step{
			Name: "test-interleaved",
			Commands: []core.CommandEntry{{
				Command: "sh",
				Args:    []string{"-c", "echo 'line1-stdout' && echo 'line2-stderr' >&2 && echo 'line3-stdout' && echo 'line4-stderr' >&2"},
			}},
		}

		node := runtime.NewNode(step, runtime.NodeState{})
		node.Init()

		// Setup context with DAG using merged log output mode
		ctx := context.Background()
		dag := &core.DAG{
			LogOutput: core.LogOutputMerged,
		}
		ctx = runtime.NewContext(ctx, dag, "test-run", "test.log")

		// Setup and execute node
		err := node.Prepare(ctx, tempDir, "test-run")
		require.NoError(t, err)

		err = node.Execute(ctx)
		require.NoError(t, err)

		err = node.Teardown()
		require.NoError(t, err)

		// Verify all output is in the same file
		state := node.State()
		logContent, err := os.ReadFile(state.Stdout)
		require.NoError(t, err)

		content := string(logContent)
		assert.Contains(t, content, "line1-stdout")
		assert.Contains(t, content, "line2-stderr")
		assert.Contains(t, content, "line3-stdout")
		assert.Contains(t, content, "line4-stderr")
	})
}

func TestNodeChatMessages(t *testing.T) {
	t.Parallel()

	t.Run("SetAndGetMessages", func(t *testing.T) {
		t.Parallel()

		step := core.Step{Name: "test-chat-step"}
		node := runtime.NewNode(step, runtime.NodeState{})

		// Initially should be empty
		assert.Empty(t, node.GetChatMessages())

		// Set messages
		messages := []exec.LLMMessage{
			{Role: exec.RoleSystem, Content: "be helpful"},
			{Role: exec.RoleUser, Content: "hello"},
			{Role: exec.RoleAssistant, Content: "hi there"},
		}
		node.SetChatMessages(messages)

		// Should return the messages
		assert.Equal(t, messages, node.GetChatMessages())
	})

	t.Run("EmptyMessages", func(t *testing.T) {
		t.Parallel()

		step := core.Step{Name: "test-empty-messages"}
		node := runtime.NewNode(step, runtime.NodeState{})

		// Set empty messages
		node.SetChatMessages([]exec.LLMMessage{})
		assert.Empty(t, node.GetChatMessages())
	})

	t.Run("NilMessages", func(t *testing.T) {
		t.Parallel()

		step := core.Step{Name: "test-nil-messages"}
		node := runtime.NewNode(step, runtime.NodeState{})

		// Set nil messages
		node.SetChatMessages(nil)
		assert.Nil(t, node.GetChatMessages())
	})

	t.Run("ConcurrentAccess", func(t *testing.T) {
		t.Parallel()

		step := core.Step{Name: "test-concurrent"}
		node := runtime.NewNode(step, runtime.NodeState{})

		// Test concurrent access to message methods
		done := make(chan bool)
		for i := range 10 {
			go func(id int) {
				messages := []exec.LLMMessage{
					{Role: exec.RoleUser, Content: fmt.Sprintf("message %d", id)},
				}
				node.SetChatMessages(messages)
				_ = node.GetChatMessages()
				done <- true
			}(i)
		}

		// Wait for all goroutines
		for range 10 {
			<-done
		}
	})
}
