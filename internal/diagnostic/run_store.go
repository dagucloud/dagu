// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package diagnostic

import "sync"

const defaultRunStoreLimit = 512

// RunRef identifies one DAG-run for transient diagnostics.
type RunRef struct {
	Name string
	ID   string
}

// RunStore keeps recent run diagnostics in memory.
type RunStore struct {
	mu         sync.RWMutex
	limit      int
	order      []RunRef
	collectors map[RunRef]*Collector
}

// NewRunStore creates an in-memory run diagnostics store.
func NewRunStore() *RunStore {
	return NewRunStoreWithLimit(defaultRunStoreLimit)
}

// NewRunStoreWithLimit creates an in-memory run diagnostics store with a bounded run count.
func NewRunStoreWithLimit(limit int) *RunStore {
	if limit <= 0 {
		limit = defaultRunStoreLimit
	}
	return &RunStore{
		limit:      limit,
		collectors: make(map[RunRef]*Collector),
	}
}

// Sink returns a diagnostic sink for ref.
func (s *RunStore) Sink(ref RunRef) Sink {
	if s == nil || ref.Name == "" || ref.ID == "" {
		return nil
	}
	return runSink{store: s, ref: ref}
}

// Diagnostics returns diagnostics currently held for ref.
func (s *RunStore) Diagnostics(ref RunRef) []Diagnostic {
	if s == nil || ref.Name == "" || ref.ID == "" {
		return nil
	}

	s.mu.RLock()
	collector := s.collectors[ref]
	s.mu.RUnlock()
	if collector == nil {
		return nil
	}
	return collector.Diagnostics()
}

func (s *RunStore) report(ref RunRef, d Diagnostic) {
	collector := s.collector(ref)
	if collector == nil {
		return
	}
	collector.Report(d)
}

func (s *RunStore) collector(ref RunRef) *Collector {
	if s == nil || ref.Name == "" || ref.ID == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.collectors == nil {
		s.collectors = make(map[RunRef]*Collector)
	}
	if collector := s.collectors[ref]; collector != nil {
		return collector
	}

	collector := &Collector{}
	s.collectors[ref] = collector
	s.order = append(s.order, ref)
	for len(s.order) > s.limit {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.collectors, oldest)
	}
	return collector
}

type runSink struct {
	store *RunStore
	ref   RunRef
}

func (s runSink) Report(d Diagnostic) {
	s.store.report(s.ref, d)
}
