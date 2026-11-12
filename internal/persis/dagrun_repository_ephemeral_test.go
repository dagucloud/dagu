// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/artifactpath"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
	"github.com/dagucloud/dagu/v2/internal/persis/file/dagrun/dagrunindex"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/require"
)

func newEphemeralTestRepository(t *testing.T, now func() time.Time) *persis.DAGRunRepository {
	t.Helper()
	root := t.TempDir()
	return testutil.NewFileDAGRunRepositoryWithEphemeral(
		filepath.Join(root, "dag-runs"),
		filepath.Join(root, "dag-runs-ephemeral"),
		persis.DAGRunRepositoryOptions{Now: now},
	)
}

func writeTestRun(t *testing.T, repo *persis.DAGRunRepository, dag *ir.DAG, runID string, status ir.Status, finished time.Time, ephemeral bool) {
	t.Helper()
	ctx := context.Background()
	// A run's attempt is created when it starts, so no later than it
	// finished; a later attempt of the run sorts after it.
	createdAt := time.Now()
	if !finished.IsZero() && finished.Before(createdAt) {
		createdAt = finished
	}
	attempt, err := repo.CreateAttempt(ctx, dag, createdAt, runID, persis.DAGRunCreateAttemptOptions{Ephemeral: ephemeral})
	require.NoError(t, err)
	opts := []ir.StatusOption{ir.WithAttemptID(attempt.ID()), ir.WithEphemeral(ephemeral)}
	if !finished.IsZero() {
		opts = append(opts, ir.WithFinishedAt(finished))
	}
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, ir.NewStatusBuilder(dag).Create(runID, status, 0, time.Now(), opts...)))
	require.NoError(t, attempt.Close(ctx))
}

// An ephemeral run is found by its ID but reaches no listing, no latest
// lookup and no retention pass.
func TestEphemeralRunIsReadableButUnlisted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dag, err := spec.LoadYAML(ctx, []byte("name: eph\nsteps:\n  - run: echo hi\n"))
	require.NoError(t, err)
	repo := newEphemeralTestRepository(t, nil)

	writeTestRun(t, repo, dag, "normal", ir.Succeeded, time.Now(), false)
	writeTestRun(t, repo, dag, "ephemeral", ir.Succeeded, time.Now(), true)

	attempt, err := repo.FindAttempt(ctx, ir.NewDAGRunRef(dag.Name, "ephemeral"))
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.True(t, status.Ephemeral)

	listed, err := repo.ListStatuses(ctx, persis.DAGRunListOptions{AllHistory: true, Unbounded: true})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, "normal", listed[0].DAGRunID)

	latest, err := repo.LatestAttempt(ctx, dag.Name, persis.DAGRunLatestAttemptOptions{AllHistory: true})
	require.NoError(t, err)
	latestStatus, err := latest.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, "normal", latestStatus.DAGRunID)

	recent, err := repo.RecentStatuses(ctx, dag.Name, 10)
	require.NoError(t, err)
	require.Len(t, recent, 1)

	// Retention keeps the one normal run: the ephemeral run is neither counted
	// nor removed.
	keep := 1
	removed, err := repo.RemoveOldDAGRuns(ctx, dag.Name, 0, persis.DAGRunRetentionOptions{RetentionRuns: &keep})
	require.NoError(t, err)
	require.Empty(t, removed)
	_, err = repo.FindAttempt(ctx, ir.NewDAGRunRef(dag.Name, "ephemeral"))
	require.NoError(t, err)
}

