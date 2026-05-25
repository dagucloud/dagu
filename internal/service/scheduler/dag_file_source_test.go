// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDAGFileSourceSnapshotRetriesTemporaryAbsence verifies transient missing files are retried before deletion.
func TestDAGFileSourceSnapshotRetriesTemporaryAbsence(t *testing.T) {
	t.Parallel()

	attempts := 0
	source := &dagFileSource{
		dir: t.TempDir(),
		load: func(context.Context, string) (*core.DAG, error) {
			attempts++
			if attempts == 1 {
				return nil, fmt.Errorf("open dag.yaml: %w", os.ErrNotExist)
			}
			return &core.DAG{Name: "replace-test"}, nil
		},
	}

	snapshot, err := source.snapshot(context.Background(), "replace-test.yaml")

	require.NoError(t, err)
	assert.True(t, snapshot.exists)
	assert.Equal(t, "replace-test", snapshot.dag.Name)
	assert.Equal(t, 2, attempts)
}

// TestDAGFileSourceSnapshotReturnsNonAbsenceError verifies parse/load errors are not treated as deletion.
func TestDAGFileSourceSnapshotReturnsNonAbsenceError(t *testing.T) {
	t.Parallel()

	loadErr := errors.New("invalid dag")
	source := &dagFileSource{
		dir: t.TempDir(),
		load: func(context.Context, string) (*core.DAG, error) {
			return nil, loadErr
		},
	}

	snapshot, err := source.snapshot(context.Background(), "invalid.yaml")

	require.ErrorIs(t, err, loadErr)
	assert.False(t, snapshot.exists)
}

// TestDAGFileSourceSnapshotHonorsContextCancellation verifies retry waits stop on cancellation.
func TestDAGFileSourceSnapshotHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	attempts := 0
	source := &dagFileSource{
		dir: t.TempDir(),
		load: func(context.Context, string) (*core.DAG, error) {
			attempts++
			return nil, os.ErrNotExist
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	snapshot, err := source.snapshot(ctx, "missing.yaml")

	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, snapshot.exists)
	assert.Equal(t, 1, attempts)
}
