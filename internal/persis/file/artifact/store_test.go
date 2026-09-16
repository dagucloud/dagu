// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package artifact_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/artifactpath"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file/artifact"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type storeFixture struct {
	root  string
	store *artifact.Store
}

func newStoreFixture(t *testing.T) storeFixture {
	t.Helper()

	root := t.TempDir()
	return storeFixture{root: root, store: artifact.NewStore(root)}
}

// index writes one run's artifacts plus its index record, the way a finished
// run leaves them behind.
func (f storeFixture) index(t *testing.T, dagName, dagRunID string, at time.Time, labels []string, files ...string) string {
	t.Helper()
	return f.indexStartedAt(t, dagName, dagRunID, at, at, labels, files...)
}

// indexStartedAt writes a run whose directory and start time differ, as a run
// admitted before midnight and started after it does.
func (f storeFixture) indexStartedAt(
	t *testing.T, dagName, dagRunID string, at, startedAt time.Time, labels []string, files ...string,
) string {
	t.Helper()

	dir, err := artifactpath.NewRunDir(context.Background(), f.root, "", dagName, dagRunID, at)
	require.NoError(t, err)
	for _, name := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(name), 0o600))
	}

	metaPath, ok := artifactpath.MetaPath(f.root, dir)
	require.True(t, ok)
	require.NoError(t, artifact.WriteRecord(metaPath, artifact.Record{
		Version:   artifact.RecordVersion,
		Name:      dagName,
		DAGRunID:  dagRunID,
		Status:    ir.Succeeded,
		StartedAt: stringutil.FormatTime(startedAt),
		Labels:    labels,
		Dir:       dir,
	}))
	return dir
}

func (f storeFixture) query(t *testing.T, q persis.ArtifactQuery) persis.ArtifactPage {
	t.Helper()

	if q.Limit == 0 {
		q.Limit = 100
	}
	page, err := f.store.QueryArtifacts(context.Background(), q)
	require.NoError(t, err)
	return page
}

func runIDs(page persis.ArtifactPage) []string {
	ids := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		ids = append(ids, item.DAGRunID)
	}
	return ids
}

func filesOf(run persis.ArtifactRun) []string {
	paths := make([]string, 0, len(run.Files))
	for _, f := range run.Files {
		paths = append(paths, f.Path)
	}
	return paths
}

func manyFiles(n int) []string {
	names := make([]string, 0, n)
	for i := range n {
		names = append(names, fmt.Sprintf("f%04d.txt", i))
	}
	return names
}

var (
	day1  = time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	day2  = time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC)
	day2b = time.Date(2026, 9, 15, 18, 0, 0, 0, time.UTC)
)

