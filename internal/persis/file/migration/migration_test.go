// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package migration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errKilled = errors.New("killed")

func TestRunMovesStateOnce(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	runs := v2Dir(t, dataDir, "dag-runs", "etl", "status.jsonl")
	logs := v2Dir(t, dataDir, "logs", "etl", "run.log")
	moves := []move{
		{From: runs, To: filepath.Join(dataDir, "projects", "default", "runs", "etl")},
		{From: logs, To: filepath.Join(dataDir, "projects", "default", "logs", "etl")},
	}
	m, _ := newTestMigrator(t, dataDir, &fakeServices{}, moves)

	require.NoError(t, m.Run(context.Background()))

	for _, mv := range moves {
		assert.NoDirExists(t, mv.From)
		assert.DirExists(t, mv.To)
	}
	assert.FileExists(t, filepath.Join(moves[0].To, "status.jsonl"))
	assert.Equal(t, Progress{Phase: phaseDone, Moved: 2, Total: 2}, m.Progress())

	again, _ := newTestMigrator(t, dataDir, &fakeServices{}, nil)
	again.planMoves = refusePlan(t)
	done, err := again.Done()
	require.NoError(t, err)
	assert.True(t, done)
	require.NoError(t, again.Run(context.Background()))
	assert.Equal(t, phaseDone, again.Progress().Phase)
}

// A process killed partway resumes with the plan it recorded, not a new
// one, and finishes each move it stopped in.
func TestRunResumesAfterKill(t *testing.T) {
	t.Parallel()

	t.Run("AfterRenameBeforeJournal", func(t *testing.T) {
		t.Parallel()
		dataDir := t.TempDir()
		moves := []move{
			{From: v2Dir(t, dataDir, "dag-runs", "a", "f"), To: filepath.Join(dataDir, "projects", "default", "runs", "a")},
			{From: v2Dir(t, dataDir, "dag-runs", "b", "f"), To: filepath.Join(dataDir, "projects", "default", "runs", "b")},
			{From: v2Dir(t, dataDir, "dag-runs", "c", "f"), To: filepath.Join(dataDir, "projects", "default", "runs", "c")},
		}
		first, _ := newTestMigrator(t, dataDir, &fakeServices{}, moves)
		first.afterMove = func(index int) error {
			if index == 1 {
				return errKilled
			}
			return nil
		}
		require.ErrorIs(t, first.Run(context.Background()), errKilled)
		assert.DirExists(t, moves[1].To, "the move happened, the journal does not know")
		assert.DirExists(t, moves[2].From)

		resumed, _ := newTestMigrator(t, dataDir, &fakeServices{}, nil)
		resumed.planMoves = refusePlan(t)
		require.NoError(t, resumed.Run(context.Background()))
		for _, mv := range moves {
			assert.NoDirExists(t, mv.From)
			assert.FileExists(t, filepath.Join(mv.To, "f"))
		}
		assert.Equal(t, Progress{Phase: phaseDone, Moved: 3, Total: 3}, resumed.Progress())
	})

	t.Run("DuringCopy", func(t *testing.T) {
		t.Parallel()
		dataDir := t.TempDir()
		mv := move{From: v2Dir(t, dataDir, "queue", "etl", "item.json"), To: filepath.Join(dataDir, "projects", "default", "state", "queue", "etl")}
		first, _ := newTestMigrator(t, dataDir, &fakeServices{}, []move{mv})
		first.mover.sameFileSystem = otherFileSystem
		first.mover.beforeInstall = func() error { return errKilled }
		require.ErrorIs(t, first.Run(context.Background()), errKilled)
		assert.DirExists(t, mv.To+copySuffix, "the copy is staged")
		assert.NoDirExists(t, mv.To)
		assert.DirExists(t, mv.From)

		resumed, _ := newTestMigrator(t, dataDir, &fakeServices{}, nil)
		resumed.planMoves = refusePlan(t)
		resumed.mover.sameFileSystem = otherFileSystem
		require.NoError(t, resumed.Run(context.Background()))
		assert.FileExists(t, filepath.Join(mv.To, "item.json"))
		assert.NoDirExists(t, mv.To+copySuffix)
		assert.NoDirExists(t, mv.From)
	})
}

