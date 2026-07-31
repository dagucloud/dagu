// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"errors"
	"testing"
	"time"

	apigen "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/service/eventstore"
	frontendapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListEventLogsBuildsFilterAndMapsResponse(t *testing.T) {
	t.Parallel()

	startTime := time.Date(2026, 3, 28, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC)
	store := &mockEventStore{
		result: &eventstore.QueryResult{
			Entries: []*eventstore.Event{
				{
					ID:             "evt-1",
					SchemaVersion:  eventstore.SchemaVersion,
					OccurredAt:     startTime.Add(2 * time.Hour),
					RecordedAt:     startTime.Add(2*time.Hour + time.Minute),
					Kind:           eventstore.KindDAGRun,
					Type:           eventstore.TypeDAGRunFailed,
					SourceService:  eventstore.SourceServiceScheduler,
					SourceInstance: "sched-1",
					DAGName:        "example",
					DAGRunID:       "run-1",
					AttemptID:      "attempt-1",
					UserID:         "user-1",
					Model:          "gpt-test",
					Status:         "failed",
					Data: map[string]any{
						"reason": "boom",
					},
				},
			},
			NextCursor: "cursor-1",
		},
	}

	api := frontendapi.New(
		nil, nil, nil, nil, runtime.Manager{},
		&config.Config{}, nil, nil,
		prometheus.NewRegistry(),
		nil,
		frontendapi.WithEventService(eventstore.New(store)),
	)

	kind := "dag_run"
	eventType := "dag.run.failed"
	dagName := "example"
	dagRunID := "run-1"
	attemptID := "attempt-1"
	sessionID := "session-1"
	userID := "user-1"
	model := "gpt-test"
	limit := 700
	cursor := "cursor-0"
	paginationMode := apigen.ListEventLogsParamsPaginationMode("cursor")

	resp, err := api.ListEventLogs(context.Background(), apigen.ListEventLogsRequestObject{
		Params: apigen.ListEventLogsParams{
			Kind:           &kind,
			Type:           &eventType,
			DagName:        &dagName,
			DagRunId:       &dagRunID,
			AttemptId:      &attemptID,
			SessionId:      &sessionID,
			UserId:         &userID,
			Model:          &model,
			StartTime:      &startTime,
			EndTime:        &endTime,
			Limit:          &limit,
			Cursor:         &cursor,
			PaginationMode: &paginationMode,
		},
	})
	require.NoError(t, err)

	require.Equal(t, eventstore.QueryFilter{
		Kind:           eventstore.KindDAGRun,
		Type:           eventstore.TypeDAGRunFailed,
		DAGName:        dagName,
		DAGRunID:       dagRunID,
		AttemptID:      attemptID,
		SessionID:      sessionID,
		UserID:         userID,
		Model:          model,
		StartTime:      startTime,
		EndTime:        endTime,
		Limit:          500,
		Cursor:         cursor,
		PaginationMode: eventstore.QueryPaginationModeCursor,
	}, store.lastFilter)

	okResp, ok := resp.(apigen.ListEventLogs200JSONResponse)
	require.True(t, ok)
	require.Len(t, okResp.Entries, 1)
	require.NotNil(t, okResp.NextCursor)
	assert.Equal(t, "cursor-1", *okResp.NextCursor)
	assert.Equal(t, "evt-1", okResp.Entries[0].Id)
	assert.Equal(t, "dag.run.failed", okResp.Entries[0].Type)
	require.NotNil(t, okResp.Entries[0].Data)
	assert.Equal(t, "boom", (*okResp.Entries[0].Data)["reason"])
	assert.Nil(t, okResp.Total)
}

func TestListEventLogsSupportsOffsetCompatibilityPagination(t *testing.T) {
	t.Parallel()

	total := 23
	store := &mockEventStore{
		result: &eventstore.QueryResult{
			Entries: []*eventstore.Event{},
			Total:   &total,
		},
	}

	api := frontendapi.New(
		nil, nil, nil, nil, runtime.Manager{},
		&config.Config{}, nil, nil,
		prometheus.NewRegistry(),
		nil,
		frontendapi.WithEventService(eventstore.New(store)),
	)

	offset := 10
	resp, err := api.ListEventLogs(context.Background(), apigen.ListEventLogsRequestObject{
		Params: apigen.ListEventLogsParams{
			Offset: &offset,
		},
	})
	require.NoError(t, err)

	assert.Equal(t, eventstore.QueryPaginationModeOffset, store.lastFilter.PaginationMode)
	assert.Equal(t, offset, store.lastFilter.Offset)

	okResp, ok := resp.(apigen.ListEventLogs200JSONResponse)
	require.True(t, ok)
	require.NotNil(t, okResp.Total)
	assert.Equal(t, total, *okResp.Total)
}

