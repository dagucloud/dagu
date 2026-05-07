// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

// lockKeyPrefix is the prefix for lock keys.
const lockKeyPrefix = "dagu:lock:"

// unlockScript is a Lua script for atomic unlock.
// It only deletes the key if the value matches (to avoid deleting someone else's lock).
const unlockScript = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
else
    return 0
end
`

// LockManager handles distributed locking using Redis.
type LockManager struct {
	client  goredis.UniversalClient
	cfg     *Config
	lockKey string
	lockVal string
}

// NewLockManager creates a new lock manager.
func NewLockManager(client goredis.UniversalClient, cfg *Config) *LockManager {
	return &LockManager{
		client:  client,
		cfg:     cfg,
		lockKey: lockKeyPrefix + cfg.Lock,
		lockVal: uuid.Must(uuid.NewV7()).String(),
	}
}

// Acquire attempts to acquire the lock with retry.
// Returns a release function that must be called to release the lock.
func (m *LockManager) Acquire(ctx context.Context) (func() error, error) {
	// Validate lock key to prevent cross-DAG collisions from empty lock names
	if m.cfg.Lock == "" {
		return nil, fmt.Errorf("lock name cannot be empty: would create shared global key")
	}

	timeout := time.Duration(m.cfg.LockTimeout) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	retries := m.cfg.LockRetry
	if retries == 0 {
		retries = 10
	}

	waitTime := time.Duration(m.cfg.LockWait) * time.Millisecond
	if waitTime == 0 {
		waitTime = 100 * time.Millisecond
	}

	var lastErr error
	for attempt := 0; attempt < retries; attempt++ {
		// Check context
		if ctx.Err() != nil {
			return nil, fmt.Errorf("context cancelled while acquiring lock: %w", ctx.Err())
		}

		// Try to acquire lock
		ok, err := m.client.SetNX(ctx, m.lockKey, m.lockVal, timeout).Result()
		if err != nil {
			lastErr = err
			continue
		}

		if ok {
			// Lock acquired
			return m.releaseFunc(), nil
		}

		// Lock not acquired, wait and retry
		if attempt < retries-1 {
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("context cancelled while waiting for lock: %w", ctx.Err())
			case <-time.After(waitTime):
				// Continue to next attempt
			}
		}
	}

	if lastErr != nil {
		return nil, fmt.Errorf("failed to acquire lock after %d attempts: %w", retries, lastErr)
	}
	return nil, fmt.Errorf("failed to acquire lock after %d attempts: lock is held by another process", retries)
}

// releaseFunc returns a function that releases the lock.
func (m *LockManager) releaseFunc() func() error {
	return func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		result, err := m.client.Eval(ctx, unlockScript, []string{m.lockKey}, m.lockVal).Result()
		if err != nil {
			return fmt.Errorf("failed to release lock: %w", err)
		}

		// If result is 0, the lock was not held by us (expired or stolen)
		if result == int64(0) {
			return fmt.Errorf("lock was not held or has expired")
		}

		return nil
	}
}

// Extend extends the lock timeout.
func (m *LockManager) Extend(ctx context.Context, duration time.Duration) error {
	// First verify we still hold the lock
	val, err := m.client.Get(ctx, m.lockKey).Result()
	if err != nil {
		return fmt.Errorf("failed to verify lock ownership: %w", err)
	}
	if val != m.lockVal {
		return fmt.Errorf("lock is not owned by this process")
	}

	// Extend the lock
	ok, err := m.client.Expire(ctx, m.lockKey, duration).Result()
	if err != nil {
		return fmt.Errorf("failed to extend lock: %w", err)
	}
	if !ok {
		return fmt.Errorf("lock no longer exists")
	}

	return nil
}

// IsHeld checks if the lock is currently held by this manager.
func (m *LockManager) IsHeld(ctx context.Context) (bool, error) {
	val, err := m.client.Get(ctx, m.lockKey).Result()
	if err == goredis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return val == m.lockVal, nil
}

// TTL returns the remaining time-to-live of the lock.
func (m *LockManager) TTL(ctx context.Context) (time.Duration, error) {
	return m.client.TTL(ctx, m.lockKey).Result()
}