func TestQueryArtifacts(t *testing.T) {
	t.Run("NewestRunFirstAcrossDays", func(t *testing.T) {
		f := newStoreFixture(t)
		f.index(t, "alpha", "run-old", day1, nil, "a.txt")
		f.index(t, "beta", "run-mid", day2, nil, "b.txt")
		f.index(t, "gamma", "run-new", day2b, nil, "c.txt")

		page := f.query(t, persis.ArtifactQuery{})

		assert.Equal(t, []string{"run-new", "run-mid", "run-old"}, runIDs(page))
	})

	t.Run("OneItemPerRunWithItsFiles", func(t *testing.T) {
		f := newStoreFixture(t)
		f.index(t, "alpha", "run-1", day2, nil, "reports/summary.md", "top.txt")

		page := f.query(t, persis.ArtifactQuery{})

		require.Len(t, page.Items, 1)
		run := page.Items[0]
		assert.Equal(t, "alpha", run.Name)
		assert.Equal(t, "run-1", run.DAGRunID)
		assert.Equal(t, day2, run.StartedAt.UTC())
		assert.Equal(t, []string{"reports/summary.md", "top.txt"}, filesOf(run))
		assert.Equal(t, int64(len("reports/summary.md")), run.Files[0].Size)
		assert.False(t, run.FilesTruncated)
	})

	t.Run("FiltersByDAGName", func(t *testing.T) {
		f := newStoreFixture(t)
		f.index(t, "daily-report", "run-1", day2, nil, "a.txt")
		f.index(t, "nightly-sync", "run-2", day2b, nil, "b.txt")

		page := f.query(t, persis.ArtifactQuery{Name: "REPORT"})

		assert.Equal(t, []string{"run-1"}, runIDs(page))
	})

	// Pruning compares a truncated bound against a year or month prefix, so a
	// range spanning both is where an off-by-one would show up.
	t.Run("BoundsAcrossMonthsAndYears", func(t *testing.T) {
		f := newStoreFixture(t)
		f.index(t, "alpha", "run-2025", time.Date(2025, 12, 31, 12, 0, 0, 0, time.UTC), nil, "a.txt")
		f.index(t, "alpha", "run-jan", time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC), nil, "b.txt")
		f.index(t, "alpha", "run-sep", day2, nil, "c.txt")
		f.index(t, "alpha", "run-2027", time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC), nil, "d.txt")

		page := f.query(t, persis.ArtifactQuery{
			From: persis.NewUTC(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
			To:   persis.NewUTC(time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)),
		})

		assert.Equal(t, []string{"run-sep", "run-jan"}, runIDs(page))
	})

	t.Run("BoundsByDateRange", func(t *testing.T) {
		f := newStoreFixture(t)
		f.index(t, "alpha", "run-old", day1, nil, "a.txt")
		f.index(t, "alpha", "run-new", day2, nil, "b.txt")

		page := f.query(t, persis.ArtifactQuery{From: persis.NewUTC(day2.Add(-time.Hour))})

		assert.Equal(t, []string{"run-new"}, runIDs(page))
	})

	// Filtering and ordering read the directory's timestamp, so a run admitted
	// before midnight stays on its admission day even though it started after
	// it. One clock decides both: the run is reachable, and the time it reports
	// is the time it was ordered by.
	t.Run("PlacesACrossMidnightRunOnItsAdmissionDay", func(t *testing.T) {
		f := newStoreFixture(t)
		admitted := time.Date(2026, 9, 14, 23, 59, 0, 0, time.UTC)
		started := time.Date(2026, 9, 15, 0, 30, 0, 0, time.UTC)
		f.indexStartedAt(t, "alpha", "run-late", admitted, started, nil, "a.txt")

		onAdmissionDay := f.query(t, persis.ArtifactQuery{
			From: persis.NewUTC(admitted.Add(-time.Hour)),
			To:   persis.NewUTC(admitted.Add(time.Minute)),
		})
		require.Len(t, onAdmissionDay.Items, 1)
		assert.Equal(t, admitted, onAdmissionDay.Items[0].CreatedAt.UTC())
		assert.Equal(t, started, onAdmissionDay.Items[0].StartedAt.UTC())

		onStartDay := f.query(t, persis.ArtifactQuery{From: persis.NewUTC(started)})
		assert.Empty(t, onStartDay.Items)
	})

	// Bounds are compared to the second, not rounded to the day.
	t.Run("BoundsWithinASingleDay", func(t *testing.T) {
		f := newStoreFixture(t)
		morning := time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC)
		evening := time.Date(2026, 9, 15, 21, 0, 0, 0, time.UTC)
		f.index(t, "alpha", "run-morning", morning, nil, "a.txt")
		f.index(t, "alpha", "run-evening", evening, nil, "b.txt")

		page := f.query(t, persis.ArtifactQuery{From: persis.NewUTC(evening.Add(-time.Hour))})

		assert.Equal(t, []string{"run-evening"}, runIDs(page))
	})

	// A DAG relocated by artifacts.dir is indexed in the global tree and must
	// still be listed, with its files read from where they actually live.
	t.Run("ListsRelocatedArtifacts", func(t *testing.T) {
		f := newStoreFixture(t)
		elsewhere := t.TempDir()

		dir, err := artifactpath.NewRunDir(context.Background(), elsewhere, "", "scoped", "run-1", day2)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "out.txt"), []byte("x"), 0o600))
		metaPath, ok := artifactpath.MetaPath(f.root, dir)
		require.True(t, ok)
		require.NoError(t, artifact.WriteRecord(metaPath, artifact.Record{
			Version: artifact.RecordVersion, Name: "scoped", DAGRunID: "run-1",
			StartedAt: stringutil.FormatTime(day2), Dir: dir,
		}))

		page := f.query(t, persis.ArtifactQuery{})

		require.Len(t, page.Items, 1)
		assert.Equal(t, []string{"out.txt"}, filesOf(page.Items[0]))
	})

	t.Run("SkipsUnreadableRecord", func(t *testing.T) {
		f := newStoreFixture(t)
		f.index(t, "alpha", "run-1", day2, nil, "a.txt")
		dir := f.index(t, "beta", "run-2", day2b, nil, "b.txt")
		metaPath, ok := artifactpath.MetaPath(f.root, dir)
		require.True(t, ok)
		require.NoError(t, os.WriteFile(metaPath, []byte("{not json"), 0o600))

		page := f.query(t, persis.ArtifactQuery{})

		assert.Equal(t, []string{"run-1"}, runIDs(page))
	})

	t.Run("IgnoresRunDirsWithoutRecord", func(t *testing.T) {
		f := newStoreFixture(t)
		dir, err := artifactpath.NewRunDir(context.Background(), f.root, "", "alpha", "run-1", day2)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o600))

		page := f.query(t, persis.ArtifactQuery{})

		assert.Empty(t, page.Items)
	})
}

