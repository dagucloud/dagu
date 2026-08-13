// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/proc"
)

// WithLock runs fn while holding the process-group lock.
func (s *ProcStore) WithLock(ctx context.Context, groupName string, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return proc.NewLockError(err)
	}
	callbackStarted := false
	err := s.col.WithLock(ctx, procLockKey(groupName), func() error {
		callbackStarted = true
		return fn()
	})
	if err != nil && !callbackStarted {
		return proc.NewLockError(err)
	}
	return err
}

func procLockKey(groupName string) string {
	return strings.TrimSuffix(procGroupPrefix(groupName), "/") + "/_lock"
}
