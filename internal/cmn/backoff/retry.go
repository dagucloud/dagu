// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backoff

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
)

type (
	// Operation to retry
	Operation func(ctx context.Context) error

	// IsRetriableFunc defines a function that checks if an error is retriable.
	IsRetriableFunc func(err error) bool
)

var (
	ErrPermanent = errors.New("permanent error")
)

type retryFailureLogLevelKey struct{}

func PermanentError(err error) error {
	return fmt.Errorf("%w: %w", ErrPermanent, err)
}

// WithRetryFailureLogLevel overrides the log level used for terminal retry
// failures such as exhausted retries and non-retriable errors.
func WithRetryFailureLogLevel(ctx context.Context, level slog.Level) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, retryFailureLogLevelKey{}, level)
}

// Retry executes the operation with retry logic based on the provided policy.
// If isRetriable is nil, all errors are considered retriable.
func Retry(ctx context.Context, op Operation, policy RetryPolicy, isRetriable IsRetriableFunc) error {
	if isRetriable == nil {
		isRetriable = func(err error) bool {
			return !errors.Is(err, ErrPermanent)
		}
	}

	retrier := NewRetrier(policy)
	attempt := 0
	var lastDebugLog time.Time

	for {
		attempt++

		if err := ctx.Err(); err != nil {
			logRetryFailure(ctx, "Retry aborted due to context error",
				tag.Attempt(attempt),
				tag.Error(err))
			return err
		}

		err := op(ctx)
		if err == nil {
			logSuccessIfNeeded(ctx, attempt, &lastDebugLog)
			return nil
		}

		if !isRetriable(err) {
			logRetryFailure(ctx, "Retryable operation failed with non-retriable error",
				tag.Attempt(attempt),
				tag.Error(err))
			return err
		}

		interval, retryErr := retrier.Next(err)
		if retryErr != nil {
			logRetryFailure(ctx, "Retry attempts exhausted",
				tag.Attempt(attempt),
				tag.Error(err))
			return err
		}

		if interval <= 0 {
			interval = time.Millisecond * 100 // Default small delay
		}

		logRetryIfNeeded(ctx, attempt, interval, err, &lastDebugLog)

		if err := waitWithContext(ctx, interval, attempt); err != nil {
			return err
		}
	}
}

func logSuccessIfNeeded(ctx context.Context, attempt int, lastDebugLog *time.Time) {
	if attempt > 1 && time.Since(*lastDebugLog) >= 30*time.Second {
		logger.Debug(ctx, "Retryable operation succeeded",
			tag.Attempt(attempt))
		*lastDebugLog = time.Now()
	}
}

func logRetryIfNeeded(ctx context.Context, attempt int, interval time.Duration, err error, lastDebugLog *time.Time) {
	if time.Since(*lastDebugLog) >= 30*time.Second {
		logger.Debug(ctx, "Retryable operation failed, scheduling retry",
			tag.Attempt(attempt),
			tag.Interval(interval),
			tag.Error(err))
		*lastDebugLog = time.Now()
	}
}

func logRetryFailure(ctx context.Context, msg string, tags ...slog.Attr) {
	switch retryFailureLogLevel(ctx) {
	case slog.LevelDebug:
		logger.Debug(ctx, msg, tags...)
	case slog.LevelInfo:
		logger.Info(ctx, msg, tags...)
	case slog.LevelWarn:
		logger.Warn(ctx, msg, tags...)
	case slog.LevelError:
		logger.Error(ctx, msg, tags...)
	default:
		logger.Warn(ctx, msg, tags...)
	}
}

func retryFailureLogLevel(ctx context.Context) slog.Level {
	level, ok := ctx.Value(retryFailureLogLevelKey{}).(slog.Level)
	if !ok {
		return slog.LevelWarn
	}
	return level
}

func waitWithContext(ctx context.Context, interval time.Duration, attempt int) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		logRetryFailure(ctx, "Retry aborted during backoff wait",
			tag.Attempt(attempt),
			tag.Error(ctx.Err()))
		return ctx.Err()
	}
}
