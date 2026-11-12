// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package backfill stores backfill records in files.
//
//	<dir>/
//	├── <backfill id>.json   one record per backfill
//	└── .locks/<hex DAG name>/   the submit lock of a DAG
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
	locksDirName   = ".locks"
	dirPermissions = 0o750
	filePerms      = 0o600
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

// List reads the records of a DAG, or of every DAG, oldest first.
func (s *Store) List(_ context.Context, dagName string) ([]*backfill.Record, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("backfill store: read %s: %w", s.dir, err)
	}
	var records []*backfill.Record
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), recordSuffix) {
			continue
		}
		record, err := s.read(filepath.Join(s.dir, entry.Name()))
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

// LockDAG holds the submit lock of a DAG.
func (s *Store) LockDAG(ctx context.Context, dagName string) (func(), error) {
	dir := filepath.Join(s.dir, locksDirName, hex.EncodeToString([]byte(dagName)))
	if err := os.MkdirAll(dir, dirPermissions); err != nil {
		return nil, fmt.Errorf("backfill store: create %s: %w", dir, err)
	}
	lock := dirlock.New(dir, &dirlock.LockOptions{
		StaleThreshold: lockStaleThreshold,
		RetryInterval:  lockRetryInterval,
	})
	if err := lock.Lock(ctx); err != nil {
		return nil, fmt.Errorf("backfill store: lock %s: %w", dagName, err)
	}
	return func() { _ = lock.Unlock() }, nil
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
