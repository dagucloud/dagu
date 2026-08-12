// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package testutil

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

var _ persis.DAGRunStore = DAGRunBackendStub{}

// DAGRunBackendStub fails when a test calls a backend method it did not override.
type DAGRunBackendStub struct{}

func (DAGRunBackendStub) CreateAttempt(context.Context, persis.DAGRunCreateAttemptRequest) (dagrun.Attempt, error) {
	panic("unexpected DAG-run backend call: CreateAttempt")
}

func (DAGRunBackendStub) RecentStatuses(context.Context, string, int) ([]ir.DAGRunStatus, error) {
	panic("unexpected DAG-run backend call: RecentStatuses")
}

func (DAGRunBackendStub) LatestAttempt(context.Context, persis.DAGRunLatestAttemptQuery) (dagrun.Attempt, error) {
	panic("unexpected DAG-run backend call: LatestAttempt")
}

func (DAGRunBackendStub) QueryStatuses(context.Context, persis.DAGRunStatusQuery) (persis.DAGRunStatusPage, error) {
	panic("unexpected DAG-run backend call: QueryStatuses")
}

func (DAGRunBackendStub) CompareAndSwapLatestAttemptStatus(
	context.Context,
	persis.DAGRunCompareAndSwapStatusRequest,
) (*ir.DAGRunStatus, bool, error) {
	panic("unexpected DAG-run backend call: CompareAndSwapLatestAttemptStatus")
}

func (DAGRunBackendStub) FindAttempt(context.Context, ir.DAGRunRef) (dagrun.Attempt, error) {
	panic("unexpected DAG-run backend call: FindAttempt")
}

func (DAGRunBackendStub) FindSubAttempt(context.Context, ir.DAGRunRef, string) (dagrun.Attempt, error) {
	panic("unexpected DAG-run backend call: FindSubAttempt")
}

func (DAGRunBackendStub) RemoveOldDAGRuns(context.Context, persis.DAGRunRetentionRequest) ([]ir.DAGRunRef, error) {
	panic("unexpected DAG-run backend call: RemoveOldDAGRuns")
}

func (DAGRunBackendStub) RemoveDAGRun(context.Context, persis.DAGRunRemoveRequest) error {
	panic("unexpected DAG-run backend call: RemoveDAGRun")
}
