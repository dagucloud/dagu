// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	exec1 "github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewSubDAGExecutor_LocalDAG(t *testing.T) {
	t.Parallel()

	// Create a context with environment
	ctx := context.Background()

	// Create a parent DAG with local DAGs
	parentDAG := &core.DAG{
		Name: "parent",
		LocalDAGs: map[string]*core.DAG{
			"local-child": {
				Name: "local-child",
				Steps: []core.Step{
					{Name: "step1", Commands: []core.CommandEntry{{Command: "echo", Args: []string{"hello"}}}},
				},
				YamlData: []byte("name: local-child\nsteps:\n  - name: step1\n    command: echo hello"),
			},
		},
	}

	// Set up the DAG context
	mockDB := new(mockDatabase)
	dagCtx := exec1.Context{
		DAG:        parentDAG,
		DB:         mockDB,
		RootDAGRun: exec1.NewDAGRunRef("parent", "root-123"),
		DAGRunID:   "parent-456",
	}
	ctx = exec1.WithContext(ctx, dagCtx)

	// Test creating executor for local DAG
	executor, err := NewSubDAGExecutor(ctx, "local-child")
	require.NoError(t, err)
	require.NotNil(t, executor)

	// Verify it has yaml data (indicating it's local)
	assert.Equal(t, "local-child", executor.DAG.Name)
	assert.NotEmpty(t, executor.tempFile)
	assert.Contains(t, executor.tempFile, "local-child")
	assert.Contains(t, executor.tempFile, ".yaml")

	// Verify the temp file was created
	assert.FileExists(t, executor.tempFile)

	// Read and verify the content
	content, err := os.ReadFile(executor.tempFile)
	require.NoError(t, err)
	assert.Equal(t, parentDAG.LocalDAGs["local-child"].YamlData, content)

	// Cleanup
	err = executor.Cleanup(ctx)
	assert.NoError(t, err)
	assert.NoFileExists(t, executor.tempFile)
}

func TestNewSubDAGExecutor_RegularDAG(t *testing.T) {
	t.Parallel()

	// Create a context with environment
	ctx := context.Background()

	// Create a parent DAG without local DAGs
	parentDAG := &core.DAG{
		Name: "parent",
	}

	// Set up the DAG context
	mockDB := new(mockDatabase)
	dagCtx := exec1.Context{
		DAG:        parentDAG,
		DB:         mockDB,
		RootDAGRun: exec1.NewDAGRunRef("parent", "root-123"),
		DAGRunID:   "parent-456",
	}
	ctx = exec1.WithContext(ctx, dagCtx)

	// Mock the database call
	expectedDAG := &core.DAG{
		Name:     "regular-child",
		Location: "/path/to/regular-child.yaml",
	}
	mockDB.On("GetDAG", ctx, "regular-child").Return(expectedDAG, nil)

	// Test creating executor for regular DAG
	executor, err := NewSubDAGExecutor(ctx, "regular-child")
	require.NoError(t, err)
	require.NotNil(t, executor)

	// Verify it doesn't have yaml data (not local)
	assert.Equal(t, "regular-child", executor.DAG.Name)
	assert.Empty(t, executor.tempFile)

	// Cleanup should do nothing for regular DAGs
	err = executor.Cleanup(ctx)
	assert.NoError(t, err)

	mockDB.AssertExpectations(t)
}

func TestNewSubDAGExecutor_NotFound(t *testing.T) {
	t.Parallel()

	// Create a context with environment
	ctx := context.Background()

	// Create a parent DAG without the requested local DAG
	parentDAG := &core.DAG{
		Name: "parent",
		LocalDAGs: map[string]*core.DAG{
			"other-child": {Name: "other-child"},
		},
	}

	// Set up the DAG context
	mockDB := new(mockDatabase)
	dagCtx := exec1.Context{
		DAG:        parentDAG,
		DB:         mockDB,
		RootDAGRun: exec1.NewDAGRunRef("parent", "root-123"),
		DAGRunID:   "parent-456",
	}
	ctx = exec1.WithContext(ctx, dagCtx)

	// Mock the database call to return not found
	mockDB.On("GetDAG", ctx, "non-existent").Return(nil, assert.AnError)

	// Test creating executor for non-existent DAG
	executor, err := NewSubDAGExecutor(ctx, "non-existent")
	assert.Error(t, err)
	assert.Nil(t, executor)
	assert.Contains(t, err.Error(), "failed to find DAG")

	mockDB.AssertExpectations(t)
}