func TestRunWaitsForV2Processes(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	mv := move{From: v2Dir(t, dataDir, "dag-runs", "etl", "f"), To: filepath.Join(dataDir, "projects", "default", "runs", "etl")}
	services := &fakeServices{}
	services.set(serviceregistry.ServiceNameScheduler, serviceregistry.HostInfo{ID: "s1", Host: "host-a"})
	services.set(serviceregistry.ServiceNameCoordinator, serviceregistry.HostInfo{ID: "c1", Host: "host-b"})
	m, ticks := newTestMigrator(t, dataDir, services, []move{mv})

	done := runAsync(m)
	tickWaiting(t, ticks, done)

	waiting := Progress{
		Phase:      phaseWaitingForV2,
		WaitingFor: []string{"coordinator c1 on host-b", "scheduler s1 on host-a"},
	}
	assert.Equal(t, waiting, m.Progress())
	assert.DirExists(t, mv.From, "nothing moves while v2 processes live")
	status, found, err := m.journal.readStatus()
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, waiting, status.Progress, "waiting processes report the same progress")

	services.set(serviceregistry.ServiceNameScheduler)
	services.set(serviceregistry.ServiceNameCoordinator)
	require.NoError(t, tickUntilDone(ticks, done))
	assert.DirExists(t, mv.To)
}

// A second process waits while the first holds the lock, reports the first
// one's progress, and returns once it finishes, without planning.
func TestRunWaitsForAnotherProcess(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	mv := move{From: v2Dir(t, dataDir, "dag-runs", "etl", "f"), To: filepath.Join(dataDir, "projects", "default", "runs", "etl")}

	first, _ := newTestMigrator(t, dataDir, &fakeServices{}, []move{mv})
	moving := make(chan struct{})
	release := make(chan struct{})
	first.afterMove = func(int) error {
		close(moving)
		<-release
		return nil
	}
	firstDone := runAsync(first)
	<-moving

	second, ticks := newTestMigrator(t, dataDir, &fakeServices{}, nil)
	second.planMoves = refusePlan(t)
	secondDone := runAsync(second)
	tickWaiting(t, ticks, secondDone)

	assert.Equal(t, Progress{Phase: phaseMoving, Total: 1}, second.Progress())

	close(release)
	require.NoError(t, <-firstDone)
	require.NoError(t, tickUntilDone(ticks, secondDone))
	assert.Equal(t, phaseDone, second.Progress().Phase)
}

func TestRunWaitsForFreeSpace(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	from := v2Dir(t, dataDir, "queue", "etl", "item.json")
	require.NoError(t, os.WriteFile(filepath.Join(from, "item.json"), []byte("0123456789"), 0o600))
	mv := move{From: from, To: filepath.Join(dataDir, "projects", "default", "state", "queue", "etl")}
	m, ticks := newTestMigrator(t, dataDir, &fakeServices{}, []move{mv})
	m.mover.sameFileSystem = otherFileSystem
	var free atomic.Uint64
	free.Store(4)
	m.mover.freeSpace = func(string) (uint64, error) { return free.Load(), nil }

	done := runAsync(m)
	tickWaiting(t, ticks, done)

	assert.Equal(t, Progress{
		Phase: phaseWaitingForSpace,
		Total: 1,
		// A 10-byte file and its directory take a block each, and a
		// sixteenth more.
		Space: &SpaceShortage{Path: filepath.Dir(mv.To), Needed: 8704, Free: 4},
	}, m.Progress())
	assert.DirExists(t, mv.From, "nothing moves while space is short")
	assert.NoDirExists(t, mv.To)

	free.Store(1 << 20)
	require.NoError(t, tickUntilDone(ticks, done))
	data, err := os.ReadFile(filepath.Join(mv.To, "item.json"))
	require.NoError(t, err)
	assert.Equal(t, "0123456789", string(data))
	assert.NoDirExists(t, mv.From)
}

