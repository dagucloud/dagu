// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sse

import (
	"context"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/service/eventstore"
	"github.com/stretchr/testify/require"
)

func TestDAGRunInvalidatorRefreshesListsOnlyForLifecycleEvents(t *testing.T) {
	tests := []struct {
		name              string
		eventType         eventstore.EventType
		currentStatus     ir.Status
		filterStatus      ir.Status
		wantListRefreshes int64
	}{
		{
			name:              "progress update",
			eventType:         eventstore.TypeDAGRunUpdated,
			currentStatus:     ir.Running,
			filterStatus:      ir.Running,
			wantListRefreshes: 0,
		},
		{
			name:              "lifecycle update",
			eventType:         eventstore.TypeDAGRunRunning,
			currentStatus:     ir.Running,
			filterStatus:      ir.Running,
			wantListRefreshes: 1,
		},
		{
			name:              "human task resumed",
			eventType:         eventstore.TypeDAGRunQueued,
			currentStatus:     ir.Queued,
			filterStatus:      ir.Waiting,
			wantListRefreshes: 1,
		},
		{
			name:              "failed auto retry canceled",
			eventType:         eventstore.TypeDAGRunAborted,
			currentStatus:     ir.Aborted,
			filterStatus:      ir.Failed,
			wantListRefreshes: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := NewMultiplexer(StreamConfig{}, nil)
			t.Cleanup(mux.Shutdown)

			var detailRefreshes atomic.Int64
			mux.RegisterFetcher(TopicTypeDAGRun, func(_ context.Context, _ string) (any, error) {
				detailRefreshes.Add(1)
				return nil, nil
			})
			mux.SetRefreshMode(TopicTypeDAGRun, TopicRefreshModeOnDemand)

			var listRefreshes atomic.Int64
			mux.RegisterFetcher(TopicTypeDAGRuns, func(_ context.Context, _ string) (any, error) {
				listRefreshes.Add(1)
				return nil, nil
			})
			mux.SetRefreshMode(TopicTypeDAGRuns, TopicRefreshModeOnDemand)

			result, err := mux.createSession(
				context.Background(),
				httptest.NewRecorder(),
				[]string{"dagrun:test/run-1", "dagruns:status=" + strconv.Itoa(int(tt.filterStatus))},
				0,
			)
			require.NoError(t, err)
			require.NotNil(t, result.session)
			defer mux.removeSession(result.session)

			status := &exec.DAGRunStatus{
				Name:      "test",
				DAGRunID:  "run-1",
				AttemptID: "attempt-1",
				Status:    tt.currentStatus,
			}
			event := eventstore.NewDAGRunEvent(
				eventstore.Source{Service: eventstore.SourceServiceServer},
				tt.eventType,
				status,
				nil,
			)

			wakeTopicsForDAGRunEvents(mux, []*eventstore.Event{event})

			require.Eventually(t, func() bool {
				return detailRefreshes.Load() == 1 && listRefreshes.Load() == tt.wantListRefreshes
			}, time.Second, 10*time.Millisecond)
			if tt.wantListRefreshes == 0 {
				require.Never(t, func() bool {
					return listRefreshes.Load() != 0
				}, 200*time.Millisecond, 20*time.Millisecond)
			}
		})
	}
}

func TestDAGRunInvalidatorBatchesAndTargetsLifecycleListRefreshes(t *testing.T) {
	mux := NewMultiplexer(StreamConfig{}, nil)
	t.Cleanup(mux.Shutdown)

	var counts sync.Map
	mux.RegisterFetcher(TopicTypeDAGRuns, func(_ context.Context, identifier string) (any, error) {
		counter, _ := counts.LoadOrStore(identifier, &atomic.Int64{})
		counter.(*atomic.Int64).Add(1)
		return nil, nil
	})
	mux.SetRefreshMode(TopicTypeDAGRuns, TopicRefreshModeOnDemand)

	tests := []struct {
		topic         string
		wantRefreshes int64
	}{
		{topic: "dagruns:status=0&status=5", wantRefreshes: 1},
		{topic: "dagruns:status=1", wantRefreshes: 1},
		{topic: "dagruns:status=7", wantRefreshes: 1},
		{topic: "dagruns:status=4&status=6"},
		{topic: "dagruns:status=2&status=3&status=8"},
		{topic: "dagruns:status=1%2C4", wantRefreshes: 1},
		{topic: "dagruns:status=4%2C6"},
		{topic: "dagruns:status=+1+", wantRefreshes: 1},
		{topic: "dagruns:status=running", wantRefreshes: 1},
		{topic: "dagruns:status=999", wantRefreshes: 1},
		{topic: "dagruns:workspace=all", wantRefreshes: 1},
	}
	topics := make([]string, 0, len(tests))
	identifiers := make([]string, 0, len(tests))
	for _, tt := range tests {
		topics = append(topics, tt.topic)
		parsed, err := ParseTopic(tt.topic)
		require.NoError(t, err)
		identifiers = append(identifiers, parsed.Identifier)
	}
	result, err := mux.createSession(context.Background(), httptest.NewRecorder(), topics, 0)
	require.NoError(t, err)
	require.NotNil(t, result.session)
	defer mux.removeSession(result.session)

	events := make([]*eventstore.Event, 0, 2)
	for _, runID := range []string{"run-1", "run-2"} {
		events = append(events, eventstore.NewDAGRunEvent(
			eventstore.Source{Service: eventstore.SourceServiceServer},
			eventstore.TypeDAGRunRunning,
			&exec.DAGRunStatus{
				Name:      "test",
				DAGRunID:  runID,
				AttemptID: "attempt-1",
				Status:    ir.Running,
			},
			nil,
		))
	}

	wakeTopicsForDAGRunEvents(mux, events)

	count := func(identifier string) int64 {
		counter, ok := counts.Load(identifier)
		if !ok {
			return 0
		}
		return counter.(*atomic.Int64).Load()
	}
	require.Eventually(t, func() bool {
		for i, tt := range tests {
			if tt.wantRefreshes > 0 && count(identifiers[i]) != tt.wantRefreshes {
				return false
			}
		}
		return true
	}, time.Second, 10*time.Millisecond)
	require.Never(t, func() bool {
		for i, tt := range tests {
			if count(identifiers[i]) != tt.wantRefreshes {
				return true
			}
		}
		return false
	}, 200*time.Millisecond, 20*time.Millisecond)
}
