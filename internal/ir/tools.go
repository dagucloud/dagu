// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"fmt"
	"strings"
)

const (
	// DefaultAquaStandardRegistryRef is the last-resort aqua standard registry
	// commit, used only when the latest registry release cannot be resolved
	// and no previously resolved ref is cached on disk.
	DefaultAquaStandardRegistryRef = "080d723b75cd0ea7c2b2059bf6266d3ab39aa792"
)

// ToolConfig declares external CLI tools required by a DAG run.
type ToolConfig struct {
	Provider string        `json:"provider,omitempty"`
	Registry *ToolRegistry `json:"registry,omitempty"`
	Packages []ToolPackage `json:"packages,omitempty"`
}

// ToolRegistry identifies the aqua registry used to resolve tool packages.
type ToolRegistry struct {
	Name      string `json:"name,omitempty"`
	Type      string `json:"type,omitempty"`
	RepoOwner string `json:"repoOwner,omitempty"`
	RepoName  string `json:"repoName,omitempty"`
	Ref       string `json:"ref,omitempty"`
	Path      string `json:"path,omitempty"`
}

// ValidateWorkerAbsPath accepts POSIX, Windows drive, and UNC absolute paths.
// The DAG parser's host OS may differ from the worker's, so filepath.IsAbs cannot be used.
func ValidateWorkerAbsPath(path string) error {
	p := strings.TrimSpace(path)
	if p == "" {
		return fmt.Errorf("path is required")
	}
	if strings.HasPrefix(p, "/") {
		return nil
	}
	if len(p) >= 3 && p[1] == ':' && (p[2] == '/' || p[2] == '\\') {
		if drive := p[0]; (drive >= 'A' && drive <= 'Z') || (drive >= 'a' && drive <= 'z') {
			return nil
		}
	}
	if strings.HasPrefix(p, `\\`) {
		server, rest, found := strings.Cut(p[2:], `\`)
		share, _, _ := strings.Cut(rest, `\`)
		// Both components must be nonempty, including the share in \\server\\share.
		if found && server != "" && share != "" {
			return nil
		}
	}
	return fmt.Errorf("path must be an absolute path, got %q", p)
}

// ToolPackage declares one aqua package and optional command names Dagu should expose.
type ToolPackage struct {
	Name     string   `json:"name,omitempty"`
	Package  string   `json:"package"`
	Version  string   `json:"version"`
	Commands []string `json:"commands,omitempty"`
	Registry string   `json:"registry,omitempty"`
	// Digest pins the sha256 of the downloaded artifact for the platform the
	// DAG runs on, in "sha256:<64 hex>" form. The install fails when the
	// artifact hash recorded for this package does not match.
	Digest string `json:"digest,omitempty"`
}
