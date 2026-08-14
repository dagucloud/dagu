// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/persis"
)

// WithLock runs fn while holding the process-group lock.
func (s *ProcStore) WithLock(ctx context.Context, groupName string, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return persis.NewProcLockError(err)
	}
	callbackStarted := false
	err := s.col.WithLock(ctx, procLockKey(groupName), func() error {
		callbackStarted = true
		return fn()
	})
	if err != nil && !callbackStarted {
		return persis.NewProcLockError(err)
	}
	return err
}

func procLockKey(groupName string) string {
	return strings.TrimSuffix(procGroupPrefix(groupName), "/") + "/_lock"
}
