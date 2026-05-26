// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/filedagrun"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
)

func newDAGRunStore(t *testing.T, opts ...store.DAGRunStoreOption) exec.DAGRunStore {
	t.Helper()
	allOpts := append([]store.DAGRunStoreOption{
		store.WithDAGRunLatestStatusToday(false),
		store.WithDAGRunLocation(time.UTC),
	}, opts...)
	return store.NewDAGRunStore(testutil.NewMemoryBackend().Collection("dag_runs"), allOpts...)
}

func testDAG(name string, labels ...string) *core.DAG {
	return &core.DAG{
		Name:     name,
		Location: "/tmp/" + name + ".yaml",
		Labels:   core.NewLabels(labels),
	}
}

type recordingLockCollection struct {
	persis.Collection
	mu   sync.Mutex
	keys []string
}

func (c *recordingLockCollection) WithLock(ctx context.Context, key string, fn func() error) error {
	c.mu.Lock()
	c.keys = append(c.keys, key)
	c.mu.Unlock()

	if lockable, ok := c.Collection.(interface {
		WithLock(context.Context, string, func() error) error
	}); ok {
		return lockable.WithLock(ctx, key, fn)
	}
	return fn()
}

func (c *recordingLockCollection) lockedKeys() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.keys...)
}

func (c *recordingLockCollection) resetKeys() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keys = nil
}

func writeDAGRunStatus(t *testing.T, ctx context.Context, att exec.DAGRunAttempt, dag *core.DAG, dagRunID string, status core.Status) exec.DAGRunStatus {
	t.Helper()

	st := exec.InitialStatus(dag)
	st.DAGRunID = dagRunID
	st.AttemptID = att.ID()
	st.AttemptKey = exec.GenerateAttemptKey(dag.Name, dagRunID, dag.Name, dagRunID, att.ID())
	st.Status = status

	require.NoError(t, att.Open(ctx))
	require.NoError(t, att.Write(ctx, st))
	require.NoError(t, att.Close(ctx))
	return st
}

func writeSubDAGRunStatus(t *testing.T, ctx context.Context, att exec.DAGRunAttempt, root exec.DAGRunRef, dag *core.DAG, dagRunID string, status core.Status) exec.DAGRunStatus {
	t.Helper()

	st := exec.InitialStatus(dag)
	st.Root = root
	st.Parent = root
	st.DAGRunID = dagRunID
	st.AttemptID = att.ID()
	st.AttemptKey = exec.GenerateAttemptKey(root.Name, root.ID, dag.Name, dagRunID, att.ID())
	st.Status = status

	require.NoError(t, att.Open(ctx))
	require.NoError(t, att.Write(ctx, st))
	require.NoError(t, att.Close(ctx))
	return st
}

func requireAttemptStatus(t *testing.T, ctx context.Context, att exec.DAGRunAttempt) *exec.DAGRunStatus {
	t.Helper()
	st, err := att.ReadStatus(ctx)
	require.NoError(t, err)
	require.NotNil(t, st)
	return st
}

func TestDAGRunStore_FileCollectionReadsExistingFileDAGRunLayout(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagRunsDir := t.TempDir()
	dag := testDAG("file-compat-dag", "env=prod")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	existing := filedagrun.New(
		dagRunsDir,
		filedagrun.WithLatestStatusToday(false),
		filedagrun.WithLocation(time.UTC),
	)
	attempt, err := existing.CreateAttempt(ctx, dag, base, "run-file", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-file"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, attempt, dag, "run-file", core.Succeeded)

	compat := store.NewDAGRunStore(
		file.NewCollection(dagRunsDir),
		store.WithDAGRunLatestStatusToday(false),
		store.WithDAGRunLocation(time.UTC),
	)

	found, err := compat.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, "run-file"))
	require.NoError(t, err)
	assert.Equal(t, "attempt-file", found.ID())
	assert.Equal(t, core.Succeeded, requireAttemptStatus(t, ctx, found).Status)

	statuses, err := compat.ListStatuses(ctx, exec.WithAllHistory(), exec.WithExactName(dag.Name))
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.Equal(t, "run-file", statuses[0].DAGRunID)
	assert.Equal(t, "attempt-file", statuses[0].AttemptID)
}

func TestDAGRunStore_FileCollectionReadsExistingFileSubDAGRunLayout(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagRunsDir := t.TempDir()
	parent := testDAG("file-compat-parent")
	child := testDAG("file-compat-child")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	existing := filedagrun.New(
		dagRunsDir,
		filedagrun.WithLatestStatusToday(false),
		filedagrun.WithLocation(time.UTC),
	)
	parentAttempt, err := existing.CreateAttempt(ctx, parent, base, "parent-run", exec.NewDAGRunAttemptOptions{AttemptID: "parent-attempt"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, parentAttempt, parent, "parent-run", core.Running)

	rootRef := exec.NewDAGRunRef(parent.Name, "parent-run")
	childAttempt, err := existing.CreateAttempt(ctx, child, base.Add(time.Second), "child-run", exec.NewDAGRunAttemptOptions{
		AttemptID:  "child-attempt",
		RootDAGRun: &rootRef,
	})
	require.NoError(t, err)
	writeSubDAGRunStatus(t, ctx, childAttempt, rootRef, child, "child-run", core.Succeeded)

	compat := store.NewDAGRunStore(
		file.NewCollection(dagRunsDir),
		store.WithDAGRunLatestStatusToday(false),
		store.WithDAGRunLocation(time.UTC),
	)

	found, err := compat.FindSubAttempt(ctx, rootRef, "child-run")
	require.NoError(t, err)
	assert.Equal(t, "child-attempt", found.ID())
	status := requireAttemptStatus(t, ctx, found)
	assert.Equal(t, core.Succeeded, status.Status)
	assert.Equal(t, rootRef, status.Root)
	assert.Equal(t, rootRef, status.Parent)
}

func TestDAGRunStore_FileCollectionWritesExistingFileDAGRunLayout(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagRunsDir := t.TempDir()
	dag := testDAG("file-layout-dag")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	compat := store.NewDAGRunStore(
		file.NewCollection(dagRunsDir),
		store.WithDAGRunLatestStatusToday(false),
		store.WithDAGRunLocation(time.UTC),
	)
	attempt, err := compat.CreateAttempt(ctx, dag, base, "run-file", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-file"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, attempt, dag, "run-file", core.Succeeded)

	matches, err := filepath.Glob(filepath.Join(
		dagRunsDir,
		"*",
		"dag-runs",
		"2026",
		"01",
		"02",
		"dag-run_*_run-file",
		"attempt_*_attempt-file",
		"status.jsonl",
	))
	require.NoError(t, err)
	require.Len(t, matches, 1)

	existing := filedagrun.New(
		dagRunsDir,
		filedagrun.WithLatestStatusToday(false),
		filedagrun.WithLocation(time.UTC),
	)
	found, err := existing.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, "run-file"))
	require.NoError(t, err)
	assert.Equal(t, "attempt-file", found.ID())
	assert.Equal(t, core.Succeeded, requireAttemptStatus(t, ctx, found).Status)
}

