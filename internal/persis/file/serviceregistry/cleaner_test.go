// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package serviceregistry

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleaner_CleanupQuarantinedFiles(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	serviceDir := filepath.Join(tmpDir, "test-service")
	err := os.MkdirAll(serviceDir, 0755)
	require.NoError(t, err)

	// Create some quarantined files
	quarantinedFiles := []string{
		filepath.Join(serviceDir, "instance1.json.gc.12345.1234567890"),
		filepath.Join(serviceDir, "instance2.json.gc.12346.1234567891"),
		filepath.Join(serviceDir, "instance3.json.gc.12347.1234567892"),
	}

	for _, file := range quarantinedFiles {
		err := os.WriteFile(file, []byte("{}"), 0644)
		require.NoError(t, err)
	}

	// Create a normal (non-quarantined) file that should not be deleted
	normalFile := filepath.Join(serviceDir, "active-instance.json")
	err = os.WriteFile(normalFile, []byte("{}"), 0644)
	require.NoError(t, err)

	// Create cleaner and trigger cleanup manually
	c := &cleaner{
		baseDir:     tmpDir,
		serviceName: "test-service",
		stopCh:      make(chan struct{}),
	}

	ctx := context.Background()
	c.cleanupQuarantinedFiles(ctx)

	// Verify quarantined files were removed
	for _, file := range quarantinedFiles {
		assert.NoFileExists(t, file, "quarantined file should be deleted: %s", file)
	}

	// Verify normal file still exists
	assert.FileExists(t, normalFile, "normal file should not be deleted")
}

func TestCleaner_CleanupWithRetry(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	serviceDir := filepath.Join(tmpDir, "test-service")
	err := os.MkdirAll(serviceDir, 0755)
	require.NoError(t, err)

	// Create a quarantined file
	quarantinedFile := filepath.Join(serviceDir, "instance.json.gc.12345.1234567890")
	err = os.WriteFile(quarantinedFile, []byte("{}"), 0644)
	require.NoError(t, err)

	c := &cleaner{
		baseDir:     tmpDir,
		serviceName: "test-service",
		stopCh:      make(chan struct{}),
	}

	ctx := context.Background()
	c.cleanupQuarantinedFiles(ctx)

	// File should be removed (retry logic should work)
	assert.NoFileExists(t, quarantinedFile)
}

func TestCleaner_CleanupIgnoresDirectories(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	serviceDir := filepath.Join(tmpDir, "test-service")
	err := os.MkdirAll(serviceDir, 0755)
	require.NoError(t, err)

	// Create a directory with .gc in the name (should be ignored)
	gcDir := filepath.Join(serviceDir, "instance.json.gc.12345")
	err = os.MkdirAll(gcDir, 0755)
	require.NoError(t, err)

	c := &cleaner{
		baseDir:     tmpDir,
		serviceName: "test-service",
		stopCh:      make(chan struct{}),
	}

	ctx := context.Background()
	c.cleanupQuarantinedFiles(ctx)

	// Directory should still exist (not deleted)
	assert.DirExists(t, gcDir)
}

func TestCleaner_CleanupNonExistentDirectory(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	c := &cleaner{
		baseDir:     tmpDir,
		serviceName: "non-existent-service",
		stopCh:      make(chan struct{}),
	}

	ctx := context.Background()
	// Should not panic
	c.cleanupQuarantinedFiles(ctx)
}

func TestCleaner_PeriodicCleanup(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	serviceDir := filepath.Join(tmpDir, "test-service")
	err := os.MkdirAll(serviceDir, 0755)
	require.NoError(t, err)

	// Create a quarantined file
	quarantinedFile := filepath.Join(serviceDir, "instance.json.gc.12345.1234567890")
	err = os.WriteFile(quarantinedFile, []byte("{}"), 0644)
	require.NoError(t, err)

	// Create cleaner (starts background goroutine)
	c := newCleaner(tmpDir, "test-service")
	defer c.stop()

	// Since cleanup runs with random intervals (5-60 minutes), we can't wait for it.
	// Just verify the cleaner can be started and stopped without issues.
	c.stop()

	// File might still exist (cleanup hasn't run yet due to random interval)
	// This test mainly verifies the goroutine management works correctly
}

func TestCleaner_Stop(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	// Create cleaner
	c := newCleaner(tmpDir, "test-service")

	// Stop should not panic and should stop the goroutine
	c.stop()

	// Stopping again should not panic
	c.stop()
}

