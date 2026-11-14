// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const markerTestDAG = "steps:\n  - name: step1\n    command: echo hello\n"

func markerTestStore(t *testing.T) (store *Store, dagsDir, markerPath string) {
	t.Helper()

	root := t.TempDir()
	dagsDir = filepath.Join(root, "dags")
	indexDir := filepath.Join(root, "data", "cache", "dag-index")
	require.NoError(t, os.MkdirAll(dagsDir, 0750))
	store = NewStore(dagsDir, WithSkipExamples(true), WithIndexDir(indexDir))
	return store, dagsDir, ChangeMarkerPath(indexDir, dagsDir)
}

// readMarkerFile returns the content of a marker file, or "" when it is absent.
func readMarkerFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// TestStoreBumpsChangeMarker verifies every DAG definition write changes the
// marker, and that the marker stays out of the DAG directory.
func TestStoreBumpsChangeMarker(t *testing.T) {
	ctx := context.Background()
	store, dagsDir, markerPath := markerTestStore(t)
	require.Empty(t, readMarkerFile(markerPath))

	last := ""
	expectBump := func(op string) {
		t.Helper()
		current := readMarkerFile(markerPath)
		require.NotEmpty(t, current, "%s did not write the marker", op)
		require.NotEqual(t, last, current, "%s did not change the marker", op)
		last = current
	}

	require.NoError(t, store.Create(ctx, "a", []byte(markerTestDAG)))
	expectBump("create")
	require.NoError(t, store.Update(ctx, "a", []byte(markerTestDAG+"# edited\n")))
	expectBump("update")
	require.NoError(t, store.Rename(ctx, "a", "b"))
	expectBump("rename")
	require.NoError(t, store.Delete(ctx, "b"))
	expectBump("delete")

	entries, err := os.ReadDir(dagsDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "the marker must not be written into the DAG directory")
}

// TestStoreExampleDAGsBumpChangeMarker verifies creating the example DAGs
// signals a change like any other DAG write.
func TestStoreExampleDAGsBumpChangeMarker(t *testing.T) {
	root := t.TempDir()
	dagsDir := filepath.Join(root, "dags")
	indexDir := filepath.Join(root, "data", "cache", "dag-index")
	markerPath := ChangeMarkerPath(indexDir, dagsDir)

	store := NewStore(dagsDir, WithIndexDir(indexDir))
	require.NoError(t, store.ensureDirExist())
	assert.FileExists(t, filepath.Join(dagsDir, ".examples-created"))
	assert.NotEmpty(t, readMarkerFile(markerPath))
}

// TestStoreFailedWriteKeepsChangeMarker verifies a rejected write does not signal a change.
func TestStoreFailedWriteKeepsChangeMarker(t *testing.T) {
	ctx := context.Background()
	store, _, markerPath := markerTestStore(t)

	require.NoError(t, store.Create(ctx, "a", []byte(markerTestDAG)))
	before := readMarkerFile(markerPath)
	require.ErrorIs(t, store.Create(ctx, "a", []byte(markerTestDAG)), persis.ErrDAGAlreadyExists)
	assert.Equal(t, before, readMarkerFile(markerPath))
}

// TestStoreWithoutMarkerLocation verifies stores that have no index directory
// work without a marker and write nothing besides the DAG file.
func TestStoreWithoutMarkerLocation(t *testing.T) {
	ctx := context.Background()
	dagsDir := t.TempDir()
	store := NewStore(dagsDir, WithSkipExamples(true))

	require.NoError(t, store.Create(ctx, "a", []byte(markerTestDAG)))
	require.NoError(t, store.Update(ctx, "a", []byte(markerTestDAG+"# edited\n")))
	require.NoError(t, store.Delete(ctx, "a"))
	assert.Empty(t, ChangeMarkerPath("", dagsDir))
}

