// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"cmp"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	"github.com/stretchr/testify/require"
)

// seededRun is what a test wrote for one run, to compute expected pages.
type seededRun struct {
	name      string
	id        string
	created   time.Time
	status    ir.Status
	labels    []string
	attemptID string
	trigger   ir.TriggerType
}

type oracleSeed struct {
	th   RepositoryTest
	runs []seededRun
}

func (s *oracleSeed) write(t *testing.T, run seededRun, retry bool) {
	t.Helper()
	ctx := s.th.Context
	dag := &ir.DAG{Name: run.name}
	attempt, err := s.th.Backend.CreateAttempt(ctx, persis.DAGRunCreateAttemptRequest{
		DAG:       dag,
		Timestamp: run.created,
		DAGRunID:  run.id,
		Retry:     retry,
	})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	status := ir.InitialStatus(dag)
	status.DAGRunID = run.id
	status.AttemptID = attempt.ID()
	status.Status = run.status
	status.Labels = run.labels
	status.TriggerType = run.trigger
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))
	run.attemptID = attempt.ID()
	if !retry {
		s.runs = append(s.runs, run)
		return
	}
	for i := range s.runs {
		if s.runs[i].name == run.name && s.runs[i].id == run.id {
			s.runs[i].status = run.status
			s.runs[i].attemptID = run.attemptID
		}
	}
}

// update rewrites the status of a seeded run in place.
func (s *oracleSeed) update(t *testing.T, name, id string, status ir.Status) {
	t.Helper()
	ctx := s.th.Context
	attempt, err := s.th.Backend.FindAttempt(ctx, ir.NewDAGRunRef(name, id))
	require.NoError(t, err)
	current, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	current.Status = status
	require.NoError(t, attempt.Write(ctx, *current))
	require.NoError(t, attempt.Close(ctx))
	for i := range s.runs {
		if s.runs[i].name == name && s.runs[i].id == id {
			s.runs[i].status = status
		}
	}
}

// expected lists the seeded runs matching query in list order.
func (s *oracleSeed) expected(query persis.DAGRunStatusQuery) []string {
	labelFilters := make([]ir.LabelFilter, 0, len(query.Labels))
	for _, label := range query.Labels {
		labelFilters = append(labelFilters, ir.ParseLabelFilter(label))
	}
	var matched []seededRun
	for _, run := range s.runs {
		labels := ir.NewLabels(run.labels)
		switch {
		case len(query.Statuses) > 0 && !slices.Contains(query.Statuses, run.status),
			!labels.MatchesFilters(labelFilters),
			!query.WorkspaceFilter.MatchesLabels(labels),
			query.Name != "" && !containsFold(run.name, query.Name),
			query.ExactName != "" && run.name != query.ExactName,
			!strings.Contains(run.id, query.DAGRunID),
			!query.From.IsZero() && run.created.Before(query.From.Time),
			!query.To.IsZero() && !run.created.Before(query.To.Time),
			slices.Contains(query.ExcludeTriggerTypes, run.trigger):
			continue
		}
		matched = append(matched, run)
	}
	slices.SortFunc(matched, func(left, right seededRun) int {
		return cmp.Or(
			right.created.Compare(left.created),
			cmp.Compare(left.name, right.name),
			cmp.Compare(left.id, right.id),
		)
	})
	keys := make([]string, 0, len(matched))
	for _, run := range matched {
		keys = append(keys, listKey(run.name, run.id, run.status, run.attemptID, run.labels))
	}
	return keys
}

// listKey identifies a listed run with saved fields both read paths carry,
// so a path that dropped them would not match.
func listKey(name, id string, status ir.Status, attemptID string, labels []string) string {
	return fmt.Sprintf("%s/%s/%s/%s/%s", name, id, status, attemptID, strings.Join(labels, ","))
}

// listAll follows every cursor of query and returns the runs it listed.
func listAll(t *testing.T, store *Store, query persis.DAGRunStatusQuery) []string {
	t.Helper()
	var keys []string
	for {
		page, err := store.QueryStatuses(t.Context(), query)
		require.NoError(t, err)
		if query.Limit > 0 {
			require.LessOrEqual(t, len(page.Items), query.Limit)
		}
		for _, status := range page.Items {
			keys = append(keys, listKey(status.Name, status.DAGRunID, status.Status, status.AttemptID, status.Labels))
		}
		if page.NextCursor == "" {
			return keys
		}
		query.Cursor = page.NextCursor
	}
}

