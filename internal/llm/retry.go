// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package llm

import (
	"context"
	"errors"
	"io"
	"math"
	"net"
	"syscall"
	"time"
)

const (
	defaultLogicalRetryAttempts = 3
	defaultLogicalRetryInitial  = time.Second
	defaultLogicalRetryMax      = 2 * time.Second
	defaultLogicalRetryFactor   = 2.0
)

// LogicalRetryConfig configures retries for a single logical LLM request.
// This sits above the provider's own HTTP retries and is intended for
// transient failures that happen around or after a successful HTTP exchange,
// such as interrupted body reads or response decode failures.
type LogicalRetryConfig struct {
	MaxAttempts     int
	InitialInterval time.Duration
	MaxInterval     time.Duration
	Multiplier      float64
}

// DefaultLogicalRetryConfig returns the standard logical request retry budget.
func DefaultLogicalRetryConfig() LogicalRetryConfig {
	return LogicalRetryConfig{
		MaxAttempts:     defaultLogicalRetryAttempts,
		InitialInterval: defaultLogicalRetryInitial,
		MaxInterval:     defaultLogicalRetryMax,
		Multiplier:      defaultLogicalRetryFactor,
	}
}

// ChatWithRetry executes provider.Chat with bounded logical retries for
// transient request failures.
func ChatWithRetry(ctx context.Context, provider Provider, req *ChatRequest, cfg LogicalRetryConfig) (*ChatResponse, error) {
	cfg = normalizeLogicalRetryConfig(cfg)
	req = NormalizeChatRequest(req)

	var lastErr error
	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		resp, err := provider.Chat(ctx, req)
		if err == nil {
			return resp, nil
		}

		lastErr = err
		if attempt == cfg.MaxAttempts || !ShouldRetryRequest(ctx, err) {
			return nil, err
		}

		if err := waitForRetry(ctx, logicalRetryDelay(cfg, attempt)); err != nil {
			return nil, err
		}
	}

	return nil, lastErr
}

// ShouldRetryRequest returns true when an LLM request failure is transient and
// the caller context is still active, making another logical attempt safe.
func ShouldRetryRequest(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx != nil && ctx.Err() != nil {
		return false
	}

	switch {
	case errors.Is(err, ErrNoAPIKey),
		errors.Is(err, ErrInvalidProvider),
		errors.Is(err, ErrContextTooLong),
		errors.Is(err, ErrModelNotFound),
		errors.Is(err, ErrInvalidRequest),
		errors.Is(err, ErrUnauthorized):
		return false
	}

	if IsRetryable(err) {
		return true
	}

	// A context-canceled decode/read with an otherwise-live parent context is
	// treated as a transient transport interruption rather than an explicit user
	// cancellation.
	return errors.Is(err, context.Canceled)
}

func normalizeLogicalRetryConfig(cfg LogicalRetryConfig) LogicalRetryConfig {
	def := DefaultLogicalRetryConfig()
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = def.MaxAttempts
	}
	if cfg.InitialInterval <= 0 {
		cfg.InitialInterval = def.InitialInterval
	}
	if cfg.MaxInterval <= 0 {
		cfg.MaxInterval = def.MaxInterval
	}
	if cfg.Multiplier <= 1 {
		cfg.Multiplier = def.Multiplier
	}
	return cfg
}

func logicalRetryDelay(cfg LogicalRetryConfig, failedAttempt int) time.Duration {
	if failedAttempt <= 0 {
		return 0
	}

	delay := float64(cfg.InitialInterval) * math.Pow(cfg.Multiplier, float64(failedAttempt-1))
	if delay > float64(cfg.MaxInterval) {
		delay = float64(cfg.MaxInterval)
	}
	return time.Duration(delay)
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func isRetryableTransportError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, ErrTimeout) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.EPIPE) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	return false
}