// A retry attempt and a child run of an ephemeral run stay in the ephemeral
// store, so they stay out of the history too. The child's status says it is
// ephemeral whatever its writer sets, so no lookup takes it for history.
func TestEphemeralRunAttemptsFollowTheRoot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dag, err := spec.LoadYAML(ctx, []byte("name: eph\nsteps:\n  - run: echo hi\n"))
	require.NoError(t, err)
	repo := newEphemeralTestRepository(t, nil)
	writeTestRun(t, repo, dag, "root", ir.Failed, time.Now(), true)
	root := ir.NewDAGRunRef(dag.Name, "root")

	retry, err := repo.CreateAttempt(ctx, dag, time.Now(), "root", persis.DAGRunCreateAttemptOptions{Retry: true})
	require.NoError(t, err)
	require.NoError(t, retry.Open(ctx))
	require.NoError(t, retry.Write(ctx, ir.NewStatusBuilder(dag).Create("root", ir.Succeeded, 0, time.Now(),
		ir.WithAttemptID(retry.ID()), ir.WithEphemeral(true), ir.WithFinishedAt(time.Now()))))
	require.NoError(t, retry.Close(ctx))

	child, err := repo.CreateAttempt(ctx, dag, time.Now(), "child", persis.DAGRunCreateAttemptOptions{RootDAGRun: root})
	require.NoError(t, err)
	require.NoError(t, child.Open(ctx))
	require.NoError(t, child.Write(ctx, ir.NewStatusBuilder(dag).Create("child", ir.Succeeded, 0, time.Now(),
		ir.WithAttemptID(child.ID()), ir.WithHierarchyRefs(root, root))))
	require.NoError(t, child.Close(ctx))

	found, err := repo.FindSubAttempt(ctx, root, "child")
	require.NoError(t, err)
	status, err := found.ReadStatus(ctx)
	require.NoError(t, err)
	require.True(t, status.Ephemeral)
	listed, err := repo.ListStatuses(ctx, persis.DAGRunListOptions{AllHistory: true, Unbounded: true})
	require.NoError(t, err)
	require.Empty(t, listed)
}

// The ephemeral option applies only to a new root run: a retry and a child of
// a history run stay in the history with their root.
func TestEphemeralOptionFollowsHistoryRoot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dag, err := spec.LoadYAML(ctx, []byte("name: eph\nsteps:\n  - run: echo hi\n"))
	require.NoError(t, err)
	repo := newEphemeralTestRepository(t, nil)
	writeTestRun(t, repo, dag, "root", ir.Failed, time.Now(), false)
	root := ir.NewDAGRunRef(dag.Name, "root")

	retry, err := repo.CreateAttempt(ctx, dag, time.Now(), "root", persis.DAGRunCreateAttemptOptions{Retry: true, Ephemeral: true})
	require.NoError(t, err)
	require.NoError(t, retry.Open(ctx))
	require.NoError(t, retry.Write(ctx, ir.NewStatusBuilder(dag).Create("root", ir.Succeeded, 0, time.Now(),
		ir.WithAttemptID(retry.ID()), ir.WithFinishedAt(time.Now()))))
	require.NoError(t, retry.Close(ctx))

	child, err := repo.CreateAttempt(ctx, dag, time.Now(), "child", persis.DAGRunCreateAttemptOptions{RootDAGRun: root, Ephemeral: true})
	require.NoError(t, err)
	require.NoError(t, child.Open(ctx))
	require.NoError(t, child.Write(ctx, ir.NewStatusBuilder(dag).Create("child", ir.Succeeded, 0, time.Now(),
		ir.WithAttemptID(child.ID()), ir.WithHierarchyRefs(root, root))))
	require.NoError(t, child.Close(ctx))

	listed, err := repo.ListStatuses(ctx, persis.DAGRunListOptions{AllHistory: true, Unbounded: true})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, ir.Succeeded, listed[0].Status)
	_, err = repo.FindSubAttempt(ctx, root, "child")
	require.NoError(t, err)
}

