// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package controller

import (
	"context"
	"errors"
	"time"

	"github.com/dagucloud/dagu/internal/core"
)

func persistRuntimeCandidate(
	ctx context.Context,
	store RuntimeStore,
	base *Runtime,
	candidate *Runtime,
	now time.Time,
) (*Runtime, bool, error) {
	if err := store.Put(ctx, candidate); err != nil {
		if !errors.Is(err, ErrSnapshotTooLarge) {
			return nil, false, err
		}
		failure := cloneRuntime(base)
		markRuntimeFailed(failure, "runtime_snapshot_limit", now)
		if err := store.Put(ctx, failure); err != nil {
			return nil, false, err
		}
		return failure, false, nil
	}
	return candidate, true, nil
}

func markRuntimeFailed(runtime *Runtime, code string, now time.Time) {
	now = now.UTC()
	runtime.Status = core.Failed
	runtime.ActiveDAGRun = nil
	runtime.WaitingQuestion = nil
	runtime.FinishedAt = &now
	runtime.UpdatedAt = now
	bounded := boundedErrorCode(code)
	runtime.LastError = &bounded
}
