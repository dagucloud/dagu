// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package subflow_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/collections"
	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/core/spec"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	filematerialization "github.com/dagucloud/dagu/v2/internal/persis/file/materialization"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/subflow"
	"github.com/dagucloud/dagu/v2/internal/test"
	dagutools "github.com/dagucloud/dagu/v2/internal/tools"
)

func TestLocalCancelRequestsStoredChildAttemptWhenInactive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := exec.NewDAGRunRef("root", "root-run")
	attempt := new(exec.MockDAGRunAttempt)
	attempt.On("Abort", ctx).Return(nil).Once()
	store := &localDAGRunStore{subAttempt: attempt}
	runner := subflow.NewLocal(runtime.Manager{}, nil, subflow.WithLocalDAGRunStore(store))

	err := runner.Cancel(ctx, executor.SubWorkflowCancelRequest{
		DAG:        &core.DAG{Name: "child"},
		RootDAGRun: root,
		RunID:      "child-run",
	})

	require.NoError(t, err)
	require.Equal(t, root, store.findRoot)
	require.Equal(t, "child-run", store.findRunID)
	attempt.AssertExpectations(t)
}

func TestLocalCancelIgnoresMissingStoredChildAttemptWhenInactive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := exec.NewDAGRunRef("root", "root-run")
	store := &localDAGRunStore{}
	runner := subflow.NewLocal(runtime.Manager{}, nil, subflow.WithLocalDAGRunStore(store))

	err := runner.Cancel(ctx, executor.SubWorkflowCancelRequest{
		DAG:        &core.DAG{Name: "child"},
		RootDAGRun: root,
		RunID:      "child-run",
	})

	require.NoError(t, err)
	require.Equal(t, root, store.findRoot)
	require.Equal(t, "child-run", store.findRunID)
}

func TestLocalCancelReturnsStoredChildAttemptLookupError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := exec.NewDAGRunRef("root", "root-run")
	findErr := errors.New("store unavailable")
	store := &localDAGRunStore{findErr: findErr}
	runner := subflow.NewLocal(runtime.Manager{}, nil, subflow.WithLocalDAGRunStore(store))

	err := runner.Cancel(ctx, executor.SubWorkflowCancelRequest{
		DAG:        &core.DAG{Name: "child"},
		RootDAGRun: root,
		RunID:      "child-run",
	})

	require.ErrorIs(t, err, findErr)
	require.ErrorContains(t, err, "failed to find child workflow attempt")
	require.Equal(t, root, store.findRoot)
	require.Equal(t, "child-run", store.findRunID)
}

func TestLocalRetryRejectsMissingRunDatabase(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := exec.NewDAGRunRef("root", "root-run")
	runner := subflow.NewLocal(runtime.Manager{}, nil)

	result, err := runner.Retry(ctx, executor.SubWorkflowRetryRequest{
		SubWorkflowRequest: executor.SubWorkflowRequest{
			DAG:        &core.DAG{Name: "child"},
			RootDAGRun: root,
			RunID:      "child-run",
		},
		StepName: "step-1",
	})

	require.Nil(t, result)
	require.ErrorContains(t, err, "child workflow status database is not configured")
}

func TestLocalRunRejectsBuildWorkflowOnRemoteWorker(t *testing.T) {
	t.Parallel()

	runner := subflow.NewLocal(
		runtime.Manager{},
		nil,
		subflow.WithLocalWorkerID("worker-1"),
	)
	result, err := runner.Run(context.Background(), executor.SubWorkflowRequest{
		DAG:        &core.DAG{Name: "child", Type: core.TypeBuild},
		RootDAGRun: exec.NewDAGRunRef("parent", "root-1"),
		RunID:      "child-run",
	})

	require.Nil(t, result)
	require.ErrorIs(t, err, dispatch.ErrBuildRequiresLocal)
}

