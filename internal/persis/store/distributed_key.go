// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/internal/persis"
)

const (
	distributedLockStaleThreshold = 5 * time.Second
	distributedLockRetryInterval  = 5 * time.Millisecond
)

type distributedLockCollection interface {
	WithLock(ctx context.Context, key string, fn func() error) error
}

type distributedLockOptionsCollection interface {
	WithLockOptions(ctx context.Context, key string, opts dirlock.LockOptions, fn func() error) error
}

func distributedRecordKey(input string) string {
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}

func withDistributedCollectionLock(ctx context.Context, col persis.Collection, key string, fn func() error) error {
	if lockable, ok := col.(distributedLockOptionsCollection); ok {
		return lockable.WithLockOptions(ctx, key, dirlock.LockOptions{
			StaleThreshold: distributedLockStaleThreshold,
			RetryInterval:  distributedLockRetryInterval,
		}, fn)
	}
	lockable, ok := col.(distributedLockCollection)
	if !ok {
		return fmt.Errorf("distributed store requires collection with WithLock support: %T", col)
	}
	return lockable.WithLock(ctx, key, fn)
}