// TestNewSubDAGExecutor_NilDB verifies that NewSubDAGExecutor returns a
// structured error wrapping exec.ErrDAGNotFound when the runtime context
// has no DAG store (rCtx.DB == nil), instead of panicking with a nil
// pointer dereference. The error message must include the
// worker_selector: local remediation hint.
func TestNewSubDAGExecutor_NilDB(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	parentDAG := &core.DAG{Name: "parent"}

	// Set up context with nil DB
	dagCtx := exec1.Context{
		DAG:        parentDAG,
		DB:         nil,
		RootDAGRun: exec1.NewDAGRunRef("parent", "root-123"),
		DAGRunID:   "parent-456",
	}
	ctx = exec1.WithContext(ctx, dagCtx)

	executor, err := NewSubDAGExecutor(ctx, "child-dag")
	require.Error(t, err)
	require.Nil(t, executor)
	assert.ErrorIs(t, err, exec1.ErrDAGNotFound)
	assert.Contains(t, err.Error(), "worker_selector: local")
}

// TestNewSubDAGExecutor_NilDAGReturn verifies that NewSubDAGExecutor
// returns a structured error wrapping exec.ErrDAGNotFound when the DAG
// store's GetDAG call resolves to a nil DAG without an explicit error,
// instead of passing nil down to newSubDAGExecutor and panicking.
func TestNewSubDAGExecutor_NilDAGReturn(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	parentDAG := &core.DAG{Name: "parent"}

	mockDB := new(mockDatabase)
	dagCtx := exec1.Context{
		DAG:        parentDAG,
		DB:         mockDB,
		RootDAGRun: exec1.NewDAGRunRef("parent", "root-123"),
		DAGRunID:   "parent-456",
	}
	ctx = exec1.WithContext(ctx, dagCtx)

	// Mock returns nil DAG with nil error
	mockDB.On("GetDAG", ctx, "child-dag").Return(nil, nil)

	executor, err := NewSubDAGExecutor(ctx, "child-dag")
	require.Error(t, err)
	require.Nil(t, executor)
	assert.ErrorIs(t, err, exec1.ErrDAGNotFound)
	assert.Contains(t, err.Error(), "worker_selector: local")

	mockDB.AssertExpectations(t)
}

func TestExecute_NoRunID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Set up the DAG context
	mockDB := new(mockDatabase)
	dagCtx := exec1.Context{
		DAG:        &core.DAG{Name: "parent"},
		DB:         mockDB,
		RootDAGRun: exec1.NewDAGRunRef("parent", "root-123"),
		DAGRunID:   "parent-456",
	}
	ctx = exec1.WithContext(ctx, dagCtx)

	executor := &SubDAGExecutor{
		DAG:    &core.DAG{Name: "test-child"},
		killed: make(chan struct{}),
	}

	// Build command without RunID
	runParams := RunParams{
		RunID: "", // Empty RunID
	}

	result, err := executor.Execute(ctx, runParams, "/work/dir")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "DAG run ID is not set")
}

func TestExecute_NoRootDAGRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Set up the DAG context without RootDAGRun
	mockDB := new(mockDatabase)
	dagCtx := exec1.Context{
		DAG: &core.DAG{Name: "parent"},
		DB:  mockDB,
		// RootDAGRun is zero value
		DAGRunID: "parent-456",
	}
	ctx = exec1.WithContext(ctx, dagCtx)

	executor := &SubDAGExecutor{
		DAG: &core.DAG{Name: "test-child"},
	}

	runParams := RunParams{RunID: "child-789"}

	result, err := executor.Execute(ctx, runParams, "/work/dir")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "root DAG run ID is not set")
}