func TestLocalRetryReadsStoredChildAttemptStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := exec.NewDAGRunRef("root", "root-run")
	readErr := errors.New("read status failed")
	attempt := new(exec.MockDAGRunAttempt)
	attempt.On("ReadStatus", ctx).Return(nil, readErr).Once()
	store := &localDAGRunStore{subAttempt: attempt}
	runner := subflow.NewLocal(runtime.Manager{}, nil, subflow.WithLocalDAGRunStore(store))

	result, err := runner.Retry(ctx, executor.SubWorkflowRetryRequest{
		SubWorkflowRequest: executor.SubWorkflowRequest{
			DAG:        &core.DAG{Name: "child"},
			RootDAGRun: root,
			RunID:      "child-run",
		},
		StepName: "step-1",
	})

	require.Nil(t, result)
	require.ErrorIs(t, err, readErr)
	require.ErrorContains(t, err, "failed to read child workflow status")
	require.Equal(t, root, store.findRoot)
	require.Equal(t, "child-run", store.findRunID)
	attempt.AssertExpectations(t)
}

func TestLocalRunWithoutStatusStoreStartsFresh(t *testing.T) {
	th := test.Setup(t)
	child := th.DAG(t, `name: local-no-store-child
steps:
  - name: work
    run: echo ok
`)
	root := exec.NewDAGRunRef("root", uuid.Must(uuid.NewV7()).String())
	runner := subflow.NewLocal(th.DAGRunMgr, th.DAGStore)

	result, err := runner.Run(th.Context, executor.SubWorkflowRequest{
		DAG:          child.DAG,
		RootDAGRun:   root,
		ParentDAGRun: root,
		RunID:        uuid.Must(uuid.NewV7()).String(),
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, core.Succeeded, result.Status)
}

func TestLocalRunPreservesBuildPathBaseFromCopiedDefinition(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("uses a POSIX command")
	}

	th := test.Setup(t)
	authoredDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(authoredDir, "source.txt"), []byte("source"), 0o600))
	authoredPath := filepath.Join(authoredDir, "child.yaml")
	require.NoError(t, os.WriteFile(authoredPath, []byte(`
name: build-child
type: build
steps:
  - id: build
    inputs:
      - name: source
        path: source.txt
    outputs:
      - name: artifact
        path: artifact.txt
    run: cp "${inputs.source}" "${outputs.artifact}"
`), 0o600))

	child, err := spec.Load(th.Context, authoredPath)
	require.NoError(t, err)
	copyDir := t.TempDir()
	child.Location = filepath.Join(copyDir, "child.yaml")
	require.NoError(t, os.WriteFile(child.Location, child.YamlData, 0o600))

	root := exec.NewDAGRunRef("parent", uuid.Must(uuid.NewV7()).String())
	ctx := exec.NewContext(
		th.Context,
		&core.DAG{Name: root.Name},
		root.ID,
		filepath.Join(t.TempDir(), "parent.log"),
		exec.WithMaterializationStore(filematerialization.New(filepath.Join(t.TempDir(), "materializations"))),
	)
	runner := subflow.NewLocal(th.DAGRunMgr, th.DAGStore)
	result, err := runner.Run(ctx, executor.SubWorkflowRequest{
		DAG:          child,
		RootDAGRun:   root,
		ParentDAGRun: root,
		RunID:        uuid.Must(uuid.NewV7()).String(),
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, core.Succeeded, result.Status)
	content, err := os.ReadFile(filepath.Join(authoredDir, "artifact.txt"))
	require.NoError(t, err)
	require.Equal(t, "source", string(content))
}

