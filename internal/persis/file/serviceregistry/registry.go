// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package serviceregistry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
)

// registrationInfo holds information about a single service registration
type registrationInfo struct {
	instanceInfo *instanceInfo
	fileName     string
	serviceName  serviceregistry.ServiceName
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	// unwritten reports that the last write of the instance file failed,
	// so the heartbeat writes it again.
	unwritten bool
}

// registry implements models.ServiceRegistry using file-based service registry
type registry struct {
	baseDir string
	finders map[serviceregistry.ServiceName]*finder
	mu      sync.RWMutex

	// Map of service registrations (can have multiple)
	registrations     map[serviceregistry.ServiceName]*registrationInfo
	registrationsMu   sync.Mutex
	heartbeatInterval time.Duration
	// releasedProjects goes into every registration's instance file.
	releasedProjects []string
	// refreshHooks run after each heartbeat of their service's registration.
	refreshHooks map[serviceregistry.ServiceName]func(context.Context)
}

var (
	_ serviceregistry.ServiceRegistry        = (*registry)(nil)
	_ serviceregistry.ProjectReleaseRecorder = (*registry)(nil)
	_ serviceregistry.RefreshObserver        = (*registry)(nil)
)

// New creates a new file-based service registry
func New(serviceRegistryDir string) *registry {
	return &registry{
		baseDir:           serviceRegistryDir,
		finders:           make(map[serviceregistry.ServiceName]*finder),
		registrations:     make(map[serviceregistry.ServiceName]*registrationInfo),
		heartbeatInterval: 10 * time.Second, // default
		refreshHooks:      make(map[serviceregistry.ServiceName]func(context.Context)),
	}
}

// Register begins monitoring services and registers this instance
func (r *registry) Register(ctx context.Context, serviceName serviceregistry.ServiceName, hostInfo serviceregistry.HostInfo) error {
	r.registrationsMu.Lock()
	defer r.registrationsMu.Unlock()

	// Check if this service is already registered
	if _, exists := r.registrations[serviceName]; exists {
		return fmt.Errorf("service %s already registered", serviceName)
	}

	logger.Info(ctx, "Starting service registry",
		tag.Service(serviceName),
		tag.ServiceID(hostInfo.ID),
		slog.String("host", hostInfo.Host),
		slog.Int("port", hostInfo.Port),
		tag.Status(hostInfo.Status.String()))

	// Ensure base directory exists
	if err := os.MkdirAll(r.baseDir, 0750); err != nil {
		return fmt.Errorf("failed to create service registry directory: %w", err)
	}

	// Create registration info
	reg := &registrationInfo{
		serviceName: serviceName,
		instanceInfo: &instanceInfo{
			ID:               hostInfo.ID,
			Host:             hostInfo.Host,
			Port:             hostInfo.Port,
			PID:              os.Getpid(),
			Status:           hostInfo.Status,
			StartedAt:        time.Now(),
			ReleasedProjects: r.releasedProjects,
			TLS:              hostInfo.TLS,
		},
	}

	// Generate file path
	reg.fileName = filepath.Join(r.baseDir, string(serviceName), fmt.Sprintf("%s.json", fileutil.SafeKey(hostInfo.ID)))

	// Write initial instance file
	if err := writeInstanceFile(reg.fileName, reg.instanceInfo); err != nil {
		return fmt.Errorf("failed to write instance file: %w", err)
	}

	// Store registration
	r.registrations[serviceName] = reg

	// Start heartbeat for this instance
	if err := r.startHeartbeat(ctx, serviceName, r.heartbeatInterval); err != nil {
		return fmt.Errorf("failed to start heartbeat: %w", err)
	}

	return nil
}

// GetServiceMembers returns the list of active hosts for the given service.
// This method combines service resolution and member lookup.
func (r *registry) GetServiceMembers(ctx context.Context, serviceName serviceregistry.ServiceName) ([]serviceregistry.HostInfo, error) {
	finder := r.getFinder(serviceName)
	return finder.members(ctx)
}

// getFinder returns the service finder for a specific service (internal method)
func (r *registry) getFinder(serviceName serviceregistry.ServiceName) *finder {
	r.mu.RLock()
	f, exists := r.finders[serviceName]
	r.mu.RUnlock()

	if exists {
		return f
	}

	// Create new finder with double-check locking
	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock
	if f, exists = r.finders[serviceName]; exists {
		return f
	}

	// Only coordinator processes should run cleanup to avoid conflicts
	enableCleanup := r.isCoordinatorService()
	f = newFinder(r.baseDir, serviceName, enableCleanup)
	r.finders[serviceName] = f

	return f
}

// isCoordinatorService checks if this registry instance runs as coordinator
func (r *registry) isCoordinatorService() bool {
	r.registrationsMu.Lock()
	defer r.registrationsMu.Unlock()
	_, isCoordinator := r.registrations[serviceregistry.ServiceNameCoordinator]
	return isCoordinator
}

