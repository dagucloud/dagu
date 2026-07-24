// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type retryCandidateDAGRunStore struct {
	exec.DAGRunStore

	candidateCalls int
	candidateFrom  exec.TimeInUTC
	listCalls      int
	listOptions    exec.ListDAGRunStatusesOptions
}

func (s *retryCandidateDAGRunStore) ListRetryCandidates(_ context.Context, from exec.TimeInUTC) ([]*exec.DAGRunStatus, error) {
	s.candidateCalls++
	s.candidateFrom = from
	return nil, nil
}

func (s *retryCandidateDAGRunStore) ListStatuses(_ context.Context, opts ...exec.ListDAGRunStatusesOption) ([]*exec.DAGRunStatus, error) {
	s.listCalls++
	for _, opt := range opts {
		opt(&s.listOptions)
	}
	return nil, nil
}

type fallbackRetryDAGRunStore struct {
	exec.DAGRunStore

	listCalls   int
	listOptions exec.ListDAGRunStatusesOptions
}

func (s *fallbackRetryDAGRunStore) ListStatuses(_ context.Context, opts ...exec.ListDAGRunStatusesOption) ([]*exec.DAGRunStatus, error) {
	s.listCalls++
	for _, opt := range opts {
		opt(&s.listOptions)
	}
	return nil, nil
}

func TestRetryScannerUsesRetryCandidateListerWhenAvailable(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	store := &retryCandidateDAGRunStore{}
	scanner, err := scheduler.NewRetryScanner(
		store,
		nil,
		nil,
		time.Hour,
		func() time.Time { return now },
	)
	require.NoError(t, err)

	require.NoError(t, scanner.ScanForTest(context.Background()))

	assert.Equal(t, 1, store.candidateCalls)
	assert.Equal(t, now.Add(-time.Hour), store.candidateFrom.Time)
	assert.Equal(t, 0, store.listCalls)
}

func TestRetryScannerFallsBackToStatusListingWithoutCandidateLister(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	store := &fallbackRetryDAGRunStore{}
	scanner, err := scheduler.NewRetryScanner(
		store,
		nil,
		nil,
		time.Hour,
		func() time.Time { return now },
	)
	require.NoError(t, err)

	require.NoError(t, scanner.ScanForTest(context.Background()))

	assert.Equal(t, 1, store.listCalls)
	assert.Equal(t, now.Add(-time.Hour), store.listOptions.From.Time)
	assert.Equal(t, []core.Status{core.Failed}, store.listOptions.Statuses)
	assert.True(t, store.listOptions.Unlimited)
}
