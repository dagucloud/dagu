// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"embed"
	"fmt"
	"io/fs"
	"os"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

//go:embed assets/*
var assetsFS embed.FS

var osMapping = map[string]string{
	"linux":  "linux",
	"darwin": "darwin",
}

var archMapping = map[string]string{
	"amd64":       "amd64",
	"x86_64":      "amd64",
	"arm64":       "arm64",
	"aarch64":     "arm64",
	"386":         "386",
	"x86":         "386",
	"i386":        "386",
	"arm":         "armv7", // default to v7 if no variant specified
	"armv7":       "armv7",
	"armv7l":      "armv7",
	"armv6":       "armv6",
	"armv6l":      "armv6",
	"ppc64le":     "ppc64le",
	"powerpc64le": "ppc64le",
	"s390x":       "s390x",
}

// GetKeepaliveFile copies the embedded keepalive binary to a temp file and returns its path
func GetKeepaliveFile(platform specs.Platform) (string, error) {
	// Map OS
	osName, ok := osMapping[platform.OS]
	if !ok {
		return "", fmt.Errorf("unsupported OS: %s", platform.OS)
	}

	// Map architecture
	arch, ok := archMapping[platform.Architecture]
	if !ok {
		return "", fmt.Errorf("unsupported architecture: %s", platform.Architecture)
	}

	// Handle ARM variants
	if platform.Architecture == "arm" && platform.Variant != "" {
		switch platform.Variant {
		case "v6", "6":
			arch = "armv6"
		case "v7", "7":
			arch = "armv7"
		}
	}

	// Construct filename
	filename := fmt.Sprintf("keepalive_%s_%s", osName, arch)
	if osName == "windows" {
		filename += ".exe"
	}

	// Read the embedded file
	data, err := assetsFS.ReadFile(fmt.Sprintf("assets/%s", filename))
	if err != nil {
		if _, ok := err.(*fs.PathError); ok {
			return "", fmt.Errorf("keepalive binary not found for %s/%s", platform.OS, platform.Architecture)
		}
		return "", fmt.Errorf("failed to read keepalive binary: %w", err)
	}

	// Create a unique temporary file for each keepalive binary
	tmpFile, err := os.CreateTemp("", fmt.Sprintf("dagu-keepalive-%s-*", filename))
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Write the binary data and close the file
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = fileutil.Remove(tmpPath)
		return "", fmt.Errorf("failed to write keepalive binary: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = fileutil.Remove(tmpPath)
		return "", fmt.Errorf("failed to close temp file: %w", err)
	}

	// Set executable permissions
	// #nosec G302 - Binary needs to be executable by the user
	if err := os.Chmod(tmpPath, 0755); err != nil {
		_ = fileutil.Remove(tmpPath)
		return "", fmt.Errorf("failed to set executable permissions: %w", err)
	}

	return tmpPath, nil
}