// One listing returns at most a hundred of a run's files. A run over that is
// reported as truncated rather than either read out in full or dropped.
func TestQueryArtifactsFileCap(t *testing.T) {
	t.Run("RunAtTheCapIsWhole", func(t *testing.T) {
		f := newStoreFixture(t)
		f.index(t, "alpha", "run-1", day2, nil, manyFiles(100)...)

		page := f.query(t, persis.ArtifactQuery{})

		require.Len(t, page.Items, 1)
		assert.Len(t, page.Items[0].Files, 100)
		assert.False(t, page.Items[0].FilesTruncated)
	})

	t.Run("RunOverTheCapIsTruncated", func(t *testing.T) {
		f := newStoreFixture(t)
		f.index(t, "alpha", "run-1", day2, nil, manyFiles(150)...)

		page := f.query(t, persis.ArtifactQuery{})

		require.Len(t, page.Items, 1)
		assert.Len(t, page.Items[0].Files, 100)
		assert.True(t, page.Items[0].FilesTruncated)
		assert.Equal(t, "f0000.txt", page.Items[0].Files[0].Path)
		assert.Equal(t, "f0099.txt", page.Items[0].Files[99].Path)
	})

	// The cap counts matches, not files scanned.
	t.Run("CapAppliesToMatchingFiles", func(t *testing.T) {
		f := newStoreFixture(t)
		files := append(manyFiles(150), "other.log")
		f.index(t, "alpha", "run-1", day2, nil, files...)

		page := f.query(t, persis.ArtifactQuery{FileName: ".txt"})

		require.Len(t, page.Items, 1)
		assert.Len(t, page.Items[0].Files, 100)
		assert.True(t, page.Items[0].FilesTruncated)
	})

	t.Run("SmallRunIsNotTruncated", func(t *testing.T) {
		f := newStoreFixture(t)
		f.index(t, "alpha", "run-1", day2, nil, "a.txt", "b.txt", "c.txt")

		page := f.query(t, persis.ArtifactQuery{})

		require.Len(t, page.Items, 1)
		assert.Len(t, page.Items[0].Files, 3)
		assert.False(t, page.Items[0].FilesTruncated)
	})
}

