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
	casRetryInitialBackoff = 5 * time.Millisecond
	casRetryMaxBackoff     = 5 * time.Second
)

// retryCAS runs op with exponential full-jitter backoff while op returns
// [persis.ErrConflict]. Any other error (including ErrNotFound) propagates.
// Total time is bounded by ctx.
func retryCAS(ctx context.Context, op func(ctx context.Context) error) error {
	backoff := casRetryInitialBackoff
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := op(ctx)
		if err == nil {
			return nil
		}
		if !errors.Is(err, persis.ErrConflict) {
			return err
		}

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
