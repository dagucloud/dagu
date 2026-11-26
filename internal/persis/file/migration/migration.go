// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package migration moves v2 state into the state directories of projects
// on the first start with projects on, and on the first start of a release
// that plans more moves. One process migrates under a lock; every other
// process waits for it.
//
//	<data_dir>/migration/
//	├── .dagu_lock/     held by the process that migrates
//	├── plan.json       the moves and their version, written before the first move
//	├── moved.log       the index of each finished move
//	├── installing.log  the index of each copy, before it takes its destination's name
//	└── status.json     progress, and the version of a finished migration
package migration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
)

const (
	dirName = "migration"

	// pollInterval is how often a process checks again what it waits for:
	// the lock, v2 processes, or free space.
	pollInterval = time.Second
	// lockStaleThreshold frees the lock of a process killed while it
	// migrates, so the next start resumes.
	lockStaleThreshold = 30 * time.Second
	// statusEvery is how many moves pass between two writes of the status
	// that waiting processes report.
	statusEvery = 100
	// planVersion identifies the moves that this release plans. A release
	// that plans moves an earlier one did not make raises it, so the
	// migration runs again where the earlier one finished.
	planVersion = 1
)

// Phase is a stage of the migration.
type Phase string

const (
	// phaseStarting is reported until the migrating process writes its
	// first status.
	phaseStarting Phase = "starting"
	// phaseWaitingForV2 waits while a v2 scheduler or coordinator is live.
	phaseWaitingForV2 Phase = "waiting-for-v2"
	// phaseMoving moves state.
	phaseMoving Phase = "moving"
	// phaseWaitingForSpace waits for free space to copy state to another
	// file system.
	phaseWaitingForSpace Phase = "waiting-for-space"
	// phaseDone reports that the state is migrated.
	phaseDone Phase = "done"
)

// Progress reports how far the migration has come.
type Progress struct {
	Phase Phase `json:"phase"`
	// WaitingFor names each live v2 scheduler and coordinator.
	WaitingFor []string `json:"waitingFor,omitempty"`
	Moved      int      `json:"moved"`
	Total      int      `json:"total"`
	// Space describes the copy that waits for free space.
	Space *SpaceShortage `json:"space,omitempty"`
}

// SpaceShortage names a file system that lacks the free space a copy needs.
type SpaceShortage struct {
	Path   string `json:"path"`
	Needed uint64 `json:"neededBytes"`
	Free   uint64 `json:"freeBytes"`
}

// Migrator migrates the state of one paths.data_dir. Progress may be called
// while Run runs, and several processes may each run a Migrator on the same
// directory.
type Migrator struct {
	journal  journal
	lock     dirlock.DirLock
	services serviceregistry.ServiceRegistry
	mover    mover
	// planMoves returns the moves of the v2 state. It runs once, under the
	// lock, before the first move.
	planMoves func(context.Context) ([]move, error)
	// pollTicks returns the ticks on which a waiting process checks again,
	// and a function that stops them.
	pollTicks func() (<-chan time.Time, func())
	// now returns the current time, by which held judges the age of the
	// last renewal of the lock.
	now func() time.Time
	// afterMove, when set by tests, runs after each move and before the
	// journal records it.
	afterMove func(index int) error
	// beforeRecordCopy, when set by tests, runs before the journal records
	// a copy as installing.
	beforeRecordCopy func(index int) error

	mu       sync.Mutex
	progress Progress
}

// move moves the file or directory From to To.
type move struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// New returns the Migrator of dataDir. services lists the live schedulers
// and coordinators, each of which is a v2 process while the migration is
// not done, since a v3.0 process registers only after it.
func New(dataDir string, services serviceregistry.ServiceRegistry) *Migrator {
	dir := filepath.Join(dataDir, dirName)
	return &Migrator{
		journal: journal{dir: dir},
		lock: dirlock.New(dir, &dirlock.LockOptions{
			StaleThreshold:   lockStaleThreshold,
			ObserveHeartbeat: true,
		}),
		services:  services,
		mover:     newMover(),
		planMoves: planNoMoves,
		pollTicks: newPollTicker,
		now:       time.Now,
		progress:  Progress{Phase: phaseStarting},
	}
}

// planNoMoves moves nothing; the state stays where v2 keeps it.
func planNoMoves(context.Context) ([]move, error) {
	return nil, nil
}

func newPollTicker() (<-chan time.Time, func()) {
	ticker := time.NewTicker(pollInterval)
	return ticker.C, ticker.Stop
}

// Done reports whether the state is migrated, up to the moves that this
// release plans. It changes nothing on disk.
func (m *Migrator) Done() (bool, error) {
	status, found, err := m.journal.readStatus()
	if err != nil || !found {
		return false, err
	}
	return status.done(), nil
}

