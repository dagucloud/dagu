// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag_test

import (
	"bytes"
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	_ "github.com/dagucloud/dagu/v2/internal/runtime/builtin/dag"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnqueueExecutorPersistsInheritedProfile(t *testing.T) {
	t.Parallel()

	th := test.Setup(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Queues.Enabled = true
		cfg.Queues.Config = []config.QueueConfig{{Name: "default", MaxActiveRuns: 1}}
	}))

	parent := &ir.DAG{
		Name: "parent",
		LocalDAGs: map[string]*ir.DAG{
			"child": {
				Name:     "child",
				YamlData: []byte("name: child\nsteps:\n  - name: step\n    run: echo child\n"),
				Steps: []ir.Step{
					{Name: "step", ExecutorConfig: ir.ExecutorConfig{Type: "noop"}},
				},
			},
		},
	}
	parentRun := exec.NewDAGRunRef(parent.Name, "parent-run")
	ctx := runtime.NewContext(
		th.Context,
		parent,
		parentRun.ID,
		filepath.Join(th.Config.Paths.LogDir, "parent.log"),
		runtime.WithRootDAGRun(parentRun),
		runtime.WithDAGRunStore(th.DAGRunStore),
		runtime.WithQueueStore(th.QueueStore),
		runtime.WithDAGRunLogDir(th.Config.Paths.LogDir),
		runtime.WithDAGRunArtifactDir(th.Config.Paths.ArtifactDir),
		runtime.WithRuntimeProfile("prod", "", nil),
	)

	step := ir.Step{
		Name:           "enqueue-child",
		ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeDAGEnqueue},
		SubDAG:         &ir.SubDAG{Name: "child"},
	}
	execImpl, err := executor.NewExecutor(ctx, step)
	require.NoError(t, err)

	dagExec, ok := execImpl.(executor.DAGExecutor)
	require.True(t, ok)
	dagExec.SetParams(executor.RunParams{RunID: "child-run", Params: "FOO=bar"})

	var stdout bytes.Buffer
	execImpl.SetStdout(&stdout)
	require.NoError(t, execImpl.Run(ctx))

	attempt, err := th.DAGRunStore.FindAttempt(ctx, exec.NewDAGRunRef("child", "child-run"))
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)

	assert.Equal(t, ir.Queued, status.Status)
	assert.Equal(t, ir.TriggerTypeSubDAG, status.TriggerType)
	assert.Equal(t, "prod", status.ProfileName)
	assert.Equal(t, exec.NewDAGRunRef("child", "child-run"), status.Root)
	assert.True(t, status.Parent.Zero())
}

func TestEnqueueWorkerSelector(t *testing.T) {
	t.Parallel()

	th := test.Setup(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Queues.Enabled = true
		cfg.Queues.Config = []config.QueueConfig{{Name: "default", MaxActiveRuns: 1}}
	}))

	parent := &ir.DAG{
		Name: "parent",
		LocalDAGs: map[string]*ir.DAG{
			"child": {
				Name:     "child",
				YamlData: []byte("name: child\nsteps:\n  - name: step\n    run: echo child\n"),
				Steps: []ir.Step{
					{Name: "step", ExecutorConfig: ir.ExecutorConfig{Type: "noop"}},
				},
			},
		},
	}
	parentRun := exec.NewDAGRunRef(parent.Name, "parent-run")
	ctx := runtime.NewContext(
		th.Context,
		parent,
		parentRun.ID,
		filepath.Join(th.Config.Paths.LogDir, "parent.log"),
		runtime.WithRootDAGRun(parentRun),
		runtime.WithDAGRunStore(th.DAGRunStore),
		runtime.WithQueueStore(th.QueueStore),
		runtime.WithDAGRunLogDir(th.Config.Paths.LogDir),
		runtime.WithDAGRunArtifactDir(th.Config.Paths.ArtifactDir),
	)

	step := ir.Step{
		Name:           "enqueue-child",
		ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeDAGEnqueue},
		SubDAG:         &ir.SubDAG{Name: "child"},
		Parallel:       &ir.ParallelConfig{},
		WorkerSelector: map[string]string{"host": "${ITEM}"},
	}
	execImpl, err := executor.NewExecutor(ctx, step)
	require.NoError(t, err)

	parallelExec, ok := execImpl.(executor.ParallelExecutor)
	require.True(t, ok)
	parallelExec.SetParamsList([]executor.RunParams{{
		RunID:          "child-run",
		DAGName:        "child",
		Params:         "FACILITY=serverA",
		WorkerSelector: map[string]string{"host": "serverA"},
	}})

	require.NoError(t, execImpl.Run(ctx))

	attempt, err := th.DAGRunStore.FindAttempt(ctx, exec.NewDAGRunRef("child", "child-run"))
	require.NoError(t, err)
	child, err := attempt.ReadDAG(ctx)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"host": "serverA"}, child.WorkerSelector)
}

