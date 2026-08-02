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

// Decide evaluates whether a run scheduled at t should dispatch under cfg.
// A nil cfg always dispatches.
func (s *Service) Decide(cfg *core.CalendarConfig, t time.Time) Decision {
	if cfg == nil {
		return Decision{}
	}
	if s.licensed == nil || !s.licensed() {
		return Decision{Kind: DecisionUnlicensed}
	}
	calendar, err := s.store.Load(cfg.Name)
	if err != nil {
		return Decision{Kind: DecisionError, Err: err}
	}
	return calendar.Evaluate(t, cfg.Days)
}