// The cleanup removes an ephemeral run once it has ended for the TTL, and
// never removes a run that has not ended.
func TestRemoveEndedEphemeralRuns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dag, err := spec.LoadYAML(ctx, []byte("name: eph\nsteps:\n  - run: echo hi\n"))
	require.NoError(t, err)
	now := time.Now()
	repo := newEphemeralTestRepository(t, func() time.Time { return now })

	writeTestRun(t, repo, dag, "old", ir.Succeeded, now.Add(-persis.EphemeralRunTTL-time.Minute), true)
	writeTestRun(t, repo, dag, "recent", ir.Succeeded, now.Add(-time.Minute), true)
	writeTestRun(t, repo, dag, "running", ir.Running, time.Time{}, true)
	writeTestRun(t, repo, dag, "history", ir.Succeeded, now.Add(-24*time.Hour), false)
	// The agent writes NotStarted before the plan starts; the run was queued
	// long enough ago to look ended by its creation time.
	starting, err := repo.CreateAttempt(ctx, dag, now, "starting", persis.DAGRunCreateAttemptOptions{Ephemeral: true})
	require.NoError(t, err)
	require.NoError(t, starting.Open(ctx))
	require.NoError(t, starting.Write(ctx, ir.NewStatusBuilder(dag).Create("starting", ir.NotStarted, 0, time.Time{},
		ir.WithAttemptID(starting.ID()), ir.WithEphemeral(true),
		ir.WithCreatedAt(now.Add(-persis.EphemeralRunTTL-time.Minute).UnixMilli()))))
	require.NoError(t, starting.Close(ctx))

	removed, err := repo.RemoveEndedEphemeralRuns(ctx, persis.EphemeralRunTTL)
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	_, err = repo.FindAttempt(ctx, ir.NewDAGRunRef(dag.Name, "old"))
	require.ErrorIs(t, err, dagrun.ErrDAGRunIDNotFound)
	for _, id := range []string{"recent", "running", "starting", "history"} {
		_, err = repo.FindAttempt(ctx, ir.NewDAGRunRef(dag.Name, id))
		require.NoError(t, err, id)
	}

	// A zero TTL, as used at server start, removes every ended run but still
	// spares the active one and the history.
	removed, err = repo.RemoveEndedEphemeralRuns(ctx, 0)
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	for _, id := range []string{"running", "starting", "history"} {
		_, err = repo.FindAttempt(ctx, ir.NewDAGRunRef(dag.Name, id))
		require.NoError(t, err, id)
	}
}

// listHookStore runs afterList once, after its first status query returns.
type listHookStore struct {
	persis.DAGRunStore
	afterList func()
}

func (s *listHookStore) QueryStatuses(ctx context.Context, query persis.DAGRunStatusQuery) (persis.DAGRunStatusPage, error) {
	page, err := s.DAGRunStore.QueryStatuses(ctx, query)
	if hook := s.afterList; hook != nil {
		s.afterList = nil
		hook()
	}
	return page, err
}

// The cleanup keeps a run whose retry starts between the listing and the
// removal. The retry attempt has no status yet, as before the runner opens
// it, or a NotStarted one, as a dispatched attempt has.
func TestRemoveEndedEphemeralRunsKeepsStartingRetry(t *testing.T) {
	t.Parallel()

	for name, start := range map[string]func(t *testing.T, dag *ir.DAG, retry dagrun.Attempt){
		"no status": func(*testing.T, *ir.DAG, dagrun.Attempt) {},
		"not started": func(t *testing.T, dag *ir.DAG, retry dagrun.Attempt) {
			ctx := context.Background()
			require.NoError(t, retry.Open(ctx))
			require.NoError(t, retry.Write(ctx, ir.NewStatusBuilder(dag).Create("old", ir.NotStarted, 0, time.Time{},
				ir.WithAttemptID(retry.ID()), ir.WithEphemeral(true))))
			require.NoError(t, retry.Close(ctx))
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			dag, err := spec.LoadYAML(ctx, []byte("name: eph\nsteps:\n  - run: echo hi\n"))
			require.NoError(t, err)
			now := time.Now()
			root := t.TempDir()
			base := filepath.Join(root, "dag-runs")
			ephemeral := &listHookStore{DAGRunStore: filedagrun.NewStore(filepath.Join(root, "dag-runs-ephemeral"))}
			repo := persis.NewDAGRunRepository(
				filedagrun.NewStore(base),
				filedagrun.NewWorkDirStore(filepath.Join(base, ".dag-run-work"), base),
				persis.DAGRunRepositoryOptions{Now: func() time.Time { return now }, EphemeralStore: ephemeral},
			)
			writeTestRun(t, repo, dag, "old", ir.Failed, now.Add(-persis.EphemeralRunTTL-time.Minute), true)

			ephemeral.afterList = func() {
				retry, err := repo.CreateAttempt(ctx, dag, time.Now(), "old", persis.DAGRunCreateAttemptOptions{Retry: true})
				require.NoError(t, err)
				start(t, dag, retry)
			}
			removed, err := repo.RemoveEndedEphemeralRuns(ctx, persis.EphemeralRunTTL)
			require.NoError(t, err)
			require.Zero(t, removed)
			_, err = repo.FindAttempt(ctx, ir.NewDAGRunRef(dag.Name, "old"))
			require.NoError(t, err)
		})
	}
}

