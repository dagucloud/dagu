// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package serviceregistry

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistry_RegisterUnregister(t *testing.T) {
	tmpDir := t.TempDir()
	registry := New(tmpDir)

	ctx := context.Background()
	hostInfo := exec.HostInfo{
		ID:     "test-instance",
		Host:   "localhost",
		Port:   8080,
		Status: exec.ServiceStatusActive,
	}
	err := registry.Register(ctx, exec.ServiceNameCoordinator, hostInfo)
	require.NoError(t, err)

	// Check that service registry directory was created
	assert.DirExists(t, tmpDir)

	// Stop should not error
	registry.Unregister(ctx)
}

func TestRegistry_GetServiceMembers(t *testing.T) {
	tmpDir := t.TempDir()
	registry := New(tmpDir)

	ctx := context.Background()

	// Test getting members for empty service
	members, err := registry.GetServiceMembers(ctx, exec.ServiceNameCoordinator)
	require.NoError(t, err)
	assert.Empty(t, members)

	// Register a service
	hostInfo := exec.HostInfo{
		ID:     "test-instance",
		Host:   "localhost",
		Port:   8080,
		Status: exec.ServiceStatusActive,
	}
	err = registry.Register(ctx, exec.ServiceNameCoordinator, hostInfo)
	require.NoError(t, err)
	defer registry.Unregister(ctx)

	// Now should find the registered member
	members, err = registry.GetServiceMembers(ctx, exec.ServiceNameCoordinator)
	require.NoError(t, err)
	assert.Len(t, members, 1)
	assert.Equal(t, "localhost:8080", fmt.Sprintf("%s:%d", members[0].Host, members[0].Port))
}

func TestRegistry_RegisterInstance(t *testing.T) {
	tmpDir := t.TempDir()
	registry := New(tmpDir)

	ctx := context.Background()
	hostInfo := exec.HostInfo{
		ID:     "test-coordinator",
		Host:   "localhost",
		Port:   8080,
		Status: exec.ServiceStatusActive,
	}
	err := registry.Register(ctx, exec.ServiceNameCoordinator, hostInfo)
	require.NoError(t, err)
	defer registry.Unregister(ctx)

	// Check that instance file was created
	serviceDir := filepath.Join(tmpDir, string(exec.ServiceNameCoordinator))
	entries, err := os.ReadDir(serviceDir)
	require.NoError(t, err)
	assert.Len(t, entries, 1)

	// Verify registry can find the registered instance
	members, err := registry.GetServiceMembers(ctx, exec.ServiceNameCoordinator)
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.Equal(t, "localhost:8080", fmt.Sprintf("%s:%d", members[0].Host, members[0].Port))
}

func TestRegistry_Heartbeat(t *testing.T) {
	tmpDir := t.TempDir()
	registry := New(tmpDir)
	registry.heartbeatInterval = 100 * time.Millisecond // Short interval for testing

	ctx := context.Background()
	hostInfo := exec.HostInfo{
		ID:     "test-heartbeat",
		Host:   "localhost",
		Port:   8080,
		Status: exec.ServiceStatusActive,
	}
	err := registry.Register(ctx, exec.ServiceNameCoordinator, hostInfo)
	require.NoError(t, err)
	defer registry.Unregister(ctx)

	// Get the initial mtime of the instance file
	instanceFile := instanceFilePath(tmpDir, string(exec.ServiceNameCoordinator), hostInfo.ID)
	initialInfo, err := os.Stat(instanceFile)
	require.NoError(t, err)

	// Heartbeat already started by Register method.
	// Wait for heartbeat to update the instance file (mtime should advance).
	require.Eventually(t, func() bool {
		info, err := os.Stat(instanceFile)
		return err == nil && info.ModTime().After(initialInfo.ModTime())
	}, 5*time.Second, 20*time.Millisecond)
}