// TestStoreIgnoresUnwritableMarker verifies a marker that cannot be written
// never fails the DAG write.
func TestStoreIgnoresUnwritableMarker(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dagsDir := filepath.Join(root, "dags")
	require.NoError(t, os.MkdirAll(dagsDir, 0750))
	// A regular file where the index directory should be makes every marker write fail.
	indexDir := filepath.Join(root, "not-a-directory")
	require.NoError(t, os.WriteFile(indexDir, []byte("x"), 0600))
	store := NewStore(dagsDir, WithSkipExamples(true), WithIndexDir(indexDir))

	require.NoError(t, store.Create(ctx, "a", []byte(markerTestDAG)))
	require.NoError(t, store.Update(ctx, "a", []byte(markerTestDAG+"# edited\n")))
	require.NoError(t, store.Rename(ctx, "a", "b"))
	require.NoError(t, store.Delete(ctx, "b"))
	assert.Empty(t, readMarkerFile(ChangeMarkerPath(indexDir, dagsDir)))
}

// readerMarkerPath returns the marker file of a reader whose host is "reader-host",
// next to the marker files of the store's host.
func readerMarkerPath(markerPath string) string {
	return hostMarkerPath(markerPath, "reader-host")
}

// hostMarkerPath returns the marker file that host writes next to markerPath.
func hostMarkerPath(markerPath, host string) string {
	prefix, _, _ := strings.Cut(filepath.Base(markerPath), ".")
	return filepath.Join(filepath.Dir(markerPath), prefix+"."+hostID(host)+changeMarkerSuffix)
}

var bumpCount atomic.Int64

// bumpFrom writes the marker of host next to markerPath, as a Dagu process on
// that host does, and returns the file path. It gives each write a distinct
// modification time, so a test never depends on timestamp resolution or on the
// file system reusing an inode number.
func bumpFrom(t *testing.T, markerPath, host string) string {
	t.Helper()
	path := hostMarkerPath(markerPath, host)
	NewChangeMarker(path).Bump(context.Background())
	mtime := time.Now().Add(-time.Hour + time.Duration(bumpCount.Add(1))*time.Second)
	require.NoError(t, os.Chtimes(path, mtime, mtime))
	return path
}

func startMarkerReader(t *testing.T, dagsDir, markerPath string, markerOpts ...EntryReaderOption) chan persis.DAGChangeEvent {
	t.Helper()
	return startMarkerReaderMode(t, dagsDir, markerPath, false, markerOpts...)
}

func startMarkerReaderMode(t *testing.T, dagsDir, markerPath string, recursive bool, markerOpts ...EntryReaderOption) chan persis.DAGChangeEvent {
	t.Helper()
	repo := persis.NewDAGRepository(NewStore(dagsDir, WithSkipExamples(true), WithRecursiveDiscovery(recursive)), persis.DAGRepositoryOptions{})
	opts := append([]EntryReaderOption{
		withWatcherFactory(newSilentWatcher),
		withResyncInterval(0),
		WithChangeMarker(readerMarkerPath(markerPath), 25*time.Millisecond),
		withMarkerRefreshGap(0),
	}, markerOpts...)
	er := NewFileEntryReader(dagsDir, repo, recursive, "", "", opts...)
	events := make(chan persis.DAGChangeEvent, 64)
	er.events = events
	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, er.Init(ctx))
	go er.Start(ctx)
	t.Cleanup(func() {
		cancel()
		er.Stop()
	})
	return events
}

// slowFirstLoadStore makes the first metadata load of one DAG take delay.
type slowFirstLoadStore struct {
	*Store
	dagName string
	delay   time.Duration
	once    sync.Once
}

func (s *slowFirstLoadStore) GetMetadata(ctx context.Context, id string) (*ir.DAG, error) {
	if definitionIDForFile(id) == s.dagName {
		s.once.Do(func() { time.Sleep(s.delay) })
	}
	return s.Store.GetMetadata(ctx, id)
}