func seedOracleRuns(t *testing.T) *oracleSeed {
	t.Helper()
	seed := &oracleSeed{th: setupTestRepository(t)}
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	dags := map[string][]string{
		"alpha": {"team=a", "workspace=ops"},
		"beta":  {"team=b"},
		"gamma": nil,
	}
	terminal := []ir.Status{ir.Succeeded, ir.Failed, ir.Succeeded, ir.Aborted}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		for day := range 3 {
			// At least ten finished runs a day, so each day gets an index.
			for i := range 12 {
				trigger := ir.TriggerTypeManual
				if name == "gamma" && i%3 != 0 {
					trigger = ir.TriggerTypeRestart
				}
				seed.write(t, seededRun{
					name:    name,
					id:      fmt.Sprintf("%s-%d-%02d", name, day, i),
					created: base.Add(time.Duration(day)*24*time.Hour + time.Duration(i)*7*time.Minute),
					status:  terminal[i%len(terminal)],
					labels:  dags[name],
					trigger: trigger,
				}, false)
			}
		}
	}

	// Runs created in the same second, within one DAG and across DAGs, with
	// one run ID used by two DAGs.
	same := base.Add(48*time.Hour + 2*time.Hour)
	for _, id := range []string{"same-c", "same-a", "same-b"} {
		seed.write(t, seededRun{name: "alpha", id: id, created: same, status: ir.Succeeded, labels: dags["alpha"]}, false)
	}
	seed.write(t, seededRun{name: "beta", id: "twin", created: same, status: ir.Failed, labels: dags["beta"]}, false)
	seed.write(t, seededRun{name: "gamma", id: "twin", created: same, status: ir.Succeeded}, false)

	// Runs still in progress, which an index never covers.
	seed.write(t, seededRun{name: "alpha", id: "running", created: base.Add(48*time.Hour + 3*time.Hour), status: ir.Running, labels: dags["alpha"]}, false)
	seed.write(t, seededRun{name: "gamma", id: "queued", created: base.Add(48*time.Hour + 4*time.Hour), status: ir.Queued}, false)

	// A retried run lists its latest attempt.
	retried := seededRun{name: "beta", id: "retried", created: base.Add(24*time.Hour + 5*time.Hour), status: ir.Failed, labels: dags["beta"]}
	seed.write(t, retried, false)
	retry := retried
	retry.created = retried.created.Add(time.Minute)
	retry.status = ir.Succeeded
	seed.write(t, retry, true)

	// Entries that are not readable runs.
	dayDir := filepath.Join(seed.th.TmpDir, "alpha", "dag-runs", "2026", "03", "03")
	require.NoError(t, os.MkdirAll(filepath.Join(dayDir, "dag-run_garbage"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(dayDir, "dag-run_20260303_230500Z_empty"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dayDir, "dag-run_20260303_230000Z_file"), nil, 0o600))

	// Build the day indexes, then change a run they describe.
	listAll(t, seed.th.Backend, persis.DAGRunStatusQuery{})
	seed.update(t, "beta", "beta-0-03", ir.Succeeded)
	return seed
}

// Lists read lazily, whole, or switching midway must all return the runs a
// whole-day read returns, in the same order, across every page.
func TestQueryStatusesMatchesOracle(t *testing.T) {
	seed := seedOracleRuns(t)
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	filters := map[string]persis.DAGRunStatusQuery{
		"None":            {},
		"Statuses":        {Statuses: []ir.Status{ir.Failed, ir.Running}},
		"Labels":          {Labels: []string{"team=a"}},
		"Name":            {Name: "ALP"},
		"ExactName":       {ExactName: "beta"},
		"DAGRunID":        {DAGRunID: "-1-"},
		"ExcludeTriggers": {ExcludeTriggerTypes: []ir.TriggerType{ir.TriggerTypeRestart}},
		"Workspace":       {WorkspaceFilter: &workspace.WorkspaceFilter{Enabled: true, Workspaces: []string{"ops"}}},
		"Range": {
			From: persis.NewUTC(base.Add(24*time.Hour + 30*time.Minute)),
			To:   persis.NewUTC(base.Add(48*time.Hour + 2*time.Hour + time.Second)),
		},
	}
	// Seeded days hold 12 to 18 runs; resolveShare 12, 6 and 4 fall back
	// after about one, two and three runs.
	tunings := map[string]statusListTuning{
		"Whole":        {minDayRuns: math.MaxInt, pagesPerDay: 1, resolveShare: 1},
		"Lazy":         {minDayRuns: 1, resolveShare: 1, rejectFloor: math.MaxInt, rejectRatio: 1},
		"FallbackAt1":  {minDayRuns: 1, resolveShare: 12, rejectFloor: math.MaxInt, rejectRatio: 1},
		"FallbackAt2":  {minDayRuns: 1, resolveShare: 6, rejectFloor: math.MaxInt, rejectRatio: 1},
		"FallbackAt3":  {minDayRuns: 1, resolveShare: 4, rejectFloor: math.MaxInt, rejectRatio: 1},
		"SelectiveHit": {minDayRuns: 1, resolveShare: 1},
	}

	for filterName, filter := range filters {
		want := seed.expected(filter)
		require.NotEmpty(t, want, filterName)
		for tuningName, tuning := range tunings {
			for _, limit := range []int{0, 3, 100} {
				t.Run(fmt.Sprintf("%s/%s/Limit%d", filterName, tuningName, limit), func(t *testing.T) {
					seed.th.Backend.listTuning = tuning
					query := filter
					query.Limit = limit
					require.Equal(t, want, listAll(t, seed.th.Backend, query))
				})
			}
		}
	}
}

