// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package backfill stores backfill records in files.
//
//	<dir>/
//	├── <backfill id>.json             one record per backfill
//	├── <backfill id>.progress.jsonl   its slots' progress, appended
//	├── <backfill id>.open             present while the backfill is open
//	├── .dags/<hex DAG name>/<id>      names each backfill of a DAG
//	└── .locks/
//	    ├── <hex DAG name>/            the submit lock of a DAG
//	    └── record-<backfill id>/      the lock of one backfill
package backfill

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	recordSuffix   = ".json"
	progressSuffix = ".progress.jsonl"
	openSuffix     = ".open"
	dagsDirName    = ".dags"
	locksDirName   = ".locks"
	// recordLockPrefix keeps the lock of a backfill apart from the hex
	// names of the DAG locks.
	recordLockPrefix = "record-"
	dirPermissions   = 0o750
	filePerms        = 0o600
	// lockStaleThreshold frees the lock of a process that died holding it.
	lockStaleThreshold = 30 * time.Second
	lockRetryInterval  = 50 * time.Millisecond
)

// Dir returns the directory of backfill records under a data directory.
func Dir(dataDir string) string {
	return filepath.Join(dataDir, "backfills")
}

// Store is a file-backed backfill.Store.
type Store struct {
	dir string
}

var _ backfill.Store = (*Store)(nil)

// New returns a store rooted at dir.
func New(dir string) *Store {
	return &Store{dir: dir}
}

// Create writes a new record. It fails with backfill.ErrExists when the ID
// is taken.
func (s *Store) Create(_ context.Context, record *backfill.Record) error {
	if record.ID == "" || strings.ContainsAny(record.ID, `/\.`) {
		return fmt.Errorf("backfill store: invalid ID %q", record.ID)
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("backfill store: encode %s: %w", record.ID, err)
	}
	if err := os.MkdirAll(s.dir, dirPermissions); err != nil {
		return fmt.Errorf("backfill store: create %s: %w", s.dir, err)
	}
	// The markers come first: one left by a crash names no record and is
	// passed over, while a record without its markers would never run or be
	// listed with its DAG.
	if record.Open() {
		if err := fileutil.WriteFileAtomic(s.openPath(record.ID), nil, filePerms); err != nil {
			return fmt.Errorf("backfill store: mark %s open: %w", record.ID, err)
		}
	}
	if err := os.MkdirAll(s.dagDir(record.DAGName), dirPermissions); err != nil {
		return fmt.Errorf("backfill store: create %s: %w", s.dagDir(record.DAGName), err)
	}
	if err := fileutil.WriteFileAtomic(filepath.Join(s.dagDir(record.DAGName), record.ID), nil, filePerms); err != nil {
		return fmt.Errorf("backfill store: list %s with its DAG: %w", record.ID, err)
	}
	err = fileutil.WriteFileAtomicExclusive(s.recordPath(record.ID), data, filePerms)
	if errors.Is(err, os.ErrExist) {
		return backfill.ErrExists
	}
	if err != nil {
		return fmt.Errorf("backfill store: write %s: %w", record.ID, err)
	}
	return nil
}

// Get reads the record of a backfill.
func (s *Store) Get(_ context.Context, id string) (*backfill.Record, error) {
	if id == "" || strings.ContainsAny(id, `/\.`) {
		return nil, backfill.ErrNotFound
	}
	return s.read(s.recordPath(id))
}

