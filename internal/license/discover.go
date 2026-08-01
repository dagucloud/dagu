// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package license

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// DiscoverySource indicates where the license was found.
type DiscoverySource int

const (
	SourceNone           DiscoverySource = iota
	SourceEnvInline                      // DAGU_LICENSE env (inline JWT)
	SourceEnvKey                         // DAGU_LICENSE_KEY env (needs activation)
	SourceConfigKey                      // config file license.key (needs activation)
	SourceActivationFile                 // activation.json (has token)
	SourceFileJWT                        // file-based JWT (DAGU_LICENSE_FILE or default path)
)

// String returns a human-readable name for the discovery source.
func (s DiscoverySource) String() string {
	switch s {
	case SourceNone:
		return "none"
	case SourceEnvInline:
		return "environment"
	case SourceEnvKey:
		return "environment-key"
	case SourceConfigKey:
		return "config-key"
	case SourceActivationFile:
		return "activation-file"
	case SourceFileJWT:
		return "file"
	default:
		return "unknown"
	}
}

// IsEnv returns true if the license was discovered from an environment variable.
func (s DiscoverySource) IsEnv() bool {
	return s == SourceEnvInline || s == SourceEnvKey
}

// NeedsHeartbeat returns true if this source requires periodic heartbeats.
func (s DiscoverySource) NeedsHeartbeat() bool {
	switch s {
	case SourceEnvKey, SourceConfigKey, SourceActivationFile:
		return true
	case SourceNone, SourceEnvInline, SourceFileJWT:
		return false
	}
	return false
}

// DiscoveryResult holds the outcome of license discovery.
type DiscoveryResult struct {
	Source     DiscoverySource
	Token      string
	LicenseKey string
	Activation *ActivationData
}

// Discover searches for a license using the following precedence:
//  1. DAGU_LICENSE env var (inline JWT)
//  2. DAGU_LICENSE_KEY env var (needs activation)
//  3. configKey parameter (needs activation)
//  4. activation.json via store (has token)
//  5. DAGU_LICENSE_FILE env var or licenseDir/license.jwt (offline JWT)
func Discover(licenseDir, configKey string, store ActivationStore) (*DiscoveryResult, error) {
	// 1. Inline JWT from env
	if token := os.Getenv("DAGU_LICENSE"); token != "" {
		return &DiscoveryResult{
			Source: SourceEnvInline,
			Token:  token,
		}, nil
	}

	// 2. License key from env (needs activation)
	if key := os.Getenv("DAGU_LICENSE_KEY"); key != "" {
		return &DiscoveryResult{
			Source:     SourceEnvKey,
			LicenseKey: key,
		}, nil
	}

	// 3. License key from config (needs activation)
	if configKey != "" {
		return &DiscoveryResult{
			Source:     SourceConfigKey,
			LicenseKey: configKey,
		}, nil
	}

	// 4. Persisted activation data
	if store != nil {
		ad, err := store.Load()
		if err != nil {
			return nil, err
		}
		if ad != nil && ad.Token != "" {
			return &DiscoveryResult{
				Source:     SourceActivationFile,
				Token:      ad.Token,
				Activation: ad,
			}, nil
		}
	}

	// 5. Offline JWT file
	filePath := os.Getenv("DAGU_LICENSE_FILE")
	explicitFile := filePath != ""
	if filePath == "" && licenseDir != "" {
		filePath = filepath.Join(licenseDir, "license.jwt")
	}
	if filePath != "" {
		data, err := os.ReadFile(filePath) //nolint:gosec // path from env or config
		if err != nil {
			if explicitFile || !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("failed to read license file %s: %w", filePath, err)
			}
			// File does not exist — fall through to next source.
		} else {
			if explicitFile && len(bytes.TrimSpace(data)) == 0 {
				return nil, fmt.Errorf("license file %s is empty", filePath)
			}
			token := string(data)
			if token != "" {
				return &DiscoveryResult{
					Source: SourceFileJWT,
					Token:  token,
				}, nil
			}
		}
	}

	return &DiscoveryResult{Source: SourceNone}, nil
}
