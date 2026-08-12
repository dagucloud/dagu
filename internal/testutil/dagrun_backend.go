// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package testutil

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

var _ dagrun.Store = DAGRunBackendStub{}

// DAGRunBackendStub fails when a test calls a backend method it did not override.
type DAGRunBackendStub struct{}

func (DAGRunBackendStub) CreateAttempt(context.Context, dagrun.CreateAttemptRequest) (dagrun.Attempt, error) {
	panic("unexpected DAG-run backend call: CreateAttempt")
}

func (DAGRunBackendStub) RecentStatuses(context.Context, string, int) ([]ir.DAGRunStatus, error) {
	panic("unexpected DAG-run backend call: RecentStatuses")
}

func (DAGRunBackendStub) LatestAttempt(context.Context, dagrun.LatestAttemptQuery) (dagrun.Attempt, error) {
	panic("unexpected DAG-run backend call: LatestAttempt")
}

func (DAGRunBackendStub) QueryStatuses(context.Context, dagrun.StatusQuery) (dagrun.StatusPage, error) {
	panic("unexpected DAG-run backend call: QueryStatuses")
}

func (DAGRunBackendStub) CompareAndSwapLatestAttemptStatus(
	context.Context,
	dagrun.CompareAndSwapStatusRequest,
) (*ir.DAGRunStatus, bool, error) {
	panic("unexpected DAG-run backend call: CompareAndSwapLatestAttemptStatus")
}

func (DAGRunBackendStub) FindAttempt(context.Context, ir.DAGRunRef) (dagrun.Attempt, error) {
	panic("unexpected DAG-run backend call: FindAttempt")
}

func (DAGRunBackendStub) FindSubAttempt(context.Context, ir.DAGRunRef, string) (dagrun.Attempt, error) {
	panic("unexpected DAG-run backend call: FindSubAttempt")
}

func (DAGRunBackendStub) RemoveOldDAGRuns(context.Context, dagrun.RetentionRequest) ([]ir.DAGRunRef, error) {
	panic("unexpected DAG-run backend call: RemoveOldDAGRuns")
}

func (DAGRunBackendStub) RemoveDAGRun(context.Context, dagrun.RemoveDAGRunRequest) error {
	panic("unexpected DAG-run backend call: RemoveDAGRun")
}
