// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package artifact_test

import (
	"context"
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

// newStoreFixtureWithRoots builds a store that can look up a child run's root.
func newStoreFixtureWithRoots(t *testing.T, resolve artifact.RootLabelsFunc) storeFixture {
	t.Helper()

	root := t.TempDir()
	return storeFixture{root: root, store: artifact.NewStore(root, artifact.WithRootLabels(resolve))}
}

// indexChild writes a child run's record, pointing at rootRef, with the
// child's own labels as given.
func (f storeFixture) indexChild(t *testing.T, dagName, dagRunID string, rootRef ir.DAGRunRef, labels []string, at time.Time) {
	t.Helper()

	dir, err := artifactpath.NewRunDir(context.Background(), f.root, "", dagName, dagRunID, at)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "out.txt"), []byte("x"), 0o600))

	metaPath, ok := artifactpath.MetaPath(f.root, dir)
	require.True(t, ok)
	require.NoError(t, artifact.WriteRecord(metaPath, artifact.Record{
		Version: artifact.RecordVersion, Name: dagName, DAGRunID: dagRunID, Status: ir.Succeeded,
		StartedAt: stringutil.FormatTime(at), Labels: labels, Dir: dir,
		RootName: rootRef.Name, RootDAGRunID: rootRef.ID,
	}))
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

	t.Run("ReturnsFilesWithRunIdentity", func(t *testing.T) {
		f := newStoreFixture(t)
		f.index(t, "alpha", "run-1", day2, nil, "reports/summary.md", "top.txt")

		page := f.query(t, persis.ArtifactQuery{})

		require.Len(t, page.Items, 2)
		assert.Equal(t, "alpha", page.Items[0].Name)
		assert.Equal(t, "run-1", page.Items[0].DAGRunID)
		assert.Equal(t, day2, page.Items[0].StartedAt.UTC())
		assert.Equal(t, "reports/summary.md", page.Items[0].Path)
		assert.Equal(t, int64(len("reports/summary.md")), page.Items[0].Size)
		assert.Equal(t, "top.txt", page.Items[1].Path)
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
	// it. The point is that one clock decides both: the run is reachable, and
	// the time it reports is the time it was ordered by.
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
		assert.Equal(t, "out.txt", page.Items[0].Path)
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
	// Paging must not repeat or drop an entry, including where a page boundary
	// falls inside a run and where it falls between days.
	t.Run("WalksEveryFileExactlyOnce", func(t *testing.T) {
		f := newStoreFixture(t)
		f.index(t, "alpha", "run-old", day1, nil, "a1.txt", "a2.txt")
		f.index(t, "beta", "run-mid", day2, nil, "b1.txt", "b2.txt", "b3.txt")
		f.index(t, "gamma", "run-new", day2b, nil, "c1.txt")

		for _, limit := range []int{1, 2, 3, 5} {
			var seen []string
			query := persis.ArtifactQuery{Limit: limit}
			for {
				page := f.query(t, query)
				for _, item := range page.Items {
					seen = append(seen, item.DAGRunID+"/"+item.Path)
				}
				if page.NextCursor == "" {
					break
				}
				query.Cursor = page.NextCursor
			}
			assert.Equal(t, []string{
				"run-new/c1.txt",
				"run-mid/b1.txt", "run-mid/b2.txt", "run-mid/b3.txt",
				"run-old/a1.txt", "run-old/a2.txt",
			}, seen, "limit %d", limit)
		}
	})

	// Paths where a nested file and a sibling file share a prefix are where
	// resuming by a plain string comparison would skip or repeat an entry.
	t.Run("WalksNestedAndSiblingPathsExactlyOnce", func(t *testing.T) {
		f := newStoreFixture(t)
		f.index(t, "alpha", "run-1", day2, nil, "a.txt", "a/b.txt", "a/y/z.txt", "a-b.txt")

		for _, limit := range []int{1, 2, 3, 5} {
			var seen []string
			query := persis.ArtifactQuery{Limit: limit}
			for {
				page := f.query(t, query)
				for _, item := range page.Items {
					seen = append(seen, item.Path)
				}
				if page.NextCursor == "" {
					break
				}
				query.Cursor = page.NextCursor
			}
			assert.ElementsMatch(t,
				[]string{"a.txt", "a/b.txt", "a/y/z.txt", "a-b.txt"}, seen, "limit %d", limit)
			assert.Len(t, seen, 4, "limit %d", limit)
		}
	})

	// Every filter has to be part of the fingerprint. One that is not lets a
	// cursor issued under a different filter resume past entries the new one
	// would have matched.
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
				f.index(t, "alpha", "run-1", day2, nil, "a1.txt", "a2.txt")

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

func TestQueryArtifactsFileNameFilter(t *testing.T) {
	newFixture := func(t *testing.T) storeFixture {
		t.Helper()
		f := newStoreFixture(t)
		f.index(t, "alpha", "run-1", day2, nil,
			"reports/summary.md", "reports/q3.csv", "data/nested/deep.csv", "logs/stdout.txt")
		return f
	}

	paths := func(page persis.ArtifactPage) []string {
		out := make([]string, 0, len(page.Items))
		for _, item := range page.Items {
			out = append(out, item.Path)
		}
		return out
	}

	t.Run("Substring", func(t *testing.T) {
		f := newFixture(t)
		assert.Equal(t, []string{"reports/summary.md"},
			paths(f.query(t, persis.ArtifactQuery{FileName: "summary"})))
	})

	t.Run("SubstringIsCaseInsensitive", func(t *testing.T) {
		f := newFixture(t)
		assert.Equal(t, []string{"reports/summary.md"},
			paths(f.query(t, persis.ArtifactQuery{FileName: "SUMMARY"})))
	})

	t.Run("SubstringMatchesDirectorySegment", func(t *testing.T) {
		f := newFixture(t)
		assert.ElementsMatch(t, []string{"reports/summary.md", "reports/q3.csv"},
			paths(f.query(t, persis.ArtifactQuery{FileName: "reports/"})))
	})

	// A glob segment stops at a separator; ** crosses it.
	t.Run("GlobDoesNotCrossSeparator", func(t *testing.T) {
		f := newFixture(t)
		assert.Equal(t, []string{"reports/q3.csv"},
			paths(f.query(t, persis.ArtifactQuery{FileName: "reports/*.csv"})))
	})

	t.Run("GlobCrossesSeparatorWithDoubleStar", func(t *testing.T) {
		f := newFixture(t)
		assert.ElementsMatch(t, []string{"reports/q3.csv", "data/nested/deep.csv"},
			paths(f.query(t, persis.ArtifactQuery{FileName: "**/*.csv"})))
	})

	// "*.csv" is a glob anchored at the path root, so it must not behave like
	// the substring ".csv" and match nested paths.
	t.Run("GlobIsNotTreatedAsSubstring", func(t *testing.T) {
		f := newFixture(t)
		assert.Empty(t, paths(f.query(t, persis.ArtifactQuery{FileName: "*.csv"})))
		assert.ElementsMatch(t, []string{"reports/q3.csv", "data/nested/deep.csv"},
			paths(f.query(t, persis.ArtifactQuery{FileName: ".csv"})))
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

	// A run contributing nothing must not end the page early.
	t.Run("RunWithoutMatchDoesNotEndPage", func(t *testing.T) {
		f := newStoreFixture(t)
		f.index(t, "older", "run-old", day1, nil, "reports/summary.md")
		f.index(t, "newer", "run-new", day2, nil, "logs/stdout.txt")

		page := f.query(t, persis.ArtifactQuery{FileName: "summary"})

		assert.Equal(t, []string{"run-old"}, runIDs(page))
	})

	t.Run("PagesFilteredResults", func(t *testing.T) {
		f := newFixture(t)

		for _, limit := range []int{1, 2, 3} {
			var seen []string
			query := persis.ArtifactQuery{FileName: ".csv", Limit: limit}
			for {
				page := f.query(t, query)
				seen = append(seen, paths(page)...)
				if page.NextCursor == "" {
					break
				}
				query.Cursor = page.NextCursor
			}
			assert.ElementsMatch(t,
				[]string{"reports/q3.csv", "data/nested/deep.csv"}, seen, "limit %d", limit)
		}
	})
}

// A child run's row carries its root's identity, so a scoped viewer must not
// see it unless they may see the root. The child's own labels cannot decide
// that: an unlabelled child of a hidden root would otherwise read as public
// and hand over the root's name and run ID.
func TestQueryArtifactsChildRootVisibility(t *testing.T) {
	secretRoot := ir.NewDAGRunRef("secret-dag", "secret-run")
	publicRoot := ir.NewDAGRunRef("public-dag", "public-run")
	rootWorkspaces := map[ir.DAGRunRef][]string{
		secretRoot: {"workspace=secret"},
		publicRoot: {"workspace=public"},
	}
	resolve := func(_ context.Context, ref ir.DAGRunRef) ([]string, bool) {
		labels, ok := rootWorkspaces[ref]
		return labels, ok
	}
	publicViewer := &workspace.WorkspaceFilter{
		Enabled: true, Workspaces: []string{"public"}, IncludeUnlabelled: true,
	}

	t.Run("UnlabelledChildOfHiddenRootIsHidden", func(t *testing.T) {
		f := newStoreFixtureWithRoots(t, resolve)
		f.indexChild(t, "child", "child-run", secretRoot, nil, day2)

		page := f.query(t, persis.ArtifactQuery{WorkspaceFilter: publicViewer})

		assert.Empty(t, page.Items)
	})

	// A child declaring a workspace the viewer may see still leaks the root's
	// identity if the root is hidden, so the root decides regardless.
	t.Run("LabelledChildOfHiddenRootIsHidden", func(t *testing.T) {
		f := newStoreFixtureWithRoots(t, resolve)
		f.indexChild(t, "child", "child-run", secretRoot, []string{"workspace=public"}, day2)

		page := f.query(t, persis.ArtifactQuery{WorkspaceFilter: publicViewer})

		assert.Empty(t, page.Items)
	})

	t.Run("ChildOfVisibleRootIsShownWithItsRoot", func(t *testing.T) {
		f := newStoreFixtureWithRoots(t, resolve)
		f.indexChild(t, "child", "child-run", publicRoot, nil, day2)

		page := f.query(t, persis.ArtifactQuery{WorkspaceFilter: publicViewer})

		require.Len(t, page.Items, 1)
		assert.Equal(t, "public-dag", page.Items[0].RootName)
		assert.Equal(t, "public-run", page.Items[0].RootDAGRunID)
	})

	// A root that no longer exists gives nothing to decide with; hidden is the
	// only answer that cannot be wrong.
	t.Run("ChildOfUnresolvableRootIsHidden", func(t *testing.T) {
		f := newStoreFixtureWithRoots(t, resolve)
		f.indexChild(t, "child", "child-run", ir.NewDAGRunRef("gone-dag", "gone-run"), nil, day2)

		page := f.query(t, persis.ArtifactQuery{WorkspaceFilter: publicViewer})

		assert.Empty(t, page.Items)
	})

	t.Run("ChildIsHiddenFromScopedViewerWithoutResolver", func(t *testing.T) {
		f := newStoreFixture(t)
		f.indexChild(t, "child", "child-run", publicRoot, nil, day2)

		page := f.query(t, persis.ArtifactQuery{WorkspaceFilter: publicViewer})

		assert.Empty(t, page.Items)
	})

	t.Run("UnscopedViewerSeesChildren", func(t *testing.T) {
		f := newStoreFixture(t)
		f.indexChild(t, "child", "child-run", secretRoot, nil, day2)

		page := f.query(t, persis.ArtifactQuery{})

		require.Len(t, page.Items, 1)
		assert.Equal(t, "secret-dag", page.Items[0].RootName)
	})

	t.Run("RootRowsStillAnswerForThemselves", func(t *testing.T) {
		f := newStoreFixtureWithRoots(t, resolve)
		f.index(t, "secret-dag", "secret-run", day2, []string{"workspace=secret"}, "a.txt")
		f.index(t, "public-dag", "public-run", day2b, []string{"workspace=public"}, "b.txt")

		page := f.query(t, persis.ArtifactQuery{WorkspaceFilter: publicViewer})

		assert.Equal(t, []string{"public-run"}, runIDs(page))
	})

	t.Run("RootResolvedOncePerQuery", func(t *testing.T) {
		calls := 0
		counting := func(ctx context.Context, ref ir.DAGRunRef) ([]string, bool) {
			calls++
			return resolve(ctx, ref)
		}
		f := newStoreFixtureWithRoots(t, counting)
		f.indexChild(t, "child", "child-1", publicRoot, nil, day2)
		f.indexChild(t, "child", "child-2", publicRoot, nil, day2b)

		page := f.query(t, persis.ArtifactQuery{WorkspaceFilter: publicViewer})

		assert.Len(t, page.Items, 2)
		assert.Equal(t, 1, calls)
	})
}
