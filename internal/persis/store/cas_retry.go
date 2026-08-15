// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/dagucloud/dagu/v2/internal/persis"
)

func createOrSwap(ctx context.Context, col persis.Collection, current, next *persis.Record) error {
	if current == nil {
		return col.Create(ctx, next)
	}
	err := col.CompareAndSwap(ctx, current.ID, current.Data, next.Data)
	if errors.Is(err, persis.ErrNotFound) {
		return persis.ErrConflict
	}
	return err
}

const (
	conflictRetryInitialBackoff = 5 * time.Millisecond
	conflictRetryMaxBackoff     = 5 * time.Second
)

// retryConflict runs op with exponential full-jitter backoff while op returns
// [persis.ErrConflict]. Any other error (including ErrNotFound) propagates.
// Total time is bounded by ctx.
func retryConflict(ctx context.Context, op func(ctx context.Context) error) error {
	backoff := conflictRetryInitialBackoff
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

		backoff = min(backoff*2, conflictRetryMaxBackoff)
	}
}
