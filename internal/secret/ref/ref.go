// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package ref defines references to external secrets.
package ref

import "strings"

// ReservedPrefix starts every registry ref that Dagu reserves for the secrets
// of its AI features. No DAG, runtime profile or read by ref resolves such a
// ref.
const ReservedPrefix = "ai/"

// ProviderConnection is the provider that exports a connection's access
// token. Its key is the connection's name.
const ProviderConnection = "connection"

// IsReserved reports whether a registry ref starts with ReservedPrefix.
func IsReserved(ref string) bool {
	return strings.HasPrefix(ref, ReservedPrefix)
}

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
