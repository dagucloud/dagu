// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package calendar stores business calendar files in a directory.
package calendar

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

var _ persis.CalendarStore = (*Store)(nil)

const (
	dirPermissions  = 0750
	filePermissions = 0600
)

// Store keeps each calendar as the file <name>.yaml in one directory. A
// calendar file that is a symbolic link stays one: updates write to its
// target.
type Store struct {
	dir      string
	mu       sync.Mutex
	onChange func(context.Context)
}

// Option configures a Store.
type Option func(*Store)

// WithOnChange registers a function called after every successful write.
func WithOnChange(fn func(context.Context)) Option {
	return func(s *Store) {
		s.onChange = fn
	}
}

// New returns a store for the calendar files in dir, which is created on
// the first write.
func New(dir string, opts ...Option) (*Store, error) {
	if dir == "" {
		return nil, errors.New("calendar store: directory is not set")
	}
	s := &Store{dir: dir}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Get returns the content of the named calendar file.
func (s *Store) Get(_ context.Context, name string) ([]byte, error) {
	path, err := s.path(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is a validated calendar name in the store's directory.
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", persis.ErrCalendarNotFound, name)
	}
	return data, err
}

// Create writes a new calendar file.
func (s *Store) Create(ctx context.Context, name string, data []byte) error {
	path, err := s.path(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, dirPermissions); err != nil {
		return fmt.Errorf("create calendars directory: %w", err)
	}
	if err := fileutil.WriteFileAtomicExclusive(path, data, filePermissions); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%w: %s", persis.ErrCalendarAlreadyExists, name)
		}
		return fmt.Errorf("write calendar %s: %w", name, err)
	}
	s.changed(ctx)
	return nil
}

// Update replaces the content of a calendar file.
func (s *Store) Update(ctx context.Context, name string, data []byte) error {
	path, err := s.path(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	target, err := filepath.EvalSymlinks(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %s", persis.ErrCalendarNotFound, name)
	}
	if err != nil {
		return fmt.Errorf("locate calendar %s: %w", name, err)
	}
	if err := fileutil.WriteFileAtomic(target, data, filePermissions); err != nil {
		return fmt.Errorf("write calendar %s: %w", name, err)
	}
	s.changed(ctx)
	return nil
}

// Delete removes a calendar file.
func (s *Store) Delete(ctx context.Context, name string) error {
	path, err := s.path(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fileutil.RemoveFileDurable(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s", persis.ErrCalendarNotFound, name)
		}
		return fmt.Errorf("delete calendar %s: %w", name, err)
	}
	s.changed(ctx)
	return nil
}

// path returns the file of calendar name, which the name pattern keeps in
// the store's directory.
func (s *Store) path(name string) (string, error) {
	if err := ir.ValidateCalendarName(name); err != nil {
		return "", err
	}
	return filepath.Join(s.dir, name+spec.CalendarFileExtension), nil
}

func (s *Store) changed(ctx context.Context) {
	if s.onChange != nil {
		s.onChange(ctx)
	}
}
