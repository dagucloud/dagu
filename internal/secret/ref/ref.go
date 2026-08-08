// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package ref defines references to external secrets.
package ref

// Ref identifies a secret that is resolved at DAG execution time.
type Ref struct {
	// Name is the environment variable name to set.
	Name string `json:"name"`
	// Ref is the workspace-local registry reference for a managed secret.
	Ref string `json:"ref,omitempty"`
	// Provider identifies the secret backend.
	Provider string `json:"provider,omitempty"`
	// Key is the provider-specific identifier for a direct provider reference.
	Key string `json:"key,omitempty"`
	// Options contains provider-specific configuration.
	Options map[string]string `json:"options,omitempty"`
}