func TestDAGRunStore_FileCollectionWritesExistingFileSubDAGRunLayout(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagRunsDir := t.TempDir()
	parent := testDAG("file-layout-parent")
	child := testDAG("file-layout-child")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	compat := store.NewDAGRunStore(
		file.NewCollection(dagRunsDir),
		store.WithDAGRunLatestStatusToday(false),
		store.WithDAGRunLocation(time.UTC),
	)
	parentAttempt, err := compat.CreateAttempt(ctx, parent, base, "parent-run", exec.NewDAGRunAttemptOptions{AttemptID: "parent-attempt"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, parentAttempt, parent, "parent-run", core.Running)

	rootRef := exec.NewDAGRunRef(parent.Name, "parent-run")
	childAttempt, err := compat.CreateAttempt(ctx, child, base.Add(time.Second), "child-run", exec.NewDAGRunAttemptOptions{
		AttemptID:  "child-attempt",
		RootDAGRun: &rootRef,
	})
	require.NoError(t, err)
	writeSubDAGRunStatus(t, ctx, childAttempt, rootRef, child, "child-run", core.Succeeded)

	matches, err := filepath.Glob(filepath.Join(
		dagRunsDir,
		"*",
		"dag-runs",
		"2026",
		"01",
		"02",
		"dag-run_*_parent-run",
		"children",
		"child_child-run",
		"attempt_*_child-attempt",
		"status.jsonl",
	))
	require.NoError(t, err)
	require.Len(t, matches, 1)

	existing := filedagrun.New(
		dagRunsDir,
		filedagrun.WithLatestStatusToday(false),
		filedagrun.WithLocation(time.UTC),
	)
	found, err := existing.FindSubAttempt(ctx, rootRef, "child-run")
	require.NoError(t, err)
	assert.Equal(t, "child-attempt", found.ID())
	status := requireAttemptStatus(t, ctx, found)
	assert.Equal(t, core.Succeeded, status.Status)
	assert.Equal(t, rootRef, status.Root)
	assert.Equal(t, rootRef, status.Parent)
}

func TestDAGRunStore_FileCollectionPreservesAttemptSidecarLayout(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagRunsDir := t.TempDir()
	dag := testDAG("file-sidecar-dag")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	compat := store.NewDAGRunStore(
		file.NewCollection(dagRunsDir),
		store.WithDAGRunLatestStatusToday(false),
		store.WithDAGRunLocation(time.UTC),
	)
	attempt, err := compat.CreateAttempt(ctx, dag, base, "run-file", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-file"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, attempt, dag, "run-file", core.Succeeded)

	outputs := &exec.DAGRunOutputs{
		Metadata: exec.OutputsMetadata{
			DAGName:   dag.Name,
			DAGRunID:  "run-file",
			AttemptID: attempt.ID(),
			Status:    core.Succeeded.String(),
		},
		Outputs: map[string]string{"result": "ok"},
	}
	require.NoError(t, attempt.WriteOutputs(ctx, outputs))

	messages := []exec.LLMMessage{{Role: "assistant", Content: "ready"}}
	require.NoError(t, attempt.WriteStepMessages(ctx, "step-one", messages))
	require.NoError(t, attempt.Abort(ctx))
	aborting, err := attempt.IsAborting(ctx)
	require.NoError(t, err)
	assert.True(t, aborting)

	dagRunDirs, err := filepath.Glob(filepath.Join(
		dagRunsDir,
		"*",
		"dag-runs",
		"2026",
		"01",
		"02",
		"dag-run_*_run-file",
	))
	require.NoError(t, err)
	require.Len(t, dagRunDirs, 1)
	attemptDirs, err := filepath.Glob(filepath.Join(dagRunDirs[0], "attempt_*_attempt-file"))
	require.NoError(t, err)
	require.Len(t, attemptDirs, 1)
	require.FileExists(t, filepath.Join(attemptDirs[0], "dag.json"))
	require.FileExists(t, filepath.Join(attemptDirs[0], "outputs.json"))
	require.FileExists(t, filepath.Join(attemptDirs[0], "status.jsonl"))
	require.FileExists(t, filepath.Join(dagRunDirs[0], "messages", "step-one.json"))
	require.DirExists(t, filepath.Join(dagRunDirs[0], "work"))
	require.NoDirExists(t, filepath.Join(dagRunsDir, "runs"))

	existing := filedagrun.New(
		dagRunsDir,
		filedagrun.WithLatestStatusToday(false),
		filedagrun.WithLocation(time.UTC),
	)
	found, err := existing.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, "run-file"))
	require.NoError(t, err)

	readDAG, err := found.ReadDAG(ctx)
	require.NoError(t, err)
	assert.Equal(t, dag.Name, readDAG.Name)

	readOutputs, err := found.ReadOutputs(ctx)
	require.NoError(t, err)
	require.NotNil(t, readOutputs)
	assert.Equal(t, outputs.Metadata, readOutputs.Metadata)
	assert.Equal(t, outputs.Outputs, readOutputs.Outputs)

	readMessages, err := found.ReadStepMessages(ctx, "step-one")
	require.NoError(t, err)
	assert.Equal(t, messages, readMessages)

	aborting, err = found.IsAborting(ctx)
	require.NoError(t, err)
	assert.True(t, aborting)
}

func TestDAGRunStore_FileCollectionLatestAttemptUsesDefaultLocation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		opts []store.DAGRunStoreOption
	}{
		{name: "default"},
		{name: "nil-location", opts: []store.DAGRunStoreOption{store.WithDAGRunLocation(nil)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			dagRunsDir := t.TempDir()
			dag := testDAG("file-default-location-" + tc.name)
			base := time.Now().Add(24 * time.Hour)

			s := store.NewDAGRunStore(file.NewCollection(dagRunsDir), tc.opts...)
			attempt, err := s.CreateAttempt(ctx, dag, base, "run-file", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-file"})
			require.NoError(t, err)
			writeDAGRunStatus(t, ctx, attempt, dag, "run-file", core.Succeeded)

			latest, err := s.LatestAttempt(ctx, dag.Name)
			require.NoError(t, err)
			assert.Equal(t, "attempt-file", latest.ID())
		})
	}
}