func TestExecute_UsesInjectedSubWorkflowRunner(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagCtx := exec1.Context{
		DAG:        &core.DAG{Name: "parent"},
		RootDAGRun: exec1.NewDAGRunRef("parent", "root-123"),
		DAGRunID:   "parent-456",
	}
	ctx = exec1.WithContext(ctx, dagCtx)

	runner := &mockSubWorkflowRunner{
		shouldRun: true,
		runResult: &exec1.RunStatus{
			Name:     "test-child",
			DAGRunID: "child-789",
			Status:   core.Succeeded,
		},
	}
	executor := &SubDAGExecutor{
		DAG: &core.DAG{
			Name:           "test-child",
			YamlData:       []byte("name: test-child"),
			WorkerSelector: map[string]string{"role": "worker"},
		},
		subWorkflowRunner: runner,
		externalStepRetry: true,
		activeRuns:        make(map[string]context.CancelFunc),
		dagCtx:            dagCtx,
		killed:            make(chan struct{}),
	}

	result, err := executor.Execute(ctx, RunParams{RunID: "child-789", Params: "ITEM=1"}, "/work/dir")
	require.NoError(t, err)
	require.NotNil(t, result)

	require.Len(t, runner.runRequests, 1)
	req := runner.runRequests[0]
	assert.Equal(t, "child-789", req.RunID)
	assert.Equal(t, "ITEM=1", req.Params)
	assert.Equal(t, "/work/dir", req.WorkDir)
	assert.Equal(t, exec1.NewDAGRunRef("parent", "root-123"), req.RootDAGRun)
	assert.Equal(t, exec1.NewDAGRunRef("parent", "parent-456"), req.ParentDAGRun)
	assert.Equal(t, map[string]string{"role": "worker"}, req.WorkerSelector)
	assert.True(t, req.ExternalStepRetry)
	assert.NotContains(t, executor.activeRuns, "child-789")
}

func TestRetry_Distributed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagCtx := exec1.Context{
		DAG:        &core.DAG{Name: "parent"},
		RootDAGRun: exec1.NewDAGRunRef("parent", "root-123"),
		DAGRunID:   "parent-456",
	}
	ctx = exec1.WithContext(ctx, dagCtx)

	runner := &mockSubWorkflowRunner{
		shouldRun: true,
		retryResult: &exec1.RunStatus{
			Name:     "test-child",
			DAGRunID: "child-789",
			Status:   core.Succeeded,
		},
	}
	executor := &SubDAGExecutor{
		DAG: &core.DAG{
			Name:           "test-child",
			YamlData:       []byte("name: test-child"),
			WorkerSelector: map[string]string{"role": "worker"},
		},
		subWorkflowRunner: runner,
		externalStepRetry: true,
		activeRuns:        make(map[string]context.CancelFunc),
		dagCtx:            dagCtx,
		killed:            make(chan struct{}),
	}

	result, err := executor.Retry(ctx, RunParams{RunID: "child-789"}, "flaky", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, core.Succeeded, result.Status)

	require.Len(t, runner.retryRequests, 1)
	req := runner.retryRequests[0]
	assert.Equal(t, "flaky", req.StepName)
	assert.Equal(t, "child-789", req.RunID)
	assert.True(t, req.ExternalStepRetry)
	assert.NotContains(t, executor.activeRuns, "child-789")

	require.NoError(t, executor.Kill(os.Interrupt))
	assert.Equal(t, 0, runner.cancelCalled)
}

