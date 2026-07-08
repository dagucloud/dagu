// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package history

import (
	"errors"
	"fmt"

	"github.com/dagucloud/dagu/internal/core"
)

// ErrRetryStaleLatest indicates the caller tried to retry a non-latest attempt.
var ErrRetryStaleLatest = errors.New("retry target is no longer the latest attempt")

// RetryRunOptions control how retry metadata is persisted.
type RetryRunOptions struct {
	// AutoRetry marks scheduler-issued DAG auto-retries.
	AutoRetry bool
}

// RunNotPendingError reports that the latest visible attempt is not pending dispatch.
type RunNotPendingError struct {
	Status    core.Status
	HasStatus bool
}

func (e *RunNotPendingError) Error() string {
	if e == nil || !e.HasStatus {
		return "dag-run is not pending dispatch"
	}
	return fmt.Sprintf("dag-run is not pending dispatch: %s", e.Status)
}
