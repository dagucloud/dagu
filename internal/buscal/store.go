// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package buscal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/core"
)

// ErrNotFound indicates that no calendar definition exists for the requested name.
var ErrNotFound = errors.New("calendar not found")

// calendarExtensions are the recognized calendar definition file extensions,
// in lookup order.
var calendarExtensions = []string{".yaml", ".yml"}

// Store loads calendar definitions from a directory. Definitions are cached
// and reloaded when the underlying file changes.
type Store struct {
	dir string

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	calendar *Calendar
	modTime  time.Time
	size     int64
	path     string
}

// NewStore creates a calendar store backed by dir. The directory does not
// need to exist; lookups then report ErrNotFound.
func NewStore(dir string) *Store {
	return &Store{dir: dir, cache: map[string]cacheEntry{}}
}

// Load returns the calendar with the given name, reading
// {dir}/{name}.yaml (or .yml). It returns ErrNotFound when no definition
// file exists.
func (s *Store) Load(name string) (*Calendar, error) {
	if !core.ValidCalendarName(name) {
		return nil, fmt.Errorf("invalid calendar name %q", name)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	path, info, err := s.stat(name)
	if err != nil {
		return nil, err
	}

	if entry, ok := s.cache[name]; ok &&
		entry.path == path && entry.modTime.Equal(info.ModTime()) && entry.size == info.Size() {
		return entry.calendar, nil
	}

	data, err := os.ReadFile(path) //nolint:gosec // path is derived from a validated name under the configured calendars dir
	if err != nil {
		return nil, fmt.Errorf("failed to read calendar %q: %w", name, err)
	}
	calendar, err := Parse(name, data)
	if err != nil {
		return nil, err
	}

	s.cache[name] = cacheEntry{
		calendar: calendar,
		modTime:  info.ModTime(),
		size:     info.Size(),
		path:     path,
	}
	return calendar, nil
}

// stat finds the definition file for name and returns its path and file info.
func (s *Store) stat(name string) (string, os.FileInfo, error) {
	for _, ext := range calendarExtensions {
		path := filepath.Join(s.dir, name+ext)
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() {
			return path, info, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return "", nil, fmt.Errorf("failed to stat calendar %q: %w", name, err)
		}
	}
	return "", nil, fmt.Errorf("%w: %q (searched %s)", ErrNotFound, name, s.dir)
}
