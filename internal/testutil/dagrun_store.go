// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package testutil

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

var _ persis.DAGRunStore = DAGRunStoreStub{}

// DAGRunStoreStub fails when a test calls a store method it did not override.
type DAGRunStoreStub struct{}

func (DAGRunStoreStub) CreateAttempt(context.Context, persis.DAGRunCreateAttemptRequest) (dagrun.Attempt, error) {
	panic("unexpected DAG-run store call: CreateAttempt")
}

func (DAGRunStoreStub) RecentStatuses(context.Context, string, int) ([]ir.DAGRunStatus, error) {
	panic("unexpected DAG-run store call: RecentStatuses")
}

func (DAGRunStoreStub) LatestAttempt(context.Context, persis.DAGRunLatestAttemptQuery) (dagrun.Attempt, error) {
	panic("unexpected DAG-run store call: LatestAttempt")
}

func (DAGRunStoreStub) QueryStatuses(context.Context, persis.DAGRunStatusQuery) (persis.DAGRunStatusPage, error) {
	panic("unexpected DAG-run store call: QueryStatuses")
}

func (DAGRunStoreStub) CompareAndSwapLatestAttemptStatus(
	context.Context,
	persis.DAGRunCompareAndSwapStatusRequest,
) (*ir.DAGRunStatus, bool, error) {
	panic("unexpected DAG-run store call: CompareAndSwapLatestAttemptStatus")
}

func (DAGRunStoreStub) FindAttempt(context.Context, ir.DAGRunRef) (dagrun.Attempt, error) {
	panic("unexpected DAG-run store call: FindAttempt")
}

func (DAGRunStoreStub) FindSubAttempt(context.Context, ir.DAGRunRef, string) (dagrun.Attempt, error) {
	panic("unexpected DAG-run store call: FindSubAttempt")
}

func (DAGRunStoreStub) RemoveOldDAGRuns(context.Context, persis.DAGRunRetentionRequest) ([]ir.DAGRunRef, error) {
	panic("unexpected DAG-run store call: RemoveOldDAGRuns")
}

func (DAGRunStoreStub) RemoveDAGRun(context.Context, persis.DAGRunRemoveRequest) error {
	panic("unexpected DAG-run store call: RemoveDAGRun")
}