func TestQueryArtifactsWorkspaceScoping(t *testing.T) {
	f := newStoreFixture(t)
	f.index(t, "team-a", "run-a", day2, []string{"workspace=alpha"}, "a.txt")
	f.index(t, "team-b", "run-b", day2b, []string{"workspace=beta"}, "b.txt")
	f.index(t, "shared", "run-c", day1, nil, "c.txt")

	t.Run("OnlyMatchingWorkspace", func(t *testing.T) {
		page := f.query(t, persis.ArtifactQuery{
			WorkspaceFilter: &workspace.WorkspaceFilter{Enabled: true, Workspaces: []string{"alpha"}},
		})
		assert.Equal(t, []string{"run-a"}, runIDs(page))
	})

	t.Run("IncludingUnlabelled", func(t *testing.T) {
		page := f.query(t, persis.ArtifactQuery{
			WorkspaceFilter: &workspace.WorkspaceFilter{
				Enabled: true, Workspaces: []string{"alpha"}, IncludeUnlabelled: true,
			},
		})
		assert.Equal(t, []string{"run-a", "run-c"}, runIDs(page))
	})

	t.Run("DisabledFilterSeesAll", func(t *testing.T) {
		page := f.query(t, persis.ArtifactQuery{})
		assert.Len(t, page.Items, 3)
	})
}

func TestQueryArtifactsPagination(t *testing.T) {
	// A run is never split across pages, so paging must return every run
	// exactly once, whole, at any page size.
	t.Run("WalksEveryRunExactlyOnce", func(t *testing.T) {
		f := newStoreFixture(t)
		f.index(t, "alpha", "run-old", day1, nil, "a1.txt", "a2.txt")
		f.index(t, "beta", "run-mid", day2, nil, "b1.txt", "b2.txt", "b3.txt")
		f.index(t, "gamma", "run-new", day2b, nil, "c1.txt")

		for _, limit := range []int{1, 2, 3, 5} {
			var seen []string
			query := persis.ArtifactQuery{Limit: limit}
			for {
				page := f.query(t, query)
				for _, run := range page.Items {
					seen = append(seen, fmt.Sprintf("%s:%d", run.DAGRunID, len(run.Files)))
				}
				if page.NextCursor == "" {
					break
				}
				query.Cursor = page.NextCursor
			}
			assert.Equal(t, []string{"run-new:1", "run-mid:3", "run-old:2"}, seen, "limit %d", limit)
		}
	})

	// Every filter has to be part of the fingerprint. One that is not lets a
	// cursor issued under a different filter resume past runs the new one
	// would have returned.
	t.Run("RejectsCursorFromDifferentFilters", func(t *testing.T) {
		changed := []struct {
			name  string
			query persis.ArtifactQuery
		}{
			{"Name", persis.ArtifactQuery{Limit: 1, Name: "alpha"}},
			{"FileName", persis.ArtifactQuery{Limit: 1, FileName: "a1"}},
			{"From", persis.ArtifactQuery{Limit: 1, From: persis.NewUTC(day1)}},
			{"To", persis.ArtifactQuery{Limit: 1, To: persis.NewUTC(day2b)}},
			{"Workspace", persis.ArtifactQuery{Limit: 1, WorkspaceFilter: &workspace.WorkspaceFilter{
				Enabled: true, Workspaces: []string{"alpha"},
			}}},
		}
		for _, tt := range changed {
			t.Run(tt.name, func(t *testing.T) {
				f := newStoreFixture(t)
				f.index(t, "alpha", "run-1", day2, nil, "a1.txt")
				f.index(t, "alpha", "run-2", day2b, nil, "a2.txt")

				page := f.query(t, persis.ArtifactQuery{Limit: 1})
				require.NotEmpty(t, page.NextCursor)

				tt.query.Cursor = page.NextCursor
				_, err := f.store.QueryArtifacts(context.Background(), tt.query)
				assert.ErrorIs(t, err, persis.ErrInvalidArtifactCursor)
			})
		}
	})

	t.Run("RejectsMalformedCursor", func(t *testing.T) {
		f := newStoreFixture(t)

		_, err := f.store.QueryArtifacts(context.Background(), persis.ArtifactQuery{Cursor: "!!!"})
		assert.ErrorIs(t, err, persis.ErrInvalidArtifactCursor)
	})
}