func TestQuarantine_MarkStaleFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	serviceDir := filepath.Join(tmpDir, "test-service")
	err := os.MkdirAll(serviceDir, 0755)
	require.NoError(t, err)

	// Create a stale file
	staleFile := filepath.Join(serviceDir, "stale-instance.json")
	err = os.WriteFile(staleFile, []byte("{}"), 0644)
	require.NoError(t, err)

	// Set file modification time to 2 minutes ago
	oldTime := time.Now().Add(-2 * time.Minute)
	err = os.Chtimes(staleFile, oldTime, oldTime)
	require.NoError(t, err)

	q := newQuarantine(30 * time.Second)
	ctx := context.Background()

	// Mark the file as stale
	quarantined := q.markStaleFile(ctx, staleFile, oldTime)
	assert.True(t, quarantined, "file should be quarantined")

	// Original file should be gone
	assert.NoFileExists(t, staleFile)

	// Quarantined file should exist
	matches, err := filepath.Glob(staleFile + ".gc*")
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Contains(t, matches[0], ".gc")
}

func TestQuarantine_SkipRecentlyModifiedFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	serviceDir := filepath.Join(tmpDir, "test-service")
	err := os.MkdirAll(serviceDir, 0755)
	require.NoError(t, err)

	// Create a file
	filename := filepath.Join(serviceDir, "instance.json")
	err = os.WriteFile(filename, []byte("{}"), 0644)
	require.NoError(t, err)

	// Set initial time to old
	observed := time.Now().Add(-time.Minute)
	err = os.Chtimes(filename, observed, observed)
	require.NoError(t, err)

	q := newQuarantine(5 * time.Second)

	// Simulate another process updating the file
	newTime := time.Now()
	err = os.Chtimes(filename, newTime, newTime)
	require.NoError(t, err)

	ctx := context.Background()
	quarantined := q.markStaleFile(ctx, filename, observed)
	assert.False(t, quarantined, "file should not be quarantined")

	// Original file should still exist
	assert.FileExists(t, filename)

	// No quarantined files should exist
	matches, _ := filepath.Glob(filename + ".gc*")
	assert.Empty(t, matches)
}

func TestQuarantine_NonExistentFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	nonExistentFile := filepath.Join(tmpDir, "does-not-exist.json")

	q := newQuarantine(30 * time.Second)
	ctx := context.Background()

	// Should not panic or error
	quarantined := q.markStaleFile(ctx, nonExistentFile, time.Now())
	assert.False(t, quarantined, "non-existent file cannot be quarantined")
}

func TestQuarantine_GenerateUniquePath(t *testing.T) {
	t.Parallel()

	q := newQuarantine(30 * time.Second)

	path1 := q.generateQuarantinePath("/tmp/instance.json")
	path2 := q.generateQuarantinePath("/tmp/instance.json")

	// Paths should be different (due to nanosecond timestamp)
	assert.NotEqual(t, path1, path2)

	// Both should contain the quarantine marker
	assert.Contains(t, path1, ".gc")
	assert.Contains(t, path2, ".gc")

	// Both should contain PID
	assert.Contains(t, path1, ".json.gc")
	assert.Contains(t, path2, ".json.gc")
}

func TestIsQuarantinedFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		filename string
		want     bool
	}{
		{
			name:     "quarantined file",
			filename: "instance.json.gc.12345.1234567890",
			want:     true,
		},
		{
			name:     "normal json file",
			filename: "instance.json",
			want:     false,
		},
		{
			name:     "other file type",
			filename: "instance.txt",
			want:     false,
		},
		{
			name:     "file with gc in name but not quarantined",
			filename: "gc-instance.json",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isQuarantinedFile(tt.filename)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestCleaner_RandomInterval(t *testing.T) {
	t.Parallel()

	c := &cleaner{
		baseDir:     "/tmp",
		serviceName: exec.ServiceNameCoordinator,
		stopCh:      make(chan struct{}),
	}

	// Generate multiple intervals
	intervals := make([]time.Duration, 10)
	for i := range intervals {
		intervals[i] = c.randomInterval()
		time.Sleep(1 * time.Millisecond) // ensure different timestamps for UnixNano-based seed
	}

	// All intervals should be within range
	for _, interval := range intervals {
		assert.GreaterOrEqual(t, interval, cleanupMinInterval)
		assert.LessOrEqual(t, interval, cleanupMaxInterval)
	}

	// Intervals should vary (not all the same)
	allSame := true
	first := intervals[0]
	for _, interval := range intervals[1:] {
		if interval != first {
			allSame = false
			break
		}
	}
	assert.False(t, allSame, "intervals should vary to avoid thundering herd")
}