func TestDAGRunStore_FileCollectionPreservesLockRoot(t *testing.T) {
	ctx := context.Background()
	dagRunsDir := filepath.Join(t.TempDir(), "dag-runs")
	lockRoot := filepath.Join(t.TempDir(), "locks")
	dag := testDAG("file-lock-root")

	lock := dirlock.New(filepath.Join(lockRoot, "file-lock-root", "dag-runs"), nil)
	require.NoError(t, lock.Lock(ctx))
	defer func() { require.NoError(t, lock.Unlock()) }()

	s := store.NewDAGRunStore(
		file.NewCollectionWithLockRoot(dagRunsDir, lockRoot),
		store.WithDAGRunLatestStatusToday(false),
		store.WithDAGRunLocation(time.UTC),
	)

	blockedCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_, err := s.CreateAttempt(blockedCtx, dag, time.Now(), "run-file", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-file"})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestDAGRunStore_CreateWriteFindAndRetry(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newDAGRunStore(t)
	dag := testDAG("collection-dag", "env=prod")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	first, err := s.CreateAttempt(ctx, dag, base, "run-1", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-a"})
	require.NoError(t, err)
	assert.Equal(t, "attempt-a", first.ID())
	expected := writeDAGRunStatus(t, ctx, first, dag, "run-1", core.Running)

	readBack := requireAttemptStatus(t, ctx, first)
	assert.Equal(t, expected.Name, readBack.Name)
	assert.Equal(t, expected.DAGRunID, readBack.DAGRunID)
	assert.Equal(t, expected.AttemptID, readBack.AttemptID)
	assert.Equal(t, expected.Status, readBack.Status)

	persistedDAG, err := first.ReadDAG(ctx)
	require.NoError(t, err)
	assert.Equal(t, dag.Name, persistedDAG.Name)
	assert.Equal(t, dag.Labels.Strings(), persistedDAG.Labels.Strings())

	found, err := s.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, "run-1"))
	require.NoError(t, err)
	assert.Equal(t, "attempt-a", found.ID())

	latest, err := s.LatestAttempt(ctx, dag.Name)
	require.NoError(t, err)
	assert.Equal(t, "attempt-a", latest.ID())

	recent := s.RecentAttempts(ctx, dag.Name, 10)
	require.Len(t, recent, 1)
	assert.Equal(t, "attempt-a", recent[0].ID())

	_, err = s.CreateAttempt(ctx, dag, base, "run-1", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-dup"})
	assert.ErrorIs(t, err, exec.ErrDAGRunAlreadyExists)

	retry, err := s.CreateAttempt(ctx, dag, base.Add(time.Hour), "run-1", exec.NewDAGRunAttemptOptions{
		Retry:     true,
		AttemptID: "attempt-b",
	})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, retry, dag, "run-1", core.Succeeded)

	found, err = s.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, "run-1"))
	require.NoError(t, err)
	assert.Equal(t, "attempt-b", found.ID())
	assert.Equal(t, core.Succeeded, requireAttemptStatus(t, ctx, found).Status)
}

func TestDAGRunStore_RecentAttemptsRejectsEmptyName(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newDAGRunStore(t)
	dag := testDAG("recent-name-dag")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	attempt, err := s.CreateAttempt(ctx, dag, base, "run-1", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-a"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, attempt, dag, "run-1", core.Succeeded)

	require.Empty(t, s.RecentAttempts(ctx, "", 10))
	require.Len(t, s.RecentAttempts(ctx, dag.Name, 10), 1)
}

func TestDAGRunAttempt_UpdateUsesDAGRunLock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	col := &recordingLockCollection{Collection: testutil.NewMemoryBackend().Collection("dag_runs")}
	s := store.NewDAGRunStore(
		col,
		store.WithDAGRunLatestStatusToday(false),
		store.WithDAGRunLocation(time.UTC),
	)
	dag := testDAG("lock-dag")
	attempt, err := s.CreateAttempt(ctx, dag, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), "run-1", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-a"})
	require.NoError(t, err)

	col.resetKeys()
	writeDAGRunStatus(t, ctx, attempt, dag, "run-1", core.Succeeded)

	keys := col.lockedKeys()
	assert.Contains(t, keys, "runs/lock-dag/run-1/lock")
	for _, key := range keys {
		assert.NotContains(t, key, "record/")
	}
}

func TestDAGRunStore_GeneratedAttemptIDUsesEightRandomBytes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newDAGRunStore(t)
	dag := testDAG("generated-attempt-id-dag")

	att, err := s.CreateAttempt(ctx, dag, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), "run-1", exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)
	assert.Len(t, att.ID(), 16)
}

func TestDAGRunStore_CompareAndSwapLatestAttemptStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newDAGRunStore(t)
	dag := testDAG("cas-dag")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	att, err := s.CreateAttempt(ctx, dag, base, "run-1", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-a"})
	require.NoError(t, err)
	initial := writeDAGRunStatus(t, ctx, att, dag, "run-1", core.Running)

	updated, swapped, err := s.CompareAndSwapLatestAttemptStatus(
		ctx,
		exec.NewDAGRunRef(dag.Name, "run-1"),
		initial.AttemptID,
		core.Running,
		func(st *exec.DAGRunStatus) error {
			st.Status = core.Succeeded
			return nil
		},
		exec.WithCompareAndSwapExpectedAttemptKey(initial.AttemptKey),
	)
	require.NoError(t, err)
	assert.True(t, swapped)
	require.NotNil(t, updated)
	assert.Equal(t, core.Succeeded, updated.Status)

	current, swapped, err := s.CompareAndSwapLatestAttemptStatus(
		ctx,
		exec.NewDAGRunRef(dag.Name, "run-1"),
		initial.AttemptID,
		core.Running,
		func(st *exec.DAGRunStatus) error {
			st.Status = core.Failed
			return nil
		},
	)
	require.NoError(t, err)
	assert.False(t, swapped)
	require.NotNil(t, current)
	assert.Equal(t, core.Succeeded, current.Status)
}

func TestDAGRunStore_CompareAndSwapSubAttemptStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newDAGRunStore(t)
	parent := testDAG("parent")
	child := testDAG("child")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	parentAttempt, err := s.CreateAttempt(ctx, parent, base, "parent-run", exec.NewDAGRunAttemptOptions{AttemptID: "parent-attempt"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, parentAttempt, parent, "parent-run", core.Running)

	rootRef := exec.NewDAGRunRef(parent.Name, "parent-run")
	subAttempt, err := s.CreateSubAttempt(ctx, rootRef, "child-run")
	require.NoError(t, err)
	subAttempt.SetDAG(child)

	subStatus := exec.InitialStatus(child)
	subStatus.Root = rootRef
	subStatus.Parent = rootRef
	subStatus.DAGRunID = "child-run"
	subStatus.AttemptID = subAttempt.ID()
	subStatus.AttemptKey = exec.GenerateAttemptKey(parent.Name, rootRef.ID, child.Name, subStatus.DAGRunID, subAttempt.ID())
	subStatus.Status = core.Running
	require.NoError(t, subAttempt.Open(ctx))
	require.NoError(t, subAttempt.Write(ctx, subStatus))
	require.NoError(t, subAttempt.Close(ctx))

	updated, swapped, err := s.CompareAndSwapLatestAttemptStatus(
		ctx,
		exec.NewDAGRunRef("", "child-run"),
		subAttempt.ID(),
		core.Running,
		func(st *exec.DAGRunStatus) error {
			st.Status = core.Succeeded
			return nil
		},
		exec.WithCompareAndSwapRootDAGRun(rootRef),
		exec.WithCompareAndSwapExpectedAttemptKey(subStatus.AttemptKey),
	)
	require.NoError(t, err)
	assert.True(t, swapped)
	require.NotNil(t, updated)
	assert.Equal(t, core.Succeeded, updated.Status)

	found, err := s.FindSubAttempt(ctx, rootRef, "child-run")
	require.NoError(t, err)
	assert.Equal(t, core.Succeeded, requireAttemptStatus(t, ctx, found).Status)
}

