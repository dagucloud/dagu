// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusQuery(t *testing.T) {
	from := dagrun.NewUTC(time.Now().Add(-24 * time.Hour))
	to := dagrun.NewUTC(time.Now())
	statuses := []ir.Status{ir.Succeeded, ir.Failed}

	opts := dagrun.StatusQuery{}

	// Apply options
	dagrun.WithFrom(from)(&opts)
	dagrun.WithTo(to)(&opts)
	dagrun.WithStatuses(statuses)(&opts)
	dagrun.WithExactName("test-dag")(&opts)
	dagrun.WithName("partial-name")(&opts)
	dagrun.WithDAGRunID("run-123")(&opts)
	dagrun.WithAllHistory()(&opts)

	// Verify options were set correctly
	assert.Equal(t, from, opts.From)
	assert.Equal(t, to, opts.To)
	assert.Equal(t, statuses, opts.Statuses)
	assert.Equal(t, "test-dag", opts.ExactName)
	assert.Equal(t, "partial-name", opts.Name)
	assert.Equal(t, "run-123", opts.DAGRunID)
	assert.True(t, opts.AllHistory)
}

func TestCreateAttemptOptions(t *testing.T) {
	rootDAGRun := &ir.DAGRunRef{
		Name: "root-dag",
		ID:   "root-run-123",
	}

	opts := dagrun.CreateAttemptOptions{
		RootDAGRun: rootDAGRun,
		Retry:      true,
	}

	assert.Equal(t, rootDAGRun, opts.RootDAGRun)
	assert.True(t, opts.Retry)
}

func TestRepositoryNormalizesStatusQueries(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 12, 15, 4, 5, 0, time.UTC)
	store := &recordingStore{}
	repository := dagrun.NewRepository(store, dagrun.RepositoryOptions{
		Now: func() time.Time { return now },
	})

	_, err := repository.ListStatuses(context.Background(), dagrun.WithLimit(2000))
	require.NoError(t, err)
	assert.Equal(t, dagrun.NewUTC(now.Truncate(24*time.Hour)), store.statusQuery.From)
	assert.Equal(t, 1000, store.statusQuery.Limit)

	_, err = repository.ListStatuses(context.Background(), dagrun.WithAllHistory(), dagrun.WithoutLimit())
	require.NoError(t, err)
	assert.True(t, store.statusQuery.From.IsZero())
	assert.True(t, store.statusQuery.Unlimited)
}

func TestRepositoryNormalizesLatestAndRetentionRequests(t *testing.T) {
	t.Parallel()

	location, err := time.LoadLocation("Asia/Tokyo")
	require.NoError(t, err)
	now := time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC)
	store := &recordingStore{}
	repository := dagrun.NewRepository(store, dagrun.RepositoryOptions{
		LatestStatusToday: true,
		Location:          location,
		Now:               func() time.Time { return now },
	})

	_, err = repository.LatestAttempt(context.Background(), "daily")
	require.NoError(t, err)
	assert.Equal(t, "daily", store.latestQuery.Name)
	assert.Equal(t, dagrun.NewUTC(time.Date(2026, 8, 12, 0, 0, 0, 0, location)), store.latestQuery.NotBefore)

	_, err = repository.RemoveOldDAGRuns(context.Background(), "daily", 7)
	require.NoError(t, err)
	assert.Equal(t, "daily", store.retentionRequest.Name)
	assert.Equal(t, dagrun.NewUTC(now.AddDate(0, 0, -7)), store.retentionRequest.OlderThan)

	_, err = repository.RemoveOldDAGRuns(context.Background(), "daily", 0, dagrun.WithRetentionRuns(3))
	require.NoError(t, err)
	assert.Equal(t, 3, store.retentionRequest.KeepRuns)
	assert.True(t, store.retentionRequest.OlderThan.IsZero())
}

func TestRepositoryCreatesChildAttemptThroughStore(t *testing.T) {
	t.Parallel()

	store := &recordingStore{}
	repository := dagrun.NewRepository(store, dagrun.RepositoryOptions{})
	dag := &ir.DAG{Name: "child"}
	root := ir.NewDAGRunRef("root", "root-run")
	timestamp := time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC)

	attempt, err := repository.CreateAttempt(context.Background(), dag, timestamp, "child-run", dagrun.CreateAttemptOptions{
		RootDAGRun: &root,
		Retry:      true,
		AttemptID:  "attempt-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "attempt-1", attempt.ID())
	assert.Same(t, dag, store.createRequest.DAG)
	assert.Equal(t, timestamp, store.createRequest.Timestamp)
	assert.Equal(t, "child-run", store.createRequest.DAGRunID)
	assert.Equal(t, root, *store.createRequest.RootDAGRun)
	assert.True(t, store.createRequest.Retry)
	assert.Equal(t, "attempt-1", store.createRequest.AttemptID)

	_, err = repository.CreateAttempt(context.Background(), dag, timestamp, "child-run", dagrun.CreateAttemptOptions{
		RootDAGRun: &ir.DAGRunRef{Name: "root"},
	})
	require.ErrorIs(t, err, dagrun.ErrDAGRunIDEmpty)
}

func TestNormalizeStateValueCompactsJSON(t *testing.T) {
	value, err := dagrun.NormalizeStateValue([]byte(`{ "b": 2, "a": 1 }`))
	require.NoError(t, err)
	assert.Equal(t, `{"a":1,"b":2}`, string(value))

	_, err = dagrun.NormalizeStateValue([]byte(`{`))
	require.ErrorIs(t, err, dagrun.ErrInvalidStateValue)
}

func TestNormalizeStateValueRejectsNormalizedValueOverLimit(t *testing.T) {
	raw := []byte(`"` + strings.Repeat("<", dagrun.MaxStateValueBytes/6+1) + `"`)
	assert.Less(t, len(raw), dagrun.MaxStateValueBytes)

	_, err := dagrun.NormalizeStateValue(raw)
	require.ErrorIs(t, err, dagrun.ErrStateValueTooLarge)
}

func TestNormalizeStateValuePreservesNumericPrecision(t *testing.T) {
	value, err := dagrun.NormalizeStateValue([]byte(`{"id":9007199254740993,"decimal":1.2300}`))
	require.NoError(t, err)
	assert.Equal(t, `{"decimal":1.2300,"id":9007199254740993}`, string(value))
}

type recordingStore struct {
	dagrun.Store
	createRequest    dagrun.CreateAttemptRequest
	latestQuery      dagrun.LatestAttemptQuery
	statusQuery      dagrun.StatusQuery
	retentionRequest dagrun.RetentionRequest
}

func (s *recordingStore) CreateAttempt(_ context.Context, req dagrun.CreateAttemptRequest) (dagrun.Attempt, error) {
	s.createRequest = req
	return dagrun.NewNoopAttempt(req.AttemptID, req.DAG), nil
}

func (s *recordingStore) LatestAttempt(_ context.Context, query dagrun.LatestAttemptQuery) (dagrun.Attempt, error) {
	s.latestQuery = query
	return dagrun.NewNoopAttempt("latest", nil), nil
}

func (s *recordingStore) QueryStatuses(_ context.Context, query dagrun.StatusQuery) (dagrun.DAGRunStatusPage, error) {
	s.statusQuery = query
	return dagrun.DAGRunStatusPage{}, nil
}

func (s *recordingStore) RemoveOldDAGRuns(_ context.Context, req dagrun.RetentionRequest) ([]string, error) {
	s.retentionRequest = req
	return nil, nil
}