func TestLocalRunPreparesDeclaredTools(t *testing.T) {
	th := test.Setup(t)
	binDir := t.TempDir()
	toolPath := filepath.Join(binDir, "child-tool")
	toolScript := "#!/bin/sh\nexit 0\n"
	if goruntime.GOOS == "windows" {
		toolPath += ".cmd"
		toolScript = "@echo off\r\nexit /b 0\r\n"
	}
	require.NoError(t, os.WriteFile(toolPath, []byte(toolScript), 0o755))

	installer := &staticInstaller{
		manifest: &dagutools.Manifest{
			RootDir:      binDir,
			EnvDir:       binDir,
			BinDir:       binDir,
			ManifestFile: filepath.Join(binDir, "manifest.json"),
		},
	}
	child := th.DAG(t, `name: local-tools-child
tools:
  - test/child-tool@v1.0.0
steps:
  - name: use-tool
    run: |
      child-tool
`)
	root := exec.NewDAGRunRef("root", uuid.Must(uuid.NewV7()).String())
	runner := subflow.NewLocal(
		th.DAGRunMgr,
		th.DAGStore,
		subflow.WithLocalToolInstaller(installer),
	)

	result, err := runner.Run(th.Context, executor.SubWorkflowRequest{
		DAG:          child.DAG,
		RootDAGRun:   root,
		ParentDAGRun: root,
		RunID:        uuid.Must(uuid.NewV7()).String(),
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, core.Succeeded, result.Status)
}

func TestLocalRunReusesSucceededChildForExternalStepRetry(t *testing.T) {
	th := test.Setup(t)
	rootDAG := th.DAG(t, `name: retry-parent
steps:
  - name: child
    run: echo child
`)
	childDAG := th.DAG(t, `name: retry-child
steps:
  - name: work
    run: echo ok
`)

	const (
		rootRunID  = "root-run"
		childRunID = "child-run"
	)
	var outputVars collections.SyncMap
	outputVars.Store("RESULT", "RESULT=ok")
	childStatus := localRunStatus(childDAG.DAG, childRunID, core.Succeeded, core.NodeSucceeded)
	childStatus.Nodes[0].OutputVariables = &outputVars
	originalAttempt := createStoredRunningChildAttempt(
		t,
		th,
		rootDAG.DAG,
		childDAG.DAG,
		rootRunID,
		childRunID,
		childStatus,
	)

	rootRef := exec.NewDAGRunRef(rootDAG.Name, rootRunID)
	runner := subflow.NewLocal(
		th.DAGRunMgr,
		th.DAGStore,
		subflow.WithLocalDAGRunStore(th.DAGRunStore),
	)
	result, err := runner.Run(th.Context, executor.SubWorkflowRequest{
		DAG:               childDAG.DAG,
		RootDAGRun:        rootRef,
		ParentDAGRun:      rootRef,
		RunID:             childRunID,
		ExternalStepRetry: true,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, core.Succeeded, result.Status)
	require.Equal(t, "ok", result.Outputs["RESULT"])

	latestAttempt, err := th.DAGRunStore.FindSubAttempt(th.Context, rootRef, childRunID)
	require.NoError(t, err)
	require.Equal(t, originalAttempt.ID(), latestAttempt.ID())
}

func TestLocalRunRepairsStaleChildBeforeRetry(t *testing.T) {
	th := test.Setup(t)
	rootDAG := th.DAG(t, `name: stale-parent
steps:
  - name: child
    run: echo child
`)
	childDAG := th.DAG(t, `name: stale-child
steps:
  - name: work
    run: echo ok
`)

	rootRunID := "root-run"
	childRunID := "child-run"
	staleAt := time.Now().Add(-3 * time.Second)
	childStatus := localRunStatus(childDAG.DAG, childRunID, core.Running, core.NodeRunning)
	childStatus.WorkerID = "local"
	childStatus.StartedAt = exec.FormatTime(staleAt)
	childStatus.CreatedAt = staleAt.UnixMilli()
	createStoredRunningChildAttempt(t, th, rootDAG.DAG, childDAG.DAG, rootRunID, childRunID, childStatus)

	rootRef := exec.NewDAGRunRef(rootDAG.Name, rootRunID)
	runner := subflow.NewLocal(th.DAGRunMgr, th.DAGStore, subflow.WithLocalDAGRunStore(th.DAGRunStore))

	result, err := runner.Run(th.Context, executor.SubWorkflowRequest{
		DAG:          childDAG.DAG,
		RootDAGRun:   rootRef,
		ParentDAGRun: rootRef,
		RunID:        childRunID,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, core.Succeeded, result.Status)

	persisted, err := th.DAGRunMgr.FindSubDAGRunStatus(th.Context, rootRef, childRunID)
	require.NoError(t, err)
	require.Equal(t, core.Succeeded, persisted.Status)
}

func localRunStatus(dag *core.DAG, dagRunID string, dagStatus core.Status, nodeStatus core.NodeStatus) exec.DAGRunStatus {
	status := exec.InitialStatus(dag)
	status.DAGRunID = dagRunID
	status.Status = dagStatus
	status.StartedAt = exec.FormatTime(time.Now())
	status.CreatedAt = time.Now().UnixMilli()
	for _, node := range status.Nodes {
		node.Status = nodeStatus
	}
	return status
}

func createStoredRunningChildAttempt(
	t *testing.T,
	th test.Helper,
	rootDAG *core.DAG,
	childDAG *core.DAG,
	rootRunID string,
	childRunID string,
	status exec.DAGRunStatus,
) exec.DAGRunAttempt {
	t.Helper()

	ctx := th.Context
	rootRef := exec.NewDAGRunRef(rootDAG.Name, rootRunID)

	rootAttempt, err := th.DAGRunStore.CreateAttempt(ctx, rootDAG, time.Now(), rootRunID, exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, rootAttempt.Open(ctx))
	rootStatus := localRunStatus(rootDAG, rootRunID, core.Running, core.NodeRunning)
	rootStatus.AttemptID = rootAttempt.ID()
	rootStatus.AttemptKey = exec.GenerateAttemptKey(rootDAG.Name, rootRunID, rootDAG.Name, rootRunID, rootStatus.AttemptID)
	require.NoError(t, rootAttempt.Write(ctx, rootStatus))
	require.NoError(t, rootAttempt.Close(ctx))

	childAttempt, err := th.DAGRunStore.CreateSubAttempt(ctx, rootRef, childRunID)
	require.NoError(t, err)
	childAttempt.SetDAG(childDAG)
	require.NoError(t, childAttempt.Open(ctx))
	status.AttemptID = childAttempt.ID()
	status.AttemptKey = exec.GenerateAttemptKey(rootRef.Name, rootRef.ID, childDAG.Name, childRunID, status.AttemptID)
	status.Root = rootRef
	status.Parent = rootRef
	status.DAGRunID = childRunID
	require.NoError(t, childAttempt.Write(ctx, status))
	require.NoError(t, childAttempt.Close(ctx))
	return childAttempt
}

type localDAGRunStore struct {
	subAttempt exec.DAGRunAttempt
	findErr    error
	findRoot   exec.DAGRunRef
	findRunID  string
}

type staticInstaller struct {
	manifest *dagutools.Manifest
}

func (i *staticInstaller) Install(
	_ context.Context,
	_ *core.ToolConfig,
	_ dagutools.InstallOptions,
) (*dagutools.Manifest, error) {
	return i.manifest, nil
}

func (s *localDAGRunStore) CreateAttempt(context.Context, *core.DAG, time.Time, string, exec.NewDAGRunAttemptOptions) (exec.DAGRunAttempt, error) {
	return nil, nil
}

func (s *localDAGRunStore) RecentAttempts(context.Context, string, int) []exec.DAGRunAttempt {
	return nil
}

func (s *localDAGRunStore) LatestAttempt(context.Context, string) (exec.DAGRunAttempt, error) {
	return nil, exec.ErrDAGRunIDNotFound
}

func (s *localDAGRunStore) ListStatuses(context.Context, ...exec.ListDAGRunStatusesOption) ([]*exec.DAGRunStatus, error) {
	return nil, nil
}

func (s *localDAGRunStore) ListStatusesPage(context.Context, ...exec.ListDAGRunStatusesOption) (exec.DAGRunStatusPage, error) {
	return exec.DAGRunStatusPage{}, nil
}

func (s *localDAGRunStore) CompareAndSwapLatestAttemptStatus(
	context.Context,
	exec.DAGRunRef,
	string,
	core.Status,
	func(*exec.DAGRunStatus) error,
	...exec.CompareAndSwapStatusOption,
) (*exec.DAGRunStatus, bool, error) {
	return nil, false, nil
}

func (s *localDAGRunStore) FindAttempt(context.Context, exec.DAGRunRef) (exec.DAGRunAttempt, error) {
	return nil, exec.ErrDAGRunIDNotFound
}

func (s *localDAGRunStore) FindSubAttempt(_ context.Context, root exec.DAGRunRef, childRunID string) (exec.DAGRunAttempt, error) {
	s.findRoot = root
	s.findRunID = childRunID
	if s.findErr != nil {
		return nil, s.findErr
	}
	if s.subAttempt == nil {
		return nil, exec.ErrDAGRunIDNotFound
	}
	return s.subAttempt, nil
}

func (s *localDAGRunStore) CreateSubAttempt(context.Context, exec.DAGRunRef, string) (exec.DAGRunAttempt, error) {
	return nil, nil
}

func (s *localDAGRunStore) RemoveOldDAGRuns(context.Context, string, int, ...exec.RemoveOldDAGRunsOption) ([]string, error) {
	return nil, nil
}

func (s *localDAGRunStore) RemoveDAGRun(context.Context, exec.DAGRunRef, ...exec.RemoveDAGRunOption) error {
	return nil
}