// The history of a DAG comes in pages of older runs, newest first. Ephemeral
// runs come only on request, on the first page, merged by start time.
func TestRecentStatusesPage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dag, err := spec.LoadYAML(ctx, []byte("name: eph\nsteps:\n  - run: echo hi\n"))
	require.NoError(t, err)
	repo := newEphemeralTestRepository(t, nil)

	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	write := func(runID string, offset time.Duration, ephemeral bool) {
		start := base.Add(offset)
		attempt, err := repo.CreateAttempt(ctx, dag, start, runID, persis.DAGRunCreateAttemptOptions{Ephemeral: ephemeral})
		require.NoError(t, err)
		status := ir.NewStatusBuilder(dag).Create(runID, ir.Succeeded, 0, start,
			ir.WithAttemptID(attempt.ID()), ir.WithEphemeral(ephemeral), ir.WithFinishedAt(start.Add(time.Second)))
		require.NoError(t, attempt.Open(ctx))
		require.NoError(t, attempt.Write(ctx, status))
		require.NoError(t, attempt.Close(ctx))
	}
	write("n1", 0, false)
	write("e1", time.Minute, true)
	write("n2", 2*time.Minute, false)
	write("e2", 3*time.Minute, true)

	read := func(options persis.RecentStatusesPageOptions) ([]string, string) {
		page, err := repo.RecentStatusesPage(ctx, dag.Name, options)
		require.NoError(t, err)
		var ids []string
		for _, status := range page.Items {
			ids = append(ids, status.DAGRunID)
		}
		return ids, page.NextCursor
	}

	ids, cursor := read(persis.RecentStatusesPageOptions{Limit: 10})
	require.Equal(t, []string{"n2", "n1"}, ids)
	require.Empty(t, cursor)

	ids, cursor = read(persis.RecentStatusesPageOptions{Limit: 10, IncludeEphemeral: true})
	require.Equal(t, []string{"e2", "n2", "e1", "n1"}, ids)
	require.Empty(t, cursor)

	// e1 is older than n2: it would sort among the runs of the next page.
	ids, cursor = read(persis.RecentStatusesPageOptions{Limit: 1, IncludeEphemeral: true})
	require.Equal(t, []string{"e2", "n2"}, ids)
	require.NotEmpty(t, cursor)
	ids, cursor = read(persis.RecentStatusesPageOptions{Limit: 1, Cursor: cursor, IncludeEphemeral: true})
	require.Equal(t, []string{"n1"}, ids)
	require.Empty(t, cursor)

	// A limit that is not positive takes the default.
	ids, _ = read(persis.RecentStatusesPageOptions{IncludeEphemeral: true})
	require.Equal(t, []string{"e2", "n2", "e1", "n1"}, ids)
}

// A day with enough runs is listed from its run index, whose summaries have
// no nodes. A page of the history still holds whole statuses.
func TestRecentStatusesPageReadsWholeStatuses(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dag, err := spec.LoadYAML(ctx, []byte("name: day\nsteps:\n  - run: echo hi\n"))
	require.NoError(t, err)
	repo := newEphemeralTestRepository(t, nil)

	noon := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	for i := range dagrunindex.MinRunsForIndex + 2 {
		start := noon.Add(time.Duration(i) * time.Minute)
		runID := fmt.Sprintf("run-%02d", i)
		attempt, err := repo.CreateAttempt(ctx, dag, start, runID, persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, attempt.Open(ctx))
		require.NoError(t, attempt.Write(ctx, ir.NewStatusBuilder(dag).Create(runID, ir.Succeeded, 0, start,
			ir.WithAttemptID(attempt.ID()), ir.WithFinishedAt(start.Add(time.Second)))))
		require.NoError(t, attempt.Close(ctx))
	}

	page, err := repo.RecentStatusesPage(ctx, dag.Name, persis.RecentStatusesPageOptions{Limit: 5})
	require.NoError(t, err)
	require.Len(t, page.Items, 5)
	for _, status := range page.Items {
		require.Len(t, status.Nodes, 1, status.DAGRunID)
	}
}

