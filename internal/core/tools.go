// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

const (
	// DefaultAquaStandardRegistryRef is the aqua standard registry commit Dagu
	// uses when a DAG does not pin tools.registry.ref.
	// aqua-registry v4.547.0
	DefaultAquaStandardRegistryRef = "080d723b75cd0ea7c2b2059bf6266d3ab39aa792"

	// ToolRegistryPolicyPinned restricts tool resolution to the pinned standard
	// registry snapshot; no newer registry release is tried on failure.
	ToolRegistryPolicyPinned = "pinned"

	// ToolRegistryPolicyFallback names the default resolution behavior: when
	// the pinned standard registry snapshot cannot resolve the declared
	// packages, resolution is retried once against the latest registry release.
	ToolRegistryPolicyFallback = "fallback"
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
	// Policy selects how the standard registry ref is resolved when the DAG
	// does not pin one: ToolRegistryPolicyFallback (default) or
	// ToolRegistryPolicyPinned.
	Policy string `json:"policy,omitempty"`
}

// ToolPackage declares one aqua package and optional command names Dagu should expose.
type ToolPackage struct {
	Name     string   `json:"name,omitempty"`
	Package  string   `json:"package"`
	Version  string   `json:"version"`
	Commands []string `json:"commands,omitempty"`
	Registry string   `json:"registry,omitempty"`
}
