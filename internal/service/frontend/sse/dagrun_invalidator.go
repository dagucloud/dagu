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

	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

const defaultDAGRunInvalidationPollInterval = time.Second

// ServiceDAGNames returns the names of the DAGs that set a restart policy.
type ServiceDAGNames func(ctx context.Context) ([]string, error)

// StartDAGRunEventInvalidation wakes the topics that show the runs of new
// DAG-run events. serviceDAGs tells which events concern a service DAG;
// when it is nil or fails, every event counts as one that may.
func StartDAGRunEventInvalidation(
	ctx context.Context,
	service *eventstore.Service,
	mux *Multiplexer,
	serviceDAGs ServiceDAGNames,
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
			if len(events) == 0 {
				continue
			}

			wakeTopicsForDAGRunEvents(mux, events, readServiceDAGs(ctx, serviceDAGs, logger))
		}
	}()
}

// readServiceDAGs returns the set of service DAG names, or nil when they
// cannot be read.
func readServiceDAGs(ctx context.Context, read ServiceDAGNames, logger *slog.Logger) map[string]struct{} {
	if read == nil {
		return nil
	}
	names, err := read(ctx)
	if err != nil {
		logger.Warn("Failed to read the service DAGs for SSE invalidation", slog.String("error", err.Error()))
		return nil
	}
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	return set
}

// wakeTopicsForDAGRunEvents wakes the topics that show the runs of events.
// serviceDAGs holds the names of the service DAGs; nil means they are unknown.
func wakeTopicsForDAGRunEvents(mux *Multiplexer, events []*eventstore.Event, serviceDAGs map[string]struct{}) {
	if mux == nil {
		return
	}

	affected := listWakeStatuses{
		all:         make(map[ir.Status]struct{}),
		withoutRuns: make(map[ir.Status]struct{}),
	}
	for _, event := range events {
		statuses, serviceDAGRun := wakeTopicsForDAGRunEvent(mux, event, serviceDAGs)
		for _, status := range statuses {
			affected.all[status] = struct{}{}
			if !serviceDAGRun {
				affected.withoutRuns[status] = struct{}{}
			}
		}
	}

	wakeDAGRunListTopics(mux, affected)
}

// listWakeStatuses holds the statuses that a batch of events changed. A run
// of a service DAG changes a list only when the list shows service DAG runs,
// so those events count in all but not in withoutRuns.
type listWakeStatuses struct {
	all         map[ir.Status]struct{}
	withoutRuns map[ir.Status]struct{}
}

// wakeTopicsForDAGRunEvent wakes the topics that show the run of an event. It
// returns the statuses that the event changed in lists, and whether the run
// belongs to a service DAG. A sub-run belongs to the DAG of its root run.
func wakeTopicsForDAGRunEvent(mux *Multiplexer, event *eventstore.Event, serviceDAGs map[string]struct{}) ([]ir.Status, bool) {
	if event == nil {
		return nil, false
	}

	snapshot, err := eventstore.DAGRunSnapshotFromEvent(event)
	if err != nil || snapshot == nil {
		return nil, false
	}
	dagName := snapshot.Root.Name
	if dagName == "" {
		dagName = snapshot.Name
	}
	_, serviceDAGRun := serviceDAGs[dagName]

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
	if serviceDAGs == nil || serviceDAGRun {
		mux.WakeTopicType(TopicTypeServiceDAGs)
	}

	if snapshot.ProcGroup != "" {
		mux.WakeTopic(TopicTypeQueueItems, snapshot.ProcGroup)
	}

	affectedStatuses := affectedDAGRunListStatuses(event.Type, snapshot.Status)
	if snapshot.DAGFile != "" {
		mux.wakeDAGFileTopics(TopicTypeDAG, snapshot.DAGFile)
		mux.wakeDAGFileTopics(TopicTypeDAGHistory, snapshot.DAGFile)
		return affectedStatuses, serviceDAGRun
	}

	mux.WakeTopicType(TopicTypeDAG)
	mux.WakeTopicType(TopicTypeDAGHistory)
	return affectedStatuses, serviceDAGRun
}

func affectedDAGRunListStatuses(eventType eventstore.EventType, current ir.Status) []ir.Status {
	switch eventType {
	case eventstore.TypeDAGRunQueued:
		return []ir.Status{
			ir.NotStarted, ir.Queued, ir.Waiting,
			ir.Succeeded, ir.PartiallySucceeded, ir.Failed, ir.Aborted, ir.Rejected,
		}
	case eventstore.TypeDAGRunRunning:
		return []ir.Status{ir.NotStarted, ir.Queued, ir.Running, ir.Waiting}
	case eventstore.TypeDAGRunWaiting:
		return []ir.Status{ir.Running, ir.Waiting}
	case eventstore.TypeDAGRunSucceeded, eventstore.TypeDAGRunPartiallySucceeded:
		return []ir.Status{ir.Running, ir.Waiting, current}
	case eventstore.TypeDAGRunFailed:
		return []ir.Status{ir.NotStarted, ir.Queued, ir.Running, ir.Waiting, current}
	case eventstore.TypeDAGRunAborted:
		return []ir.Status{ir.NotStarted, ir.Queued, ir.Running, ir.Waiting, ir.Failed, current}
	case eventstore.TypeDAGRunRejected:
		return []ir.Status{ir.Waiting, current}
	case eventstore.TypeDAGRunUpdated, eventstore.TypeLLMUsageRecorded:
		return nil
	default:
		return nil
	}
}

func wakeDAGRunListTopics(mux *Multiplexer, affected listWakeStatuses) {
	if len(affected.all) == 0 {
		return
	}

	mux.mu.RLock()
	topics := make([]*multiplexTopic, 0, len(mux.topics))
	for _, topic := range mux.topics {
		if topic == nil {
			continue
		}
		if topic.topicType == TopicTypeDAGRunActivity && dagRunActivityTopicMatches(topic.identifier, affected) ||
			topic.topicType == TopicTypeDAGRuns && dagRunListTopicMatchesStatuses(topic.identifier, affected) {
			topics = append(topics, topic)
		}
	}
	mux.mu.RUnlock()

	batch := persis.NewDAGRunListReadBatch()
	for _, topic := range topics {
		topic.requestPoll(batch)
	}
}

// dagRunActivityTopicMatches reports whether the changes reach an activity
// topic. Activity has no status filter, so any change does, except changes
// that only concern service DAG runs when the topic leaves them out.
func dagRunActivityTopicMatches(identifier string, affected listWakeStatuses) bool {
	if excludesServiceDAGRuns(identifier) {
		return len(affected.withoutRuns) > 0
	}
	return true
}

func excludesServiceDAGRuns(identifier string) bool {
	values, err := url.ParseQuery(identifier)
	if err != nil {
		return false
	}
	exclude, err := strconv.ParseBool(values.Get("excludeServiceDAGRuns"))
	return err == nil && exclude
}

func dagRunListTopicMatchesStatuses(identifier string, affected listWakeStatuses) bool {
	values, err := url.ParseQuery(identifier)
	if err != nil {
		return true
	}
	affectedStatuses := affected.all
	if excludesServiceDAGRuns(identifier) {
		affectedStatuses = affected.withoutRuns
	}
	if len(affectedStatuses) == 0 {
		return false
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
			status := ir.Status(value)
			if status < ir.NotStarted || status > ir.Rejected {
				return true
			}
			if _, ok := affectedStatuses[status]; ok {
				return true
			}
		}
	}
	return !hasValue
}
