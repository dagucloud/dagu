// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/tunnel"
)

// GetSchedulerStatus returns the status of all registered scheduler instances
func (a *API) GetSchedulerStatus(ctx context.Context, _ api.GetSchedulerStatusRequestObject) (api.GetSchedulerStatusResponseObject, error) {
	logger.Info(ctx, "GetSchedulerStatus called")
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
	members, err := a.serviceRegistry.GetServiceMembers(ctx, exec.ServiceNameScheduler)
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
		case exec.ServiceStatusActive:
			status = api.SchedulerInstanceStatusActive
		case exec.ServiceStatusInactive:
			status = api.SchedulerInstanceStatusInactive
		case exec.ServiceStatusUnknown:
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
	logger.Info(ctx, "GetCoordinatorStatus called")
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
	members, err := a.serviceRegistry.GetServiceMembers(ctx, exec.ServiceNameCoordinator)
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
		case exec.ServiceStatusActive:
			status = api.CoordinatorInstanceStatusActive
		case exec.ServiceStatusInactive:
			status = api.CoordinatorInstanceStatusInactive
		case exec.ServiceStatusUnknown:
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
	logger.Info(ctx, "GetTunnelStatus called")
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
