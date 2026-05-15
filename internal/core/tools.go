// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

const (
	// DefaultAquaStandardRegistryRef is the aqua standard registry commit Dagu
	// uses when a DAG does not specify tools.registry.
	DefaultAquaStandardRegistryRef = "5e2f56743d66abe9dfc7c56d35086511b7dc92d8"
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

// ToolPackage declares one aqua package and optional command names Dagu should expose.
type ToolPackage struct {
	Name     string   `json:"name,omitempty"`
	Package  string   `json:"package"`
	Version  string   `json:"version"`
	Commands []string `json:"commands,omitempty"`
	Registry string   `json:"registry,omitempty"`
}
