// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package connection

import (
	"context"
	"errors"
	"time"
)

// ErrLocked reports that another holder has a connection's lock.
var ErrLocked = errors.New("connection is locked by another holder")

// Locks hands out one lock per connection, which every process that shares
// the token state honors. The token broker holds it while it refreshes.
type Locks interface {
	// TryLock takes the lock of key without waiting. It returns ErrLocked
	// when another holder has it.
	TryLock(ctx context.Context, key Key) (Lock, error)
	// WaitUnlocked returns once no live holder has the lock of key: the
	// holder released it, or stopped renewing it for StaleThreshold. It
	// returns ctx's error when ctx ends first.
	WaitUnlocked(ctx context.Context, key Key) error
	// StaleThreshold is how long a holder that stops renewing its lock
	// keeps it.
	StaleThreshold() time.Duration
}

// Lock is a held connection lock. It renews itself until Unlock.
type Lock interface {
	// Held fails once the lock may have passed to another holder. Call it
	// right before each write that the lock guards.
	Held() error
	// Unlock releases the lock.
	Unlock()
}

// CatalogLocks hands out one lock per tool catalog: a connection and one of
// its MCP services. A catalog lock is never one of Locks, so a listing that
// asks the broker for a token never waits on itself.
type CatalogLocks interface {
	// TryLock takes the lock of service's catalog of key without waiting.
	// It returns ErrLocked when another holder has it.
	TryLock(ctx context.Context, key Key, service string) (Lock, error)
	// WaitUnlocked returns once no live holder has the lock of service's
	// catalog of key. It returns ctx's error when ctx ends first.
	WaitUnlocked(ctx context.Context, key Key, service string) error
}