// Progress returns the progress of the migration, whichever process runs it.
func (m *Migrator) Progress() Progress {
	m.mu.Lock()
	defer m.mu.Unlock()
	progress := m.progress
	progress.WaitingFor = slices.Clone(progress.WaitingFor)
	return progress
}

// Run returns once the state is migrated, by this process under the lock or
// by another one it waits for. A migration that a killed process left
// resumes where it stopped. Run waits while a v2 scheduler or coordinator is
// live, and while a copy to another file system lacks free space, until ctx
// ends.
func (m *Migrator) Run(ctx context.Context) error {
	ticks, stop := m.pollTicks()
	defer stop()

	waitLogged := false
	for {
		if done, err := m.checkDone(); err != nil || done {
			return err
		}
		err := m.lock.TryLock()
		if err == nil {
			return m.migrateLocked(ctx, ticks)
		}
		if !errors.Is(err, dirlock.ErrLockConflict) {
			return fmt.Errorf("take the migration lock: %w", err)
		}
		if !waitLogged {
			logger.Info(ctx, "Waiting for another process to migrate state to projects",
				tag.Dir(m.journal.dir))
			waitLogged = true
		}
		if err := waitTick(ctx, ticks); err != nil {
			return err
		}
	}
}

// checkDone reads the status that the migrating process writes, so this
// process reports it while it waits.
func (m *Migrator) checkDone() (bool, error) {
	status, found, err := m.journal.readStatus()
	if err != nil || !found {
		return false, err
	}
	m.setProgress(status.Progress)
	return status.done(), nil
}

func (m *Migrator) migrateLocked(ctx context.Context, ticks <-chan time.Time) error {
	held, stopRenewing := m.keepLock(ctx)
	defer func() {
		stopRenewing()
		_ = m.lock.Unlock()
	}()

	// Another process may have finished between the check and the lock.
	if done, err := m.checkDone(); err != nil || done {
		return err
	}
	if err := m.waitForV2(ctx, ticks, held); err != nil {
		return err
	}
	for {
		plan, err := m.loadOrPlan(ctx, held)
		if err != nil {
			return err
		}
		progress, err := m.makeMoves(ctx, ticks, plan.Moves, held)
		if err != nil {
			return err
		}
		if plan.Version >= planVersion {
			progress.Phase = phaseDone
			if err := m.publishStatus(statusFile{Progress: progress, Version: plan.Version}, held); err != nil {
				return err
			}
			logger.Info(ctx, "Migrated state to projects", slog.Int("moves", len(plan.Moves)))
			return nil
		}
		// The plan of an earlier release is finished; plan this release's.
		logger.Info(ctx, "Planning the moves that this release adds to the migration",
			slog.Int("from-version", plan.Version), slog.Int("version", planVersion))
		if err := held(); err != nil {
			return err
		}
		if err := m.journal.reset(); err != nil {
			return err
		}
	}
}

// makeMoves makes each move that the journal does not record, and returns
// the progress once all are made.
func (m *Migrator) makeMoves(ctx context.Context, ticks <-chan time.Time, moves []move, held func() error) (Progress, error) {
	moved, err := m.journal.readMoved(len(moves))
	if err != nil {
		return Progress{}, err
	}
	installing, err := m.journal.readInstalling(len(moves))
	if err != nil {
		return Progress{}, err
	}
	progress := Progress{Phase: phaseMoving, Moved: len(moved), Total: len(moves)}
	// floors holds, by destination, the space a copy needs at least, learned
	// when its file system filled up during the copy.
	floors := make(map[string]uint64)
	if err := m.waitForSpace(ctx, ticks, pendingMoves(moves, moved, 0), floors, progress, held); err != nil {
		return Progress{}, err
	}
	if err := m.publishHeld(progress, held); err != nil {
		return Progress{}, err
	}
	logger.Info(ctx, "Migrating state to projects",
		slog.Int("moves", len(moves)), slog.Int("done", len(moved)))

	for i := 0; i < len(moves); {
		if _, ok := moved[i]; ok {
			i++
			continue
		}
		_, recorded := installing[i]
		err := m.moveOne(ctx, i, moves[i], recorded, held)
		if isNoSpace(err) {
			// The estimate fell short, another writer took the room, or the
			// journal's file system is full.
			err = m.waitAfterFull(ctx, ticks, moves[i], err, pendingMoves(moves, moved, i), floors, progress, held)
			if err != nil {
				return Progress{}, err
			}
			if err := m.publishHeld(progress, held); err != nil {
				return Progress{}, err
			}
			continue
		}
		if err != nil {
			return Progress{}, err
		}
		i++
		progress.Moved++
		if progress.Moved%statusEvery != 0 {
			m.setProgress(progress)
		} else if err := m.publishHeld(progress, held); err != nil {
			return Progress{}, err
		}
	}
	return progress, nil
}

