// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/humantask"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

type humanTaskInputContextKey struct{}

const maxHumanTaskRequestBodyBytes int64 = 16 << 20

func humanTaskInputMiddleware(mountedAPIPath string) func(http.Handler) http.Handler {
	return humanTaskInputMiddlewareWithLimit(mountedAPIPath, maxHumanTaskRequestBodyBytes)
}

func humanTaskInputMiddlewareWithLimit(mountedAPIPath string, maxBodyBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || !isHumanTaskCompletionPath(r.URL.Path, mountedAPIPath) {
				next.ServeHTTP(w, r)
				return
			}
			limitedBody := http.MaxBytesReader(w, r.Body, maxBodyBytes)
			raw, err := io.ReadAll(limitedBody)
			_ = limitedBody.Close()
			if err != nil {
				if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
					WriteErrorResponse(w, &Error{
						HTTPStatus: http.StatusRequestEntityTooLarge,
						Code:       api.ErrorCodePayloadTooLarge,
						Message:    fmt.Sprintf("human-task completion input exceeds the %d-byte limit", maxBodyBytes),
					})
					return
				}
				WriteErrorResponse(w, &Error{HTTPStatus: http.StatusBadRequest, Code: api.ErrorCodeBadRequest, Message: "failed to read human-task input"})
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			r.ContentLength = int64(len(raw))
			input, err := humantask.ParseJSONInput(raw)
			if err != nil {
				WriteErrorResponse(w, &Error{HTTPStatus: http.StatusBadRequest, Code: api.ErrorCodeBadRequest, Message: err.Error()})
				return
			}
			ctx := context.WithValue(r.Context(), humanTaskInputContextKey{}, input)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func isHumanTaskCompletionPath(path, mountedAPIPath string) bool {
	relative, ok := strings.CutPrefix(path, strings.TrimSuffix(mountedAPIPath, "/"))
	if !ok {
		return false
	}
	parts := strings.Split(strings.Trim(relative, "/"), "/")
	return len(parts) == 6 && parts[0] == "dag-runs" && parts[3] == "human-tasks" && parts[5] == "complete"
}

// CompleteHumanTask validates and completes one root DAG-run human task.
func (a *API) CompleteHumanTask(
	ctx context.Context,
	request api.CompleteHumanTaskRequestObject,
) (api.CompleteHumanTaskResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}
	status, err := a.authorizeHumanTaskMutation(ctx, request.Name, request.DagRunId)
	if err != nil {
		return nil, err
	}
	if err := a.requireDAGRunStatusExecute(ctx, status); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return &api.CompleteHumanTask400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: "human-task completion input must be a JSON object",
		}, nil
	}
	input, ok := ctx.Value(humanTaskInputContextKey{}).(humantask.Input)
	if !ok {
		return nil, errors.New("validated human-task input is missing from the request context")
	}

	service := a.humanTaskService()
	completedBy, completedByID := manualActionSubject(ctx)
	result, err := service.Complete(a.withEventContext(ctx), humantask.CompleteRequest{
		DAGName:       request.Name,
		DAGRunID:      request.DagRunId,
		StepID:        request.StepId,
		Input:         input,
		CompletedBy:   completedBy,
		CompletedByID: completedByID,
	})
	if err != nil {
		a.logHumanTaskCompletion(ctx, request.Name, request.DagRunId, request.StepId, result, err)
		return completeHumanTaskErrorResponse(ctx, err)
	}
	a.logHumanTaskCompletion(ctx, request.Name, request.DagRunId, request.StepId, result, nil)
	return &api.CompleteHumanTask200JSONResponse{
		DagName:               result.DAGName,
		DagRunId:              result.DAGRunID,
		StepId:                result.StepID,
		AlreadyCompleted:      result.AlreadyCompleted,
		Queued:                result.Queued,
		RemainingWaitingSteps: result.RemainingWaitingSteps,
	}, nil
}

// ResumeHumanTaskDAGRun retries a pending human-task enqueue.
func (a *API) ResumeHumanTaskDAGRun(
	ctx context.Context,
	request api.ResumeHumanTaskDAGRunRequestObject,
) (api.ResumeHumanTaskDAGRunResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}
	status, err := a.authorizeHumanTaskMutation(ctx, request.Name, request.DagRunId)
	if err != nil {
		return nil, err
	}
	if err := a.requireDAGRunStatusExecute(ctx, status); err != nil {
		return nil, err
	}

	result, err := a.humanTaskService().Resume(a.withEventContext(ctx), request.Name, request.DagRunId)
	if err != nil {
		a.logHumanTaskResume(ctx, request.Name, request.DagRunId, result, err)
		return resumeHumanTaskErrorResponse(ctx, err)
	}
	a.logHumanTaskResume(ctx, request.Name, request.DagRunId, result, nil)
	return &api.ResumeHumanTaskDAGRun200JSONResponse{
		DagName:  result.DAGName,
		DagRunId: result.DAGRunID,
		Queued:   result.Queued,
	}, nil
}

