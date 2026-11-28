// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"net/http"

	"github.com/dagucloud/dagu/v2/api/v1"
)

// errWorkerAccessNotImplemented answers the worker invite, removal, and
// join readiness endpoints until they are served.
var errWorkerAccessNotImplemented = &Error{
	HTTPStatus: http.StatusNotImplemented,
	Code:       api.ErrorCodeInternalError,
	Message:    "not implemented",
}

// CreateWorkerInvite implements the createWorkerInvite operation.
func (a *API) CreateWorkerInvite(context.Context, api.CreateWorkerInviteRequestObject) (api.CreateWorkerInviteResponseObject, error) {
	return nil, errWorkerAccessNotImplemented
}

// ListWorkerInvites implements the listWorkerInvites operation.
func (a *API) ListWorkerInvites(context.Context, api.ListWorkerInvitesRequestObject) (api.ListWorkerInvitesResponseObject, error) {
	return nil, errWorkerAccessNotImplemented
}

// DeleteWorkerInvite implements the deleteWorkerInvite operation.
func (a *API) DeleteWorkerInvite(context.Context, api.DeleteWorkerInviteRequestObject) (api.DeleteWorkerInviteResponseObject, error) {
	return nil, errWorkerAccessNotImplemented
}

// RemoveWorker implements the removeWorker operation.
func (a *API) RemoveWorker(context.Context, api.RemoveWorkerRequestObject) (api.RemoveWorkerResponseObject, error) {
	return nil, errWorkerAccessNotImplemented
}

// GetWorkerJoinReadiness implements the getWorkerJoinReadiness operation.
func (a *API) GetWorkerJoinReadiness(context.Context, api.GetWorkerJoinReadinessRequestObject) (api.GetWorkerJoinReadinessResponseObject, error) {
	return nil, errWorkerAccessNotImplemented
}