// moveOne makes the move at index and records it in the journal. recorded
// reports that the journal records the move's copy as installing.
func (m *Migrator) moveOne(ctx context.Context, index int, mv move, recorded bool, held func() error) error {
	install := installRecord{
		recorded: recorded,
		record: func() error {
			if m.beforeRecordCopy != nil {
				if err := m.beforeRecordCopy(index); err != nil {
					return err
				}
			}
			return m.journal.markInstalling(index)
		},
	}
	if err := m.mover.move(ctx, mv, install, held); err != nil {
		return fmt.Errorf("move %s to %s: %w", mv.From, mv.To, err)
	}
	if m.afterMove != nil {
		if err := m.afterMove(index); err != nil {
			return err
		}
	}
	return m.journal.markMoved(index)
}

// keepLock renews the lock until stop is called, so a long migration keeps
// it. held fences each step that changes state: it refuses once this process
// lost the lock, or once its last renewal is three quarters of the stale
// threshold old, before a waiting process could judge the lock stale.
func (m *Migrator) keepLock(ctx context.Context) (held func() error, stop func()) {
	var renewedAt atomic.Int64
	renewedAt.Store(m.now().UnixNano())
	ctx, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(lockStaleThreshold / 4)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				start := m.now()
				if err := m.lock.Heartbeat(ctx); err != nil {
					logger.Warn(ctx, "Failed to renew the migration lock", tag.Error(err))
					continue
				}
				renewedAt.Store(start.UnixNano())
			}
		}
	}()
	held = func() error {
		renewed := time.Unix(0, renewedAt.Load())
		if m.now().Sub(renewed) > lockStaleThreshold*3/4 || !m.lock.IsHeldByMe() {
			return fmt.Errorf("lost the migration lock: %w", dirlock.ErrLockNotHeld)
		}
		return nil
	}
	stop = func() {
		cancel()
		<-stopped
	}
	return held, stop
}

// waitForV2 returns once no v2 scheduler or coordinator is live. A v2
// server has no service registry entry, so stopping it is the operator's
// step. It returns an error once this process loses the lock: after the
// process that took it over finishes, the live schedulers are v3.0 ones.
func (m *Migrator) waitForV2(ctx context.Context, ticks <-chan time.Time, held func() error) error {
	var reported []string
	for {
		if err := held(); err != nil {
			return err
		}
		live, err := m.liveV2(ctx)
		if err != nil {
			return err
		}
		if len(live) == 0 {
			return nil
		}
		if !slices.Equal(live, reported) {
			logger.Info(ctx, "Waiting for v2 processes to stop before migrating state to projects",
				slog.Any("processes", live))
			if err := m.publishHeld(Progress{Phase: phaseWaitingForV2, WaitingFor: live}, held); err != nil {
				return err
			}
			reported = live
		}
		if err := waitTick(ctx, ticks); err != nil {
			return err
		}
	}
}

func (m *Migrator) liveV2(ctx context.Context) ([]string, error) {
	var live []string
	for _, name := range []serviceregistry.ServiceName{
		serviceregistry.ServiceNameScheduler,
		serviceregistry.ServiceNameCoordinator,
	} {
		members, err := m.services.GetServiceMembers(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("list live %s processes: %w", name, err)
		}
		for _, member := range members {
			live = append(live, fmt.Sprintf("%s %s on %s", name, member.ID, member.Host))
		}
	}
	slices.Sort(live)
	return live, nil
}

// loadOrPlan returns the plan of an earlier attempt, which an earlier
// release may have made, or plans the moves and records them before the
// first one.
func (m *Migrator) loadOrPlan(ctx context.Context, held func() error) (planFile, error) {
	plan, found, err := m.journal.readPlan()
	if err != nil || found {
		return plan, err
	}
	moves, err := m.planMoves(ctx)
	if err != nil {
		return planFile{}, fmt.Errorf("plan the migration: %w", err)
	}
	if err := checkPlan(moves); err != nil {
		return planFile{}, err
	}
	if err := held(); err != nil {
		return planFile{}, err
	}
	plan = planFile{Version: planVersion, Moves: moves}
	if err := m.journal.writePlan(plan); err != nil {
		return planFile{}, err
	}
	return plan, nil
}