// withSlowFirstLoad makes the reader load DAGs through a slowFirstLoadStore.
func withSlowFirstLoad(dagsDir, dagName string, delay time.Duration) EntryReaderOption {
	return func(er *entryReaderImpl) {
		store := &slowFirstLoadStore{Store: NewStore(dagsDir, WithSkipExamples(true)), dagName: dagName, delay: delay}
		er.dagRepository = persis.NewDAGRepository(store, persis.DAGRepositoryOptions{})
		er.dagSource = newDAGFileSource(dagsDir, er.dagRepository)
	}
}

// TestChangeMarkerPathPerHost verifies each host gets its own marker file next
// to the others, and that an unknown host has a fixed one.
func TestChangeMarkerPathPerHost(t *testing.T) {
	index := filepath.Join(t.TempDir(), "index")
	dags := filepath.Join(t.TempDir(), "dags")

	a := changeMarkerPathForHost(index, dags, "host-a")
	b := changeMarkerPathForHost(index, dags, "host-b")
	unknown := changeMarkerPathForHost(index, dags, "")
	assert.NotEqual(t, a, b)
	assert.Equal(t, filepath.Dir(a), filepath.Dir(b))
	assert.True(t, strings.HasSuffix(unknown, ".unknown.changed"))
	assert.NotEqual(t, a, changeMarkerPathForHost(index, filepath.Join(dags, "other"), "host-a"))
	assert.Equal(t, a, changeMarkerPathForHost(index, dags, "host-a"))
	assert.Equal(t, changeMarkerPathForHost(index, dags, localHostName()), ChangeMarkerPath(index, dags))
	assert.Empty(t, changeMarkerPathForHost("", dags, "host-a"))
}

// TestEntryReaderFollowsChangeMarker covers DAG changes that another host makes
// through the store on a shared volume: the watcher is silent and the full
// resync is off, so only the marker can report them.
func TestEntryReaderFollowsChangeMarker(t *testing.T) {
	ctx := context.Background()
	store, dagsDir, markerPath := markerTestStore(t)
	require.NoError(t, store.Create(ctx, "existing", []byte("name: existing\n"+markerTestDAG)))
	events := startMarkerReader(t, dagsDir, markerPath)

	// A write that does not touch the marker stays unseen.
	writeDAGFile(t, dagsDir, "unannounced.yaml", "unannounced")
	expectNoEvent(t, events, 300*time.Millisecond)

	// Any store write wakes the reader, which then also sees the earlier file.
	require.NoError(t, store.Create(ctx, "remote", []byte("name: remote\n"+markerTestDAG)))
	expectEvent(t, events, persis.DAGChangeAdded, "remote")
	expectEvent(t, events, persis.DAGChangeAdded, "unannounced")

	require.NoError(t, store.Update(ctx, "remote", []byte("name: remote-edited\n"+markerTestDAG)))
	expectEvent(t, events, persis.DAGChangeAdded, "remote-edited")

	require.NoError(t, store.Delete(ctx, "remote"))
	expectEvent(t, events, persis.DAGChangeDeleted, "remote-edited")
	expectNoEvent(t, events, 300*time.Millisecond)
}

// TestEntryReaderSpacesMarkerRefreshes verifies refreshes triggered by change
// markers keep their minimum gap from the end of the previous refresh: a second
// remote edit right after the first is still applied, but only once the gap
// has passed. The first refresh outlasts the gap, so a gap counted from its
// start would already be over when it ends.
func TestEntryReaderSpacesMarkerRefreshes(t *testing.T) {
	ctx := context.Background()
	store, dagsDir, markerPath := markerTestStore(t)
	const gap = 600 * time.Millisecond
	events := startMarkerReader(t, dagsDir, markerPath,
		withMarkerRefreshGap(gap),
		withSlowFirstLoad(dagsDir, "first", 2*gap))

	require.NoError(t, store.Create(ctx, "first", []byte("name: first\n"+markerTestDAG)))
	expectEvent(t, events, persis.DAGChangeAdded, "first")
	firstAt := time.Now()

	require.NoError(t, store.Create(ctx, "second", []byte("name: second\n"+markerTestDAG)))
	expectEvent(t, events, persis.DAGChangeAdded, "second")
	assert.GreaterOrEqual(t, time.Since(firstAt), gap/2)
}

