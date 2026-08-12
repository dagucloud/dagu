// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

type repositoryEntry struct {
	DAGEntry
	revision string
}

type repositoryEntryReader struct {
	repository *persis.DAGRepository
	interval   time.Duration

	mu      sync.Mutex
	entries map[string]repositoryEntry
	events  chan DAGChangeEvent
	quit    chan struct{}
	stop    sync.Once
}

var _ EntryReader = (*repositoryEntryReader)(nil)

// NewRepositoryEntryReader creates a polling reader for any DAG repository backend.
func NewRepositoryEntryReader(repository *persis.DAGRepository, interval time.Duration) EntryReader {
	if interval <= 0 {
		interval = time.Minute
	}
	return &repositoryEntryReader{
		repository: repository,
		interval:   interval,
		entries:    make(map[string]repositoryEntry),
		events:     make(chan DAGChangeEvent, 64),
		quit:       make(chan struct{}),
	}
}

func (r *repositoryEntryReader) Init(ctx context.Context) error {
	return r.refresh(ctx, false)
}

func (r *repositoryEntryReader) Start(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.quit:
			return
		case <-ticker.C:
			if err := r.refresh(ctx, true); err != nil {
				logger.Error(ctx, "Failed to refresh DAG repository", tag.Error(err))
			}
		}
	}
}

func (r *repositoryEntryReader) Stop() {
	r.stop.Do(func() { close(r.quit) })
}

func (r *repositoryEntryReader) Entries() []DAGEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.entries))
	for id := range r.entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	entries := make([]DAGEntry, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, r.entries[id].DAGEntry)
	}
	return entries
}

func (r *repositoryEntryReader) Events() <-chan DAGChangeEvent {
	return r.events
}

func (r *repositoryEntryReader) refresh(ctx context.Context, emit bool) error {
	if r.repository == nil {
		return errors.New("DAG repository is required")
	}
	paginator := pagination.NewPaginator(1, math.MaxInt)
	result, issues, err := r.repository.List(ctx, persis.DAGListOptions{Paginator: &paginator})
	for _, issue := range issues {
		logger.Error(ctx, "DAG excluded from scheduler", slog.String("issue", issue))
	}
	if err != nil {
		return err
	}

	r.mu.Lock()
	next := make(map[string]repositoryEntry, len(r.entries))
	maps.Copy(next, r.entries)
	r.mu.Unlock()

	events := make([]DAGChangeEvent, 0)
	seen := make(map[string]struct{}, len(result.Items))
	for _, item := range result.Items {
		seen[item.ID] = struct{}{}
		previous, exists := next[item.ID]
		if exists && previous.revision == item.Revision {
			continue
		}
		dag, loadErr := r.repository.GetDetails(ctx, item.ID, persis.DAGLoadOptions{})
		var buildErr error
		if dag != nil {
			buildErr = errors.Join(dag.BuildErrors...)
		}
		if loadErr != nil || buildErr != nil {
			logger.Error(ctx, "DAG excluded from scheduler",
				tag.Name(item.ID),
				tag.Error(errors.Join(loadErr, buildErr)),
			)
			continue
		}
		entry := repositoryEntry{
			DAGEntry: DAGEntry{DefinitionID: item.ID, DAG: dag},
			revision: item.Revision,
		}
		next[item.ID] = entry
		if !emit {
			continue
		}
		if exists && previous.DAG != nil && previous.DAG.Name != dag.Name {
			events = append(events,
				DAGChangeEvent{Type: DAGChangeDeleted, DAGEntry: previous.DAGEntry},
				DAGChangeEvent{Type: DAGChangeAdded, DAGEntry: entry.DAGEntry},
			)
			continue
		}
		changeType := DAGChangeAdded
		if exists {
			changeType = DAGChangeUpdated
		}
		events = append(events, DAGChangeEvent{Type: changeType, DAGEntry: entry.DAGEntry})
	}

	deletedIDs := make([]string, 0)
	for id := range next {
		if _, exists := seen[id]; exists {
			continue
		}
		deletedIDs = append(deletedIDs, id)
	}
	sort.Strings(deletedIDs)
	for _, id := range deletedIDs {
		entry := next[id]
		delete(next, id)
		if emit {
			events = append(events, DAGChangeEvent{Type: DAGChangeDeleted, DAGEntry: entry.DAGEntry})
		}
	}

	r.mu.Lock()
	r.entries = next
	r.mu.Unlock()
	for _, event := range events {
		r.send(ctx, event)
	}
	return nil
}

func (r *repositoryEntryReader) send(ctx context.Context, event DAGChangeEvent) {
	select {
	case r.events <- event:
	case <-ctx.Done():
	case <-r.quit:
	}
}