// The part of a copy that a killed attempt left does not count against the
// free space the copy needs again.
func TestRunReclaimsStagedCopyBeforeSpaceCheck(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	from := v2Dir(t, dataDir, "queue", "etl", "item.json")
	require.NoError(t, os.WriteFile(filepath.Join(from, "item.json"), []byte("0123456789"), 0o600))
	mv := move{From: from, To: filepath.Join(dataDir, "projects", "default", "state", "queue", "etl")}
	staging := v2Dir(t, filepath.Dir(mv.To), filepath.Base(mv.To)+copySuffix, "etl", "item.json")
	m, ticks := newTestMigrator(t, dataDir, &fakeServices{}, []move{mv})
	m.mover.sameFileSystem = otherFileSystem
	// The staged copy holds the room the copy needs.
	m.mover.freeSpace = func(string) (uint64, error) {
		if _, err := os.Stat(staging); err == nil {
			return allocationUnit, nil
		}
		return 1 << 20, nil
	}

	requireNoWait(t, runAsync(m), ticks)
	assert.FileExists(t, filepath.Join(mv.To, "item.json"))
	assert.NoDirExists(t, filepath.Dir(staging))
}

// A copy installed before its source was removed needs no space, only the
// removal.
func TestRunSkipsInstalledCopyInSpaceCheck(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	from := v2Dir(t, dataDir, "queue", "etl", "item.json")
	to := v2Dir(t, filepath.Join(dataDir, "projects", "default", "state"), "queue", "etl", "item.json")
	mv := move{From: from, To: to}
	m, ticks := newTestMigrator(t, dataDir, &fakeServices{}, nil)
	require.NoError(t, m.journal.writePlan(planFile{Version: planVersion, Moves: []move{mv}}), "the plan of the killed attempt")
	require.NoError(t, m.journal.markInstalling(0), "the copy it installed")
	m.planMoves = refusePlan(t)
	m.mover.sameFileSystem = otherFileSystem
	m.mover.freeSpace = func(string) (uint64, error) { return 0, nil }

	requireNoWait(t, runAsync(m), ticks)
	assert.NoDirExists(t, from)
	assert.FileExists(t, filepath.Join(to, "item.json"))
}

// A destination that appeared after the plan, beside a source the journal
// records no copy of, stops the migration and keeps both.
func TestRunRefusesForeignDestination(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	from := v2Dir(t, dataDir, "queue", "etl", "item.json")
	to := v2Dir(t, filepath.Join(dataDir, "projects", "default", "state"), "queue", "etl", "item.json")
	m, _ := newTestMigrator(t, dataDir, &fakeServices{}, nil)
	require.NoError(t, m.journal.writePlan(planFile{Version: planVersion, Moves: []move{{From: from, To: to}}}))
	m.planMoves = refusePlan(t)

	require.ErrorIs(t, m.Run(context.Background()), errForeignDestination)
	assert.FileExists(t, filepath.Join(from, "item.json"))
	assert.FileExists(t, filepath.Join(to, "item.json"))
	done, err := m.Done()
	require.NoError(t, err)
	assert.False(t, done)
}

// Queue directories keep the dots of DAG names, so one move's destination
// may be another's name plus a suffix. The staging copy of one move never
// replaces what another moved, whatever their order.
func TestRunCopiesBesideDottedNames(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	dest := filepath.Join(dataDir, "projects", "default", "state", "queue")
	moves := []move{
		{From: v2Dir(t, dataDir, "queue", "etl.migrating", "item.json"), To: filepath.Join(dest, "etl.migrating")},
		{From: v2Dir(t, dataDir, "queue", "etl", "item.json"), To: filepath.Join(dest, "etl")},
	}
	m, _ := newTestMigrator(t, dataDir, &fakeServices{}, moves)
	m.mover.sameFileSystem = otherFileSystem

	require.NoError(t, m.Run(context.Background()))
	for _, mv := range moves {
		assert.NoDirExists(t, mv.From)
		assert.FileExists(t, filepath.Join(mv.To, "item.json"))
	}
}