func (a *API) authorizeHumanTaskMutation(
	ctx context.Context,
	dagName string,
	dagRunID string,
) (*ir.DAGRunStatus, error) {
	attempt, err := a.dagRunRepository.FindAttempt(ctx, ir.NewDAGRunRef(dagName, dagRunID))
	if err != nil {
		if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
			return nil, &Error{
				HTTPStatus: http.StatusNotFound,
				Code:       api.ErrorCodeNotFound,
				Message:    fmt.Sprintf("dag-run ID %s not found for DAG %s", dagRunID, dagName),
			}
		}
		return nil, fmt.Errorf("failed to find DAG-run: %w", err)
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read DAG-run status: %w", err)
	}
	if status == nil {
		return nil, errors.New("failed to read DAG-run status: status data is nil")
	}
	if err := a.requireDAGRunStatusVisible(ctx, status); err != nil {
		return nil, err
	}
	return status, nil
}

func (a *API) humanTaskService() *humantask.Service {
	return &humantask.Service{
		DAGRunRepository: a.dagRunRepository,
		QueueStore:       a.queueStore,
		ProcRepository:   a.procRepository,
	}
}

func completeHumanTaskErrorResponse(ctx context.Context, err error) (api.CompleteHumanTaskResponseObject, error) {
	if resumeErr, ok := errors.AsType[*humantask.ResumeError](err); ok {
		logger.Error(ctx, "Failed to queue DAG-run after human-task completion",
			tag.Error(resumeErr.Err),
			tag.DAG(resumeErr.Result.DAGName),
			tag.RunID(resumeErr.Result.DAGRunID),
			slog.String("step", resumeErr.Result.StepID),
		)
		details := map[string]any{
			"completionStored": true,
			"resumePending":    true,
			"dagRunId":         resumeErr.Result.DAGRunID,
			"stepId":           resumeErr.Result.StepID,
		}
		return &api.CompleteHumanTask503JSONResponse{
			Code:    api.ErrorCodeHumanTaskResumeFailed,
			Message: "human-task completion was saved, but the DAG-run could not be queued for resume; retry the same completion request",
			Details: &details,
		}, nil
	}
	switch humantask.KindOf(err) {
	case humantask.ErrorInvalid:
		return &api.CompleteHumanTask400JSONResponse{Code: api.ErrorCodeBadRequest, Message: err.Error()}, nil
	case humantask.ErrorNotFound:
		return &api.CompleteHumanTask404JSONResponse{Code: api.ErrorCodeNotFound, Message: err.Error()}, nil
	case humantask.ErrorConflict:
		return &api.CompleteHumanTask409JSONResponse{Code: api.ErrorCodeConflict, Message: err.Error()}, nil
	case humantask.ErrorInternal:
		return nil, err
	}
	return nil, err
}

func resumeHumanTaskErrorResponse(ctx context.Context, err error) (api.ResumeHumanTaskDAGRunResponseObject, error) {
	if resumeErr, ok := errors.AsType[*humantask.ResumeError](err); ok {
		logger.Error(ctx, "Failed to queue human-task DAG-run resume",
			tag.Error(resumeErr.Err),
			tag.DAG(resumeErr.Result.DAGName),
			tag.RunID(resumeErr.Result.DAGRunID),
		)
		details := map[string]any{
			"completionStored": true,
			"resumePending":    true,
			"dagRunId":         resumeErr.Result.DAGRunID,
		}
		return &api.ResumeHumanTaskDAGRun503JSONResponse{
			Code:    api.ErrorCodeHumanTaskResumeFailed,
			Message: "the DAG-run could not be queued for resume; retry the resume request",
			Details: &details,
		}, nil
	}
	switch humantask.KindOf(err) {
	case humantask.ErrorNotFound:
		return &api.ResumeHumanTaskDAGRun404JSONResponse{Code: api.ErrorCodeNotFound, Message: err.Error()}, nil
	case humantask.ErrorConflict, humantask.ErrorInvalid:
		return &api.ResumeHumanTaskDAGRun409JSONResponse{Code: api.ErrorCodeConflict, Message: err.Error()}, nil
	case humantask.ErrorInternal:
		return nil, err
	}
	return nil, err
}

func (a *API) logHumanTaskCompletion(
	ctx context.Context,
	dagName, dagRunID, stepID string,
	result humantask.Result,
	err error,
) {
	details := map[string]any{
		"dag_name":                dagName,
		"dag_run_id":              dagRunID,
		"step_id":                 stepID,
		"already_completed":       result.AlreadyCompleted,
		"queued":                  result.Queued,
		"remaining_waiting_steps": result.RemainingWaitingSteps,
		"outcome":                 "succeeded",
	}
	if err != nil {
		details["outcome"] = "failed"
		if _, ok := errors.AsType[*humantask.ResumeError](err); ok {
			details["outcome"] = "completion_stored_resume_pending"
		}
	}
	a.logAudit(ctx, audit.CategoryDAG, "dag_human_task_complete", details)
}

func (a *API) logHumanTaskResume(
	ctx context.Context,
	dagName, dagRunID string,
	result humantask.Result,
	err error,
) {
	details := map[string]any{
		"dag_name":   dagName,
		"dag_run_id": dagRunID,
		"queued":     result.Queued,
		"outcome":    "succeeded",
	}
	if err != nil {
		details["outcome"] = "failed"
	}
	a.logAudit(ctx, audit.CategoryDAG, "dag_human_task_resume", details)
}
