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

func TestResolver_Members_EmptyDirectory(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	finder := newFinder(tmpDir, "test-service", true)

	ctx := context.Background()
	members, err := finder.members(ctx)
	require.NoError(t, err)
	assert.Empty(t, members)
}

func TestResolver_Members_WithInstances(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	serviceDir := filepath.Join(tmpDir, "test-service")
	err := os.MkdirAll(serviceDir, 0755)
	require.NoError(t, err)

	// Create test instances
	instances := []instanceInfo{
		{
			ID:     "instance-1",
			Host:   "host1",
			Port:   8080,
			PID:    1234,
			Status: exec.ServiceStatusActive,
		},
		{
			ID:     "instance-2",
			Host:   "host2",
			Port:   8081,
			PID:    1235,
			Status: exec.ServiceStatusActive,
		},
	}

	for _, inst := range instances {
		filename := instanceFilePath(tmpDir, "test-service", inst.ID)
		err := writeInstanceFile(filename, &inst)
		require.NoError(t, err)
	}

	finder := newFinder(tmpDir, "test-service", true)
	ctx := context.Background()
	members, err := finder.members(ctx)
	require.NoError(t, err)
	assert.Len(t, members, 2)

	// Verify members data
	expectedHosts := map[string]int{
		"host1": 8080,
		"host2": 8081,
	}
	for _, member := range members {
		expectedPort, ok := expectedHosts[member.Host]
		assert.True(t, ok)
		assert.Equal(t, expectedPort, member.Port)
		assert.NotEmpty(t, member.ID)
	}
}

func TestResolver_Members_FiltersStaleInstances(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	serviceDir := filepath.Join(tmpDir, "test-service")
	err := os.MkdirAll(serviceDir, 0755)
	require.NoError(t, err)

	// Create fresh and stale instances
	freshInstance := instanceInfo{
		ID:     "fresh",
		Host:   "freshhost",
		Port:   8080,
		PID:    1234,
		Status: exec.ServiceStatusActive,
	}
	staleInstance := instanceInfo{
		ID:     "stale",
		Host:   "stalehost",
		Port:   8081,
		PID:    1235,
		Status: exec.ServiceStatusInactive,
	}

	filename := instanceFilePath(tmpDir, "test-service", freshInstance.ID)
	err = writeInstanceFile(filename, &freshInstance)
	require.NoError(t, err)
	filename = instanceFilePath(tmpDir, "test-service", staleInstance.ID)
	err = writeInstanceFile(filename, &staleInstance)
	require.NoError(t, err)

	// Make stale instance file old by changing its modification time
	staleFile := filepath.Join(tmpDir, "test-service", "stale.json")
	oldTime := time.Now().Add(-300 * time.Second)
	err = os.Chtimes(staleFile, oldTime, oldTime)
	require.NoError(t, err)

	finder := newFinder(tmpDir, "test-service", true)
	// Note: staleTimeout is now internal to the quarantine object (default 30s)

	ctx := context.Background()
	members, err := finder.members(ctx)
	require.NoError(t, err)
	assert.Len(t, members, 1)
	assert.Equal(t, "freshhost", members[0].Host)
	assert.Equal(t, 8080, members[0].Port)

	// Verify stale file was quarantined (renamed, not removed)
	assert.NoFileExists(t, staleFile)

	// Verify quarantined file exists
	matches, err := filepath.Glob(staleFile + ".gc*")
	require.NoError(t, err)
	assert.Len(t, matches, 1, "expected quarantined file to exist")

	// Trigger cleanup manually to test cleanup functionality
	if finder.cleaner != nil {
		finder.cleaner.cleanupQuarantinedFiles(ctx)
	}

	// Verify quarantined file was cleaned up
	matches, err = filepath.Glob(staleFile + ".gc*")
	require.NoError(t, err)
	assert.Empty(t, matches, "expected quarantined file to be cleaned up after manual cleanup")
}

func TestResolver_Members_IgnoresInvalidFiles(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	serviceDir := filepath.Join(tmpDir, "test-service")
	err := os.MkdirAll(serviceDir, 0755)
	require.NoError(t, err)

	// Create valid instance
	validInstance := instanceInfo{
		ID:     "valid",
		Host:   "validhost",
		Port:   8080,
		PID:    1234,
		Status: exec.ServiceStatusActive,
	}
	filename := instanceFilePath(tmpDir, "test-service", validInstance.ID)
	err = writeInstanceFile(filename, &validInstance)
	require.NoError(t, err)

	// Create invalid files
	invalidFiles := []struct {
		name    string
		content string
	}{
		{"invalid.json", "not json"},
		{"textfile.txt", "some text"},
		{"empty.json", ""},
	}

	for _, f := range invalidFiles {
		err := os.WriteFile(filepath.Join(serviceDir, f.name), []byte(f.content), 0644)
		require.NoError(t, err)
	}

	// Create a directory (should be ignored)
	err = os.Mkdir(filepath.Join(serviceDir, "subdir"), 0755)
	require.NoError(t, err)

	finder := newFinder(tmpDir, "test-service", true)
	ctx := context.Background()
	members, err := finder.members(ctx)
	require.NoError(t, err)
	assert.Len(t, members, 1)
	assert.Equal(t, "validhost", members[0].Host)
	assert.Equal(t, 8080, members[0].Port)
}

