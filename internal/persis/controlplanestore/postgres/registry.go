// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/controlplanestore/postgres/db"
)

var _ exec.ServiceRegistry = (*Store)(nil)

const (
	serviceHeartbeatInterval = 10 * time.Second
	serviceStaleTimeout      = 30 * time.Second
)

type postgresServiceRegistration struct {
	serviceName exec.ServiceName
	hostInfo    exec.HostInfo
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

// Services returns the service registry implemented by this PostgreSQL control-plane store.
func (s *Store) Services() exec.ServiceRegistry {
	return s
}

// Register registers a service instance and starts its heartbeat loop.
func (s *Store) Register(ctx context.Context, serviceName exec.ServiceName, hostInfo exec.HostInfo) error {
	if serviceName == "" {
		return errors.New("service name is required")
	}
	if hostInfo.ID == "" {
		return errors.New("service instance id is required")
	}
	if hostInfo.Host == "" {
		return errors.New("service host is required")
	}
	if hostInfo.Port < 0 || hostInfo.Port > math.MaxUint16 {
		return fmt.Errorf("service port out of range: %d", hostInfo.Port)
	}

	s.serviceMu.Lock()
	defer s.serviceMu.Unlock()

	if _, ok := s.services[serviceName]; ok {
		return fmt.Errorf("service %s already registered", serviceName)
	}

	if hostInfo.StartedAt.IsZero() {
		hostInfo.StartedAt = time.Now().UTC()
	}
	if err := s.upsertServiceInstance(ctx, serviceName, hostInfo); err != nil {
		return err
	}

	heartbeatCtx, cancel := context.WithCancel(ctx)
	reg := &postgresServiceRegistration{
		serviceName: serviceName,
		hostInfo:    hostInfo,
		cancel:      cancel,
	}
	reg.wg.Go(func() {
		s.serviceHeartbeatLoop(heartbeatCtx, reg)
	})
	s.services[serviceName] = reg
	return nil
}

// Unregister unregisters all service instances owned by this store.
func (s *Store) Unregister(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.serviceMu.Lock()
	registrations := s.services
	s.services = make(map[exec.ServiceName]*postgresServiceRegistration)
	s.serviceMu.Unlock()

	for _, reg := range registrations {
		if reg.cancel != nil {
			reg.cancel()
		}
		reg.wg.Wait()
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		_, err := s.queries.DeleteServiceInstance(cleanupCtx, db.DeleteServiceInstanceParams{
			ServiceName: string(reg.serviceName),
			InstanceID:  reg.hostInfo.ID,
		})
		cancel()
		if err != nil {
			logger.Warn(ctx, "Failed to unregister service instance",
				tag.Error(err),
				slog.String("service", string(reg.serviceName)),
				slog.String("instance_id", reg.hostInfo.ID),
			)
		}
	}
}

// GetServiceMembers returns active members for a service.
func (s *Store) GetServiceMembers(ctx context.Context, serviceName exec.ServiceName) ([]exec.HostInfo, error) {
	activeAfter := time.Now().UTC().Add(-serviceStaleTimeout)
	_, err := s.queries.DeleteStaleServiceInstances(ctx, db.DeleteStaleServiceInstancesParams{
		ServiceName: string(serviceName),
		ActiveAfter: timestamptz(activeAfter),
	})
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListActiveServiceInstances(ctx, db.ListActiveServiceInstancesParams{
		ServiceName: string(serviceName),
		ActiveAfter: timestamptz(activeAfter),
	})
	if err != nil {
		return nil, err
	}
	members := make([]exec.HostInfo, 0, len(rows))
	for _, row := range rows {
		members = append(members, exec.HostInfo{
			ID:        row.InstanceID,
			Host:      row.Host,
			Port:      int(row.Port),
			Status:    serviceStatusFromCode(row.Status),
			StartedAt: timeFromTimestamptz(row.StartedAt),
		})
	}
	return members, nil
}

// UpdateStatus updates the status of the current registered instance.
func (s *Store) UpdateStatus(ctx context.Context, serviceName exec.ServiceName, status exec.ServiceStatus) error {
	s.serviceMu.Lock()
	reg := s.services[serviceName]
	if reg != nil {
		reg.hostInfo.Status = status
	}
	s.serviceMu.Unlock()
	if reg == nil {
		return errors.New("not registered")
	}

	rows, err := s.queries.UpdateServiceInstanceStatus(ctx, db.UpdateServiceInstanceStatusParams{
		Status:      serviceStatusCode(status),
		ServiceName: string(serviceName),
		InstanceID:  reg.hostInfo.ID,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return s.upsertServiceInstance(ctx, serviceName, reg.hostInfo)
	}
	return nil
}

func (s *Store) serviceHeartbeatLoop(ctx context.Context, reg *postgresServiceRegistration) {
	ticker := time.NewTicker(serviceHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().UTC()
			rows, err := s.queries.TouchServiceInstance(ctx, db.TouchServiceInstanceParams{
				LastHeartbeatAt: timestamptz(now),
				ServiceName:     string(reg.serviceName),
				InstanceID:      reg.hostInfo.ID,
			})
			if err != nil {
				continue
			}
			if rows == 0 {
				_ = s.upsertServiceInstance(ctx, reg.serviceName, reg.hostInfo)
			}
		}
	}
}

func (s *Store) upsertServiceInstance(ctx context.Context, serviceName exec.ServiceName, hostInfo exec.HostInfo) error {
	data, err := json.Marshal(hostInfo)
	if err != nil {
		return fmt.Errorf("marshal service instance: %w", err)
	}
	return s.queries.UpsertServiceInstance(ctx, db.UpsertServiceInstanceParams{
		ServiceName:     string(serviceName),
		InstanceID:      hostInfo.ID,
		Host:            hostInfo.Host,
		Port:            int32(hostInfo.Port), //nolint:gosec
		Status:          serviceStatusCode(hostInfo.Status),
		StartedAt:       timestamptz(hostInfo.StartedAt),
		LastHeartbeatAt: timestamptz(time.Now().UTC()),
		Data:            data,
	})
}

func serviceStatusCode(status exec.ServiceStatus) int32 {
	switch status {
	case exec.ServiceStatusUnknown:
		return 0
	case exec.ServiceStatusActive:
		return 1
	case exec.ServiceStatusInactive:
		return 2
	default:
		return 0
	}
}

func serviceStatusFromCode(code int32) exec.ServiceStatus {
	switch code {
	case 1:
		return exec.ServiceStatusActive
	case 2:
		return exec.ServiceStatusInactive
	default:
		return exec.ServiceStatusUnknown
	}
}

func nullableText(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: value != ""}
}