// Runs created in the same second keep the order of their IDs, which grow
// with time: the newer run comes first.
func TestRecentStatusesPageOrdersRunsOfOneSecond(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dag, err := spec.LoadYAML(ctx, []byte("name: second\nsteps:\n  - run: echo hi\n"))
	require.NoError(t, err)
	repo := newEphemeralTestRepository(t, nil)

	at := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	var ids []string
	for range 3 {
		runID, err := ir.NewDAGRunID()
		require.NoError(t, err)
		ids = append([]string{runID}, ids...)
		attempt, err := repo.CreateAttempt(ctx, dag, at, runID, persis.DAGRunCreateAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, attempt.Open(ctx))
		require.NoError(t, attempt.Write(ctx, ir.NewStatusBuilder(dag).Create(runID, ir.Succeeded, 0, at,
			ir.WithAttemptID(attempt.ID()), ir.WithFinishedAt(at))))
		require.NoError(t, attempt.Close(ctx))
	}

	var got []string
	cursor := ""
	for {
		page, err := repo.RecentStatusesPage(ctx, dag.Name, persis.RecentStatusesPageOptions{Limit: 2, Cursor: cursor})
		require.NoError(t, err)
		for _, status := range page.Items {
			got = append(got, status.DAGRunID)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	require.Equal(t, ids, got)
}

// A run ID names one run across the history and the ephemeral store.
func TestRunIDIsUniqueAcrossStores(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dag, err := spec.LoadYAML(ctx, []byte("name: eph\nsteps:\n  - run: echo hi\n"))
	require.NoError(t, err)
	repo := newEphemeralTestRepository(t, nil)

	writeTestRun(t, repo, dag, "eph-run", ir.Succeeded, time.Now(), true)
	writeTestRun(t, repo, dag, "history-run", ir.Succeeded, time.Now(), false)

	_, err = repo.CreateAttempt(ctx, dag, time.Now(), "eph-run", persis.DAGRunCreateAttemptOptions{})
	require.ErrorIs(t, err, dagrun.ErrDAGRunAlreadyExists)
	_, err = repo.CreateAttempt(ctx, dag, time.Now(), "history-run", persis.DAGRunCreateAttemptOptions{Ephemeral: true})
	require.ErrorIs(t, err, dagrun.ErrDAGRunAlreadyExists)
}

// An ephemeral run whose attempts are all hidden is still found in the
// ephemeral store, as a run with no status data.
func TestFindAttemptReportsHiddenEphemeralRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dag, err := spec.LoadYAML(ctx, []byte("name: eph\nsteps:\n  - run: echo hi\n"))
	require.NoError(t, err)
	repo := newEphemeralTestRepository(t, nil)

	writeTestRun(t, repo, dag, "hidden", ir.Queued, time.Time{}, true)
	attempt, err := repo.FindAttempt(ctx, ir.NewDAGRunRef(dag.Name, "hidden"))
	require.NoError(t, err)
	require.NoError(t, attempt.Hide(ctx))

	_, err = repo.FindAttempt(ctx, ir.NewDAGRunRef(dag.Name, "hidden"))
	require.ErrorIs(t, err, dagrun.ErrNoStatusData)
}

// An artifact sweep keeps the artifacts of an ephemeral run: the run shares
// the artifact root with the history.
func TestPruneArtifactsKeepsEphemeralRuns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dag, err := spec.LoadYAML(ctx, []byte("name: eph\nsteps:\n  - run: echo hi\n"))
	require.NoError(t, err)
	root := t.TempDir()
	repo := testutil.NewFileDAGRunRepositoryWithEphemeral(
		filepath.Join(root, "dag-runs"),
		filepath.Join(root, "dag-runs-ephemeral"),
		persis.DAGRunRepositoryOptions{},
	)
	writeTestRun(t, repo, dag, "eph-run", ir.Running, time.Time{}, true)

	old := time.Now().Add(-72 * time.Hour)
	artifacts := filepath.Join(root, "artifacts")
	live, err := artifactpath.NewRunDir(ctx, artifacts, "", dag.Name, "eph-run", old)
	require.NoError(t, err)
	gone, err := artifactpath.NewRunDir(ctx, artifacts, "", dag.Name, "gone-run", old)
	require.NoError(t, err)

	_, err = repo.PruneArtifacts(ctx, persis.ArtifactPruneRequest{OlderThan: persis.NewUTC(time.Now().Add(-24 * time.Hour))})
	require.NoError(t, err)
	require.DirExists(t, live)
	require.NoDirExists(t, gone)
}
