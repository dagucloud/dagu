// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/dagucloud/dagu/internal/persis"
)

const (
	// casRetryInitialBackoff matches the previous distributed-lock retry
	// interval so a CAS-retry under contention has the same baseline latency
	// as the lock-wait it replaces.
	casRetryInitialBackoff = 5 * time.Millisecond

	// casRetryMaxBackoff caps the per-attempt jittered sleep. It mirrors the
	// previous distributedLockStaleThreshold, so a single op cannot starve
	// the caller longer than the lock-acquisition deadline did before.
	casRetryMaxBackoff = 5 * time.Second
)

// retryCAS runs op until success, ctx cancellation, or op returns an error
// that is not [persis.ErrConflict]. Conflicts trigger an exponential
// full-jitter backoff (sleep uniformly in [0, backoff)) and double the bound
// up to casRetryMaxBackoff. Total time is bounded by ctx, not by an attempt
// count — callers control the deadline.
//
// Only persis.ErrConflict is treated as retryable. Any other error
// (including persis.ErrNotFound from CompareAndSwap on a deleted record)
// propagates immediately so callers can translate it to their domain error.
func retryCAS(ctx context.Context, op func(ctx context.Context) error) error {
	backoff := casRetryInitialBackoff
	for {
		err := op(ctx)
		if err == nil {
			return nil
		}
		if !errors.Is(err, persis.ErrConflict) {
			return err
		}

		// Full jitter: sleep in [0, backoff). math/rand/v2 is intentional —
		// retry spread is not a security context.
		sleep := time.Duration(rand.Int64N(int64(backoff))) //nolint:gosec // jitter, not cryptographic
		timer := time.NewTimer(sleep)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}

		if backoff < casRetryMaxBackoff {
			backoff *= 2
			if backoff > casRetryMaxBackoff {
				backoff = casRetryMaxBackoff
			}
		}
	}
}
