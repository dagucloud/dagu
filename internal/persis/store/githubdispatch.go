// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/dagucloud/dagu/internal/githubdispatch"
	"github.com/dagucloud/dagu/internal/persis"
)

const githubDispatchRecordID = "tracked"

var _ githubdispatch.Tracker = (*GitHubDispatchStore)(nil)

// GitHubDispatchStore implements [githubdispatch.Tracker] over a single
// [persis.Collection] record holding the entire tracked-jobs map.
type GitHubDispatchStore struct {
	rec *SingleRecord[map[string]githubdispatch.TrackedJob]
	mu  sync.Mutex
}

// NewGitHubDispatchStore creates a GitHubDispatchStore backed by col.
func NewGitHubDispatchStore(col persis.Collection) *GitHubDispatchStore {
	return &GitHubDispatchStore{
		rec: NewSingleRecord[map[string]githubdispatch.TrackedJob](col, githubDispatchRecordID),
	}
}

// Upsert inserts or replaces a tracked job.
func (s *GitHubDispatchStore) Upsert(ctx context.Context, job githubdispatch.TrackedJob) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	jobs, err := s.load(ctx)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	jobs[job.JobID] = job
	return s.save(ctx, jobs)
}

// Delete removes a tracked job. Missing jobID is a no-op.
func (s *GitHubDispatchStore) Delete(ctx context.Context, jobID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	jobs, err := s.load(ctx)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	delete(jobs, jobID)
	return s.save(ctx, jobs)
}

// List returns tracked jobs ordered by JobID.
func (s *GitHubDispatchStore) List(ctx context.Context) ([]githubdispatch.TrackedJob, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	jobs, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]githubdispatch.TrackedJob, 0, len(jobs))
	for _, job := range jobs {
		out = append(out, job)
	}
	slices.SortFunc(out, func(a, b githubdispatch.TrackedJob) int {
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

func (s *GitHubDispatchStore) load(ctx context.Context) (map[string]githubdispatch.TrackedJob, error) {
	jobs := map[string]githubdispatch.TrackedJob{}
	if _, err := s.rec.Load(ctx, &jobs); err != nil {
		return nil, fmt.Errorf("github-dispatch store: load: %w", err)
	}
	return jobs, nil
}

func (s *GitHubDispatchStore) save(ctx context.Context, jobs map[string]githubdispatch.TrackedJob) error {
	if err := s.rec.Save(ctx, &jobs); err != nil {
		return fmt.Errorf("github-dispatch store: save: %w", err)
	}
	return nil
}
