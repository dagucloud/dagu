// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Connection settings - generous to handle slow container startup in CI environments.
const (
	maxConnRetries    = 30
	initialRetryDelay = 500 * time.Millisecond
	maxRetryDelay     = 2 * time.Second
	pingTimeout       = 5 * time.Second
)

// createClientWithRetry creates a Redis client with retry logic for transient failures.
// Used in non-worker mode where each step creates its own connection.
func createClientWithRetry(ctx context.Context, cfg *Config) (redis.UniversalClient, error) {
	var client redis.UniversalClient
	var lastErr error
	retryDelay := initialRetryDelay

	for attempt := 1; attempt <= maxConnRetries; attempt++ {
		// Check context before attempting
		if ctx.Err() != nil {
			return nil, fmt.Errorf("context cancelled before connection: %w", ctx.Err())
		}

		// Building the client reads only configuration and local files such
		// as TLS certificates, so a failure here is not transient.
		client, lastErr = createClient(cfg)
		if lastErr != nil {
			return nil, fmt.Errorf("failed to create client: %w", lastErr)
		}

		// Verify connection with ping
		pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
		lastErr = client.Ping(pingCtx).Err()
		cancel()

		if lastErr == nil {
			return client, nil
		}

		// Ping failed, close and retry
		_ = client.Close()

		if attempt < maxConnRetries {
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("context cancelled during ping retry: %w", ctx.Err())
			case <-time.After(retryDelay):
				retryDelay = min(retryDelay*2, maxRetryDelay)
			}
		}
	}

	return nil, fmt.Errorf("failed to ping redis after %d attempts: %w", maxConnRetries, lastErr)
}
