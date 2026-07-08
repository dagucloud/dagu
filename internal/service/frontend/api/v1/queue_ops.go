// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	openapiv1 "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/history"
)

func (a *API) queueNameForDAGRun(ctx context.Context, dagRun exec.DAGRunRef) (string, error) {
	historySvc := history.New(history.Config{DAGRunStore: a.dagRunStore})
	metadata, err := historySvc.DispatchMetadata(ctx, history.DispatchMetadataRequest{
		DAGRun: dagRun,
	})
	if err != nil {
		return "", err
	}
	return metadata.QueueName, nil
}

func mapDispatchCancelAPIError(dagName, dagRunID string, err error) error {
	switch {
	case errors.Is(err, exec.ErrDAGRunIDNotFound), errors.Is(err, exec.ErrNoStatusData):
		return &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       openapiv1.ErrorCodeNotFound,
			Message:    fmt.Sprintf("dag-run ID %s not found for DAG %s", dagRunID, dagName),
		}
	}

	var notQueuedErr *history.RunNotPendingError
	if errors.As(err, &notQueuedErr) {
		message := "DAGRun is not pending dispatch"
		if notQueuedErr.HasStatus {
			message = fmt.Sprintf("DAGRun is not pending dispatch: %s", notQueuedErr.Status)
		}
		return &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       openapiv1.ErrorCodeBadRequest,
			Message:    message,
		}
	}

	return err
}
