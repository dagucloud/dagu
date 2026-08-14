// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type retryCandidateDAGRunStore struct {
	testutil.DAGRunStoreStub

	candidateCalls int
	candidateFrom  persis.TimeInUTC
	listCalls      int
}

func (s *retryCandidateDAGRunStore) ListRetryCandidates(_ context.Context, from persis.TimeInUTC) ([]*ir.DAGRunStatus, error) {
	s.candidateCalls++
	s.candidateFrom = from
	return nil, nil
}

func (s *retryCandidateDAGRunStore) QueryStatuses(context.Context, persis.DAGRunStatusQuery) (persis.DAGRunStatusPage, error) {
	s.listCalls++
	return persis.DAGRunStatusPage{}, nil
}

type fallbackRetryDAGRunStore struct {
	testutil.DAGRunStoreStub

	listCalls   int
	listOptions persis.DAGRunStatusQuery
}

func (s *fallbackRetryDAGRunStore) QueryStatuses(_ context.Context, query persis.DAGRunStatusQuery) (persis.DAGRunStatusPage, error) {
	s.listCalls++
	s.listOptions = query
	return persis.DAGRunStatusPage{}, nil
}

func TestRetryScannerUsesRetryCandidateListerWhenAvailable(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	store := &retryCandidateDAGRunStore{}
	scanner, err := scheduler.NewRetryScanner(
		persis.NewDAGRunRepository(store, nil, persis.DAGRunRepositoryOptions{}),
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
		persis.NewDAGRunRepository(store, nil, persis.DAGRunRepositoryOptions{}),
		nil,
		nil,
		time.Hour,
		func() time.Time { return now },
	)
	require.NoError(t, err)

	require.NoError(t, scanner.ScanForTest(context.Background()))

	assert.Equal(t, 1, store.listCalls)
	assert.Equal(t, now.Add(-time.Hour), store.listOptions.From.Time)
	assert.Equal(t, []ir.Status{ir.Failed}, store.listOptions.Statuses)
	assert.Zero(t, store.listOptions.Limit)
}