// checkPlan refuses a plan that would stop partway or lose state, before any
// move is made:
//   - a destination that exists, which its move would refuse;
//   - a path, source or destination or the staging copy of a destination,
//     that names another path of the plan again or lies in one, so that one
//     move would change or discard what another moves, or create the
//     destination another finds.
func checkPlan(moves []move) error {
	paths := make(map[string]string, 3*len(moves))
	for _, mv := range moves {
		for _, path := range []string{mv.From, mv.To, mv.To + copySuffix} {
			key := planKey(path)
			if other, dup := paths[key]; dup {
				return fmt.Errorf("plan the migration: %s and %s name one path", other, path)
			}
			paths[key] = path
		}
		found, err := exists(mv.To)
		switch {
		case err != nil:
			return fmt.Errorf("plan the migration: %w", err)
		case found:
			return fmt.Errorf("plan the migration: %s already exists", mv.To)
		}
	}
	for _, mv := range moves {
		for _, path := range []string{mv.From, mv.To} {
			key := planKey(path)
			for parent := filepath.Dir(key); parent != key; key, parent = parent, filepath.Dir(parent) {
				if other, ok := paths[parent]; ok {
					return fmt.Errorf("plan the migration: %s lies in %s", path, other)
				}
			}
		}
	}
	return nil
}

// planKey returns the key under which checkPlan compares path. The file
// systems of macOS and Windows ignore case by default, so there two paths
// that differ in case only name one entry.
func planKey(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

// pendingMoves returns the moves from index from on that the journal does
// not record.
func pendingMoves(moves []move, moved map[int]struct{}, from int) []move {
	var pending []move
	for i := from; i < len(moves); i++ {
		if _, ok := moved[i]; !ok {
			pending = append(pending, moves[i])
		}
	}
	return pending
}

// waitForSpace returns once every copy the pending moves need fits in the
// free space of its file system. Until then it moves nothing.
func (m *Migrator) waitForSpace(
	ctx context.Context, ticks <-chan time.Time, pending []move, floors map[string]uint64,
	progress Progress, held func() error,
) error {
	copies, err := m.mover.pendingCopies(pending, floors, held)
	if err != nil || len(copies) == 0 {
		return err
	}
	var reported *SpaceShortage
	for {
		if err := held(); err != nil {
			return err
		}
		shortage, err := m.mover.shortage(copies)
		if err != nil {
			return err
		}
		if shortage == nil {
			return nil
		}
		if reported == nil || *reported != *shortage {
			logger.Warn(ctx, "Waiting for free space to copy state to another file system",
				tag.Path(shortage.Path),
				slog.Uint64("needed-bytes", shortage.Needed),
				slog.Uint64("free-bytes", shortage.Free))
			if err := m.publishShortage(progress, shortage, held); err != nil {
				return err
			}
			reported = shortage
		}
		if err := waitTick(ctx, ticks); err != nil {
			return err
		}
	}
}

// waitAfterFull waits after a file system filled up during the move of mv:
// at least one tick, and, when moveErr reports that the copy filled the file
// system of mv.To, until that one has more room than the copy ran out of.
// Any other write that found its file system full, such as the journal's,
// sets no floor, since the destination is not what lacks room.
func (m *Migrator) waitAfterFull(
	ctx context.Context, ticks <-chan time.Time, mv move, moveErr error, pending []move,
	floors map[string]uint64, progress Progress, held func() error,
) error {
	if errors.Is(moveErr, errCopyFull) {
		free, err := m.mover.freeSpace(mv.To)
		if err != nil {
			return fmt.Errorf("check free space for %s: %w", mv.To, err)
		}
		floors[mv.To] = free + free/4 + allocationUnit
		shortage := &SpaceShortage{Path: filepath.Dir(mv.To), Needed: floors[mv.To], Free: free}
		logger.Warn(ctx, "File system filled up while migrating state; waiting for free space",
			tag.Path(shortage.Path), slog.Uint64("free-bytes", free))
		if err := m.publishShortage(progress, shortage, held); err != nil {
			return err
		}
	} else {
		logger.Warn(ctx, "File system filled up while migrating state; retrying", tag.Error(moveErr))
	}
	if err := waitTick(ctx, ticks); err != nil {
		return err
	}
	return m.waitForSpace(ctx, ticks, pending, floors, progress, held)
}

func (m *Migrator) publishShortage(progress Progress, shortage *SpaceShortage, held func() error) error {
	progress.Phase = phaseWaitingForSpace
	progress.Space = shortage
	return m.publishHeld(progress, held)
}

// publishHeld records progress for this process and, while it holds the
// lock, writes it for the processes that wait. A process that lost the lock
// writes nothing, so it never replaces the status of the one that took the
// lock over.
func (m *Migrator) publishHeld(progress Progress, held func() error) error {
	return m.publishStatus(statusFile{Progress: progress}, held)
}

// publishStatus is publishHeld for a status that also names the version a
// finished migration made.
func (m *Migrator) publishStatus(status statusFile, held func() error) error {
	if err := held(); err != nil {
		return err
	}
	m.setProgress(status.Progress)
	return m.journal.writeStatus(status)
}

func (m *Migrator) setProgress(progress Progress) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.progress = progress
}

func waitTick(ctx context.Context, ticks <-chan time.Time) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ticks:
		return nil
	}
}