func TestDAGRunAttempt_WriteBackfillsSubAttemptHierarchy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newDAGRunStore(t)
	parent := testDAG("parent")
	child := testDAG("child")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	parentAttempt, err := s.CreateAttempt(ctx, parent, base, "parent-run", exec.NewDAGRunAttemptOptions{AttemptID: "parent-attempt"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, parentAttempt, parent, "parent-run", core.Running)

	rootRef := exec.NewDAGRunRef(parent.Name, "parent-run")
	subAttempt, err := s.CreateAttempt(ctx, child, base.Add(time.Second), "child-run", exec.NewDAGRunAttemptOptions{
		RootDAGRun: &rootRef,
		AttemptID:  "child-attempt",
	})
	require.NoError(t, err)

	subStatus := exec.InitialStatus(child)
	subStatus.DAGRunID = "child-run"
	subStatus.AttemptID = subAttempt.ID()
	subStatus.AttemptKey = exec.GenerateAttemptKey(parent.Name, rootRef.ID, child.Name, subStatus.DAGRunID, subAttempt.ID())
	subStatus.Status = core.Running

	require.NoError(t, subAttempt.Open(ctx))
	require.NoError(t, subAttempt.Write(ctx, subStatus))
	require.NoError(t, subAttempt.Close(ctx))

	readBack := requireAttemptStatus(t, ctx, subAttempt)
	assert.Equal(t, rootRef, readBack.Root)
	assert.Equal(t, rootRef, readBack.Parent)
}

func TestDAGRunAttempt_WriteRejectsMismatchedSubAttemptHierarchy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newDAGRunStore(t)
	parent := testDAG("parent")
	child := testDAG("child")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	parentAttempt, err := s.CreateAttempt(ctx, parent, base, "parent-run", exec.NewDAGRunAttemptOptions{AttemptID: "parent-attempt"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, parentAttempt, parent, "parent-run", core.Running)

	rootRef := exec.NewDAGRunRef(parent.Name, "parent-run")
	subAttempt, err := s.CreateAttempt(ctx, child, base.Add(time.Second), "child-run", exec.NewDAGRunAttemptOptions{
		RootDAGRun: &rootRef,
		AttemptID:  "child-attempt",
	})
	require.NoError(t, err)

	subStatus := exec.InitialStatus(child)
	subStatus.Root = exec.NewDAGRunRef("other-parent", "parent-run")
	subStatus.Parent = rootRef
	subStatus.DAGRunID = "child-run"
	subStatus.AttemptID = subAttempt.ID()
	subStatus.AttemptKey = exec.GenerateAttemptKey(parent.Name, rootRef.ID, child.Name, subStatus.DAGRunID, subAttempt.ID())
	subStatus.Status = core.Running

	require.NoError(t, subAttempt.Open(ctx))
	err = subAttempt.Write(ctx, subStatus)
	require.ErrorContains(t, err, "status root")
	require.NoError(t, subAttempt.Close(ctx))

	parentMismatchAttempt, err := s.CreateAttempt(ctx, parent, base.Add(2*time.Second), "parent-run-with-parent", exec.NewDAGRunAttemptOptions{
		AttemptID: "parent-attempt-with-parent",
	})
	require.NoError(t, err)
	parentMismatchStatus := exec.InitialStatus(parent)
	parentMismatchStatus.Parent = exec.NewDAGRunRef("other-parent", "parent-run")
	parentMismatchStatus.DAGRunID = "parent-run-with-parent"
	parentMismatchStatus.AttemptID = parentMismatchAttempt.ID()
	parentMismatchStatus.AttemptKey = exec.GenerateAttemptKey(parent.Name, parentMismatchStatus.DAGRunID, parent.Name, parentMismatchStatus.DAGRunID, parentMismatchAttempt.ID())
	parentMismatchStatus.Status = core.Running

	require.NoError(t, parentMismatchAttempt.Open(ctx))
	err = parentMismatchAttempt.Write(ctx, parentMismatchStatus)
	require.ErrorContains(t, err, "status parent")
	require.NoError(t, parentMismatchAttempt.Close(ctx))
}