// A copy that fills its file system despite the estimate frees its space
// again and waits for more room than it ran out of, instead of exiting.
func TestRunWaitsWhenCopyFillsDisk(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	mv := move{From: v2Dir(t, dataDir, "queue", "etl", "item.json"), To: filepath.Join(dataDir, "projects", "default", "state", "queue", "etl")}
	m, ticks := newTestMigrator(t, dataDir, &fakeServices{}, []move{mv})
	m.mover.sameFileSystem = otherFileSystem
	var free atomic.Uint64
	free.Store(1 << 20)
	m.mover.freeSpace = func(string) (uint64, error) { return free.Load(), nil }
	var attempts atomic.Int32
	m.mover.beforeInstall = func() error {
		if attempts.Add(1) == 1 {
			return errNoSpace
		}
		return nil
	}

	done := runAsync(m)
	tickWaiting(t, ticks, done)

	progress := m.Progress()
	assert.Equal(t, phaseWaitingForSpace, progress.Phase)
	require.NotNil(t, progress.Space)
	assert.Equal(t, uint64(1<<20+1<<18+allocationUnit), progress.Space.Needed, "more than the copy ran out of")
	assert.NoDirExists(t, mv.To+copySuffix, "the failed copy frees its space")
	assert.DirExists(t, mv.From)

	free.Store(2 << 20)
	require.NoError(t, tickUntilDone(ticks, done))
	assert.FileExists(t, filepath.Join(mv.To, "item.json"))
	assert.NoDirExists(t, mv.From)
	assert.Equal(t, int32(2), attempts.Load())
}

// A process that lost the lock while it waits for v2 processes stops: the
// schedulers it sees may be v3.0 ones that registered after the process that
// took the lock over finished, and it must not replace that one's status.
func TestRunStopsWaitingForV2WithoutLock(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	services := &fakeServices{}
	services.set(serviceregistry.ServiceNameScheduler, serviceregistry.HostInfo{ID: "s1", Host: "v2-host"})
	stalled, ticks := newTestMigrator(t, dataDir, services, nil)
	stalledDone := runAsync(stalled)
	tickWaiting(t, ticks, stalledDone)

	// The lock passes to another process, as after a stall past the stale
	// threshold, and that process finishes.
	require.NoError(t, dirlock.ForceUnlock(filepath.Join(dataDir, dirName)))
	other, _ := newTestMigrator(t, dataDir, &fakeServices{}, nil)
	require.NoError(t, other.Run(context.Background()))
	services.set(serviceregistry.ServiceNameScheduler, serviceregistry.HostInfo{ID: "s2", Host: "v3-host"})

	tickWaiting(t, ticks, stalledDone) // wakes the first process
	select {
	case ticks <- time.Now():
		t.Fatal("kept waiting without the lock")
	case err := <-stalledDone:
		require.ErrorIs(t, err, dirlock.ErrLockNotHeld)
	}
	done, err := New(dataDir, &fakeServices{}).Done()
	require.NoError(t, err)
	assert.True(t, done, "the status of the finished migration stays")
}

// A destination that exists when the plan is made is refused before any
// move, rather than at its move after earlier ones were made.
func TestRunRefusesPlanOverExistingState(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		// destination creates the entry the plan moves onto.
		destination func(t *testing.T, dataDir string) string
	}{
		{name: "Directory", destination: func(t *testing.T, dataDir string) string {
			return v2Dir(t, dataDir, "elsewhere", "etl", "g")
		}},
		{name: "DanglingSymlink", destination: func(t *testing.T, dataDir string) string {
			if runtime.GOOS == "windows" {
				t.Skip("creating a symbolic link needs a privilege on Windows")
			}
			link := filepath.Join(dataDir, "dangling")
			require.NoError(t, os.Symlink(filepath.Join(dataDir, "missing"), link))
			return link
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dataDir := t.TempDir()
			mv := move{From: v2Dir(t, dataDir, "dag-runs", "etl", "f"), To: tc.destination(t, dataDir)}
			m, _ := newTestMigrator(t, dataDir, &fakeServices{}, []move{mv})

			err := m.Run(context.Background())
			require.Error(t, err)
			assert.Contains(t, err.Error(), mv.To+" already exists")
			assert.FileExists(t, filepath.Join(mv.From, "f"))
			done, err := m.Done()
			require.NoError(t, err)
			assert.False(t, done)
		})
	}
}

