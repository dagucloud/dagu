// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package buscal

import (
	"time"

	"github.com/dagucloud/dagu/v2/internal/core"
)

// Service evaluates DAG calendar configs against registered calendars,
// applying the license gate for the business-calendar feature.
type Service struct {
	store    *Store
	licensed func() bool
}

// NewService creates a calendar evaluation service. The licensed func gates
// the feature; a nil func means unlicensed.
func NewService(store *Store, licensed func() bool) *Service {
	return &Service{store: store, licensed: licensed}
}

// Outcome is the dispatch decision for a scheduled run under a DAG's calendar
// config.
type Outcome struct {
	// Skip reports whether the run must not dispatch.
	Skip bool
	// Reason explains a skip in human-readable form.
	Reason string
	// Unlicensed is set when the calendar config was ignored because the
	// feature requires an active license. Skip is always false in that case.
	Unlicensed bool
	// Err is set when the calendar could not be loaded or evaluated. The run
	// is skipped, because dispatching against a broken calendar could fire a
	// run on a non-business day.
	Err error
}

// Decide evaluates whether a run scheduled at t should dispatch under cfg.
// A nil cfg always dispatches.
func (s *Service) Decide(cfg *core.CalendarConfig, t time.Time) Outcome {
	if cfg == nil {
		return Outcome{}
	}
	if s.licensed == nil || !s.licensed() {
		return Outcome{Unlicensed: true}
	}
	calendar, err := s.store.Load(cfg.Name)
	if err != nil {
		return Outcome{Skip: true, Err: err}
	}
	decision := calendar.Evaluate(t, cfg.Days)
	return Outcome{Skip: decision.Skip, Reason: decision.Reason}
}