// A file-name filter selects which of a run's files come back, and omits a run
// that has none.
func TestQueryArtifactsFileNameFilter(t *testing.T) {
	newFixture := func(t *testing.T) storeFixture {
		t.Helper()
		f := newStoreFixture(t)
		f.index(t, "alpha", "run-1", day2, nil,
			"reports/summary.md", "reports/q3.csv", "data/nested/deep.csv", "logs/stdout.txt")
		return f
	}

	// filesOfOnly returns the single run's files, failing if the page holds
	// anything other than one run.
	filesOfOnly := func(t *testing.T, page persis.ArtifactPage) []string {
		t.Helper()
		require.Len(t, page.Items, 1)
		return filesOf(page.Items[0])
	}

	t.Run("Substring", func(t *testing.T) {
		f := newFixture(t)
		assert.Equal(t, []string{"reports/summary.md"},
			filesOfOnly(t, f.query(t, persis.ArtifactQuery{FileName: "summary"})))
	})

	t.Run("SubstringIsCaseInsensitive", func(t *testing.T) {
		f := newFixture(t)
		assert.Equal(t, []string{"reports/summary.md"},
			filesOfOnly(t, f.query(t, persis.ArtifactQuery{FileName: "SUMMARY"})))
	})

	t.Run("SubstringMatchesDirectorySegment", func(t *testing.T) {
		f := newFixture(t)
		assert.ElementsMatch(t, []string{"reports/summary.md", "reports/q3.csv"},
			filesOfOnly(t, f.query(t, persis.ArtifactQuery{FileName: "reports/"})))
	})

	// A glob segment stops at a separator; ** crosses it.
	t.Run("GlobDoesNotCrossSeparator", func(t *testing.T) {
		f := newFixture(t)
		assert.Equal(t, []string{"reports/q3.csv"},
			filesOfOnly(t, f.query(t, persis.ArtifactQuery{FileName: "reports/*.csv"})))
	})

	t.Run("GlobCrossesSeparatorWithDoubleStar", func(t *testing.T) {
		f := newFixture(t)
		assert.ElementsMatch(t, []string{"reports/q3.csv", "data/nested/deep.csv"},
			filesOfOnly(t, f.query(t, persis.ArtifactQuery{FileName: "**/*.csv"})))
	})

	// "*.csv" is a glob anchored at the path root, so it must not behave like
	// the substring ".csv" and match nested paths.
	t.Run("GlobIsNotTreatedAsSubstring", func(t *testing.T) {
		f := newFixture(t)
		assert.Empty(t, f.query(t, persis.ArtifactQuery{FileName: "*.csv"}).Items)
		assert.ElementsMatch(t, []string{"reports/q3.csv", "data/nested/deep.csv"},
			filesOfOnly(t, f.query(t, persis.ArtifactQuery{FileName: ".csv"})))
	})

	t.Run("ComposesWithNameAndDateRange", func(t *testing.T) {
		f := newFixture(t)
		f.index(t, "beta", "run-2", day2b, nil, "reports/summary.md")

		page := f.query(t, persis.ArtifactQuery{
			FileName: "summary",
			Name:     "alpha",
			From:     persis.NewUTC(day2.Add(-time.Hour)),
		})

		assert.Equal(t, []string{"run-1"}, runIDs(page))
	})

	t.Run("RunWithoutMatchIsOmitted", func(t *testing.T) {
		f := newStoreFixture(t)
		f.index(t, "older", "run-old", day1, nil, "reports/summary.md")
		f.index(t, "newer", "run-new", day2, nil, "logs/stdout.txt")

		page := f.query(t, persis.ArtifactQuery{FileName: "summary"})

		assert.Equal(t, []string{"run-old"}, runIDs(page))
	})

	t.Run("PagesFilteredRuns", func(t *testing.T) {
		f := newStoreFixture(t)
		f.index(t, "alpha", "run-1", day2, nil, "a.csv", "a.log")
		f.index(t, "beta", "run-2", day2b, nil, "b.csv", "b.log")
		f.index(t, "gamma", "run-3", day1, nil, "c.log")

		var seen []string
		query := persis.ArtifactQuery{FileName: ".csv", Limit: 1}
		for {
			page := f.query(t, query)
			for _, run := range page.Items {
				seen = append(seen, run.DAGRunID+":"+filesOf(run)[0])
				assert.Len(t, run.Files, 1, "only the matching file is listed")
			}
			if page.NextCursor == "" {
				break
			}
			query.Cursor = page.NextCursor
		}
		assert.Equal(t, []string{"run-2:b.csv", "run-1:a.csv"}, seen)
	})
}