// A plan whose paths overlap is refused before any move: one move would
// change what another moves, or create the destination another finds.
func TestRunRefusesOverlappingPlan(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		// plan returns moves of the sources a and b in dataDir.
		plan func(dataDir, a, b string) []move
		// foldsCase marks a plan that overlaps only where case is ignored.
		foldsCase bool
	}{
		{name: "NestedDestinations", plan: func(dataDir, a, b string) []move {
			return []move{
				{From: b, To: filepath.Join(dataDir, "projects", "x", "y")},
				{From: a, To: filepath.Join(dataDir, "projects", "x")},
			}
		}},
		{name: "NestedSources", plan: func(dataDir, a, _ string) []move {
			return []move{
				{From: a, To: filepath.Join(dataDir, "projects", "a")},
				{From: filepath.Join(a, "f"), To: filepath.Join(dataDir, "projects", "f")},
			}
		}},
		{name: "DestinationInSource", plan: func(_, a, _ string) []move {
			return []move{{From: a, To: filepath.Join(a, "moved")}}
		}},
		{name: "SameSource", plan: func(dataDir, a, _ string) []move {
			return []move{
				{From: a, To: filepath.Join(dataDir, "projects", "a")},
				{From: a, To: filepath.Join(dataDir, "projects", "b")},
			}
		}},
		{name: "DestinationNamesStagingCopy", plan: func(dataDir, a, b string) []move {
			return []move{
				{From: a, To: filepath.Join(dataDir, "projects", "a")},
				{From: b, To: filepath.Join(dataDir, "projects", "a"+copySuffix)},
			}
		}},
		{name: "DestinationsDifferInCase", foldsCase: true, plan: func(dataDir, a, b string) []move {
			return []move{
				{From: a, To: filepath.Join(dataDir, "projects", "Etl")},
				{From: b, To: filepath.Join(dataDir, "projects", "etl")},
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.foldsCase && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
				t.Skip("file systems here tell case apart by default")
			}
			dataDir := t.TempDir()
			a := v2Dir(t, dataDir, "dag-runs", "a", "f")
			b := v2Dir(t, dataDir, "dag-runs", "b", "f")
			m, _ := newTestMigrator(t, dataDir, &fakeServices{}, tc.plan(dataDir, a, b))

			err := m.Run(context.Background())
			require.Error(t, err)
			assert.Contains(t, err.Error(), "plan the migration")
			assert.FileExists(t, filepath.Join(a, "f"))
			assert.FileExists(t, filepath.Join(b, "f"))
			assert.NoDirExists(t, filepath.Join(dataDir, "projects"))
		})
	}
}

// A migration that an earlier release planned does not count as done,
// finished or not: Run finishes its plan, then plans and makes the moves of
// this release.
func TestRunPlansAgainAfterEarlierRelease(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		// earlier records the journal that the earlier release left for its
		// move of the source a to the destination to.
		earlier func(t *testing.T, j journal, a, to string)
	}{
		{name: "Finished", earlier: func(t *testing.T, j journal, a, to string) {
			require.NoError(t, os.MkdirAll(filepath.Dir(to), 0o750))
			require.NoError(t, os.Rename(a, to))
			require.NoError(t, j.writePlan(planFile{Version: planVersion - 1, Moves: []move{{From: a, To: to}}}))
			require.NoError(t, j.markMoved(0))
			require.NoError(t, j.writeStatus(statusFile{
				Progress: Progress{Phase: phaseDone, Moved: 1, Total: 1},
				Version:  planVersion - 1,
			}))
		}},
		{name: "Interrupted", earlier: func(t *testing.T, j journal, a, to string) {
			require.NoError(t, j.writePlan(planFile{Version: planVersion - 1, Moves: []move{{From: a, To: to}}}))
			require.NoError(t, j.writeStatus(statusFile{Progress: Progress{Phase: phaseMoving, Total: 1}}))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dataDir := t.TempDir()
			earlier := move{From: v2Dir(t, dataDir, "dag-runs", "a", "f"), To: filepath.Join(dataDir, "projects", "default", "runs", "a")}
			later := move{From: v2Dir(t, dataDir, "logs", "a", "f"), To: filepath.Join(dataDir, "projects", "default", "logs", "a")}
			m, _ := newTestMigrator(t, dataDir, &fakeServices{}, []move{later})
			tc.earlier(t, m.journal, earlier.From, earlier.To)

			done, err := m.Done()
			require.NoError(t, err)
			require.False(t, done)

			require.NoError(t, m.Run(context.Background()))
			for _, mv := range []move{earlier, later} {
				assert.NoDirExists(t, mv.From)
				assert.FileExists(t, filepath.Join(mv.To, "f"))
			}
			assert.Equal(t, Progress{Phase: phaseDone, Moved: 1, Total: 1}, m.Progress())
			done, err = New(dataDir, &fakeServices{}).Done()
			require.NoError(t, err)
			assert.True(t, done)
		})
	}
}

