// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// subRunFixture writes parent runs and their sub DAG-runs the way dag.run
// does: a child attempt is created under the root run.
type subRunFixture struct {
	t          *testing.T
	ctx        context.Context
	store      *Store
	repository *persis.DAGRunRepository
	child      *ir.DAG
}

func newSubRunFixture(t *testing.T) *subRunFixture {
	t.Helper()
	baseDir := t.TempDir()
	store := NewStore(baseDir, WithArtifactDir(filepath.Join(baseDir, "artifacts")))
	return &subRunFixture{
		t:          t,
		ctx:        context.Background(),
		store:      store,
		repository: persis.NewDAGRunRepository(store, NewWorkDirStore(filepath.Join(baseDir, ".work"), baseDir), persis.DAGRunRepositoryOptions{}),
		child:      &ir.DAG{Name: "child"},
	}
}

// runParent writes a finished run of parent at ts that ran the child as
// childRunID one second later, and returns the parent's run ref.
func (f *subRunFixture) runParent(parent string, ts time.Time, runID, childRunID string, ephemeral bool) ir.DAGRunRef {
	f.t.Helper()
	parentDAG := &ir.DAG{Name: parent}
	rootRef := ir.NewDAGRunRef(parent, runID)
	parentAttempt, err := f.repository.CreateAttempt(f.ctx, parentDAG, ts, runID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(f.t, err)
	require.NoError(f.t, parentAttempt.Open(f.ctx))
	parentStatus := ir.InitialStatus(parentDAG)
	parentStatus.DAGRunID = runID
	parentStatus.AttemptID = parentAttempt.ID()
	parentStatus.Status = ir.Succeeded
	require.NoError(f.t, parentAttempt.Write(f.ctx, parentStatus))
	require.NoError(f.t, parentAttempt.Close(f.ctx))

	f.writeChild(rootRef, ts.Add(time.Second), childRunID, ephemeral)
	return rootRef
}

func (f *subRunFixture) writeChild(rootRef ir.DAGRunRef, ts time.Time, childRunID string, ephemeral bool) {
	f.t.Helper()
	childAttempt, err := f.repository.CreateAttempt(f.ctx, f.child, ts, childRunID, persis.DAGRunCreateAttemptOptions{
		RootDAGRun: rootRef,
	})
	require.NoError(f.t, err)
	require.NoError(f.t, childAttempt.Open(f.ctx))
	status := ir.InitialStatus(f.child)
	status.Root = rootRef
	status.Parent = rootRef
	status.DAGRunID = childRunID
	status.AttemptID = childAttempt.ID()
	status.Status = ir.Succeeded
	status.Ephemeral = ephemeral
	require.NoError(f.t, childAttempt.Write(f.ctx, status))
	require.NoError(f.t, childAttempt.Close(f.ctx))
}

func (f *subRunFixture) latestSub(notBefore time.Time) (*ir.DAGRunStatus, error) {
	f.t.Helper()
	query := persis.DAGRunLatestAttemptQuery{Name: f.child.Name}
	if !notBefore.IsZero() {
		query.NotBefore = persis.NewUTC(notBefore)
	}
	attempt, err := f.store.LatestSubAttempt(f.ctx, query)
	if err != nil {
		return nil, err
	}
	return attempt.ReadStatus(f.ctx)
}

func TestLatestSubAttempt(t *testing.T) {
	t.Parallel()

	f := newSubRunFixture(t)
	_, err := f.latestSub(time.Time{})
	require.ErrorIs(t, err, dagrun.ErrNoStatusData)

	early := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	later := early.Add(time.Hour)
	firstRoot := f.runParent("parent-a", early, "run-a", "child-a", false)
	status, err := f.latestSub(time.Time{})
	require.NoError(t, err)
	assert.Equal(t, "child-a", status.DAGRunID)
	assert.Equal(t, firstRoot, status.Root)

	// The newer run wins, even when another parent ran it.
	secondRoot := f.runParent("parent-b", later, "run-b", "child-b", false)
	status, err = f.latestSub(time.Time{})
	require.NoError(t, err)
	assert.Equal(t, "child-b", status.DAGRunID)
	assert.Equal(t, secondRoot, status.Root)

	// A later write to an older run does not move the pointer back.
	f.writeChild(firstRoot, early.Add(2*time.Second), "child-a-retry", false)
	status, err = f.latestSub(time.Time{})
	require.NoError(t, err)
	assert.Equal(t, "child-b", status.DAGRunID)

	// The child's own history is unchanged.
	_, err = f.store.LatestAttempt(f.ctx, persis.DAGRunLatestAttemptQuery{Name: f.child.Name})
	require.ErrorIs(t, err, dagrun.ErrNoStatusData)

	_, err = f.latestSub(later.Add(time.Hour))
	require.ErrorIs(t, err, dagrun.ErrNoStatusData, "a run before the cutoff is not reported")

	// Without the parent run there is nothing to show, and nothing is scanned.
	require.NoError(t, f.store.RemoveDAGRun(f.ctx, persis.DAGRunRemoveRequest{DAGRun: secondRoot}))
	_, err = f.latestSub(time.Time{})
	require.ErrorIs(t, err, dagrun.ErrNoStatusData)
}

func TestLatestSubAttemptSkipsEphemeralRuns(t *testing.T) {
	t.Parallel()

	f := newSubRunFixture(t)
	f.runParent("parent", time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC), "run", "child-run", true)

	_, err := f.latestSub(time.Time{})
	require.ErrorIs(t, err, dagrun.ErrNoStatusData)
}

// A DAG that runs itself under its own root run keeps no sub-run pointer: its
// own run already shows that run.
func TestLatestSubAttemptSkipsSelfRuns(t *testing.T) {
	t.Parallel()

	f := newSubRunFixture(t)
	f.runParent(f.child.Name, time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC), "run", "child-run", false)

	_, err := f.latestSub(time.Time{})
	require.ErrorIs(t, err, dagrun.ErrNoStatusData)
}

// Two DAG names can share a run directory, so a pointer written for one DAG
// is not reported for the other.
func TestLatestSubAttemptChecksDAGName(t *testing.T) {
	t.Parallel()

	f := newSubRunFixture(t)
	f.runParent("parent", time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC), "run", "child-run", false)
	path := latestSubAttemptPointerPath(f.store.dataRoot(f.child.Name).dagRunsDir)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var ptr latestSubAttemptPointer
	require.NoError(t, json.Unmarshal(data, &ptr))
	ptr.DAGName = "other"
	data, err = json.Marshal(ptr)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))

	_, err = f.latestSub(time.Time{})
	require.ErrorIs(t, err, dagrun.ErrNoStatusData)
}
