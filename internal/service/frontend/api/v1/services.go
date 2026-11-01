// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/tunnel"
)

// schedulerPauseUnavailable is returned when no pause store is configured, which
// means the deployment cannot record or observe a scheduler pause.
const schedulerPauseUnavailable = "Scheduler pause state not configured"

// maxSchedulerPauseReasonLength bounds the pause reason. It is rendered in a
// banner that every client polls, so it is checked here rather than relying on
// the OpenAPI bound, which only applies when strict validation is enabled.
const maxSchedulerPauseReasonLength = 512

// GetSchedulerPauseState returns the cluster-wide scheduler pause state.
//
// Deliberately readable by any authenticated user: a paused scheduler explains
// why nothing is running, and that explanation is useless if only admins see it.
func (a *API) GetSchedulerPauseState(ctx context.Context, _ api.GetSchedulerPauseStateRequestObject) (api.GetSchedulerPauseStateResponseObject, error) {
	if a.schedulerPauseStore == nil {
		return api.GetSchedulerPauseStatedefaultJSONResponse{
			Body:       api.Error{Code: api.ErrorCodeInternalError, Message: schedulerPauseUnavailable},
			StatusCode: http.StatusInternalServerError,
		}, nil
	}

	pause, err := a.schedulerPauseStore.Get(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to read scheduler pause state", tag.Error(err))
		return api.GetSchedulerPauseStatedefaultJSONResponse{
			Body:       api.Error{Code: api.ErrorCodeInternalError, Message: "Failed to read scheduler pause state"},
			StatusCode: http.StatusInternalServerError,
		}, nil
	}

	response := api.GetSchedulerPauseState200JSONResponse{Paused: pause.Paused}
	if !pause.PausedAt.IsZero() {
		response.PausedAt = ptrOf(pause.PausedAt.Format(time.RFC3339))
	}
	if pause.PausedBy != "" {
		response.PausedBy = ptrOf(pause.PausedBy)
	}
	if pause.Reason != "" {
		response.Reason = ptrOf(pause.Reason)
	}
	if running, known := a.schedulerRunning(ctx); known {
		response.SchedulerRunning = &running
	}
	return response, nil
}

// schedulerRunning reports whether any scheduler instance holds the scheduling
// lock, and whether that can be told at all. A member in an unknown state may
// hold the lock, so it leaves the answer unknown unless another member is
// active.
func (a *API) schedulerRunning(ctx context.Context) (bool, bool) {
	if a.serviceRegistry == nil {
		return false, false
	}
	members, err := a.serviceRegistry.GetServiceMembers(ctx, serviceregistry.ServiceNameScheduler)
	if err != nil {
		logger.Warn(ctx, "Failed to read scheduler instances", tag.Error(err))
		return false, false
	}
	known := true
	for _, member := range members {
		switch member.Status {
		case serviceregistry.ServiceStatusActive:
			return true, true
		case serviceregistry.ServiceStatusUnknown:
			known = false
		case serviceregistry.ServiceStatusInactive:
		}
	}
	return false, known
}

// UpdateSchedulerPauseState pauses or resumes scheduler-driven run creation for
// every DAG at once. Admin only, because it affects every workspace.
func (a *API) UpdateSchedulerPauseState(ctx context.Context, request api.UpdateSchedulerPauseStateRequestObject) (api.UpdateSchedulerPauseStateResponseObject, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	if a.schedulerPauseStore == nil {
		return api.UpdateSchedulerPauseStatedefaultJSONResponse{
			Body:       api.Error{Code: api.ErrorCodeInternalError, Message: schedulerPauseUnavailable},
			StatusCode: http.StatusInternalServerError,
		}, nil
	}

	var reason string
	if request.Body.Reason != nil {
		reason = *request.Body.Reason
	}
	if utf8.RuneCountInString(reason) > maxSchedulerPauseReasonLength {
		return nil, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    fmt.Sprintf("reason must be at most %d characters", maxSchedulerPauseReasonLength),
		}
	}
	actor := ""
	if user, ok := auth.UserFromContext(ctx); ok && user != nil {
		actor = user.Username
	}

	if err := a.schedulerPauseStore.Set(ctx, request.Body.Paused, actor, reason); err != nil {
		return nil, fmt.Errorf("error updating scheduler pause state: %w", err)
	}

	action := "scheduler_pause"
	if !request.Body.Paused {
		action = "scheduler_resume"
	}
	a.logAudit(ctx, audit.CategorySystem, action, map[string]any{
		"paused": request.Body.Paused,
		"reason": reason,
	})

	return api.UpdateSchedulerPauseState200Response{}, nil
}

