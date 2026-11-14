// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/humantask"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/queue"
)

const manualResumeTimeout = 30 * time.Second

// ResumeDAGRun retries execution admission for a root run's saved approval.
func (a *API) ResumeDAGRun(ctx context.Context, request api.ResumeDAGRunRequestObject) (api.ResumeDAGRunResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}
	ref := ir.NewDAGRunRef(request.Name, request.DagRunId)
	attempt, err := a.dagRunRepository.FindAttempt(ctx, ref)
	if err != nil {
		if isDAGRunLookupNotFound(err) {
			return &api.ResumeDAGRun404JSONResponse{Code: api.ErrorCodeNotFound, Message: "DAG-run not found"}, nil
		}
		return nil, err
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, err
	}
	if err := a.requireDAGRunStatusExecute(ctx, status); err != nil {
		return nil, err
	}
	status, err = a.waitForManualStepMutationReady(ctx, attempt, status)
	if err != nil {
		return nil, err
	}
	if !hasRootApproval(status) {
		return &api.ResumeDAGRun409JSONResponse{Code: api.ErrorCodeConflict, Message: "DAG-run has no saved root approval to resume"}, nil
	}
	if status.Status == ir.Queued || status.Status == ir.Running {
		return &api.ResumeDAGRun200JSONResponse{DagRunId: request.DagRunId, Resumed: true}, nil
	}
	if !approvalResumePending(status) {
		return &api.ResumeDAGRun409JSONResponse{Code: api.ErrorCodeConflict, Message: "DAG-run has no approved work ready to resume"}, nil
	}
	if err := a.resumeWaitingDAGRun(ctx, ref, status); err != nil {
		logger.Error(ctx, "Failed to resume approved work", tag.Error(err), tag.RunID(ref.ID))
		if errors.Is(err, queue.ErrRetryStaleLatest) {
			return nil, staleManualResumeError()
		}
		return ptrOf(api.ResumeDAGRun503JSONResponse(approvalResumeFailure())), nil
	}
	a.logAudit(ctx, audit.CategoryDAG, "dag_approval_resume", map[string]any{"dag_name": ref.Name, "dag_run_id": ref.ID})
	return &api.ResumeDAGRun200JSONResponse{DagRunId: ref.ID, Resumed: true}, nil
}

func approvalResumePending(status *ir.DAGRunStatus) bool {
	return hasRootApproval(status) && status.Status == ir.Waiting &&
		(!hasWaitingSteps(status.Nodes) || humantask.ResumePending(status) || humantask.UnblockedNodeReady(status))
}

func hasRootApproval(status *ir.DAGRunStatus) bool {
	if status == nil || !status.Parent.Zero() || (!status.Root.Zero() && status.Root != status.DAGRun()) {
		return false
	}
	for _, node := range status.Nodes {
		if node != nil && node.Step.Approval != nil && node.Status == ir.NodeSucceeded && node.ApprovedAt != "" {
			return true
		}
	}
	return false
}

func approvalResumeFailure() api.Error {
	details := map[string]any{"approvalStored": true, "resumePending": true}
	return api.Error{
		Code: api.ErrorCodeInternalError, Details: &details,
		Message: "approval was saved, but resume failed; retry using the DAG-run resume endpoint",
	}
}

func staleManualResumeError() *Error {
	return &Error{HTTPStatus: http.StatusConflict, Code: api.ErrorCodeConflict,
		Message: "DAG-run state changed before resume could be accepted"}
}