func TestDAGRunAttempt_WriteRejectsMismatchedRunIdentity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newDAGRunStore(t)
	dag := testDAG("identity-dag")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	dagRunMismatchAttempt, err := s.CreateAttempt(ctx, dag, base, "run-1", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-1"})
	require.NoError(t, err)
	dagRunMismatchStatus := exec.InitialStatus(dag)
	dagRunMismatchStatus.DAGRunID = "other-run"
	dagRunMismatchStatus.AttemptID = dagRunMismatchAttempt.ID()
	dagRunMismatchStatus.Status = core.Running

	require.NoError(t, dagRunMismatchAttempt.Open(ctx))
	err = dagRunMismatchAttempt.Write(ctx, dagRunMismatchStatus)
	require.ErrorContains(t, err, "status dag-run ID")
	require.NoError(t, dagRunMismatchAttempt.Close(ctx))

	nameMismatchAttempt, err := s.CreateAttempt(ctx, dag, base.Add(time.Second), "run-2", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-2"})
	require.NoError(t, err)
	nameMismatchStatus := exec.InitialStatus(dag)
	nameMismatchStatus.Name = "other-dag"
	nameMismatchStatus.DAGRunID = "run-2"
	nameMismatchStatus.AttemptID = nameMismatchAttempt.ID()
	nameMismatchStatus.Status = core.Running

	require.NoError(t, nameMismatchAttempt.Open(ctx))
	err = nameMismatchAttempt.Write(ctx, nameMismatchStatus)
	require.ErrorContains(t, err, "status name")
	require.NoError(t, nameMismatchAttempt.Close(ctx))

	attemptMismatchAttempt, err := s.CreateAttempt(ctx, dag, base.Add(2*time.Second), "run-3", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-3"})
	require.NoError(t, err)
	attemptMismatchStatus := exec.InitialStatus(dag)
	attemptMismatchStatus.DAGRunID = "run-3"
	attemptMismatchStatus.AttemptID = "other-attempt"
	attemptMismatchStatus.Status = core.Running

	require.NoError(t, attemptMismatchAttempt.Open(ctx))
	err = attemptMismatchAttempt.Write(ctx, attemptMismatchStatus)
	require.ErrorContains(t, err, "status attempt")
	require.NoError(t, attemptMismatchAttempt.Close(ctx))
}

func TestDAGRunStore_CompareAndSwapSubAttemptRejectsMissingRootName(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newDAGRunStore(t)
	rootDAG := testDAG("root")
	childDAG := testDAG("child")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	rootAttempt, err := s.CreateAttempt(ctx, rootDAG, base, "parent-run", exec.NewDAGRunAttemptOptions{AttemptID: "root-attempt"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, rootAttempt, rootDAG, "parent-run", core.Running)

	rootRef := exec.NewDAGRunRef(rootDAG.Name, "parent-run")
	subAttempt, err := s.CreateAttempt(ctx, childDAG, base.Add(time.Second), "child-run", exec.NewDAGRunAttemptOptions{
		RootDAGRun: &rootRef,
		AttemptID:  "child-attempt",
	})
	require.NoError(t, err)
	subStatus := writeDAGRunStatus(t, ctx, subAttempt, childDAG, "child-run", core.Running)

	_, swapped, err := s.CompareAndSwapLatestAttemptStatus(
		ctx,
		exec.NewDAGRunRef(childDAG.Name, "child-run"),
		subAttempt.ID(),
		core.Running,
		func(st *exec.DAGRunStatus) error {
			st.Status = core.Succeeded
			return nil
		},
		exec.WithCompareAndSwapRootDAGRun(exec.NewDAGRunRef("", "parent-run")),
	)
	require.ErrorContains(t, err, "root DAG name is required")
	assert.False(t, swapped)

	found, err := s.FindSubAttempt(ctx, rootRef, "child-run")
	require.NoError(t, err)
	assert.Equal(t, subStatus.Status, requireAttemptStatus(t, ctx, found).Status)
}

func TestDAGRunAttempt_WriteAllowsNestedSubRunParent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newDAGRunStore(t)
	rootDAG := testDAG("root")
	childDAG := testDAG("child")
	grandchildDAG := testDAG("grandchild")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	rootAttempt, err := s.CreateAttempt(ctx, rootDAG, base, "root-run", exec.NewDAGRunAttemptOptions{AttemptID: "root-attempt"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, rootAttempt, rootDAG, "root-run", core.Running)

	rootRef := exec.NewDAGRunRef(rootDAG.Name, "root-run")
	childAttempt, err := s.CreateAttempt(ctx, childDAG, base.Add(time.Second), "child-run", exec.NewDAGRunAttemptOptions{
		RootDAGRun: &rootRef,
		AttemptID:  "child-attempt",
	})
	require.NoError(t, err)
	writeSubDAGRunStatus(t, ctx, childAttempt, rootRef, childDAG, "child-run", core.Running)

	childRef := exec.NewDAGRunRef(childDAG.Name, "child-run")
	grandchildAttempt, err := s.CreateAttempt(ctx, grandchildDAG, base.Add(2*time.Second), "grandchild-run", exec.NewDAGRunAttemptOptions{
		RootDAGRun: &rootRef,
		AttemptID:  "grandchild-attempt",
	})
	require.NoError(t, err)

	grandchildStatus := exec.InitialStatus(grandchildDAG)
	grandchildStatus.Root = rootRef
	grandchildStatus.Parent = childRef
	grandchildStatus.DAGRunID = "grandchild-run"
	grandchildStatus.AttemptID = grandchildAttempt.ID()
	grandchildStatus.AttemptKey = exec.GenerateAttemptKey(rootRef.Name, rootRef.ID, grandchildDAG.Name, grandchildStatus.DAGRunID, grandchildAttempt.ID())
	grandchildStatus.Status = core.Succeeded
	require.NoError(t, grandchildAttempt.Open(ctx))
	require.NoError(t, grandchildAttempt.Write(ctx, grandchildStatus))
	require.NoError(t, grandchildAttempt.Close(ctx))

	found, err := s.FindSubAttempt(ctx, rootRef, "grandchild-run")
	require.NoError(t, err)
	foundStatus := requireAttemptStatus(t, ctx, found)
	assert.Equal(t, rootRef, foundStatus.Root)
	assert.Equal(t, childRef, foundStatus.Parent)
	assert.Equal(t, core.Succeeded, foundStatus.Status)
}

func TestDAGRunStore_RejectsMissingDAGRunRefNames(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newDAGRunStore(t)
	parent := testDAG("parent")
	child := testDAG("child")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	parentAttempt, err := s.CreateAttempt(ctx, parent, base, "parent-run", exec.NewDAGRunAttemptOptions{AttemptID: "parent-attempt"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, parentAttempt, parent, "parent-run", core.Running)

	rootRef := exec.NewDAGRunRef(parent.Name, "parent-run")
	subAttempt, err := s.CreateAttempt(ctx, child, base.Add(time.Second), "child-run", exec.NewDAGRunAttemptOptions{
		RootDAGRun: &rootRef,
		AttemptID:  "child-attempt",
	})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, subAttempt, child, "child-run", core.Running)

	_, swapped, err := s.CompareAndSwapLatestAttemptStatus(
		ctx,
		exec.NewDAGRunRef("", "parent-run"),
		parentAttempt.ID(),
		core.Running,
		func(st *exec.DAGRunStatus) error {
			st.Status = core.Succeeded
			return nil
		},
	)
	require.ErrorContains(t, err, "DAG name is required")
	assert.False(t, swapped)

	_, err = s.FindAttempt(ctx, exec.NewDAGRunRef("", "parent-run"))
	require.ErrorContains(t, err, "DAG name is required")

	_, err = s.FindSubAttempt(ctx, exec.NewDAGRunRef("", "parent-run"), "child-run")
	require.ErrorContains(t, err, "root DAG name is required")

	_, err = s.FindSubAttempt(ctx, rootRef, "")
	require.ErrorIs(t, err, store.ErrDAGRunIDEmpty)

	_, err = s.CreateSubAttempt(ctx, exec.NewDAGRunRef("", "parent-run"), "child-run-2")
	require.ErrorContains(t, err, "root DAG name is required")

	_, err = s.CreateAttempt(ctx, child, base.Add(2*time.Second), "child-run-2", exec.NewDAGRunAttemptOptions{
		RootDAGRun: &exec.DAGRunRef{ID: "parent-run"},
		AttemptID:  "child-attempt-2",
	})
	require.ErrorContains(t, err, "root DAG name is required")

	err = s.RemoveDAGRun(ctx, exec.NewDAGRunRef("", "parent-run"))
	require.ErrorContains(t, err, "DAG name is required")
}

func TestDAGRunStore_CreateSubAttemptRejectsDuplicateAttemptID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newDAGRunStore(t)
	parent := testDAG("parent")
	child := testDAG("child")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	parentAttempt, err := s.CreateAttempt(ctx, parent, base, "parent-run", exec.NewDAGRunAttemptOptions{AttemptID: "parent-attempt"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, parentAttempt, parent, "parent-run", core.Running)

	rootRef := exec.NewDAGRunRef(parent.Name, "parent-run")
	first, err := s.CreateAttempt(ctx, child, base.Add(time.Second), "child-run", exec.NewDAGRunAttemptOptions{
		RootDAGRun: &rootRef,
		AttemptID:  "child-attempt",
	})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, first, child, "child-run", core.Running)

	_, err = s.CreateAttempt(ctx, child, base.Add(2*time.Second), "child-run", exec.NewDAGRunAttemptOptions{
		RootDAGRun: &rootRef,
		Retry:      true,
		AttemptID:  "child-attempt",
	})
	require.ErrorIs(t, err, exec.ErrDAGRunAlreadyExists)

	found, err := s.FindSubAttempt(ctx, rootRef, "child-run")
	require.NoError(t, err)
	assert.Equal(t, "child-attempt", found.ID())
	assert.Equal(t, core.Running, requireAttemptStatus(t, ctx, found).Status)
}

func TestDAGRunStore_RenameDAGRunsPreservesSubAttempts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newDAGRunStore(t)
	parent := testDAG("old-parent")
	child := testDAG("child")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	parentAttempt, err := s.CreateAttempt(ctx, parent, base, "parent-run", exec.NewDAGRunAttemptOptions{AttemptID: "parent-attempt"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, parentAttempt, parent, "parent-run", core.Running)
	require.NoError(t, parentAttempt.WriteOutputs(ctx, &exec.DAGRunOutputs{
		Metadata: exec.OutputsMetadata{DAGName: parent.Name, DAGRunID: "parent-run", AttemptID: parentAttempt.ID(), Status: core.Running.String()},
		Outputs:  map[string]string{"root": "ok"},
	}))
	parentOutputsBeforeRename, err := parentAttempt.ReadOutputs(ctx)
	require.NoError(t, err)
	require.NotNil(t, parentOutputsBeforeRename)
	assert.Equal(t, "old-parent", parentOutputsBeforeRename.Metadata.DAGName)

	rootRef := exec.NewDAGRunRef(parent.Name, "parent-run")
	subAttempt, err := s.CreateAttempt(ctx, child, base.Add(time.Second), "child-run", exec.NewDAGRunAttemptOptions{
		RootDAGRun: &rootRef,
		AttemptID:  "child-attempt",
	})
	require.NoError(t, err)

	subStatus := exec.InitialStatus(child)
	subStatus.Root = rootRef
	subStatus.Parent = rootRef
	subStatus.DAGRunID = "child-run"
	subStatus.AttemptID = subAttempt.ID()
	subStatus.AttemptKey = exec.GenerateAttemptKey(parent.Name, rootRef.ID, child.Name, subStatus.DAGRunID, subAttempt.ID())
	subStatus.Status = core.Succeeded
	require.NoError(t, subAttempt.Open(ctx))
	require.NoError(t, subAttempt.Write(ctx, subStatus))
	require.NoError(t, subAttempt.Close(ctx))
	require.NoError(t, subAttempt.WriteOutputs(ctx, &exec.DAGRunOutputs{
		Metadata: exec.OutputsMetadata{DAGName: child.Name, DAGRunID: "child-run", AttemptID: subAttempt.ID(), Status: core.Succeeded.String()},
		Outputs:  map[string]string{"child": "ok"},
	}))

	require.NoError(t, s.RenameDAGRuns(ctx, "old-parent", "new-parent"))

	newRootRef := exec.NewDAGRunRef("new-parent", "parent-run")
	foundParent, err := s.FindAttempt(ctx, newRootRef)
	require.NoError(t, err)
	assert.Equal(t, "new-parent", requireAttemptStatus(t, ctx, foundParent).Name)
	parentOutputs, err := foundParent.ReadOutputs(ctx)
	require.NoError(t, err)
	require.NotNil(t, parentOutputs)
	assert.Equal(t, "new-parent", parentOutputs.Metadata.DAGName)
	assert.Equal(t, map[string]string{"root": "ok"}, parentOutputs.Outputs)

	foundSub, err := s.FindSubAttempt(ctx, newRootRef, "child-run")
	require.NoError(t, err)
	foundSubStatus := requireAttemptStatus(t, ctx, foundSub)
	assert.Equal(t, "child", foundSubStatus.Name)
	assert.Equal(t, "new-parent", foundSubStatus.Root.Name)
	assert.Equal(t, "new-parent", foundSubStatus.Parent.Name)
	subOutputs, err := foundSub.ReadOutputs(ctx)
	require.NoError(t, err)
	require.NotNil(t, subOutputs)
	assert.Equal(t, "child", subOutputs.Metadata.DAGName)
	assert.Equal(t, map[string]string{"child": "ok"}, subOutputs.Outputs)

	_, err = s.FindSubAttempt(ctx, rootRef, "child-run")
	require.Error(t, err)
}

func TestDAGRunStore_RenameDAGRunsRejectsDestinationConflict(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newDAGRunStore(t)
	oldDAG := testDAG("old-dag")
	newDAG := testDAG("new-dag")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	oldAttempt, err := s.CreateAttempt(ctx, oldDAG, base, "run-1", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-a"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, oldAttempt, oldDAG, "run-1", core.Failed)

	newAttempt, err := s.CreateAttempt(ctx, newDAG, base, "run-1", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-a"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, newAttempt, newDAG, "run-1", core.Succeeded)

	err = s.RenameDAGRuns(ctx, "old-dag", "new-dag")
	require.ErrorIs(t, err, exec.ErrDAGRunAlreadyExists)

	foundNew, err := s.FindAttempt(ctx, exec.NewDAGRunRef("new-dag", "run-1"))
	require.NoError(t, err)
	assert.Equal(t, core.Succeeded, requireAttemptStatus(t, ctx, foundNew).Status)

	foundOld, err := s.FindAttempt(ctx, exec.NewDAGRunRef("old-dag", "run-1"))
	require.NoError(t, err)
	assert.Equal(t, core.Failed, requireAttemptStatus(t, ctx, foundOld).Status)
}

func TestDAGRunStore_RenameDAGRunsLocksDestinationRunNamespace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	col := &recordingLockCollection{Collection: testutil.NewMemoryBackend().Collection("dag_runs")}
	s := store.NewDAGRunStore(
		col,
		store.WithDAGRunLatestStatusToday(false),
		store.WithDAGRunLocation(time.UTC),
	)
	oldDAG := testDAG("old-dag")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	att, err := s.CreateAttempt(ctx, oldDAG, base, "run-1", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-a"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, att, oldDAG, "run-1", core.Succeeded)

	require.NoError(t, s.RenameDAGRuns(ctx, "old-dag", "new-dag"))
	keys := col.lockedKeys()
	assert.Contains(t, keys, "rename/old-dag")
	assert.Contains(t, keys, "rename/new-dag")
	assert.Contains(t, keys, "runs/new-dag/run-1/lock")
}

func TestDAGRunStore_RemoveDAGRunReturnsNotFoundForMissingRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newDAGRunStore(t)

	err := s.RemoveDAGRun(ctx, exec.NewDAGRunRef("missing-dag", "missing-run"))
	require.ErrorIs(t, err, exec.ErrDAGRunIDNotFound)
}

func TestDAGRunAttempt_MetadataHelpers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newDAGRunStore(t)
	dag := testDAG("helpers-dag")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	att, err := s.CreateAttempt(ctx, dag, base, "run-1", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-a"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, att, dag, "run-1", core.Running)

	aborting, err := att.IsAborting(ctx)
	require.NoError(t, err)
	assert.False(t, aborting)
	require.NoError(t, att.Abort(ctx))
	aborting, err = att.IsAborting(ctx)
	require.NoError(t, err)
	assert.True(t, aborting)

	require.NoError(t, att.WriteOutputs(ctx, &exec.DAGRunOutputs{
		Metadata: exec.OutputsMetadata{DAGName: dag.Name, DAGRunID: "run-1", AttemptID: att.ID(), Status: core.Succeeded.String()},
		Outputs:  map[string]string{"result": "ok"},
	}))
	outputs, err := att.ReadOutputs(ctx)
	require.NoError(t, err)
	require.NotNil(t, outputs)
	assert.Equal(t, "ok", outputs.Outputs["result"])

	require.NoError(t, att.WriteOutputs(ctx, &exec.DAGRunOutputs{
		Metadata: exec.OutputsMetadata{DAGName: dag.Name, DAGRunID: "run-1", AttemptID: att.ID(), Status: core.Succeeded.String()},
		Outputs:  map[string]string{},
	}))
	outputs, err = att.ReadOutputs(ctx)
	require.NoError(t, err)
	require.NotNil(t, outputs)
	assert.Equal(t, dag.Name, outputs.Metadata.DAGName)
	assert.Equal(t, "run-1", outputs.Metadata.DAGRunID)
	assert.Empty(t, outputs.Outputs)

	messages := []exec.LLMMessage{{Role: "assistant", Content: "ready"}}
	require.NoError(t, att.WriteStepMessages(ctx, "step-1", messages))
	gotMessages, err := att.ReadStepMessages(ctx, "step-1")
	require.NoError(t, err)
	assert.Equal(t, messages, gotMessages)

	require.NoError(t, att.Hide(ctx))
	assert.True(t, att.Hidden())
	_, err = s.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, "run-1"))
	assert.ErrorIs(t, err, exec.ErrNoStatusData)
}

func TestDAGRunStore_HiddenLatestRetryFallsBackToLatestVisibleAttempt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newDAGRunStore(t)
	dag := testDAG("hidden-retry-dag")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	first, err := s.CreateAttempt(ctx, dag, base, "run-1", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-a"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, first, dag, "run-1", core.Failed)

	retry, err := s.CreateAttempt(ctx, dag, base.Add(time.Second), "run-1", exec.NewDAGRunAttemptOptions{
		Retry:     true,
		AttemptID: "attempt-b",
	})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, retry, dag, "run-1", core.Queued)
	require.NoError(t, retry.Hide(ctx))

	found, err := s.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, "run-1"))
	require.NoError(t, err)
	assert.Equal(t, "attempt-a", found.ID())
	assert.Equal(t, core.Failed, requireAttemptStatus(t, ctx, found).Status)
}