func TestRegistry_UnregisterRemovesInstance(t *testing.T) {
	tmpDir := t.TempDir()
	registry := New(tmpDir)

	ctx := context.Background()
	hostInfo := exec.HostInfo{
		ID:     "test-stop",
		Host:   "localhost",
		Port:   8080,
		Status: exec.ServiceStatusActive,
	}
	err := registry.Register(ctx, exec.ServiceNameCoordinator, hostInfo)
	require.NoError(t, err)

	// Verify it exists
	members, err := registry.GetServiceMembers(ctx, exec.ServiceNameCoordinator)
	require.NoError(t, err)
	assert.Len(t, members, 1)

	// Unregister from registry
	registry.Unregister(ctx)

	// Verify instance file was removed
	serviceDir := filepath.Join(tmpDir, string(exec.ServiceNameCoordinator))
	entries, err := os.ReadDir(serviceDir)
	if err == nil {
		assert.Empty(t, entries)
	}
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	registry := New(tmpDir)

	ctx := context.Background()
	hostInfo := exec.HostInfo{
		ID:     "test-concurrent",
		Host:   "localhost",
		Port:   8080,
		Status: exec.ServiceStatusActive,
	}
	err := registry.Register(ctx, exec.ServiceNameCoordinator, hostInfo)
	require.NoError(t, err)
	defer registry.Unregister(ctx)

	// Concurrent finder access
	done := make(chan bool)
	for i := range 10 {
		go func(i int) {
			serviceName := exec.ServiceName(fmt.Sprintf("%s-%d", exec.ServiceNameCoordinator, i))
			// Just verify we can get members without error
			_, err := registry.GetServiceMembers(context.Background(), serviceName)
			assert.NoError(t, err)
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for range 10 {
		<-done
	}
}

func TestRegistry_HeartbeatRecreatesFile(t *testing.T) {
	tmpDir := t.TempDir()
	registry := New(tmpDir)
	registry.heartbeatInterval = 100 * time.Millisecond // Short interval for testing

	ctx := context.Background()
	hostInfo := exec.HostInfo{
		ID:     "test-recreate",
		Host:   "localhost",
		Port:   8080,
		Status: exec.ServiceStatusActive,
	}
	err := registry.Register(ctx, exec.ServiceNameCoordinator, hostInfo)
	require.NoError(t, err)
	defer registry.Unregister(ctx)

	// Verify file exists
	instanceFile := filepath.Join(tmpDir, string(exec.ServiceNameCoordinator), "test-recreate.json")
	assert.FileExists(t, instanceFile)

	// Delete the file to simulate accidental deletion
	err = os.Remove(instanceFile)
	require.NoError(t, err)
	assert.NoFileExists(t, instanceFile)

	// Wait for heartbeat to recreate the file
	require.Eventually(t, func() bool {
		_, err := os.Stat(instanceFile)
		return err == nil
	}, 5*time.Second, 20*time.Millisecond)

	// Verify content is correct
	info, err := readInstanceFile(instanceFile)
	require.NoError(t, err)
	assert.Equal(t, "test-recreate", info.ID)
	assert.Equal(t, "localhost:8080", fmt.Sprintf("%s:%d", info.Host, info.Port))
}

func TestRegistry_MultipleInstances(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create multiple monitors for different instances
	instances := []struct {
		serviceName exec.ServiceName
		hostInfo    exec.HostInfo
	}{
		{
			serviceName: exec.ServiceNameCoordinator,
			hostInfo: exec.HostInfo{
				ID:     "coord-1",
				Host:   "coord1.example.com",
				Port:   9090,
				Status: exec.ServiceStatusActive,
			},
		},
		{
			serviceName: exec.ServiceNameCoordinator,
			hostInfo: exec.HostInfo{
				ID:     "coord-2",
				Host:   "coord2.example.com",
				Port:   9090,
				Status: exec.ServiceStatusActive,
			},
		},
		{
			serviceName: "worker",
			hostInfo: exec.HostInfo{
				ID:     "worker-1",
				Host:   "worker1.example.com",
				Port:   8080,
				Status: exec.ServiceStatusActive,
			},
		},
	}

	// Register all instances
	registries := make([]*registry, len(instances))
	for i, inst := range instances {
		registry := New(tmpDir)
		err := registry.Register(ctx, inst.serviceName, inst.hostInfo)
		require.NoError(t, err)
		registries[i] = registry
		defer registry.Unregister(ctx)
	}

	// Use any registry to resolve services
	resolver := registries[0]

	// Check coordinator service has 2 instances
	coordMembers, err := resolver.GetServiceMembers(ctx, exec.ServiceNameCoordinator)
	require.NoError(t, err)
	assert.Len(t, coordMembers, 2)

	// Check worker service has 1 instance
	workerMembers, err := resolver.GetServiceMembers(ctx, "worker")
	require.NoError(t, err)
	assert.Len(t, workerMembers, 1)
	assert.Equal(t, "worker1.example.com:8080", fmt.Sprintf("%s:%d", workerMembers[0].Host, workerMembers[0].Port))
}

func TestRegistry_CleanupOnlyEnabledForCoordinator(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Test 1: Registry without coordinator registration - cleanup disabled
	registryScheduler := New(tmpDir)
	hostInfoScheduler := exec.HostInfo{
		ID:     "scheduler-1",
		Host:   "localhost",
		Port:   8080,
		Status: exec.ServiceStatusActive,
	}
	err := registryScheduler.Register(ctx, exec.ServiceNameScheduler, hostInfoScheduler)
	require.NoError(t, err)

	// Get finder - should have cleanup disabled
	_, err = registryScheduler.GetServiceMembers(ctx, exec.ServiceNameCoordinator)
	require.NoError(t, err)

	registryScheduler.mu.RLock()
	finder := registryScheduler.finders[exec.ServiceNameCoordinator]
	registryScheduler.mu.RUnlock()

	assert.NotNil(t, finder)
	assert.Nil(t, finder.cleaner, "cleanup should be disabled for non-coordinator registry")

	registryScheduler.Unregister(ctx)

	// Test 2: Registry with coordinator registration - cleanup enabled
	registryCoordinator := New(tmpDir)
	hostInfoCoordinator := exec.HostInfo{
		ID:     "coordinator-1",
		Host:   "localhost",
		Port:   9090,
		Status: exec.ServiceStatusActive,
	}
	err = registryCoordinator.Register(ctx, exec.ServiceNameCoordinator, hostInfoCoordinator)
	require.NoError(t, err)

	// Get finder - should have cleanup enabled
	_, err = registryCoordinator.GetServiceMembers(ctx, exec.ServiceNameCoordinator)
	require.NoError(t, err)

	registryCoordinator.mu.RLock()
	finderCoord := registryCoordinator.finders[exec.ServiceNameCoordinator]
	registryCoordinator.mu.RUnlock()

	assert.NotNil(t, finderCoord)
	assert.NotNil(t, finderCoord.cleaner, "cleanup should be enabled for coordinator registry")

	registryCoordinator.Unregister(ctx)
}
