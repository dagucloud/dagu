// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sse

import (
	"context"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
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

			wakeTopicsForDAGRunEvents(mux, events)
		}
	}()
}

func wakeTopicsForDAGRunEvents(mux *Multiplexer, events []*eventstore.Event) {
	if mux == nil {
		return
	}

	affectedStatuses := make(map[core.Status]struct{})
	for _, event := range events {
		for _, status := range wakeTopicsForDAGRunEvent(mux, event) {
			affectedStatuses[status] = struct{}{}
		}
	}

	wakeDAGRunListTopics(mux, affectedStatuses)
}

func wakeTopicsForDAGRunEvent(mux *Multiplexer, event *eventstore.Event) []core.Status {
	if event == nil {
		return nil
	}

	snapshot, err := eventstore.DAGRunSnapshotFromEvent(event)
	if err != nil || snapshot == nil {
		return nil
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

	mux.WakeTopicType(TopicTypeQueues)
	mux.WakeTopicType(TopicTypeDAGsList)

	if snapshot.ProcGroup != "" {
		mux.WakeTopic(TopicTypeQueueItems, snapshot.ProcGroup)
	}

	affectedStatuses := affectedDAGRunListStatuses(event.Type, snapshot.Status)
	if snapshot.DAGFile != "" {
		mux.WakeTopic(TopicTypeDAG, snapshot.DAGFile)
		mux.WakeTopic(TopicTypeDAGHistory, snapshot.DAGFile)
		return affectedStatuses
	}

	mux.WakeTopicType(TopicTypeDAG)
	mux.WakeTopicType(TopicTypeDAGHistory)
	return affectedStatuses
}

func affectedDAGRunListStatuses(eventType eventstore.EventType, current core.Status) []core.Status {
	switch eventType {
	case eventstore.TypeDAGRunQueued:
		return []core.Status{
			core.NotStarted, core.Queued, core.Waiting,
			core.Succeeded, core.PartiallySucceeded, core.Failed, core.Aborted, core.Rejected,
		}
	case eventstore.TypeDAGRunRunning:
		return []core.Status{core.NotStarted, core.Queued, core.Running, core.Waiting}
	case eventstore.TypeDAGRunWaiting:
		return []core.Status{core.Running, core.Waiting}
	case eventstore.TypeDAGRunSucceeded, eventstore.TypeDAGRunPartiallySucceeded:
		return []core.Status{core.Running, core.Waiting, current}
	case eventstore.TypeDAGRunFailed:
		return []core.Status{core.NotStarted, core.Queued, core.Running, core.Waiting, current}
	case eventstore.TypeDAGRunAborted:
		return []core.Status{core.NotStarted, core.Queued, core.Running, core.Waiting, core.Failed, current}
	case eventstore.TypeDAGRunRejected:
		return []core.Status{core.Waiting, current}
	case eventstore.TypeDAGRunUpdated, eventstore.TypeLLMUsageRecorded:
		return nil
	default:
		return nil
	}
}

func wakeDAGRunListTopics(mux *Multiplexer, affectedStatuses map[core.Status]struct{}) {
	if len(affectedStatuses) == 0 {
		return
	}

	mux.mu.RLock()
	topics := make([]*multiplexTopic, 0, len(mux.topics))
	for _, topic := range mux.topics {
		if topic != nil && topic.topicType == TopicTypeDAGRuns && dagRunListTopicMatchesStatuses(topic.identifier, affectedStatuses) {
			topics = append(topics, topic)
		}
	}
	mux.mu.RUnlock()

	batch := exec.NewDAGRunListReadBatch()
	for _, topic := range topics {
		topic.requestPoll(batch)
	}
}

func dagRunListTopicMatchesStatuses(identifier string, affectedStatuses map[core.Status]struct{}) bool {
	values, err := url.ParseQuery(identifier)
	if err != nil {
		return true
	}
	rawStatuses, hasStatusFilter := values["status"]
	if !hasStatusFilter {
		return true
	}

	hasValue := false
	for _, rawStatus := range rawStatuses {
		for part := range strings.SplitSeq(rawStatus, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			hasValue = true
			value, err := strconv.Atoi(part)
			if err != nil {
				return true
			}
			status := core.Status(value)
			if status < core.NotStarted || status > core.Rejected {
				return true
			}
			if _, ok := affectedStatuses[status]; ok {
				return true
			}
		}
	}
	return !hasValue
}