func TestDAGRunStore_ListStatusesAndPages(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newDAGRunStore(t)
	alpha := testDAG("alpha", "team=platform")
	beta := testDAG("beta", "team=data")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	for _, tc := range []struct {
		dag     *core.DAG
		runID   string
		status  core.Status
		offset  time.Duration
		attempt string
	}{
		{dag: alpha, runID: "run-1", status: core.Succeeded, offset: 0, attempt: "attempt-a"},
		{dag: beta, runID: "run-2", status: core.Failed, offset: time.Second, attempt: "attempt-b"},
		{dag: alpha, runID: "run-3", status: core.Running, offset: 2 * time.Second, attempt: "attempt-c"},
	} {
		att, err := s.CreateAttempt(ctx, tc.dag, base.Add(tc.offset), tc.runID, exec.NewDAGRunAttemptOptions{AttemptID: tc.attempt})
		require.NoError(t, err)
		writeDAGRunStatus(t, ctx, att, tc.dag, tc.runID, tc.status)
	}

	filtered, err := s.ListStatuses(ctx,
		exec.WithExactName("alpha"),
		exec.WithLabels([]string{"team=platform"}),
		exec.WithAllHistory(),
	)
	require.NoError(t, err)
	require.Len(t, filtered, 2)
	assert.Equal(t, "run-3", filtered[0].DAGRunID)
	assert.Equal(t, "run-1", filtered[1].DAGRunID)

	page1, err := s.ListStatusesPage(ctx, exec.WithAllHistory(), exec.WithLimit(2))
	require.NoError(t, err)
	require.Len(t, page1.Items, 2)
	assert.Equal(t, "run-3", page1.Items[0].DAGRunID)
	assert.Equal(t, "run-2", page1.Items[1].DAGRunID)
	require.NotEmpty(t, page1.NextCursor)

	page2, err := s.ListStatusesPage(ctx, exec.WithAllHistory(), exec.WithLimit(2), exec.WithCursor(page1.NextCursor))
	require.NoError(t, err)
	require.Len(t, page2.Items, 1)
	assert.Equal(t, "run-1", page2.Items[0].DAGRunID)
	assert.Empty(t, page2.NextCursor)
}

