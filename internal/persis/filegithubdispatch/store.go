// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package filegithubdispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/githubdispatch"
)

const (
	trackerFile = "tracked.json"
	dirPerm     = 0700
	filePerm    = 0600
)

type TrackedJob = githubdispatch.TrackedJob

type Store struct {
	dir string
	mu  sync.RWMutex
}

func New(dir string) *Store {
	return &Store{dir: dir}
}

func (s *Store) Upsert(ctx context.Context, job TrackedJob) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	jobs, err := s.loadLocked()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	jobs[job.JobID] = job
	return s.saveLocked(jobs)
}

func (s *Store) Delete(ctx context.Context, jobID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	jobs, err := s.loadLocked()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	delete(jobs, jobID)
	return s.saveLocked(jobs)
}

func (s *Store) List(ctx context.Context) ([]TrackedJob, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	jobs, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]TrackedJob, 0, len(jobs))
	for _, job := range jobs {
		out = append(out, job)
	}
	slices.SortFunc(out, func(a, b TrackedJob) int {
		if a.JobID < b.JobID {
			return -1
		}
		if a.JobID > b.JobID {
			return 1
		}
		return 0
	})
	return out, nil
}

func (s *Store) loadLocked() (map[string]TrackedJob, error) {
	path := filepath.Join(s.dir, trackerFile)
	data, err := fileutil.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]TrackedJob{}, nil
		}
		return nil, fmt.Errorf("read tracked jobs: %w", err)
	}
	var jobs map[string]TrackedJob
	if err := json.Unmarshal(data, &jobs); err != nil {
		return nil, fmt.Errorf("unmarshal tracked jobs: %w", err)
	}
	if jobs == nil {
		jobs = map[string]TrackedJob{}
	}
	return jobs, nil
}

func (s *Store) saveLocked(jobs map[string]TrackedJob) error {
	if err := os.MkdirAll(s.dir, dirPerm); err != nil {
		return fmt.Errorf("create tracker dir: %w", err)
	}

	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tracked jobs: %w", err)
	}

	tmp, err := os.CreateTemp(s.dir, ".tracked-*.tmp")
	if err != nil {
		return fmt.Errorf("create tracker temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = fileutil.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write tracker temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync tracker temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tracker temp file: %w", err)
	}
	if err := os.Chmod(tmpName, filePerm); err != nil {
		return fmt.Errorf("chmod tracker temp file: %w", err)
	}
	finalPath := filepath.Join(s.dir, trackerFile)
	if err := fileutil.ReplaceFile(tmpName, finalPath); err != nil {
		return fmt.Errorf("rename tracker temp file: %w", err)
	}
	if err := syncTrackerDir(s.dir); err != nil {
		return err
	}
	return nil
}