func TestSubDAGExecutorsRejectHumanTasks(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	parent := &ir.DAG{
		Name: "parent",
		LocalDAGs: map[string]*ir.DAG{
			"child": {
				Name: "child",
				Steps: []ir.Step{{
					ID:        "review",
					Name:      "review",
					HumanTask: &ir.HumanTaskConfig{Prompt: "Review"},
				}},
			},
		},
	}
	root := exec.NewDAGRunRef(parent.Name, "parent-run")
	ctx := runtime.NewContext(
		th.Context,
		parent,
		root.ID,
		"",
		runtime.WithRootDAGRun(root),
		runtime.WithDAGRunStore(th.DAGRunStore),
		runtime.WithQueueStore(th.QueueStore),
	)

	for _, step := range []ir.Step{
		{
			Name:           "run-child",
			ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeDAG},
			SubDAG:         &ir.SubDAG{Name: "child"},
		},
		{
			Name:           "run-child-in-parallel",
			ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeDAG},
			SubDAG:         &ir.SubDAG{Name: "child"},
			Parallel:       &ir.ParallelConfig{Items: []ir.ParallelItem{{Value: "one"}}},
		},
	} {
		_, err := executor.NewExecutor(ctx, step)
		require.ErrorContains(t, err, "human task steps are not allowed in sub-DAGs")
	}

	enqueueStep := ir.Step{
		Name:           "enqueue-child",
		ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeDAGEnqueue},
		SubDAG:         &ir.SubDAG{Name: "child"},
	}
	enqueueExecutor, err := executor.NewExecutor(ctx, enqueueStep)
	require.NoError(t, err)
	dagExecutor := enqueueExecutor.(executor.DAGExecutor)
	dagExecutor.SetParams(executor.RunParams{RunID: "child-run"})
	err = enqueueExecutor.Run(ctx)
	require.ErrorContains(t, err, "human task steps are not allowed in sub-DAGs")
}

