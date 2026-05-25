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

type distributedLockOptionsCollection interface {
	WithLockOptions(ctx context.Context, key string, opts dirlock.LockOptions, fn func() error) error
}

func distributedRecordKey(input string) string {
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}

func distributedLeaseLockKey(attemptKey string) string {
	return "locks/" + distributedRecordKey(attemptKey)
}

func distributedActiveRunLockKey(attemptKey string) string {
	return "locks/active-run-" + distributedRecordKey(attemptKey)
}

func withDistributedCollectionLock(ctx context.Context, col persis.Collection, key string, fn func() error) error {
	lockable, ok := col.(distributedLockOptionsCollection)
	if !ok {
		return fmt.Errorf("distributed store requires collection with WithLockOptions support: %T", col)
	}
	return lockable.WithLockOptions(ctx, key, dirlock.LockOptions{
		StaleThreshold: distributedLockStaleThreshold,
		RetryInterval:  distributedLockRetryInterval,
	}, fn)
}
