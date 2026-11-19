// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"errors"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

var (
	// ErrWaitingAbortNotRoot reports a stop of a waiting child DAG-run. Only
	// its root DAG-run can end the waiting checkpoint.
	ErrWaitingAbortNotRoot = errors.New("only a root dag-run can be stopped while waiting")
	// ErrWaitingAbortStateChanged reports that a DAG-run left its waiting
	// checkpoint before the stop was recorded.
	ErrWaitingAbortStateChanged = errors.New("dag-run state changed before the stop was recorded")
)

// AbortWaiting ends a waiting checkpoint at finishedAt. Waiting steps and the
// run become aborted; every other step keeps its status.
func AbortWaiting(status *ir.DAGRunStatus, finishedAt string) {
	for _, node := range status.Nodes {
		if node == nil || node.Status != ir.NodeWaiting {
			continue
		}
		node.Status = ir.NodeAborted
		node.FinishedAt = finishedAt
	}
	status.Status = ir.Aborted
	status.FinishedAt = finishedAt
}
