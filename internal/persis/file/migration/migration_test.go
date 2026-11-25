// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package migration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	ticks <- time.Now() // received once Run waits

	waiting := Progress{
		Phase:      phaseWaitingForV2,
		WaitingFor: []string{"coordinator c1 on host-b", "scheduler s1 on host-a"},
	}
	assert.Equal(t, waiting, m.Progress())
	assert.DirExists(t, mv.From, "nothing moves while v2 processes live")
	status, found, err := m.journal.readStatus()
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, waiting, status, "waiting processes report the same progress")

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
	ticks <- time.Now() // received once the second process waits

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
	ticks <- time.Now() // received once Run waits

	assert.Equal(t, Progress{
		Phase: phaseWaitingForSpace,
		Total: 1,
		Space: &SpaceShortage{Path: filepath.Dir(mv.To), Needed: 10, Free: 4},
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

// A destination that exists when the plan is made would read, on resume,
// as a finished copy whose source may be removed.
func TestRunRefusesPlanOverExistingState(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	mv := move{From: v2Dir(t, dataDir, "dag-runs", "etl", "f"), To: v2Dir(t, dataDir, "elsewhere", "etl", "g")}
	m, _ := newTestMigrator(t, dataDir, &fakeServices{}, []move{mv})

	err := m.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), mv.To+" already exists")
	assert.FileExists(t, filepath.Join(mv.From, "f"))
	done, err := m.Done()
	require.NoError(t, err)
	assert.False(t, done)
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
