// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backoff

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/stretchr/testify/assert"
)

func TestRetry(t *testing.T) {
	t.Run("SuccessfulRetry", func(t *testing.T) {
		// Operation succeeds after 2 failures
		attempts := 0
		op := func(_ context.Context) error {
			attempts++
			if attempts < 3 {
				return errors.New("temporary error")
			}
			return nil
		}

		policy := NewConstantBackoffPolicy(10 * time.Millisecond)
		err := Retry(context.Background(), op, policy, nil)

		assert.NoError(t, err)
		assert.Equal(t, 3, attempts)
	})

	t.Run("NonRetriableError", func(t *testing.T) {
		// Operation returns non-retriable error
		permanentErr := errors.New("permanent error")
		attempts := 0
		op := func(_ context.Context) error {
			attempts++
			return permanentErr
		}

		isRetriable := func(err error) bool {
			return err != permanentErr
		}

		policy := NewConstantBackoffPolicy(10 * time.Millisecond)
		err := Retry(context.Background(), op, policy, isRetriable)

		assert.Equal(t, permanentErr, err)
		assert.Equal(t, 1, attempts) // Should not retry
	})

	t.Run("ContextCancellation", func(t *testing.T) {
		// Context canceled during operation
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		op := func(ctx context.Context) error {
			return ctx.Err()
		}

		policy := NewConstantBackoffPolicy(10 * time.Millisecond)
		err := Retry(ctx, op, policy, nil)

		assert.Equal(t, context.Canceled, err)
	})

	t.Run("ContextCancellationDuringWait", func(t *testing.T) {
		// Context canceled during backoff wait
		ctx, cancel := context.WithCancel(context.Background())
		attempts := 0

		op := func(_ context.Context) error {
			attempts++
			return errors.New("error")
		}

		enteredBackoff := make(chan struct{}, 1)
		go func() {
			<-enteredBackoff
			cancel()
		}()

		policy := &signalingRetryPolicy{
			interval:       time.Second,
			enteredBackoff: enteredBackoff,
		}
		start := time.Now()
		err := Retry(ctx, op, policy, nil)
		elapsed := time.Since(start)

		assert.Equal(t, context.Canceled, err)
		assert.Equal(t, 1, attempts)
		assert.Less(t, elapsed, 200*time.Millisecond)
	})

	t.Run("RetriesExhausted", func(t *testing.T) {
		// Operation never succeeds, retries exhausted
		attempts := 0
		testErr := errors.New("test error")
		op := func(_ context.Context) error {
			attempts++
			return testErr
		}

		policy := NewConstantBackoffPolicy(10 * time.Millisecond)
		policy.MaxRetries = 3
		err := Retry(context.Background(), op, policy, nil)

		assert.Equal(t, testErr, err) // Should return original error
		assert.Equal(t, 4, attempts)  // Initial + 3 retries
	})

	t.Run("NilIsRetriableFunc", func(t *testing.T) {
		// When isRetriable is nil, all errors should be retriable
		attempts := 0
		op := func(_ context.Context) error {
			attempts++
			if attempts < 3 {
				return errors.New("any error")
			}
			return nil
		}

		policy := NewConstantBackoffPolicy(10 * time.Millisecond)
		err := Retry(context.Background(), op, policy, nil)

		assert.NoError(t, err)
		assert.Equal(t, 3, attempts)
	})

	t.Run("ExponentialBackoffWithJitter", func(t *testing.T) {
		// Test with exponential backoff and jitter
		attempts := int32(0)
		op := func(_ context.Context) error {
			atomic.AddInt32(&attempts, 1)
			if atomic.LoadInt32(&attempts) < 3 {
				return errors.New("retry me")
			}
			return nil
		}

		basePolicy := NewExponentialBackoffPolicy(10 * time.Millisecond)
		basePolicy.MaxInterval = 100 * time.Millisecond
		policy := WithJitter(basePolicy, FullJitter)

		start := time.Now()
		err := Retry(context.Background(), op, policy, nil)
		elapsed := time.Since(start)

		assert.NoError(t, err)
		assert.Equal(t, int32(3), atomic.LoadInt32(&attempts))
		// With jitter, timing is unpredictable but should be relatively quick
		assert.Less(t, elapsed, 200*time.Millisecond)
	})
}

type signalingRetryPolicy struct {
	interval       time.Duration
	enteredBackoff chan<- struct{}
}

func (p *signalingRetryPolicy) ComputeNextInterval(_ int, _ time.Duration, _ error) (time.Duration, error) {
	select {
	case p.enteredBackoff <- struct{}{}:
	default:
	}
	return p.interval, nil
}

func TestRetryFailureLogLevelOverride(t *testing.T) {
	runRetryExhausted := func(ctx context.Context) {
		policy := NewConstantBackoffPolicy(time.Millisecond)
		policy.MaxRetries = 1
		_ = Retry(ctx, func(context.Context) error {
			return errors.New("boom")
		}, policy, nil)
	}

	t.Run("DefaultWarnRemainsVisible", func(t *testing.T) {
		var buf bytes.Buffer
		ctx := logger.WithLogger(context.Background(), logger.NewLogger(
			logger.WithFormat("text"),
			logger.WithWriter(&buf),
			logger.WithQuiet(),
		))

		runRetryExhausted(ctx)

		assert.Contains(t, buf.String(), "Retry attempts exhausted")
	})

	t.Run("DebugOverrideSuppressesFailureLogOutsideDebugMode", func(t *testing.T) {
		var buf bytes.Buffer
		ctx := logger.WithLogger(context.Background(), logger.NewLogger(
			logger.WithFormat("text"),
			logger.WithWriter(&buf),
			logger.WithQuiet(),
		))
		ctx = WithRetryFailureLogLevel(ctx, slog.LevelDebug)

		runRetryExhausted(ctx)

		assert.NotContains(t, buf.String(), "Retry attempts exhausted")
	})

	t.Run("DebugOverrideStillLogsInDebugMode", func(t *testing.T) {
		var buf bytes.Buffer
		ctx := logger.WithLogger(context.Background(), logger.NewLogger(
			logger.WithDebug(),
			logger.WithFormat("text"),
			logger.WithWriter(&buf),
			logger.WithQuiet(),
		))
		ctx = WithRetryFailureLogLevel(ctx, slog.LevelDebug)

		runRetryExhausted(ctx)

		assert.True(t, strings.Contains(buf.String(), "Retry attempts exhausted"))
	})
}