func TestResolver_Members_ContextCancellation(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	serviceDir := filepath.Join(tmpDir, "test-service")
	err := os.MkdirAll(serviceDir, 0755)
	require.NoError(t, err)

	// Create many instances
	for i := range 100 {
		inst := instanceInfo{
			ID:     fmt.Sprintf("instance-%d", i),
			Host:   "host",
			Port:   8080 + i,
			PID:    1000 + i,
			Status: exec.ServiceStatusActive,
		}
		filename := instanceFilePath(tmpDir, "test-service", inst.ID)
		err := writeInstanceFile(filename, &inst)
		require.NoError(t, err)
	}

	finder := newFinder(tmpDir, "test-service", true)

	// Cancel context immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = finder.members(ctx)
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	// Members may be nil or empty depending on when cancellation occurred
	// (this is acceptable behavior - caller should check error first)
}

func TestQuarantineStaleFileCreatesUniqueCopy(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	serviceDir := filepath.Join(tmpDir, "svc")
	err := os.MkdirAll(serviceDir, 0755)
	require.NoError(t, err)

	instance := instanceInfo{
		ID:     "stale-node",
		Host:   "host",
		Port:   8080,
		PID:    42,
		Status: exec.ServiceStatusInactive,
	}
	original := filepath.Join(serviceDir, "stale-node.json")
	err = writeInstanceFile(original, &instance)
	require.NoError(t, err)

	oldTime := time.Now().Add(-2 * time.Minute)
	err = os.Chtimes(original, oldTime, oldTime)
	require.NoError(t, err)

	f := newFinder(tmpDir, "svc", true)
	ctx := context.Background()

	// Use the quarantine directly
	quarantined := f.quarantine.markStaleFile(ctx, original, oldTime)
	assert.True(t, quarantined, "file should have been quarantined")

	// Verify original file was renamed
	assert.NoFileExists(t, original)

	// Verify quarantined file exists with .gc marker
	matches, err := filepath.Glob(original + ".gc*")
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Contains(t, matches[0], ".gc")
}

func TestQuarantineSkipsRecentlyTouchedFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	serviceDir := filepath.Join(tmpDir, "svc")
	err := os.MkdirAll(serviceDir, 0755)
	require.NoError(t, err)

	instance := instanceInfo{
		ID:     "fresh-node",
		Host:   "host",
		Port:   8080,
		PID:    42,
		Status: exec.ServiceStatusActive,
	}
	filename := filepath.Join(serviceDir, "fresh-node.json")
	err = writeInstanceFile(filename, &instance)
	require.NoError(t, err)

	observed := time.Now().Add(-time.Minute)
	err = os.Chtimes(filename, observed, observed)
	require.NoError(t, err)

	f := newFinder(tmpDir, "svc", true)

	// Another process updates the mod time before quarantine runs.
	newTime := time.Now()
	err = os.Chtimes(filename, newTime, newTime)
	require.NoError(t, err)

	ctx := context.Background()
	quarantined := f.quarantine.markStaleFile(ctx, filename, observed)
	assert.False(t, quarantined, "file should not have been quarantined")

	// Verify original file still exists (not quarantined)
	assert.FileExists(t, filename)

	// Verify no quarantined files were created
	matches, _ := filepath.Glob(filename + ".gc*")
	assert.Empty(t, matches)
}

