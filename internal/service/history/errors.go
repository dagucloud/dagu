// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package history

import (
	"errors"
	"fmt"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

// ErrRetryStaleLatest indicates the caller tried to retry a non-latest attempt.
var ErrRetryStaleLatest = errors.New("retry target is no longer the latest attempt")

// RetryRunOptions control how queued retry metadata is persisted.
type RetryRunOptions struct {
	// AutoRetry marks scheduler-issued DAG auto-retries.
	AutoRetry bool
	// OnQueued is called after the queued status and queue item are both durably written.
	OnQueued func(*exec.DAGRunStatus) error
}

// DAGRunNotQueuedError reports that the latest visible attempt is no longer queued.
type DAGRunNotQueuedError struct {
	Status    core.Status
	HasStatus bool
}

func (e *DAGRunNotQueuedError) Error() string {
	if e == nil || !e.HasStatus {
		return "dag-run is not queued"
	}
	return fmt.Sprintf("dag-run is not queued: %s", e.Status)
}
