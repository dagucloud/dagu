// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/githubdispatch"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
)

func newMemoryGitHubDispatchStore() *store.GitHubDispatchStore {
	col := testutil.NewMemoryBackend().Collection("github-dispatch")
	return store.NewGitHubDispatchStore(col)
}

func newFileGitHubDispatchStore(t *testing.T, dir string) *store.GitHubDispatchStore {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o700))
	return store.NewGitHubDispatchStore(file.NewCollection(dir, file.WithIndentedJSON()))
}

func TestGitHubDispatchStore_ListMissing_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	s := newMemoryGitHubDispatchStore()

	jobs, err := s.List(context.Background())
	require.NoError(t, err)
	assert.Empty(t, jobs)
}

func TestGitHubDispatchStore_UpsertListDelete(t *testing.T) {
	t.Parallel()
	s := newMemoryGitHubDispatchStore()
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	require.NoError(t, s.Upsert(ctx, githubdispatch.TrackedJob{
		JobID: "job-2", DAGName: "deploy.yaml", DAGRunID: "run-2", Phase: "accepted", UpdatedAt: now,
	}))
	require.NoError(t, s.Upsert(ctx, githubdispatch.TrackedJob{
		JobID: "job-1", DAGName: "ci.yaml", DAGRunID: "run-1", Phase: "pending_accept", UpdatedAt: now,
	}))
	require.NoError(t, s.Upsert(ctx, githubdispatch.TrackedJob{
		JobID: "job-1", DAGName: "ci.yaml", DAGRunID: "run-1", Phase: "accepted", UpdatedAt: now.Add(time.Minute),
	}))

	jobs, err := s.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, []githubdispatch.TrackedJob{
		{JobID: "job-1", DAGName: "ci.yaml", DAGRunID: "run-1", Phase: "accepted", UpdatedAt: now.Add(time.Minute)},
		{JobID: "job-2", DAGName: "deploy.yaml", DAGRunID: "run-2", Phase: "accepted", UpdatedAt: now},
	}, jobs)

	require.NoError(t, s.Delete(ctx, "job-1"))
	jobs, err = s.List(ctx)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	assert.Equal(t, "job-2", jobs[0].JobID)
}

func TestGitHubDispatchStore_ContextCancel(t *testing.T) {
	t.Parallel()
	s := newMemoryGitHubDispatchStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.ErrorIs(t, s.Upsert(ctx, githubdispatch.TrackedJob{JobID: "job-1"}), context.Canceled)
	jobs, err := s.List(ctx)
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, jobs)
	require.ErrorIs(t, s.Delete(ctx, "job-1"), context.Canceled)
}

// On-disk bytes equal json.MarshalIndent(map, "", "  ") at {dir}/tracked.json.
func TestGitHubDispatchStore_File_OnDiskFormatMatchesReleasedJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := newFileGitHubDispatchStore(t, dir)
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	require.NoError(t, s.Upsert(ctx, githubdispatch.TrackedJob{
		JobID: "job-1", DAGName: "ci.yaml", DAGRunID: "run-1", Phase: "accepted", UpdatedAt: now,
	}))

	got, err := os.ReadFile(filepath.Join(dir, "tracked.json"))
	require.NoError(t, err)

	expected, err := json.MarshalIndent(map[string]githubdispatch.TrackedJob{
		"job-1": {JobID: "job-1", DAGName: "ci.yaml", DAGRunID: "run-1", Phase: "accepted", UpdatedAt: now},
	}, "", "  ")
	require.NoError(t, err)
	assert.True(t, bytes.Equal(got, expected),
		"on-disk bytes must equal json.MarshalIndent output\n  got:  %q\n  want: %q",
		string(got), string(expected))
}

// Files written by older binaries decode through List.
func TestGitHubDispatchStore_File_ReadsReleasedFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(dir, 0o700))
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)

	raw, err := json.MarshalIndent(map[string]githubdispatch.TrackedJob{
		"job-1": {JobID: "job-1", DAGName: "ci.yaml", DAGRunID: "run-1", Phase: "accepted", UpdatedAt: now},
	}, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.json"), raw, 0o600))

	s := store.NewGitHubDispatchStore(file.NewCollection(dir, file.WithIndentedJSON()))
	jobs, err := s.List(context.Background())
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	assert.Equal(t, "job-1", jobs[0].JobID)
}

func TestGitHubDispatchStore_File_FilePermissions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := newFileGitHubDispatchStore(t, dir)
	require.NoError(t, s.Upsert(context.Background(), githubdispatch.TrackedJob{JobID: "job-1"}))

	info, err := os.Stat(filepath.Join(dir, "tracked.json"))
	require.NoError(t, err)

	if testutil.SupportsPOSIXPermissionBits() {
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}