func TestResolver_RealWorldScenario(t *testing.T) {
	tmpDir := t.TempDir()

	// Simulate coordinator service registry
	coordinatorFinder := newFinder(tmpDir, exec.ServiceNameCoordinator, true)
	// Disable caching for this test to ensure we see updates immediately
	coordinatorFinder.cacheDuration = 0

	ctx := context.Background()

	// Initially no coordinators
	members, err := coordinatorFinder.members(ctx)
	require.NoError(t, err)
	assert.Empty(t, members)

	// Coordinator comes online
	coordinator1 := instanceInfo{
		ID:     "coordinator-primary",
		Host:   "coord1.example.com",
		Port:   9090,
		PID:    2000,
		Status: exec.ServiceStatusActive,
	}
	filename := instanceFilePath(tmpDir, string(exec.ServiceNameCoordinator), coordinator1.ID)
	err = writeInstanceFile(filename, &coordinator1)
	require.NoError(t, err)

	// Now we should see the coordinator
	members, err = coordinatorFinder.members(ctx)
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.Equal(t, "coord1.example.com", members[0].Host)
	assert.Equal(t, 9090, members[0].Port)

	// Second coordinator joins
	coordinator2 := instanceInfo{
		ID:     "coordinator-secondary",
		Host:   "coord2.example.com",
		Port:   9090,
		Status: exec.ServiceStatusActive,
		PID:    2001,
	}
	filename = instanceFilePath(tmpDir, string(exec.ServiceNameCoordinator), coordinator2.ID)
	err = writeInstanceFile(filename, &coordinator2)
	require.NoError(t, err)

	// Should see both coordinators
	members, err = coordinatorFinder.members(ctx)
	require.NoError(t, err)
	assert.Len(t, members, 2)
}

func TestResolver_Members_Caching(t *testing.T) {
	tmpDir := t.TempDir()
	serviceDir := filepath.Join(tmpDir, "test-service")
	err := os.MkdirAll(serviceDir, 0755)
	require.NoError(t, err)

	// Create initial instances
	instance1 := instanceInfo{
		ID:     "instance-1",
		Host:   "host1",
		Port:   8080,
		Status: exec.ServiceStatusActive,
		PID:    1234,
	}
	filename := instanceFilePath(tmpDir, "test-service", instance1.ID)
	err = writeInstanceFile(filename, &instance1)
	require.NoError(t, err)

	finder := newFinder(tmpDir, "test-service", true)
	ctx := context.Background()

	// First call - should read from disk
	members1, err := finder.members(ctx)
	require.NoError(t, err)
	assert.Len(t, members1, 1)
	assert.Equal(t, "host1:8080", fmt.Sprintf("%s:%d", members1[0].Host, members1[0].Port))

	// Add another instance to disk
	instance2 := instanceInfo{
		ID:     "instance-2",
		Host:   "host2",
		Port:   8081,
		Status: exec.ServiceStatusActive,
		PID:    1235,
	}
	filename = instanceFilePath(tmpDir, "test-service", instance2.ID)
	err = writeInstanceFile(filename, &instance2)
	require.NoError(t, err)

	// Second call immediately - should return cached result
	members2, err := finder.members(ctx)
	require.NoError(t, err)
	assert.Len(t, members2, 1) // Still only 1 member from cache
	assert.Equal(t, "host1:8080", fmt.Sprintf("%s:%d", members2[0].Host, members2[0].Port))

	// Verify cache is being used by checking the same data
	assert.Equal(t, members1[0].ID, members2[0].ID)
	assert.Equal(t, fmt.Sprintf("%s:%d", members1[0].Host, members1[0].Port), fmt.Sprintf("%s:%d", members2[0].Host, members2[0].Port))
}

func TestResolver_Members_CacheExpiration(t *testing.T) {
	tmpDir := t.TempDir()
	serviceDir := filepath.Join(tmpDir, "test-service")
	err := os.MkdirAll(serviceDir, 0755)
	require.NoError(t, err)

	// Create initial instance
	instance1 := instanceInfo{
		ID:     "instance-1",
		Host:   "host1",
		Port:   8080,
		Status: exec.ServiceStatusActive,
		PID:    1234,
	}
	filename := instanceFilePath(tmpDir, "test-service", instance1.ID)
	err = writeInstanceFile(filename, &instance1)
	require.NoError(t, err)

	finder := newFinder(tmpDir, "test-service", true)
	// Set short cache duration for testing
	finder.cacheDuration = 100 * time.Millisecond
	ctx := context.Background()

	// First call - should read from disk
	members1, err := finder.members(ctx)
	require.NoError(t, err)
	assert.Len(t, members1, 1)

	// Add another instance
	instance2 := instanceInfo{
		ID:     "instance-2",
		Host:   "host2",
		Port:   8081,
		Status: exec.ServiceStatusActive,
		PID:    1235,
	}
	filename = instanceFilePath(tmpDir, "test-service", instance2.ID)
	err = writeInstanceFile(filename, &instance2)
	require.NoError(t, err)

	// Poll until cache expires and the new instance is visible
	require.Eventually(t, func() bool {
		members3, err := finder.members(ctx)
		return err == nil && len(members3) == 2
	}, 5*time.Second, 20*time.Millisecond)
}

