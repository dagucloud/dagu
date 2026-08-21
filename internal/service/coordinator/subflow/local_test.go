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
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	filematerialization "github.com/dagucloud/dagu/v2/internal/persis/file/materialization"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator/subflow"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	dagutools "github.com/dagucloud/dagu/v2/internal/tools"
)

func TestLocalCancelRequestsStoredChildAttemptWhenInactive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := ir.NewDAGRunRef("root", "root-run")
	attempt := new(testutil.MockAttempt)
	attempt.On("Abort", ctx).Return(nil).Once()
	store := &localDAGRunStore{subAttempt: attempt}
	runner := subflow.NewLocal(runtime.Manager{}, nil, subflow.WithLocalDAGRunRepository(persis.NewDAGRunRepository(store, nil, persis.DAGRunRepositoryOptions{})))

	err := runner.Cancel(ctx, executor.SubWorkflowCancelRequest{
		DAG:        &ir.DAG{Name: "child"},
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
	root := ir.NewDAGRunRef("root", "root-run")
	store := &localDAGRunStore{}
	runner := subflow.NewLocal(runtime.Manager{}, nil, subflow.WithLocalDAGRunRepository(persis.NewDAGRunRepository(store, nil, persis.DAGRunRepositoryOptions{})))

	err := runner.Cancel(ctx, executor.SubWorkflowCancelRequest{
		DAG:        &ir.DAG{Name: "child"},
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
	root := ir.NewDAGRunRef("root", "root-run")
	findErr := errors.New("store unavailable")
	store := &localDAGRunStore{findErr: findErr}
	runner := subflow.NewLocal(runtime.Manager{}, nil, subflow.WithLocalDAGRunRepository(persis.NewDAGRunRepository(store, nil, persis.DAGRunRepositoryOptions{})))

	err := runner.Cancel(ctx, executor.SubWorkflowCancelRequest{
		DAG:        &ir.DAG{Name: "child"},
		RootDAGRun: root,
		RunID:      "child-run",
	})

	require.ErrorIs(t, err, findErr)
	require.ErrorContains(t, err, "failed to find child workflow attempt")
	require.Equal(t, root, store.findRoot)
	require.Equal(t, "child-run", store.findRunID)
}

func TestLocalRetryRejectsMissingRunStateStore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := ir.NewDAGRunRef("root", "root-run")
	runner := subflow.NewLocal(runtime.Manager{}, nil)

	result, err := runner.Retry(ctx, executor.SubWorkflowRetryRequest{
		SubWorkflowRequest: executor.SubWorkflowRequest{
			DAG:        &ir.DAG{Name: "child"},
			RootDAGRun: root,
			RunID:      "child-run",
		},
		StepName: "step-1",
	})

	require.Nil(t, result)
	require.ErrorContains(t, err, "child workflow run-state store is not configured")
}

func TestLocalEnqueueRecognizesExistingRunWithoutStatus(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	child := th.DAG(t, "name: child\nsteps:\n  - name: ok\n    run: echo ok\n")
	runID := uuid.Must(uuid.NewV7()).String()
	_, err := th.DAGRunRepository.CreateAttempt(
		th.Context,
		child.DAG,
		time.Now(),
		runID,
		persis.DAGRunCreateAttemptOptions{},
	)
	require.NoError(t, err)

	runner := subflow.NewLocal(
		th.DAGRunMgr,
		th.DAGRepository,
		subflow.WithLocalDAGRunRepository(th.DAGRunRepository),
		subflow.WithLocalQueueStore(th.QueueStore),
	)
	result, err := runner.Enqueue(th.Context, executor.EnqueueRequest{
		DAG:   child.DAG,
		RunID: runID,
	})

	require.NoError(t, err)
	require.True(t, result.AlreadyExists)
	require.Equal(t, ir.Queued, result.Status)
}

func TestLocalRunRejectsBuildWorkflowOnRemoteWorker(t *testing.T) {
	t.Parallel()

	runner := subflow.NewLocal(
		runtime.Manager{},
		nil,
		subflow.WithLocalWorkerID("worker-1"),
	)
	result, err := runner.Run(context.Background(), executor.SubWorkflowRequest{
		DAG:        &ir.DAG{Name: "child", Type: ir.TypeBuild},
		RootDAGRun: ir.NewDAGRunRef("parent", "root-1"),
		RunID:      "child-run",
	})

	require.Nil(t, result)
	require.ErrorIs(t, err, dispatch.ErrBuildRequiresLocal)
}

func TestLocalRetryReadsStoredChildAttemptStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := ir.NewDAGRunRef("root", "root-run")
	readErr := errors.New("read status failed")
	attempt := new(testutil.MockAttempt)
	attempt.On("ReadStatus", ctx).Return(nil, readErr).Once()
	store := &localDAGRunStore{subAttempt: attempt}
	runner := subflow.NewLocal(runtime.Manager{}, nil, subflow.WithLocalDAGRunRepository(persis.NewDAGRunRepository(store, nil, persis.DAGRunRepositoryOptions{})))

	result, err := runner.Retry(ctx, executor.SubWorkflowRetryRequest{
		SubWorkflowRequest: executor.SubWorkflowRequest{
			DAG:        &ir.DAG{Name: "child"},
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
	root := ir.NewDAGRunRef("root", uuid.Must(uuid.NewV7()).String())
	runner := subflow.NewLocal(th.DAGRunMgr, th.DAGRepository)

	result, err := runner.Run(th.Context, executor.SubWorkflowRequest{
		DAG:          child.DAG,
		RootDAGRun:   root,
		ParentDAGRun: root,
		RunID:        uuid.Must(uuid.NewV7()).String(),
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, ir.Succeeded, result.Status)
}

func TestLocalRunPreservesDAGBaseConfigWithWorkspace(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	rootDir := t.TempDir()
	outputFile := filepath.Join(t.TempDir(), "base-value.txt")
	definition := []byte(`name: workspace-base-config
steps:
  - id: write
    action: file.write
    with:
      path: "` + filepath.ToSlash(outputFile) + `"
      content: ${env.BASE_VALUE}
`)
	baseConfig := []byte("env:\n  BASE_VALUE: base\n")
	dag, err := spec.LoadYAMLAt(
		th.Context,
		definition,
		filepath.Join(rootDir, "dag.yaml"),
		spec.WithBaseConfigContent(baseConfig),
	)
	require.NoError(t, err)
	desc, archive, err := workspacebundle.PackDirectory(rootDir, workspacebundle.PackOptions{
		DAGPath: "dag.yaml",
		DAGData: definition,
	})
	require.NoError(t, err)

	root := ir.NewDAGRunRef("parent", uuid.Must(uuid.NewV7()).String())
	runner := subflow.NewLocal(th.DAGRunMgr, th.DAGRepository)
	result, err := runner.Run(th.Context, executor.SubWorkflowRequest{
		DAG:          dag,
		RootDAGRun:   root,
		ParentDAGRun: root,
		RunID:        uuid.Must(uuid.NewV7()).String(),
		Workspace: &executor.SubWorkflowWorkspace{
			Descriptor: *desc,
			Archive:    archive,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, ir.Succeeded, result.Status)
	content, err := os.ReadFile(outputFile)
	require.NoError(t, err)
	require.Equal(t, "base", string(content))
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

	root := ir.NewDAGRunRef("parent", uuid.Must(uuid.NewV7()).String())
	ctx := runctx.NewContext(
		th.Context,
		&ir.DAG{Name: root.Name},
		root.ID,
		filepath.Join(t.TempDir(), "parent.log"),
		runctx.WithMaterializationStore(filematerialization.New(filepath.Join(t.TempDir(), "materializations"))),
	)
	runner := subflow.NewLocal(th.DAGRunMgr, th.DAGRepository)
	result, err := runner.Run(ctx, executor.SubWorkflowRequest{
		DAG:          child,
		RootDAGRun:   root,
		ParentDAGRun: root,
		RunID:        uuid.Must(uuid.NewV7()).String(),
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, ir.Succeeded, result.Status)
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
	root := ir.NewDAGRunRef("root", uuid.Must(uuid.NewV7()).String())
	runner := subflow.NewLocal(
		th.DAGRunMgr,
		th.DAGRepository,
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
	require.Equal(t, ir.Succeeded, result.Status)
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
	childStatus := localRunStatus(childDAG.DAG, childRunID, ir.Succeeded, ir.NodeSucceeded)
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

	rootRef := ir.NewDAGRunRef(rootDAG.Name, rootRunID)
	runner := subflow.NewLocal(
		th.DAGRunMgr,
		th.DAGRepository,
		subflow.WithLocalDAGRunRepository(th.DAGRunRepository),
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
	require.Equal(t, ir.Succeeded, result.Status)
	require.Equal(t, "ok", result.Outputs["RESULT"])

	latestAttempt, err := th.DAGRunRepository.FindSubAttempt(th.Context, rootRef, childRunID)
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
	childStatus := localRunStatus(childDAG.DAG, childRunID, ir.Running, ir.NodeRunning)
	childStatus.WorkerID = "local"
	childStatus.StartedAt = stringutil.FormatTime(staleAt)
	childStatus.CreatedAt = staleAt.UnixMilli()
	createStoredRunningChildAttempt(t, th, rootDAG.DAG, childDAG.DAG, rootRunID, childRunID, childStatus)

	rootRef := ir.NewDAGRunRef(rootDAG.Name, rootRunID)
	runner := subflow.NewLocal(th.DAGRunMgr, th.DAGRepository, subflow.WithLocalDAGRunRepository(th.DAGRunRepository))

	result, err := runner.Run(th.Context, executor.SubWorkflowRequest{
		DAG:          childDAG.DAG,
		RootDAGRun:   rootRef,
		ParentDAGRun: rootRef,
		RunID:        childRunID,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, ir.Succeeded, result.Status)

	persisted, err := th.DAGRunMgr.FindSubDAGRunStatus(th.Context, rootRef, childRunID)
	require.NoError(t, err)
	require.Equal(t, ir.Succeeded, persisted.Status)
}

func localRunStatus(dag *ir.DAG, dagRunID string, dagStatus ir.Status, nodeStatus ir.NodeStatus) ir.DAGRunStatus {
	status := ir.InitialStatus(dag)
	status.DAGRunID = dagRunID
	status.Status = dagStatus
	status.StartedAt = stringutil.FormatTime(time.Now())
	status.CreatedAt = time.Now().UnixMilli()
	for _, node := range status.Nodes {
		node.Status = nodeStatus
	}
	return status
}

func createStoredRunningChildAttempt(
	t *testing.T,
	th test.Helper,
	rootDAG *ir.DAG,
	childDAG *ir.DAG,
	rootRunID string,
	childRunID string,
	status ir.DAGRunStatus,
) dagrun.Attempt {
	t.Helper()

	ctx := th.Context
	rootRef := ir.NewDAGRunRef(rootDAG.Name, rootRunID)

	rootAttempt, err := th.DAGRunRepository.CreateAttempt(ctx, rootDAG, time.Now(), rootRunID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, rootAttempt.Open(ctx))
	rootStatus := localRunStatus(rootDAG, rootRunID, ir.Running, ir.NodeRunning)
	rootStatus.AttemptID = rootAttempt.ID()
	rootStatus.AttemptKey = ir.GenerateAttemptKey(rootDAG.Name, rootRunID, rootDAG.Name, rootRunID, rootStatus.AttemptID)
	require.NoError(t, rootAttempt.Write(ctx, rootStatus))
	require.NoError(t, rootAttempt.Close(ctx))

	childAttempt, err := th.DAGRunRepository.CreateAttempt(ctx, childDAG, time.Now(), childRunID, persis.DAGRunCreateAttemptOptions{
		RootDAGRun: rootRef,
	})
	require.NoError(t, err)
	require.NoError(t, childAttempt.Open(ctx))
	status.AttemptID = childAttempt.ID()
	status.AttemptKey = ir.GenerateAttemptKey(rootRef.Name, rootRef.ID, childDAG.Name, childRunID, status.AttemptID)
	status.Root = rootRef
	status.Parent = rootRef
	status.DAGRunID = childRunID
	require.NoError(t, childAttempt.Write(ctx, status))
	require.NoError(t, childAttempt.Close(ctx))
	return childAttempt
}

type localDAGRunStore struct {
	testutil.DAGRunStoreStub
	subAttempt dagrun.Attempt
	findErr    error
	findRoot   ir.DAGRunRef
	findRunID  string
}

type staticInstaller struct {
	manifest *dagutools.Manifest
}

func (i *staticInstaller) Install(
	_ context.Context,
	_ *ir.ToolConfig,
	_ dagutools.InstallOptions,
) (*dagutools.Manifest, error) {
	return i.manifest, nil
}

func (s *localDAGRunStore) FindSubAttempt(_ context.Context, root ir.DAGRunRef, childRunID string) (dagrun.Attempt, error) {
	s.findRoot = root
	s.findRunID = childRunID
	if s.findErr != nil {
		return nil, s.findErr
	}
	if s.subAttempt == nil {
		return nil, dagrun.ErrDAGRunIDNotFound
	}
	return s.subAttempt, nil
}