// GetSchedulerStatus returns the status of all registered scheduler instances
func (a *API) GetSchedulerStatus(ctx context.Context, _ api.GetSchedulerStatusRequestObject) (api.GetSchedulerStatusResponseObject, error) {
	logger.Debug(ctx, "GetSchedulerStatus called")
	if err := a.requireDeveloperOrAbove(ctx); err != nil {
		return nil, err
	}

	schedulers := []api.SchedulerInstance{}

	// Check if service registry is available
	if a.serviceRegistry == nil {
		return api.GetSchedulerStatusdefaultJSONResponse{
			Body: api.Error{
				Code:    api.ErrorCodeInternalError,
				Message: "Service registry not configured",
			},
			StatusCode: 500,
		}, nil
	}

	// Get all scheduler instances from service registry
	members, err := a.serviceRegistry.GetServiceMembers(ctx, serviceregistry.ServiceNameScheduler)
	if err != nil {
		logger.Error(ctx, "Failed to get scheduler members from service registry", tag.Error(err))
		return api.GetSchedulerStatusdefaultJSONResponse{
			Body: api.Error{
				Code:    api.ErrorCodeInternalError,
				Message: "Failed to retrieve scheduler instances",
			},
			StatusCode: 500,
		}, nil
	}

	// Convert members to API response
	for _, member := range members {
		var status api.SchedulerInstanceStatus
		switch member.Status {
		case serviceregistry.ServiceStatusActive:
			status = api.SchedulerInstanceStatusActive
		case serviceregistry.ServiceStatusInactive:
			status = api.SchedulerInstanceStatusInactive
		case serviceregistry.ServiceStatusUnknown:
			status = api.SchedulerInstanceStatusUnknown
		}

		schedulers = append(schedulers, api.SchedulerInstance{
			InstanceId: member.ID,
			Host:       member.Host,
			Status:     status,
			StartedAt:  member.StartedAt.Format(time.RFC3339),
		})
	}

	return api.GetSchedulerStatus200JSONResponse{
		Schedulers: schedulers,
	}, nil
}

// GetCoordinatorStatus returns the status of all registered coordinator instances
func (a *API) GetCoordinatorStatus(ctx context.Context, _ api.GetCoordinatorStatusRequestObject) (api.GetCoordinatorStatusResponseObject, error) {
	logger.Debug(ctx, "GetCoordinatorStatus called")
	if err := a.requireDeveloperOrAbove(ctx); err != nil {
		return nil, err
	}

	coordinators := []api.CoordinatorInstance{}

	// Check if service registry is available
	if a.serviceRegistry == nil {
		return api.GetCoordinatorStatusdefaultJSONResponse{
			Body: api.Error{
				Code:    api.ErrorCodeInternalError,
				Message: "Service registry not configured",
			},
			StatusCode: 500,
		}, nil
	}

	// Get all coordinator instances from service registry
	members, err := a.serviceRegistry.GetServiceMembers(ctx, serviceregistry.ServiceNameCoordinator)
	if err != nil {
		logger.Error(ctx, "Failed to get coordinator members from service registry", tag.Error(err))
		return api.GetCoordinatorStatusdefaultJSONResponse{
			Body: api.Error{
				Code:    api.ErrorCodeInternalError,
				Message: "Failed to retrieve coordinator instances",
			},
			StatusCode: 500,
		}, nil
	}

	// Convert members to API response
	for _, member := range members {
		var status api.CoordinatorInstanceStatus
		switch member.Status {
		case serviceregistry.ServiceStatusActive:
			status = api.CoordinatorInstanceStatusActive
		case serviceregistry.ServiceStatusInactive:
			status = api.CoordinatorInstanceStatusInactive
		case serviceregistry.ServiceStatusUnknown:
			status = api.CoordinatorInstanceStatusUnknown
		}

		coordinators = append(coordinators, api.CoordinatorInstance{
			InstanceId: member.ID,
			Host:       member.Host,
			Port:       member.Port,
			Status:     status,
			StartedAt:  member.StartedAt.Format(time.RFC3339),
		})
	}

	return api.GetCoordinatorStatus200JSONResponse{
		Coordinators: coordinators,
	}, nil
}

// GetTunnelStatus returns the status of the tunnel service
func (a *API) GetTunnelStatus(ctx context.Context, _ api.GetTunnelStatusRequestObject) (api.GetTunnelStatusResponseObject, error) {
	logger.Debug(ctx, "GetTunnelStatus called")
	if err := a.requireDeveloperOrAbove(ctx); err != nil {
		return nil, err
	}

	// Return disabled if tunnel is not configured or service unavailable
	if !a.config.Tunnel.Enabled || a.tunnelService == nil {
		return api.GetTunnelStatus200JSONResponse{
			Enabled: a.config.Tunnel.Enabled,
			Status:  api.TunnelStatusResponseStatusDisabled,
		}, nil
	}

	info := a.tunnelService.Info()

	// Map tunnel status to API status
	statusMap := map[tunnel.Status]api.TunnelStatusResponseStatus{
		tunnel.StatusConnected:    api.TunnelStatusResponseStatusConnected,
		tunnel.StatusConnecting:   api.TunnelStatusResponseStatusConnecting,
		tunnel.StatusReconnecting: api.TunnelStatusResponseStatusReconnecting,
		tunnel.StatusError:        api.TunnelStatusResponseStatusError,
	}
	status, ok := statusMap[info.Status]
	if !ok {
		status = api.TunnelStatusResponseStatusDisabled
	}

	// Build response
	response := api.GetTunnelStatus200JSONResponse{
		Enabled:   true,
		Status:    status,
		PublicUrl: ptrOf(info.PublicURL),
		Error:     ptrOf(info.Error),
		Mode:      ptrOf(info.Mode),
		IsPublic:  ptrOf(info.IsPublic),
	}

	// Set provider if available
	if info.Provider != "" {
		response.Provider = new(api.TunnelStatusResponseProvider(info.Provider))
	}

	// Set startedAt if tunnel has been started
	if !info.StartedAt.IsZero() {
		response.StartedAt = new(info.StartedAt)
	}

	return response, nil
}