func TestResolver_Members_NoCacheForEmptyMembers(t *testing.T) {
	tmpDir := t.TempDir()
	finder := newFinder(tmpDir, "test-service", true)
	ctx := context.Background()

	// First call - no instances
	members1, err := finder.members(ctx)
	require.NoError(t, err)
	assert.Empty(t, members1)

	// Create service directory and add instance
	serviceDir := filepath.Join(tmpDir, "test-service")
	err = os.MkdirAll(serviceDir, 0755)
	require.NoError(t, err)

	instance := instanceInfo{
		ID:     "instance-1",
		Host:   "host1",
		Port:   8080,
		Status: exec.ServiceStatusActive,
		PID:    1234,
	}
	filename := instanceFilePath(tmpDir, "test-service", instance.ID)
	err = writeInstanceFile(filename, &instance)
	require.NoError(t, err)

	// Second call immediately - should NOT use cache (since it was empty)
	members2, err := finder.members(ctx)
	require.NoError(t, err)
	assert.Len(t, members2, 1) // Should see the new instance
	assert.Equal(t, "host1:8080", fmt.Sprintf("%s:%d", members2[0].Host, members2[0].Port))
}

func TestResolver_Members_CacheConcurrency(t *testing.T) {
	tmpDir := t.TempDir()
	serviceDir := filepath.Join(tmpDir, "test-service")
	err := os.MkdirAll(serviceDir, 0755)
	require.NoError(t, err)

	// Create instances
	for i := range 5 {
		inst := instanceInfo{
			ID:     fmt.Sprintf("instance-%d", i),
			Host:   fmt.Sprintf("host%d", i),
			Port:   8080 + i,
			PID:    1234 + i,
			Status: exec.ServiceStatusActive,
		}
		filename := instanceFilePath(tmpDir, "test-service", inst.ID)
		err := writeInstanceFile(filename, &inst)
		require.NoError(t, err)
	}

	finder := newFinder(tmpDir, "test-service", true)
	ctx := context.Background()

	// Run concurrent reads
	const numGoroutines = 10
	results := make(chan []exec.HostInfo, numGoroutines)
	errors := make(chan error, numGoroutines)

	for range numGoroutines {
		go func() {
			members, err := finder.members(ctx)
			if err != nil {
				errors <- err
				return
			}
			results <- members
		}()
	}

	// Collect results
	for range numGoroutines {
		select {
		case err := <-errors:
			t.Fatalf("Unexpected error: %v", err)
		case members := <-results:
			assert.Len(t, members, 5)
		}
	}
}

func TestResolver_Members_CacheInvalidation(t *testing.T) {
	tmpDir := t.TempDir()
	serviceDir := filepath.Join(tmpDir, "test-service")
	err := os.MkdirAll(serviceDir, 0755)
	require.NoError(t, err)

	// Create initial instance
	instance1 := instanceInfo{
		ID:     "instance-1",
		Host:   "host1",
		Port:   8080,
		Status: exec.ServiceStatusActive,
		PID:    1234,
	}
	filename := instanceFilePath(tmpDir, "test-service", instance1.ID)
	err = writeInstanceFile(filename, &instance1)
	require.NoError(t, err)

	finder := newFinder(tmpDir, "test-service", true)
	// Use longer cache for this test
	finder.cacheDuration = 5 * time.Second
	ctx := context.Background()

	// First call - populate cache
	members1, err := finder.members(ctx)
	require.NoError(t, err)
	assert.Len(t, members1, 1)

	// Remove the instance file
	err = os.Remove(filename)
	require.NoError(t, err)

	// Second call - should still return cached result
	members2, err := finder.members(ctx)
	require.NoError(t, err)
	assert.Len(t, members2, 1)

	// Manually expire cache
	finder.mu.Lock()
	finder.cacheTime = time.Now().Add(-10 * time.Second)
	finder.mu.Unlock()

	// Third call - cache expired, should see no instances
	members3, err := finder.members(ctx)
	require.NoError(t, err)
	assert.Empty(t, members3)
}

func TestResolver_CleanupOnlyEnabledWhenRequested(t *testing.T) {
	tmpDir := t.TempDir()

	// Create finder without cleanup enabled
	finderNoCleanup := newFinder(tmpDir, "test-service", false)
	assert.Nil(t, finderNoCleanup.cleaner, "cleanup should be disabled")

	// Create finder with cleanup enabled
	finderWithCleanup := newFinder(tmpDir, "test-service", true)
	assert.NotNil(t, finderWithCleanup.cleaner, "cleanup should be enabled")

	// Close both - should not panic
	finderNoCleanup.close()
	finderWithCleanup.close()
}