func TestDoneRefusesUnreadableStatus(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	dir := filepath.Join(dataDir, dirName)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, statusFileName), []byte("{"), 0o600))

	_, err := New(dataDir, &fakeServices{}).Done()
	require.Error(t, err)
	assert.Contains(t, err.Error(), statusFileName)
}

func newTestMigrator(t *testing.T, dataDir string, services serviceregistry.ServiceRegistry, moves []move) (*Migrator, chan time.Time) {
	t.Helper()
	m := New(dataDir, services)
	ticks := make(chan time.Time)
	m.pollTicks = func() (<-chan time.Time, func()) { return ticks, func() {} }
	m.planMoves = func(context.Context) ([]move, error) { return moves, nil }
	return m, ticks
}

// refusePlan fails the test when a resumed or waiting process plans.
func refusePlan(t *testing.T) func(context.Context) ([]move, error) {
	return func(context.Context) ([]move, error) {
		t.Error("planned the migration again")
		return nil, errors.New("planned again")
	}
}

// v2Dir creates <dataDir>/<store>/<dag>/<file> and returns the DAG's
// directory.
func v2Dir(t *testing.T, dataDir, store, dag, file string) string {
	t.Helper()
	dir := filepath.Join(dataDir, store, dag)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte(dag), 0o600))
	return dir
}

func otherFileSystem(string, string) (bool, error) { return false, nil }

func runAsync(m *Migrator) <-chan error {
	done := make(chan error, 1)
	go func() { done <- m.Run(context.Background()) }()
	return done
}

// requireNoWait fails the test when Run waits instead of returning.
func requireNoWait(t *testing.T, done <-chan error, ticks chan<- time.Time) {
	t.Helper()
	select {
	case err := <-done:
		require.NoError(t, err)
	case ticks <- time.Now():
		t.Fatal("Run waited")
	}
}

// tickWaiting sends a tick that a waiting Run receives, and fails the test
// when Run returns instead.
func tickWaiting(t *testing.T, ticks chan<- time.Time, done <-chan error) {
	t.Helper()
	select {
	case ticks <- time.Now():
	case err := <-done:
		t.Fatalf("Run returned instead of waiting: %v", err)
	}
}

// tickUntilDone ticks a waiting Run until it returns.
func tickUntilDone(ticks chan<- time.Time, done <-chan error) error {
	for {
		select {
		case ticks <- time.Now():
		case err := <-done:
			return err
		}
	}
}

type fakeServices struct {
	mu      sync.Mutex
	members map[serviceregistry.ServiceName][]serviceregistry.HostInfo
}

var _ serviceregistry.ServiceRegistry = (*fakeServices)(nil)

func (f *fakeServices) set(name serviceregistry.ServiceName, members ...serviceregistry.HostInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.members == nil {
		f.members = make(map[serviceregistry.ServiceName][]serviceregistry.HostInfo)
	}
	f.members[name] = members
}

func (f *fakeServices) GetServiceMembers(_ context.Context, name serviceregistry.ServiceName) ([]serviceregistry.HostInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]serviceregistry.HostInfo(nil), f.members[name]...), nil
}

func (f *fakeServices) Register(context.Context, serviceregistry.ServiceName, serviceregistry.HostInfo) error {
	return nil
}

func (f *fakeServices) Unregister(context.Context) {}

func (f *fakeServices) UpdateStatus(context.Context, serviceregistry.ServiceName, serviceregistry.ServiceStatus) error {
	return nil
}
