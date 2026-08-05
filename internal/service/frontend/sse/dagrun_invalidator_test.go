// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sse

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/service/eventstore"
	"github.com/stretchr/testify/require"
)

func TestDAGRunInvalidatorRefreshesListsOnlyForLifecycleEvents(t *testing.T) {
	tests := []struct {
		name              string
		eventType         eventstore.EventType
		wantListRefreshes int64
	}{
		{
			name:              "progress update",
			eventType:         eventstore.TypeDAGRunUpdated,
			wantListRefreshes: 0,
		},
		{
			name:              "lifecycle update",
			eventType:         eventstore.TypeDAGRunRunning,
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
				[]string{"dagrun:test/run-1", "dagruns:status=running"},
				0,
			)
			require.NoError(t, err)
			require.NotNil(t, result.session)
			defer mux.removeSession(result.session)

			status := &exec.DAGRunStatus{
				Name:      "test",
				DAGRunID:  "run-1",
				AttemptID: "attempt-1",
				Status:    core.Running,
			}
			event := eventstore.NewDAGRunEvent(
				eventstore.Source{Service: eventstore.SourceServiceServer},
				tt.eventType,
				status,
				nil,
			)

			wakeTopicsForDAGRunEvent(mux, event)

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
