// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aiendpoint

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
)

const (
	// maxRefreshIdle is the longest wait between two scans. A scan sees the
	// endpoints that another process created.
	maxRefreshIdle = time.Hour
	// minRefreshWait is the shortest wait between two scans.
	minRefreshWait = time.Second
	// maxStartJitter spreads the first scan after a start.
	maxStartJitter = 30 * time.Second
	// maxWaitJitter is the largest random time that a scan adds to its wait.
	maxWaitJitter = 5 * time.Minute
)

// Refresher reads the models list of each endpoint once every Period (Spec 093,
// "Refresh and reload"). The server process runs one Refresher. The period
// counts from the last read of the endpoint, good or not, which the store holds.
// So a restart does not read again at once, and two servers on one data
// directory read an endpoint once in a period, except in a short race.
type Refresher struct {
	Service *Service
	// Period is ai.models_refresh_hours as a duration. It must be positive.
	Period time.Duration

	// Now, Wait and Jitter replace the clock, the sleep and the random time. A
	// test sets them. The zero values use the real ones.
	Now    func() time.Time
	Wait   func(ctx context.Context, d time.Duration) error
	Jitter func(max time.Duration) time.Duration
}

func (r *Refresher) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Refresher) wait(ctx context.Context, d time.Duration) error {
	if r.Wait != nil {
		return r.Wait(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (r *Refresher) jitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	if r.Jitter != nil {
		return r.Jitter(max)
	}
	return rand.N(max) //nolint:gosec // jitter of a poll interval, not a secret
}

// Run scans the endpoints until ctx ends. It returns at once for a period of
// zero or below, and for a service with no models list.
func (r *Refresher) Run(ctx context.Context) {
	if r.Period <= 0 || r.Service == nil || !r.Service.ModelsEnabled() {
		return
	}
	if err := r.wait(ctx, r.jitter(maxStartJitter)); err != nil {
		return
	}
	for {
		next := r.Scan(ctx)
		if ctx.Err() != nil {
			return
		}
		pause := min(max(next, minRefreshWait), maxRefreshIdle)
		pause += r.jitter(min(pause/10, maxWaitJitter))
		if err := r.wait(ctx, pause); err != nil {
			return
		}
	}
}

// Scan reads the lists that are due, one endpoint after the other, and returns the
// time until the next list is due. It returns the idle time when no endpoint has
// a list.
func (r *Refresher) Scan(ctx context.Context) time.Duration {
	endpoints, err := r.Service.List(ctx)
	if err != nil {
		logger.Warn(ctx, "AI models scan failed", slog.String("error", err.Error()))
		return maxRefreshIdle
	}
	next := maxRefreshIdle
	for _, ep := range endpoints {
		if ctx.Err() != nil {
			return next
		}
		if ep.ListURL() == "" {
			continue
		}
		due, err := r.dueIn(ctx, ep)
		if err != nil {
			logger.Warn(ctx, "AI models scan failed", slog.String("endpoint", ep.Name), slog.String("error", err.Error()))
			continue
		}
		if due <= 0 {
			if _, err := r.Service.RefreshModelsIfDue(ctx, ep.Scope, ep.Name, r.Period, r.now()); err != nil {
				if !errors.Is(err, context.Canceled) {
					logger.Warn(ctx, "AI models refresh failed", slog.String("endpoint", ep.Name), slog.String("error", err.Error()))
				}
				continue
			}
			due = r.Period
		}
		next = min(next, due)
	}
	return next
}

// dueIn returns the time until the list of the endpoint is due. It is zero or
// negative when the list is due now.
func (r *Refresher) dueIn(ctx context.Context, ep *Endpoint) (time.Duration, error) {
	current, err := r.Service.ModelsOf(ctx, ep)
	if err != nil {
		return 0, err
	}
	if current.AttemptedAt.IsZero() {
		return 0, nil
	}
	return current.AttemptedAt.Add(r.Period).Sub(r.now()), nil
}
