// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package slotindex stores the slot index in files: one directory per DAG
// and one JSON Lines file per UTC day of slots.
//
//	<dir>/<hex DAG name>/
//	├── 2026-10-02.jsonl   events of slots on that day, in recorded order
//	├── 2026-10-03.jsonl
//	└── .imported          the DAG's run history has been added
package slotindex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

const (
	dayLayout      = "2006-01-02"
	dayFileSuffix  = ".jsonl"
	importedMarker = ".imported"
	dirPermissions = 0o750
	filePerms      = 0o600
	// lockStaleThreshold frees the lock of a process that died holding it.
	lockStaleThreshold = 30 * time.Second
	lockRetryInterval  = 20 * time.Millisecond
)

// Store is a file-backed persis.SlotIndex. Writers in several processes are
// serialized by a lock per DAG directory.
type Store struct {
	dir string
}

var _ persis.SlotIndex = (*Store)(nil)

// New returns a store rooted at dir.
func New(dir string) *Store {
	return &Store{dir: dir}
}

// Record appends an event to the file of its slot's day.
func (s *Store) Record(ctx context.Context, event persis.SlotEvent) error {
	if event.DAGName == "" {
		return errors.New("slot index: DAG name is empty")
	}
	if event.Slot.IsZero() {
		return errors.New("slot index: slot is zero")
	}
	event.Slot = event.Slot.UTC()
	if event.RecordedAt.IsZero() {
		event.RecordedAt = time.Now().UTC()
	}
	line, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("slot index: encode event: %w", err)
	}
	line = append(line, '\n')
	path := filepath.Join(s.dagDir(event.DAGName), event.Slot.Format(dayLayout)+dayFileSuffix)
	return s.withDAGLock(ctx, event.DAGName, func() error {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, filePerms) //nolint:gosec // path is built from the store's own layout
		if err != nil {
			return fmt.Errorf("slot index: open %s: %w", path, err)
		}
		if _, err := f.Write(line); err != nil {
			_ = f.Close()
			return fmt.Errorf("slot index: append to %s: %w", path, err)
		}
		return f.Close()
	})
}

// Events returns the events of a DAG whose slots fall in [from, to].
func (s *Store) Events(_ context.Context, dagName string, from, to time.Time) ([]persis.SlotEvent, error) {
	from, to = from.UTC(), to.UTC()
	days, err := s.dayFiles(dagName)
	if err != nil {
		return nil, err
	}
	first, last := from.Format(dayLayout), to.Format(dayLayout)
	var events []persis.SlotEvent
	for _, day := range days {
		if day < first || day > last {
			continue
		}
		dayEvents, err := s.readDay(dagName, day)
		if err != nil {
			return nil, err
		}
		for _, event := range dayEvents {
			if !event.Slot.Before(from) && !event.Slot.After(to) {
				events = append(events, event)
			}
		}
	}
	return events, nil
}

// Prune removes whole days of events before the day of cutoff.
func (s *Store) Prune(ctx context.Context, dagName string, cutoff time.Time) error {
	days, err := s.dayFiles(dagName)
	if err != nil || len(days) == 0 {
		return err
	}
	keepFrom := cutoff.UTC().Format(dayLayout)
	return s.withDAGLock(ctx, dagName, func() error {
		for _, day := range days {
			if day >= keepFrom {
				break
			}
			path := filepath.Join(s.dagDir(dagName), day+dayFileSuffix)
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("slot index: remove %s: %w", path, err)
			}
		}
		return nil
	})
}

// Imported reports whether the import marker of a DAG exists.
func (s *Store) Imported(_ context.Context, dagName string) (bool, error) {
	_, err := os.Stat(filepath.Join(s.dagDir(dagName), importedMarker))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("slot index: stat import marker: %w", err)
	}
}

// MarkImported writes the import marker of a DAG.
func (s *Store) MarkImported(_ context.Context, dagName string) error {
	dir := s.dagDir(dagName)
	if err := os.MkdirAll(dir, dirPermissions); err != nil {
		return fmt.Errorf("slot index: create %s: %w", dir, err)
	}
	return fileutil.WriteFileAtomic(filepath.Join(dir, importedMarker), nil, filePerms)
}

func (s *Store) dagDir(dagName string) string {
	return filepath.Join(s.dir, hex.EncodeToString([]byte(dagName)))
}

// dayFiles returns the days that have an event file, earliest first.
func (s *Store) dayFiles(dagName string) ([]string, error) {
	entries, err := os.ReadDir(s.dagDir(dagName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("slot index: read %s: %w", s.dagDir(dagName), err)
	}
	var days []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, dayFileSuffix) {
			continue
		}
		day := strings.TrimSuffix(name, dayFileSuffix)
		if _, err := time.Parse(dayLayout, day); err == nil {
			days = append(days, day)
		}
	}
	sort.Strings(days)
	return days, nil
}

// readDay decodes the events of one day. A line cut short by a crash during
// an append is skipped; every complete line before and after it is kept.
func (s *Store) readDay(dagName, day string) ([]persis.SlotEvent, error) {
	path := filepath.Join(s.dagDir(dagName), day+dayFileSuffix)
	data, err := os.ReadFile(path) //nolint:gosec // path is built from the store's own layout
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("slot index: read %s: %w", path, err)
	}
	var events []persis.SlotEvent
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var event persis.SlotEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("slot index: scan %s: %w", path, err)
	}
	return events, nil
}

func (s *Store) withDAGLock(ctx context.Context, dagName string, fn func() error) error {
	dir := s.dagDir(dagName)
	if err := os.MkdirAll(dir, dirPermissions); err != nil {
		return fmt.Errorf("slot index: create %s: %w", dir, err)
	}
	lock := dirlock.New(dir, &dirlock.LockOptions{
		StaleThreshold: lockStaleThreshold,
		RetryInterval:  lockRetryInterval,
	})
	if err := lock.Lock(ctx); err != nil {
		return fmt.Errorf("slot index: lock %s: %w", dir, err)
	}
	defer func() { _ = lock.Unlock() }()
	return fn()
}