func TestDAGRunStore_ListStatusesKeepsSameRunIDAcrossDAGNames(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newDAGRunStore(t)
	alpha := testDAG("alpha")
	beta := testDAG("beta")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	alphaAttempt, err := s.CreateAttempt(ctx, alpha, base, "shared-run", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-a"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, alphaAttempt, alpha, "shared-run", core.Succeeded)

	betaAttempt, err := s.CreateAttempt(ctx, beta, base.Add(time.Second), "shared-run", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-b"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, betaAttempt, beta, "shared-run", core.Failed)

	statuses, err := s.ListStatuses(ctx, exec.WithAllHistory())
	require.NoError(t, err)
	require.Len(t, statuses, 2)
	assert.Equal(t, "beta", statuses[0].Name)
	assert.Equal(t, "alpha", statuses[1].Name)
	assert.Equal(t, "shared-run", statuses[0].DAGRunID)
	assert.Equal(t, "shared-run", statuses[1].DAGRunID)
}

func TestDAGRunStore_ListStatusesDefaultUsesConfiguredLocation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc := time.FixedZone("minus-12", -12*60*60)
	s := newDAGRunStore(t, store.WithDAGRunLocation(loc))
	dag := testDAG("location-dag")

	now := time.Now()
	utcStart := now.UTC().Truncate(24 * time.Hour)
	localNow := now.In(loc)
	localStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc).UTC()
	require.NotEqual(t, utcStart, localStart)

	var runAt time.Time
	expectIncluded := localStart.Before(utcStart)
	if expectIncluded {
		runAt = localStart.Add(utcStart.Sub(localStart) / 2)
	} else {
		runAt = utcStart.Add(localStart.Sub(utcStart) / 2)
	}

	att, err := s.CreateAttempt(ctx, dag, runAt, "run-1", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-a"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, att, dag, "run-1", core.Succeeded)

	statuses, err := s.ListStatuses(ctx)
	require.NoError(t, err)
	if expectIncluded {
		require.Len(t, statuses, 1)
		assert.Equal(t, "run-1", statuses[0].DAGRunID)
	} else {
		assert.Empty(t, statuses)
	}
}

func TestDAGRunStore_ListStatusesPageRejectsChangedFiltersWithSharedCursorError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newDAGRunStore(t)
	dag := testDAG("cursor-dag")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	for _, tc := range []struct {
		runID   string
		status  core.Status
		offset  time.Duration
		attempt string
	}{
		{runID: "run-1", status: core.Succeeded, offset: time.Second, attempt: "attempt-a"},
		{runID: "run-0", status: core.Succeeded, offset: 0, attempt: "attempt-b"},
	} {
		att, err := s.CreateAttempt(ctx, dag, base.Add(tc.offset), tc.runID, exec.NewDAGRunAttemptOptions{AttemptID: tc.attempt})
		require.NoError(t, err)
		writeDAGRunStatus(t, ctx, att, dag, tc.runID, tc.status)
	}

	page, err := s.ListStatusesPage(
		ctx,
		exec.WithAllHistory(),
		exec.WithStatuses([]core.Status{core.Succeeded}),
		exec.WithLimit(1),
	)
	require.NoError(t, err)
	require.NotEmpty(t, page.NextCursor)

	_, err = s.ListStatusesPage(
		ctx,
		exec.WithAllHistory(),
		exec.WithStatuses([]core.Status{core.Failed}),
		exec.WithLimit(1),
		exec.WithCursor(page.NextCursor),
	)
	require.ErrorIs(t, err, exec.ErrInvalidDAGRunQueryCursor)
}