// Unregister stops all service registrations
func (r *registry) Unregister(ctx context.Context) {
	r.registrationsMu.Lock()
	registrations := r.registrations
	r.registrations = make(map[serviceregistry.ServiceName]*registrationInfo)
	r.registrationsMu.Unlock()

	// Stop all registrations
	for serviceName, reg := range registrations {
		logger.Info(ctx, "Stopping service registry",
			tag.Service(serviceName),
			tag.ServiceID(reg.instanceInfo.ID),
			slog.String("host", reg.instanceInfo.Host),
			slog.Int("port", reg.instanceInfo.Port))

		// Cancel the context to stop background goroutines
		if reg.cancel != nil {
			reg.cancel()
		}

		// Wait for background goroutines with timeout
		done := make(chan struct{})
		go func() {
			reg.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			// Clean shutdown
		case <-time.After(5 * time.Second):
			// Force shutdown after timeout
			logger.Warn(ctx, "Timeout waiting for registry shutdown",
				tag.Service(serviceName))
		}

		// Remove instance file after heartbeat stops so it cannot recreate the
		// file during shutdown on Windows.
		if err := removeInstanceFile(reg.fileName); err != nil {
			logger.Error(ctx, "Failed to remove instance file",
				tag.Error(err),
				tag.File(reg.instanceInfo.ID))
		}
	}

	// Close all finders to stop their cleanup goroutines
	r.mu.Lock()
	for _, f := range r.finders {
		f.close()
	}
	r.finders = make(map[serviceregistry.ServiceName]*finder)
	r.mu.Unlock()
}

// UpdateStatus updates the status of the registered instance for the given service
func (r *registry) UpdateStatus(_ context.Context, serviceName serviceregistry.ServiceName, status serviceregistry.ServiceStatus) error {
	r.registrationsMu.Lock()
	defer r.registrationsMu.Unlock()

	reg, exists := r.registrations[serviceName]
	if !exists {
		return fmt.Errorf("not registered")
	}

	reg.instanceInfo.Status = status
	return writeInstanceFile(reg.fileName, reg.instanceInfo)
}

// SetReleasedProjects records projectIDs in the instance file of every
// service this process registers, now or later. A file that fails to write
// is written again on the next heartbeat.
func (r *registry) SetReleasedProjects(_ context.Context, projectIDs []string) error {
	r.registrationsMu.Lock()
	defer r.registrationsMu.Unlock()

	r.releasedProjects = slices.Clone(projectIDs)
	var errs []error
	for _, reg := range r.registrations {
		reg.instanceInfo.ReleasedProjects = r.releasedProjects
		reg.unwritten = false
		if err := writeInstanceFile(reg.fileName, reg.instanceInfo); err != nil {
			reg.unwritten = true
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// OnRefresh runs fn after each heartbeat of the registration of
// serviceName.
func (r *registry) OnRefresh(serviceName serviceregistry.ServiceName, fn func(context.Context)) {
	r.registrationsMu.Lock()
	defer r.registrationsMu.Unlock()
	r.refreshHooks[serviceName] = fn
}

// refreshHook returns the function OnRefresh set for serviceName, or nil.
func (r *registry) refreshHook(serviceName serviceregistry.ServiceName) func(context.Context) {
	r.registrationsMu.Lock()
	defer r.registrationsMu.Unlock()
	return r.refreshHooks[serviceName]
}

// rewrite writes reg's instance file when it is missing or its last write
// failed.
func (r *registry) rewrite(reg *registrationInfo, missing bool) error {
	r.registrationsMu.Lock()
	defer r.registrationsMu.Unlock()
	if !missing && !reg.unwritten {
		return nil
	}
	if err := writeInstanceFile(reg.fileName, reg.instanceInfo); err != nil {
		return err
	}
	reg.unwritten = false
	return nil
}

// startHeartbeat starts a background goroutine to update heartbeat for a specific service instance
// Must be called with registrationsMu held
func (r *registry) startHeartbeat(ctx context.Context, serviceName serviceregistry.ServiceName, interval time.Duration) error {
	reg := r.registrations[serviceName]
	if reg == nil {
		return fmt.Errorf("service not registered")
	}

	// Create a cancellable context
	ctx, cancel := context.WithCancel(ctx)
	reg.cancel = cancel

	reg.wg.Go(func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Recreate the file when it is missing or a write failed.
				_, statErr := os.Stat(reg.fileName)
				if err := r.rewrite(reg, os.IsNotExist(statErr)); err != nil {
					logger.Error(ctx, "Failed to recreate instance file",
						tag.Error(err),
						tag.File(reg.fileName))
					continue
				}

				// Update modification time
				now := time.Now()
				if err := os.Chtimes(reg.fileName, now, now); err != nil {
					logger.Error(ctx, "Failed to update heartbeat",
						tag.Error(err),
						tag.File(reg.fileName))
				}
				if hook := r.refreshHook(serviceName); hook != nil {
					hook(ctx)
				}
			}
		}
	})

	return nil
}
