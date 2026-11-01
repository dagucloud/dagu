// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"context"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

// ephemeralCleanupInterval is the gap between two passes that remove ended
// ephemeral runs.
const ephemeralCleanupInterval = time.Minute

// ephemeralCleanupConfig tunes the removal of ephemeral runs. A zero field
// selects the default.
type ephemeralCleanupConfig struct {
	ttl      time.Duration
	interval time.Duration
}

// WithEphemeralRunCleanup sets how long an ephemeral run stays after it ends
// and how often the server looks for runs to remove. It exists for tests; the
// defaults are persis.EphemeralRunTTL and one minute.
func WithEphemeralRunCleanup(ttl, interval time.Duration) ServerOption {
	return func(s *Server) {
		s.ephemeralCleanup = ephemeralCleanupConfig{ttl: ttl, interval: interval}
	}
}

// startEphemeralRunCleanup removes the ephemeral runs that an earlier process
// left, then keeps removing each one once it has ended for the TTL. A run that
// has not ended is never removed.
func (srv *Server) startEphemeralRunCleanup(ctx context.Context) {
	repo := srv.dagRunRepository
	if repo == nil {
		return
	}
	ttl := srv.ephemeralCleanup.ttl
	if ttl <= 0 {
		ttl = persis.EphemeralRunTTL
	}
	interval := srv.ephemeralCleanup.interval
	if interval <= 0 {
		interval = ephemeralCleanupInterval
	}

	// A run left by an earlier process has no owner that reads it any more.
	if removed, err := repo.RemoveEndedEphemeralRuns(ctx, 0); err != nil {
		logger.Warn(ctx, "Failed to remove ephemeral runs left at start", tag.Error(err))
	} else if removed > 0 {
		logger.Info(ctx, "Removed ephemeral runs left at start", tag.Count(removed))
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := repo.RemoveEndedEphemeralRuns(ctx, ttl); err != nil {
					logger.Warn(ctx, "Failed to remove ended ephemeral runs", tag.Error(err))
				}
			}
		}
	}()
}