func TestSubDAGExecutor_ExecuteDoesNotDispatchAfterPreRunKill(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagCtx := exec1.Context{
		DAG:        &core.DAG{Name: "parent"},
		RootDAGRun: exec1.NewDAGRunRef("parent", "root-123"),
		DAGRunID:   "parent-456",
	}
	ctx = exec1.WithContext(ctx, dagCtx)

	runner := &mockSubWorkflowRunner{shouldRun: true}
	executor := &SubDAGExecutor{
		DAG: &core.DAG{
			Name:           "test-child",
			YamlData:       []byte("name: test-child"),
			WorkerSelector: map[string]string{"role": "worker"},
		},
		subWorkflowRunner: runner,
		activeRuns:        make(map[string]context.CancelFunc),
		dagCtx:            dagCtx,
		killed:            make(chan struct{}),
	}

	require.NoError(t, executor.Kill(os.Interrupt))

	result, err := executor.Execute(ctx, RunParams{RunID: "child-789"}, "")
	require.ErrorIs(t, err, errSubDAGCancelled)
	require.Nil(t, result)
	assert.Empty(t, runner.runRequests)
	assert.NotContains(t, executor.activeRuns, "child-789")
}

func TestRetry_NoRootDAGRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	mockDB := new(mockDatabase)
	dagCtx := exec1.Context{
		DAG:      &core.DAG{Name: "parent"},
		DB:       mockDB,
		DAGRunID: "parent-456",
	}
	ctx = exec1.WithContext(ctx, dagCtx)

	executor := &SubDAGExecutor{
		DAG:    &core.DAG{Name: "test-child"},
		killed: make(chan struct{}),
	}

	result, err := executor.Retry(ctx, RunParams{RunID: "child-789"}, "flaky", "/work/dir")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "root DAG run ID is not set")
}

func TestCleanup_LocalDAG(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Create a temporary file using t.TempDir() for automatic cleanup
	tempDir := t.TempDir()
	tempFile := filepath.Join(tempDir, "test.yaml")
	err := os.WriteFile(tempFile, []byte("test content"), 0600)
	require.NoError(t, err)

	executor := &SubDAGExecutor{
		DAG:      &core.DAG{Name: "test-child"},
		tempFile: tempFile,
		killed:   make(chan struct{}),
	}

	// Verify file exists
	assert.FileExists(t, tempFile)

	// Cleanup
	err = executor.Cleanup(ctx)
	assert.NoError(t, err)

	// Verify file is removed
	assert.NoFileExists(t, tempFile)
}

func TestCleanup_NonExistentFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	executor := &SubDAGExecutor{
		DAG:      &core.DAG{Name: "test-child"},
		tempFile: "/non/existent/file.yaml",
		killed:   make(chan struct{}),
	}

	// Cleanup should not error on non-existent file
	err := executor.Cleanup(ctx)
	assert.NoError(t, err)
}

func TestSubDAGExecutor_Kill_ActiveRunner(t *testing.T) {
	t.Parallel()

	dagCtx := exec1.Context{
		RootDAGRun: exec1.NewDAGRunRef("root-dag", "root-run-id"),
		DAGRunID:   "parent-run-id",
	}
	subDAG := &core.DAG{
		Name: "sub-dag",
	}
	runner := &mockSubWorkflowRunner{shouldRun: true}
	cancelCalled := false

	executor := &SubDAGExecutor{
		DAG:               subDAG,
		dagCtx:            dagCtx,
		subWorkflowRunner: runner,
		activeRuns: map[string]context.CancelFunc{
			"child-run": func() { cancelCalled = true },
		},
		killed: make(chan struct{}),
	}

	err := executor.Kill(os.Interrupt)

	assert.NoError(t, err)
	assert.True(t, cancelCalled)
	assert.Equal(t, 1, runner.cancelCalled)
}