// List reads the records of a DAG, or of every DAG, oldest first. The
// records of one DAG are read without reading those of the others.
func (s *Store) List(_ context.Context, dagName string) ([]*backfill.Record, error) {
	paths, err := s.recordPaths(dagName)
	if err != nil {
		return nil, err
	}
	var records []*backfill.Record
	for _, path := range paths {
		record, err := s.read(path)
		if errors.Is(err, backfill.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if dagName == "" || record.DAGName == dagName {
			records = append(records, record)
		}
	}
	slices.SortFunc(records, func(a, b *backfill.Record) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return records, nil
}

// Open reads the records that have an open marker and have not ended.
func (s *Store) Open(_ context.Context) ([]*backfill.Record, error) {
	matches, err := filepath.Glob(filepath.Join(s.dir, "*"+openSuffix))
	if err != nil {
		return nil, fmt.Errorf("backfill store: list open backfills: %w", err)
	}
	var records []*backfill.Record
	for _, marker := range matches {
		id := strings.TrimSuffix(filepath.Base(marker), openSuffix)
		record, err := s.read(s.recordPath(id))
		if errors.Is(err, backfill.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if record.Open() {
			records = append(records, record)
		}
	}
	slices.SortFunc(records, func(a, b *backfill.Record) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return records, nil
}

// LockDAG holds the submit lock of a DAG.
func (s *Store) LockDAG(ctx context.Context, dagName string) (func(), error) {
	return s.lock(ctx, filepath.Join(s.dir, locksDirName, hex.EncodeToString([]byte(dagName))), dagName)
}

// LockRecord holds the lock of one backfill.
func (s *Store) LockRecord(ctx context.Context, id string) (func(), error) {
	if id == "" || strings.ContainsAny(id, `/\.`) {
		return nil, backfill.ErrNotFound
	}
	return s.lock(ctx, filepath.Join(s.dir, locksDirName, recordLockPrefix+id), id)
}

func (s *Store) lock(ctx context.Context, dir, name string) (func(), error) {
	if err := os.MkdirAll(dir, dirPermissions); err != nil {
		return nil, fmt.Errorf("backfill store: create %s: %w", dir, err)
	}
	lock := dirlock.New(dir, &dirlock.LockOptions{
		StaleThreshold: lockStaleThreshold,
		RetryInterval:  lockRetryInterval,
	})
	if err := lock.Lock(ctx); err != nil {
		return nil, fmt.Errorf("backfill store: lock %s: %w", name, err)
	}
	return func() { _ = lock.Unlock() }, nil
}

// Append adds progress events to the backfill's progress file.
func (s *Store) Append(_ context.Context, id string, events ...backfill.Progress) error {
	if _, err := os.Stat(s.recordPath(id)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return backfill.ErrNotFound
		}
		return fmt.Errorf("backfill store: stat %s: %w", id, err)
	}
	lines := make([][]byte, 0, len(events))
	for _, event := range events {
		event.Slot, event.At = event.Slot.UTC(), event.At.UTC()
		line, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("backfill store: encode progress of %s: %w", id, err)
		}
		lines = append(lines, line)
	}
	if err := fileutil.AppendLines(s.progressPath(id), filePerms, lines...); err != nil {
		return fmt.Errorf("backfill store: append progress of %s: %w", id, err)
	}
	return nil
}

// Progress reads the backfill's progress events. A line a crash cut short
// is skipped.
func (s *Store) Progress(_ context.Context, id string) ([]backfill.Progress, error) {
	data, err := os.ReadFile(s.progressPath(id)) //nolint:gosec // path is built from the store's own layout
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("backfill store: read progress of %s: %w", id, err)
	}
	var events []backfill.Progress
	for line := range strings.SplitSeq(string(data), "\n") {
		var event backfill.Progress
		if line == "" || json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		events = append(events, event)
	}
	return events, nil
}

// End rewrites the record with how the backfill ended.
func (s *Store) End(ctx context.Context, id string, status backfill.Status, at time.Time) error {
	record, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	record.Status, record.EndedAt = status, at.UTC()
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("backfill store: encode %s: %w", id, err)
	}
	if err := fileutil.WriteFileAtomic(s.recordPath(id), data, filePerms); err != nil {
		return fmt.Errorf("backfill store: write %s: %w", id, err)
	}
	if err := os.Remove(s.openPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("backfill store: clear the open mark of %s: %w", id, err)
	}
	return nil
}

// Update rewrites a record and restores its open marker while it is open.
func (s *Store) Update(ctx context.Context, record *backfill.Record) error {
	if _, err := s.Get(ctx, record.ID); err != nil {
		return err
	}
	if record.Open() {
		if err := fileutil.WriteFileAtomic(s.openPath(record.ID), nil, filePerms); err != nil {
			return fmt.Errorf("backfill store: mark %s open: %w", record.ID, err)
		}
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("backfill store: encode %s: %w", record.ID, err)
	}
	if err := fileutil.WriteFileAtomic(s.recordPath(record.ID), data, filePerms); err != nil {
		return fmt.Errorf("backfill store: write %s: %w", record.ID, err)
	}
	return nil
}

// recordPaths returns the paths of the records of a DAG, or of every DAG
// when dagName is empty.
func (s *Store) recordPaths(dagName string) ([]string, error) {
	dir, suffix := s.dir, recordSuffix
	if dagName != "" {
		dir, suffix = s.dagDir(dagName), ""
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("backfill store: read %s: %w", dir, err)
	}
	var paths []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, suffix) {
			continue
		}
		paths = append(paths, s.recordPath(strings.TrimSuffix(name, suffix)))
	}
	return paths, nil
}

// dagDir holds a marker per backfill of a DAG.
func (s *Store) dagDir(dagName string) string {
	return filepath.Join(s.dir, dagsDirName, hex.EncodeToString([]byte(dagName)))
}

func (s *Store) openPath(id string) string {
	return filepath.Join(s.dir, id+openSuffix)
}

func (s *Store) progressPath(id string) string {
	return filepath.Join(s.dir, id+progressSuffix)
}

func (s *Store) recordPath(id string) string {
	return filepath.Join(s.dir, id+recordSuffix)
}

func (s *Store) read(path string) (*backfill.Record, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is built from the store's own layout
	if errors.Is(err, os.ErrNotExist) {
		return nil, backfill.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("backfill store: read %s: %w", path, err)
	}
	var record backfill.Record
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("backfill store: decode %s: %w", path, err)
	}
	return &record, nil
}
