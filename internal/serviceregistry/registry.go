// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package serviceregistry

import (
	"context"
	"time"
)

// ServiceRegistry is responsible for registering and persisting
// running service information.
type ServiceRegistry interface {
	// Register registers services for the given service name and host info.
	// It returns an error if the registry failed to start heartbeat.
	Register(ctx context.Context, serviceName ServiceName, hostInfo HostInfo) error

	// Unregister un-registers current service.
	Unregister(ctx context.Context)

	// GetServiceMembers returns the list of active hosts for the given service.
	// This method combines service resolution and member lookup.
	GetServiceMembers(ctx context.Context, serviceName ServiceName) ([]HostInfo, error)

	// UpdateStatus updates the status of the current registered instance
	UpdateStatus(ctx context.Context, serviceName ServiceName, status ServiceStatus) error
}

// ServiceName represents the name of a service in the service registry system
type ServiceName string

const (
	// ServiceNameCoordinator is the name of the coordinator service
	ServiceNameCoordinator ServiceName = "coordinator"
	// ServiceNameScheduler is the name of the scheduler service
	ServiceNameScheduler ServiceName = "scheduler"
)

// ServiceStatus represents the operational status of a service instance
type ServiceStatus int

const (
	// ServiceStatusUnknown indicates unknown status
	ServiceStatusUnknown ServiceStatus = iota
	// ServiceStatusActive indicates the service is active (e.g., scheduler holds lock)
	ServiceStatusActive
	// ServiceStatusInactive indicates the service is inactive (e.g., scheduler waiting for lock)
	ServiceStatusInactive
)

// String returns the string representation of the service status
func (s ServiceStatus) String() string {
	switch s {
	case ServiceStatusActive:
		return "active"
	case ServiceStatusInactive:
		return "inactive"
	case ServiceStatusUnknown:
		return "unknown"
	default:
		return "unknown"
	}
}

// HostInfo contains information about a host in the service registry system
type HostInfo struct {
	// ID is a unique identifier for the host
	ID string
	// Host is the hostname or IP address
	Host string
	// Port is the port number (0 if not applicable)
	Port int
	// Status is the operational status of the service instance
	Status ServiceStatus
	// StartedAt is when the service instance was started
	StartedAt time.Time
}