func TestSubDAGExecutor_Kill_FallbackDB(t *testing.T) {
	t.Parallel()

	mockDB := new(mockDatabase)
	dagCtx := exec1.Context{
		DB:         mockDB,
		RootDAGRun: exec1.NewDAGRunRef("root-dag", "root-run-id"),
		DAGRunID:   "parent-run-id",
	}
	subDAG := &core.DAG{
		Name: "sub-dag",
	}

	executor := &SubDAGExecutor{
		DAG:    subDAG,
		dagCtx: dagCtx,
		activeRuns: map[string]context.CancelFunc{
			"child-run": func() {},
		},
		killed: make(chan struct{}),
	}

	mockDB.On("RequestChildCancel", mock.Anything, "child-run", dagCtx.RootDAGRun).Return(nil)

	err := executor.Kill(os.Interrupt)

	assert.NoError(t, err)
	mockDB.AssertExpectations(t)
}

func TestSubDAGExecutor_Kill_Empty(t *testing.T) {
	t.Parallel()

	// Create a mock database
	mockDB := new(mockDatabase)

	// Create a DAG context
	dagCtx := exec1.Context{
		DB:         mockDB,
		RootDAGRun: exec1.NewDAGRunRef("root-dag", "root-run-id"),
		DAGRunID:   "parent-run-id",
	}

	// Create a sub DAG
	subDAG := &core.DAG{
		Name: "sub-dag",
	}

	executor := &SubDAGExecutor{
		DAG:        subDAG,
		dagCtx:     dagCtx,
		activeRuns: make(map[string]context.CancelFunc),
		killed:     make(chan struct{}),
	}

	// Call Kill
	err := executor.Kill(os.Interrupt)

	// Verify no error
	assert.NoError(t, err)

	// Verify RequestChildCancel was NOT called
	mockDB.AssertNotCalled(t, "RequestChildCancel")
}

var _ exec1.Database = (*mockDatabase)(nil)

// mockDatabase is a mock implementation of core.Database
type mockDatabase struct {
	mock.Mock
}

type mockSubWorkflowRunner struct {
	shouldRun     bool
	runResult     *exec1.RunStatus
	runErr        error
	retryResult   *exec1.RunStatus
	retryErr      error
	runRequests   []SubWorkflowRequest
	retryRequests []SubWorkflowRetryRequest
	cancelCalled  int
}

func (m *mockSubWorkflowRunner) ShouldRun(context.Context, SubWorkflowRequest) bool {
	return m.shouldRun
}

func (m *mockSubWorkflowRunner) Run(_ context.Context, req SubWorkflowRequest) (*exec1.RunStatus, error) {
	m.runRequests = append(m.runRequests, req)
	return m.runResult, m.runErr
}

func (m *mockSubWorkflowRunner) Retry(_ context.Context, req SubWorkflowRetryRequest) (*exec1.RunStatus, error) {
	m.retryRequests = append(m.retryRequests, req)
	return m.retryResult, m.retryErr
}

func (m *mockSubWorkflowRunner) Cancel(context.Context, SubWorkflowCancelRequest) error {
	m.cancelCalled++
	return nil
}

func (m *mockDatabase) GetDAG(ctx context.Context, name string) (*core.DAG, error) {
	args := m.Called(ctx, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*core.DAG), args.Error(1)
}

func (m *mockDatabase) GetSubDAGRunStatus(ctx context.Context, dagRunID string, rootDAGRun exec1.DAGRunRef) (*exec1.RunStatus, error) {
	args := m.Called(ctx, dagRunID, rootDAGRun)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*exec1.RunStatus), args.Error(1)
}

// IsSubDAGRunCompleted implements core.Database.
func (m *mockDatabase) IsSubDAGRunCompleted(ctx context.Context, dagRunID string, rootDAGRun exec1.DAGRunRef) (bool, error) {
	args := m.Called(ctx, dagRunID, rootDAGRun)
	return args.Bool(0), args.Error(1)
}

// RequestChildCancel implements core.Database.
func (m *mockDatabase) RequestChildCancel(ctx context.Context, dagRunID string, rootDAGRun exec1.DAGRunRef) error {
	args := m.Called(ctx, dagRunID, rootDAGRun)
	return args.Error(0)
}
