// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	openapiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/queue"
)

func (a *API) queueNameForDAGRun(ctx context.Context, dagRun ir.DAGRunRef) (string, error) {
	attempt, err := a.dagRunRepository.FindAttempt(ctx, dagRun)
	if err != nil {
		return "", err
	}

	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		return "", fmt.Errorf("error reading DAG: %w", err)
	}

	return dag.ProcGroup(), nil
}

func mapAbortQueuedDAGRunAPIError(dagName, dagRunID string, err error) error {
	switch {
	case errors.Is(err, dagrun.ErrDAGRunIDNotFound), errors.Is(err, dagrun.ErrNoStatusData):
		return &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       openapiv1.ErrorCodeNotFound,
			Message:    fmt.Sprintf("dag-run ID %s not found for DAG %s", dagRunID, dagName),
		}
	}

	if notQueuedErr, ok := errors.AsType[*queue.DAGRunNotQueuedError](err); ok {
		message := "DAGRun status is not queued"
		if notQueuedErr.HasStatus {
			message = fmt.Sprintf("DAGRun status is not queued: %s", notQueuedErr.Status)
		}
		return &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       openapiv1.ErrorCodeBadRequest,
			Message:    message,
		}
	}

	return err
}
