// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetKeepaliveFile(t *testing.T) {
	tests := []struct {
		name        string
		platform    specs.Platform
		wantErr     bool
		errContains string
		fileCheck   func(t *testing.T, path string)
	}{
		{
			name: "LinuxAmd64",
			platform: specs.Platform{
				OS:           "linux",
				Architecture: "amd64",
			},
			fileCheck: func(t *testing.T, path string) {
				assert.Contains(t, path, "keepalive_linux_amd64")
				assert.NotContains(t, path, ".exe")
			},
		},
		{
			name: "LinuxX8664MappedToAmd64",
			platform: specs.Platform{
				OS:           "linux",
				Architecture: "x86_64",
			},
			fileCheck: func(t *testing.T, path string) {
				assert.Contains(t, path, "keepalive_linux_amd64")
			},
		},
		{
			name: "DarwinArm64",
			platform: specs.Platform{
				OS:           "darwin",
				Architecture: "arm64",
			},
			fileCheck: func(t *testing.T, path string) {
				assert.Contains(t, path, "keepalive_darwin_arm64")
			},
		},
		{
			name: "WindowsAmd64",
			platform: specs.Platform{
				OS:           "windows",
				Architecture: "amd64",
			},
			fileCheck: func(t *testing.T, path string) {
				assert.Contains(t, path, "keepalive_windows_amd64.exe")
			},
		},
		{
			name: "LinuxArmWithV7Variant",
			platform: specs.Platform{
				OS:           "linux",
				Architecture: "arm",
				Variant:      "v7",
			},
			fileCheck: func(t *testing.T, path string) {
				assert.Contains(t, path, "keepalive_linux_armv7")
			},
		},
		{
			name: "LinuxArmWithV6Variant",
			platform: specs.Platform{
				OS:           "linux",
				Architecture: "arm",
				Variant:      "v6",
			},
			fileCheck: func(t *testing.T, path string) {
				assert.Contains(t, path, "keepalive_linux_armv6")
			},
		},
		{
			name: "LinuxArmWithoutVariantDefaultsToV7",
			platform: specs.Platform{
				OS:           "linux",
				Architecture: "arm",
			},
			fileCheck: func(t *testing.T, path string) {
				assert.Contains(t, path, "keepalive_linux_armv7")
			},
		},
		{
			name: "LinuxPpc64le",
			platform: specs.Platform{
				OS:           "linux",
				Architecture: "ppc64le",
			},
			fileCheck: func(t *testing.T, path string) {
				assert.Contains(t, path, "keepalive_linux_ppc64le")
			},
		},
		{
			name: "UnsupportedOS",
			platform: specs.Platform{
				OS:           "freebsd",
				Architecture: "amd64",
			},
			wantErr:     true,
			errContains: "unsupported OS: freebsd",
		},
		{
			name: "UnsupportedArchitecture",
			platform: specs.Platform{
				OS:           "linux",
				Architecture: "mips",
			},
			wantErr:     true,
			errContains: "unsupported architecture: mips",
		},
	}

	// Only run tests for platforms where we have binaries
	currentOS := runtime.GOOS
	currentArch := runtime.GOARCH

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip tests for binaries we don't have in the test environment
			if !tt.wantErr && !hasBinaryForPlatform(tt.platform) {
				t.Skipf("Skipping test for %s/%s on %s/%s", tt.platform.OS, tt.platform.Architecture, currentOS, currentArch)
			}

			path, err := GetKeepaliveFile(tt.platform)
			if path != "" {
				t.Cleanup(func() { _ = os.Remove(path) })
			}

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			require.NotEmpty(t, path)

			// Check that file exists
			info, err := os.Stat(path)
			require.NoError(t, err)
			assert.False(t, info.IsDir())

			// Check file permissions
			if runtime.GOOS != "windows" {
				assert.Equal(t, os.FileMode(0755), info.Mode().Perm())
			}

			// Check file is in temp directory
			assert.True(t, strings.HasPrefix(path, os.TempDir()))
			assert.Contains(t, path, "dagu-keepalive")

			// Run custom checks
			if tt.fileCheck != nil {
				tt.fileCheck(t, path)
			}
		})
	}
}

func TestGetKeepaliveFile_Concurrent(t *testing.T) {
	// Test that concurrent calls work correctly
	platform := specs.Platform{
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
	}

	// Skip if we don't have a binary for current platform
	if !hasBinaryForCurrentPlatform() {
		t.Skip("No binary available for current platform")
	}

	done := make(chan bool, 10)
	for range 10 {
		go func() {
			path, err := GetKeepaliveFile(platform)
			assert.NoError(t, err)
			assert.NotEmpty(t, path)

			// Verify file exists
			_, err = os.Stat(path)
			assert.NoError(t, err)
			_ = os.Remove(path)

			done <- true
		}()
	}

	// Wait for all goroutines
	for range 10 {
		<-done
	}
}

func TestGetKeepaliveFile_TempDirCleanup(t *testing.T) {
	platform := specs.Platform{
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
	}

	// Skip if we don't have a binary for current platform
	if !hasBinaryForCurrentPlatform() {
		t.Skip("No binary available for current platform")
	}

	// Get keepalive file
	path, err := GetKeepaliveFile(platform)
	require.NoError(t, err)
	defer func() { _ = os.Remove(path) }()

	// Check temp directory exists
	tmpDir := filepath.Dir(path)
	info, err := os.Stat(tmpDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	// Verify it's in temp directory
	assert.True(t, strings.HasPrefix(path, os.TempDir()))
}

func TestGetKeepaliveFile_OverwriteExisting(t *testing.T) {
	platform := specs.Platform{
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
	}

	// Skip if we don't have a binary for current platform
	if !hasBinaryForCurrentPlatform() {
		t.Skip("No binary available for current platform")
	}

	// Get keepalive file first time
	path1, err := GetKeepaliveFile(platform)
	require.NoError(t, err)
	defer func() { _ = os.Remove(path1) }()

	// Get keepalive file again - should create a new file
	path2, err := GetKeepaliveFile(platform)
	require.NoError(t, err)
	defer func() { _ = os.Remove(path2) }()

	// Should be different paths now since we create unique files
	assert.NotEqual(t, path1, path2)

	// Both files should exist and contain the keepalive binary
	content1, err := os.ReadFile(path1)
	require.NoError(t, err)
	content2, err := os.ReadFile(path2)
	require.NoError(t, err)

	assert.Equal(t, content1, content2)  // Same binary content
	assert.Greater(t, len(content1), 10) // Should be a real binary
}

// Helper function to check if we have a binary for the current platform
func hasBinaryForCurrentPlatform() bool {
	platform := specs.Platform{
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
	}
	return hasBinaryForPlatform(platform)
}

// Helper function to check if we expect to have a binary for a given platform
func hasBinaryForPlatform(targetPlatform specs.Platform) bool {
	// In CI or when all binaries are built, all platforms should be available
	// For local development, we might only have binaries for the current platform

	// Check if the target platform is in our supported list
	_, osSupported := osMapping[targetPlatform.OS]
	_, archSupported := archMapping[targetPlatform.Architecture]

	return osSupported && archSupported
}