func TestDAGRunStore_ListStatusesPageRejectsChangedWorkspaceFilter(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newDAGRunStore(t)
	dag := testDAG("workspace-dag", "workspace=ops")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	for _, tc := range []struct {
		runID   string
		offset  time.Duration
		attempt string
	}{
		{runID: "run-1", offset: time.Second, attempt: "attempt-a"},
		{runID: "run-0", offset: 0, attempt: "attempt-b"},
	} {
		att, err := s.CreateAttempt(ctx, dag, base.Add(tc.offset), tc.runID, exec.NewDAGRunAttemptOptions{AttemptID: tc.attempt})
		require.NoError(t, err)
		writeDAGRunStatus(t, ctx, att, dag, tc.runID, core.Succeeded)
	}

	page, err := s.ListStatusesPage(
		ctx,
		exec.WithAllHistory(),
		exec.WithWorkspaceFilter(&exec.WorkspaceFilter{Enabled: true, Workspaces: []string{"ops"}}),
		exec.WithLimit(1),
	)
	require.NoError(t, err)
	require.NotEmpty(t, page.NextCursor)

	_, err = s.ListStatusesPage(
		ctx,
		exec.WithAllHistory(),
		exec.WithWorkspaceFilter(&exec.WorkspaceFilter{Enabled: true, Workspaces: []string{"dev"}}),
		exec.WithLimit(1),
		exec.WithCursor(page.NextCursor),
	)
	require.ErrorIs(t, err, exec.ErrInvalidDAGRunQueryCursor)
}

func TestDAGRunStore_RemoveOldDAGRunsUsesLatestAttemptActivity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("dag_runs")
	s := store.NewDAGRunStore(
		col,
		store.WithDAGRunLatestStatusToday(false),
		store.WithDAGRunLocation(time.UTC),
	)
	dag := testDAG("retention-dag")
	old := time.Now().UTC().AddDate(0, 0, -10)

	stale, err := s.CreateAttempt(ctx, dag, old, "stale-run", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-a"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, stale, dag, "stale-run", core.Failed)
	staleRecord, err := col.Get(ctx, "runs/retention-dag/stale-run/attempts/attempt-a")
	require.NoError(t, err)
	staleRecord.UpdatedAt = old
	require.NoError(t, col.Put(ctx, staleRecord))

	first, err := s.CreateAttempt(ctx, dag, old, "retried-run", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-a"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, first, dag, "retried-run", core.Failed)
	retry, err := s.CreateAttempt(ctx, dag, old.Add(time.Hour), "retried-run", exec.NewDAGRunAttemptOptions{
		Retry:     true,
		AttemptID: "attempt-b",
	})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, retry, dag, "retried-run", core.Succeeded)

	removed, err := s.RemoveOldDAGRuns(ctx, dag.Name, 7)
	require.NoError(t, err)
	assert.Contains(t, removed, "stale-run")
	assert.NotContains(t, removed, "retried-run")

	_, err = s.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, "stale-run"))
	require.Error(t, err)
	_, err = s.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, "retried-run"))
	require.NoError(t, err)
}

func TestDAGRunStore_RemoveOldDAGRunsPreservesStatuslessLatestAttempts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newDAGRunStore(t)
	dag := testDAG("statusless-retention-dag")
	old := time.Now().UTC().AddDate(0, 0, -10)

	_, err := s.CreateAttempt(ctx, dag, old, "statusless-run", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-a"})
	require.NoError(t, err)

	removed, err := s.RemoveOldDAGRuns(ctx, dag.Name, 0)
	require.NoError(t, err)
	assert.NotContains(t, removed, "statusless-run")

	_, err = s.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, "statusless-run"))
	require.NoError(t, err)
}

func TestDAGRunStore_RemoveOldDAGRunsRetentionRunsZeroRemovesEligibleRuns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newDAGRunStore(t)
	dag := testDAG("retention-zero-dag")
	old := time.Now().UTC().AddDate(0, 0, -10)

	first, err := s.CreateAttempt(ctx, dag, old, "run-1", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-a"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, first, dag, "run-1", core.Failed)

	second, err := s.CreateAttempt(ctx, dag, old.Add(time.Second), "run-2", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-b"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, second, dag, "run-2", core.Succeeded)

	removed, err := s.RemoveOldDAGRuns(ctx, dag.Name, -1, exec.WithRetentionRuns(0))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"run-1", "run-2"}, removed)

	_, err = s.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, "run-1"))
	require.Error(t, err)
	_, err = s.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, "run-2"))
	require.Error(t, err)
}

func TestDAGRunAttempt_StepMessagesPersistAcrossRetries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newDAGRunStore(t)
	dag := testDAG("messages-dag")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	first, err := s.CreateAttempt(ctx, dag, base, "run-1", exec.NewDAGRunAttemptOptions{AttemptID: "attempt-a"})
	require.NoError(t, err)
	messages := []exec.LLMMessage{{Role: "assistant", Content: "ready"}}
	require.NoError(t, first.WriteStepMessages(ctx, "step-1", messages))

	retry, err := s.CreateAttempt(ctx, dag, base.Add(time.Hour), "run-1", exec.NewDAGRunAttemptOptions{
		Retry:     true,
		AttemptID: "attempt-b",
	})
	require.NoError(t, err)
	gotMessages, err := retry.ReadStepMessages(ctx, "step-1")
	require.NoError(t, err)
	assert.Equal(t, messages, gotMessages)
}

func TestDAGRunStore_ListStatusesExcludesSubAttempts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newDAGRunStore(t)
	parent := testDAG("parent")
	child := testDAG("child")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	parentAttempt, err := s.CreateAttempt(ctx, parent, base, "parent-run", exec.NewDAGRunAttemptOptions{AttemptID: "parent-attempt"})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, parentAttempt, parent, "parent-run", core.Running)

	rootRef := exec.NewDAGRunRef(parent.Name, "parent-run")
	subAttempt, err := s.CreateAttempt(ctx, child, base.Add(time.Second), "child-run", exec.NewDAGRunAttemptOptions{
		RootDAGRun: &rootRef,
		AttemptID:  "child-attempt",
	})
	require.NoError(t, err)
	writeDAGRunStatus(t, ctx, subAttempt, child, "child-run", core.Succeeded)

	foundSub, err := s.FindSubAttempt(ctx, rootRef, "child-run")
	require.NoError(t, err)
	assert.Equal(t, "child-attempt", foundSub.ID())

	foundParent, err := s.FindAttempt(ctx, rootRef)
	require.NoError(t, err)
	assert.Equal(t, "parent-attempt", foundParent.ID())

	statuses, err := s.ListStatuses(ctx, exec.WithAllHistory())
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.Equal(t, "parent-run", statuses[0].DAGRunID)
}
