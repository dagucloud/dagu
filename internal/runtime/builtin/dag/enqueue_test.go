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

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/dag"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnqueueExecutorPersistsInheritedProfile(t *testing.T) {
	t.Parallel()

	th := test.Setup(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Queues.Enabled = true
		cfg.Queues.Config = []config.QueueConfig{{Name: "default", MaxActiveRuns: 1}}
	}))

	parent := &core.DAG{
		Name: "parent",
		LocalDAGs: map[string]*core.DAG{
			"child": {
				Name:     "child",
				YamlData: []byte("name: child\nsteps:\n  - name: step\n    run: echo child\n"),
				Steps: []core.Step{
					{Name: "step", ExecutorConfig: core.ExecutorConfig{Type: "noop"}},
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

	step := core.Step{
		Name:           "enqueue-child",
		ExecutorConfig: core.ExecutorConfig{Type: core.ExecutorTypeDAGEnqueue},
		SubDAG:         &core.SubDAG{Name: "child"},
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

	assert.Equal(t, core.Queued, status.Status)
	assert.Equal(t, core.TriggerTypeSubDAG, status.TriggerType)
	assert.Equal(t, "prod", status.ProfileName)
	assert.Equal(t, exec.NewDAGRunRef("child", "child-run"), status.Root)
	assert.True(t, status.Parent.Zero())
}

func TestEnqueueExecutorParallelHonorsMaxConcurrent(t *testing.T) {
	t.Parallel()

	th := test.Setup(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Queues.Enabled = true
		cfg.Queues.Config = []config.QueueConfig{{Name: "default", MaxActiveRuns: 1}}
	}))

	parent := &core.DAG{
		Name: "parent",
		LocalDAGs: map[string]*core.DAG{
			"child": {
				Name:     "child",
				YamlData: []byte("name: child\nsteps:\n  - name: step\n    run: echo child\n"),
				Steps: []core.Step{
					{Name: "step", ExecutorConfig: core.ExecutorConfig{Type: "noop"}},
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

	step := core.Step{
		Name:           "enqueue-child",
		ExecutorConfig: core.ExecutorConfig{Type: core.ExecutorTypeDAGEnqueue},
		SubDAG:         &core.SubDAG{Name: "child"},
		Parallel:       &core.ParallelConfig{MaxConcurrent: 2},
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

	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
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