func TestListEventLogsReturnsServiceUnavailableWithoutStore(t *testing.T) {
	t.Parallel()

	api := frontendapi.New(
		nil, nil, nil, nil, runtime.Manager{},
		&config.Config{}, nil, nil,
		prometheus.NewRegistry(),
		nil,
	)

	resp, err := api.ListEventLogs(context.Background(), apigen.ListEventLogsRequestObject{})
	require.Nil(t, resp)

	errResp, ok := err.(*frontendapi.Error)
	require.True(t, ok)
	assert.Equal(t, 503, errResp.HTTPStatus)
	assert.Equal(t, "Event logging is not configured", errResp.Message)
}

func TestListEventLogsHandlesNilResultsAndEntries(t *testing.T) {
	t.Parallel()

	api := frontendapi.New(
		nil, nil, nil, nil, runtime.Manager{},
		&config.Config{}, nil, nil,
		prometheus.NewRegistry(),
		nil,
		frontendapi.WithEventService(eventstore.New(&mockEventStore{
			result: &eventstore.QueryResult{
				Entries: []*eventstore.Event{nil},
			},
		})),
	)

	resp, err := api.ListEventLogs(context.Background(), apigen.ListEventLogsRequestObject{})
	require.NoError(t, err)

	okResp, ok := resp.(apigen.ListEventLogs200JSONResponse)
	require.True(t, ok)
	assert.Empty(t, okResp.Entries)
	assert.Nil(t, okResp.NextCursor)

	nilStoreAPI := frontendapi.New(
		nil, nil, nil, nil, runtime.Manager{},
		&config.Config{}, nil, nil,
		prometheus.NewRegistry(),
		nil,
		frontendapi.WithEventService(eventstore.New(&mockEventStore{})),
	)

	resp, err = nilStoreAPI.ListEventLogs(context.Background(), apigen.ListEventLogsRequestObject{})
	require.NoError(t, err)

	okResp, ok = resp.(apigen.ListEventLogs200JSONResponse)
	require.True(t, ok)
	assert.Empty(t, okResp.Entries)
	assert.Nil(t, okResp.NextCursor)
}

func TestListEventLogsReturnsBadRequestForInvalidCursor(t *testing.T) {
	t.Parallel()

	api := frontendapi.New(
		nil, nil, nil, nil, runtime.Manager{},
		&config.Config{}, nil, nil,
		prometheus.NewRegistry(),
		nil,
		frontendapi.WithEventService(eventstore.New(&mockEventStore{
			err: errors.New("boom"),
		})),
	)

	apiWithInvalidCursor := frontendapi.New(
		nil, nil, nil, nil, runtime.Manager{},
		&config.Config{}, nil, nil,
		prometheus.NewRegistry(),
		nil,
		frontendapi.WithEventService(eventstore.New(&mockEventStore{
			err: eventstore.ErrInvalidQueryCursor,
		})),
	)

	resp, err := apiWithInvalidCursor.ListEventLogs(context.Background(), apigen.ListEventLogsRequestObject{})
	require.Nil(t, resp)

	apiErr, ok := err.(*frontendapi.Error)
	require.True(t, ok)
	assert.Equal(t, 400, apiErr.HTTPStatus)
	assert.Equal(t, apigen.ErrorCodeBadRequest, apiErr.Code)

	resp, err = api.ListEventLogs(context.Background(), apigen.ListEventLogsRequestObject{})
	require.Nil(t, resp)
	apiErr, ok = err.(*frontendapi.Error)
	require.True(t, ok)
	assert.Equal(t, 500, apiErr.HTTPStatus)
}

func TestListEventLogsRejectsOffsetWithCursorPagination(t *testing.T) {
	t.Parallel()

	api := frontendapi.New(
		nil, nil, nil, nil, runtime.Manager{},
		&config.Config{}, nil, nil,
		prometheus.NewRegistry(),
		nil,
		frontendapi.WithEventService(eventstore.New(&mockEventStore{})),
	)

	offset := 0
	mode := apigen.ListEventLogsParamsPaginationMode("cursor")
	resp, err := api.ListEventLogs(context.Background(), apigen.ListEventLogsRequestObject{
		Params: apigen.ListEventLogsParams{
			Offset:         &offset,
			PaginationMode: &mode,
		},
	})
	require.Nil(t, resp)

	apiErr, ok := err.(*frontendapi.Error)
	require.True(t, ok)
	assert.Equal(t, 400, apiErr.HTTPStatus)
	assert.Equal(t, apigen.ErrorCodeBadRequest, apiErr.Code)
}

type mockEventStore struct {
	lastFilter eventstore.QueryFilter
	result     *eventstore.QueryResult
	err        error
}

func (m *mockEventStore) Emit(context.Context, *eventstore.Event) error {
	return nil
}

func (m *mockEventStore) Query(_ context.Context, filter eventstore.QueryFilter) (*eventstore.QueryResult, error) {
	m.lastFilter = filter
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}