func TestEnqueueExecutorParallelHonorsMaxConcurrent(t *testing.T) {
	t.Parallel()

	th := test.Setup(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Queues.Enabled = true
		cfg.Queues.Config = []config.QueueConfig{{Name: "default", MaxActiveRuns: 1}}
	}))

	parent := &ir.DAG{
		Name: "parent",
		LocalDAGs: map[string]*ir.DAG{
			"child": {
				Name:     "child",
				YamlData: []byte("name: child\nsteps:\n  - name: step\n    run: echo child\n"),
				Steps: []ir.Step{
					{Name: "step", ExecutorConfig: ir.ExecutorConfig{Type: "noop"}},
				},
			},
		},
	}
	parentRun := exec.NewDAGRunRef(parent.Name, "parent-run")
	queueStore := newRecordingQueueStore(th.QueueStore, 2)
	ctx := runtime.NewContext(
		th.Context,
		parent,
		parentRun.ID,
		filepath.Join(th.Config.Paths.LogDir, "parent.log"),
		runtime.WithRootDAGRun(parentRun),
		runtime.WithDAGRunStore(th.DAGRunStore),
		runtime.WithQueueStore(queueStore),
		runtime.WithDAGRunLogDir(th.Config.Paths.LogDir),
		runtime.WithDAGRunArtifactDir(th.Config.Paths.ArtifactDir),
	)

	step := ir.Step{
		Name:           "enqueue-child",
		ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeDAGEnqueue},
		SubDAG:         &ir.SubDAG{Name: "child"},
		Parallel:       &ir.ParallelConfig{MaxConcurrent: 2},
	}
	execImpl, err := executor.NewExecutor(ctx, step)
	require.NoError(t, err)

	parallelExec, ok := execImpl.(executor.ParallelExecutor)
	require.True(t, ok)
	parallelExec.SetParamsList([]executor.RunParams{
		{RunID: "child-run-1", DAGName: "child", Params: "VALUE=one"},
		{RunID: "child-run-2", DAGName: "child", Params: "VALUE=two"},
		{RunID: "child-run-3", DAGName: "child", Params: "VALUE=three"},
	})

	var stdout bytes.Buffer
	execImpl.SetStdout(&stdout)

	runCtx, cancel := context.WithTimeout(ctx, enqueueConcurrencyWaitTimeout(t))
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- execImpl.Run(runCtx)
	}()

	select {
	case <-queueStore.TargetActiveReached():
	case err := <-done:
		require.NoError(t, err)
		t.Fatal("enqueue completed before reaching target concurrency")
	case <-runCtx.Done():
		t.Fatalf("enqueue did not reach target concurrency: %v", runCtx.Err())
	}

	queueStore.Release()
	require.NoError(t, <-done)

	assert.Equal(t, 2, queueStore.MaxActive())
}

// enqueueConcurrencyWaitTimeout bounds the wait for the executor to reach the
// queue store. Each child run performs filesystem setup before it gets there,
// and that cost varies widely with host load and platform, so the budget is
// generous and shrinks only to stay inside the test deadline.
func enqueueConcurrencyWaitTimeout(t *testing.T) time.Duration {
	t.Helper()

	timeout := time.Minute
	if deadline, ok := t.Deadline(); ok {
		remaining := time.Until(deadline) - 15*time.Second
		if remaining > 0 && remaining < timeout {
			return remaining
		}
	}
	return timeout
}

type recordingQueueStore struct {
	exec.QueueStore

	mu        sync.Mutex
	active    int
	maxActive int

	targetActive int
	reached      chan struct{}
	reachedOnce  sync.Once
	release      chan struct{}
	releaseOnce  sync.Once
}

func newRecordingQueueStore(store exec.QueueStore, targetActive int) *recordingQueueStore {
	return &recordingQueueStore{
		QueueStore:   store,
		targetActive: targetActive,
		reached:      make(chan struct{}),
		release:      make(chan struct{}),
	}
}

func (s *recordingQueueStore) Enqueue(ctx context.Context, name string, priority exec.QueuePriority, dagRun exec.DAGRunRef) error {
	s.mu.Lock()
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	if s.active >= s.targetActive {
		s.reachedOnce.Do(func() { close(s.reached) })
	}
	s.mu.Unlock()

	select {
	case <-s.release:
	case <-ctx.Done():
		s.mu.Lock()
		s.active--
		s.mu.Unlock()
		return ctx.Err()
	}

	err := s.QueueStore.Enqueue(ctx, name, priority, dagRun)

	s.mu.Lock()
	s.active--
	s.mu.Unlock()

	return err
}

func (s *recordingQueueStore) TargetActiveReached() <-chan struct{} {
	return s.reached
}

func (s *recordingQueueStore) Release() {
	s.releaseOnce.Do(func() { close(s.release) })
}

func (s *recordingQueueStore) MaxActive() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxActive
}