// TestEntryReaderStartsWithoutMarker verifies a missing marker is not an error
// and that its first appearance counts as a change.
func TestEntryReaderStartsWithoutMarker(t *testing.T) {
	ctx := context.Background()
	store, dagsDir, markerPath := markerTestStore(t)
	require.NoFileExists(t, markerPath)
	events := startMarkerReader(t, dagsDir, markerPath)

	expectNoEvent(t, events, 150*time.Millisecond)
	require.NoError(t, store.Create(ctx, "first", []byte("name: first\n"+markerTestDAG)))
	expectEvent(t, events, persis.DAGChangeAdded, "first")
}

// TestEntryReaderFollowsChangeMarkerRecursive covers the same remote changes
// for a reader that discovers DAGs in subdirectories.
func TestEntryReaderFollowsChangeMarkerRecursive(t *testing.T) {
	ctx := context.Background()
	store, dagsDir, markerPath := markerTestStore(t)
	require.NoError(t, os.MkdirAll(filepath.Join(dagsDir, "team"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(dagsDir, "team", "nested.yaml"), []byte("name: nested\n"+markerTestDAG), 0600))
	events := startMarkerReaderMode(t, dagsDir, markerPath, true)

	writeDAGFile(t, dagsDir, "unannounced.yaml", "unannounced")
	expectNoEvent(t, events, 300*time.Millisecond)

	require.NoError(t, store.Create(ctx, "remote", []byte("name: remote\n"+markerTestDAG)))
	expectEvent(t, events, persis.DAGChangeAdded, "remote")
	expectEvent(t, events, persis.DAGChangeAdded, "unannounced")

	require.NoError(t, store.Delete(ctx, "remote"))
	expectEvent(t, events, persis.DAGChangeDeleted, "remote")
}

// TestEntryReaderIgnoresOwnHostMarkerBumps verifies the running reader does not
// refresh for a bump made on its own host, and still does for another host.
func TestEntryReaderIgnoresOwnHostMarkerBumps(t *testing.T) {
	_, dagsDir, markerPath := markerTestStore(t)
	events := startMarkerReader(t, dagsDir, markerPath)

	writeDAGFile(t, dagsDir, "unseen.yaml", "unseen")
	bumpFrom(t, markerPath, "reader-host")
	expectNoEvent(t, events, 300*time.Millisecond)

	bumpFrom(t, markerPath, "other-host")
	expectEvent(t, events, persis.DAGChangeAdded, "unseen")
}

// TestChangeMarkerPoll covers the directory poll: a new or rewritten marker of
// another host is reported once, the reader's own marker is only recorded, and
// markers that disappear or do not belong to this DAG directory are ignored.
func TestChangeMarkerPoll(t *testing.T) {
	ctx := context.Background()
	_, _, markerPath := markerTestStore(t)
	reader := NewChangeMarker(readerMarkerPath(markerPath))

	// No directory, then no marker yet.
	seen, changed := reader.poll(ctx, nil)
	assert.False(t, changed)
	assert.Empty(t, seen)
	require.NoError(t, os.MkdirAll(filepath.Dir(markerPath), 0750))
	seen, changed = reader.poll(ctx, seen)
	assert.False(t, changed)

	// First appearance of another host's marker counts.
	remote := bumpFrom(t, markerPath, "host-a")
	seen, changed = reader.poll(ctx, seen)
	assert.True(t, changed)
	assert.Contains(t, seen, filepath.Base(remote))

	// The same version is not reported again.
	seen, changed = reader.poll(ctx, seen)
	assert.False(t, changed)

	// A rewrite counts, also when only the modification time moves.
	bumpFrom(t, markerPath, "host-a")
	seen, changed = reader.poll(ctx, seen)
	assert.True(t, changed)
	later := time.Now().Add(time.Hour)
	require.NoError(t, os.Chtimes(remote, later, later))
	seen, changed = reader.poll(ctx, seen)
	assert.True(t, changed)

	// Another host's marker counts independently of the first one.
	bumpFrom(t, markerPath, "host-b")
	seen, changed = reader.poll(ctx, seen)
	assert.True(t, changed)
	assert.Len(t, seen, 2)

	// The reader's own marker is recorded but never counts.
	own := bumpFrom(t, markerPath, "reader-host")
	seen, changed = reader.poll(ctx, seen)
	assert.False(t, changed)
	assert.Contains(t, seen, filepath.Base(own))
	bumpFrom(t, markerPath, "reader-host")
	seen, changed = reader.poll(ctx, seen)
	assert.False(t, changed)

	// A removed marker is dropped without a refresh; coming back counts again.
	require.NoError(t, os.Remove(remote))
	seen, changed = reader.poll(ctx, seen)
	assert.False(t, changed)
	assert.NotContains(t, seen, filepath.Base(remote))
	bumpFrom(t, markerPath, "host-a")
	_, changed = reader.poll(ctx, seen)
	assert.True(t, changed)
}

// TestChangeMarkerPollIgnoresStaleMarkers verifies markers that were not
// modified for a week, such as those of decommissioned hosts, are ignored and
// count again once rewritten.
func TestChangeMarkerPollIgnoresStaleMarkers(t *testing.T) {
	ctx := context.Background()
	_, _, markerPath := markerTestStore(t)
	reader := NewChangeMarker(readerMarkerPath(markerPath))

	stale := bumpFrom(t, markerPath, "old-host")
	old := time.Now().Add(-staleMarkerAge - time.Hour)
	require.NoError(t, os.Chtimes(stale, old, old))
	seen, changed := reader.poll(ctx, nil)
	assert.False(t, changed)
	assert.Empty(t, seen)

	recent := time.Now().Add(-staleMarkerAge + time.Hour)
	require.NoError(t, os.Chtimes(stale, recent, recent))
	seen, changed = reader.poll(ctx, seen)
	assert.True(t, changed)
	assert.Len(t, seen, 1)

	// A marker that turns stale is dropped without a refresh.
	require.NoError(t, os.Chtimes(stale, old, old))
	seen, changed = reader.poll(ctx, seen)
	assert.False(t, changed)
	assert.Empty(t, seen)
	assert.FileExists(t, stale, "polling never removes a marker")
}

// TestChangeMarkerBumpRemovesStaleMarkers verifies a bump deletes the marker
// files that every reader already ignores, and nothing else.
func TestChangeMarkerBumpRemovesStaleMarkers(t *testing.T) {
	_, dagsDir, markerPath := markerTestStore(t)
	dir := filepath.Dir(markerPath)
	require.NoError(t, os.MkdirAll(dir, 0750))
	old := time.Now().Add(-staleMarkerAge - time.Hour)
	writeStale := func(path string) string {
		t.Helper()
		require.NoError(t, os.WriteFile(path, []byte("x"), 0600))
		require.NoError(t, os.Chtimes(path, old, old))
		return path
	}

	staleHost := writeStale(hostMarkerPath(markerPath, "old-host"))
	staleOtherDir := writeStale(changeMarkerPathForHost(dir, filepath.Join(dagsDir, "elsewhere"), "old-host"))
	staleTemp := writeStale(hostMarkerPath(markerPath, "old-host") + ".tmp.1")
	staleIndex := writeStale(filepath.Join(dir, "0123456789abcdef.index"))
	staleDir := filepath.Join(dir, "dir"+changeMarkerSuffix)
	require.NoError(t, os.Mkdir(staleDir, 0750))
	require.NoError(t, os.Chtimes(staleDir, old, old))
	fresh := bumpFrom(t, markerPath, "live-host")

	NewChangeMarker(markerPath).Bump(context.Background())

	assert.NoFileExists(t, staleHost)
	assert.NoFileExists(t, staleOtherDir)
	for _, kept := range []string{markerPath, fresh, staleTemp, staleIndex} {
		assert.FileExists(t, kept)
	}
	assert.DirExists(t, staleDir)
}

// TestChangeMarkerPollIgnoresOtherFiles verifies only regular marker files of
// the reader's own DAG directory count.
func TestChangeMarkerPollIgnoresOtherFiles(t *testing.T) {
	ctx := context.Background()
	_, dagsDir, markerPath := markerTestStore(t)
	dir := filepath.Dir(markerPath)
	require.NoError(t, os.MkdirAll(dir, 0750))
	reader := NewChangeMarker(readerMarkerPath(markerPath))

	otherBase := changeMarkerPathForHost(dir, filepath.Join(dagsDir, "elsewhere"), "host-a")
	prefix, _, _ := strings.Cut(filepath.Base(markerPath), ".")
	require.NoError(t, os.WriteFile(otherBase, []byte("x"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.json"), []byte("{}"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, prefix+".host.tmp"), []byte("x"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, prefix+changeMarkerSuffix), []byte("x"), 0600))
	require.NoError(t, os.Mkdir(filepath.Join(dir, prefix+".dirhost"+changeMarkerSuffix), 0750))

	seen, changed := reader.poll(ctx, nil)
	assert.False(t, changed)
	assert.Empty(t, seen)
	_, changed = NewChangeMarker("").poll(ctx, nil)
	assert.False(t, changed)
}

// TestEntryReaderMarkerSignals covers which marker changes ask for a refresh:
// markers of other hosts and of an unknown host do, the reader's own does not
// but is recorded, and an unknown reader host ignores nothing.
func TestEntryReaderMarkerSignals(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name        string
		readerHost  string
		writerHost  string
		wantRefresh bool
	}{
		{name: "OtherHost", readerHost: "reader-host", writerHost: "other-host", wantRefresh: true},
		{name: "OwnHost", readerHost: "reader-host", writerHost: "reader-host"},
		{name: "UnknownWriterHost", readerHost: "reader-host", writerHost: "", wantRefresh: true},
		{name: "UnknownReaderHost", readerHost: "", writerHost: "", wantRefresh: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, dagsDir, markerPath := markerTestStore(t)
			repo := persis.NewDAGRepository(NewStore(dagsDir, WithSkipExamples(true)), persis.DAGRepositoryOptions{})
			er := NewFileEntryReader(dagsDir, repo, false, "", "",
				WithChangeMarker(hostMarkerPath(markerPath, tc.readerHost), time.Hour),
			)
			// A marker that exists at start-up is the baseline, not a change.
			bumpFrom(t, markerPath, "other-host")
			er.markerSeen, _ = er.marker.poll(ctx, nil)
			assert.False(t, er.markerSignalsChange(ctx), "nothing changed since the baseline")

			written := bumpFrom(t, markerPath, tc.writerHost)
			assert.Equal(t, tc.wantRefresh, er.markerSignalsChange(ctx))
			assert.Contains(t, er.markerSeen, filepath.Base(written), "the new version is recorded either way")
			assert.False(t, er.markerSignalsChange(ctx), "a recorded version is not reported twice")
		})
	}
}

// TestEntryReaderOwnBumpDoesNotHideRemoteBump is the regression test for hosts
// writing one shared marker: a bump from another host followed by a bump from
// the reader's own host within one poll interval must still refresh.
func TestEntryReaderOwnBumpDoesNotHideRemoteBump(t *testing.T) {
	ctx := context.Background()
	_, dagsDir, markerPath := markerTestStore(t)
	repo := persis.NewDAGRepository(NewStore(dagsDir, WithSkipExamples(true)), persis.DAGRepositoryOptions{})
	er := NewFileEntryReader(dagsDir, repo, false, "", "",
		WithChangeMarker(readerMarkerPath(markerPath), time.Hour),
	)
	er.markerSeen, _ = er.marker.poll(ctx, nil)

	bumpFrom(t, markerPath, "other-host")
	bumpFrom(t, markerPath, "reader-host")
	assert.True(t, er.markerSignalsChange(ctx))
	assert.False(t, er.markerSignalsChange(ctx))
}
