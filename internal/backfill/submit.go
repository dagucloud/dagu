// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Service submits backfills.
type Service struct {
	Store   Store
	History HistoryReader
	Now     func() time.Time
}

// SubmitRequest is a backfill to plan and, unless DryRun, submit.
type SubmitRequest struct {
	Plan    PlanRequest
	Options Options
	DAGFile string
	Actor   string
	DryRun  bool
	// Revision, when set, is the revision of the plan the operator saw. A
	// submit whose plan has changed since starts nothing.
	Revision string
}

// SubmitResult is the outcome of a submit.
type SubmitResult struct {
	Plan Plan
	// Record is the backfill the submit created, or the open backfill with
	// the same options, which a dry run also reports. It is nil when no slot
	// needs a run.
	Record *Record
	// Existing reports that Record was open before the submit.
	Existing bool
}

// ErrStalePlan is returned when a submit's revision no longer matches its
// plan. The result carries the new plan.
var ErrStalePlan = errors.New("the plan changed since it was shown")

// Submit plans a backfill and records it. Planning and recording happen
// under the DAG's lock, so two submits never select the same slot.
func (s *Service) Submit(ctx context.Context, req SubmitRequest) (SubmitResult, error) {
	if req.Plan.Now.IsZero() {
		req.Plan.Now = s.now()
	}
	unlock, err := s.Store.LockDAG(ctx, req.Plan.DAG.Name)
	if err != nil {
		return SubmitResult{}, err
	}
	defer unlock()

	records, err := s.Store.List(ctx, req.Plan.DAG.Name)
	if err != nil {
		return SubmitResult{}, err
	}
	pending := make(map[time.Time]bool)
	var existing *Record
	for _, record := range records {
		if !record.Open() {
			continue
		}
		if existing == nil && record.Options.Equal(req.Options) {
			if !req.DryRun {
				return SubmitResult{Record: record, Existing: true}, nil
			}
			existing = record
		}
		for _, slot := range record.Slots {
			pending[slot.At.UTC()] = true
		}
	}

	plan, err := s.plan(ctx, req.Plan, pending)
	if err != nil {
		return SubmitResult{}, err
	}
	result := SubmitResult{Plan: plan}
	if req.DryRun {
		result.Record, result.Existing = existing, existing != nil
		return result, nil
	}
	if req.Revision != "" && req.Revision != plan.Revision {
		return result, ErrStalePlan
	}
	runs := plan.Runs()
	if len(runs) == 0 {
		return result, nil
	}

	record := &Record{
		ID:         NewID(),
		DAGName:    req.Plan.DAG.Name,
		DAGFile:    req.DAGFile,
		Definition: req.Plan.DAG.YamlData,
		Options:    req.Options,
		Actor:      req.Actor,
		CreatedAt:  req.Plan.Now.UTC(),
		Revision:   plan.Revision,
	}
	for _, slot := range runs {
		record.Slots = append(record.Slots, RecordSlot{At: slot.At.UTC(), Window: slot.Window})
	}
	if err := s.Store.Create(ctx, record); err != nil {
		return result, fmt.Errorf("record backfill: %w", err)
	}
	result.Record = record
	return result, nil
}

func (s *Service) plan(ctx context.Context, req PlanRequest, pending map[time.Time]bool) (Plan, error) {
	rng, err := SlotsOf(req)
	if err != nil {
		return Plan{}, err
	}
	if len(rng.Slots) == 0 {
		return MakePlan(req, rng, nil, pending), nil
	}
	history, err := LoadHistory(ctx, s.History, req.DAG, req.Schedule, rng.Slots[0], rng.Slots[len(rng.Slots)-1])
	if err != nil {
		return Plan{}, err
	}
	return MakePlan(req, rng, SlotStates(rng.Slots, req.Schedule, history), pending), nil
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
