// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sse

import (
	"context"
	"log/slog"
	"time"

	"github.com/dagucloud/dagu/v2/internal/service/eventstore"
)

const defaultDAGRunInvalidationPollInterval = time.Second

func StartDAGRunEventInvalidation(
	ctx context.Context,
	service *eventstore.Service,
	mux *Multiplexer,
	logger *slog.Logger,
	pollInterval time.Duration,
) {
	if ctx == nil || service == nil || mux == nil {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	if pollInterval <= 0 {
		pollInterval = defaultDAGRunInvalidationPollInterval
	}

	go func() {
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()

		var (
			cursor       eventstore.DAGRunCursor
			bootstrapped bool
		)

		for {
			if !bootstrapped {
				head, err := service.DAGRunHeadCursor(ctx)
				if err != nil {
					logger.Warn("Failed to bootstrap DAG-run event invalidation cursor", slog.String("error", err.Error()))
				} else {
					cursor = head
					bootstrapped = true
				}
			}

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}

			if !bootstrapped {
				continue
			}

			events, nextCursor, err := service.ReadDAGRunEvents(ctx, cursor)
			if err != nil {
				logger.Warn("Failed to read DAG-run events for SSE invalidation", slog.String("error", err.Error()))
				continue
			}
			cursor = nextCursor

			for _, event := range events {
				wakeTopicsForDAGRunEvent(mux, event)
			}
		}
	}()
}

func wakeTopicsForDAGRunEvent(mux *Multiplexer, event *eventstore.Event) {
	if mux == nil || event == nil {
		return
	}

	snapshot, err := eventstore.DAGRunSnapshotFromEvent(event)
	if err != nil || snapshot == nil {
		return
	}

	if snapshot.Name != "" && snapshot.DAGRunID != "" {
		mux.WakeTopic(TopicTypeDAGRun, snapshot.Name+"/"+snapshot.DAGRunID)
	}

	root := snapshot.Root
	if root.Name != "" && root.DAGRunID != "" && (root.Name != snapshot.Name || root.DAGRunID != snapshot.DAGRunID) {
		mux.WakeTopic(TopicTypeDAGRun, root.Name+"/"+root.DAGRunID)
		mux.WakeTopic(TopicTypeSubDAGRun, root.Name+"/"+root.DAGRunID+"/"+snapshot.DAGRunID)
		if snapshot.Parent.Name != "" && snapshot.Parent.DAGRunID != "" {
			mux.WakeTopic(TopicTypeSubDAGRun, root.Name+"/"+root.DAGRunID+"/"+snapshot.Parent.DAGRunID)
		}
	}

	if event.Type != eventstore.TypeDAGRunUpdated {
		mux.WakeTopicType(TopicTypeDAGRuns)
	}
	mux.WakeTopicType(TopicTypeQueues)
	mux.WakeTopicType(TopicTypeDAGsList)

	if snapshot.ProcGroup != "" {
		mux.WakeTopic(TopicTypeQueueItems, snapshot.ProcGroup)
	}

	if snapshot.DAGFile != "" {
		mux.WakeTopic(TopicTypeDAG, snapshot.DAGFile)
		mux.WakeTopic(TopicTypeDAGHistory, snapshot.DAGFile)
		return
	}

	mux.WakeTopicType(TopicTypeDAG)
	mux.WakeTopicType(TopicTypeDAGHistory)
}