// A lazy read lists finished runs from the day's index, so a page of them
// reads no status from disk, including runs created in the same second.
func TestQueryStatusesLazyReadUsesIndex(t *testing.T) {
	th := setupTestRepository(t)
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	dag := &ir.DAG{Name: "indexed"}
	for i := range 40 {
		attempt, err := th.Backend.CreateAttempt(th.Context, persis.DAGRunCreateAttemptRequest{
			DAG:       dag,
			Timestamp: base.Add(time.Duration(i/2) * time.Minute),
			DAGRunID:  fmt.Sprintf("run-%02d", i),
		})
		require.NoError(t, err)
		require.NoError(t, attempt.Open(th.Context))
		status := ir.InitialStatus(dag)
		status.DAGRunID = fmt.Sprintf("run-%02d", i)
		status.AttemptID = attempt.ID()
		status.Status = ir.Succeeded
		require.NoError(t, attempt.Write(th.Context, status))
		require.NoError(t, attempt.Close(th.Context))
	}
	// Reading the day whole writes its index.
	listAll(t, th.Backend, persis.DAGRunStatusQuery{})

	cache := fileutil.NewCache[*ir.DAGRunStatus]("test", 100, time.Hour)
	store := NewStore(th.TmpDir, WithHistoryFileCache(cache))
	store.listTuning = statusListTuning{minDayRuns: 1, resolveShare: 1, rejectFloor: math.MaxInt, rejectRatio: 1}
	page, err := store.QueryStatuses(th.Context, persis.DAGRunStatusQuery{Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Items, 10)
	require.Equal(t, "run-38", page.Items[0].DAGRunID)
	require.Zero(t, cache.Size())
}

type fakeDirEntry struct {
	name string
	dir  bool
}

func (e fakeDirEntry) Name() string { return e.name }
func (e fakeDirEntry) IsDir() bool  { return e.dir }
func (e fakeDirEntry) Type() fs.FileMode {
	if e.dir {
		return fs.ModeDir
	}
	return 0
}
func (e fakeDirEntry) Info() (fs.FileInfo, error) { return nil, nil }

func TestNextRunGroup(t *testing.T) {
	entries := []os.DirEntry{
		fakeDirEntry{name: ".dagrun.index"},
		fakeDirEntry{name: "dag-run_20260301_000000Z_a", dir: true},
		fakeDirEntry{name: "dag-run_20260301_000001Z_", dir: true},
		fakeDirEntry{name: "dag-run_20260301_000001Z__x", dir: true},
		fakeDirEntry{name: "dag-run_20260301_000001Z_file"},
		fakeDirEntry{name: "dag-run_20260301_000001Z_日本", dir: true},
		fakeDirEntry{name: "dag-run_bad", dir: true},
	}
	slices.SortFunc(entries, func(left, right os.DirEntry) int {
		return strings.Compare(left.Name(), right.Name())
	})

	var groups [][]string
	for end := len(entries); end > 0; {
		group, start := nextRunGroup(entries, end)
		names := make([]string, 0, len(group))
		for _, entry := range group {
			names = append(names, entry.Name())
		}
		groups = append(groups, names)
		end = start
	}

	require.Equal(t, [][]string{
		{},
		{"dag-run_20260301_000001Z_", "dag-run_20260301_000001Z__x", "dag-run_20260301_000001Z_日本"},
		{"dag-run_20260301_000000Z_a"},
		{},
	}, groups)
}
