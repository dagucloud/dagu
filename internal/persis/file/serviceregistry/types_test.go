// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package serviceregistry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteReadInstanceFile(t *testing.T) {
	tmpDir := t.TempDir()

	original := &instanceInfo{
		ID:     "test-instance",
		Host:   "testhost",
		Port:   8080,
		Status: exec.ServiceStatusActive,
		PID:    1234,
	}

	// Write instance file
	filename := instanceFilePath(tmpDir, "test-service", original.ID)
	err := writeInstanceFile(filename, original)
	require.NoError(t, err)

	// Verify file exists
	assert.FileExists(t, filename)

	// Read instance file
	read, err := readInstanceFile(filename)
	require.NoError(t, err)

	// Compare
	assert.Equal(t, original.ID, read.ID)
	assert.Equal(t, original.Host, read.Host)
	assert.Equal(t, original.PID, read.PID)
}

func TestWriteInstanceFile_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	info := &instanceInfo{
		ID:     "test",
		Host:   "host",
		Port:   8080,
		Status: exec.ServiceStatusActive,
		PID:    1234,
	}

	// Write to non-existent service directory
	filename := instanceFilePath(tmpDir, "new-service", info.ID)
	err := writeInstanceFile(filename, info)
	require.NoError(t, err)

	// Verify directory was created
	serviceDir := filepath.Join(tmpDir, "new-service")
	assert.DirExists(t, serviceDir)
}

func TestWriteInstanceFile_Atomic(t *testing.T) {
	tmpDir := t.TempDir()

	info := &instanceInfo{
		ID:     "atomic-test",
		Host:   "host",
		Port:   8080,
		Status: exec.ServiceStatusActive,
		PID:    1234,
	}

	// Write initial file
	filename := instanceFilePath(tmpDir, "service", info.ID)
	err := writeInstanceFile(filename, info)
	require.NoError(t, err)

	// Update with new data
	info.Host = "newhost"
	info.Port = 9090
	err = writeInstanceFile(filename, info)
	require.NoError(t, err)

	// Read and verify update
	read, err := readInstanceFile(filename)
	require.NoError(t, err)
	assert.Equal(t, "newhost", read.Host)
	assert.Equal(t, 9090, read.Port)
}

func TestReadInstanceFile_Errors(t *testing.T) {
	tmpDir := t.TempDir()

	// Non-existent file
	_, err := readInstanceFile(filepath.Join(tmpDir, "nonexistent.json"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read instance file")

	// Invalid JSON
	invalidFile := filepath.Join(tmpDir, "invalid.json")
	err = os.WriteFile(invalidFile, []byte("not json"), 0644)
	require.NoError(t, err)

	_, err = readInstanceFile(invalidFile)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unmarshal instance info")
}

func TestRemoveInstanceFile(t *testing.T) {
	tmpDir := t.TempDir()

	info := &instanceInfo{
		ID:     "to-remove",
		Host:   "host",
		Port:   8080,
		Status: exec.ServiceStatusActive,
		PID:    1234,
	}

	// Write instance file
	filename := instanceFilePath(tmpDir, "service", info.ID)
	err := writeInstanceFile(filename, info)
	require.NoError(t, err)

	assert.FileExists(t, filename)

	// Remove instance file
	err = removeInstanceFile(filename)
	require.NoError(t, err)
	assert.NoFileExists(t, filename)

	// Remove non-existent file should not error
	nonExistentFile := instanceFilePath(tmpDir, "service", "nonexistent")
	err = removeInstanceFile(nonExistentFile)
	assert.NoError(t, err)
}

func TestInstanceInfo_Serialization(t *testing.T) {
	tmpDir := t.TempDir()

	// Test serialization
	info := &instanceInfo{
		ID:     "test-serialization",
		Host:   "host1",
		Port:   8080,
		Status: exec.ServiceStatusActive,
		PID:    1234,
	}

	filename := instanceFilePath(tmpDir, "service", info.ID)
	err := writeInstanceFile(filename, info)
	require.NoError(t, err)

	read, err := readInstanceFile(filename)
	require.NoError(t, err)
	assert.Equal(t, info.ID, read.ID)
	assert.Equal(t, info.Host, read.Host)
	assert.Equal(t, info.Port, read.Port)
	assert.Equal(t, info.Status, read.Status)
	assert.Equal(t, info.PID, read.PID)
}
